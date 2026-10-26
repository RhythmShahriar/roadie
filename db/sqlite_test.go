// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/dump/mysql"
)

// usersRows is more than two keyset pages, so paging is exercised.
const usersRows = 2*pageRows + 345

// newSQLite creates a SQLite file with a small WordPress-shaped schema.
func newSQLite(t *testing.T, rows int) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "site.sqlite")
	db, err := sql.Open("sqlite", p)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stmts := []string{
		`CREATE TABLE wp_options (option_id INTEGER PRIMARY KEY, option_name VARCHAR(191) NOT NULL UNIQUE, option_value TEXT)`,
		`CREATE TABLE wp_users (ID INTEGER PRIMARY KEY, user_email VARCHAR(100), created DATETIME, avatar BLOB, score REAL)`,
		`CREATE TABLE wp_2_options (option_id INTEGER PRIMARY KEY, option_name VARCHAR(191))`,
		`CREATE TABLE notes (body TEXT, n INTEGER)`,
		`CREATE TABLE calc (a INTEGER PRIMARY KEY, b INTEGER GENERATED ALWAYS AS (a * 2) VIRTUAL)`,
		`INSERT INTO wp_options VALUES (1, 'siteurl', 'https://shop.example.invalid'), (2, 'blogname', 'Shop')`,
		`INSERT INTO wp_2_options VALUES (1, 'siteurl')`,
		`INSERT INTO notes VALUES ('line one
line two', 1), ('tab	and ''quote''', 2), (NULL, NULL)`,
		`INSERT INTO calc (a) VALUES (1), (2), (3)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	ins, err := tx.Prepare(`INSERT INTO wp_users VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= rows; i++ {
		var avatar any
		if i%3 == 0 {
			avatar = []byte{0, byte(i), 0xff}
		}
		if _, err := ins.Exec(i, fmt.Sprintf("customer-%05d@example.invalid", i), "2026-10-26 12:00:00", avatar, float64(i)/4); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return p
}

// emptySQLite creates an empty SQLite file, as a target.
func emptySQLite(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "staging.sqlite")
	db, err := sql.Open("sqlite", p)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE keep (x INTEGER)`); err != nil {
		t.Fatal(err)
	}
	return p
}

func sqliteEP(p string) core.Endpoint {
	return core.Endpoint{Kind: core.DSN, Driver: core.SQLite, Path: p, DSN: "sqlite://" + filepath.ToSlash(p), Display: "sqlite://" + filepath.ToSlash(p)}
}

func openT(t *testing.T, p string, m Mode) *Conn {
	t.Helper()
	c, err := Open(context.Background(), sqliteEP(p), m)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestSQLiteSchema(t *testing.T) {
	c := openT(t, newSQLite(t, 3), ReadOnly)
	s, err := c.Schema(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.Dialect != core.SQLite || s.Server == "" || s.Prefix != "wp_" {
		t.Fatalf("schema head: %+v", s)
	}
	var names []string
	for _, tb := range s.Tables {
		names = append(names, tb.Name)
	}
	if got := strings.Join(names, ","); got != "calc,notes,wp_2_options,wp_options,wp_users" {
		t.Fatalf("tables %s", got)
	}
	opt := s.Table("wp_options")
	if len(opt.PrimaryKey) != 1 || opt.PrimaryKey[0] != "option_id" {
		t.Fatalf("primary key %v", opt.PrimaryKey)
	}
	name := opt.Columns[opt.Col("option_name")]
	if !name.Unique || name.MaxLen != 191*4 || name.Nullable || name.Type != "varchar(191)" {
		t.Fatalf("option_name %+v", name)
	}
	if v := opt.Columns[opt.Col("option_value")]; v.Unique || !v.Nullable || v.MaxLen != 0 {
		t.Fatalf("option_value %+v", v)
	}
	u := s.Table("wp_users")
	if a := u.Columns[u.Col("avatar")]; !a.Binary {
		t.Fatalf("avatar %+v", a)
	}
	calc := s.Table("calc")
	if b := calc.Columns[calc.Col("b")]; !b.Generated {
		t.Fatalf("calc.b %+v", b)
	}
	if a := calc.Columns[calc.Col("a")]; a.Generated {
		t.Fatalf("calc.a %+v", a)
	}
	if n := s.Table("notes"); len(n.PrimaryKey) != 0 {
		t.Fatalf("notes key %v", n.PrimaryKey)
	}
	id, err := c.Identity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if id.Engine != core.SQLite || id.Prefix != "wp_" || !filepath.IsAbs(id.Name) || id.Host != "" {
		t.Fatalf("identity %+v", id)
	}
}

// A read-only source can't write, through the session or a sink.
func TestSQLiteReadOnly(t *testing.T) {
	p := newSQLite(t, 3)
	c := openT(t, p, ReadOnly)
	if _, err := c.conn.ExecContext(context.Background(), `INSERT INTO wp_options VALUES (9, 'x', 'y')`); err == nil {
		t.Fatal("an INSERT through the read-only session worked")
	}
	if _, err := c.Sink(context.Background(), SinkOptions{}); !errors.Is(err, core.ErrReadOnly) {
		t.Fatalf("Sink on a read-only connection: %v", err)
	}
	// Negative: the same INSERT works on a read-write connection.
	w := openT(t, p, ReadWrite)
	if _, err := w.conn.ExecContext(context.Background(), `INSERT INTO wp_options VALUES (9, 'x', 'y')`); err != nil {
		t.Fatalf("read-write insert: %v", err)
	}
}

// A missing file is an error, never a new empty database.
func TestSQLiteMissingFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "nope.sqlite")
	for _, m := range []Mode{ReadOnly, ReadWrite} {
		if c, err := Open(context.Background(), sqliteEP(p), m); err == nil {
			_ = c.Close()
			t.Fatalf("mode %d opened a missing file", m)
		}
	}
}

func drain(t *testing.T, src core.Source) []*core.Batch {
	t.Helper()
	var out []*core.Batch
	for {
		b, err := src.Next(context.Background())
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, b)
	}
}

func TestSQLiteSource(t *testing.T) {
	c := openT(t, newSQLite(t, usersRows), ReadOnly)
	src, err := c.Source(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Source(context.Background(), nil); !errors.Is(err, errBusy) {
		t.Fatalf("a second source on a busy session: %v", err)
	}
	batches := drain(t, src)
	counts := map[string]int64{}
	var seq uint64
	creates := 0
	for _, b := range batches {
		if b.Seq != seq+1 {
			t.Fatalf("seq %d after %d", b.Seq, seq)
		}
		seq = b.Seq
		if b.Pass != nil {
			if b.Pass.Kind == core.StmtCreateTable {
				creates++
			}
			continue
		}
		if !b.First || !b.Last || !strings.HasPrefix(string(b.Head), `INSERT INTO "`) || string(b.Tail) != ";\n" {
			t.Fatalf("batch shape %q %q", b.Head, b.Tail)
		}
		for i, r := range b.Rows {
			if r.Src != nil || (i == 0) != (r.Lead == nil) {
				t.Fatalf("row %d of %s: Src %q Lead %q", i, b.Table.Name, r.Src, r.Lead)
			}
			if len(r.Values) != len(b.Columns) {
				t.Fatalf("%s: %d values for %d columns", b.Table.Name, len(r.Values), len(b.Columns))
			}
		}
		counts[b.Table.Name] += int64(len(b.Rows))
	}
	if creates != 5 {
		t.Fatalf("%d CREATE TABLE statements", creates)
	}
	want := map[string]int64{"calc": 3, "notes": 3, "wp_2_options": 1, "wp_options": 2, "wp_users": usersRows}
	for k, v := range want {
		if counts[k] != v {
			t.Fatalf("%s: %d rows, want %d", k, counts[k], v)
		}
	}
	// The fingerprint is SHA-256 of the sorted "table\trows\n" lines.
	h := sha256.Sum256([]byte(fmt.Sprintf("calc\t3\nnotes\t3\nwp_2_options\t1\nwp_options\t2\nwp_users\t%d\n", usersRows)))
	if fp := src.Fingerprint(); fp.Kind != "db-tables" || fp.Value != hex.EncodeToString(h[:]) || fp.Bytes == 0 {
		t.Fatalf("fingerprint %+v", fp)
	}
	if err := src.Close(); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(mustErr(src.Next(context.Background())), io.EOF) {
		t.Fatal("Next after EOF")
	}
}

