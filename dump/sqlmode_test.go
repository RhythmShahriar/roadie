// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package dump

import (
	"errors"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
)

// smuggle is a row that is one string while backslashes escape and ends
// early when they don't, leaving SET GLOBAL to run on import.
const smuggle = `INSERT INTO t VALUES ('a\'); SET GLOBAL general_log=1; -- ');`

// smuggleBack is the reverse: two rows while backslashes are plain bytes,
// one string holding SET GLOBAL when they escape.
const smuggleBack = `INSERT INTO t VALUES ('a\'), ('); SET GLOBAL general_log=1; -- ');`

// mysqlDump wraps statements in a mysqldump head, a table t and the
// trailer.
func mysqlDump(stmts ...string) string {
	return strings.Join(append(append([]string{
		"-- MySQL dump 10.13  Distrib 8.0.36, for Linux (x86_64)",
		"CREATE TABLE t (a text);",
	}, stmts...), "-- Dump completed on 2026-10-26 10:00:00", ""), "\n")
}

// rowsOf counts the rows of the batches.
func rowsOf(bs []*core.Batch) int {
	n := 0
	for _, b := range bs {
		n += len(b.Rows)
	}
	return n
}

// A sql_mode the reader can't follow exactly stops the run, so no row
// can be read under a quoting the server doesn't use.
func TestOpenSQLModeRefused(t *testing.T) {
	for name, stmts := range map[string][]string{
		"saved literal":          {"/*!40101 SET @OLD_SQL_MODE='NO_BACKSLASH_ESCAPES' */;", "/*!40101 SET SQL_MODE=@OLD_SQL_MODE */;", smuggle},
		"variable":               {"SET @m='NO_BACKSLASH_ESCAPES';", "SET sql_mode=@m;", smuggle},
		"concat":                 {"SET sql_mode=CONCAT('NO_BACKSLASH','_ESCAPES');", smuggle},
		"adjacent literals":      {"SET sql_mode='NO_BACKSLASH' '_ESCAPES';", smuggle},
		"hex number":             {"SET sql_mode=0x4E4F5F4241434B534C4153485F45534341504553;", smuggle},
		"hex string":             {"SET sql_mode=X'4E4F5F4241434B534C4153485F45534341504553';", smuggle},
		"replace":                {"SET sql_mode=REPLACE('NO_BACKSLASH_XSCAPES','X','E');", smuggle},
		"bitmask":                {"SET sql_mode=2097152;", smuggle},
		"variable from concat":   {"SET @m=CONCAT('NO_BACKSLASH','_ESCAPES');", "SET sql_mode=@m;", smuggle},
		"newer server only":      {"/*!99999 SET sql_mode='NO_BACKSLASH_ESCAPES' */;", smuggleBack},
		"mariadb only":           {"/*M!100100 SET sql_mode='NO_BACKSLASH_ESCAPES' */;", smuggleBack},
		"beside another setting": {"SET sql_mode='NO_BACKSLASH_ESCAPES', time_zone='bogus';", smuggleBack},
	} {
		bs, err := drainAll(t, mustOpen(t, mysqlDump(stmts...), Options{Name: "prod.sql"}))
		var ce *core.Error
		if !errors.As(err, &ce) || ce.Code != core.ErrDumpRefused {
			t.Errorf("%s: err = %v, %d rows", name, err, rowsOf(bs))
		}
	}
}

// The sql_mode changes dump tools write are followed: the rows read as
// the server reads them.
func TestOpenSQLModeFollowed(t *testing.T) {
	for _, c := range []struct {
		name  string
		stmts []string
		rows  int
	}{
		{"mysqldump", []string{
			"/*!40101 SET @OLD_SQL_MODE=@@SQL_MODE, SQL_MODE='NO_AUTO_VALUE_ON_ZERO' */;",
			smuggle,
			"/*!40101 SET SQL_MODE=@OLD_SQL_MODE */;",
		}, 1},
		{"no backslash escapes", []string{"SET sql_mode='NO_BACKSLASH_ESCAPES';", `INSERT INTO t VALUES ('a\'),('b');`}, 2},
		{"saved and restored", []string{
			"/*!50003 SET @saved_sql_mode = @@sql_mode */ ;",
			"/*!50003 SET sql_mode = 'NO_BACKSLASH_ESCAPES' */ ;",
			`INSERT INTO t VALUES ('a\'),('b');`,
			"/*!50003 SET sql_mode = @saved_sql_mode */ ;",
			smuggle,
		}, 3},
		{"phpMyAdmin", []string{`SET SQL_MODE = "NO_AUTO_VALUE_ON_ZERO";`, smuggle}, 1},
	} {
		bs, err := drainAll(t, mustOpen(t, mysqlDump(c.stmts...), Options{Name: "prod.sql"}))
		if err != nil || rowsOf(bs) != c.rows {
			t.Errorf("%s: err = %v, %d rows, want %d", c.name, err, rowsOf(bs), c.rows)
		}
	}
}

// pgSmuggle is a row that is one string while backslashes are plain
// bytes (standard_conforming_strings on) and ends early when they escape.
const pgSmuggle = `INSERT INTO public.t VALUES ('a\'); DROP TABLE x; COPY public.t FROM PROGRAM 'id'; --');`

// pgDump wraps statements in a pg_dump head that sets
// standard_conforming_strings on, a table t and the trailer.
func pgDump(stmts ...string) []byte {
	return []byte(strings.Join(append([]string{
		"--\n-- PostgreSQL database dump\n--\n",
		"SET standard_conforming_strings = on;",
		"CREATE TABLE public.t (a text);",
	}, stmts...), "\n") + "\n--\n-- PostgreSQL database dump complete\n--\n")
}

// A standard_conforming_strings change the server may undo or skip stops
// the run; a lasting one is followed.
func TestOpenStandardStrings(t *testing.T) {
	for name, stmts := range map[string][]string{
		"rolled back":                 {"BEGIN;", "SET standard_conforming_strings = off;", "ROLLBACK;", pgSmuggle},
		"aborted":                     {"START TRANSACTION;", "SET standard_conforming_strings = off;", "ABORT;", pgSmuggle},
		"local outside a block":       {"SET LOCAL standard_conforming_strings = off;", pgSmuggle},
		"local inside a block":        {"BEGIN;", "SET LOCAL standard_conforming_strings = off;", "COMMIT;", pgSmuggle},
		"set_config local":            {"SELECT pg_catalog.set_config('standard_conforming_strings', 'off', true);", pgSmuggle},
		"after a failed statement":    {"BEGIN;", "CREATE TABLE public.t (a text);", "SET standard_conforming_strings = off;", "COMMIT;", pgSmuggle},
		"a value Roadie can't follow": {"SET standard_conforming_strings = of;", pgSmuggle},
	} {
		_, bs, err := openPG(t, pgDump(stmts...), Options{Name: "prod.sql"})
		var ce *core.Error
		if !errors.As(err, &ce) || ce.Code != core.ErrDumpRefused {
			t.Errorf("%s: err = %v, %d rows", name, err, rowsOf(bs))
		}
	}
	_, bs, err := openPG(t, pgDump("SET standard_conforming_strings = off;", `INSERT INTO public.t VALUES ('a\'b'), ('c');`), Options{Name: "prod.sql"})
	if err != nil || rowsOf(bs) != 2 {
		t.Errorf("a lasting change: err = %v, %d rows", err, rowsOf(bs))
	}
}
