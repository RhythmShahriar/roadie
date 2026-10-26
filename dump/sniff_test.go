// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package dump

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/dump/compress"
)

func fixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func pack(t testing.TB, kind string, plain []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := compress.NewWriter(&buf, kind)
	if err != nil {
		t.Fatal(err)
	}
	w.Write(plain)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func head(b []byte) []byte { return b[:min(len(b), HeadSize)] }

// The same dump sniffs the same in every compression.
func TestSniffCompressions(t *testing.T) {
	plain := fixture(t, "small.sql")
	for kind, file := range map[string][]byte{
		compress.None:  plain,
		compress.Gzip:  pack(t, compress.Gzip, plain),
		compress.Zstd:  pack(t, compress.Zstd, plain),
		compress.Bzip2: fixture(t, "small.sql.bz2"),
	} {
		d, c, err := Sniff(head(file))
		if err != nil || d != core.MySQL || c != kind {
			t.Errorf("%q: Sniff = %q, %q, %v", kind, d, c, err)
		}
		_, _, server, _ := sniff("x", head(file))
		if server != "8.0.36" {
			t.Errorf("%q: server %q", kind, server)
		}
	}
}

func TestSniffDialects(t *testing.T) {
	for _, c := range []struct {
		head    string
		dialect core.Dialect
		server  string
	}{
		{"-- MySQL dump 10.13  Distrib 8.0.36, for Linux (x86_64)\n--\n-- Server version\t8.0.36\n", core.MySQL, "8.0.36"},
		{"-- MySQL dump 10.19  Distrib 10.6.18-MariaDB, for debian-linux-gnu (x86_64)\n", core.MariaDB, ""},
		{"-- MySQL dump 10.19\n--\n-- Server version\t10.11.6-MariaDB-0+deb12u1\n", core.MariaDB, "10.11.6"},
		{"/*M!999999\\- enable the sandbox mode */ \n-- MariaDB dump 10.19-11.4.2-MariaDB, for Linux (x86_64)\n", core.MariaDB, ""},
		{"-- MariaDB dump 10.19-11.4.2-MariaDB, for Linux (x86_64)\n", core.MariaDB, ""},
		{"-- phpMyAdmin SQL Dump\n-- version 5.2.1\n-- Server version: 8.0.36\n", core.MySQL, "8.0.36"},
		{"-- phpMyAdmin SQL Dump\n-- version 5.2.1\n-- Server version: 10.6.12-MariaDB-0ubuntu0.22.04.1\n", core.MariaDB, "10.6.12"},
		{"-- Adminer 4.8.1 MySQL 8.0.36 dump\n\nSET NAMES utf8;\n", core.MySQL, "8.0.36"},
		{"-- Adminer 4.8.1 MySQL 5.5.5-10.6.12-MariaDB dump\n", core.MariaDB, "10.6.12"},
		{"/*!40101 SET NAMES utf8 */;\nCREATE TABLE x (a int);\n", core.MySQL, ""},
		{"SET FOREIGN_KEY_CHECKS=0;\n", core.MySQL, ""},
		{"--\n-- PostgreSQL database dump\n--\n", core.Postgres, ""},
		{"SET statement_timeout = 0;\nSET standard_conforming_strings = on;\n", core.Postgres, ""},
		{"CREATE TABLE t (a int);\n", "", ""},
		{"", "", ""},
	} {
		d, comp, server, err := sniff("x.sql", []byte(c.head))
		if err != nil || d != c.dialect || comp != "" || server != c.server {
			t.Errorf("%q: %q %q %q %v, want %q %q", c.head, d, comp, server, err, c.dialect, c.server)
		}
	}
}

func tarFile(t *testing.T) []byte {
	var buf bytes.Buffer
	w := tar.NewWriter(&buf)
	body := fixture(t, "small.sql")
	w.WriteHeader(&tar.Header{Name: "shop.sql", Mode: 0o600, Size: int64(len(body))})
	w.Write(body)
	w.Close()
	return buf.Bytes()
}

func zipFile(t *testing.T) []byte {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, _ := w.Create("shop.sql")
	f.Write(fixture(t, "small.sql"))
	w.Close()
	return buf.Bytes()
}

// randomBytes stands in for a .wpress or Duplicator archive renamed .sql:
// it is refused by its content, whatever its name says.
func randomBytes(n int, seed uint64) []byte {
	r := rand.New(rand.NewPCG(seed, seed))
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(r.Uint32())
	}
	return b
}

