// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package pgsql

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
)

func fixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name)) // #nosec G304 -- test fixture
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// drain reads every batch of a dump.
func drain(t testing.TB, in []byte, o SourceOptions) ([]*core.Batch, *Source, error) {
	t.Helper()
	if o.Name == "" {
		o.Name = "app.sql"
	}
	s := NewSource(bytes.NewReader(in), o)
	var out []*core.Batch
	for {
		b, err := s.Next(context.Background())
		if errors.Is(err, io.EOF) {
			return out, s, nil
		}
		if err != nil {
			return out, s, err
		}
		out = append(out, b)
	}
}

// write runs batches through the Writer.
func write(t testing.TB, bs []*core.Batch) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := NewWriter(&buf)
	for _, b := range bs {
		if err := w.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Finish(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

var fixtures = []string{"pg16-prisma.sql", "pg16-inserts.sql", "pg16-column-inserts.sql"}

// Every fixture reads and writes back byte for byte.
func TestRoundTripFixtures(t *testing.T) {
	for _, name := range fixtures {
		in := fixture(t, name)
		bs, _, err := drain(t, in, SourceOptions{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if out := write(t, bs); !bytes.Equal(out, in) {
			t.Errorf("%s: round trip differs", name)
		}
	}
}

// rowsOf collects the rows of table name.
func rowsOf(bs []*core.Batch, name string) []core.Row {
	var out []core.Row
	for _, b := range bs {
		if b.Table != nil && b.Table.Name == name {
			out = append(out, b.Rows...)
		}
	}
	return out
}

// val is value i of row r as a string; "<NULL>" for Null.
func val(r core.Row, i int) string {
	if r.Values[i].Kind == core.Null {
		return "<NULL>"
	}
	return string(r.Values[i].Raw)
}

// COPY, --inserts and --column-inserts of one database decode to the
// same values: tabs, newlines, backslashes, quotes, \N against an empty
// string and bytea text.
func TestValuesAgree(t *testing.T) {
	want := map[string][]string{
		"u1": {"u1", "ada@example.com", "Ada Example", "ADMIN", "line one\nline two\ttabbed", "2024-01-02 03:04:05.678"},
		"u2": {"u2", "bob@example.test", "", "USER", `back\slash and \N literal`, "2024-02-03 04:05:06"},
		"u3": {"u3", "cy@example.com", "<NULL>", "USER", "<NULL>", "2024-03-04 05:06:07"},
		"u4": {"u4", "dee@example.com", "Dée Ünïcode ✓", "USER", "carriage\rreturn and quote ' here", "2024-04-05 06:07:08"},
	}
	for _, name := range fixtures {
		bs, _, err := drain(t, fixture(t, name), SourceOptions{})
		if err != nil {
			t.Fatal(err)
		}
		users := rowsOf(bs, "User")
		if len(users) != 4 {
			t.Fatalf("%s: %d users", name, len(users))
		}
		for _, r := range users {
			w := want[val(r, 0)]
			for i := range w {
				if got := val(r, i); got != w[i] {
					t.Errorf("%s: %s value %d = %q, want %q", name, w[0], i, got, w[i])
				}
			}
		}
		sess := rowsOf(bs, "Session")
		if len(sess) != 2 || val(sess[0], 2) != "\x00\x01\x02\x03\xff\\\t" || sess[0].Values[2].Kind != core.BinaryString ||
			sess[1].Values[2].Kind != core.BinaryString || val(sess[1], 2) != "" || val(sess[1], 3) != "<NULL>" {
			t.Errorf("%s: sessions %v", name, sess)
		}
		posts := rowsOf(bs, "Post")
		if len(posts) != 3 || val(posts[1], 2) != "tab\there\nnew line" || val(posts[2], 1) != "$tag$ dollars" {
			t.Errorf("%s: posts %v", name, posts)
		}
	}
}

func TestSchemaFromDump(t *testing.T) {
	_, s, err := drain(t, fixture(t, "pg16-prisma.sql"), SourceOptions{Server: "16.14"})
	if err != nil {
		t.Fatal(err)
	}
	sc := s.Schema()
	if sc.Dialect != core.Postgres || sc.Server != "16.14" || len(sc.Tables) != 5 {
		t.Fatalf("schema %+v", sc)
	}
	u := sc.Table("User")
	if u == nil || len(u.Columns) != 6 {
		t.Fatalf("User %+v", u)
	}
	email, name, created := u.Columns[u.Col("email")], u.Columns[u.Col("name")], u.Columns[u.Col("createdAt")]
	if !email.Unique || email.Nullable || email.Type != "text" || email.MaxLen != 0 {
		t.Errorf("email %+v", email)
	}
	if name.Type != "character varying(191)" || name.MaxLen != 191*4 || !name.Nullable || name.Unique {
		t.Errorf("name %+v", name)
	}
	if created.Type != "timestamp(3) without time zone" || created.Nullable {
		t.Errorf("createdAt %+v", created)
	}
	if len(u.PrimaryKey) != 1 || u.PrimaryKey[0] != "id" || !u.Columns[0].Unique {
		t.Errorf("User key %v", u.PrimaryKey)
	}
	p := sc.Table("Post")
	if slug := p.Columns[p.Col("slug")]; !slug.Generated {
		t.Errorf("slug %+v", slug)
	}
	if p.Columns[p.Col("authorId")].Unique {
		t.Error("a foreign key made authorId unique")
	}
	sess := sc.Table("Session")
	if tok := sess.Columns[sess.Col("token")]; !tok.Binary || !tok.Unique {
		t.Errorf("token %+v", tok)
	}
	if mig := sc.Table("_prisma_migrations"); mig.Columns[0].MaxLen != 36*4 {
		t.Errorf("migrations %+v", mig.Columns[0])
	}
}

// Statements split where psql splits them: not on a ';' in a string, an
// identifier, a dollar-quoted body or a comment.
func TestStatements(t *testing.T) {
	in := "SET standard_conforming_strings = on;\n" +
		"CREATE FUNCTION f() RETURNS text LANGUAGE sql AS $fn$ SELECT 'a;b' || $q$;$q$ $fn$;\n" +
		"CREATE FUNCTION g(int) RETURNS int LANGUAGE sql AS $$ SELECT $1; $$;\n" +
		"COMMENT ON TABLE \"we;ird\" IS 'it''s; fine';\n" +
		"/* a /* nested; */ comment */\n" +
		"SELECT E'\\';' AS x; -- trailing; comment\n" +
		"\\unrestrict abc\n"
	bs, _, err := drain(t, []byte(in), SourceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// The comment after the SELECT's ';' is a statement of its own.
	kinds := []core.StmtKind{core.StmtSet, core.StmtRoutine, core.StmtRoutine, core.StmtCommentOn, core.StmtComment, core.StmtOther, core.StmtComment, KindMeta}
	lines := []int64{1, 2, 3, 4, 5, 6, 6, 7}
	if len(bs) != len(kinds) {
		for _, b := range bs {
			t.Logf("%s %q", b.Pass.Kind, b.Pass.Text)
		}
		t.Fatalf("%d statements, want %d", len(bs), len(kinds))
	}
	for i, k := range kinds {
		if bs[i].Pass.Kind != k || bs[i].Pass.Line != lines[i] {
			t.Errorf("statement %d: %s at line %d, want %s", i, bs[i].Pass.Kind, bs[i].Pass.Line, k)
		}
	}
	if out := write(t, bs); string(out) != in {
		t.Error("round trip differs")
	}
}

// A backslash outside quotes anywhere in a statement makes it a psql
// meta-command, which the allowlist refuses.
func TestMetaCommands(t *testing.T) {
	for _, in := range []string{"\\! touch /tmp/pwned\n", "\\connect other\n", "SELECT 1 \\gexec\n;", "CREATE TABLE t (a int)\n\\! id\n;"} {
		bs, _, err := drain(t, []byte(in), SourceOptions{})
		if err != nil || len(bs) != 1 || bs[0].Pass.Kind != KindMeta {
			t.Errorf("%q: %v %v", in, bs, err)
		}
	}
	bs, _, err := drain(t, []byte("SELECT 'a\\b', \"c\\d\";\n"), SourceOptions{})
	if err != nil || bs[0].Pass.Kind == KindMeta {
		t.Errorf("backslash in quotes: %v %v", bs, err)
	}
}

// standard_conforming_strings = off makes backslashes escapes in '...'.
func TestNoStandardStrings(t *testing.T) {
	in := "SET standard_conforming_strings = off;\nCREATE TABLE t (a text);\nINSERT INTO t VALUES ('it\\'s; a\\\\b');\n"
	bs, s, err := drain(t, []byte(in), SourceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if r := rowsOf(bs, "t"); len(r) != 1 || val(r[0], 0) != `it's; a\b` {
		t.Fatalf("rows %v", r)
	}
	if !s.Lexer().Mode().NoStdStrings {
		t.Error("mode not followed")
	}
	if out := write(t, bs); string(out) != in {
		t.Error("round trip differs")
	}
	for _, c := range []struct {
		sql string
		off bool
	}{
		{"SET standard_conforming_strings TO 'off';", true},
		{"SELECT pg_catalog.set_config('standard_conforming_strings', 'off', false);", true},
		{"RESET standard_conforming_strings;", false},
		{"SET search_path = off;", false},
	} {
		if got := SetMode([]byte(c.sql), Mode{NoStdStrings: !c.off && c.sql[0] == 'R'}).NoStdStrings; got != c.off {
			t.Errorf("%s: %v", c.sql, got)
		}
	}
}

func TestCopyDecode(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{`plain`, "plain"},
		{`a\tb\nc\rd\\e`, "a\tb\nc\rd\\e"},
		{`\b\f\v`, "\b\f\v"},
		{`\101\1012\7`, "AA2\a"},
		{`\x41\x4G\xg`, "A\x04Gxg"},
		{`\q\.`, "q."},
		{`end\`, `end\`},
	} {
		if got := string(AppendCopyUnescape(nil, []byte(c.in))); got != c.want {
			t.Errorf("%q: %q, want %q", c.in, got, c.want)
		}
	}
}

// COPY lines with CRLF ends, a table with no columns and an empty COPY.
func TestCopyShapes(t *testing.T) {
	in := "CREATE TABLE t (a text, b text);\nCOPY t (a, b) FROM stdin;\r\nx\ty\r\n\\N\t\r\n\\.\r\n" +
		"CREATE TABLE n ();\nCOPY n  FROM stdin;\n\n\\.\n" +
		"COPY t (a, b) FROM stdin;\n\\.\n"
	bs, _, err := drain(t, []byte(in), SourceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	r := rowsOf(bs, "t")
	if len(r) != 2 || val(r[0], 1) != "y" || val(r[1], 0) != "<NULL>" || val(r[1], 1) != "" {
		t.Fatalf("rows %v", r)
	}
	if n := rowsOf(bs, "n"); len(n) != 1 || len(n[0].Values) != 0 {
		t.Errorf("no-column rows %v", n)
	}
	if out := write(t, bs); string(out) != in {
		t.Errorf("round trip differs:\n%q", out)
	}
}

// Changed values are encoded and read back as the same data; a value
// that looks like SQL stays data.
func TestDirtyRoundTrip(t *testing.T) {
	evil := "'); DROP TABLE users; --"
	values := []string{evil, "tab\tnew\nline\rcr\\back", "", `\N`, "\\.", "Ünï ✓", "$x$ ; $$"}
	for _, name := range fixtures {
		bs, _, err := drain(t, fixture(t, name), SourceOptions{})
		if err != nil {
			t.Fatal(err)
		}
		k := 0
		for _, b := range bs {
			if b.Table == nil || b.Table.Name != "User" {
				continue
			}
			for i := range b.Rows {
				v := &b.Rows[i].Values[4]
				*v = core.Value{Kind: core.String, Raw: []byte(values[k%len(values)]), Dirty: true}
				b.Rows[i].Values[2] = core.Value{Kind: core.Null, Dirty: true}
				k++
			}
		}
		out := write(t, bs)
		again, _, err := drain(t, out, SourceOptions{})
		if err != nil {
			t.Fatalf("%s: reread: %v\n%s", name, err, out)
		}
		users := rowsOf(again, "User")
		for i, r := range users {
			if val(r, 4) != values[i%len(values)] || val(r, 2) != "<NULL>" {
				t.Errorf("%s: row %d: %q", name, i, val(r, 4))
			}
		}
		if len(rowsOf(again, "Post")) != 3 {
			t.Errorf("%s: posts lost", name)
		}
	}
}

// Dropping rows keeps every statement valid: the first, the middle, the
// last and all rows of a multi-row INSERT; a COPY keeps its head and end
// line with every row dropped.
func TestDropRows(t *testing.T) {
	in := fixture(t, "pg16-column-inserts.sql")
	for _, drop := range [][]int{{0}, {1}, {0, 1}} {
		bs, _, err := drain(t, in, SourceOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range bs {
			if b.Table != nil && b.Table.Name == "User" {
				for _, i := range drop {
					if i < len(b.Rows) {
						b.Rows[i].Drop = true
					}
				}
			}
		}
		out := write(t, bs)
		again, _, err := drain(t, out, SourceOptions{})
		if err != nil {
			t.Fatalf("drop %v: %v", drop, err)
		}
		if got, want := len(rowsOf(again, "User")), 4-2*len(drop); got != want {
			t.Errorf("drop %v: %d users, want %d", drop, got, want)
		}
		if len(drop) == 2 && bytes.Contains(out, []byte(`INSERT INTO public."User"`)) {
			t.Error("an INSERT with every row dropped was written")
		}
	}
	bs, _, err := drain(t, fixture(t, "pg16-prisma.sql"), SourceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range bs {
		for i := range b.Rows {
			b.Rows[i].Drop = true
		}
	}
	out := write(t, bs)
	if !bytes.Contains(out, []byte("COPY public.\"User\" (id, email, name, role, bio, \"createdAt\") FROM stdin;\n\\.\n")) {
		t.Error("an emptied COPY lost its head or end line")
	}
	if again, _, err := drain(t, out, SourceOptions{}); err != nil || len(rowsOf(again, "User")) != 0 {
		t.Errorf("reread: %v", err)
	}
}

// A big COPY is cut into batches near MaxBatchBytes and still writes
// back byte for byte.
func TestCopyBatches(t *testing.T) {
	var in bytes.Buffer
	in.WriteString("CREATE TABLE t (id int, v text);\nCOPY t (id, v) FROM stdin;\n")
	line := bytes.Repeat([]byte("x"), 1000)
	for i := range 10000 {
		in.WriteString(string(rune('0' + i%10)))
		in.WriteByte('\t')
		in.Write(line)
		in.WriteByte('\n')
	}
	in.WriteString("\\.\n")
	bs, _, err := drain(t, in.Bytes(), SourceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var data []*core.Batch
	for _, b := range bs {
		if b.Table != nil {
			data = append(data, b)
		}
	}
	if len(data) < 2 || !data[0].First || data[0].Last || !data[len(data)-1].Last {
		t.Fatalf("%d batches", len(data))
	}
	n := 0
	for i, b := range data {
		n += len(b.Rows)
		if i > 0 && b.First || len(b.Rows) == 0 {
			t.Errorf("batch %d: first %v, %d rows", i, b.First, len(b.Rows))
		}
	}
	if n != 10000 {
		t.Errorf("%d rows", n)
	}
	for i := 1; i < len(bs); i++ {
		if bs[i].Seq != bs[i-1].Seq+1 {
			t.Fatal("Seq not increasing by 1")
		}
	}
	if !bytes.Equal(write(t, bs), in.Bytes()) {
		t.Error("round trip differs")
	}
}

func wantCode(t *testing.T, err error, code string, exit int) *core.Error {
	t.Helper()
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Code != code || ce.Exit != exit {
		t.Fatalf("err = %v, want %s", err, code)
	}
	if ce.Line == 0 {
		t.Errorf("%s without a line", code)
	}
	return ce
}

func TestTruncated(t *testing.T) {
	for _, c := range []struct{ in, msg string }{
		{"CREATE TABLE t (a text);\nCOPY t (a) FROM stdin;\nx\n", "dump-truncated: app.sql ends inside table t at 50 B, with no \"PostgreSQL database dump complete\" line. Export it again; the target is untouched."},
		{"CREATE TABLE t (a text);\nCOPY t (a) FROM stdin;\nx", ""},
		{"SELECT 'open;\n", "dump-truncated: app.sql ends inside a statement at line 1 at 14 B"},
		{"CREATE FUNCTION f() AS $$ x;\n", ""},
		{"/* open\n", ""},
		{"CREATE TABLE t (a int)", ""},
	} {
		_, _, err := drain(t, []byte(c.in), SourceOptions{})
		ce := wantCode(t, err, core.ErrDumpTruncated, 3)
		if c.msg != "" && !bytes.HasPrefix([]byte(ce.Msg), []byte(c.msg)) {
			t.Errorf("%q: %s", c.in, ce.Msg)
		}
	}
	_, _, err := drain(t, []byte("SELECT 'x"), SourceOptions{NoTrailer: true})
	if ce := wantCode(t, err, core.ErrDumpTruncated, 3); !bytes.Contains([]byte(ce.Msg), []byte("in the middle of a statement")) {
		t.Errorf("no-trailer wording: %s", ce.Msg)
	}
}

// Function calls and queries in row values are refused; literals,
// casts and keywords are data.
func TestRowCalls(t *testing.T) {
	head := "CREATE TABLE t (a text, b text);\nINSERT INTO t VALUES ("
	for _, v := range []string{
		"pg_read_file('/etc/passwd')", "'x' || pg_read_file('/etc/passwd')", "(SELECT 1)",
		"lo_import('/etc/passwd')", "public.f(1)", "'x'::text and pg_sleep(10)", "\"f\"(1)",
		"'x'::text || pg_sleep(1)", "(('/etc/passwd'::text)).pg_read_file", "(ROW(1)).f", "U&\"\\0070g_read_file\"('/x')",
		"pg_read_file /* c */ ('/x')",
	} {
		_, _, err := drain(t, []byte(head+"'a', "+v+");\n"), SourceOptions{})
		ce := wantCode(t, err, core.ErrDumpRefused, 3)
		if want := "dump-refused: app.sql line 2 holds a function call in a row of t, which could run code on the staging database."; !bytes.HasPrefix([]byte(ce.Msg), []byte(want)) {
			t.Errorf("%s: %s", v, ce.Msg)
		}
	}
	for _, v := range []string{
		"DEFAULT", "true", "'x'::character varying(10)", "'1.5'::numeric(10,2)", "'2024-01-01'::timestamp(3) without time zone",
		"'{1,2}'::integer[]", "ARRAY['a', 'b']", "'USER'::public.\"Role\"", "-1", "B'0101'", "X'1F'", "E'a\\nb'", "'NaN'::double precision",
		"NULL::pg_catalog.text", "'1.5'", "1.5", ".5", "'x'::text COLLATE \"C\"",
	} {
		bs, _, err := drain(t, []byte(head+"'a', "+v+");\n"), SourceOptions{})
		if err != nil {
			t.Errorf("%s: %v", v, err)
			continue
		}
		if r := rowsOf(bs, "t"); len(r) != 1 || len(r[0].Values) != 2 {
			t.Errorf("%s: rows %v", v, r)
		}
	}
}

func TestFormatErrors(t *testing.T) {
	for _, c := range []struct{ in, msg string }{
		{"INSERT INTO wp_x VALUES (1);\n", "dump-format: app.sql: table wp_x has no CREATE TABLE before line 1; pass --schema."},
		{"CREATE TABLE t (a int);\nCOPY t (a, zz) FROM stdin;\n\\.\n", "dump-format: app.sql line 2: the rows of t name column zz, which its CREATE TABLE doesn't have."},
		{"CREATE TABLE t (a int, b int);\nCOPY t (a, b) FROM stdin;\n1\n\\.\n", "dump-format: app.sql line 3: a row of t has 1 values for 2 columns."},
		{"CREATE TABLE t (a int);\nINSERT INTO t VALUES (1, 2);\n", "dump-format: app.sql line 2: a row of t has 2 values for 1 columns."},
		{"CREATE TABLE t (a int);\nCOPY t (a) FROM stdin WITH (FORMAT csv);\n1\n\\.\n", "dump-format: app.sql line 2: a COPY ... FROM stdin with options; Roadie reads the text format pg_dump writes."},
		{"CREATE TABLE t (a int);\nCOPY t (a) FROM stdin; SELECT 1;\n", "dump-format: app.sql line 2: text follows COPY ... FROM stdin on its line."},
	} {
		_, _, err := drain(t, []byte(c.in), SourceOptions{})
		if ce := wantCode(t, err, core.ErrDumpFormat, 2); ce.Msg != c.msg {
			t.Errorf("%q:\n got %s\nwant %s", c.in, ce.Msg, c.msg)
		}
	}
	// --schema supplies the table of a data-only dump; a column list is
	// a schema of its own.
	sc := &core.Schema{Tables: []*core.Table{{Name: "wp_x", Columns: []core.Column{{Name: "id"}}}}}
	if _, _, err := drain(t, []byte("INSERT INTO wp_x VALUES (1);\n"), SourceOptions{Schema: sc}); err != nil {
		t.Error(err)
	}
	if _, s, err := drain(t, []byte("COPY public.y (a, b) FROM stdin;\n1\t2\n\\.\n"), SourceOptions{}); err != nil || len(s.Schema().Table("y").Columns) != 2 {
		t.Errorf("data-only COPY: %v", err)
	}
}

func TestTooLarge(t *testing.T) {
	big := string(bytes.Repeat([]byte("v"), 2000))
	for _, in := range []string{
		"CREATE TABLE t (a text);\nCOPY t (a) FROM stdin;\n" + big + "\n\\.\n",
		"CREATE TABLE t (a text);\nINSERT INTO t VALUES ('" + big + "');\n",
		"COMMENT ON TABLE t IS '" + big + "';\n",
		"/* " + big + " */\n",
	} {
		_, _, err := drain(t, []byte(in), SourceOptions{MaxValue: 1000})
		wantCode(t, err, core.ErrValueTooLarge, 3)
	}
}

// Other INSERT and COPY forms stay statements for the allowlist.
func TestNotRows(t *testing.T) {
	for _, in := range []string{
		"INSERT INTO t SELECT * FROM u;\n", "INSERT INTO t DEFAULT VALUES;\n",
		"INSERT INTO t VALUES (1) RETURNING id;\n", "INSERT INTO t VALUES (1) ON CONFLICT (id) DO UPDATE SET a = 1;\n",
		"COPY t FROM PROGRAM 'id';\n", "COPY t TO stdout;\n", "COPY (SELECT 1) TO stdout;\n", "COPY t FROM '/etc/passwd';\n",
	} {
		bs, _, err := drain(t, []byte(in), SourceOptions{})
		if err != nil || len(bs) != 1 || bs[0].Pass == nil {
			t.Errorf("%q: %v %v", in, bs, err)
		}
	}
	in := "CREATE TABLE t (id int);\nINSERT INTO t VALUES (1) ON CONFLICT DO NOTHING;\n"
	bs, _, err := drain(t, []byte(in), SourceOptions{})
	if err != nil || len(rowsOf(bs, "t")) != 1 || string(bs[1].Tail) != " ON CONFLICT DO NOTHING;\n" {
		t.Errorf("on conflict do nothing: %v", err)
	}
}

func TestCancel(t *testing.T) {
	s := NewSource(bytes.NewReader(fixture(t, "pg16-prisma.sql")), SourceOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	if _, err := s.Next(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err := s.Next(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
}

// A bytea column comes out as its bytes from either output format, so
// the rules and verify see what it holds; a changed row writes it back
// as bytea hex and an unchanged one keeps its source bytes.
func TestByteaDecoded(t *testing.T) {
	in := "--\n-- PostgreSQL database dump\n--\n\nSET standard_conforming_strings = on;\n\n" +
		"CREATE TABLE public.s (\n    id integer NOT NULL,\n    val bytea,\n    note text\n);\n\n" +
		"COPY public.s (id, val, note) FROM stdin;\n1\t\\\\x612e62\t\\\\x612e62\n2\ta\\\\\\\\b\\\\101\tz\n\\.\n\n" +
		"INSERT INTO public.s VALUES (3, '\\x6162', 'n');\n\n" +
		"--\n-- PostgreSQL database dump complete\n--\n\n"
	bs, _, err := drain(t, []byte(in), SourceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	rows := rowsOf(bs, "s")
	if len(rows) != 3 {
		t.Fatalf("%d rows", len(rows))
	}
	for i, want := range []string{"a.b", `a\bA`, "ab"} {
		if v := rows[i].Values[1]; v.Kind != core.BinaryString || string(v.Raw) != want {
			t.Errorf("row %d val = %d %q, want %q", i, v.Kind, v.Raw, want)
		}
	}
	if v := rows[0].Values[2]; v.Kind != core.String || string(v.Raw) != `\x612e62` {
		t.Errorf("a text column was decoded: %d %q", v.Kind, v.Raw)
	}
	out, err := AppendCopyField(nil, core.Value{Kind: core.BinaryString, Raw: []byte(`a\bA`), Dirty: true})
	if err != nil || string(out) != `\\x615c6241` {
		t.Errorf("COPY field %q %v", out, err)
	}
}
