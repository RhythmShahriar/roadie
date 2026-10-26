// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package mysql

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/internal/testutil"
)

// write runs every batch through a Writer.
func write(t testing.TB, bs []*core.Batch, o WriterOptions) []byte {
	t.Helper()
	var out bytes.Buffer
	w := NewWriter(&out, o)
	for _, b := range bs {
		if err := w.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Finish(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func batchesOf(t testing.TB, in []byte) []*core.Batch {
	t.Helper()
	bs, err := drain(t, sourceOf(in, SourceOptions{}))
	if err != nil {
		t.Fatal(err)
	}
	return bs
}

// With no value changed, every fixture writes back byte for byte.
func TestIdentityRoundTrip(t *testing.T) {
	for _, name := range fixtures(t) {
		in := readFixture(t, name)
		target := core.Dialect("")
		switch {
		case strings.HasPrefix(name, "mariadb"):
			target = core.MariaDB
		case name == "mysqldump8.sql":
			target = core.MySQL
		}
		if got := write(t, batchesOf(t, in), WriterOptions{Target: target}); !bytes.Equal(got, in) {
			t.Errorf("%s: identity output differs", name)
		}
	}
}

// Every value re-encoded (all Dirty) reads back to the same values.
func TestReencodeRoundTrip(t *testing.T) {
	for _, name := range fixtures(t) {
		in := readFixture(t, name)
		bs := batchesOf(t, in)
		for _, b := range bs {
			for i := range b.Rows {
				for j := range b.Rows[i].Values {
					if k := b.Rows[i].Values[j].Kind; k == core.String || k == core.BinaryString || k == core.Null {
						b.Rows[i].Values[j].Dirty = true
					}
				}
			}
		}
		out := write(t, bs, WriterOptions{})
		if !sameValues(t, in, out) {
			t.Errorf("%s: re-encoded values differ", name)
		}
	}
}

// sameValues reports whether two dumps hold the same rows, value by value.
func sameValues(t testing.TB, a, b []byte) bool {
	t.Helper()
	x, y := batchesOf(t, a), batchesOf(t, b)
	rows := func(bs []*core.Batch) []core.Row {
		var out []core.Row
		for _, b := range bs {
			out = append(out, b.Rows...)
		}
		return out
	}
	rx, ry := rows(x), rows(y)
	if len(rx) != len(ry) {
		return false
	}
	for i := range rx {
		if !equalValues(rx[i].Values, ry[i].Values) {
			return false
		}
	}
	return true
}

// A value that looks like SQL stays data.
func TestInjectionRoundTrip(t *testing.T) {
	evil := "'); DROP TABLE wp_users; --"
	for _, extra := range []string{"", "\\", "\x00\x1a\r\n", "\\'"} {
		for _, name := range []string{"mysqldump8.sql", "no-backslash.sql"} {
			bs := batchesOf(t, readFixture(t, name))
			for _, b := range bs {
				if b.Table != nil {
					v := &b.Rows[0].Values[1]
					v.Kind, v.Raw, v.Dirty = core.String, []byte(evil+extra), true
				}
			}
			out := write(t, bs, WriterOptions{})
			after := batchesOf(t, out)
			if len(after) != len(bs) {
				t.Fatalf("%s %q: %d batches after, %d before", name, extra, len(after), len(bs))
			}
			for _, b := range after {
				if b.Table != nil && string(b.Rows[0].Values[1].Raw) != evil+extra {
					t.Errorf("%s %q: value %q", name, extra, b.Rows[0].Values[1].Raw)
				}
				if b.Pass != nil && bytes.Contains(bytes.ToUpper(b.Pass.Text), []byte("DROP TABLE WP_USERS;")) {
					t.Errorf("%s: the value became a statement", name)
				}
			}
		}
	}
}

func TestDropRows(t *testing.T) {
	const in = "INSERT INTO `t` (`a`) VALUES\n(1),\n(2),\n(3);\nSELECT 1;\n"
	for _, c := range []struct {
		drop []int
		want string
	}{
		{nil, in},
		{[]int{0}, "INSERT INTO `t` (`a`) VALUES\n(2),\n(3);\nSELECT 1;\n"},
		{[]int{1}, "INSERT INTO `t` (`a`) VALUES\n(1),\n(3);\nSELECT 1;\n"},
		{[]int{2}, "INSERT INTO `t` (`a`) VALUES\n(1),\n(2);\nSELECT 1;\n"},
		{[]int{0, 1}, "INSERT INTO `t` (`a`) VALUES\n(3);\nSELECT 1;\n"},
		{[]int{0, 1, 2}, "SELECT 1;\n"},
	} {
		bs := batchesOf(t, []byte(in))
		for _, i := range c.drop {
			bs[0].Rows[i].Drop = true
		}
		if got := string(write(t, bs, WriterOptions{})); got != c.want {
			t.Errorf("drop %v:\n got %q\nwant %q", c.drop, got, c.want)
		}
	}
}

// Dropping across batches: the first batch's rows all dropped, the head
// still comes out once, before the first row written.
func TestDropAcrossBatches(t *testing.T) {
	var b bytes.Buffer
	b.WriteString("INSERT INTO `t` (`a`) VALUES ")
	for i := range 9000 {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, "(%d,'%s')", i, strings.Repeat("x", 1000))
	}
	b.WriteString(";\n")
	bs := batchesOf(t, []byte(strings.Replace(b.String(), "(`a`)", "(`a`,`b`)", 1)))
	if len(bs) < 3 {
		t.Fatalf("%d batches", len(bs))
	}
	kept := 0
	for i, x := range bs {
		for j := range x.Rows {
			x.Rows[j].Drop = i == 0 || j%2 == 0
			if !x.Rows[j].Drop {
				kept++
			}
		}
	}
	out := write(t, bs, WriterOptions{})
	after := batchesOf(t, out)
	n := 0
	for _, x := range after {
		n += len(x.Rows)
	}
	if n != kept || bytes.Count(out, []byte("INSERT INTO")) != 1 || bytes.Contains(out, []byte("VALUES ,")) {
		t.Fatalf("%d rows written, want %d", n, kept)
	}
	// All rows of every batch dropped: nothing of the statement is left.
	for _, x := range bs {
		for j := range x.Rows {
			x.Rows[j].Drop = true
		}
	}
	if out := write(t, bs, WriterOptions{}); len(out) != 0 {
		t.Fatalf("all dropped wrote %d bytes", len(out))
	}
}

func TestFixCollation(t *testing.T) {
	in := "/*!40101 SET NAMES utf8mb4 */;\n" +
		"/*!50503 SET character_set_client = utf8mb4 */;\n" +
		"/*!40101 SET collation_connection = utf8mb4_0900_ai_ci */;\n" +
		"SET NAMES utf8mb4 COLLATE utf8mb4_0900_as_cs;\n" +
		"CREATE TABLE `t` (\n  `a` varchar(10) COLLATE utf8mb4_0900_ai_ci NOT NULL DEFAULT 'utf8mb4_0900_ai_ci',\n  `b` text COLLATE utf8mb4_0900_bin\n) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='keeps utf8mb4_0900_ai_ci in strings';\n" +
		"INSERT INTO `t` VALUES ('utf8mb4_0900_ai_ci','data stays');\n" +
		"/*!50003 SET sql_mode = 'x' */;\n"
	got := write(t, batchesOf(t, []byte(in)), WriterOptions{FixCollation: true})
	testutil.Golden(t, "fix-collation.sql", got)
	if bytes.Count(got, []byte("utf8mb4_0900_ai_ci")) != 3 {
		t.Errorf("strings and data must keep their text:\n%s", got)
	}
	if !bytes.Equal(write(t, batchesOf(t, []byte(in)), WriterOptions{}), []byte(in)) {
		t.Error("without FixCollation the text changed")
	}
}

func TestSandboxLine(t *testing.T) {
	maria := readFixture(t, "mariadb.sql")
	plain := readFixture(t, "mysqldump8.sql")
	line := []byte(SandboxLine)
	// MariaDB target: the line is first, exactly once, input with or without it.
	for _, in := range [][]byte{maria, plain} {
		out := write(t, batchesOf(t, in), WriterOptions{Target: core.MariaDB})
		if !bytes.HasPrefix(out, line) || bytes.Count(out, line) != 1 {
			t.Errorf("MariaDB output starts %q and holds the line %d times", out[:50], bytes.Count(out, line))
		}
	}
	if out := write(t, batchesOf(t, plain), WriterOptions{Target: core.MariaDB}); !bytes.Equal(out, append(append([]byte(nil), SandboxLine+"\n"...), plain...)) {
		t.Error("MariaDB output from a mysqldump is not the line plus the input")
	}
	// MySQL target: never.
	out := write(t, batchesOf(t, maria), WriterOptions{Target: core.MySQL})
	if bytes.Contains(out, line) || !bytes.Equal(out, bytes.TrimPrefix(maria, []byte(SandboxLine+" \n"))) {
		t.Errorf("MySQL output holds the sandbox line or lost more:\n%.80s", out)
	}
	// An empty MariaDB dump is the line alone.
	if out := write(t, nil, WriterOptions{Target: core.MariaDB}); string(out) != SandboxLine+"\n" {
		t.Errorf("empty MariaDB output %q", out)
	}
	// A second sandbox line later in the input is dropped for both targets.
	twice := append(append([]byte(nil), maria...), maria...)
	for _, target := range []core.Dialect{core.MariaDB, core.MySQL} {
		if n := bytes.Count(write(t, batchesOf(t, twice), WriterOptions{Target: target}), line); n > 1 || (target == core.MySQL && n != 0) {
			t.Errorf("%s: %d sandbox lines", target, n)
		}
	}
}

// Under NO_BACKSLASH_ESCAPES the writer doubles quotes and leaves
// backslashes alone, following the dump's own SET sql_mode.
func TestEncodeNoBackslash(t *testing.T) {
	bs := batchesOf(t, readFixture(t, "no-backslash.sql"))
	var first, after *core.Batch
	for _, b := range bs {
		if b.Table != nil {
			if first == nil {
				first = b
			} else {
				after = b
			}
		}
	}
	for _, b := range []*core.Batch{first, after} {
		v := &b.Rows[0].Values[1]
		v.Raw, v.Dirty = []byte(`it's C:\x`), true
	}
	out := string(write(t, bs, WriterOptions{}))
	if !strings.Contains(out, `(1,'it''s C:\x')`) || !strings.Contains(out, `(4,'it\'s C:\\x')`) {
		t.Errorf("output:\n%s", out)
	}
}

func TestAppendValue(t *testing.T) {
	for _, c := range []struct {
		v    core.Value
		want string
		err  bool
	}{
		{core.Value{Kind: core.Null}, "NULL", false},
		{core.Value{Kind: core.String, Raw: []byte("a'b\\c\x00\n\r\x1a\"%_\t")}, `'a\'b\\c\0\n\r\Z"%_` + "\t'", false},
		{core.Value{Kind: core.BinaryString, Raw: []byte{0xff, 0}}, "_binary '\xff\\0'", false},
		{core.Value{Kind: core.Number, Raw: []byte("-1.5e3"), Dirty: true}, "-1.5e3", false},
		{core.Value{Kind: core.Number, Raw: []byte("1; DROP TABLE x"), Dirty: true}, "", true},
		{core.Value{Kind: core.Number, Raw: []byte("1; DROP TABLE x")}, "1; DROP TABLE x", false}, // read from the input, so as written
		{core.Value{Kind: core.Hex, Raw: []byte("0xCAFE"), Dirty: true}, "0xCAFE", false},
		{core.Value{Kind: core.Hex, Raw: []byte("X''"), Dirty: true}, "X''", false},
		{core.Value{Kind: core.Hex, Raw: []byte("0x'"), Dirty: true}, "", true},
		{core.Value{Kind: core.Bit, Raw: []byte("b'101'"), Dirty: true}, "b'101'", false},
		{core.Value{Kind: core.Expr, Raw: []byte("NOW()")}, "NOW()", false},
		{core.Value{Kind: core.Expr, Raw: []byte("NOW()"), Dirty: true}, "", true},
		{core.Value{Kind: core.Number}, "", true},
		{HexValue([]byte{0xca, 0xfe}), "0xCAFE", false},
		{HexValue(nil), "X''", false},
	} {
		got, err := AppendValue(nil, c.v, Mode{})
		if (err != nil) != c.err || (!c.err && string(got) != c.want) {
			t.Errorf("AppendValue(%+v) = %q, %v", c.v, got, err)
		}
	}
	if got := string(AppendString(nil, []byte(`it's \n`), Mode{NoBackslashEscapes: true})); got != `'it''s \n'` {
		t.Errorf("no backslash: %q", got)
	}
}

func TestWriterOrder(t *testing.T) {
	w := NewWriter(&bytes.Buffer{}, WriterOptions{})
	pass := func(seq uint64) *core.Batch {
		return &core.Batch{Seq: seq, Pass: &core.Statement{Kind: core.StmtSet, Text: []byte("SET a=1;\n")}}
	}
	if err := w.Write(pass(3)); err != nil {
		t.Fatal(err)
	}
	if err := w.Write(pass(5)); err != nil {
		t.Fatal(err) // a gap is fine: the engine left a batch out
	}
	if err := w.Write(pass(4)); err == nil {
		t.Error("a batch out of order was written")
	}
	rows := &core.Batch{Seq: 9, Table: &core.Table{Name: "t"}, Rows: []core.Row{{Src: []byte("(1)")}}}
	if err := w.Write(rows); err == nil {
		t.Error("rows without a first batch were written")
	}
	open := NewWriter(&bytes.Buffer{}, WriterOptions{})
	_ = open.Write(&core.Batch{Seq: 1, First: true, Head: []byte("INSERT INTO t VALUES "), Rows: []core.Row{{Src: []byte("(1)")}}})
	if err := open.Finish(); err == nil {
		t.Error("Finish with an INSERT open")
	}
	if err := open.Write(pass(2)); err == nil {
		t.Error("a passthrough inside an open INSERT")
	}
}

// decode reads a single string literal back through the lexer.
func decode(t testing.TB, lit []byte, m Mode) ([]byte, bool) {
	l := NewLexer(bytes.NewReader(append(append([]byte("INSERT INTO t VALUES ("), lit...), ");\n"...)), Options{})
	l.mode = m
	st, err := l.Next()
	if err != nil || !st.Rows {
		return nil, false
	}
	r, ok, err := l.Row(&Arena{})
	if err != nil || !ok || len(r.Values) != 1 || r.Values[0].Kind != core.String {
		return nil, false
	}
	if _, ok, err := l.Row(&Arena{}); ok || err != nil {
		return nil, false
	}
	return r.Values[0].Raw, true
}

func FuzzSQLStringEscape(f *testing.F) {
	f.Add([]byte("'); DROP TABLE wp_users; --"), false)
	f.Add([]byte("a\\b'c\x00\x1a\r\n\"%_"), true)
	f.Add([]byte{0xff, 0xfe, '\\'}, false)
	f.Fuzz(func(t *testing.T, b []byte, noBackslash bool) {
		m := Mode{NoBackslashEscapes: noBackslash}
		lit := AppendString(nil, b, m)
		got, ok := decode(t, lit, m)
		if !ok || !bytes.Equal(got, b) {
			t.Fatalf("unescape(escape(%q)) = %q, %v", b, got, ok)
		}
		if again := AppendString(nil, got, m); !bytes.Equal(again, lit) {
			t.Fatalf("escape(unescape(%q)) = %q", lit, again)
		}
		// Any literal the lexer accepts encodes to one that decodes the same.
		if v, ok := decode(t, append(append([]byte{'\''}, b...), '\''), m); ok {
			if v2, ok := decode(t, AppendString(nil, v, m), m); !ok || !bytes.Equal(v, v2) {
				t.Fatalf("literal '%s': decoded %q, re-encoded decodes to %q", b, v, v2)
			}
		}
	})
}

// genFuzzDump builds a dump whose values come from the fuzz input.
func genFuzzDump(data []byte, seed uint64) []byte {
	r := rand.New(rand.NewPCG(seed, 1))
	var b bytes.Buffer
	b.WriteString("CREATE TABLE `t` (`id` int, `s` longtext, `x` blob, `n` int);\n")
	m := Mode{NoBackslashEscapes: seed%5 == 0}
	if m.NoBackslashEscapes {
		b.WriteString("/*!40101 SET SQL_MODE='NO_BACKSLASH_ESCAPES' */;\n")
	}
	stmts := 1 + int(seed%3)
	for s := range stmts {
		b.WriteString("INSERT INTO `t` VALUES ")
		rows := 1 + r.IntN(4)
		for i := range rows {
			if i > 0 {
				b.WriteString([]string{",", ",\n", ", "}[r.IntN(3)])
			}
			cut := 0
			if len(data) > 0 {
				cut = r.IntN(len(data) + 1)
			}
			fmt.Fprintf(&b, "(%d,", s*10+i)
			b.Write(AppendString(nil, data[:cut], m))
			b.WriteString(",")
			if r.IntN(2) == 0 {
				fmt.Fprintf(&b, "0x%X", data[cut:])
				if len(data[cut:]) == 0 {
					b.WriteString("00")
				}
			} else {
				b.WriteString("_binary ")
				b.Write(AppendString(nil, data[cut:], m))
			}
			fmt.Fprintf(&b, ",%s)", []string{"NULL", "-1", "2.5e3", "CURRENT_TIMESTAMP"}[r.IntN(4)])
		}
		b.WriteString(";\n")
	}
	b.WriteString("-- Dump completed\n")
	return b.Bytes()
}

func FuzzRoundTrip(f *testing.F) {
	f.Add([]byte("plain"), uint64(1))
	f.Add([]byte("'); DROP TABLE wp_users; --\\"), uint64(5))
	f.Add([]byte{0, 0x1a, '\r', '\n', '\'', '"', '\\', 0xff}, uint64(7))
	f.Fuzz(func(t *testing.T, data []byte, seed uint64) {
		in := genFuzzDump(data, seed)
		bs, err := drain(t, sourceOf(in, SourceOptions{}))
		if err != nil {
			t.Fatalf("generated dump doesn't read: %v", err)
		}
		if out := write(t, bs, WriterOptions{}); !bytes.Equal(out, in) {
			t.Fatal("identity round trip differs")
		}
		for _, b := range bs {
			for i := range b.Rows {
				for j := range b.Rows[i].Values {
					if k := b.Rows[i].Values[j].Kind; k == core.String || k == core.BinaryString {
						b.Rows[i].Values[j].Dirty = true
					}
				}
			}
		}
		if out := write(t, bs, WriterOptions{}); !sameValues(t, in, out) {
			t.Fatal("re-encoded values differ")
		}
	})
}
