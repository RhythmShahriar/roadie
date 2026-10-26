// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package mysql

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
)

// The I/O benchmarks run over the WooCommerce-shaped dump of genDump
// (wp_postmeta 30% of the bytes with serialized values, 1 MB extended
// INSERTs): 32 MB by default, the 378 MB shape with ROADIE_BENCH_FULL=1.
// CI runs them with -benchtime=1x as a smoke test.

// BenchmarkTokenize is the lexer alone: statements and every row's
// values decoded, with one arena reused per 1,000 rows as a batch would.
func BenchmarkTokenize(b *testing.B) {
	path := benchDump(b)
	data, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		l := NewLexer(bytes.NewReader(data), Options{})
		a := &Arena{}
		rows := 0
		for {
			st, err := l.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				b.Fatal(err)
			}
			if !st.Rows {
				continue
			}
			for {
				_, ok, err := l.Row(a)
				if err != nil {
					b.Fatal(err)
				}
				if !ok {
					break
				}
				if rows++; rows%1000 == 0 {
					a.Src, a.Raw, a.Vals = a.Src[:0], a.Raw[:0], a.Vals[:0]
				}
			}
			if _, err := l.Tail(); err != nil {
				b.Fatal(err)
			}
		}
		if rows == 0 {
			b.Fatal("no rows")
		}
	}
}

// readBatches reads a whole dump into batches, for the writer benchmark.
func readBatches(tb testing.TB, data []byte) []*core.Batch {
	tb.Helper()
	s := NewSource(bytes.NewReader(data), SourceOptions{})
	var out []*core.Batch
	for {
		bt, err := s.Next(context.Background())
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			tb.Fatal(err)
		}
		out = append(out, bt)
	}
}

// BenchmarkRoundTrip reads the dump and writes every row back untouched.
func BenchmarkRoundTrip(b *testing.B) {
	path := benchDump(b)
	data, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		s := NewSource(bytes.NewReader(data), SourceOptions{})
		w := NewWriter(io.Discard, WriterOptions{})
		for {
			bt, err := s.Next(context.Background())
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				b.Fatal(err)
			}
			if err := w.Write(bt); err != nil {
				b.Fatal(err)
			}
		}
		if err := w.Finish(); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkWrite is the writer alone on untouched rows, which it copies
// from their source bytes.
func BenchmarkWrite(b *testing.B) {
	path := benchDump(b)
	data, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}
	batches := readBatches(b, data)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		w := NewWriter(io.Discard, WriterOptions{})
		for _, bt := range batches {
			if err := w.Write(bt); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// Reading and writing back an untouched row costs fewer than 4
// allocations: values point into the batch arena.
func TestAllocsPerUntouchedRow(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector adds allocations")
	}
	var buf bytes.Buffer
	genDump(&buf, 4_000_000, 3)
	data := buf.Bytes()
	rows := 0
	for _, bt := range readBatches(t, data) {
		rows += len(bt.Rows)
	}
	allocs := testing.AllocsPerRun(3, func() {
		s := NewSource(bytes.NewReader(data), SourceOptions{})
		w := NewWriter(io.Discard, WriterOptions{})
		for {
			bt, err := s.Next(context.Background())
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := w.Write(bt); err != nil {
				t.Fatal(err)
			}
		}
	})
	per := allocs / float64(rows)
	t.Logf("%.0f allocations for %d rows: %.3f per row", allocs, rows, per)
	if per >= 4 {
		t.Fatalf("%.2f allocations per untouched row, want under 4", per)
	}
	// The writer alone allocates nothing per untouched row.
	batches := readBatches(t, data)
	wallocs := testing.AllocsPerRun(3, func() {
		w := NewWriter(io.Discard, WriterOptions{})
		for _, bt := range batches {
			if err := w.Write(bt); err != nil {
				t.Fatal(err)
			}
		}
	})
	if wper := wallocs / float64(rows); wper >= 0.01 {
		t.Fatalf("the writer allocates %.3f per untouched row", wper)
	}
}
