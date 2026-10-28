// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package mysql

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/internal/testutil"
)

// drain reads every batch of a source.
func drain(t testing.TB, s *Source) ([]*core.Batch, error) {
	t.Helper()
	var out []*core.Batch
	for {
		b, err := s.Next(context.Background())
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return out, err
		}
		out = append(out, b)
	}
}

func sourceOf(in []byte, o SourceOptions) *Source {
	if o.Name == "" {
		o.Name = "prod.sql"
	}
	return NewSource(bytes.NewReader(in), o)
}

func readFixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Batches are numbered in order, a statement's first batch carries its
// head and its last the tail and passthrough statements keep their place.
func TestBatchOrder(t *testing.T) {
	bs, err := drain(t, sourceOf(readFixture(t, "mysqldump8.sql"), SourceOptions{}))
	if err != nil {
		t.Fatal(err)
	}
	for i, b := range bs {
		if b.Seq != uint64(i) {
			t.Fatalf("batch %d has Seq %d", i, b.Seq)
		}
		if (b.Pass == nil) == (b.Table == nil) {
			t.Fatalf("batch %d is both or neither a passthrough and rows", i)
		}
		if b.Pass == nil && (!b.First || !b.Last || len(b.Head) == 0 || len(b.Tail) == 0) {
			t.Fatalf("batch %d: one-batch statement without head or tail", i)
		}
		if i > 0 && b.Offset < bs[i-1].Offset {
			t.Fatalf("batch %d: offset went back", i)
		}
	}
	var users *core.Batch
	for _, b := range bs {
		if b.Table != nil && b.Table.Name == "wp_users" {
			users = b
		}
	}
	if users == nil || len(users.Rows) != 3 || strings.Join(intsStr(users.Columns), ",") != "0,1,2,3,4,5" {
		t.Fatalf("wp_users batch %+v", users)
	}
}

func intsStr(xs []int) []string {
	var out []string
	for _, x := range xs {
		out = append(out, string(rune('0'+x)))
	}
	return out
}

// Columns maps value positions to table columns, also for a reordered
// --complete-insert list.
func TestColumnsMapping(t *testing.T) {
	bs, err := drain(t, sourceOf(readFixture(t, "complete-insert.sql"), SourceOptions{}))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, b := range bs {
		if b.Table != nil {
			got = append(got, strings.Join(intsStr(b.Columns), ","))
		}
	}
	if strings.Join(got, " ") != "0,1,2,3 2,0 0,1,2,3 0,1,2,3" {
		t.Errorf("columns %q", got)
	}
}

func TestSchema(t *testing.T) {
	s := sourceOf(readFixture(t, "hex-blob.sql"), SourceOptions{Dialect: core.MySQL, Server: "8.0.36"})
	if _, err := drain(t, s); err != nil {
		t.Fatal(err)
	}
	sc := s.Schema()
	if sc.Dialect != core.MySQL || sc.Server != "8.0.36" || len(sc.Tables) != 1 {
		t.Fatalf("schema %+v", sc)
	}
	tb := sc.Table("files")
	want := []core.Column{
		{Name: "id", Type: "int", Unique: true},
		{Name: "name", Type: "varchar(64)", MaxLen: 64},
		{Name: "data", Type: "longblob", Nullable: true, Binary: true},
		{Name: "hash", Type: "binary(16)", MaxLen: 16, Unique: true, Nullable: true, Binary: true},
		{Name: "flags", Type: "bit(8)", Nullable: true},
		{Name: "body", Type: "varchar(20)", MaxLen: 80, Nullable: true},
		{Name: "total", Type: "decimal(10,2)", Nullable: true},
		{Name: "created", Type: "timestamp", Nullable: true},
	}
	if len(tb.Columns) != len(want) {
		t.Fatalf("columns %+v", tb.Columns)
	}
	for i, c := range want {
		if tb.Columns[i] != c {
			t.Errorf("column %d = %+v, want %+v", i, tb.Columns[i], c)
		}
	}
	if strings.Join(tb.PrimaryKey, ",") != "id" {
		t.Errorf("primary key %q", tb.PrimaryKey)
	}
}

