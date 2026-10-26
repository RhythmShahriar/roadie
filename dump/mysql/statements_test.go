// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package mysql

import (
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
	for _, c := range []struct {
		in       string
		cur, out Mode
	}{
		{"SET sql_mode='NO_BACKSLASH_ESCAPES'", Mode{}, nb},
		{"SET @OLD_SQL_MODE=@@SQL_MODE, SQL_MODE='NO_AUTO_VALUE_ON_ZERO,NO_BACKSLASH_ESCAPES'", Mode{}, nb},
		{"SET SQL_MODE=@OLD_SQL_MODE", nb, Mode{}},
		{"SET SESSION sql_mode = 'ANSI'", Mode{}, aq},
		{"SET @@session.sql_mode = \"ANSI_QUOTES,NO_BACKSLASH_ESCAPES\"", Mode{}, both},
		{"SET sql_mode = CONCAT(@@sql_mode, ',ANSI_QUOTES')", nb, both},
		{"SET sql_mode = ''", both, Mode{}},
		{"SET GLOBAL sql_mode = 'NO_BACKSLASH_ESCAPES'", Mode{}, Mode{}},
		{"SET @x = 'NO_BACKSLASH_ESCAPES'", Mode{}, Mode{}},
		{"SET NAMES utf8mb4", nb, nb},
		{"SET sql_mode = 'TRADITIONAL', NAMES utf8", nb, Mode{}},
		{"SELECT 'NO_BACKSLASH_ESCAPES'", Mode{}, Mode{}},
		{"SET sql_mode = 'NOT_ANSI_REALLY'", Mode{}, Mode{}},
	} {
		if got := SQLMode([]byte(c.in), c.cur); got != c.out {
			t.Errorf("SQLMode(%q, %+v) = %+v, want %+v", c.in, c.cur, got, c.out)
		}
	}
}