func mustErr(_ *core.Batch, err error) error { return err }

// Values keep their types: numbers, text (DATETIME stays text), blobs
// and NULL; generated columns are left out.
func TestSQLiteSourceValues(t *testing.T) {
	c := openT(t, newSQLite(t, 3), ReadOnly)
	src, err := c.Source(context.Background(), []string{"wp_users", "calc", "notes"})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]core.Row{}
	for _, b := range drain(t, src) {
		if b.Table != nil {
			got[b.Table.Name] = append(got[b.Table.Name], b.Rows...)
		}
	}
	u := got["wp_users"][2].Values
	if u[0].Kind != core.Number || string(u[0].Raw) != "3" {
		t.Fatalf("ID %+v", u[0])
	}
	if u[1].Kind != core.String || string(u[1].Raw) != "customer-00003@example.invalid" {
		t.Fatalf("email %+v", u[1])
	}
	if u[2].Kind != core.String || string(u[2].Raw) != "2026-10-26 12:00:00" {
		t.Fatalf("created %+v", u[2])
	}
	if u[3].Kind != core.BinaryString || string(u[3].Raw) != "\x00\x03\xff" {
		t.Fatalf("avatar %+v", u[3])
	}
	if u[4].Kind != core.Number || string(u[4].Raw) != "0.75" {
		t.Fatalf("score %+v", u[4])
	}
	if v := got["wp_users"][0].Values[3]; v.Kind != core.Null {
		t.Fatalf("NULL avatar %+v", v)
	}
	if r := got["calc"][0]; len(r.Values) != 1 {
		t.Fatalf("calc row has %d values; the generated column must be left out", len(r.Values))
	}
	n := got["notes"]
	if string(n[0].Values[0].Raw) != "line one\nline two" || n[2].Values[0].Kind != core.Null {
		t.Fatalf("notes %+v", n)
	}
	if _, err := c.Source(context.Background(), []string{"wp_missing"}); err == nil {
		t.Fatal("an unknown table was accepted")
	}
}

