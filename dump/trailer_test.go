// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package dump

import (
	"bytes"
	"errors"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/dump/compress"
)

// openAll opens a dump and drains it.
func openAll(t testing.TB, in []byte, name string) error {
	t.Helper()
	src, err := Open(t.Context(), bytes.NewReader(in), Options{Name: name, KeepRoutines: true})
	if err != nil {
		return err
	}
	defer src.Close()
	_, err = drainAll(t, src)
	return err
}

var (
	reCreate = regexp.MustCompile("CREATE TABLE `(\\w+)`")
	reInsert = regexp.MustCompile("(?:INSERT|REPLACE)(?: IGNORE)? INTO `(\\w+)`(?: \\([^)]*\\))? VALUES\\s*\\(")
)

// tableAt is the table a reader has entered after reading in[:cut]: the
// last CREATE TABLE complete before the cut or the last INSERT whose
// first row has started, whichever comes later.
func tableAt(in []byte, cut int) string {
	best, table := -1, ""
	for _, m := range reCreate.FindAllSubmatchIndex(in, -1) {
		end := bytes.Index(in[m[1]:], []byte(";\n"))
		if alt := bytes.Index(in[m[1]:], []byte(";\r\n")); alt >= 0 && (end < 0 || alt < end) {
			end = alt
		}
		if end >= 0 && m[1]+end+1 <= cut && m[1]+end > best {
			best, table = m[1]+end, string(in[m[2]:m[3]])
		}
	}
	for _, m := range reInsert.FindAllSubmatchIndex(in, -1) {
		if m[1] <= cut && m[1] > best {
			best, table = m[1], string(in[m[2]:m[3]])
		}
	}
	return table
}

func mysqlFixtures(t *testing.T) []string {
	files, _ := filepath.Glob("mysql/testdata/*.sql")
	crlf, _ := filepath.Glob("mysql/testdata/crlf/*.sql")
	files = append(append(files, crlf...), "testdata/small.sql")
	if len(files) < 10 {
		t.Fatalf("only %d fixtures", len(files))
	}
	return files
}

// Every fixture passes whole. A fixture from a tool with an end line,
// cut at 10 random offsets before it, is dump-truncated naming the table
// the reader was in; one from phpMyAdmin or Adminer, cut inside an
// INSERT, is too.
func TestCutFixtures(t *testing.T) {
	rng := rand.New(rand.NewPCG(26, 10))
	checked := 0
	defer func() {
		if checked < 100 {
			t.Errorf("only %d cuts checked", checked)
		}
	}()
	for _, file := range mysqlFixtures(t) {
		in, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Base(file)
		if err := openAll(t, in, name); err != nil {
			t.Errorf("%s whole: %v", name, err)
			continue
		}
		first := bytes.IndexByte(in, '\n') + 1
		var cuts []int
		if end := bytes.Index(in, []byte("-- Dump completed")); end > 0 {
			for range 10 {
				cuts = append(cuts, first+rng.IntN(end-first))
			}
		} else {
			ins := reInsert.FindAllIndex(in, -1)
			for range 10 {
				m := ins[rng.IntN(len(ins))]
				stop := bytes.Index(in[m[1]:], []byte(");")) + m[1]
				cuts = append(cuts, m[1]+rng.IntN(max(1, stop-m[1])))
			}
		}
		for _, cut := range cuts {
			err := openAll(t, in[:cut], name)
			var ce *core.Error
			if !errors.As(err, &ce) || ce.Code != core.ErrDumpTruncated || ce.Exit != 3 {
				t.Errorf("%s cut at %d: %v", name, cut, err)
				continue
			}
			tb := tableAt(in, cut)
			ok := strings.Contains(ce.Msg, name+" ends early") || strings.Contains(ce.Msg, name+" ends inside a statement at line")
			where := "no table"
			if tb != "" {
				where = name + " ends inside table " + tb + " at "
				ok = strings.Contains(ce.Msg, where)
			}
			checked++
			if !ok {
				t.Errorf("%s cut at %d: want %q in %s", name, cut, where, ce.Msg)
			}
		}
	}
}