func TestSniffRefuses(t *testing.T) {
	const tail = ". Roadie reads plain SQL: pg_dump -Fp, mysqldump, mariadb-dump or wp db export."
	cases := map[string]struct {
		file []byte
		what string
	}{
		"pgdump":     {append([]byte("PGDMP\x01\x0e\x00\x04\x08\x01\x01"), randomBytes(500, 1)...), "a pg_dump custom-format archive"},
		"tar":        {tarFile(t), "a tar archive (Roadie doesn't read inside archives)"},
		"tar.gz":     {pack(t, compress.Gzip, tarFile(t)), "a tar archive (Roadie doesn't read inside archives)"},
		"zip":        {zipFile(t), "a zip archive (Roadie doesn't read inside archives)"},
		"wpress.sql": {randomBytes(HeadSize, 2), "an unknown binary file (.wpress, Duplicator or another backup format)"},
		"random.gz":  {pack(t, compress.Gzip, randomBytes(HeadSize, 3)), "an unknown binary file (.wpress, Duplicator or another backup format)"},
		"zeros.sql":  {make([]byte, 8192), "an unknown binary file (.wpress, Duplicator or another backup format)"},
	}
	for seed := uint64(10); seed < 60; seed++ {
		// Many random files: none may slip past as text.
		cases["random"+string(rune('A'+seed-10))] = struct {
			file []byte
			what string
		}{randomBytes(HeadSize, seed), "an unknown binary file (.wpress, Duplicator or another backup format)"}
	}
	for name, c := range cases {
		_, _, _, err := sniff(name, head(c.file))
		var ce *core.Error
		if !errors.As(err, &ce) || ce.Code != core.ErrDumpFormat || ce.Exit != 2 {
			t.Errorf("%s: err = %v, want dump-format", name, err)
			continue
		}
		if want := "dump-format: " + name + " is " + c.what + tail; ce.Msg != want {
			t.Errorf("%s: message\n got %q\nwant %q", name, ce.Msg, want)
		}
	}
	// The exported form names the input generically.
	if _, _, err := Sniff([]byte("PGDMP")); err == nil || err.Error() != "dump-format: the input is a pg_dump custom-format archive"+tail {
		t.Errorf("Sniff: %v", err)
	}
}

// Binary data inside quoted strings is normal (mysqldump without
// --hex-blob), so it doesn't make a dump binary.
func TestSniffBlobInString(t *testing.T) {
	var b bytes.Buffer
	b.WriteString("-- MySQL dump 10.13\nINSERT INTO `t` VALUES (1,'")
	for _, c := range randomBytes(3000, 7) {
		switch c {
		case '\'', '\\':
			b.WriteByte('\\')
		}
		b.WriteByte(c)
	}
	b.WriteString("');\n")
	if d, _, err := Sniff(b.Bytes()); err != nil || d != core.MySQL {
		t.Fatalf("Sniff = %q, %v", d, err)
	}
	// Latin1 text that isn't UTF-8 is text too.
	if _, _, err := Sniff([]byte("INSERT INTO t VALUES ('caf\xe9 \x80 na\xefve');\n")); err != nil {
		t.Fatalf("latin1: %v", err)
	}
}

func TestCheckInput(t *testing.T) {
	dir := t.TempDir()
	err := CheckInput(dir)
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Code != core.ErrDumpFormat ||
		ce.Msg != "dump-format: "+dir+" is a folder (pg_dump -Fd or mydumper output). Roadie reads plain SQL: pg_dump -Fp, mysqldump, mariadb-dump or wp db export." {
		t.Fatalf("dir: %v", err)
	}
	f := filepath.Join(dir, "a.sql")
	os.WriteFile(f, []byte("x"), 0o600)
	if err := CheckInput(f); err != nil {
		t.Fatalf("file: %v", err)
	}
	if err := CheckInput(filepath.Join(dir, "missing.sql")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing: %v", err)
	}
}

