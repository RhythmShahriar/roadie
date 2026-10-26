// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package dump

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/dump/compress"
)

// progress records what a source reports.
type progress struct {
	mu      sync.Mutex
	phases  int
	advance int64
	tables  []string
	notes   []string
}

func (p *progress) Phase(string, int64) { p.mu.Lock(); p.phases++; p.mu.Unlock() }
func (p *progress) Advance(n int64)     { p.mu.Lock(); p.advance += n; p.mu.Unlock() }
func (p *progress) Table(name string, _ int64) {
	p.mu.Lock()
	p.tables = append(p.tables, name)
	p.mu.Unlock()
}
func (p *progress) Note(msg string) { p.mu.Lock(); p.notes = append(p.notes, msg); p.mu.Unlock() }

// fixtureFiles lists every committed dump fixture of both dialects.
func fixtureFiles(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, pat := range []string{"mysql/testdata/*.sql", "mysql/testdata/crlf/*.sql", "pgsql/testdata/pg16-*.sql", "testdata/small.sql*"} {
		m, err := filepath.Glob(pat)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, m...)
	}
	if len(out) < 12 {
		t.Fatalf("only %d fixtures", len(out))
	}
	return out
}

// Every fixture, raw and compressed, opens and drains end to end: the
// progress counts every raw byte, names every table with rows and never
// sets the phase; the fingerprint is the file's SHA-256.
func TestOpenEveryFixture(t *testing.T) {
	for _, path := range fixtureFiles(t) {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		inputs := map[string][]byte{"": raw}
		if !strings.HasSuffix(path, ".bz2") {
			inputs[".gz"] = pack(t, compress.Gzip, raw)
			inputs[".zst"] = pack(t, compress.Zstd, raw)
		}
		for ext, file := range inputs {
			name := filepath.Base(path) + ext
			p := &progress{}
			src, err := Open(context.Background(), bytes.NewReader(file), Options{Name: name, KeepRoutines: true, Progress: p})
			if err != nil {
				t.Errorf("%s: %v", name, err)
				continue
			}
			bs, err := drainAll(t, src)
			if err != nil {
				t.Errorf("%s: %v", name, err)
				continue
			}
			if p.phases != 0 {
				t.Errorf("%s: dump.Open set the phase %d times", name, p.phases)
			}
			if p.advance != int64(len(file)) {
				t.Errorf("%s: progress advanced %d of %d bytes", name, p.advance, len(file))
			}
			seen := map[string]bool{}
			for _, tb := range p.tables {
				seen[tb] = true
			}
			for i, b := range bs {
				// Statements the allowlist drops leave gaps.
				if i > 0 && b.Seq <= bs[i-1].Seq {
					t.Fatalf("%s: seq %d after %d", name, b.Seq, bs[i-1].Seq)
				}
				if b.Table != nil && len(b.Rows) > 0 && !seen[b.Table.Name] {
					t.Errorf("%s: rows of %s with no Progress.Table", name, b.Table.Name)
				}
			}
			sum := sha256.Sum256(file)
			if fp := src.Fingerprint(); fp.Value != hex.EncodeToString(sum[:]) || fp.Bytes != int64(len(file)) {
				t.Errorf("%s: fingerprint %+v", name, fp)
			}
			if src.Schema() == nil || len(src.Schema().Tables) == 0 {
				t.Errorf("%s: no tables in the schema", name)
			}
			if err := src.Close(); err != nil {
				t.Errorf("%s: close: %v", name, err)
			}
		}
	}
}

// genDump writes a mysqldump-shaped dump of rows rows to w, one
// extended INSERT per 1,000 rows.
func genDump(w io.Writer, rows int) error {
	bw := bufio.NewWriter(w)
	fmt.Fprint(bw, "-- MySQL dump 10.13  Distrib 8.0.36\n/*!40101 SET NAMES utf8mb4 */;\n"+
		"CREATE TABLE `wp_postmeta` (\n  `meta_id` bigint unsigned NOT NULL AUTO_INCREMENT,\n  `meta_value` longtext,\n  PRIMARY KEY (`meta_id`)\n) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;\n")
	for i := 0; i < rows; i++ {
		if i%1000 == 0 {
			if i > 0 {
				fmt.Fprint(bw, ";\n")
			}
			fmt.Fprint(bw, "INSERT INTO `wp_postmeta` VALUES ")
		} else {
			fmt.Fprint(bw, ",")
		}
		fmt.Fprintf(bw, "(%d,'a:1:{s:5:\\\"email\\\";s:29:\\\"customer-%05d@example.invalid\\\";}')", i+1, i%100000)
	}
	fmt.Fprint(bw, ";\n-- Dump completed on 2026-10-26 12:00:00\n")
	return bw.Flush()
}