func TestParseCreateTable(t *testing.T) {
	text := "CREATE TABLE IF NOT EXISTS `shop`.`wp_x` (\n" +
		"  `id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,\n" +
		"  `email` varchar(100) CHARACTER SET latin1 COLLATE latin1_swedish_ci NOT NULL DEFAULT '',\n" +
		"  `code` char(8) COLLATE ascii_bin DEFAULT NULL,\n" +
		"  `name` varchar(50) DEFAULT 'a, (b)' COMMENT 'with, commas',\n" +
		"  `full` varchar(101) GENERATED ALWAYS AS (concat(`email`,' ',`name`)) VIRTUAL,\n" +
		"  `m` varchar(10) AS (lower(`name`)) PERSISTENT,\n" +
		"  `status` enum('a','b') NOT NULL DEFAULT 'a',\n" +
		"  `note` tinytext,\n" +
		"  `uuid` char(36) NOT NULL UNIQUE,\n" +
		"  `n` nvarchar(10),\n" +
		"  `bin` varchar(10) CHARACTER SET binary,\n" +
		"  `d` double precision,\n" +
		"  PRIMARY KEY (`id`),\n" +
		"  UNIQUE KEY `email` (`email`),\n" +
		"  UNIQUE KEY `pair` (`code`,`name`),\n" +
		"  KEY `name` (`name`(20)),\n" +
		"  CONSTRAINT `fk` FOREIGN KEY (`id`) REFERENCES `o` (`id`),\n" +
		"  CONSTRAINT `uc` UNIQUE (`n`)\n" +
		") ENGINE=InnoDB DEFAULT CHARSET=utf8mb3;"
	tb := ParseCreateTable([]byte(text), Mode{}, nil)
	if tb == nil || tb.Name != "wp_x" {
		t.Fatalf("table %+v", tb)
	}
	byName := map[string]core.Column{}
	for _, c := range tb.Columns {
		byName[c.Name] = c
	}
	checks := map[string]core.Column{
		"id":     {Name: "id", Type: "bigint(20) unsigned", Unique: true},
		"email":  {Name: "email", Type: "varchar(100)", MaxLen: 100, Unique: true},
		"code":   {Name: "code", Type: "char(8)", MaxLen: 8, Nullable: true},
		"name":   {Name: "name", Type: "varchar(50)", MaxLen: 150, Nullable: true},
		"full":   {Name: "full", Type: "varchar(101)", MaxLen: 303, Nullable: true, Generated: true},
		"m":      {Name: "m", Type: "varchar(10)", MaxLen: 30, Nullable: true, Generated: true},
		"status": {Name: "status", Type: "enum('a','b')"},
		"note":   {Name: "note", Type: "tinytext", MaxLen: 255, Nullable: true},
		"uuid":   {Name: "uuid", Type: "char(36)", MaxLen: 108, Unique: true},
		"n":      {Name: "n", Type: "nvarchar(10)", MaxLen: 30, Nullable: true, Unique: true},
		"bin":    {Name: "bin", Type: "varchar(10)", MaxLen: 10, Nullable: true, Binary: true},
		"d":      {Name: "d", Type: "double precision", Nullable: true},
	}
	if len(tb.Columns) != len(checks) {
		t.Errorf("%d columns: %+v", len(tb.Columns), tb.Columns)
	}
	for name, want := range checks {
		if got := byName[name]; got != want {
			t.Errorf("%s = %+v\n     want %+v", name, got, want)
		}
	}
	if strings.Join(tb.PrimaryKey, ",") != "id" {
		t.Errorf("primary key %q", tb.PrimaryKey)
	}
	// Column-level primary key; LIKE; AS SELECT; not a create table.
	tb = ParseCreateTable([]byte("CREATE TABLE t (id int PRIMARY KEY, v varchar(5))"), Mode{}, nil)
	if strings.Join(tb.PrimaryKey, ",") != "id" || !tb.Columns[0].Unique || tb.Columns[0].Nullable || tb.Columns[1].MaxLen != 20 {
		t.Errorf("inline pk %+v", tb)
	}
	like := ParseCreateTable([]byte("CREATE TABLE `copy` LIKE `t`"), Mode{}, func(n string) *core.Table {
		if n == "t" {
			return tb
		}
		return nil
	})
	if like.Name != "copy" || len(like.Columns) != 2 || &like.Columns[0] == &tb.Columns[0] {
		t.Errorf("like %+v", like)
	}
	if as := ParseCreateTable([]byte("CREATE TABLE a AS SELECT 1"), Mode{}, nil); as == nil || len(as.Columns) != 0 {
		t.Errorf("as select %+v", as)
	}
	if ParseCreateTable([]byte("CREATE VIEW v AS SELECT 1"), Mode{}, nil) != nil {
		t.Error("a view parsed as a table")
	}
}

