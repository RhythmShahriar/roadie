// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package allow

import (
	"bytes"
	"strings"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/dump/mysql"
)

// Plan reasons for dropped statements.
const (
	reasonPrivileges  = "the staging database keeps its own users and privileges"
	reasonOwner       = "objects belong to the user that imports the dump"
	reasonEvents      = "scheduled events would run on the staging database"
	reasonUseSame     = "the import goes to the database --to names"
	reasonDBSettings  = "the staging database keeps its own settings"
	reasonReplication = "replication settings stay the staging server's"
	reasonPercona     = "Percona Server's rocksdb_bulk_load check runs SQL built while the dump imports"
	reasonNoRoutines  = "routines aren't copied without --keep-routines"
)

// clientWords are the mysql client's named commands (mysql.cc). None of
// them starts a statement Roadie keeps.
var clientWords = map[string]bool{
	"clear": true, "connect": true, "edit": true, "ego": true, "exit": true, "go": true,
	"help": true, "nopager": true, "notee": true, "pager": true, "print": true, "prompt": true,
	"quit": true, "rehash": true, "source": true, "status": true, "system": true, "tee": true,
	"charset": true, "warnings": true, "nowarning": true, "resetconnection": true,
	"query_attributes": true, "ssl_session_data_print": true, "sandbox": true,
}

// sessionVars are the session variables dump tools set and restore.
var sessionVars = map[string]bool{
	"character_set_client": true, "character_set_results": true, "character_set_connection": true,
	"collation_connection": true, "time_zone": true, "sql_mode": true, "foreign_key_checks": true,
	"unique_checks": true, "sql_notes": true, "autocommit": true, "note_verbosity": true,
	"sql_quote_show_create": true, "check_constraint_checks": true,
}

// engines are the storage engines a kept CREATE TABLE may name. Engines
// that reach other servers or files (FEDERATED, CONNECT, SPIDER, S3) are
// left out on purpose.
var engines = map[string]bool{
	"innodb": true, "myisam": true, "aria": true, "maria": true, "memory": true, "heap": true,
	"archive": true, "csv": true, "blackhole": true, "merge": true, "mrg_myisam": true,
	"rocksdb": true, "tokudb": true, "mroonga": true, "ndbcluster": true, "ndb": true,
	"columnstore": true, "xtradb": true, "sequence": true,
}

// setFuncs are the functions a kept SET value may call: the ones dump
// tools use to save and restore settings.
var setFuncs = map[string]bool{
	"ifnull": true, "concat": true, "concat_ws": true, "coalesce": true, "if": true,
	"replace": true, "lower": true, "upper": true,
}

// mysqlSyntax is the quoting the sql_mode m gives.
func mysqlSyntax(m mysql.Mode) syntax {
	return syntax{backslashes: !m.NoBackslashEscapes, ansiQuotes: m.ANSIQuotes}
}

// trimDelims drops the delimiter tokens (';' or ';;') that end a statement.
func trimDelims(t toks) toks {
	for len(t) > 0 && t.p(len(t)-1, ';') {
		t = t[:len(t)-1]
	}
	return t
}

