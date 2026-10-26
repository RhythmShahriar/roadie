// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package db

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	gomysql "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/dump/mysql"
)

// Limits of one INSERT the sink sends.
const (
	maxStmtBytes  = 1 << 20 // values per INSERT, about 1 MiB
	maxMySQLVars  = 65535   // placeholders in one MySQL prepared statement
	maxSQLiteVars = 32766   // SQLITE_MAX_VARIABLE_NUMBER since 3.32
)

// sink imports a stream table by table. A CREATE TABLE in the stream
// replaces the table (DROP TABLE IF EXISTS first); rows of a table the
// stream didn't create replace the rows of the existing table. Each
// table's rows go in one transaction; PostgreSQL and SQLite also run the
// DROP and CREATE in it, so a table there is replaced whole or not at
// all. MySQL commits DDL on its own, so a MySQL table is changed from
// its DROP on.
type sink struct {
	c        *Conn
	o        SinkOptions
	only     map[string]bool
	state    core.TargetState
	st       importState
	inTx     bool
	unit     bool   // the open unit holds a table
	cur      string // table of the open unit
	qcur     string // the same, quoted as the statement wrote it
	drop     string // a DROP TABLE seen without its CREATE yet, quoted
	dropFor  string
	mode     mysql.Mode
	vars     mysql.Vars // the user variables the dump has set, for sql_mode
	delim    string     // the mysql client's current DELIMITER, "" for ';'
	verb     string     // INSERT INTO, INSERT IGNORE INTO or REPLACE INTO
	copyCmd  string     // the COPY command of the open PostgreSQL statement
	copyPos  []int      // value positions it sends
	copyDump bool       // the stream's own COPY block: untouched lines go as they are
	err      error
	done     bool
}

func (c *Conn) newSink(ctx context.Context, o SinkOptions) (core.Sink, error) {
	if c.closed {
		return nil, errClosed
	}
	if c.mode == ReadOnly {
		return nil, core.ErrReadOnly
	}
	if c.busy {
		return nil, errBusy
	}
	s := &sink{c: c, o: o, state: core.Untouched}
	s.st = importState{engine: c.engine, server: c.server, target: c.targetName(), total: o.Total, fix: o.FixCollation}
	if o.Tables != nil {
		s.only = map[string]bool{}
		for _, t := range o.Tables {
			s.only[t] = true
		}
	}
	switch c.engine {
	case core.MySQL, core.MariaDB:
		// A dump creates tables in name order, not in key order.
		if _, err := c.conn.ExecContext(ctx, "SET foreign_key_checks = 0"); err != nil {
			return nil, importError(err, s.st)
		}
	case core.SQLite:
		if _, err := c.conn.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
			return nil, importError(err, s.st)
		}
	}
	c.busy = true
	return s, nil
}

func (s *sink) State() core.TargetState { return s.state }

func (s *sink) Write(ctx context.Context, b *core.Batch) error {
	if s.err != nil {
		return s.err
	}
	if s.done {
		return errors.New("db: write after Commit or Abort")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var err error
	if b.Pass != nil {
		err = s.pass(ctx, b.Pass)
	} else if b.Table != nil {
		err = s.rows(ctx, b)
	}
	if err != nil {
		return s.fail(err)
	}
	return nil
}

func (s *sink) Commit(ctx context.Context) error {
	if s.err != nil {
		return s.err
	}
	if s.done {
		return nil
	}
	if err := s.flushDrop(ctx); err != nil {
		return s.fail(err)
	}
	if err := s.endUnit(ctx); err != nil {
		return s.fail(err)
	}
	s.done = true
	s.state = core.Replaced
	s.c.busy = false
	return nil
}

// Abort rolls back the open table, so the target holds only whole
// tables: untouched when none was replaced, mixed otherwise.
func (s *sink) Abort(context.Context) error {
	if s.done {
		return nil
	}
	s.done = true
	s.c.busy = false
	if s.inTx {
		// The context may be canceled already; the rollback must still run.
		_, _ = s.c.conn.ExecContext(context.Background(), "ROLLBACK")
		s.inTx = false
	}
	if s.st.changed {
		s.state = core.Mixed
	}
	return nil
}

// fail keeps the worded error for every later call. The open
// transaction is rolled back by Abort.
func (s *sink) fail(err error) error {
	if s.st.changed {
		s.state = core.Mixed
	}
	err = importError(err, s.st)
	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		s.err = err
	}
	return err
}

