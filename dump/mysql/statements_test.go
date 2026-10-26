// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package mysql

import (
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
)

func TestBody(t *testing.T) {
	for in, want := range map[string]string{
		"/*!40101 SET NAMES utf8mb4 */;":                                  "SET NAMES utf8mb4  ;",
		"/*!50000 GRANT ALL ON *.* TO x */;":                              "GRANT ALL ON *.* TO x  ;",
		"/*M!100616 SET NOTE_VERBOSITY=0 */;":                             "SET NOTE_VERBOSITY=0  ;",
		"/*!50003 CREATE*/ /*!50017 DEFINER=`a`@`b`*/ /*!50003 TRIGGER x": "CREATE    DEFINER=`a`@`b`    TRIGGER x",
		"/* plain */ SELECT 1; -- note\n":                                 "SELECT 1;",
		"SELECT '/*!50000 not a marker */', \"--x\";":                     "SELECT '/*!50000 not a marker */', \"--x\";",
		"# hash\nSELECT 'a\\'b' # tail":                                   "SELECT 'a\\'b'",
		"/*! SET x=1 */;":                                                 "SET x=1  ;",
		"/*!40000 never closed":                                           "never closed",
		"SELECT 1 */":                                                     "SELECT 1 */",
	} {
		if got := string(Body([]byte(in), Mode{})); got != want {
			t.Errorf("Body(%q) = %q, want %q", in, got, want)
		}
	}
	// Under NO_BACKSLASH_ESCAPES a trailing backslash ends nothing.
	if got := string(Body([]byte(`SELECT 'C:\' /* c */;`), Mode{NoBackslashEscapes: true})); got != `SELECT 'C:\'  ;` {
		t.Errorf("no backslash: %q", got)
	}
}

func TestClassify(t *testing.T) {
	for in, want := range map[string]struct {
		kind  core.StmtKind
		table string
	}{
		"CREATE TABLE `wp_users` (a int);":              {core.StmtCreateTable, "wp_users"},
		"create table if not exists shop.wp_x (a int);": {core.StmtCreateTable, "wp_x"},
		"CREATE TEMPORARY TABLE t (a int);":             {core.StmtCreateTable, "t"},
		"/*!50001 CREATE ALGORITHM=UNDEFINED */ /*!50013 DEFINER=`root`@`localhost` SQL SECURITY DEFINER */ /*!50001 VIEW `v` AS select 1 */;": {core.StmtRoutine, ""},
		"CREATE DEFINER=`root`@`%` PROCEDURE `p`() BEGIN END":    {core.StmtRoutine, ""},
		"CREATE DEFINER=CURRENT_USER() FUNCTION f() RETURNS int": {core.StmtRoutine, ""},
		"CREATE OR REPLACE VIEW v AS SELECT 1;":                  {core.StmtRoutine, ""},
		"CREATE EVENT e ON SCHEDULE EVERY 1 DAY DO SELECT 1;":    {core.StmtRoutine, ""},
		"CREATE UNIQUE INDEX i ON t (a);":                        {core.StmtCreateIndex, ""},
		"CREATE SEQUENCE s;":                                     {core.StmtSequence, ""},
		"CREATE DATABASE x;":                                     {core.StmtOther, ""},
		"CREATE USER x;":                                         {core.StmtOther, ""},
		"DROP TABLE IF EXISTS `wp_users`;":                       {core.StmtDropTable, "wp_users"},
		"DROP TEMPORARY TABLE t;":                                {core.StmtDropTable, "t"},
		"/*!50001 DROP VIEW IF EXISTS `v`*/;":                    {core.StmtOther, ""},
		"LOCK TABLES `wp_users` WRITE;":                          {core.StmtLock, "wp_users"},
		"UNLOCK TABLES;":                                         {core.StmtUnlock, ""},
		"/*!40000 ALTER TABLE `wp_users` DISABLE KEYS */;":       {core.StmtAlterKeys, "wp_users"},
		"ALTER TABLE `db`.`t` ENABLE KEYS;":                      {core.StmtAlterKeys, "t"},
		"ALTER TABLE t ADD PRIMARY KEY (id);":                    {KindAlterTable, "t"},
		"SET NAMES utf8mb4;":                                     {core.StmtSet, ""},
		"START TRANSACTION;":                                     {core.StmtTransaction, ""},
		"BEGIN;":                                                 {core.StmtTransaction, ""},
		"COMMIT;":                                                {core.StmtTransaction, ""},
		"USE shop;":                                              {KindUse, ""},
		"INSERT INTO t SELECT 1;":                                {core.StmtOther, ""},
		"GRANT ALL ON *.* TO x;":                                 {core.StmtOther, ""},
		"SELECT 1 INTO OUTFILE '/tmp/x';":                        {core.StmtOther, ""},
		"-- only a comment\n":                                    {core.StmtComment, ""},
		"":                                                       {core.StmtComment, ""},
	} {
		k, table := Classify([]byte(in), Mode{})
		if k != want.kind || table != want.table {
			t.Errorf("Classify(%q) = %s %q, want %s %q", in, k, table, want.kind, want.table)
		}
	}
}