// tableSums is every table's rows in key order as text, for comparing
// two databases.
func tableSums(t *testing.T, p string) map[string]string {
	t.Helper()
	db, err := sql.Open("sqlite", p)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	out := map[string]string{}
	for _, tb := range []string{"wp_options", "wp_users", "wp_2_options", "notes", "calc"} {
		rows, err := db.Query(`SELECT * FROM "` + tb + `" ORDER BY 1, 2`)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		h := sha256.New()
		n := 0
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(h, "%#v\n", vals)
			n++
		}
		rows.Close()
		out[tb] = fmt.Sprintf("%d:%x", n, h.Sum(nil))
	}
	return out
}

func copyDB(ctx context.Context, src core.Source, sk core.Sink) error {
	for {
		b, err := src.Next(ctx)
		if errors.Is(err, io.EOF) {
			return sk.Commit(ctx)
		}
		if err == nil {
			err = sk.Write(ctx, b)
		}
		if err != nil {
			_ = sk.Abort(ctx)
			return err
		}
	}
}

func TestSQLiteSinkRoundTrip(t *testing.T) {
	ctx := context.Background()
	from := newSQLite(t, usersRows)
	to := emptySQLite(t)
	rc := openT(t, from, ReadOnly)
	wc := openT(t, to, ReadWrite)
	src, err := rc.Source(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	sk, err := wc.Sink(ctx, SinkOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if sk.State() != core.Untouched {
		t.Fatalf("state before writing %s", sk.State())
	}
	if err := copyDB(ctx, src, sk); err != nil {
		t.Fatal(err)
	}
	if sk.State() != core.Replaced {
		t.Fatalf("state after Commit %s", sk.State())
	}
	if a, b := tableSums(t, from), tableSums(t, to); fmt.Sprint(a) != fmt.Sprint(b) {
		t.Fatalf("tables differ:\n%v\n%v", a, b)
	}
	// A second run over the copy replaces it again with the same rows.
	src2, _ := openT(t, from, ReadOnly).Source(ctx, nil)
	sk2, err := openT(t, to, ReadWrite).Sink(ctx, SinkOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := copyDB(ctx, src2, sk2); err != nil {
		t.Fatal(err)
	}
	if a, b := tableSums(t, from), tableSums(t, to); fmt.Sprint(a) != fmt.Sprint(b) {
		t.Fatal("tables differ after the second run")
	}
}

// A changed value is written; a dropped row is left out.
func TestSQLiteSinkChangedRows(t *testing.T) {
	ctx := context.Background()
	from := newSQLite(t, 5)
	to := emptySQLite(t)
	src, _ := openT(t, from, ReadOnly).Source(ctx, []string{"wp_users"})
	sk, err := openT(t, to, ReadWrite).Sink(ctx, SinkOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for {
		b, err := src.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if b.Table != nil {
			b.Rows[0].Values[1] = core.Value{Kind: core.String, Raw: []byte("'); DROP TABLE wp_users; --"), Dirty: true}
			b.Rows[1].Drop = true
		}
		if err := sk.Write(ctx, b); err != nil {
			t.Fatal(err)
		}
	}
	if err := sk.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	db, _ := sql.Open("sqlite", to)
	defer db.Close()
	var n int
	var email string
	if err := db.QueryRow(`SELECT COUNT(*) FROM wp_users`).Scan(&n); err != nil || n != 4 {
		t.Fatalf("rows %d %v", n, err)
	}
	if err := db.QueryRow(`SELECT user_email FROM wp_users WHERE ID = 1`).Scan(&email); err != nil || email != "'); DROP TABLE wp_users; --" {
		t.Fatalf("email %q %v", email, err)
	}
}

// --only: a sink limited to some tables leaves the others alone.
func TestSQLiteSinkOnlyTables(t *testing.T) {
	ctx := context.Background()
	from := newSQLite(t, 5)
	to := newSQLite(t, 2)
	src, _ := openT(t, from, ReadOnly).Source(ctx, nil)
	sk, err := openT(t, to, ReadWrite).Sink(ctx, SinkOptions{Tables: []string{"wp_options"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := copyDB(ctx, src, sk); err != nil {
		t.Fatal(err)
	}
	db, _ := sql.Open("sqlite", to)
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM wp_users`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("wp_users has %d rows; it isn't in --only", n)
	}
}

// Rows of a table with no CREATE TABLE in the stream replace the rows of
// the table as it is.
func TestSQLiteSinkDataOnly(t *testing.T) {
	ctx := context.Background()
	from := newSQLite(t, 7)
	to := newSQLite(t, 2)
	src, _ := openT(t, from, ReadOnly).Source(ctx, []string{"wp_users"})
	sk, err := openT(t, to, ReadWrite).Sink(ctx, SinkOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for {
		b, err := src.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if b.Pass != nil && (b.Pass.Kind == core.StmtCreateTable || b.Pass.Kind == core.StmtDropTable) {
			continue
		}
		if err := sk.Write(ctx, b); err != nil {
			t.Fatal(err)
		}
	}
	if err := sk.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	db, _ := sql.Open("sqlite", to)
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM wp_users`).Scan(&n); err != nil || n != 7 {
		t.Fatalf("wp_users has %d rows, want 7", n)
	}
}

// A connection lost after two of three tables leaves the target mixed
// and says so; Abort keeps that state.
func TestSQLiteSinkLostConnection(t *testing.T) {
	ctx := context.Background()
	from := newSQLite(t, 5)
	to := emptySQLite(t)
	src, _ := openT(t, from, ReadOnly).Source(ctx, []string{"wp_options", "wp_2_options", "wp_users"})
	wc := openT(t, to, ReadWrite)
	sk, err := wc.Sink(ctx, SinkOptions{Total: 3})
	if err != nil {
		t.Fatal(err)
	}
	s := sk.(*sink)
	var werr error
	for werr == nil {
		b, err := src.Next(ctx)
		if err != nil {
			t.Fatalf("source ended first: %v", err)
		}
		if s.st.committed == 2 && b.Pass != nil && b.Pass.Kind == core.StmtCreateTable {
			_ = wc.conn.Close() // the server goes away
		}
		werr = sk.Write(ctx, b)
	}
	var ce *core.Error
	if !errors.As(werr, &ce) || ce.Code != core.ErrImportMixed || ce.Exit != 3 || ce.State != core.Mixed {
		t.Fatalf("error %#v", werr)
	}
	want := "import: the connection to staging.sqlite was lost after 2 of 3 tables. The target is in a mixed state: some tables are pseudonymised, the rest still hold the earlier copy. Run the same command again."
	if ce.Msg != want {
		t.Fatalf("message\n%s\nwant\n%s", ce.Msg, want)
	}
	if err := sk.Abort(ctx); err != nil {
		t.Fatal(err)
	}
	if sk.State() != core.Mixed {
		t.Fatalf("state %s", sk.State())
	}
}

// A failure before any table was replaced leaves the target untouched.
func TestSQLiteSinkAbortUntouched(t *testing.T) {
	ctx := context.Background()
	to := emptySQLite(t)
	sk, err := openT(t, to, ReadWrite).Sink(ctx, SinkOptions{})
	if err != nil {
		t.Fatal(err)
	}
	bad := &core.Batch{Seq: 1, Pass: &core.Statement{Kind: core.StmtCreateTable, Text: []byte("CREATE TABLE wp_x (a INTEGER, PRIMARY KEY (missing));\n"), Line: 4}}
	err = sk.Write(ctx, bad)
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Code != core.ErrImport || ce.State != core.Untouched || !strings.HasSuffix(ce.Msg, "The target is untouched.") {
		t.Fatalf("error %#v", err)
	}
	if !strings.HasPrefix(ce.Msg, "import: table wp_x: ") {
		t.Fatalf("message %q", ce.Msg)
	}
	_ = sk.Abort(ctx)
	if sk.State() != core.Untouched {
		t.Fatalf("state %s", sk.State())
	}
	db, _ := sql.Open("sqlite", to)
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name = 'wp_x'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("the failed table is there: %d %v", n, err)
	}
}

// Canceling stops the source with the context's error.
func TestSQLiteSourceCancel(t *testing.T) {
	c := openT(t, newSQLite(t, 50), ReadOnly)
	src, err := c.Source(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if _, err := src.Next(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err := src.Next(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("after cancel: %v", err)
	}
}

// A mysql client DELIMITER line is never sent to the database, and the
// statements it covers reach it without the custom delimiter.
func TestSinkDelimiterNeverRuns(t *testing.T) {
	ctx := context.Background()
	to := emptySQLite(t)
	sk, err := openT(t, to, ReadWrite).Sink(ctx, SinkOptions{})
	if err != nil {
		t.Fatal(err)
	}
	stmts := []*core.Statement{
		{Kind: mysql.KindDelimiter, Text: []byte("DELIMITER $$\n")},
		{Kind: core.StmtOther, Text: []byte("CREATE VIEW v AS SELECT 1 AS a$$\n")},
		{Kind: mysql.KindDelimiter, Text: []byte("DELIMITER ;\n")},
		{Kind: core.StmtOther, Text: []byte("CREATE VIEW w AS SELECT 2 AS a;\n")},
		{Kind: mysql.KindClient, Text: []byte("\\! id\n")},
	}
	for i, st := range stmts {
		if err := sk.Write(ctx, &core.Batch{Seq: uint64(i + 1), Pass: st}); err != nil {
			t.Fatalf("statement %d: %v", i+1, err)
		}
	}
	if err := sk.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	conn, err := sql.Open("sqlite", to)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var n int
	if err := conn.QueryRow("SELECT (SELECT a FROM v) + (SELECT a FROM w)").Scan(&n); err != nil || n != 3 {
		t.Fatalf("views %d %v", n, err)
	}
}