func TestAlterTable(t *testing.T) {
	tb := ParseCreateTable([]byte("CREATE TABLE t (a int, b varchar(4), c text)"), Mode{}, nil)
	for _, c := range []struct {
		alter string
		want  string
	}{
		{"ALTER TABLE t ADD COLUMN d int", "a b c d"},
		{"ALTER TABLE `t` ADD `d` int FIRST", "d a b c"},
		{"ALTER TABLE t ADD d int AFTER a, ADD e int AFTER d", "a d e b c"},
		{"ALTER TABLE t ADD COLUMN (d int, e int)", "a b c d e"},
		{"ALTER TABLE t DROP COLUMN b", "a c"},
		{"ALTER TABLE t DROP b, DROP INDEX x", "a c"},
		{"ALTER TABLE t CHANGE b bb varchar(9)", "a bb c"},
		{"ALTER TABLE t RENAME COLUMN c TO cc", "a b cc"},
		{"/*!40000 ALTER TABLE t ADD COLUMN z int */", "a b c z"},
	} {
		got := AlterTable(tb, []byte(c.alter), Mode{})
		var names []string
		for _, col := range got.Columns {
			names = append(names, col.Name)
		}
		if strings.Join(names, " ") != c.want {
			t.Errorf("%s: columns %q, want %q", c.alter, names, c.want)
		}
		if len(tb.Columns) != 3 {
			t.Fatalf("%s changed the original table", c.alter)
		}
	}
	// phpMyAdmin's trailing keys.
	got := AlterTable(tb, []byte("ALTER TABLE `t`\n  ADD PRIMARY KEY (`a`),\n  ADD UNIQUE KEY `b` (`b`),\n  ADD KEY `c` (`c`(191))"), Mode{})
	if strings.Join(got.PrimaryKey, ",") != "a" || !got.Columns[0].Unique || !got.Columns[1].Unique || got.Columns[2].Unique {
		t.Errorf("keys %+v", got)
	}
	got = AlterTable(got, []byte("ALTER TABLE t MODIFY `b` varchar(8) NOT NULL"), Mode{})
	if got.Columns[1].MaxLen != 32 || got.Columns[1].Nullable || !got.Columns[1].Unique {
		t.Errorf("modify %+v", got.Columns[1])
	}
	if same := AlterTable(tb, []byte("ALTER TABLE t ENGINE=InnoDB"), Mode{}); same != tb {
		t.Error("a no-op alter made a new table")
	}
}

// The source follows ALTER TABLE ADD COLUMN, so an INSERT after it maps
// its values to the new column list and earlier batches keep theirs.
func TestAlterThenInsert(t *testing.T) {
	in := "CREATE TABLE `t` (`a` int, `b` int);\nINSERT INTO `t` VALUES (1,2);\nALTER TABLE `t` ADD COLUMN `e` varchar(100) AFTER `a`;\nINSERT INTO `t` VALUES (1,'x@example.test',2);\n"
	bs, err := drain(t, sourceOf([]byte(in), SourceOptions{}))
	if err != nil {
		t.Fatal(err)
	}
	if len(bs[1].Table.Columns) != 2 || bs[3].Table.Columns[1].Name != "e" || len(bs[3].Columns) != 3 {
		t.Fatalf("tables %+v %+v", bs[1].Table, bs[3].Table)
	}
}