func (s *sink) wanted(name string) bool { return s.only == nil || s.only[name] }

func (s *sink) isMySQL() bool { return s.c.engine == core.MySQL || s.c.engine == core.MariaDB }

// exec runs one statement on the session.
func (s *sink) exec(ctx context.Context, q string, args ...any) error {
	_, err := s.c.conn.ExecContext(ctx, q, args...)
	return err
}

// begin opens a transaction when none is open.
func (s *sink) begin(ctx context.Context) error {
	if s.inTx {
		return nil
	}
	q := "BEGIN"
	if s.isMySQL() {
		q = "START TRANSACTION"
	}
	if err := s.exec(ctx, q); err != nil {
		return err
	}
	s.inTx = true
	return nil
}

// endUnit commits the open transaction and counts its table.
func (s *sink) endUnit(ctx context.Context) error {
	if s.inTx {
		if err := s.exec(ctx, "COMMIT"); err != nil {
			return err
		}
		s.inTx = false
		s.st.changed = true
	}
	if s.unit {
		s.st.committed++
		s.st.changed = true
	}
	if s.st.changed {
		s.state = core.Mixed
	}
	s.unit, s.cur, s.qcur, s.st.current = false, "", "", ""
	s.copyCmd, s.copyPos, s.copyDump = "", nil, false
	return nil
}

// tableName returns the name the sink puts in the SQL it builds (DROP
// TABLE IF EXISTS, DELETE FROM, INSERT INTO) for raw, a name as
// objectName read it. On MySQL and MariaDB that is the bare table name
// quoted again. A name with a database in front is refused: the
// statement would act on that database instead of the one --to names.
// On shared hosting staging and production often share one server. The
// allowlist refuses such names first; this keeps the import safe for a
// stream from anywhere else. PostgreSQL's schema-qualified names
// (public.t) stay as written: a schema lives inside the --to database.
func (s *sink) tableName(raw, name string, line int64) (string, error) {
	if !s.isMySQL() || raw == "" {
		return raw, nil
	}
	if qualifiedName(raw) {
		where := "the input"
		if line > 0 {
			where = fmt.Sprintf("line %d of the input", line)
		}
		e := core.Errorf(core.ErrDumpRefused, 3, "dump-refused: %s names a table with its database, which would send the import to a database --to doesn't name. Export the dump again with mysqldump or mariadb-dump, which write table names without their database.", where)
		e.Line = line
		return "", e
	}
	return quoteIdent(s.c.engine, name), nil
}

// flushDrop runs a DROP TABLE that no CREATE TABLE followed.
func (s *sink) flushDrop(ctx context.Context) error {
	if s.drop == "" {
		return nil
	}
	q := "DROP TABLE IF EXISTS " + s.drop
	s.drop, s.dropFor = "", ""
	if err := s.exec(ctx, q); err != nil {
		return err
	}
	s.st.changed = true
	s.state = core.Mixed
	return nil
}