func checkMySQL(st core.Statement, o Options) Decision {
	text := trimmed(st.Text)
	if bytes.Equal(bytes.TrimRight(text, " \t\r\n"), []byte(mysql.SandboxLine)) {
		return keep()
	}
	if st.Kind == mysql.KindClient || bytes.HasPrefix(text, []byte{'\\'}) {
		return clientRefusal(text)
	}
	t := trimDelims(lex(mysql.Body(st.Text, o.Mode), mysqlSyntax(o.Mode)))
	if len(t) == 0 {
		if st.Kind == mysql.KindUse {
			return refuse("a USE statement for another database", whyOtherDB)
		}
		return keep() // comments only
	}
	w0 := t.w(0)
	switch {
	case st.Kind == mysql.KindUse || w0 == "use":
		return useStatement(t, o)
	case clientWords[w0] || t.p(0, '?') || hasBackslash(t):
		return clientRefusal(text)
	case w0 == "delimiter":
		if bytes.ContainsAny(bytes.TrimRight(text, " \t\r\n"), "\r\n") {
			return refuse(label(t, 1), whyUnknown)
		}
		return keep()
	}
	if d, ok := globalChecks(t); ok {
		return d
	}
	switch w0 {
	case "create":
		return mysqlCreate(st, t, o)
	case "drop":
		return mysqlDrop(t)
	case "alter":
		return mysqlAlter(st, t, o)
	case "lock", "unlock":
		if t.w(1) == "tables" || t.w(1) == "table" {
			return keep()
		}
	case "set":
		return mysqlSet(t)
	case "start":
		if t.w(1) == "transaction" && onlyWords(t[2:], "with", "consistent", "snapshot", "read", "only", "write") {
			return keep()
		}
	case "begin":
		if onlyWords(t[1:], "work") {
			return keep()
		}
	case "commit":
		if onlyWords(t[1:], "work", "and", "no", "chain", "release") {
			return keep()
		}
	case "rollback":
		if onlyWords(t[1:], "work") {
			return keep()
		}
	case "insert", "replace", "update", "delete":
		l := label(t, 1)
		return refuse(article(l)+" "+l+" statement that isn't a list of rows", whyRows)
	case "select":
		return mysqlSelect(t)
	case "do":
		if isSetval(t, 1) {
			return keep()
		}
		return refuse("a DO statement", whyRunsCode)
	case "load":
		return refuse(label(t, 2), whyReadsFiles)
	case "grant", "revoke":
		return drop(strings.ToUpper(w0), reasonPrivileges)
	case "rename":
		if t.w(1) == "user" {
			return drop("RENAME USER", reasonPrivileges)
		}
	case "flush":
		if t.w(1) == "privileges" {
			return drop("FLUSH PRIVILEGES", reasonPrivileges)
		}
	case "prepare":
		if name, ok := perconaPrepare(t); ok {
			d := drop("PREPARE", reasonPercona)
			d.prepared = name
			return d
		}
		return refuse("a PREPARE statement", whyRunsCode)
	case "execute", "deallocate":
		n := 1
		if w0 == "deallocate" {
			n = 2
		}
		if t.name(n) && o.Prepared[t[n].w] {
			return drop(label(t, n), reasonPercona)
		}
		return refuse(label(t, n), whyRunsCode)
	}
	return refuse(unknownLabel(t), whyUnknown)
}

// whyRows is the danger of a statement that changes rows on its own.
var whyRows = why{"would change rows outside the dump's INSERTs, where Roadie can't pseudonymise them", adviceLeaveOut}

// onlyWords reports whether every token is one of the words ws.
func onlyWords(t toks, ws ...string) bool {
	for i := range t {
		if t.p(i, ',') {
			continue
		}
		ok := false
		for _, w := range ws {
			ok = ok || t.w(i) == w
		}
		if !ok {
			return false
		}
	}
	return true
}

// clientRefusal refuses a mysql client command, naming it.
func clientRefusal(text []byte) Decision {
	what := "a mysql client command"
	if len(text) >= 2 && text[0] == '\\' && text[1] > ' ' && text[1] < 0x7f {
		what += " (\\" + string(text[1]) + ")"
	} else if t := lex(text, syntax{}); safeName(t.w(0)) != "" && len(t.w(0)) <= 24 {
		what += " (" + t.w(0) + ")"
	}
	return refuse(what, whyClient)
}

// useStatement drops USE of the dump's own database and refuses any other.
func useStatement(t toks, o Options) Decision {
	name, _ := t.objName(1, false)
	if o.Database != "" && name == o.Database {
		return drop("USE", reasonUseSame)
	}
	return refuse("a USE statement for another database", whyOtherDB)
}

// skipCreate skips what may stand between CREATE (or ALTER) and the kind
// of object: OR REPLACE, TEMPORARY, ALGORITHM=x, DEFINER=user@host, SQL
// SECURITY x, AGGREGATE, ONLINE, IF NOT EXISTS comes after the kind.
func skipCreate(t toks, i int) int {
	for i < len(t) {
		switch {
		case t.is(i, "or", "replace"):
			i += 2
		case t.w(i) == "temporary" || t.w(i) == "aggregate" || t.w(i) == "online" || t.w(i) == "offline":
			i++
		case t.w(i) == "algorithm" && t.p(i+1, '='):
			i += 3
		case t.is(i, "sql", "security"):
			i += 3
		case t.w(i) == "definer":
			i = skipDefinerToks(t, i)
		default:
			return i
		}
	}
	return i
}

// skipDefinerToks skips DEFINER = user[@host] or DEFINER = CURRENT_USER[()].
func skipDefinerToks(t toks, i int) int {
	i++
	if t.p(i, '=') {
		i++
	}
	i++ // the user
	if t.p(i, '(') && t.p(i+1, ')') {
		return i + 2
	}
	if t.p(i, '@') {
		i += 2
	}
	return i
}

