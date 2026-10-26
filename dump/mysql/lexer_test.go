// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package mysql

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/RhythmShahriar/roadie/core"
)

// piece is one statement as lexAll saw it, with its rows when it had any.
type piece struct {
	st   *Stmt
	rows []core.Row
	tail []byte
	// the lexer state the statement started in, for re-lexing it alone
	delim []byte
	mode  Mode
}

// lexAll reads every statement and row of r.
func lexAll(t testing.TB, r io.Reader, o Options) ([]piece, *Lexer, error) {
	t.Helper()
	l := NewLexer(r, o)
	var out []piece
	for {
		delim, mode := l.delim, l.mode
		st, err := l.Next()
		if errors.Is(err, io.EOF) {
			return out, l, nil
		}
		if err != nil {
			return out, l, err
		}
		p := piece{st: st, delim: delim, mode: mode}
		if st.Rows {
			a := &Arena{}
			for {
				r, ok, err := l.Row(a)
				if err != nil {
					return out, l, err
				}
				if !ok {
					break
				}
				p.rows = append(p.rows, r)
			}
			if p.tail, err = l.Tail(); err != nil {
				return out, l, err
			}
		}
		out = append(out, p)
	}
}

// join concatenates the pieces back into text.
func join(ps []piece) []byte {
	var b bytes.Buffer
	for _, p := range ps {
		b.Write(p.st.Text)
		for _, r := range p.rows {
			b.Write(r.Lead)
			b.Write(r.Src)
		}
		b.Write(p.tail)
	}
	return b.Bytes()
}

func lexFile(t *testing.T, name string) ([]piece, *Lexer, []byte) {
	t.Helper()
	in, err := os.ReadFile(filepath.Join("testdata", filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	ps, l, err := lexAll(t, bytes.NewReader(in), Options{})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return ps, l, in
}

func fixtures(t *testing.T) []string {
	t.Helper()
	var names []string
	for _, glob := range []string{"testdata/*.sql", "testdata/crlf/*.sql"} {
		m, _ := filepath.Glob(glob)
		for _, f := range m {
			names = append(names, filepath.ToSlash(strings.TrimPrefix(f, "testdata"+string(filepath.Separator))))
		}
	}
	if len(names) < 9 {
		t.Fatalf("only %d fixtures", len(names))
	}
	return names
}

// Every fixture splits into statements and rows that join back to it and
// each statement read alone is the same single statement.
func TestFixturesJoin(t *testing.T) {
	for _, name := range fixtures(t) {
		ps, _, in := lexFile(t, name)
		if got := join(ps); !bytes.Equal(got, in) {
			t.Errorf("%s: joined text differs", name)
		}
		checkAlone(t, ps)
	}
}

// checkAlone re-lexes each statement on its own, in the state it started
// in and wants the same statement back: no boundary fell inside a quote.
func checkAlone(t testing.TB, ps []piece) {
	t.Helper()
	for _, p := range ps {
		var text []byte
		text = append(text, p.st.Text...)
		for _, r := range p.rows {
			text = append(append(text, r.Lead...), r.Src...)
		}
		text = append(text, p.tail...)
		l := NewLexer(bytes.NewReader(text), Options{})
		l.setDelim(p.delim)
		l.mode = p.mode
		st, err := l.Next()
		if err != nil {
			t.Fatalf("statement at line %d alone: %v", p.st.Line, err)
		}
		if st.Kind != p.st.Kind || st.Rows != p.st.Rows {
			t.Fatalf("statement at line %d alone: kind %s, was %s", p.st.Line, st.Kind, p.st.Kind)
		}
		if st.Rows {
			a := &Arena{}
			n := 0
			for {
				_, ok, err := l.Row(a)
				if err != nil {
					t.Fatalf("statement at line %d alone: row: %v", p.st.Line, err)
				}
				if !ok {
					break
				}
				n++
			}
			if n != len(p.rows) {
				t.Fatalf("statement at line %d alone: %d rows, was %d", p.st.Line, n, len(p.rows))
			}
			if _, err := l.Tail(); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := l.Next(); !errors.Is(err, io.EOF) {
			t.Fatalf("statement at line %d alone is more than one statement (%v)", p.st.Line, err)
		}
	}
}

// One byte at a time and in odd chunks, the result is the same: buffer
// refills never split a token wrongly.
func TestChunkedReads(t *testing.T) {
	for _, name := range fixtures(t) {
		in, _ := os.ReadFile(filepath.Join("testdata", filepath.FromSlash(name)))
		want, _, _ := lexAll(t, bytes.NewReader(in), Options{})
		for _, r := range []io.Reader{iotest.OneByteReader(bytes.NewReader(in)), iotest.HalfReader(bytes.NewReader(in)), iotest.DataErrReader(bytes.NewReader(in))} {
			got, _, err := lexAll(t, r, Options{})
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if !equalPieces(got, want) {
				t.Fatalf("%s: chunked reads give other pieces", name)
			}
		}
	}
}

func equalPieces(a, b []piece) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if x.st.Kind != y.st.Kind || x.st.Line != y.st.Line || !bytes.Equal(x.st.Text, y.st.Text) || !bytes.Equal(x.tail, y.tail) || len(x.rows) != len(y.rows) {
			return false
		}
		for j := range x.rows {
			if !bytes.Equal(x.rows[j].Src, y.rows[j].Src) || !bytes.Equal(x.rows[j].Lead, y.rows[j].Lead) || !equalValues(x.rows[j].Values, y.rows[j].Values) {
				return false
			}
		}
	}
	return true
}

func equalValues(a, b []core.Value) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Kind != b[i].Kind || !bytes.Equal(a[i].Raw, b[i].Raw) {
			return false
		}
	}
	return true
}