// Rows are cut into batches near core.MaxBatchBytes at row boundaries;
// the head is on the first batch only and the tail on the last.
func TestBatchCut(t *testing.T) {
	var b bytes.Buffer
	b.WriteString("CREATE TABLE `t` (`id` int, `v` longtext);\nINSERT INTO `t` VALUES ")
	row := "'" + strings.Repeat("v", 1000) + "'"
	const rows = 10000
	for i := range rows {
		if i > 0 {
			b.WriteString(",\n")
		}
		b.WriteString("(1," + row + ")")
	}
	// One row larger than a batch, then one more small row.
	b.WriteString(",(2,'" + strings.Repeat("w", core.MaxBatchBytes+10) + "'),(3,'z')")
	b.WriteString(" ON DUPLICATE KEY UPDATE v=v;\n")
	in := b.Bytes()
	bs, err := drain(t, sourceOf(in, SourceOptions{}))
	if err != nil {
		t.Fatal(err)
	}
	ins := bs[1:]
	if len(ins) < 3 {
		t.Fatalf("%d batches", len(ins))
	}
	total := 0
	var out bytes.Buffer
	out.Write(bs[0].Pass.Text)
	for i, x := range ins {
		if x.First != (i == 0) || x.Last != (i == len(ins)-1) || (len(x.Head) > 0) != x.First || (len(x.Tail) > 0) != x.Last {
			t.Fatalf("batch %d: first %v last %v head %d tail %d", i, x.First, x.Last, len(x.Head), len(x.Tail))
		}
		size := 0
		for _, r := range x.Rows {
			size += len(r.Src) + len(r.Lead)
		}
		if size > core.MaxBatchBytes+2000 && len(x.Rows) > 1 {
			t.Fatalf("batch %d holds %d bytes in %d rows", i, size, len(x.Rows))
		}
		total += len(x.Rows)
		out.Write(x.Head)
		for _, r := range x.Rows {
			out.Write(r.Lead)
			out.Write(r.Src)
		}
		out.Write(x.Tail)
	}
	if total != rows+2 {
		t.Fatalf("%d rows", total)
	}
	if !bytes.Equal(out.Bytes(), in) {
		t.Fatal("batches don't join back to the input")
	}
	big := 0
	for _, x := range ins {
		for _, r := range x.Rows {
			if len(r.Values[1].Raw) > core.MaxBatchBytes && len(x.Rows) == 1 {
				big++
			}
		}
	}
	if big != 1 {
		t.Errorf("the large row is not a batch of its own")
	}
}

func TestDataOnlyDump(t *testing.T) {
	in := []byte("-- data only\nINSERT INTO `wp_x` VALUES (1,'a');\n")
	_, err := drain(t, sourceOf(in, SourceOptions{Name: "data.sql"}))
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Code != core.ErrDumpFormat || ce.Exit != 2 || ce.Line != 2 ||
		ce.Msg != "dump-format: data.sql: table wp_x has no CREATE TABLE before line 2; pass --schema." {
		t.Fatalf("err = %#v", err)
	}
	// With --schema it reads and the table joins the dump's schema.
	sc := &core.Schema{Tables: []*core.Table{{Name: "wp_x", Columns: []core.Column{{Name: "id"}, {Name: "v"}}}}}
	s := sourceOf(in, SourceOptions{Schema: sc})
	bs, err := drain(t, s)
	if err != nil || len(bs) != 2 || bs[1].Table.Name != "wp_x" || s.Schema().Table("wp_x") == nil {
		t.Fatalf("with --schema: %v %+v", err, bs)
	}
	// A column list is enough on its own.
	s = sourceOf([]byte("INSERT INTO `wp_y` (`id`,`email`) VALUES (1,'a@example.test');\n"), SourceOptions{})
	bs, err = drain(t, s)
	if err != nil || bs[0].Table.Col("email") != 1 {
		t.Fatalf("column list: %v", err)
	}
}