func TestSQLMode(t *testing.T) {
	nb := Mode{NoBackslashEscapes: true}
	aq := Mode{ANSIQuotes: true}
	both := Mode{NoBackslashEscapes: true, ANSIQuotes: true}
	unk := func(m Mode) Mode { m.Unknown = true; return m }
	for _, c := range []struct {
		in       string
		cur, out Mode
	}{
		// Positive: the forms dump tools write are followed.
		{"SET sql_mode='NO_BACKSLASH_ESCAPES'", Mode{}, nb},
		{"SET @OLD_SQL_MODE=@@SQL_MODE, SQL_MODE='NO_AUTO_VALUE_ON_ZERO,NO_BACKSLASH_ESCAPES'", Mode{}, nb},
		{"SET SESSION sql_mode = 'ANSI'", Mode{}, aq},
		{"SET @@session.sql_mode = \"ANSI_QUOTES,NO_BACKSLASH_ESCAPES\"", Mode{}, both},
		{"SET sql_mode = ''", both, Mode{}},
		{"SET sql_mode = DEFAULT", both, Mode{}},
		{"SET sql_mode := @@sql_mode", nb, nb},
		{"/*!40101 SET SQL_MODE='NO_BACKSLASH_ESCAPES' */", Mode{}, nb},
		{"/*!50003 SET sql_mode = 'ONLY_FULL_GROUP_BY,NO_AUTO_CREATE_USER' */ ;", Mode{}, Mode{}},
		{"SET GLOBAL sql_mode = 'NO_BACKSLASH_ESCAPES'", Mode{}, Mode{}},
		{"SET @x = 'NO_BACKSLASH_ESCAPES'", Mode{}, Mode{}},
		{"SET NAMES utf8mb4", nb, nb},
		{"SELECT 'NO_BACKSLASH_ESCAPES'", Mode{}, Mode{}},
		{"SET SQL_MODE=@NEVER_SET", nb, nb}, // NULL: the server rejects the SET
		// Negative: anything Roadie can't follow exactly is unknown.
		{"SET sql_mode = CONCAT(@@sql_mode, ',ANSI_QUOTES')", nb, unk(nb)},
		{"SET sql_mode = CONCAT('NO_BACKSLASH','_ESCAPES')", Mode{}, unk(Mode{})},
		{"SET sql_mode = 'NO_BACKSLASH' '_ESCAPES'", Mode{}, unk(Mode{})},
		{"SET sql_mode = 0x4E4F5F4241434B534C4153485F45534341504553", Mode{}, unk(Mode{})},
		{"SET sql_mode = X'4E4F5F4241434B534C4153485F45534341504553'", Mode{}, unk(Mode{})},
		{"SET sql_mode = REPLACE('NO_BACKSLASH_XSCAPES','X','E')", Mode{}, unk(Mode{})},
		{"SET sql_mode = 2097152", Mode{}, unk(Mode{})},
		{"SET sql_mode = '2097152'", Mode{}, unk(Mode{})},
		{"SET sql_mode = _utf8mb4'NO_BACKSLASH_ESCAPES'", Mode{}, unk(Mode{})},
		{"SET sql_mode = 'NO\\_BACKSLASH_ESCAPES'", Mode{}, unk(Mode{})},
		{"SET sql_mode = 'ANSI_QUOTES, NO_BACKSLASH_ESCAPES'", Mode{}, unk(Mode{})},
		{"SET sql_mode = 'NOT_ANSI_REALLY'", Mode{}, unk(Mode{})},
		{"SET sql_mode = 'ORACLE'", Mode{}, unk(Mode{})},
		{"SET sql_mode = 'TRADITIONAL', NAMES utf8", nb, unk(nb)},
		{"SET sql_mode = 'NO_BACKSLASH_ESCAPES', time_zone = 'x'", Mode{}, unk(Mode{})},
		{"SET sql_mode = 'NO_BACKSLASH_ESCAPES,NO_AUTO_CREATE_USER'", Mode{}, unk(Mode{})},
		{"/*!99999 SET sql_mode='NO_BACKSLASH_ESCAPES' */", Mode{}, unk(Mode{})},
		{"/*!80000 SET sql_mode='' */", nb, unk(nb)},
		{"/*M!100100 SET sql_mode='NO_BACKSLASH_ESCAPES' */", Mode{}, unk(Mode{})},
		{"/*!500001 SET sql_mode='NO_BACKSLASH_ESCAPES' */", Mode{}, unk(Mode{})},
		{"SET GLOBAL max_connections = 10, sql_mode = 'NO_BACKSLASH_ESCAPES'", Mode{}, Mode{}},
		{"SET sql_mode = 'NO_BACKSLASH_ESCAPES', sql_mode = @NEVER_SET", Mode{}, unk(Mode{})},
		{"SET sql_mode = \"NO_BACKSLASH_ESCAPES\"", aq, unk(aq)},
	} {
		if got := SQLMode([]byte(c.in), c.cur, nil); got != c.out {
			t.Errorf("SQLMode(%q, %+v) = %+v, want %+v", c.in, c.cur, got, c.out)
		}
	}
}

