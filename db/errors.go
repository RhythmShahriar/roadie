// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/RhythmShahriar/roadie/core"
)

// NoTableError is a table a source was asked for that the database
// doesn't have. A caller that only looks (a kill-switch check) reads it
// as "not there"; one that needs the table stops with the db code.
type NoTableError struct {
	DB, Table string
}

func (e *NoTableError) Error() string {
	return fmt.Sprintf("db: %s has no table %s", e.DB, e.Table)
}

// skipKey marks a context whose sources leave out the named tables the
// database doesn't have.
type skipKey struct{}

// SkipMissing returns a context under which Source leaves out a named
// table the database doesn't have instead of failing: prepare --only
// side-effects asks for the kill switch's tables, which a site without
// WooCommerce webhooks or AutomateWoo lacks.
func SkipMissing(ctx context.Context) context.Context {
	return context.WithValue(ctx, skipKey{}, true)
}

func skipMissing(ctx context.Context) bool {
	v, _ := ctx.Value(skipKey{}).(bool)
	return v
}

// importState is what a sink knows when an import stops: how far it got
// and the words that name the target.
type importState struct {
	engine    core.Dialect
	server    string // "10.6.12"
	target    string // "staging@localhost"
	committed int    // tables replaced and committed
	total     int    // tables in the stream; 0 = unknown
	changed   bool   // the target changed (a committed table or a MySQL DDL)
	current   string // the table being imported, "" between tables
	fix       bool   // --fix-collation was given
}

// collationError is a collation the target server doesn't know, found by
// the sink before it ran the CREATE TABLE.
type collationError struct{ name string }

func (e *collationError) Error() string { return "unknown collation " + e.name }

// importError words err as the PRD 17 import or import-mixed error. A
// canceled context is returned as it is. Driver messages are kept only
// with every quoted part masked, since a duplicate-key or bad-value
// message can quote a row's data.
func importError(err error, st importState) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var ce *core.Error
	if errors.As(err, &ce) {
		return err
	}
	state := core.Untouched
	if st.changed {
		state = core.Mixed
	}
	code := core.ErrImport
	if state == core.Mixed {
		code = core.ErrImportMixed
	}
	if name, ok := unknownCollation(err); ok {
		advice := "Change it in the dump to one the server knows."
		if strings.Contains(name, "_0900_") && !st.fix {
			advice = "Run again with --fix-collation."
		}
		e := core.Errorf(code, 3, "import: %s doesn't know collation %s%s. %s %s",
			serverLabel(st.engine, st.server), name, collationOrigin(name), advice, stateSentence(st, false))
		e.State = state
		return e
	}
	if lostConnection(err) {
		var e *core.Error
		if st.changed {
			e = core.Errorf(core.ErrImportMixed, 3, "import: the connection to %s was lost %s. The target is in a mixed state: some tables are pseudonymised, the rest still hold the earlier copy. Run the same command again.",
				st.target, progress(st))
		} else {
			e = core.Errorf(core.ErrImport, 3, "import: the connection to %s was lost before any table was replaced. The target is untouched. Run the same command again.", st.target)
		}
		e.State = state
		return e
	}
	if st.current != "" && undefinedTable(err) {
		e := core.Errorf(code, 3, "import: the target has no table %s and the input doesn't create it. A copy from a live PostgreSQL database brings rows only, so create the schema on the target first (run the app's migrations there or load pg_dump --schema-only), then run the same command again. %s",
			st.current, stateSentence(st, false))
		e.State = state
		return e
	}
	where := "import"
	if st.current != "" {
		where = "import: table " + st.current
	}
	e := core.Errorf(code, 3, "%s: %s. %s", where, describe(err), stateSentence(st, true))
	e.State = state
	return e
}

// undefinedTable reports whether err is PostgreSQL's undefined_table: a
// table the sink fills that the target doesn't have.
func undefinedTable(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "42P01"
}

// stateSentence says what the target holds after a stop.
func stateSentence(st importState, again bool) string {
	if !st.changed {
		return "The target is untouched."
	}
	s := "The target is in a mixed state " + progress(st) + ": some tables are pseudonymised, the rest still hold the earlier copy."
	if again {
		s += " Fix the cause and run the same command again."
	}
	return s
}

// progress is "after 37 of 61 tables", "after 37 tables" when the total
// is unknown, or "during table wp_users" when the first table broke
// after it was dropped. One table reads "after 1 table".
func progress(st importState) string {
	if st.committed == 0 && st.current != "" {
		return "during table " + st.current
	}
	if st.total >= st.committed && st.total > 0 {
		return fmt.Sprintf("after %d of %d %s", st.committed, st.total, tables(st.total))
	}
	return fmt.Sprintf("after %d %s", st.committed, tables(st.committed))
}

// tables is "table" for one and "tables" for any other count.
func tables(n int) string {
	if n == 1 {
		return "table"
	}
	return "tables"
}