func TestRowErrors(t *testing.T) {
	for in, want := range map[string]string{
		"CREATE TABLE t (a int, b int);\nINSERT INTO t VALUES (1,2),\n(3);\n": "dump-format: prod.sql line 3: a row of t has 1 values for 2 columns.",
		"CREATE TABLE t (a int);\nINSERT INTO t (`zz`) VALUES (1);\n":         "dump-format: prod.sql line 2: the INSERT into t names column zz, which its CREATE TABLE doesn't have.",
		"CREATE TABLE t (a int);\nINSERT INTO t VALUES (1), 2;\n":             "dump-format: prod.sql line 2: a row of the INSERT doesn't start with '('.",
		"CREATE TABLE t (a int);\nINSERT INTO t VALUES ('abc":                 `dump-truncated: prod.sql ends inside table t at 50 B, with no "Dump completed" line. Export it again; the target is untouched.`,
		"SELECT 'x": `dump-truncated: prod.sql ends inside a statement at line 1 at 9 B, with no "Dump completed" line. Export it again; the target is untouched.`,
		"CREATE TABLE wp_options (a longtext);\nINSERT INTO wp_options VALUES ('" + strings.Repeat("x", 2048) + "');\n": "value-too-large: a value in wp_options at line 2 is over 1 KiB. Pass --max-value 4KiB if it's real; the target is untouched.",
		"SELECT '" + strings.Repeat("x", 2048) + "';\n":                                                                 "value-too-large: a statement at line 1 is over 1 KiB. Pass --max-value 4KiB if it's real; the target is untouched.",
	} {
		s := sourceOf([]byte(in), SourceOptions{MaxValue: 1024})
		_, err := drain(t, s)
		var ce *core.Error
		if !errors.As(err, &ce) || ce.Msg != want {
			t.Errorf("%.40q:\n got %v\nwant %s", in, err, want)
			continue
		}
		if exit, _ := core.CodeExit(ce.Code); exit != ce.Exit || ce.Line == 0 {
			t.Errorf("%s: exit %d line %d", ce.Code, ce.Exit, ce.Line)
		}
		// The error sticks.
		if _, err2 := s.Next(context.Background()); !errors.Is(err2, ce) {
			t.Errorf("second Next: %v", err2)
		}
	}
}

// The PRD 17 wording with the default limit.
func TestValueTooLargeDefault(t *testing.T) {
	le := &TooLargeError{Line: 88, Limit: DefaultMaxValue}
	s := sourceOf(nil, SourceOptions{})
	s.ins = &core.Table{Name: "wp_options"}
	err := s.fail(le)
	if err.Error() != "value-too-large: a value in wp_options at line 88 is over 256 MiB. Pass --max-value 1GiB if it's real; the target is untouched." {
		t.Errorf("%v", err)
	}
}

func TestCancel(t *testing.T) {
	var b bytes.Buffer
	genDump(&b, 20_000_000, 3)
	s := sourceOf(b.Bytes(), SourceOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n := 0
	for {
		_, err := s.Next(ctx)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("err = %v", err)
			}
			break
		}
		if n++; n == 5 {
			cancel()
		}
	}
	if n != 5 {
		t.Fatalf("stopped after %d batches, want 5", n)
	}
}

// Progress hears about each table once, in order.
func TestProgressTables(t *testing.T) {
	p := &tableProgress{}
	if _, err := drain(t, sourceOf(readFixture(t, "mysqldump8.sql"), SourceOptions{Progress: p})); err != nil {
		t.Fatal(err)
	}
	if strings.Join(p.tables, ",") != "wp_users,wp_options" {
		t.Errorf("tables %q", p.tables)
	}
}

type tableProgress struct{ tables []string }

func (p *tableProgress) Phase(string, int64)        {}
func (p *tableProgress) Advance(int64)              {}
func (p *tableProgress) Table(name string, _ int64) { p.tables = append(p.tables, name) }
func (p *tableProgress) Note(string)                {}

// With no value changed, the batches written back are the input byte for
// byte (here through the test sink's minimal writer; writer_test.go does
// the same through the real one).
func TestIdentityThroughFakeSink(t *testing.T) {
	for _, name := range fixtures(t) {
		in := readFixture(t, name)
		bs, err := drain(t, sourceOf(in, SourceOptions{}))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		sink := testutil.FakeSink()
		for _, b := range bs {
			if err := sink.Write(context.Background(), b); err != nil {
				t.Fatal(err)
			}
		}
		if !bytes.Equal(sink.Bytes(), in) {
			t.Errorf("%s: identity output differs", name)
		}
	}
}

func BenchmarkScan(b *testing.B) {
	path := benchDump(b)
	fi, _ := os.Stat(path)
	b.SetBytes(fi.Size())
	b.ResetTimer()
	start := cpuTime()
	defer func() { cpuRate(b, start, fi.Size()) }()
	for range b.N {
		f, err := os.Open(path)
		if err != nil {
			b.Fatal(err)
		}
		s := NewSource(f, SourceOptions{})
		rows := 0
		for {
			bt, err := s.Next(context.Background())
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				b.Fatal(err)
			}
			rows += len(bt.Rows)
		}
		f.Close()
		if rows == 0 {
			b.Fatal("no rows")
		}
	}
}