// pass runs a statement that isn't row data.
func (s *sink) pass(ctx context.Context, st *core.Statement) error {
	d := s.c.engine
	switch st.Kind {
	case core.StmtComment, core.StmtTransaction, core.StmtLock, core.StmtUnlock, core.StmtAlterKeys:
		// The sink runs its own transactions; LOCK TABLES and DISABLE
		// KEYS would only get in their way.
		return nil
	case mysql.KindDelimiter:
		// DELIMITER is for the mysql client: the server answers it with
		// a syntax error. The sink sends one statement at a time, so it
		// only needs the delimiter to cut it off the statements that
		// follow (END */;; or END$$).
		s.delim = delimiterArg(st.Text)
		return nil
	case mysql.KindClient:
		// The allowlist refuses client commands; should one get here it
		// is still the client's, never the server's.
		return nil
	case core.StmtSet:
		if d == core.SQLite {
			return nil
		}
		text := st.Text
		if s.isMySQL() {
			if s.o.FixCollation {
				text = mysql.FixCollation(text, s.mode)
			}
			s.mode = mysql.SQLMode(text, s.mode, &s.vars)
		}
		return s.exec(ctx, s.sql(text))
	case core.StmtDropTable:
		q, name := objectName(st.Text, "table")
		if name == "" || !s.wanted(name) {
			return nil
		}
		q, err := s.tableName(q, name, st.Line)
		if err != nil {
			return err
		}
		if err := s.flushDrop(ctx); err != nil {
			return err
		}
		if err := s.endUnit(ctx); err != nil {
			return err
		}
		// Held back until the CREATE TABLE that follows has passed the
		// collation check, so a refused table is never dropped.
		s.drop, s.dropFor = q, name
		return nil
	case core.StmtCreateTable:
		return s.create(ctx, st)
	}
	if bytes.HasPrefix(bytes.TrimSpace(st.Text), []byte(`\`)) {
		// A psql meta-command the allowlist kept (pg_dump's \restrict
		// and \unrestrict): for psql, not the server.
		return nil
	}
	if s.only != nil {
		return nil // --only: nothing beyond the listed tables
	}
	if err := s.flushDrop(ctx); err != nil {
		return err
	}
	text := st.Text
	if s.isMySQL() {
		// Any other MySQL statement here is DDL, which commits on its own.
		if err := s.endUnit(ctx); err != nil {
			return err
		}
		if err := s.exec(ctx, s.sql(text)); err != nil {
			return err
		}
		s.st.changed = true
		s.state = core.Mixed
		return nil
	}
	if d == core.Postgres && st.Kind == core.StmtSequence {
		text = reCreateSeq.ReplaceAll(text, []byte("${1} IF NOT EXISTS "))
	}
	if err := s.begin(ctx); err != nil {
		return err
	}
	return s.exec(ctx, s.sql(text))
}

// sql is a statement as the driver takes it: without the current
// DELIMITER and the trailing semicolons and space.
func (s *sink) sql(b []byte) string {
	return stmtText(trimDelim(b, s.delim))
}

// reCreateSeq finds a CREATE SEQUENCE without IF NOT EXISTS: a sequence
// owned by a table goes with DROP TABLE, one that isn't stays and must
// not stop the import.
var reCreateSeq = regexp.MustCompile(`(?i)^(\s*CREATE\s+SEQUENCE)\s+(?:IF\s+NOT\s+EXISTS\s+)?`)

// create replaces a table: collation check (MySQL), DROP TABLE IF
// EXISTS, then the CREATE TABLE of the stream.
func (s *sink) create(ctx context.Context, st *core.Statement) error {
	q, name := objectName(st.Text, "table")
	if name == "" {
		return fmt.Errorf("a CREATE TABLE on line %d names no table", st.Line)
	}
	q, err := s.tableName(q, name, st.Line)
	if err != nil {
		return err
	}
	if !s.wanted(name) {
		return nil
	}
	if s.dropFor != "" && s.dropFor != name {
		if err := s.flushDrop(ctx); err != nil {
			return err
		}
	}
	s.drop, s.dropFor = "", ""
	if err := s.endUnit(ctx); err != nil {
		return err
	}
	s.cur, s.qcur, s.st.current = name, q, name
	text := st.Text
	drop := "DROP TABLE IF EXISTS " + q
	switch s.c.engine {
	case core.MySQL, core.MariaDB:
		if s.o.FixCollation {
			text = mysql.FixCollation(text, s.mode)
		}
		if err := s.checkCollations(ctx, text); err != nil {
			return err
		}
		if err := s.exec(ctx, drop); err != nil {
			return err
		}
		s.st.changed = true
		s.state = core.Mixed
		if err := s.exec(ctx, s.sql(text)); err != nil {
			return err
		}
	case core.Postgres:
		if err := s.begin(ctx); err != nil {
			return err
		}
		// CASCADE drops the foreign keys of other tables that point here;
		// a pg_dump adds them back after the data.
		if err := s.exec(ctx, drop+" CASCADE"); err != nil {
			return err
		}
		if err := s.exec(ctx, s.sql(text)); err != nil {
			return err
		}
	default:
		if err := s.begin(ctx); err != nil {
			return err
		}
		if err := s.exec(ctx, drop); err != nil {
			return err
		}
		if err := s.exec(ctx, s.sql(text)); err != nil {
			return err
		}
	}
	s.unit = true
	return nil
}

var reCollate = regexp.MustCompile("(?i)\\bcollate\\s*=?\\s*['\"`]?([a-z0-9_]+)")

// checkCollations refuses a CREATE TABLE naming a collation the server
// doesn't have, before the table is dropped.
func (s *sink) checkCollations(ctx context.Context, text []byte) error {
	ms := reCollate.FindAllSubmatch(text, -1)
	if s.c.collate == nil {
		s.c.collate = map[string]bool{}
	}
	for _, m := range ms {
		n := strings.ToLower(string(m[1]))
		known, seen := s.c.collate[n]
		if !seen {
			var err error
			if known, err = s.knowsCollation(ctx, n); err != nil {
				return err
			}
			s.c.collate[n] = known
		}
		if !known {
			return &collationError{name: n}
		}
	}
	return nil
}

// knowsCollation asks the server itself: information_schema lists
// MariaDB's uca1400 collations without their character set, so only the
// server's own answer covers every version. name matched [a-z0-9_]+, so
// it is safe in the query text.
func (s *sink) knowsCollation(ctx context.Context, name string) (bool, error) {
	cs, _, _ := strings.Cut(name, "_")
	var out string
	err := s.c.conn.QueryRowContext(ctx, "SELECT CONVERT('a' USING "+cs+") COLLATE "+name).Scan(&out) //nolint:gosec // name is [a-z0-9_]+
	var me *gomysql.MySQLError
	switch {
	case err == nil:
		return true, nil
	case errors.As(err, &me) && (me.Number == 1273 || me.Number == 1115):
		return false, nil // unknown collation, unknown character set
	case errors.As(err, &me):
		return true, nil // let the CREATE TABLE tell
	}
	return false, err
}

// rows imports the rows of one batch.
func (s *sink) rows(ctx context.Context, b *core.Batch) error {
	tb := b.Table
	if !s.wanted(tb.Name) {
		return nil
	}
	if tb.Name != s.cur {
		// Rows of a table the stream didn't create: they replace the rows
		// of the table as it is on the target.
		if err := s.flushDrop(ctx); err != nil {
			return err
		}
		if err := s.endUnit(ctx); err != nil {
			return err
		}
		q := ""
		if b.First {
			raw, name := objectName(b.Head, "into", "copy")
			var err error
			if q, err = s.tableName(raw, name, 0); err != nil {
				return err
			}
		}
		if q == "" {
			q = quoteIdent(s.c.engine, tb.Name)
		}
		s.cur, s.qcur, s.st.current = tb.Name, q, tb.Name
		if err := s.begin(ctx); err != nil {
			return err
		}
		empty := "DELETE FROM " + q
		if s.c.engine == core.Postgres {
			empty = "TRUNCATE TABLE " + q
		}
		if err := s.exec(ctx, empty); err != nil {
			return err
		}
		s.unit = true
	}
	if err := s.begin(ctx); err != nil {
		return err
	}
	if b.First {
		s.verb = insertVerb(b.Head)
		if s.c.engine == core.Postgres {
			s.startCopy(b)
		}
	}
	if s.c.engine == core.Postgres {
		return s.copyRows(ctx, b)
	}
	return s.insertRows(ctx, b)
}

// columns lists the table column of each value position that is
// written: generated columns are left out, as the server fills them.
func columns(b *core.Batch, n int) (pos []int, names []string) {
	tb := b.Table
	for i := 0; i < n; i++ {
		ci := i
		if b.Columns != nil {
			if i >= len(b.Columns) {
				break
			}
			ci = b.Columns[i]
		}
		if ci < 0 || ci >= len(tb.Columns) || tb.Columns[ci].Generated {
			continue
		}
		pos = append(pos, i)
		names = append(names, tb.Columns[ci].Name)
	}
	return pos, names
}

// insertRows sends the rows as multi-row INSERTs with placeholders, up
// to about 1 MiB of values each.
func (s *sink) insertRows(ctx context.Context, b *core.Batch) error {
	var first *core.Row
	for i := range b.Rows {
		if !b.Rows[i].Drop {
			first = &b.Rows[i]
			break
		}
	}
	if first == nil {
		return nil
	}
	pos, names := columns(b, len(first.Values))
	if len(pos) == 0 {
		return nil
	}
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = quoteIdent(s.c.engine, n)
	}
	verb := s.verb
	if s.c.engine == core.SQLite {
		verb = strings.Replace(strings.Replace(verb, "INSERT IGNORE", "INSERT OR IGNORE", 1), "REPLACE INTO", "INSERT OR REPLACE INTO", 1)
	}
	head := verb + " " + s.qcur + " (" + strings.Join(quoted, ",") + ") VALUES "
	tuple := "(" + strings.TrimSuffix(strings.Repeat("?,", len(pos)), ",") + ")"
	maxVars := maxMySQLVars
	if s.c.engine == core.SQLite {
		maxVars = maxSQLiteVars
	}
	var (
		sb   strings.Builder
		args []any
		size int
	)
	flush := func() error {
		if len(args) == 0 {
			return nil
		}
		err := s.exec(ctx, sb.String(), args...)
		sb.Reset()
		args, size = args[:0], 0
		return err
	}
	for i := range b.Rows {
		r := &b.Rows[i]
		if r.Drop {
			continue
		}
		if len(args) > 0 && (size >= maxStmtBytes || len(args)+len(pos) > maxVars) {
			if err := flush(); err != nil {
				return err
			}
		}
		if len(args) == 0 {
			sb.WriteString(head)
		} else {
			sb.WriteByte(',')
		}
		sb.WriteString(tuple)
		for _, p := range pos {
			if p >= len(r.Values) {
				return fmt.Errorf("a row of %s has %d values, %d expected", b.Table.Name, len(r.Values), len(pos))
			}
			a, err := argOf(s.c.engine, r.Values[p])
			if err != nil {
				return err
			}
			args = append(args, a)
			size += len(r.Values[p].Raw)
		}
	}
	return flush()
}

// copyRows sends the rows as COPY text through pgx: an untouched row of
// a dump's COPY block as its own line, every other row encoded.
func (s *sink) copyRows(ctx context.Context, b *core.Batch) error {
	var buf []byte
	for i := range b.Rows {
		r := &b.Rows[i]
		if r.Drop {
			continue
		}
		if s.copyCmd == "" {
			// An INSERT (pg_dump --inserts, or a live source) becomes a
			// COPY of the columns it names.
			pos, names := columns(b, len(r.Values))
			quoted := make([]string, len(names))
			for k, n := range names {
				quoted[k] = quoteIdent(core.Postgres, n)
			}
			s.copyCmd = "COPY " + s.qcur + " (" + strings.Join(quoted, ", ") + ") FROM STDIN"
			s.copyPos = pos
		}
		if s.copyDump && r.Src != nil && !dirty(r) {
			buf = append(buf, r.Src...)
			continue
		}
		for k, p := range s.copyPos {
			if k > 0 {
				buf = append(buf, '\t')
			}
			if p >= len(r.Values) {
				return fmt.Errorf("a row of %s has %d values, %d expected", b.Table.Name, len(r.Values), len(s.copyPos))
			}
			var err error
			if buf, err = copyField(buf, r.Values[p]); err != nil {
				return err
			}
		}
		buf = append(buf, '\n')
	}
	if len(buf) == 0 {
		return nil
	}
	return s.c.conn.Raw(func(dc any) error {
		sc, ok := dc.(*stdlib.Conn)
		if !ok {
			return errors.New("not a pgx connection")
		}
		_, err := sc.Conn().PgConn().CopyFrom(ctx, bytes.NewReader(buf), s.copyCmd)
		return err
	})
}

// startCopy reads the COPY command of a dump's COPY block from its head;
// its column list is the value order of every row. An INSERT head leaves
// it empty for copyRows to build.
func (s *sink) startCopy(b *core.Batch) {
	s.copyCmd, s.copyPos, s.copyDump = "", nil, false
	head := strings.TrimSpace(string(b.Head))
	if !hasWordPrefix(head, "copy") {
		return
	}
	s.copyCmd = strings.TrimRight(head, "; \t\r\n")
	s.copyDump = true
	if len(b.Rows) > 0 {
		for j := range b.Rows[0].Values {
			s.copyPos = append(s.copyPos, j)
		}
	}
}