// afterIfExists skips IF [NOT] EXISTS at i.
func afterIfExists(t toks, i int) int {
	if t.w(i) != "if" {
		return i
	}
	i++
	if t.w(i) == "not" {
		i++
	}
	if t.w(i) == "exists" {
		i++
	}
	return i
}

func mysqlCreate(st core.Statement, t toks, o Options) Decision {
	i := skipCreate(t, 1)
	obj := t.w(i)
	switch obj {
	case "table":
		return mysqlCreateTable(t, i+1)
	case "index":
		return keep()
	case "unique", "fulltext", "spatial":
		if t.w(i+1) == "index" || t.w(i+1) == "key" {
			return keep()
		}
	case "sequence":
		d := keep()
		name, _ := t.objName(afterIfExists(t, i+1), false)
		d.creates = &object{"sequence", name}
		return d
	case "trigger", "procedure", "function", "view", "package":
		if obj == "function" && t.seq("soname") {
			return refuse("a CREATE FUNCTION ... SONAME statement", whyCommands)
		}
		return mysqlRoutine(st, t, i, o)
	case "event":
		return drop("CREATE EVENT", reasonEvents)
	case "user", "role":
		return drop("CREATE "+strings.ToUpper(obj), reasonPrivileges)
	case "database", "schema":
		return refuse("CREATE "+strings.ToUpper(obj), whyDatabase)
	}
	return refuse(unknownLabel(t), whyUnknown)
}

// mysqlCreateTable keeps a CREATE TABLE unless it fills itself from a
// query, puts its files outside the data folder or uses an engine that
// reaches another server.
func mysqlCreateTable(t toks, i int) Decision {
	name, _ := t.objName(afterIfExists(t, i), false)
	for k := range t {
		switch {
		case t.w(k) == "select":
			return refuse("a CREATE TABLE ... SELECT statement", whyQuery)
		case (t.w(k) == "data" || t.w(k) == "index") && t.w(k+1) == "directory":
			return refuse("a CREATE TABLE ... "+strings.ToUpper(t.w(k))+" DIRECTORY statement", whyWrites)
		case t.w(k) == "connection" && (t.p(k+1, '=') || t.kind(k+1) == str):
			return refuse("a CREATE TABLE ... CONNECTION statement", whyConnects)
		}
		if d, bad := badEngine(t, k); bad {
			return d
		}
	}
	d := keep()
	d.creates = &object{"table", name}
	return d
}

// badEngine refuses ENGINE=x at t[k] for an engine not on the list.
func badEngine(t toks, k int) (Decision, bool) {
	if t.w(k) != "engine" {
		return Decision{}, false
	}
	v := k + 1
	if t.p(v, '=') {
		v++
	}
	e := ""
	if t.name(v) {
		e = strings.ToLower(t[v].w)
	}
	if engines[e] {
		return Decision{}, false
	}
	what := "a table with an unknown storage engine"
	if n := safeName(e); n != "" && len(n) <= 24 {
		what = "a table with ENGINE=" + strings.ToUpper(n)
	}
	return refuse(what, whyConnects), true
}

// routineKinds are the words of the routine kinds, for messages.
var routineKinds = map[string]string{
	"trigger": "TRIGGER", "procedure": "PROCEDURE", "function": "FUNCTION", "view": "VIEW", "package": "PACKAGE",
}

// mysqlRoutine keeps a trigger, procedure, function or view with
// --keep-routines (DEFINER replaced by CURRENT_USER) and refuses it
// otherwise.
func mysqlRoutine(st core.Statement, t toks, i int, o Options) Decision {
	obj := t.w(i)
	j := i + 1
	if obj == "package" && t.w(j) == "body" {
		j++
	}
	name, _ := t.objName(afterIfExists(t, j), false)
	kind := "CREATE " + routineKinds[obj]
	if !o.KeepRoutines {
		return refuse(kind, routineWhy(obj))
	}
	d := keep()
	d.Kind, d.Routine = kind, true
	d.creates = &object{obj, name}
	if out, changed := rewriteDefiner(st.Text, o.Mode); changed {
		d.Text = out
	}
	what := kind
	if n := safeName(name); n != "" {
		what += " " + n
	}
	d.Note = "kept " + what + " with DEFINER=CURRENT_USER (--keep-routines)"
	return d
}

