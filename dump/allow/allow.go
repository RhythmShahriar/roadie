// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package allow is the statement allowlist of PRD 10.5: a dump is code,
// so every statement that isn't row data is judged before it can reach
// an output file or a database. A statement is kept, dropped with a plan
// line or refused with dump-refused; anything not on the list is refused.
//
// The rules read what the server runs: MySQL versioned comments are
// judged by their body, so /*!50000 GRANT ... */ is a GRANT; keywords
// match in any case. Messages name the line and the kind of statement,
// never the data in it.
package allow

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/dump/internal/size"
	"github.com/RhythmShahriar/roadie/dump/mysql"
)

// Verdict is what happens to a statement.
type Verdict uint8

// The verdicts, from the mildest.
const (
	Keep   Verdict = iota // copied to the output (Text when set, else as read)
	Drop                  // left out, with a plan line
	Refuse                // the run stops with dump-refused
)

func (v Verdict) String() string {
	switch v {
	case Keep:
		return "keep"
	case Drop:
		return "drop"
	}
	return "refuse"
}

// Options configures Check.
type Options struct {
	Name         string       // the file name, for messages; "" reads "the input"
	Dialect      core.Dialect // MySQL, MariaDB or "" (read as MySQL), or Postgres
	Mode         mysql.Mode   // the MySQL sql_mode quoting in force at the statement
	KeepRoutines bool         // --keep-routines: keep triggers, procedures, functions and views
	// Database is the database the dump's head names ("-- Host: x
	// Database: shop"); USE of it is dropped, USE of any other refused.
	Database string
	// Prepared holds the names of prepared statements Roadie dropped
	// (Percona Server's rocksdb_bulk_load probe), so their EXECUTE and
	// DEALLOCATE are dropped too. The Filter keeps it.
	Prepared map[string]bool
	// InTransaction is set while a PostgreSQL dump has a transaction
	// block open (BEGIN without its COMMIT yet). The Filter keeps it.
	InTransaction bool
	// Charsets holds the MySQL user variables the dump has set, true for
	// one that holds a client character set Roadie has judged (saved
	// from @@character_set_client or set to a plain name), so that SET
	// character_set_client = @saved_cs_client can be kept. CharsetsLost
	// is set once a SET assigned a variable Roadie couldn't name. The
	// Filter keeps both.
	Charsets     map[string]bool
	CharsetsLost bool
}

// Decision is the verdict on one statement.
type Decision struct {
	Verdict Verdict
	// Kind names the statement for a plan line or a refusal, such as
	// "GRANT" or "CREATE TRIGGER". It is built from keywords, never data.
	Kind string
	// Note is the plan line for a dropped statement (without its line
	// number, so the Filter can count repeats) and for a kept routine.
	Note string
	// Text is the statement to write instead of the one read, set when a
	// kept routine had its DEFINER replaced.
	Text []byte
	// Routine is set for a trigger, procedure, function or view kept
	// with KeepRoutines.
	Routine bool

	// What the Filter tracks: the objects a DROP removes, the object a
	// CREATE makes and a prepared statement Roadie dropped.
	drops    []object
	creates  *object
	prepared string
	txn      int8 // +1 opens a PostgreSQL transaction block, -1 ends it
	// vars are the user variables a kept SET assigns, each with whether
	// it then holds a judged character set; lostVars is set when the SET
	// assigns one Roadie can't name.
	vars     map[string]bool
	lostVars bool
}

// object is a table, view, sequence or other named thing of a dump.
type object struct {
	kind, name string
}

// Check judges one passthrough statement. A refused statement gives a
// Decision with Verdict Refuse and the *core.Error to stop the run with
// (code dump-refused, exit 3, Line set); otherwise the error is nil.
func Check(st core.Statement, o Options) (Decision, error) {
	var d Decision
	if o.Dialect == core.Postgres {
		d = checkPG(st, o)
	} else {
		d = checkMySQL(st, o)
	}
	if d.Verdict == Refuse {
		return d, refusal(st, o, d)
	}
	return d, nil
}

// Message parts of a refusal: the kind of statement, why it is a danger
// and what to do. The why and the advice come from the kind, so a
// refusal never needs the statement's text.
type why struct{ danger, advice string }

const (
	adviceOneDB    = "Export one database (without --databases or --all-databases) and pass the target with --to."
	adviceLeaveOut = "Leave it out of the export; rhythm-roadie explain dump-refused lists what Roadie keeps."
	adviceNever    = "Leave it out of the export; Roadie never keeps it, even with --keep-routines."
	adviceClient   = "Export the dump again with mysqldump, mariadb-dump or pg_dump -Fp; Roadie never copies client commands."
)