// values returns the raw values of every row of the n-th statement with rows.
func values(t *testing.T, ps []piece, n int) [][]string {
	t.Helper()
	for _, p := range ps {
		if !p.st.Rows {
			continue
		}
		if n--; n >= 0 {
			continue
		}
		var out [][]string
		for _, r := range p.rows {
			var row []string
			for _, v := range r.Values {
				if v.Kind == core.Null {
					row = append(row, "NULL")
					continue
				}
				row = append(row, string(v.Raw))
			}
			out = append(out, row)
		}
		return out
	}
	t.Fatalf("no statement %d with rows", n)
	return nil
}

func wantRows(t *testing.T, got [][]string, want ...[]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%d rows, want %d: %q", len(got), len(want), got)
	}
	for i := range want {
		if strings.Join(got[i], "|") != strings.Join(want[i], "|") {
			t.Errorf("row %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestMysqldump(t *testing.T) {
	ps, _, _ := lexFile(t, "mysqldump8.sql")
	wantRows(t, values(t, ps, 0),
		[]string{"1", "admin", "$P$Bfake.hash.only.for.tests0", "admin@example.test", "2024-01-02 03:04:05", "Site Admin"},
		[]string{"2", "test.user", "$P$Banother.fake.hash.value00", "test.user@example.test", "2024-02-03 04:05:06", "Test O'User; -- not a comment"},
		[]string{"3", "third", "$P$Bthird.fake.hash.value.0000", "third@example.test", "2024-03-04 05:06:07", "Line\nbreak \"quoted\" back\\slash"},
	)
	wantRows(t, values(t, ps, 1)[2:],
		[]string{"3", "widget_text", `a:1:{i:2;a:1:{s:5:"title";s:9:"Contact ;";}}`, "yes"})
	kinds := map[core.StmtKind]int{}
	for _, p := range ps {
		kinds[p.st.Kind]++
	}
	want := map[core.StmtKind]int{
		core.StmtComment: 18, core.StmtSet: 24, core.StmtDropTable: 2, core.StmtCreateTable: 2,
		core.StmtLock: 2, core.StmtUnlock: 2, core.StmtAlterKeys: 4, core.StmtInsert: 2,
	}
	for k, n := range want {
		if kinds[k] != n {
			t.Errorf("%d statements of kind %s, want %d (all: %v)", kinds[k], k, n, kinds)
		}
	}
	for _, p := range ps {
		if p.st.Kind == core.StmtCreateTable && p.st.Table != "wp_users" && p.st.Table != "wp_options" {
			t.Errorf("create table names %q", p.st.Table)
		}
		if p.st.Rows && (p.st.Line != 44 && p.st.Line != 72) {
			t.Errorf("insert into %s at line %d", p.st.Table, p.st.Line)
		}
	}
	first := ps[0]
	if first.st.Line != 1 || string(first.st.Text) != "-- MySQL dump 10.13  Distrib 8.0.36, for Linux (x86_64)\n" {
		t.Errorf("first statement %d %q", first.st.Line, first.st.Text)
	}
}

func TestHeadAndTail(t *testing.T) {
	ps, _, _ := lexFile(t, "complete-insert.sql")
	var heads, tails []string
	var cols [][]string
	for _, p := range ps {
		if p.st.Rows {
			heads = append(heads, string(p.st.Text))
			tails = append(tails, string(p.tail))
			cols = append(cols, p.st.Columns)
		}
	}
	wantHeads := []string{
		"INSERT INTO `wp_links` (`link_id`, `link_url`, `link_name`, `link_owner`) VALUES ",
		"INSERT IGNORE INTO `wp_links` (`link_name`, `link_id`) VALUES ",
		"REPLACE INTO `wp_links` VALUES ",
		"INSERT INTO `wp_links` VALUES ",
	}
	wantTails := []string{";\n", ";\n", ";\n", " ON DUPLICATE KEY UPDATE `link_name`=VALUES(`link_name`), `link_owner`=1;\n"}
	if strings.Join(heads, "\n") != strings.Join(wantHeads, "\n") || strings.Join(tails, "|") != strings.Join(wantTails, "|") {
		t.Errorf("heads %q\ntails %q", heads, tails)
	}
	if strings.Join(cols[1], ",") != "link_name,link_id" || cols[2] != nil {
		t.Errorf("columns %q", cols)
	}
	for _, p := range ps {
		if p.st.Kind == core.StmtReplace && p.st.Table != "wp_links" {
			t.Errorf("replace table %q", p.st.Table)
		}
	}
}

func TestSkipExtendedInsert(t *testing.T) {
	ps, _, _ := lexFile(t, "skip-extended.sql")
	var got []string
	for i := range 4 {
		got = append(got, values(t, ps, i)[0][1])
	}
	if strings.Join(got, "|") != "Uncategorized|News; and more|-- not a comment|# nor this" {
		t.Errorf("values %q", got)
	}
}

func TestHexBlob(t *testing.T) {
	ps, _, _ := lexFile(t, "hex-blob.sql")
	var kinds [][]core.ValueKind
	for _, p := range ps {
		for _, r := range p.rows {
			var k []core.ValueKind
			for _, v := range r.Values {
				k = append(k, v.Kind)
			}
			kinds = append(kinds, k)
		}
	}
	N, S, Num, H, B, Bit := core.Null, core.String, core.Number, core.Hex, core.BinaryString, core.Bit
	want := [][]core.ValueKind{
		{Num, S, H, H, H, B, Num, S},
		{Num, S, N, N, Bit, S, Num, N},
		{Num, S, H, N, Bit, S, Num, S},
	}
	for i := range want {
		for j := range want[i] {
			if kinds[i][j] != want[i][j] {
				t.Errorf("row %d value %d kind %d, want %d", i, j, kinds[i][j], want[i][j])
			}
		}
	}
	wantRows(t, values(t, ps, 0),
		[]string{"1", "a.bin", "0x00FF10", "0x0123456789ABCDEF0123456789ABCDEF", "0x05", "raw\x00bytes", "-12.50", "2026-01-01 00:00:00"},
		[]string{"2", "b.bin", "NULL", "NULL", "b'101'", "intro", "1e3", "NULL"},
		[]string{"3", "c.bin", "X'CAFE'", "NULL", "0b11", "national", ".5", "2026-01-02 00:00:00"},
	)
}

func TestNoBackslashEscapes(t *testing.T) {
	ps, l, _ := lexFile(t, "no-backslash.sql")
	wantRows(t, values(t, ps, 0), []string{"1", `C:\Users\`}, []string{"2", "it's"}, []string{"3", `a\nb`})
	// The SQL_MODE=@OLD_SQL_MODE line restores backslash escapes.
	wantRows(t, values(t, ps, 1), []string{"4", `back\slash`})
	if l.Mode().NoBackslashEscapes {
		t.Error("mode still NO_BACKSLASH_ESCAPES at the end")
	}
}

func TestCRLF(t *testing.T) {
	ps, _, in := lexFile(t, "crlf/crlf.sql")
	if !bytes.Contains(in, []byte("\r\n")) {
		t.Fatal("the CRLF fixture lost its CRLF line ends (check .gitattributes)")
	}
	wantRows(t, values(t, ps, 0), []string{"1", "two\r\nlines"}, []string{"2", "raw\r\ncrlf"})
	for _, p := range ps {
		if p.st.Rows && !bytes.Equal(p.tail, []byte(";\r\n")) {
			t.Errorf("tail %q", p.tail)
		}
		if p.st.Rows && !bytes.Equal(p.rows[1].Lead, []byte(",\r\n")) {
			t.Errorf("lead %q", p.rows[1].Lead)
		}
	}
}

func TestMariaDBSandbox(t *testing.T) {
	ps, l, _ := lexFile(t, "mariadb.sql")
	if !l.Sandbox() || !ps[0].st.Sandbox || ps[0].st.Kind != core.StmtComment || string(ps[0].st.Text) != SandboxLine+" \n" {
		t.Fatalf("sandbox line: %+v", ps[0].st)
	}
	for _, p := range ps[1:] {
		if p.st.Sandbox {
			t.Errorf("line %d marked as the sandbox line", p.st.Line)
		}
	}
	rows := values(t, ps, 0)
	if rows[0][5] != "Hi, this is a comment.\r\nTo get started, edit it." || rows[1][5] != `Ends with a backslash \` {
		t.Errorf("values %q", rows)
	}
	// A dump without the line has none.
	_, l2, _ := lexFile(t, "mysqldump8.sql")
	if l2.Sandbox() {
		t.Error("mysqldump marked as sandboxed")
	}
}

func TestPhpMyAdmin(t *testing.T) {
	ps, _, _ := lexFile(t, "phpmyadmin.sql")
	var ins piece
	kinds := map[core.StmtKind]int{}
	for _, p := range ps {
		kinds[p.st.Kind]++
		if p.st.Rows {
			ins = p
		}
	}
	if !bytes.HasSuffix(ins.st.Text, []byte("VALUES\n")) || len(ins.rows) != 5 || string(ins.rows[1].Lead) != ",\n" || len(ins.rows[0].Lead) != 0 {
		t.Fatalf("insert head %q rows %d", ins.st.Text, len(ins.rows))
	}
	if strings.Join(ins.st.Columns, ",") != "umeta_id,user_id,meta_key,meta_value" {
		t.Errorf("columns %q", ins.st.Columns)
	}
	if v := ins.rows[3].Values[3].Raw; !bytes.Contains(v, []byte(`s:2:"ip";s:10:"192.0.2.44"`)) {
		t.Errorf("serialized value %q", v)
	}
	if kinds[KindAlterTable] != 2 || kinds[core.StmtTransaction] != 2 || kinds[core.StmtCreateTable] != 1 {
		t.Errorf("kinds %v", kinds)
	}
}

func TestAdminer(t *testing.T) {
	ps, l, _ := lexFile(t, "adminer.sql")
	wantRows(t, values(t, ps, 0),
		[]string{"1", "10", "_billing_email", "buyer@example.test"},
		[]string{"2", "10", "_billing_first_name", "Pat"},
		[]string{"3", "10", "_customer_ip_address", "198.51.100.23"},
		[]string{"4", "10", "_order_total", "19.99"})
	if l.Mode() != (Mode{}) {
		t.Errorf("mode %+v", l.Mode())
	}
}

func TestDelimiterBlocks(t *testing.T) {
	ps, l, _ := lexFile(t, "routines.sql")
	var got []string
	for _, p := range ps {
		got = append(got, string(p.st.Kind))
	}
	want := "comment comment set create-table set set delimiter routine delimiter set delimiter routine delimiter comment"
	if strings.Join(got, " ") != want {
		t.Errorf("kinds\n got %s\nwant %s", strings.Join(got, " "), want)
	}
	for _, p := range ps {
		if p.st.Kind == core.StmtRoutine && !bytes.HasSuffix(p.st.Text, []byte("END */;;\n")) && !bytes.HasSuffix(p.st.Text, []byte("END$$\n")) {
			t.Errorf("routine text ends %q", p.st.Text[max(0, len(p.st.Text)-12):])
		}
	}
	if string(l.delim) != ";" {
		t.Errorf("delimiter at the end %q", l.delim)
	}
}

func lexString(t *testing.T, in string, o Options) ([]piece, error) {
	t.Helper()
	ps, _, err := lexAll(t, strings.NewReader(in), o)
	return ps, err
}

// Client commands are statements of their own kind, ending at the line
// end like the client runs them, whatever follows on the next line.
func TestClientCommands(t *testing.T) {
	for in, want := range map[string]core.StmtKind{
		"\\! touch /tmp/pwned\nSELECT 1;\n":       KindClient,
		"system id\nSELECT 1;\n":                  KindClient,
		"SYSTEM id\n":                             KindClient,
		"source /etc/passwd\n":                    KindClient,
		"\\. x.sql\n":                             KindClient,
		"  \\. x.sql\n":                           KindClient,
		"SELECT 1 \\! ls\n;\n":                    KindClient,
		"USE mysql;\n":                            KindUse,
		"use `shop`;\n":                           KindUse,
		"/*!40000 USE x */;\n":                    KindUse,
		"CREATE DATABASE x;\n":                    core.StmtOther,
		"/*!40000 INSERT INTO t VALUES (1) */;\n": core.StmtOther,
		"INSERT INTO t SELECT * FROM u;\n":        core.StmtOther,
		"INSERT INTO t SET a = 'x';\n":            core.StmtOther,
		"SELECT 'a\\! b';\n":                      core.StmtOther,
		"SELECT 1; -- \\! not a command\n":        core.StmtOther,
	} {
		ps, err := lexString(t, in, Options{})
		if err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		if ps[0].st.Kind != want {
			t.Errorf("%q: kind %s, want %s", in, ps[0].st.Kind, want)
		}
		if want == KindClient && strings.Contains(in, "\nSELECT 1;") && len(ps) != 2 {
			t.Errorf("%q: %d statements", in, len(ps))
		}
	}
}

// Truncated input is an error, never a panic or a short read that passes.
func TestTruncated(t *testing.T) {
	for _, in := range []string{
		"INSERT INTO t VALUES ('abc",
		"INSERT INTO t VALUES ('abc\\",
		"INSERT INTO t VALUES (1,2",
		"INSERT INTO t VALUES (1),",
		"INSERT INTO t VALUES (1)",
		"INSERT INTO t (a, b",
		"CREATE TABLE t (a int",
		"SELECT 'x;",
		"/* never closed",
		"SET x = `a",
		"INSERT INTO t VALUES (CONCAT('a'",
	} {
		_, err := lexString(t, in, Options{})
		var te *TruncatedError
		if !errors.As(err, &te) || te.Line != 1 {
			t.Errorf("%q: err = %v, want a TruncatedError at line 1", in, err)
		}
	}
	// Every cut of a real dump is either a clean statement end or an error.
	in, _ := os.ReadFile("testdata/mysqldump8.sql")
	for cut := range len(in) {
		ps, err := lexString(t, string(in[:cut]), Options{})
		if err == nil && !bytes.Equal(join(ps), in[:cut]) {
			t.Fatalf("cut at %d: joined text differs", cut)
		}
	}
}

func TestReadErrorPassesThrough(t *testing.T) {
	boom := errors.New("disk on fire")
	r := io.MultiReader(strings.NewReader("INSERT INTO t VALUES ('abc"), iotest.ErrReader(boom))
	_, _, err := lexAll(t, r, Options{})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the read error", err)
	}
}

func TestTooLarge(t *testing.T) {
	big := strings.Repeat("x", 2000)
	_, err := lexString(t, "SELECT 1;\nINSERT INTO t VALUES (1,'"+big+"');\n", Options{MaxValue: 1000})
	var te *TooLargeError
	if !errors.As(err, &te) || te.Line != 2 || te.Statement || te.Limit != 1000 {
		t.Fatalf("value: %v", err)
	}
	_, err = lexString(t, "INSERT INTO t VALUES (1,'"+strings.Repeat("\\n", 1000)+"');\n", Options{MaxValue: 1000})
	if !errors.As(err, &te) {
		t.Fatalf("escaped value: %v", err)
	}
	_, err = lexString(t, "CREATE TABLE t (a varchar(10) DEFAULT '"+big+"');\n", Options{MaxValue: 1000})
	if !errors.As(err, &te) || !te.Statement {
		t.Fatalf("statement: %v", err)
	}
	// At the limit is fine.
	if _, err := lexString(t, "INSERT INTO t VALUES ('"+big[:1000]+"');\n", Options{MaxValue: 1000}); err != nil {
		t.Fatal(err)
	}
}

// A 50 MB INSERT on one line streams: no line limit, every row read.
func TestHugeLine(t *testing.T) {
	var b bytes.Buffer
	b.WriteString("INSERT INTO `wp_postmeta` VALUES ")
	row := "(1,2,'_billing_email','a:1:{s:5:\\\"email\\\";s:16:\\\"buyer@example.test\\\";}')"
	n := 0
	for b.Len() < 30<<20 {
		if n > 0 {
			b.WriteByte(',')
		}
		b.WriteString(row)
		n++
	}
	// One value of 20 MB in the same line.
	b.WriteString(",(9,9,'big','")
	b.WriteString(strings.Repeat("y", 20<<20))
	b.WriteString("');\n-- Dump completed\n")
	n++
	l := NewLexer(bytes.NewReader(b.Bytes()), Options{})
	st, err := l.Next()
	if err != nil || !st.Rows {
		t.Fatal(st, err)
	}
	rows := 0
	for {
		a := &Arena{} // a fresh arena per row, as batches would drop them
		r, ok, err := l.Row(a)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		rows++
		if rows == 1 && string(r.Values[3].Raw) != `a:1:{s:5:"email";s:16:"buyer@example.test";}` {
			t.Fatalf("value %q", r.Values[3].Raw)
		}
		if rows == n && len(r.Values[3].Raw) != 20<<20 {
			t.Fatalf("big value %d bytes", len(r.Values[3].Raw))
		}
	}
	if rows != n {
		t.Fatalf("%d rows, want %d", rows, n)
	}
	if tail, err := l.Tail(); err != nil || string(tail) != ";\n" {
		t.Fatalf("tail %q %v", tail, err)
	}
	if st, err := l.Next(); err != nil || st.Kind != core.StmtComment {
		t.Fatal(st, err)
	}
}

func TestEmptyAndBlank(t *testing.T) {
	ps, err := lexString(t, "", Options{})
	if err != nil || len(ps) != 0 {
		t.Fatal(ps, err)
	}
	ps, err = lexString(t, "\n\n  \n", Options{})
	if err != nil || len(ps) != 1 || ps[0].st.Kind != core.StmtComment {
		t.Fatal(ps, err)
	}
	ps, err = lexString(t, "SELECT 1;   \nSELECT 2; SELECT 3;", Options{})
	if err != nil || len(ps) != 3 || string(ps[0].st.Text) != "SELECT 1;   \n" || string(ps[1].st.Text) != "SELECT 2;" || string(ps[2].st.Text) != " SELECT 3;" {
		t.Fatalf("%v %v", ps, err)
	}
	if ps[2].st.Line != 2 {
		t.Errorf("line %d", ps[2].st.Line)
	}
}

func TestRowShapes(t *testing.T) {
	for in, want := range map[string]string{
		"INSERT INTO t VALUES ();\n":                            "",
		"INSERT INTO t VALUES ( 1 , 'a' );\n":                   "1|a",
		"INSERT INTO t VALUES (NOW(),CONCAT('a', ',)'));\n":     "NOW()|CONCAT('a', ',)')",
		"INSERT INTO t VALUES ('a' 'b',1 + 2);\n":               "'a' 'b'|1 + 2",
		"INSERT INTO t VALUES (null,NULLX,-1,+2.5e-3);\n":       "NULL|NULLX|-1|+2.5e-3",
		"INSERT INTO t VALUES (_latin1 X'41',_binary 0x42);\n":  "_latin1 X'41'|_binary 0x42",
		"INSERT INTO t VALUES (\"dq\",'it''s','a\\%\\_b');\n":   "dq|it's|a\\%\\_b",
		"INSERT INTO t VALUES ('\\0\\b\\t\\Z\\q\\\\');\n":       "\x00\b\t\x1aq\\",
		"INSERT INTO `db`.`t` VALUES (1);\n":                    "1",
		"INSERT /* c */ INTO t -- c\n VALUES\n(1)\n,\n(2);\n":   "1",
		"INSERT INTO t VALUES (1 /* c */);\n":                   "1 /* c */",
		"INSERT INTO t VALUE (1);\n":                            "1",
		"insert low_priority ignore into t values (1);\n":       "1",
		"INSERT INTO t VALUES (ST_GeomFromText('POINT(1 1)'));": "ST_GeomFromText('POINT(1 1)')",
	} {
		ps, err := lexString(t, in, Options{})
		if err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		var row []string
		for _, v := range ps[0].rows[0].Values {
			if v.Kind == core.Null {
				row = append(row, "NULL")
			} else {
				row = append(row, string(v.Raw))
			}
		}
		if got := strings.Join(row, "|"); got != want {
			t.Errorf("%q: values %q, want %q", in, got, want)
		}
		if !bytes.Equal(join(ps), []byte(in)) {
			t.Errorf("%q: join differs", in)
		}
	}
}

func TestANSIQuotes(t *testing.T) {
	ps, err := lexString(t, "SET sql_mode='ANSI_QUOTES';\nINSERT INTO \"t\" (\"a\") VALUES (\"col\",'s');\n", Options{})
	if err != nil {
		t.Fatal(err)
	}
	p := ps[1]
	if p.st.Table != "t" || p.st.Columns[0] != "a" || p.rows[0].Values[0].Kind != core.Expr || p.rows[0].Values[1].Kind != core.String {
		t.Fatalf("%+v %+v", p.st, p.rows[0].Values)
	}
}

func TestSyntaxErrors(t *testing.T) {
	for _, in := range []string{
		"INSERT INTO t VALUES (1), 2;\n",
		"INSERT INTO t VALUES (1;\n",
	} {
		_, err := lexString(t, in, Options{})
		var se *SyntaxError
		if !errors.As(err, &se) || se.Line != 1 {
			t.Errorf("%q: %v", in, err)
		}
	}
}

func TestMisuse(t *testing.T) {
	l := NewLexer(strings.NewReader("INSERT INTO t VALUES (1);\n"), Options{})
	if _, _, err := l.Row(&Arena{}); err == nil {
		t.Error("Row before Next")
	}
	if _, err := l.Next(); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Next(); err == nil {
		t.Error("Next with rows open")
	}
	if _, err := l.Tail(); err == nil {
		t.Error("Tail with rows open")
	}
}

func FuzzTokenize(f *testing.F) {
	for _, name := range []string{"mysqldump8.sql", "mariadb.sql", "phpmyadmin.sql", "adminer.sql", "routines.sql", "no-backslash.sql", "hex-blob.sql", "crlf/crlf.sql"} {
		b, err := os.ReadFile(filepath.Join("testdata", filepath.FromSlash(name)))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	f.Add([]byte("INSERT INTO t VALUES ('a\\'b;',\"c\"),(0x1,_binary 'x');\nDELIMITER ;;\nSELECT ';';;\n"))
	f.Fuzz(func(t *testing.T, in []byte) {
		ps, _, err := lexAll(t, bytes.NewReader(in), Options{MaxValue: 1 << 20})
		if err != nil {
			// The pieces before the error still join to a prefix.
			if !bytes.HasPrefix(in, join(ps)) {
				t.Fatal("pieces before the error are not a prefix of the input")
			}
			return
		}
		if !bytes.Equal(join(ps), in) {
			t.Fatal("joined statements differ from the input")
		}
		checkAlone(t, ps)
	})
}