// routineWhy words the refusal of a routine of kind obj.
func routineWhy(obj string) why {
	switch obj {
	case "trigger":
		return why{whyRunsCode.danger, "Export with --skip-triggers, or pass --keep-routines to keep it without its DEFINER."}
	case "view":
		return why{whyRunsCode.danger, "Leave the view out of the export, or pass --keep-routines to keep it without its DEFINER."}
	}
	return why{whyRunsCode.danger, "Export without --routines, or pass --keep-routines to keep it without its DEFINER."}
}

func mysqlDrop(t toks) Decision {
	i := 1
	if t.w(i) == "temporary" {
		i++
	}
	obj := t.w(i)
	switch obj {
	case "table", "view", "trigger", "procedure", "function", "sequence", "package":
		j := i + 1
		if obj == "package" && t.w(j) == "body" {
			j++
		}
		d := keep()
		for j = afterIfExists(t, j); ; j++ {
			name, next := t.objName(j, false)
			if name == "" {
				break
			}
			d.drops = append(d.drops, object{dropKind(obj), name})
			j = next
			if !t.p(j, ',') {
				break
			}
		}
		return d
	case "index":
		return keep()
	case "event":
		return drop("DROP EVENT", reasonEvents)
	case "user", "role":
		return drop("DROP "+strings.ToUpper(obj), reasonPrivileges)
	case "database", "schema":
		return refuse("DROP "+strings.ToUpper(obj), whyDatabase)
	case "prepare":
		return refuse("a DROP PREPARE statement", whyRunsCode)
	}
	return refuse(unknownLabel(t), whyUnknown)
}

// dropKind maps a dropped object kind to the kind its CREATE records: a
// view may replace a placeholder table of the same name, so both count
// as one namespace.
func dropKind(obj string) string {
	if obj == "view" {
		return "table"
	}
	return obj
}

func mysqlAlter(st core.Statement, t toks, o Options) Decision {
	i := 1
	for t.w(i) == "online" || t.w(i) == "ignore" {
		i++
	}
	switch t.w(i) {
	case "table":
		return mysqlAlterTable(t, i+1)
	case "database", "schema":
		return drop("ALTER DATABASE", reasonDBSettings)
	case "user":
		return drop("ALTER USER", reasonPrivileges)
	case "event":
		return drop("ALTER EVENT", reasonEvents)
	}
	j := skipCreate(t, i)
	switch t.w(j) {
	case "view", "function", "procedure":
		kind := "ALTER " + routineKinds[t.w(j)]
		if !o.KeepRoutines {
			return refuse(kind, routineWhy(t.w(j)))
		}
		d := keep()
		d.Kind, d.Routine = kind, true
		if out, changed := rewriteDefiner(st.Text, o.Mode); changed {
			d.Text = out
		}
		d.Note = "kept " + kind + " with DEFINER=CURRENT_USER (--keep-routines)"
		return d
	}
	return refuse(unknownLabel(t), whyUnknown)
}

// alterClauses are the ALTER TABLE clauses dump tools write (phpMyAdmin
// adds its keys and AUTO_INCREMENT after the data) and other changes
// that stay inside the table.
var alterClauses = map[string]bool{
	"add": true, "modify": true, "change": true, "alter": true, "drop": true, "auto_increment": true,
	"disable": true, "enable": true, "rename": true, "convert": true, "default": true, "character": true,
	"charset": true, "collate": true, "comment": true, "row_format": true, "key_block_size": true,
	"order": true, "force": true, "algorithm": true, "lock": true, "pack_keys": true, "checksum": true,
	"stats_persistent": true, "stats_auto_recalc": true, "stats_sample_pages": true, "min_rows": true,
	"max_rows": true, "avg_row_length": true, "delay_key_write": true, "engine": true,
}

func mysqlAlterTable(t toks, i int) Decision {
	_, j := t.objName(i, false)
	for _, c := range clauses(t[j:]) {
		if len(c) == 0 {
			continue
		}
		if !alterClauses[c.w(0)] {
			return refuse("an ALTER TABLE ... "+label(c, 1)+" statement", whyUnknown)
		}
		for k := range c {
			if d, bad := badEngine(c, k); bad {
				return d
			}
			if (c.w(k) == "data" || c.w(k) == "index") && c.w(k+1) == "directory" {
				return refuse("an ALTER TABLE ... "+strings.ToUpper(c.w(k))+" DIRECTORY statement", whyWrites)
			}
			if c.w(k) == "connection" && (c.p(k+1, '=') || c.kind(k+1) == str) {
				return refuse("an ALTER TABLE ... CONNECTION statement", whyConnects)
			}
		}
	}
	return keep()
}