var (
	whyRunsCode   = why{"could run code on the staging database", adviceLeaveOut}
	whyClient     = why{"would run on the machine that imports the dump", adviceClient}
	whyOtherDB    = why{"would send the import to a database --to doesn't name", adviceOneDB}
	whyDatabase   = why{"would make or remove a database --to doesn't name", adviceOneDB}
	whyReadsFiles = why{"could read files on the staging server or the machine that imports the dump", "Export the table as INSERT statements, as mysqldump does by default."}
	whyWrites     = why{"could write files on the staging server", adviceLeaveOut}
	whyServerFile = why{"could read or write files on the staging server", "Export with pg_dump -Fp, which writes COPY ... FROM stdin."}
	whyProgram    = why{"would run a command on the staging server", "Export with pg_dump -Fp, which writes COPY ... FROM stdin."}
	whyCommands   = why{"could run commands on the staging server", adviceNever}
	whyFileCall   = why{"could read files or reach another server from the staging database", adviceNever}
	whyConnects   = why{"could connect the staging database to another server", "Leave that table or object out of the export (mysqldump --ignore-table)."}
	whyGlobal     = why{"would change the settings of the whole staging server", adviceLeaveOut}
	whySetting    = why{"isn't a session setting dump tools write", adviceLeaveOut}
	whyQuery      = why{"fills or sets something from a query instead of the dump's own rows", adviceLeaveOut}
	whyUnknown    = why{"isn't on the list of statements Roadie copies from a dump", adviceLeaveOut}
	whyHiddenSQL  = why{"could run a query hidden in a string on the staging database", adviceNever}
	whyServer     = why{"could stop or reconfigure the staging server", adviceNever}
	whyStalls     = why{"could stall the import or hold locks on the staging server", adviceNever}
	whyDropOther  = why{"removes something the dump doesn't create", adviceLeaveOut}
	whyDropBare   = why{"isn't the DROP TABLE IF EXISTS dump tools write before a CREATE TABLE", "Export the dump again with mysqldump --add-drop-table or mariadb-dump, which write DROP TABLE IF EXISTS."}
	whyDropFilled = why{"removes a table the dump has already created and filled", adviceLeaveOut}
	whyCascade    = why{"could remove objects the dump doesn't create", "Export without --clean, or with pg_dump --clean --if-exists, which never cascades."}
)

// refuse is a refused Decision; w carries its wording.
func refuse(kind string, w why) Decision {
	return Decision{Verdict: Refuse, Kind: kind, Note: w.danger + "\x00" + w.advice}
}

// drop is a dropped Decision with its plan reason. The dump itself is
// never edited: the statement is only left out of the copy.
func drop(kind, reason string) Decision {
	return Decision{Verdict: Drop, Kind: kind, Note: kind + " left out of the copy: " + reason}
}

func keep() Decision { return Decision{Verdict: Keep} }

// refusal words a refused Decision as PRD 17's dump-refused message.
func refusal(st core.Statement, o Options, d Decision) *core.Error {
	name := o.Name
	if name == "" {
		name = "the input"
	}
	danger, advice, _ := strings.Cut(d.Note, "\x00")
	label := d.Kind
	if !strings.HasPrefix(label, "a ") && !strings.HasPrefix(label, "an ") {
		label = article(label) + " " + label + " statement"
	}
	where := name + " line " + size.Count(st.Line)
	if st.Line == 0 {
		where = name // a statement Roadie built, such as a source table's DDL
	}
	msg := fmt.Sprintf("dump-refused: %s holds %s, which %s.", where, label, danger)
	if advice != "" {
		msg += " " + advice
	}
	e := core.Errorf(core.ErrDumpRefused, 3, "%s", msg)
	e.Line = st.Line
	return e
}

// article is "a" or "an" for a keyword label, read as it is spoken:
// "an XA", "a USE".
func article(label string) string {
	first, _, _ := strings.Cut(label, " ")
	switch first {
	case "XA":
		return "an"
	case "USE", "USER", "UNION", "UNIQUE":
		return "a"
	}
	if label != "" && strings.ContainsRune("AEIOU", rune(label[0])) {
		return "an"
	}
	return "a"
}

