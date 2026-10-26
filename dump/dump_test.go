// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package dump

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/dump/compress"
)

func drainAll(t testing.TB, src core.Source) ([]*core.Batch, error) {
	t.Helper()
	var out []*core.Batch
	for {
		b, err := src.Next(context.Background())
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return out, err
		}
		out = append(out, b)
	}
}

// comparable strips what differs between compressions (the raw offset).
func comparable(bs []*core.Batch) []*core.Batch {
	out := make([]*core.Batch, len(bs))
	for i, b := range bs {
		c := *b
		c.Offset = 0
		out[i] = &c
	}
	return out
}

// Plain, gzip, zstd and bzip2 copies of one dump give the same batches,
// and the fingerprint is sha256sum of each file as stored.
func TestOpenCompressions(t *testing.T) {
	plain := fixture(t, "small.sql")
	var want []*core.Batch
	for _, kind := range []string{compress.None, compress.Gzip, compress.Zstd, compress.Bzip2} {
		file := plain
		switch kind {
		case compress.Gzip, compress.Zstd:
			file = pack(t, kind, plain)
		case compress.Bzip2:
			file = fixture(t, "small.sql.bz2")
		}
		src, err := Open(context.Background(), bytes.NewReader(file), Options{Name: "small.sql"})
		if err != nil {
			t.Fatalf("%q: %v", kind, err)
		}
		bs, err := drainAll(t, src)
		if err != nil {
			t.Fatalf("%q: %v", kind, err)
		}
		if want == nil {
			want = comparable(bs)
		} else if !reflect.DeepEqual(comparable(bs), want) {
			t.Errorf("%q: batches differ from the plain file's", kind)
		}
		sum := sha256.Sum256(file)
		if fp := src.Fingerprint(); fp.Kind != "sha256-file" || fp.Value != hex.EncodeToString(sum[:]) || fp.Bytes != int64(len(file)) {
			t.Errorf("%q: fingerprint %+v", kind, fp)
		}
		if sc := src.Schema(); sc.Dialect != core.MySQL || sc.Server != "8.0.36" || len(sc.Tables) != 2 {
			t.Errorf("%q: schema %+v", kind, sc)
		}
		if err := src.Close(); err != nil {
			t.Error(err)
		}
	}
	if len(want) < 50 {
		t.Fatalf("only %d batches", len(want))
	}
}

func TestOpenRefuses(t *testing.T) {
	for name, file := range map[string][]byte{
		"prod.dump":  []byte("PGDMP\x01\x0e\x00"),
		"wpress.sql": randomBytes(1<<17, 5),
		"bomb.gz":    pack(t, compress.Gzip, randomBytes(1<<17, 6)),
	} {
		_, err := Open(context.Background(), bytes.NewReader(file), Options{Name: name})
		var ce *core.Error
		if !errors.As(err, &ce) || ce.Code != core.ErrDumpFormat || !strings.HasPrefix(ce.Msg, "dump-format: "+name+" is ") {
			t.Errorf("%s: %v", name, err)
		}
	}
	// The real decompressed head is checked, not only what the raw head
	// decodes to: a zstd frame whose first block is past the head.
	big := randomBytes(1<<20, 9)
	if _, err := Open(context.Background(), bytes.NewReader(pack(t, compress.Zstd, big)), Options{Name: "x.zst"}); err == nil {
		t.Error("random zstd content accepted")
	}
}

