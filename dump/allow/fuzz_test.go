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

var mysqlModeZero mysql.Mode

// The cores of statements that are never kept: refused or dropped.
var (
	mysqlBad = []string{
		"GRANT ALL ON *.* TO x",
		"CREATE TRIGGER t AFTER UPDATE ON wp_users FOR EACH ROW SET @x = 1",
		"CREATE DEFINER=`root`@`%` PROCEDURE p() SELECT 1",
		"CREATE VIEW v AS SELECT 1",
		"LOAD DATA LOCAL INFILE '/etc/passwd' INTO TABLE t",
		"SELECT 1 INTO OUTFILE '/tmp/x'",
		"CREATE DATABASE x",
		"DROP DATABASE x",
		"USE mysql",
		"CREATE EVENT e ON SCHEDULE EVERY 1 DAY DO DELETE FROM wp_users",
		"CREATE USER x IDENTIFIED BY 'y'",
		"SET GLOBAL general_log = 1",
		"SET @x = (SELECT 1)",
		"CALL p()",
		"UPDATE wp_users SET user_email = 'x'",
		"system id",
		"source /etc/passwd",
		"CREATE TABLE t (a int) ENGINE=FEDERATED",
		"SET NAMES utf8mb4, @@GLOBAL.general_log = 1",
		"SET NAMES utf8mb4, GLOBAL general_log = 1",
		"SET CHARACTER SET utf8, @@global.general_log_file = 'x'",
		"CREATE TABLE IF NOT EXISTS `prod`.`wp_options` (a int)",
		"CREATE TABLE v LIKE mysql.user",
		"ALTER TABLE t ADD COLUMN c int, RENAME TO mysql.user",
		"CREATE INDEX i ON mysql.user (a)",
		"DROP TABLE IF EXISTS prod.t",
		"LOCK TABLES t WRITE, prod.t WRITE",
	}
	// Row statements whose head or tail is never kept.
	mysqlBadRows = []string{
		"INSERT INTO t (a) VALUES (1)\n\\! id\n",
		"INSERT INTO t (a) VALUES (1) /*! \\! id */",
		"INSERT INTO t (a) VALUES (1) ON DUPLICATE KEY UPDATE a = LOAD_FILE('/etc/passwd')",
		"INSERT INTO t (a) VALUES (1) ON DUPLICATE KEY UPDATE a = sys_exec('id')",
		"INSERT INTO t (a) VALUES (1) ON DUPLICATE KEY UPDATE a = (SELECT 1 INTO OUTFILE '/tmp/x')",
		"INSERT INTO t (a) VALUES (1) ON DUPLICATE KEY UPDATE a = (SELECT 1 FROM mysql.user)",
		"REPLACE INTO prod.t (a) VALUES (1)",
		"INSERT IGNORE INTO `prod`.`t` (a) VALUES (1)",
	}
	pgBad = []string{
		"COPY t FROM PROGRAM 'id'",
		"CREATE FUNCTION f() RETURNS int LANGUAGE sql AS $$ SELECT 1 $$",
		"DO $$ BEGIN PERFORM 1; END $$",
		"CREATE SUBSCRIPTION s CONNECTION 'host=x' PUBLICATION p",
		"GRANT ALL ON t TO x",
		"ALTER TABLE t OWNER TO x",
		"CREATE TRIGGER t AFTER UPDATE ON u FOR EACH ROW EXECUTE FUNCTION f()",
		"SET log_statement = 'all'",
	}
)

// wrap changes a statement only in ways the server ignores: the case of
// keywords, comments, whitespace and (MySQL) versioned comment markers.
func wrap(core string, ops []byte, pg bool) string {
	s := []byte(core)
	// Case first, on the statement alone: the markers /*M! and /*! are
	// case sensitive and a flipped one is a plain comment to the server.
	for i := 0; i+1 < len(ops); i += 2 {
		if ops[i]%8 <= 1 {
			p := int(ops[i+1]) % len(s)
			if c := s[p] | 0x20; c >= 'a' && c <= 'z' {
				s[p] ^= 0x20
			}
		}
	}
	for i := 0; i+1 < len(ops); i += 2 {
		op, at := ops[i]%8, int(ops[i+1])
		switch op {
		case 2: // a block comment where a space is
			if p := spaceAt(s, at); p >= 0 {
				s = append(s[:p:p], append([]byte(" /* c; */ "), s[p+1:]...)...)
			}
		case 3: // a line comment first
			s = append([]byte("-- note; x\n"), s...)
		case 4:
			if pg {
				s = append([]byte("/* a /* nested */ one */\n"), s...)
			} else {
				s = append([]byte("# note\n"), s...)
			}
		case 5: // a versioned comment around the whole statement
			if !pg {
				s = append(append([]byte("/*!50000 "), s...), " */"...)
			}
		case 6:
			if !pg {
				s = append(append([]byte("/*M!100100 "), s...), " */"...)
			}
		case 7: // a newline and a tab for a space
			if p := spaceAt(s, at); p >= 0 {
				s = append(s[:p:p], append([]byte("\n\t"), s[p+1:]...)...)
			}
		}
	}
	return string(s)
}