func FuzzSniff(f *testing.F) {
	f.Add([]byte("-- MySQL dump 10.13\n"))
	f.Add([]byte("PGDMP"))
	f.Add([]byte("\x1f\x8b\x08\x00"))
	f.Fuzz(func(t *testing.T, b []byte) {
		d, _, err := Sniff(b)
		if err != nil && d != "" {
			t.Fatalf("dialect %q with an error", d)
		}
	})
}

// A UTF-8 byte order mark before the dump is skipped, as the mysql
// client skips it: the dialect is found and the batches are the same as
// without it.
func TestOpenUTF8BOM(t *testing.T) {
	for _, path := range []string{"mysql/testdata/mysqldump8.sql", "mysql/testdata/phpmyadmin.sql", "pgsql/testdata/pg16-inserts.sql"} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		bom := append([]byte{0xef, 0xbb, 0xbf}, raw...)
		d0, _, err0 := Sniff(head(raw))
		d1, _, err1 := Sniff(head(bom))
		if err0 != nil || err1 != nil || d0 == "" || d0 != d1 {
			t.Errorf("%s: dialect %q %v with the mark, %q %v without", path, d1, err1, d0, err0)
		}
		for _, file := range [][]byte{bom, pack(t, compress.Gzip, bom)} {
			want := drainText(t, path, raw)
			if got := drainText(t, path, file); got != want {
				t.Errorf("%s: batches differ with the mark\n got %.300q\nwant %.300q", path, got, want)
			}
		}
	}
}

// drainText opens file and joins the text of every batch with its line.
func drainText(t *testing.T, name string, file []byte) string {
	t.Helper()
	src, err := Open(context.Background(), bytes.NewReader(file), Options{Name: name, KeepRoutines: true})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	defer src.Close()
	bs, err := drainAll(t, src)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var sb strings.Builder
	for _, b := range bs {
		if b.Pass != nil {
			fmt.Fprintf(&sb, "%d:%s|", b.Pass.Line, b.Pass.Text)
		}
		for _, r := range b.Rows {
			sb.Write(r.Src)
		}
	}
	return sb.String()
}

// A UTF-16 dump (Windows PowerShell 5's '>') is refused with a message
// that names UTF-16 and says how to export UTF-8, not as a backup format.
func TestSniffUTF16(t *testing.T) {
	text := "-- MySQL dump 10.13\n/*!40101 SET NAMES utf8mb4 */;\n"
	le, be := []byte{0xff, 0xfe}, []byte{0xfe, 0xff}
	for _, c := range text {
		le = append(le, byte(c), 0)
		be = append(be, 0, byte(c))
	}
	for name, file := range map[string][]byte{"le.sql": le, "be.sql": be, "le.sql.gz": pack(t, compress.Gzip, le)} {
		_, _, _, err := sniff(name, head(file))
		var ce *core.Error
		if !errors.As(err, &ce) || ce.Code != core.ErrDumpFormat || ce.Exit != 2 {
			t.Fatalf("%s: err = %v, want dump-format", name, err)
		}
		want := "dump-format: " + name + " is UTF-16 text, which Windows PowerShell writes for '>'. Export again with mysqldump --result-file=dump.sql (or mariadb-dump --result-file), which writes the file itself in UTF-8. Roadie reads plain SQL: pg_dump -Fp, mysqldump, mariadb-dump or wp db export."
		if ce.Msg != want {
			t.Errorf("%s: message\n got %q\nwant %q", name, ce.Msg, want)
		}
		if _, err := Open(context.Background(), bytes.NewReader(file), Options{Name: name}); !errors.As(err, &ce) || ce.Msg != want {
			t.Errorf("%s: Open: %v", name, err)
		}
	}
	// Plain UTF-8 that happens to start with 0xFF as latin1 text is not a
	// mark of its own: only the two-byte pair is.
	if _, _, err := Sniff([]byte("-- MySQL dump\n\xffSET NAMES latin1;\n")); err != nil {
		t.Errorf("latin1 byte: %v", err)
	}
}