// A gzip cut short is dump-truncated naming the table it stopped in.
func TestOpenCutGzip(t *testing.T) {
	plain := bytes.Repeat(fixture(t, "small.sql"), 1)
	var b bytes.Buffer
	b.Write(plain[:bytes.Index(plain, []byte("INSERT INTO `wp_options`"))])
	b.WriteString("INSERT INTO `wp_options` VALUES ")
	for i := range 20000 {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString("(" + strings.Repeat("7", 1+i%9) + ",'option_" + strings.Repeat("x", i%50) + "','value','yes')")
	}
	b.WriteString(";\n-- Dump completed on 2026-10-26 10:00:00\n")
	file := pack(t, compress.Gzip, b.Bytes())
	src, err := Open(context.Background(), bytes.NewReader(file[:len(file)*3/4]), Options{Name: "prod.sql.gz"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = drainAll(t, src)
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Code != core.ErrDumpTruncated || ce.Exit != 3 ||
		!strings.HasPrefix(ce.Msg, "dump-truncated: prod.sql.gz ends inside table wp_options at 0.0 GB: the gzip stream is cut or damaged.") {
		t.Fatalf("err = %v", err)
	}
}

// PostgreSQL dumps wait for the pgsql reader.
func TestOpenPostgresNotYet(t *testing.T) {
	_, err := Open(context.Background(), strings.NewReader("--\n-- PostgreSQL database dump\n--\n"), Options{})
	if !errors.Is(err, core.ErrNotBuilt) {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Open(ctx, strings.NewReader("SELECT 1;\n"), Options{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

// notes records the plan lines a source sends to Progress.
type notes struct{ lines []string }

func (n *notes) Phase(string, int64) {}
func (n *notes) Advance(int64)       {}
func (n *notes) Table(string, int64) {}
func (n *notes) Note(msg string)     { n.lines = append(n.lines, msg) }

// Open applies the allowlist: a GRANT is left out with a plan line, USE
// of the dump's own database is dropped, a trigger stops the run and
// with KeepRoutines comes out without its DEFINER.
func TestOpenAllowlist(t *testing.T) {
	in := strings.Join([]string{
		"-- MySQL dump 10.13  Distrib 8.0.36, for Linux (x86_64)",
		"--",
		"-- Host: localhost    Database: shop",
		"USE `shop`;",
		"CREATE TABLE `wp_users` (`ID` int, `user_email` varchar(100));",
		"INSERT INTO `wp_users` VALUES (1,'a@shop.test');",
		"/*!50000 GRANT ALL ON *.* TO x */;",
		"DELIMITER ;;",
		"/*!50003 CREATE*/ /*!50017 DEFINER=`root`@`localhost`*/ /*!50003 TRIGGER `t1` AFTER UPDATE ON `wp_users` FOR EACH ROW SET @x = 1 */;;",
		"DELIMITER ;",
		"-- Dump completed on 2026-10-26 10:00:00",
		"",
	}, "\n")
	_, err := drainAll(t, mustOpen(t, in, Options{Name: "prod.sql"}))
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Code != core.ErrDumpRefused || ce.Line != 9 ||
		ce.Msg != "dump-refused: prod.sql line 9 holds a CREATE TRIGGER statement, which could run code on the staging database. Export with --skip-triggers, or pass --keep-routines to keep it without its DEFINER." {
		t.Fatalf("err = %v", err)
	}

	n := &notes{}
	src := mustOpen(t, in, Options{Name: "prod.sql", KeepRoutines: true, Progress: n})
	bs, err := drainAll(t, src)
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	for _, b := range bs {
		if b.Pass != nil {
			text.Write(b.Pass.Text)
		}
	}
	if s := text.String(); strings.Contains(s, "GRANT") || strings.Contains(s, "USE `") || strings.Contains(s, "`root`") ||
		!strings.Contains(s, "/*!50017 DEFINER=CURRENT_USER*/") {
		t.Errorf("passthrough text:\n%s", s)
	}
	want := []string{
		"line 4: dropped USE: the import goes to the database --to names",
		"line 7: dropped GRANT: the staging database keeps its own users and privileges",
		"line 9: kept CREATE TRIGGER t1 with DEFINER=CURRENT_USER (--keep-routines)",
	}
	if !reflect.DeepEqual(n.lines, want) || !reflect.DeepEqual(Notes(src), want) {
		t.Errorf("notes %q / %q", n.lines, Notes(src))
	}
	if Notes(nil) != nil {
		t.Error("notes from a nil source")
	}
}

func mustOpen(t testing.TB, in string, o Options) core.Source {
	t.Helper()
	src, err := Open(context.Background(), strings.NewReader(in), o)
	if err != nil {
		t.Fatal(err)
	}
	return src
}

func TestDatabaseOf(t *testing.T) {
	for head, want := range map[string]string{
		"-- MySQL dump 10.13\n--\n-- Host: 127.0.0.1    Database: shop\n": "shop",
		"-- phpMyAdmin SQL Dump\n-- Database: `wp_live`\n":                "wp_live",
		"-- Adminer 4.8.1 MySQL 8.0.31 dump\n":                            "",
		"INSERT INTO t VALUES ('-- Database: x');\n":                      "",
	} {
		if got := databaseOf([]byte(head)); got != want {
			t.Errorf("%q: %q", head, got)
		}
	}
}