// spaceAt returns the index of the at-th space (mod the count) outside
// plain block comments, or -1. A comment put inside a plain one would
// end it early and leave a stray "*/" the server refuses as a syntax
// error, so the wrapped statement would no longer be one the server
// runs. Versioned comments are code: spaces in them count.
func spaceAt(s []byte, at int) int {
	var idx []int
	for i := 0; i < len(s); i++ {
		rest := s[i:]
		switch {
		case bytes.HasPrefix(rest, []byte("/*")) && !bytes.HasPrefix(rest, []byte("/*!")) && !bytes.HasPrefix(rest, []byte("/*M!")):
			end := bytes.Index(rest[2:], []byte("*/"))
			if end < 0 {
				i = len(s)
			} else {
				i += end + 3
			}
		case s[i] == ' ':
			idx = append(idx, i)
		}
	}
	if len(idx) == 0 {
		return -1
	}
	return idx[at%len(idx)]
}

// FuzzAllow: a statement that is refused or dropped is never kept,
// whatever comments, markers or case wrap it, whatever kind the reader
// gave it and whether it is judged alone or after the lexer split it.
// With rows set the cores are row statements whose head or tail must
// be refused.
func FuzzAllow(f *testing.F) {
	f.Add(uint8(0), []byte{}, false, uint8(0), false)
	f.Add(uint8(0), []byte{5, 0, 0, 3, 2, 1}, false, uint8(3), false)
	f.Add(uint8(1), []byte{6, 0, 4, 0, 7, 2}, false, uint8(9), false)
	f.Add(uint8(2), []byte{2, 3, 4, 0}, true, uint8(1), false)
	f.Add(uint8(0), []byte{2, 3, 7, 1}, false, uint8(0), true)
	f.Add(uint8(1), []byte{0, 4, 1, 9}, false, uint8(0), true)
	f.Fuzz(func(t *testing.T, which uint8, ops []byte, pg bool, kind uint8, rows bool) {
		cores, dialect := mysqlBad, core.MySQL
		switch {
		case pg:
			cores, dialect = pgBad, core.Postgres
		case rows:
			cores = mysqlBadRows
		}
		text := wrap(cores[int(which)%len(cores)], ops, pg) + ";"
		kinds := []core.StmtKind{core.StmtOther, core.StmtComment, core.StmtSet, core.StmtCreateTable, core.StmtTransaction,
			mysql.KindDelimiter, mysql.KindClient, mysql.KindUse, core.StmtRoutine}
		o := Options{Name: "x.sql", Dialect: dialect, Database: "shop"}
		st := core.Statement{Kind: kinds[int(kind)%len(kinds)], Text: []byte(text), Line: 1}
		if d, _ := Check(st, o); d.Verdict == Keep {
			t.Fatalf("kept %q as %s", text, st.Kind)
		}
		if !pg && allKept(t, text, o, rows) {
			t.Fatalf("every statement of %q kept after the lexer", text)
		}
	})
}

// allKept runs text through the MySQL lexer and the filter and reports
// whether nothing was dropped or refused. rows says whether the text may
// be read as rows.
func allKept(t *testing.T, text string, o Options, rows bool) bool {
	src := mysql.NewSource(bytes.NewReader([]byte(text)), mysql.SourceOptions{Name: "x.sql"})
	fl := NewFilter(o)
	for {
		b, err := src.Next(context.Background())
		if errors.Is(err, io.EOF) {
			return fl.Finish() == nil
		}
		if err != nil {
			return false // the reader refused it
		}
		if b.Pass == nil && fl.Rows(b, src.InsertLine()) != nil {
			return false
		}
		out, err := fl.Add(b, src.Lexer().Mode())
		if err != nil || (b.Pass != nil && out == nil) {
			return false
		}
		if b.Pass == nil && !rows {
			// Row data reached the output: only if the core was an
			// INSERT, which none of them is.
			t.Fatalf("%q read as rows", text)
		}
	}
}

// FuzzSetGlobal: a SET that holds a GLOBAL or PERSIST assignment in any
// clause is never kept, whichever clause it is and whatever the others
// are (SET NAMES first included).
func FuzzSetGlobal(f *testing.F) {
	f.Add([]byte{0, 3}, uint8(1), uint8(0), []byte{})
	f.Add([]byte{1, 4}, uint8(0), uint8(1), []byte{5, 0})
	f.Add([]byte{2}, uint8(2), uint8(2), []byte{2, 1, 7, 0})
	plain := []string{"NAMES utf8mb4", "CHARACTER SET utf8", "CHARSET latin1", "NAMES utf8mb4 COLLATE utf8mb4_unicode_ci",
		"@x = 1", "character_set_client = utf8mb4", "SESSION sql_mode = ''", "@@SESSION.time_zone = '+00:00'"}
	globals := []string{"GLOBAL general_log = 1", "@@GLOBAL.general_log = 1", "@@global.general_log_file = 'x'",
		"PERSIST local_infile = 1", "@@persist_only.local_infile = 1", "global sql_mode = ''"}
	f.Fuzz(func(t *testing.T, picks []byte, at, which uint8, ops []byte) {
		var cs []string
		for i, p := range picks {
			if i == 4 {
				break
			}
			cs = append(cs, plain[int(p)%len(plain)])
		}
		pos := int(at) % (len(cs) + 1)
		cs = append(cs[:pos:pos], append([]string{globals[int(which)%len(globals)]}, cs[pos:]...)...)
		text := wrap("SET "+strings.Join(cs, ", "), ops, false) + ";"
		o := Options{Name: "x.sql", Database: "shop"}
		if d, _ := Check(core.Statement{Kind: core.StmtSet, Text: []byte(text), Line: 1}, o); d.Verdict == Keep {
			t.Fatalf("kept %q", text)
		}
		if allKept(t, text, o, false) {
			t.Fatalf("every statement of %q kept after the lexer", text)
		}
	})
}
