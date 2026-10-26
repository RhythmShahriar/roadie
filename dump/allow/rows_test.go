// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package allow

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/dump/mysql"
)

// judgeDump reads a MySQL dump with the source and passes every batch
// through the filter as dump.Open does. It returns the first error.
func judgeDump(text string, o Options) error {
	src := mysql.NewSource(bytes.NewReader([]byte(text)), mysql.SourceOptions{Name: "prod.sql"})
	fl := NewFilter(o)
	for {
		b, err := src.Next(context.Background())
		if errors.Is(err, io.EOF) {
			return fl.Finish()
		}
		if err != nil {
			return err
		}
		if b.Pass == nil {
			if err := fl.Rows(b, src.InsertLine()); err != nil {
				return err
			}
		}
		if _, err := fl.Add(b, src.Lexer().Mode()); err != nil {
			return err
		}
	}
}

const rowsTable = "CREATE TABLE `t` (`a` int, `b` text);\n"

// The text before the first row and after the last one is judged: a
// client command, a function call, a subquery or a table with its
// database there is refused at the statement's line.
func TestRowsRefused(t *testing.T) {
	for _, c := range []struct{ text, want string }{
		{"INSERT INTO `t` VALUES (1,'x')\n\\! id\n;", "line 2 holds a mysql client command, which would run on the machine that imports the dump."},
		{"INSERT INTO `t` VALUES (1,'x') /*! \\! id */;", "holds a mysql client command"},
		{"INSERT INTO `t` VALUES (1,'x') /*!50000 \\! id */;", "holds a mysql client command"},
		{"INSERT INTO t VALUES (1,'x') ON DUPLICATE KEY UPDATE b = LOAD_FILE('/etc/passwd');", "holds a call to load_file()"},
		{"INSERT INTO t VALUES (1,'x') ON DUPLICATE KEY UPDATE b = sys_exec('id');", "holds a call to sys_exec()"},
		{"INSERT INTO t VALUES (1,'x') ON DUPLICATE KEY UPDATE b = (SELECT 1 INTO OUTFILE '/tmp/x');", "holds a SELECT ... INTO OUTFILE statement"},
		{"INSERT INTO t VALUES (1,'x') ON DUPLICATE KEY UPDATE b = (SELECT authentication_string FROM mysql.user LIMIT 1);",
			"holds an INSERT statement with more after its rows than ON DUPLICATE KEY UPDATE col = VALUES(col), which would change rows outside the dump's INSERTs"},
		{"INSERT INTO t VALUES (1,'x') ON DUPLICATE KEY UPDATE b = b + 1;", "holds an INSERT statement with more after its rows"},
		{"INSERT INTO t VALUES (1,'x') ON DUPLICATE KEY UPDATE b = VALUES(a) + 1;", "holds an INSERT statement with more after its rows"},
		{"INSERT INTO t VALUES (1,'x') ON DUPLICATE KEY UPDATE b = VALUES(b),;", "holds an INSERT statement with more after its rows"},
		{"REPLACE INTO t VALUES (1,'x') AS n(a) b = 1;", "holds a REPLACE statement with more after its rows"},
		{"INSERT INTO t VALUES (1,'x') RETURNING b;", "holds an INSERT statement with more after its rows"},
		{"INSERT INTO `prod`.`t` VALUES (1,'pwned');", "holds an INSERT statement that names a table with its database, which would send the import to a database --to doesn't name."},
		{"REPLACE INTO mysql.t VALUES (1,'x');", "holds a REPLACE statement that names a table with its database"},
		{"INSERT IGNORE INTO `shop`.`t` VALUES (1,'x');", "holds an INSERT statement that names a table with its database"},
	} {
		err := judgeDump(rowsTable+c.text, Options{Name: "prod.sql", Database: "shop"})
		var ce *core.Error
		if !errors.As(err, &ce) || ce.Code != core.ErrDumpRefused || ce.Exit != 3 || ce.Line != 2 {
			t.Errorf("%q: %v", c.text, err)
			continue
		}
		if !strings.HasPrefix(ce.Msg, "dump-refused: prod.sql line 2 holds ") || !strings.Contains(ce.Msg, c.want) {
			t.Errorf("%q:\n got %s\nwant %s", c.text, ce.Msg, c.want)
		}
		for _, secret := range []string{"/etc/passwd", "pwned", "authentication_string", "/tmp/x"} {
			if strings.Contains(ce.Msg, secret) {
				t.Errorf("%q: message quotes the statement: %s", c.text, ce.Msg)
			}
		}
	}
}

// The heads and tails dump tools write are kept.
func TestRowsKept(t *testing.T) {
	for _, text := range []string{
		"INSERT INTO `t` VALUES (1,'x'),(2,'y');\n",
		"INSERT INTO `t` (`a`, `b`) VALUES\n(1, 'x'),\n(2, 'y');\n",
		"INSERT IGNORE INTO t VALUES (1,'x');",
		"INSERT LOW_PRIORITY INTO `t` VALUE (1,'x') /* note */;",
		"REPLACE t VALUES (1,'x');",
		"INSERT INTO t VALUES (1,'x') ON DUPLICATE KEY UPDATE b = VALUES(b), `a` = VALUES(`a`);",
		"INSERT INTO t (a,b) VALUES (1,'x'),(2,'y') AS new ON DUPLICATE KEY UPDATE b = new.b, a = new.a;",
		"INSERT INTO t VALUES (1,'x') AS new(m,n) ON DUPLICATE KEY UPDATE b = n, t.a = new.m;",
		"/* phpMyAdmin */ INSERT INTO `t` (`a`, `b`) VALUES (1, 'x')\n-- end\n;",
	} {
		if err := judgeDump(rowsTable+text, Options{Name: "prod.sql", Database: "shop"}); err != nil {
			t.Errorf("%q: %v", text, err)
		}
	}
}

// Rows leaves PostgreSQL batches and passthrough statements alone.
func TestRowsSkips(t *testing.T) {
	f := NewFilter(Options{Dialect: core.Postgres})
	b := &core.Batch{First: true, Last: true, Head: []byte("COPY t (a) FROM stdin;\n"), Tail: []byte("\\.\n")}
	if err := f.Rows(b, 1); err != nil {
		t.Fatal(err)
	}
	f = NewFilter(Options{})
	if err := f.Rows(&core.Batch{Pass: &core.Statement{Text: []byte("\\! id")}}, 1); err != nil {
		t.Fatal(err)
	}
}
