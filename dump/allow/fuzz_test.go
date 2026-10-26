// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package allow

import (
	"bytes"
	"context"
	"errors"
	"io"
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

// spaceAt returns the index of the at-th space (mod the count), or -1.
func spaceAt(s []byte, at int) int {
	var idx []int
	for i, c := range s {
		if c == ' ' {
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
func FuzzAllow(f *testing.F) {
	f.Add(uint8(0), []byte{}, false, uint8(0))
	f.Add(uint8(0), []byte{5, 0, 0, 3, 2, 1}, false, uint8(3))
	f.Add(uint8(1), []byte{6, 0, 4, 0, 7, 2}, false, uint8(9))
	f.Add(uint8(2), []byte{2, 3, 4, 0}, true, uint8(1))
	f.Fuzz(func(t *testing.T, which uint8, ops []byte, pg bool, kind uint8) {
		cores, dialect := mysqlBad, core.MySQL
		if pg {
			cores, dialect = pgBad, core.Postgres
		}
		text := wrap(cores[int(which)%len(cores)], ops, pg) + ";"
		kinds := []core.StmtKind{core.StmtOther, core.StmtComment, core.StmtSet, core.StmtCreateTable, core.StmtTransaction,
			mysql.KindDelimiter, mysql.KindClient, mysql.KindUse, core.StmtRoutine}
		o := Options{Name: "x.sql", Dialect: dialect, Database: "shop"}
		st := core.Statement{Kind: kinds[int(kind)%len(kinds)], Text: []byte(text), Line: 1}
		if d, _ := Check(st, o); d.Verdict == Keep {
			t.Fatalf("kept %q as %s", text, st.Kind)
		}
		if !pg && allKept(t, text, o) {
			t.Fatalf("every statement of %q kept after the lexer", text)
		}
	})
}

// allKept runs text through the MySQL lexer and the filter and reports
// whether nothing was dropped or refused.
func allKept(t *testing.T, text string, o Options) bool {
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
		out, err := fl.Add(b, src.Lexer().Mode())
		if err != nil || (b.Pass != nil && out == nil) {
			return false
		}
		if b.Pass == nil {
			// Row data reached the output: only if the core was an
			// INSERT, which none of them is.
			t.Fatalf("%q read as rows", text)
		}
	}
}