// label upper-cases up to n leading keywords of a statement for a
// message, such as "CREATE SERVER". Only plain keywords of letters and
// underscores are used, so no data can reach the text.
func label(t toks, n int) string {
	var parts []string
	for i := 0; i < len(t) && len(parts) < n; i++ {
		w := t.w(i)
		if w == "" || len(w) > 32 || strings.IndexFunc(w, func(r rune) bool { return (r < 'a' || r > 'z') && r != '_' }) >= 0 {
			break
		}
		parts = append(parts, strings.ToUpper(w))
	}
	if len(parts) == 0 {
		return "an unknown statement"
	}
	return strings.Join(parts, " ")
}

// twoWords are the statements whose second keyword names what they act
// on (CREATE SERVER, LOAD DATA); for any other the second word may be a
// name, so only the first is used.
var twoWords = map[string]bool{
	"create": true, "drop": true, "alter": true, "load": true, "install": true, "uninstall": true,
	"rename": true, "lock": true, "unlock": true, "start": true, "begin": true, "refresh": true,
}

// unknownLabel names a statement that isn't on the list.
func unknownLabel(t toks) string {
	if twoWords[t.w(0)] {
		return label(t, 2)
	}
	return label(t, 1)
}

// safeName returns an identifier for a plan line or message when it is
// a plain one (letters, digits, _ and $, at most 64 bytes); "" otherwise.
func safeName(s string) string {
	if s == "" || len(s) > 64 {
		return ""
	}
	for _, c := range []byte(s) {
		if !isWordByte(c) || c >= 0x80 {
			return ""
		}
	}
	return s
}

// globalChecks refuse what no statement may hold, whatever its kind and
// even in a kept routine: writing files, reading files and running
// commands through server functions.
func globalChecks(t toks) (Decision, bool) {
	switch {
	case t.seq("into", "outfile"):
		return refuse("a SELECT ... INTO OUTFILE statement", whyWrites), true
	case t.seq("into", "dumpfile"):
		return refuse("a SELECT ... INTO DUMPFILE statement", whyWrites), true
	}
	for _, fn := range []string{"sys_exec", "sys_eval"} {
		if t.call(fn) {
			return refuse("a call to "+fn+"()", whyCommands), true
		}
	}
	for _, fn := range []string{"load_file", "pg_read_file", "pg_read_binary_file", "pg_ls_dir", "pg_stat_file",
		"lo_import", "lo_export", "dblink", "dblink_exec", "dblink_connect", "dblink_send_query"} {
		if t.call(fn) {
			return refuse("a call to "+fn+"()", whyFileCall), true
		}
	}
	// These run a query given as a string, which no rule here reads.
	for _, fn := range []string{"query_to_xml", "query_to_xml_and_xmlschema", "query_to_xmlschema",
		"cursor_to_xml", "cursor_to_xmlschema", "table_to_xml", "table_to_xml_and_xmlschema",
		"table_to_xmlschema", "schema_to_xml", "schema_to_xml_and_xmlschema", "schema_to_xmlschema",
		"database_to_xml", "database_to_xml_and_xmlschema", "database_to_xmlschema"} {
		if t.call(fn) {
			return refuse("a call to "+fn+"()", whyHiddenSQL), true
		}
	}
	// A CHECK constraint or column default runs these while rows import.
	for _, fn := range []string{"pg_terminate_backend", "pg_cancel_backend", "pg_reload_conf",
		"pg_rotate_logfile", "pg_promote", "pg_switch_wal", "pg_create_restore_point",
		"pg_log_backend_memory_contexts"} {
		if t.call(fn) {
			return refuse("a call to "+fn+"()", whyServer), true
		}
	}
	// A CHECK constraint, index expression or column default runs these
	// for every row: they hold the import, or locks, for as long as they
	// say.
	for _, fn := range []string{"pg_sleep", "pg_sleep_for", "pg_sleep_until", "pg_advisory_lock",
		"pg_advisory_lock_shared", "pg_advisory_xact_lock", "pg_advisory_xact_lock_shared",
		"pg_try_advisory_lock", "pg_try_advisory_lock_shared", "pg_try_advisory_xact_lock",
		"pg_try_advisory_xact_lock_shared", "pg_notify"} {
		if t.call(fn) {
			return refuse("a call to "+fn+"()", whyStalls), true
		}
	}
	return Decision{}, false
}

// hasBackslash reports whether a backslash stands outside strings and
// identifiers, which in a dump means a client command (\! or \. in the
// mysql client, any meta-command in psql).
func hasBackslash(t toks) bool {
	for i := range t {
		if t.p(i, '\\') {
			return true
		}
	}
	return false
}

// trimmed is the statement without leading blanks.
func trimmed(b []byte) []byte { return bytes.TrimLeft(b, " \t\r\n\f\v") }