// Canceling mid-stream stops within one batch with the context's error.
func TestOpenCancelMidStream(t *testing.T) {
	var buf bytes.Buffer
	if err := genDump(&buf, 50000); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	src, err := Open(ctx, &buf, Options{Name: "big.sql"})
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	for i := 0; i < 5; i++ {
		if _, err := src.Next(ctx); err != nil {
			t.Fatal(err)
		}
	}
	cancel()
	if _, err := src.Next(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Next after cancel: %v", err)
	}
	if fp := src.Fingerprint(); fp.Value != "" {
		t.Fatalf("a canceled source has a fingerprint: %+v", fp)
	}
}

// Every error about a place in the dump carries its line.
func TestOpenErrorsCarryLine(t *testing.T) {
	head := "-- MySQL dump 10.13  Distrib 8.0.36\n"
	create := "CREATE TABLE `t` (\n  `a` int,\n  `b` text\n);\n"
	cases := []struct {
		name, text string
		o          Options
		code       string
		line       int64
	}{
		{"refused", head + "/*!40101 SET NAMES utf8mb4 */;\nLOAD DATA LOCAL INFILE '/etc/passwd' INTO TABLE t;\n-- Dump completed\n", Options{}, core.ErrDumpRefused, 3},
		{"data only", head + "\nINSERT INTO `wp_x` VALUES (1,'a');\n-- Dump completed\n", Options{}, core.ErrDumpFormat, 3},
		{"too large", head + create + "INSERT INTO `t` VALUES (1,'" + strings.Repeat("x", 4096) + "');\n-- Dump completed\n", Options{MaxValue: 1024}, core.ErrValueTooLarge, 6},
		{"cut in a string", head + create + "INSERT INTO `t` VALUES (1,'open\n", Options{}, core.ErrDumpTruncated, 6},
	}
	for _, c := range cases {
		src, err := Open(context.Background(), strings.NewReader(c.text), c.o)
		if err == nil {
			_, err = drainAll(t, src)
		}
		var ce *core.Error
		if !errors.As(err, &ce) || ce.Code != c.code {
			t.Errorf("%s: %v, want %s", c.name, err, c.code)
			continue
		}
		if ce.Line != c.line {
			t.Errorf("%s: line %d, want %d (%s)", c.name, ce.Line, c.line, ce.Msg)
		}
	}
}

// A 600 MB single INSERT drains with the heap under 64 MiB: the source
// streams; it never holds the statement.
func TestOpenHugeInsertMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("streams 600 MB; runs without -short")
	}
	if raceEnabled {
		t.Skip("streams 600 MB; the race detector makes it take minutes")
	}
	const target = 600 << 20
	const row = 1000
	pr, pw := io.Pipe()
	go func() {
		bw := bufio.NewWriterSize(pw, 1<<20)
		fmt.Fprint(bw, "-- MySQL dump 10.13  Distrib 8.0.36\nCREATE TABLE `wp_huge` (\n  `id` bigint NOT NULL,\n  `v` longtext,\n  PRIMARY KEY (`id`)\n);\nINSERT INTO `wp_huge` VALUES ")
		val := strings.Repeat("x", row)
		n := 0
		for i := 0; n < target; i++ {
			if i > 0 {
				_ = bw.WriteByte(',')
			}
			k, _ := fmt.Fprintf(bw, "(%d,'%s')", i, val)
			n += k
		}
		fmt.Fprint(bw, ";\n-- Dump completed\n")
		_ = bw.Flush()
		_ = pw.Close()
	}()
	src, err := Open(context.Background(), pr, Options{Name: "huge.sql"})
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	start := time.Now()
	var peak uint64
	var rows, batches int
	var ms runtime.MemStats
	for {
		b, err := src.Next(context.Background())
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		rows += len(b.Rows)
		batches++
		if batches%16 == 0 {
			runtime.ReadMemStats(&ms)
			peak = max(peak, ms.HeapInuse)
		}
	}
	if rows < target/(row+12) {
		t.Fatalf("%d rows", rows)
	}
	t.Logf("%d rows in %d batches, %s, peak HeapInuse %.1f MiB", rows, batches, time.Since(start).Round(time.Millisecond), float64(peak)/(1<<20))
	if peak >= 64<<20 {
		t.Fatalf("HeapInuse peaked at %.1f MiB", float64(peak)/(1<<20))
	}
}