// The exact wording for a mysqldump file that stops between statements.
func TestNoDumpCompleted(t *testing.T) {
	in := "-- MySQL dump 10.13  Distrib 8.0.36, for Linux (x86_64)\n" +
		"CREATE TABLE `wp_postmeta` (`meta_id` int, `meta_value` longtext);\n" +
		"INSERT INTO `wp_postmeta` VALUES (1,'x');\n"
	err := openAll(t, []byte(in), "prod.sql")
	want := `dump-truncated: prod.sql ends inside table wp_postmeta at 0.0 GB, with no "Dump completed" line. Export it again; the target is untouched.`
	if err == nil || err.Error() != want {
		t.Fatalf("got  %v\nwant %s", err, want)
	}
	// The end line with or without a date, followed by blank lines and
	// comments, closes the dump; a statement after it doesn't.
	for _, tail := range []string{"-- Dump completed\n", "-- Dump completed on 2026-10-26 10:00:00\n\n-- bye\n", "-- Dump completed on 2026-10-26 10:00:00"} {
		if err := openAll(t, []byte(in+tail), "prod.sql"); err != nil {
			t.Errorf("%q: %v", tail, err)
		}
	}
	if err := openAll(t, []byte(in+"-- Dump completed\nINSERT INTO `wp_postmeta` VALUES (2,'y');\n"), "prod.sql"); err == nil {
		t.Error("a statement after the end line passed")
	}
	// mariadb-dump: the same end line after the sandbox line.
	maria := "/*M!999999\\- enable the sandbox mode */ \n-- MariaDB dump 10.19-11.4.2-MariaDB\nCREATE TABLE `t` (`a` int);\n"
	if err := openAll(t, []byte(maria), "m.sql"); err == nil || !strings.Contains(err.Error(), `ends inside table t at 0.0 GB, with no "Dump completed" line`) {
		t.Errorf("mariadb: %v", err)
	}
	if err := openAll(t, []byte(maria+"-- Dump completed on 2026-10-26 10:00:00\n"), "m.sql"); err != nil {
		t.Errorf("mariadb whole: %v", err)
	}
}

// phpMyAdmin and Adminer write no end line: a clean end after COMMIT or
// a complete INSERT passes, one after CREATE TABLE doesn't and a cut
// inside an INSERT says so.
func TestNoTrailerTools(t *testing.T) {
	pma := "-- phpMyAdmin SQL Dump\n-- version 5.2.1\nSET SQL_MODE = \"NO_AUTO_VALUE_ON_ZERO\";\nSTART TRANSACTION;\n" +
		"CREATE TABLE `wp_users` (\n  `ID` bigint(20) UNSIGNED NOT NULL,\n  `user_email` varchar(100) NOT NULL\n) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;\n"
	rows := "INSERT INTO `wp_users` (`ID`, `user_email`) VALUES\n(1, 'a@shop.test'),\n(2, 'b@shop.test');\n"
	restore := "/*!40101 SET CHARACTER_SET_CLIENT=@OLD_CHARACTER_SET_CLIENT */;\n"
	for _, c := range []struct {
		in   string
		want string // "" passes
	}{
		{pma + rows + "COMMIT;\n\n" + restore, ""},
		{pma + rows, ""},
		{pma + rows + "ALTER TABLE `wp_users`\n  ADD PRIMARY KEY (`ID`);\n", ""},
		{pma, "dump-truncated: pma.sql ends inside table wp_users at 0.0 GB, with no closing COMMIT or complete INSERT. Export it again; the target is untouched."},
		{pma + rows[:60], "dump-truncated: pma.sql ends inside table wp_users at 0.0 GB, in the middle of a statement. Export it again; the target is untouched."},
		{"-- phpMyAdmin SQL Dump\nSTART TRANSACTION;\n", "dump-truncated: pma.sql ends early at 0.0 GB, with no closing COMMIT or complete INSERT."},
		{"-- phpMyAdmin SQL Dump\n-- nothing exported\n", ""},
		{"-- Adminer 4.8.1 MySQL 8.0.36 dump\nCREATE TABLE `t` (`a` int);\n\n-- 2026-10-26 10:00:00\n", ""},
		{"-- Adminer 4.8.1 MySQL 8.0.36 dump\nCREATE TABLE `t` (`a` int);\n", "with no closing COMMIT or complete INSERT"},
	} {
		err := openAll(t, []byte(c.in), "pma.sql")
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%q: %v", c.in, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%q:\n got %v\nwant %s", c.in, err, c.want)
		}
	}
}

// A compressed stream cut short is truncated whatever the plain text
// looks like; a whole stream of a dump without its end line is too.
func TestCutCompressed(t *testing.T) {
	plain := fixture(t, "small.sql")
	for _, kind := range []string{compress.Gzip, compress.Zstd} {
		file := pack(t, kind, plain)
		for _, cut := range []int{len(file) / 2, len(file) - 4, len(file) - 1} {
			err := openAll(t, file[:cut], "prod.sql.gz")
			var ce *core.Error
			if !errors.As(err, &ce) || ce.Code != core.ErrDumpTruncated {
				t.Errorf("%s cut at %d of %d: %v", kind, cut, len(file), err)
			}
		}
		cut := plain[:bytes.Index(plain, []byte("-- Dump completed"))]
		err := openAll(t, pack(t, kind, cut), "prod.sql.gz")
		if err == nil || !strings.Contains(err.Error(), `with no "Dump completed" line`) {
			t.Errorf("%s without the end line: %v", kind, err)
		}
	}
}