// clauses splits tokens at commas outside parentheses.
func clauses(t toks) []toks {
	var out []toks
	depth, start := 0, 0
	for i := range t {
		switch {
		case t.p(i, '('):
			depth++
		case t.p(i, ')'):
			depth--
		case t.p(i, ',') && depth == 0:
			out = append(out, t[start:i])
			start = i + 1
		}
	}
	return append(out, t[start:])
}

// mysqlSet keeps a SET of the session settings dump tools write and the
// user variables they save them in. A replication setting is dropped;
// any other setting, a GLOBAL one or a value that runs a query is refused.
func mysqlSet(t toks) Decision {
	cs := clauses(t[1:])
	switch {
	case t.w(1) == "names", t.is(1, "character", "set"), t.w(1) == "charset":
		// SET NAMES x is one clause of a SET; the ones after it are
		// assignments like any other ("SET NAMES x, GLOBAL y = 1").
		words := 1
		if t.w(1) == "character" {
			words = 2
		}
		if !plainValue(cs[0][words:]) {
			return refuse("a SET NAMES statement that reads a query", whyQuery)
		}
		cs = cs[1:]
	case t.w(1) == "password":
		return drop("SET PASSWORD", reasonPrivileges)
	case t.w(1) == "role", t.is(1, "default", "role"):
		return drop("SET ROLE", reasonPrivileges)
	case t.w(1) == "statement":
		return refuse("a SET STATEMENT ... FOR statement", whyUnknown)
	}
	out := keep()
	for _, a := range cs {
		d := mysqlAssign(a)
		switch d.Verdict {
		case Refuse:
			return d
		case Drop:
			out = d
		}
	}
	return out
}

// mysqlAssign judges one assignment of a SET.
func mysqlAssign(a toks) Decision {
	scope, k := "", 0
	scopes := map[string]bool{"global": true, "session": true, "local": true, "persist": true, "persist_only": true}
	if scopes[a.w(0)] {
		scope, k = a.w(0), 1
	}
	user := false
	name := ""
	switch {
	case a.p(k, '@') && a.p(k+1, '@'):
		k += 2
		if scopes[a.w(k)] && a.p(k+1, '.') {
			scope = a.w(k)
			k += 2
		}
		name = a.w(k)
		k++
	case a.p(k, '@'):
		user = true
		if a.kind(k+1) != noKind {
			name = strings.ToLower(strings.Trim(a[k+1].w, "'\""))
		}
		k += 2
	default:
		name = a.w(k)
		k++
	}
	switch {
	case a.p(k, '='):
		k++
	case a.p(k, ':') && a.p(k+1, '='):
		k += 2
	default:
		return refuse("a SET statement Roadie can't read", whyUnknown)
	}
	value := a[k:]
	switch {
	case user && strings.HasPrefix(name, "rocksdb_"):
		return drop("SET @rocksdb_*", reasonPercona)
	case user:
	case scope == "global" || scope == "persist" || scope == "persist_only":
		if name == "gtid_purged" {
			return drop("SET @@GLOBAL.GTID_PURGED", reasonReplication)
		}
		what := "a SET GLOBAL statement"
		if n := safeName(name); n != "" {
			what = "a SET GLOBAL " + n + " statement"
		}
		return refuse(what, whyGlobal)
	case name == "sql_log_bin":
		return drop("SET SQL_LOG_BIN", reasonReplication)
	case !sessionVars[name]:
		what := "a SET of an unknown setting"
		if n := safeName(name); n != "" {
			what = "a SET " + n + " statement"
		}
		return refuse(what, whySetting)
	}
	if !plainValue(value) {
		return refuse("a SET statement that reads a query", whyQuery)
	}
	return keep()
}

// plainValue reports whether a SET value holds only literals, variables
// and calls to the functions dump tools use, never a subquery.
func plainValue(v toks) bool {
	for i := range v {
		switch {
		case v.w(i) == "select":
			return false
		case v.kind(i) == word && v.p(i+1, '(') && !setFuncs[v.w(i)]:
			return false
		case v.p(i, '(') && i > 0 && v.kind(i-1) != word:
			return false // a bare (subquery)
		case v.p(i, '(') && i == 0:
			return false
		}
	}
	return true
}