// TestSQLModeVars follows sql_mode through the user variables a dump
// saves it in, statement after statement.
func TestSQLModeVars(t *testing.T) {
	nb := Mode{NoBackslashEscapes: true}
	unk := Mode{Unknown: true}
	for _, c := range []struct {
		name  string
		stmts []string
		want  Mode
	}{
		{"mysqldump save and restore", []string{
			"/*!40101 SET @OLD_SQL_MODE=@@SQL_MODE, SQL_MODE='NO_BACKSLASH_ESCAPES' */;",
			"/*!40101 SET SQL_MODE=@OLD_SQL_MODE */;",
		}, Mode{}},
		{"trigger save and restore", []string{
			"/*!50003 SET @saved_sql_mode = @@sql_mode */ ;",
			"/*!50003 SET sql_mode = 'NO_BACKSLASH_ESCAPES' */ ;",
			"/*!50003 SET sql_mode = @saved_sql_mode */ ;",
		}, Mode{}},
		{"literal in a variable", []string{"SET @m = 'NO_BACKSLASH_ESCAPES';", "SET sql_mode = @m;"}, nb},
		{"quoted variable name", []string{"SET @`M` = 'NO_BACKSLASH_ESCAPES';", "SET sql_mode = @'m';"}, nb},
		{"IFNULL of a set variable", []string{"SET @OLD_SQL_MODE = 'NO_BACKSLASH_ESCAPES';", "SET SESSION sql_mode = IFNULL(@OLD_SQL_MODE, '');"}, nb},
		{"IFNULL of an unset variable", []string{"SET sql_mode = 'NO_BACKSLASH_ESCAPES';", "SET SESSION sql_mode = IFNULL(@OLD_SQL_MODE, '');"}, Mode{}},
		{"a saved literal is followed", []string{
			"/*!40101 SET @OLD_SQL_MODE='NO_BACKSLASH_ESCAPES' */;",
			"/*!40101 SET SQL_MODE=@OLD_SQL_MODE */;",
		}, nb},
		// Negative.
		{"gated variable", []string{"/*!99999 SET @m = 'NO_BACKSLASH_ESCAPES' */;", "SET sql_mode = @m;"}, unk},
		{"variable from a query", []string{"SET @m = CONCAT('NO_BACKSLASH','_ESCAPES');", "SET sql_mode = @m;"}, unk},
		{"variable set in the same statement", []string{"SET @m = 'NO_BACKSLASH_ESCAPES', sql_mode = @m;"}, unk},
		{"variable set beside another setting", []string{"SET @m = 'NO_BACKSLASH_ESCAPES', time_zone = '+00:00';", "SET sql_mode = @m;"}, unk},
		{"variable Roadie can't name", []string{"SET @'a''b' = 'x';", "SET sql_mode = @OLD;"}, unk},
	} {
		var v Vars
		m := Mode{}
		for _, s := range c.stmts {
			m = SQLMode([]byte(s), m, &v)
		}
		if m != c.want {
			t.Errorf("%s: mode %+v, want %+v", c.name, m, c.want)
		}
	}
}

func TestSafeExpr(t *testing.T) {
	for _, s := range []string{"1 + 2", "'a' 'b'", "-(1)", "TRUE", "null", "DEFAULT", "X'41' + 0", "b'01'", "'it''s' 'x\\'y'", "1.5e3 * 2", "1e+5", "0x41", "NOT false", "CURRENT_TIMESTAMP"} {
		if !SafeExpr([]byte(s), Mode{}) {
			t.Errorf("%q refused", s)
		}
	}
	for _, s := range []string{"LOAD_FILE('/etc/passwd')", "(SELECT 1)", "`col`", "@a", "sys_exec('id')", "NOW()", "now ()", "/*!50000 x */ 1", "1 -- x", "1 # x", "'open", "1abc", "0xZZ", "_binary 'x' collate x"} {
		if SafeExpr([]byte(s), Mode{}) {
			t.Errorf("%q accepted", s)
		}
	}
	// Under ANSI_QUOTES "x" is a column.
	if SafeExpr([]byte(`"x" + 1`), Mode{ANSIQuotes: true}) {
		t.Error(`"x" accepted under ANSI_QUOTES`)
	}
	if !SafeExpr([]byte(`"x" 'y'`), Mode{}) {
		t.Error(`"x" 'y' refused`)
	}
}

func FuzzSafeExpr(f *testing.F) {
	for _, s := range []string{"1 + 2", "LOAD_FILE('x')", "'a\\'", "/*", "X'", "`"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if SafeExpr([]byte(s), Mode{}) && (strings.Contains(strings.ToLower(s), "load_file(") || strings.ContainsAny(s, "`@#")) {
			// A call or a name can only be accepted inside a string.
			for _, q := range []string{"'", "\""} {
				if strings.Contains(s, q) {
					return
				}
			}
			t.Fatalf("%q accepted", s)
		}
	})
}