// serverLabel is "MariaDB 10.6" or "MySQL 8.0": the engine and the major
// and minor version.
func serverLabel(d core.Dialect, v string) string {
	name := map[core.Dialect]string{core.MySQL: "MySQL", core.MariaDB: "MariaDB", core.Postgres: "PostgreSQL", core.SQLite: "SQLite"}[d]
	if name == "" {
		name = "The server"
	}
	parts := strings.SplitN(v, ".", 3)
	if len(parts) >= 2 {
		return name + " " + parts[0] + "." + parts[1]
	}
	if v != "" {
		return name + " " + v
	}
	return name
}

// collationOrigin names the server a collation comes from, when it is
// one of the two families that move badly between MySQL and MariaDB.
func collationOrigin(name string) string {
	switch {
	case strings.Contains(name, "_0900_"):
		return " (MySQL 8)"
	case strings.Contains(name, "_uca1400_"):
		return " (MariaDB 10.10 and later)"
	}
	return ""
}

var reUnknownCollation = regexp.MustCompile(`(?i)unknown collation:?\s*'([^']+)'`)

// unknownCollation finds MySQL error 1273 (or the sink's own check) and
// the collation it names.
func unknownCollation(err error) (string, bool) {
	var ce *collationError
	if errors.As(err, &ce) {
		return ce.name, true
	}
	var me *mysql.MySQLError
	if errors.As(err, &me) && me.Number == 1273 {
		if m := reUnknownCollation.FindStringSubmatch(me.Message); m != nil {
			return m[1], true
		}
		return "(unnamed)", true
	}
	return "", false
}

// lostConnection reports an error that means the session is gone: a
// closed or reset connection, a server that went away, or a pool that
// was closed under the sink.
func lostConnection(err error) bool {
	if errors.Is(err, driver.ErrBadConn) || errors.Is(err, mysql.ErrInvalidConn) || errors.Is(err, sql.ErrConnDone) ||
		errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	var me *mysql.MySQLError
	if errors.As(err, &me) {
		switch me.Number {
		case 1053, 2006, 2013, 4031: // shutdown, gone away, lost, idle timeout
			return true
		}
		return false
	}
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return strings.HasPrefix(pe.Code, "08") || pe.Code == "57P01" || pe.Code == "57P02" || pe.Code == "57P03"
	}
	var ne *net.OpError
	if errors.As(err, &ne) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "database is closed") || strings.Contains(msg, "conn closed") || strings.Contains(msg, "connection reset")
}

// describe names a driver error with its code and its message, the
// quoted parts masked.
func describe(err error) string {
	var me *mysql.MySQLError
	if errors.As(err, &me) {
		state := ""
		if me.SQLState != [5]byte{} {
			state = " (" + string(me.SQLState[:]) + ")"
		}
		return "MySQL error " + strconv.Itoa(int(me.Number)) + state + ": " + mask(me.Message, mysqlTails[me.Number])
	}
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return "PostgreSQL error " + pe.Code + ": " + mask(pe.Message, nil)
	}
	return mask(err.Error(), nil)
}

// reName is a key or column name in a message: no quotes, no spaces.
const reName = "[^'\"`\\s]*"

// The ends of MySQL messages that follow a quoted value and name only
// the schema, by error number: "' for key 'k'" (1062, 1586) and "' for
// column 'c' at row 3" (1366, 1292). A name is a key or column of the
// dump, never row data. Each is matched only for its own error numbers
// and must close the message: a value that spells one is followed by
// the closing quote of its own message.
var (
	reKeyTail    = regexp.MustCompile("' for key '" + reName + "'$")
	reColumnTail = regexp.MustCompile("' for column ('" + reName + "'|`" + reName + "`(\\.`" + reName + "`)*) at row [0-9]+$")
	mysqlTails   = map[uint16]*regexp.Regexp{1062: reKeyTail, 1586: reKeyTail, 1366: reColumnTail, 1292: reColumnTail}
)

// reRest are the words a message may hold after its last quoted part,
// as in "Column 'c' cannot be null" (1048).
var reRest = regexp.MustCompile(`^(['"])( cannot be null| is violated\.?| doesn't have a default value| at row [0-9]+)?$`)

// mask hides every value a driver message quotes. Servers write a value
// into a message as it is, without escaping its quotes; MySQL also cuts
// a long message short, so pairing quotes can't tell where a value ends
// ("Duplicate entry 'o'brien@example.test' for key 'k'"). Everything
// from the first quote on is masked; only the schema-naming end tail
// matches or a closing quote with known words after it (reRest) is kept.
func mask(s string, tail *regexp.Regexp) string {
	i := strings.IndexAny(s, `'"`)
	if i < 0 {
		return s
	}
	rest := s[i+1:]
	if tail != nil {
		if loc := tail.FindStringIndex(rest); loc != nil {
			return s[:i] + "'•••" + rest[loc[0]:]
		}
	}
	if j := strings.LastIndexAny(rest, `'"`); j >= 0 && reRest.MatchString(rest[j:]) {
		return s[:i] + "'•••'" + rest[j+1:]
	}
	return s[:i] + "'•••'"
}

// targetName is "name@host" for a server database and the file name for
// SQLite, as the import-mixed message names the target.
func (c *Conn) targetName() string {
	if c.engine == core.SQLite {
		return filepath.Base(c.dbName)
	}
	host := c.ep.Host
	if host == "" {
		host = "127.0.0.1"
	}
	return c.dbName + "@" + host
}