// mysqlSelect keeps MariaDB's SELECT SETVAL for a sequence and drops
// Percona's rocksdb probe; any other SELECT is refused.
func mysqlSelect(t toks) Decision {
	if isSetval(t, 1) {
		return keep()
	}
	for i := range t {
		if t.w(i) == "into" && t.p(i+1, '@') && strings.HasPrefix(t.w(i+2), "rocksdb_") {
			return drop("SELECT ... INTO @rocksdb_*", reasonPercona)
		}
	}
	return refuse("a SELECT statement", whyUnknown)
}

// isSetval reports whether t[i:] is setval(name, literals...) and nothing else.
func isSetval(t toks, i int) bool {
	if t.w(i) != "setval" || !t.p(i+1, '(') || !t.p(len(t)-1, ')') {
		return false
	}
	for k := i + 2; k < len(t)-1; k++ {
		if t.kind(k) == punct && !t.p(k, ',') && !t.p(k, '.') && !t.p(k, '-') {
			return false
		}
	}
	return true
}

// perconaPrepare matches PREPARE s FROM @rocksdb_..., the probe Percona
// Server's mysqldump writes and returns s.
func perconaPrepare(t toks) (string, bool) {
	if !t.name(1) || t.w(2) != "from" || !t.p(3, '@') || !strings.HasPrefix(t.w(4), "rocksdb_") || len(t) != 5 {
		return "", false
	}
	return t[1].w, true
}

// rewriteDefiner replaces every DEFINER=user@host clause outside strings
// and plain comments with DEFINER=CURRENT_USER, so a kept routine runs
// as the user that imports it. Versioned comments are code and are
// rewritten too.
func rewriteDefiner(text []byte, m mysql.Mode) ([]byte, bool) {
	var out []byte
	last, n := 0, len(text)
	esc := !m.NoBackslashEscapes
	for i := 0; i < n; {
		c := text[i]
		switch {
		case c == '\'' || c == '"' || c == '`':
			i = quoted(text, i, esc && c != '`')
		case c == '/' && i+1 < n && text[i+1] == '*' && (i+2 >= n || text[i+2] != '!' && text[i+2] != 'M'):
			end := bytes.Index(text[i+2:], []byte("*/"))
			if end < 0 {
				i = n
			} else {
				i += end + 4
			}
		case c == '#' || (c == '-' && i+2 < n && text[i+1] == '-' && text[i+2] <= ' '):
			end := bytes.IndexByte(text[i:], '\n')
			if end < 0 {
				i = n
			} else {
				i += end
			}
		case (c|0x20) == 'd' && (i == 0 || !isWordByte(text[i-1])) && i+7 <= n && bytes.EqualFold(text[i:i+7], []byte("definer")) &&
			(i+7 == n || !isWordByte(text[i+7])):
			end, ok := definerEnd(text, i+7, esc)
			if !ok {
				i += 7
				continue
			}
			out = append(append(out, text[last:i]...), "DEFINER=CURRENT_USER"...)
			last, i = end, end
		default:
			i++
		}
	}
	if out == nil {
		return text, false
	}
	return append(out, text[last:]...), true
}

// definerEnd parses "= user@host" from i and returns the index after it.
// A clause that is already CURRENT_USER is left alone (ok false).
func definerEnd(b []byte, i int, esc bool) (int, bool) {
	skip := func(i int) int {
		for i < len(b) && isSpace(b[i]) {
			i++
		}
		return i
	}
	i = skip(i)
	if i >= len(b) || b[i] != '=' {
		return 0, false
	}
	i = skip(i + 1)
	part := func(i int) int {
		if i < len(b) && (b[i] == '`' || b[i] == '\'' || b[i] == '"') {
			return quoted(b, i, esc && b[i] != '`')
		}
		j := i
		for j < len(b) && (isWordByte(b[j]) || b[j] == '.' || b[j] == '%' || b[j] == '-') {
			j++
		}
		return j
	}
	j := part(i)
	if j == i {
		return 0, false
	}
	if bytes.EqualFold(b[i:j], []byte("current_user")) {
		return 0, false
	}
	if k := skip(j); k < len(b) && b[k] == '@' {
		if h := part(skip(k + 1)); h > skip(k+1) {
			j = h
		}
	}
	return j, true
}
