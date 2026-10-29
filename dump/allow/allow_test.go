// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package allow

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
	"github.com/RhythmShahriar/roadie/dump/mysql"
)

// check judges one MySQL statement given as text.
func check(text string, o Options) (Decision, error) {
	kind, _ := mysql.Classify([]byte(text), o.Mode)
	if strings.HasPrefix(strings.TrimSpace(text), "\\") {
		kind = mysql.KindClient
	}
	return Check(core.Statement{Kind: kind, Text: []byte(text), Line: 4120}, o)
}

// statements reads a MySQL dump with the lexer and returns every
// passthrough statement with the sql_mode after it.
func statements(t testing.TB, in []byte) ([]*core.Batch, []mysql.Mode) {
	t.Helper()
	src := mysql.NewSource(bytes.NewReader(in), mysql.SourceOptions{Name: "prod.sql"})
	var bs []*core.Batch
	var modes []mysql.Mode
	for {
		b, err := src.Next(context.Background())
		if errors.Is(err, io.EOF) {
			return bs, modes
		}
		if err != nil {
			t.Fatal(err)
		}
		bs = append(bs, b)
		modes = append(modes, src.Lexer().Mode())
	}
}

// Every statement of the dump fixtures written by mysqldump, mariadb-dump,
// phpMyAdmin and Adminer is kept, except the routines (refused without
// --keep-routines).
func TestFixturesPass(t *testing.T) {
	files, _ := filepath.Glob("../mysql/testdata/*.sql")
	crlf, _ := filepath.Glob("../mysql/testdata/crlf/*.sql")
	files = append(files, crlf...)
	if len(files) < 8 {
		t.Fatalf("only %d fixtures", len(files))
	}
	for _, file := range files {
		in, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		bs, modes := statements(t, in)
		f := NewFilter(Options{Name: "prod.sql", Database: "shop"})
		var refused error
		for i, b := range bs {
			out, err := f.Add(b, modes[i])
			if err != nil {
				refused = err
				break
			}
			if out == nil {
				t.Errorf("%s: line %d dropped: %q", file, b.Pass.Line, b.Pass.Text)
			}
		}
		if refused == nil {
			refused = f.Finish()
		}
		if filepath.Base(file) == "routines.sql" {
			if !strings.Contains(errString(refused), "CREATE TRIGGER statement") {
				t.Errorf("%s: %v", file, refused)
			}
			continue
		}
		if refused != nil {
			t.Errorf("%s: %v", file, refused)
		}
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// One fixture per refused kind: each gives dump-refused at its line,
// naming the kind and never the data.
func TestRefused(t *testing.T) {
	for _, c := range []struct{ text, want string }{
		{"\\! touch /tmp/pwned", `holds a mysql client command (\!), which would run on the machine that imports the dump.`},
		{"system id", "holds a mysql client command (system),"},
		{"source /etc/passwd", "holds a mysql client command (source),"},
		{"\\. x.sql", `holds a mysql client command (\.),`},
		{"SELECT 1; \\! id", "holds a mysql client command"},
		{"CREATE TABLE t (a int) \\! id", `holds a mysql client command (\!), which would run`},
		{"CREATE TABLE t (a int) \\G", "holds a mysql client command, which would run"},
		{"SYSTEM id", "holds a mysql client command (system),"},
		{"USE mysql;", "holds a USE statement for another database, which would send the import to a database --to doesn't name."},
		{"use `mysql`;", "holds a USE statement for another database"},
		{"CREATE DATABASE x;", "holds a CREATE DATABASE statement, which would make or remove a database --to doesn't name."},
		{"/*!40000 CREATE DATABASE /*!32312 IF NOT EXISTS*/ `x` */;", "holds a CREATE DATABASE statement"},
		{"DROP DATABASE shop;", "holds a DROP DATABASE statement"},
		{"LOAD DATA LOCAL INFILE '/etc/passwd' INTO TABLE wp_options;", "holds a LOAD DATA statement, which could read files"},
		{"load xml infile 'x' into table t;", "holds a LOAD XML statement"},
		{"SELECT * FROM wp_users INTO OUTFILE '/var/www/x.php';", "holds a SELECT ... INTO OUTFILE statement, which could write files on the staging server."},
		{"select 1 into dumpfile '/tmp/x'", "holds a SELECT ... INTO DUMPFILE statement"},
		{"/*!50003 CREATE*/ /*!50017 DEFINER=`root`@`localhost`*/ /*!50003 TRIGGER t AFTER UPDATE ON wp_users FOR EACH ROW SET NEW.user_email = OLD.user_email */;",
			"holds a CREATE TRIGGER statement, which could run code on the staging database. Export with --skip-triggers, or pass --keep-routines to keep it without its DEFINER."},
		{"CREATE PROCEDURE p() SELECT 1;", "holds a CREATE PROCEDURE statement, which could run code on the staging database. Export without --routines"},
		{"create function f() returns int return 1;", "holds a CREATE FUNCTION statement"},
		{"CREATE ALGORITHM=UNDEFINED DEFINER=`root`@`%` SQL SECURITY DEFINER VIEW v AS SELECT 1;", "holds a CREATE VIEW statement"},
		{"CREATE FUNCTION sys_exec RETURNS INTEGER SONAME 'lib_mysqludf_sys.so';", "holds a CREATE FUNCTION ... SONAME statement, which could run commands"},
		{"SELECT sys_exec('id');", "holds a call to sys_exec(), which could run commands"},
		{"INSERT INTO wp_options SELECT LOAD_FILE('/etc/passwd');", "holds a call to load_file()"},
		{"SET GLOBAL general_log=1;", "holds a SET GLOBAL general_log statement, which would change the settings of the whole staging server."},
		{"SET @@global.general_log_file='/var/www/x.php';", "holds a SET GLOBAL general_log_file statement"},
		{"SET PERSIST local_infile=1;", "holds a SET GLOBAL local_infile statement"},
		{"SET sql_log_off=1;", "holds a SET sql_log_off statement, which isn't a session setting dump tools write."},
		{"SET @x = (SELECT user_pass FROM wp_users LIMIT 1);", "holds a SET statement that reads a query"},
		{"SET NAMES (select 1);", "holds a SET NAMES statement that reads a query"},
		{"SET NAMES utf8mb4, @x = (select 1);", "holds a SET statement that reads a query"},
		{"SET NAMES utf8mb4, @@GLOBAL.general_log_file='/var/www/html/x.php', @@GLOBAL.general_log=1;", "holds a SET GLOBAL general_log_file statement"},
		{"/*!40101 SET NAMES utf8mb4, GLOBAL general_log=1 */;", "holds a SET GLOBAL general_log statement"},
		{"SET CHARACTER SET utf8, GLOBAL general_log=1;", "holds a SET GLOBAL general_log statement"},
		{"SET CHARSET latin1, @@global.general_log=1;", "holds a SET GLOBAL general_log statement"},
		{"SET NAMES utf8mb4, sql_log_off=1;", "holds a SET sql_log_off statement"},
		{"CREATE TABLE IF NOT EXISTS `prod`.`wp_options` (a int);", "holds a CREATE TABLE statement that names a table with its database, which would send the import to a database --to doesn't name. Export the dump again with mysqldump or mariadb-dump"},
		{"CREATE TABLE mysql.x (a int);", "holds a CREATE TABLE statement that names a table with its database"},
		{"CREATE TABLE `shop`.`t` (a int);", "holds a CREATE TABLE statement that names a table with its database"},
		{"CREATE TABLE `v` LIKE mysql.user;", "holds a CREATE TABLE ... LIKE statement that names a table with its database"},
		{"CREATE TABLE v (LIKE mysql.user);", "holds a CREATE TABLE ... LIKE statement"},
		{"CREATE TABLE m (a int) ENGINE=MERGE UNION=(t1, mysql.user);", "holds a CREATE TABLE ... UNION statement"},
		{"ALTER TABLE `t` ADD COLUMN c int, RENAME TO mysql.user;", "holds an ALTER TABLE ... RENAME statement that names a table with its database"},
		{"ALTER TABLE t RENAME mysql.user;", "holds an ALTER TABLE ... RENAME statement"},
		{"ALTER TABLE t RENAME AS `prod`.`t`;", "holds an ALTER TABLE ... RENAME statement"},
		{"ALTER TABLE t RENAME TO t2;", "holds an ALTER TABLE ... RENAME TO statement, which would give a table the dump fills another name"},
		{"ALTER TABLE wp_users RENAME TO other;", "holds an ALTER TABLE ... RENAME TO statement"},
		{"ALTER TABLE wp_users RENAME other;", "holds an ALTER TABLE ... RENAME TO statement"},
		{"ALTER TABLE wp_users RENAME AS other;", "holds an ALTER TABLE ... RENAME TO statement"},
		{"ALTER TABLE wp_users RENAME COLUMN user_email TO e, RENAME TO other;", "holds an ALTER TABLE ... RENAME TO statement"},
		{"DROP TABLE wp_users;", "holds a DROP TABLE statement without IF EXISTS, which isn't the DROP TABLE IF EXISTS dump tools write"},
		{"DROP TEMPORARY TABLE `t`;", "holds a DROP TABLE statement without IF EXISTS"},
		{"ALTER TABLE mysql.user ADD COLUMN c int;", "holds an ALTER TABLE statement that names a table with its database"},
		{"ALTER TABLE m UNION=(mysql.user);", "holds an ALTER TABLE ... UNION statement"},
		{"CREATE INDEX i ON mysql.user (a);", "holds a CREATE INDEX statement that names a table with its database"},
		{"CREATE UNIQUE INDEX i USING BTREE ON `prod`.`t` (a);", "holds a CREATE INDEX statement"},
		{"DROP INDEX i ON mysql.user;", "holds a DROP INDEX statement that names a table with its database"},
		{"DROP TABLE IF EXISTS `prod`.`wp_options`;", "holds a DROP TABLE statement that names a table with its database"},
		{"DROP TABLE `t`, `prod`.`wp_options`;", "holds a DROP TABLE statement"},
		{"LOCK TABLES `t` WRITE, `prod`.`wp_options` WRITE;", "holds a LOCK TABLES statement that names a table with its database"},
		{"CREATE SEQUENCE prod.s;", "holds a CREATE SEQUENCE statement that names a table with its database"},
		{"INSERT INTO wp_users SELECT * FROM other.wp_users;", "holds an INSERT statement that isn't a list of rows, which would change rows outside the dump's INSERTs"},
		{"UPDATE wp_users SET user_email = 'x';", "holds an UPDATE statement that isn't a list of rows"},
		{"DELETE FROM wp_users;", "holds a DELETE statement"},
		{"CREATE TABLE t ENGINE=FEDERATED CONNECTION='mysql://u:p@prod/shop/t';", "holds a table with ENGINE=FEDERATED, which could connect the staging database to another server."},
		{"CREATE TABLE t (a int) ENGINE=CONNECT table_type=CSV file_name='/etc/passwd';", "holds a table with ENGINE=CONNECT"},
		{"CREATE TABLE t (a int) DATA DIRECTORY='/var/www';", "holds a CREATE TABLE ... DATA DIRECTORY statement"},
		{"CREATE TABLE t AS SELECT * FROM mysql.user;", "holds a CREATE TABLE ... SELECT statement"},
		{"ALTER TABLE t ENGINE=FEDERATED;", "holds a table with ENGINE=FEDERATED"},
		{"ALTER TABLE t DISCARD TABLESPACE;", "holds an ALTER TABLE ... DISCARD statement"},
		{"CALL p();", "holds a CALL statement, which isn't on the list of statements Roadie copies from a dump."},
		{"INSTALL PLUGIN x SONAME 'x.so';", "holds an INSTALL PLUGIN statement"},
		{"PREPARE s FROM @q;", "holds a PREPARE statement, which could run code"},
		{"EXECUTE s;", "holds an EXECUTE statement, which could run code on the staging database. Leave it out of the export; rhythm-roadie explain dump-refused lists what Roadie keeps."},
		{"EXECUTE IMMEDIATE 'GRANT ALL ON *.* TO x';", "holds an EXECUTE statement, which could run code on the staging database. Leave it out of the export;"},
		{"XA START 'x';", "holds an XA statement, which isn't on the list"},
		{"CREATE TABLE t (a text DEFAULT (`LOAD_FILE`('/etc/passwd')));", "holds a call to load_file()"},
		{"CREATE TABLE t (a text DEFAULT (`sys_exec`('id')));", "holds a call to sys_exec()"},
		{"BEGIN NOT ATOMIC SELECT 1; END;", "holds a BEGIN NOT statement"},
		{"DO SLEEP(10);", "holds a DO statement"},
		{"HANDLER t OPEN;", "holds a HANDLER statement"},
		{"\x00\x01;", "holds an unknown statement"},
		{"SET sql_mode=CONCAT('NO_BACKSLASH','_ESCAPES');", "holds a SET sql_mode statement Roadie can't follow, which could change how the staging server reads quotes and backslashes in the rows after it."},
		{"SET sql_mode='NO_BACKSLASH' '_ESCAPES';", "holds a SET sql_mode statement Roadie can't follow"},
		{"SET SESSION sql_mode=0x4E4F5F4241434B534C4153485F45534341504553;", "holds a SET sql_mode statement Roadie can't follow"},
		{"SET @@sql_mode=X'4E4F';", "holds a SET sql_mode statement Roadie can't follow"},
		{"SET sql_mode=REPLACE('NO_BACKSLASH_XSCAPES','X','E');", "holds a SET sql_mode statement Roadie can't follow"},
		{"SET sql_mode=2097152;", "holds a SET sql_mode statement Roadie can't follow"},
		{"SET sql_mode=_utf8mb4'NO_BACKSLASH_ESCAPES';", "holds a SET sql_mode statement Roadie can't follow"},
		{"SET NAMES utf8mb4, sql_mode=IFNULL(@a, @b);", "holds a SET sql_mode statement Roadie can't follow"},
	} {
		d, err := check(c.text, Options{Name: "prod.sql"})
		var ce *core.Error
		if d.Verdict != Refuse || !errors.As(err, &ce) || ce.Code != core.ErrDumpRefused || ce.Exit != 3 || ce.Line != 4120 {
			t.Errorf("%q: %v %v", c.text, d.Verdict, err)
			continue
		}
		if !strings.HasPrefix(ce.Msg, "dump-refused: prod.sql line 4,120 holds ") || !strings.Contains(ce.Msg, c.want) {
			t.Errorf("%q:\n got %s\nwant %s", c.text, ce.Msg, c.want)
		}
		for _, secret := range []string{"/etc/passwd", "/tmp/pwned", "user_email", "u:p@prod", "lib_mysqludf"} {
			if strings.Contains(ce.Msg, secret) {
				t.Errorf("%q: message quotes the statement: %s", c.text, ce.Msg)
			}
		}
	}
}

// The PRD 17 text for a trigger, byte for byte.
func TestRefusedTriggerMessage(t *testing.T) {
	_, err := check("CREATE TRIGGER t BEFORE INSERT ON wp_users FOR EACH ROW SET @x = 1;", Options{Name: "prod.sql"})
	want := "dump-refused: prod.sql line 4,120 holds a CREATE TRIGGER statement, which could run code on the staging database. Export with --skip-triggers, or pass --keep-routines to keep it without its DEFINER."
	if err == nil || err.Error() != want {
		t.Fatalf("got  %v\nwant %s", err, want)
	}
}

func TestDropped(t *testing.T) {
	for _, c := range []struct{ text, kind string }{
		{"/*!50000 GRANT ALL ON *.* TO x */;", "GRANT"},
		{"GRANT SELECT ON shop.* TO 'reader'@'%';", "GRANT"},
		{"REVOKE ALL ON *.* FROM x;", "REVOKE"},
		{"CREATE USER 'x'@'%' IDENTIFIED BY 'pw';", "CREATE USER"},
		{"ALTER USER 'x'@'%' IDENTIFIED BY 'pw';", "ALTER USER"},
		{"DROP USER x;", "DROP USER"},
		{"CREATE ROLE r;", "CREATE ROLE"},
		{"SET PASSWORD FOR x = 'y';", "SET PASSWORD"},
		{"FLUSH PRIVILEGES;", "FLUSH PRIVILEGES"},
		{"CREATE EVENT e ON SCHEDULE EVERY 1 DAY DO DELETE FROM wp_users;", "CREATE EVENT"},
		{"/*!50106 CREATE*/ /*!50117 DEFINER=`root`@`localhost`*/ /*!50106 EVENT `e` ON SCHEDULE EVERY 1 HOUR DO SELECT 1 */ ;;", "CREATE EVENT"},
		{"/*!50106 DROP EVENT IF EXISTS `e` */;;", "DROP EVENT"},
		{"ALTER DATABASE `shop` CHARACTER SET latin1;", "ALTER DATABASE"},
		{"USE `shop`;", "USE"},
		{"SET @@SESSION.SQL_LOG_BIN= 0;", "SET SQL_LOG_BIN"},
		{"SET @@GLOBAL.GTID_PURGED=/*!80000 '+'*/ '3E11FA47-71CA-11E1-9E33-C80AA9429562:1-5';", "SET @@GLOBAL.GTID_PURGED"},
		{"/*!50717 SELECT COUNT(*) INTO @rocksdb_has_p_s_session_variables FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = 'performance_schema' AND TABLE_NAME = 'session_variables' */;", "SELECT ... INTO @rocksdb_*"},
		{"/*!50717 SET @rocksdb_get_is_supported = IF (@rocksdb_has_p_s_session_variables, 'SELECT COUNT(*) INTO @rocksdb_is_supported FROM performance_schema.session_variables WHERE VARIABLE_NAME=\\'rocksdb_bulk_load\\'', 'SELECT 0') */;", "SET @rocksdb_*"},
		{"/*!50717 PREPARE s FROM @rocksdb_get_is_supported */;", "PREPARE"},
	} {
		d, err := check(c.text, Options{Name: "prod.sql", Database: "shop"})
		if err != nil || d.Verdict != Drop || d.Kind != c.kind || !strings.HasPrefix(d.Note, c.kind+" left out of the copy: ") {
			t.Errorf("%q: %+v %v", c.text, d, err)
		}
	}
}

// Each allowed statement kind and SET variable is kept.
func TestKept(t *testing.T) {
	for _, text := range []string{
		"CREATE TABLE `t` (`a` int) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;",
		"CREATE TABLE IF NOT EXISTS t (a int) ENGINE = MyISAM;",
		"DROP TABLE IF EXISTS `t`;",
		"LOCK TABLES `t` WRITE;",
		"UNLOCK TABLES;",
		"/*!40000 ALTER TABLE `t` DISABLE KEYS */;",
		"ALTER TABLE `t` ADD PRIMARY KEY (`ID`), ADD KEY `k` (`a`);",
		"ALTER TABLE `t` MODIFY `ID` bigint(20) unsigned NOT NULL AUTO_INCREMENT, AUTO_INCREMENT=5;",
		"ALTER TABLE `t` ADD CONSTRAINT `fk` FOREIGN KEY (`a`) REFERENCES `u` (`id`) ON DELETE CASCADE;",
		"START TRANSACTION;", "BEGIN;", "COMMIT;", "ROLLBACK;",
		"-- a comment", "/* a comment */", "",
		"/*M!999999\\- enable the sandbox mode */",
		"DELIMITER ;;",
		"/*!40101 SET NAMES utf8mb4 */;",
		"SET NAMES utf8mb4 COLLATE utf8mb4_unicode_ci;",
		"/*!40101 SET character_set_client = utf8mb4 */;",
		"SET character_set_results = utf8mb4;",
		"SET collation_connection = utf8mb4_unicode_ci;",
		"SET time_zone = \"+00:00\";",
		"SET SQL_MODE = \"NO_AUTO_VALUE_ON_ZERO\";",
		"/*!40014 SET @OLD_FOREIGN_KEY_CHECKS=@@FOREIGN_KEY_CHECKS, FOREIGN_KEY_CHECKS=0 */;",
		"SET foreign_key_checks = 0;",
		"/*!40014 SET UNIQUE_CHECKS=@OLD_UNIQUE_CHECKS */;",
		"/*!40111 SET @OLD_SQL_NOTES=@@SQL_NOTES, SQL_NOTES=0 */;",
		"SET autocommit = 0;",
		"/*M!100616 SET @OLD_NOTE_VERBOSITY=@@NOTE_VERBOSITY, NOTE_VERBOSITY=0 */;",
		"/*!40103 SET TIME_ZONE=@OLD_TIME_ZONE */;",
		"/*!50003 SET @saved_cs_client      = @@character_set_client */ ;",
		"SET @MYSQLDUMP_TEMP_LOG_BIN = @@SESSION.SQL_LOG_BIN;",
		"SET SESSION sql_mode = IFNULL(@OLD_SQL_MODE, '');",
		"SET sql_mode = @OLD_SQL_MODE;",
		"SET sql_mode = @`saved`.`mode`;",
		"SET @@session.sql_mode = DEFAULT;",
		"SET sql_mode = @@SESSION.sql_mode;",
		"CREATE SEQUENCE `s` start with 1 minvalue 1 maxvalue 9223372036854775806 increment by 1 cache 1000 nocycle ENGINE=InnoDB;",
		"SELECT SETVAL(`s`, 1001, 0);",
		"DO SETVAL(`s`, 1001, 0);",
		"CREATE INDEX i ON t (a);",
		"CREATE UNIQUE INDEX i ON t (a);",
		"DROP INDEX i ON t;",
		"SET NAMES utf8mb4, character_set_client = utf8mb4;",
		"/*!40101 SET NAMES utf8mb4, @x = 1 */;",
		"SET CHARACTER SET utf8mb4, collation_connection = utf8mb4_unicode_ci;",
		"CREATE TABLE v LIKE t;",
		"CREATE TABLE v (LIKE `t`);",
		"CREATE TABLE m (a int) ENGINE=MERGE UNION=(t1, t2);",
		"ALTER TABLE t RENAME COLUMN a TO b, RENAME INDEX i TO j;",
		"LOCK TABLES `t` WRITE, `u` AS x READ;",
		"ALTER TABLE `t` ADD CONSTRAINT `fk` FOREIGN KEY (`a`) REFERENCES `u` (`id`);",
	} {
		d, err := check(text, Options{Name: "prod.sql"})
		if err != nil || d.Verdict != Keep || d.Text != nil {
			t.Errorf("%q: %+v %v", text, d, err)
		}
	}
}

// With --keep-routines a trigger, procedure, function or view is kept
// with DEFINER=CURRENT_USER and listed for the plan.
func TestKeepRoutines(t *testing.T) {
	o := Options{Name: "prod.sql", KeepRoutines: true}
	for _, c := range []struct{ in, want, note string }{
		{
			"/*!50003 CREATE*/ /*!50017 DEFINER=`root`@`localhost`*/ /*!50003 TRIGGER `keep_email` AFTER UPDATE ON `wp_users` FOR EACH ROW BEGIN\n  IF NEW.user_email <> OLD.user_email THEN\n    UPDATE wp_users SET user_email = 'definer=`x`@`y`' WHERE ID = NEW.ID;\n  END IF;\nEND */;;",
			"/*!50003 CREATE*/ /*!50017 DEFINER=CURRENT_USER*/ /*!50003 TRIGGER `keep_email` AFTER UPDATE ON `wp_users` FOR EACH ROW BEGIN\n  IF NEW.user_email <> OLD.user_email THEN\n    UPDATE wp_users SET user_email = 'definer=`x`@`y`' WHERE ID = NEW.ID;\n  END IF;\nEND */;;",
			"kept CREATE TRIGGER keep_email with DEFINER=CURRENT_USER (--keep-routines)",
		},
		{
			"CREATE DEFINER = 'root'@'%' PROCEDURE `p`()\nBEGIN\n  SELECT 1; -- DEFINER=`a`@`b`\nEND$$",
			"CREATE DEFINER=CURRENT_USER PROCEDURE `p`()\nBEGIN\n  SELECT 1; -- DEFINER=`a`@`b`\nEND$$",
			"kept CREATE PROCEDURE p with DEFINER=CURRENT_USER (--keep-routines)",
		},
		{
			"/*!50001 CREATE ALGORITHM=UNDEFINED */\n/*!50013 DEFINER=`root`@`localhost` SQL SECURITY DEFINER */\n/*!50001 VIEW `v` AS select 1 AS `a` */;",
			"/*!50001 CREATE ALGORITHM=UNDEFINED */\n/*!50013 DEFINER=CURRENT_USER SQL SECURITY DEFINER */\n/*!50001 VIEW `v` AS select 1 AS `a` */;",
			"kept CREATE VIEW v with DEFINER=CURRENT_USER (--keep-routines)",
		},
		{
			"CREATE FUNCTION f() RETURNS int DETERMINISTIC RETURN 1;",
			"",
			"kept CREATE FUNCTION f with DEFINER=CURRENT_USER (--keep-routines)",
		},
	} {
		d, err := check(c.in, o)
		if err != nil || d.Verdict != Keep || !d.Routine || d.Note != c.note {
			t.Errorf("%q: %+v %v", c.in, d, err)
			continue
		}
		if string(d.Text) != c.want && (c.want != "" || d.Text != nil) {
			t.Errorf("%q:\n got %s\nwant %s", c.in, d.Text, c.want)
		}
	}
	// Never kept, even with --keep-routines.
	for _, text := range []string{
		"CREATE FUNCTION sys_eval RETURNS STRING SONAME 'lib_mysqludf_sys.so';",
		"CREATE PROCEDURE p() SELECT * FROM wp_users INTO OUTFILE '/tmp/u';",
		"CREATE TRIGGER t AFTER INSERT ON wp_users FOR EACH ROW SET @x = LOAD_FILE('/etc/passwd');",
		"CREATE TRIGGER t AFTER INSERT ON mysql.user FOR EACH ROW SET @x = 1;",
		"CREATE TRIGGER prod.t AFTER INSERT ON wp_users FOR EACH ROW SET @x = 1;",
		"CREATE VIEW `prod`.`v` AS SELECT 1;",
		"ALTER VIEW prod.v AS SELECT 1;",
		"CREATE PROCEDURE mysql.p() SELECT 1;",
	} {
		if d, _ := check(text, o); d.Verdict != Refuse {
			t.Errorf("%q kept with --keep-routines", text)
		}
	}
}

// The filter drops what Check drops, rewrites kept routines, matches
// DROPs with CREATEs at the end and lists the plan lines.
func TestFilter(t *testing.T) {
	in := strings.Join([]string{
		"-- MySQL dump 10.13",
		"DROP TABLE IF EXISTS `wp_users`;",
		"CREATE TABLE `wp_users` (`ID` int, `user_email` varchar(100));",
		"INSERT INTO `wp_users` VALUES (1,'a@shop.test');",
		"GRANT ALL ON *.* TO x;",
		"GRANT SELECT ON *.* TO y;",
		"/*!50717 PREPARE s FROM @rocksdb_enable_bulk_load */;",
		"/*!50717 EXECUTE s */;",
		"/*!50717 DEALLOCATE PREPARE s */;",
		"DELIMITER ;;",
		"/*!50003 CREATE*/ /*!50017 DEFINER=`root`@`localhost`*/ /*!50003 TRIGGER `t1` AFTER UPDATE ON `wp_users` FOR EACH ROW SET @x = 1 */;;",
		"DELIMITER ;",
		"-- Dump completed",
		"",
	}, "\n")
	bs, modes := statements(t, []byte(in))
	f := NewFilter(Options{Name: "prod.sql", KeepRoutines: true})
	var out []string
	for i, b := range bs {
		got, err := f.Add(b, modes[i])
		if err != nil {
			t.Fatal(err)
		}
		if got != nil && got.Pass != nil {
			out = append(out, strings.TrimSpace(string(got.Pass.Text)))
		}
	}
	if err := f.Finish(); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(out, "\n")
	if strings.Contains(joined, "GRANT") || strings.Contains(joined, "PREPARE") || strings.Contains(joined, "EXECUTE") || strings.Contains(joined, "`root`") {
		t.Errorf("kept:\n%s", joined)
	}
	if !strings.Contains(joined, "/*!50017 DEFINER=CURRENT_USER*/") {
		t.Errorf("trigger not rewritten:\n%s", joined)
	}
	want := []string{
		"2 GRANT statements left out of the copy (first at line 5): the staging database keeps its own users and privileges",
		"line 7: PREPARE left out of the copy: " + reasonPercona,
		"line 8: EXECUTE left out of the copy: " + reasonPercona,
		"line 9: DEALLOCATE PREPARE left out of the copy: " + reasonPercona,
		"line 11: kept CREATE TRIGGER t1 with DEFINER=CURRENT_USER (--keep-routines)",
	}
	if got := f.Notes(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("notes:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// A DROP of a table the dump never creates is refused at the end, before
// anything is committed.
func TestFilterDropWithoutCreate(t *testing.T) {
	in := "DROP TABLE IF EXISTS `wp_users`;\nDROP TABLE IF EXISTS `t`;\nCREATE TABLE `t` (`a` int);\n"
	bs, modes := statements(t, []byte(in))
	f := NewFilter(Options{Name: "prod.sql"})
	for i, b := range bs {
		if _, err := f.Add(b, modes[i]); err != nil {
			t.Fatal(err)
		}
	}
	err := f.Finish()
	want := "dump-refused: prod.sql line 1 holds a DROP TABLE statement, which removes something the dump doesn't create. " + adviceLeaveOut
	if err == nil || err.Error() != want {
		t.Fatalf("got  %v\nwant %s", err, want)
	}
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Line != 1 {
		t.Fatalf("line: %v", err)
	}
}

// A DROP TABLE IF EXISTS after the CREATE of the same table removes the
// table the dump filled: it is refused at the end. A DROP of a table
// mysqldump made as a view's placeholder, followed by the view, is kept.
func TestFilterDropAfterCreate(t *testing.T) {
	run := func(in string, o Options) error {
		t.Helper()
		bs, modes := statements(t, []byte(in))
		f := NewFilter(o)
		for i, b := range bs {
			if _, err := f.Add(b, modes[i]); err != nil {
				return err
			}
		}
		return f.Finish()
	}
	in := "DROP TABLE IF EXISTS `wp_users`;\nCREATE TABLE `wp_users` (`a` int);\nINSERT INTO `wp_users` VALUES (1);\nDROP TABLE IF EXISTS `wp_users`;\n"
	err := run(in, Options{Name: "prod.sql"})
	want := "dump-refused: prod.sql line 4 holds a DROP TABLE statement, which removes a table the dump has already created and filled. " + adviceLeaveOut
	if err == nil || err.Error() != want {
		t.Fatalf("got  %v\nwant %s", err, want)
	}
	// Negative: dropped before its CREATE, or made again after the drop.
	for _, ok := range []string{
		"DROP TABLE IF EXISTS `t`;\nCREATE TABLE `t` (`a` int);\n",
		"CREATE TABLE `v` (`a` int);\nDROP TABLE IF EXISTS `v`;\n/*!50001 DROP VIEW IF EXISTS `v`*/;\n/*!50001 CREATE VIEW `v` AS SELECT 1 AS `a` */;\n",
	} {
		if err := run(ok, Options{Name: "prod.sql", KeepRoutines: true}); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
}

// A statement whose quoting changes with the sql_mode it sets is judged
// under both modes.
func TestFilterModeChange(t *testing.T) {
	f := NewFilter(Options{Name: "prod.sql"})
	st := &core.Statement{Kind: core.StmtSet, Text: []byte(`SET sql_mode='NO_BACKSLASH_ESCAPES', @x='\', GRANT=1, @y='';`), Line: 1}
	_, err := f.Add(&core.Batch{Pass: st}, mysql.Mode{NoBackslashEscapes: true})
	if err == nil {
		t.Fatal("kept a statement that reads as a SET of GRANT under the new mode")
	}
}

// A statement after which the reader can't follow sql_mode is refused,
// whatever it is.
func TestFilterModeUnknown(t *testing.T) {
	f := NewFilter(Options{Name: "prod.sql"})
	st := &core.Statement{Kind: core.StmtSet, Text: []byte("SET sql_mode = @m;"), Line: 7}
	_, err := f.Add(&core.Batch{Pass: st}, mysql.Mode{Unknown: true})
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Line != 7 || !strings.Contains(ce.Msg, "holds a SET sql_mode statement Roadie can't follow") {
		t.Fatalf("err = %v", err)
	}
	ok := NewFilter(Options{Name: "prod.sql"})
	if _, err := ok.Add(&core.Batch{Pass: st}, mysql.Mode{NoBackslashEscapes: true}); err != nil {
		t.Fatalf("a followed mode was refused: %v", err)
	}
}
