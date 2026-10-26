// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package pgsql

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"testing"
)

// genCopyDump writes a pg_dump -Fp shaped dump of about size bytes: a
// WooCommerce-like postmeta table (30%, serialized values with tabs and
// backslashes to escape) and an orders table in COPY blocks. Every
// value is made up and every address uses a reserved domain.
func genCopyDump(w io.Writer, size int64) int64 {
	bw := bufio.NewWriterSize(w, 1<<20)
	defer bw.Flush()
	var n int64
	put := func(s string) { n += int64(len(s)); _, _ = bw.WriteString(s) }
	put("--\n-- PostgreSQL database dump\n--\n\nSET standard_conforming_strings = on;\n\n")
	put("CREATE TABLE public.postmeta (\n    meta_id bigint NOT NULL,\n    post_id bigint NOT NULL,\n    meta_key character varying(255),\n    meta_value text\n);\n\n")
	put("CREATE TABLE public.orders (\n    id bigint NOT NULL,\n    email character varying(100),\n    note text,\n    total numeric(10,2)\n);\n\n")
	put("COPY public.postmeta (meta_id, post_id, meta_key, meta_value) FROM stdin;\n")
	for id := 1; n < size*30/100; id++ {
		put(fmt.Sprintf("%d\t%d\t_shipping_lines\ta:2:{s:4:\"name\";s:13:\"Flat rate %03d\";s:4:\"note\";s:9:\"tab\\there\";}\n", id, id/4, id%1000))
	}
	put("\\.\n\nCOPY public.orders (id, email, note, total) FROM stdin;\n")
	for id := 1; n < size; id++ {
		put(fmt.Sprintf("%d\tcustomer%d@example.test\tOrder status changed from Pending payment to Processing.\\nCall C:\\\\shop first.\t%d.99\n", id, id, id%500))
	}
	put("\\.\n\n--\n-- PostgreSQL database dump complete\n--\n\n")
	return n
}

func copyBenchSize() int64 {
	if os.Getenv("ROADIE_BENCH_FULL") == "1" {
		return 378_000_000
	}
	if s := os.Getenv("ROADIE_BENCH_MB"); s != "" {
		if mb, err := strconv.Atoi(s); err == nil {
			return int64(mb) * 1_000_000
		}
	}
	return 32_000_000
}

// BenchmarkCopy reads a COPY dump and writes every row back untouched.
func BenchmarkCopy(b *testing.B) {
	var buf bytes.Buffer
	genCopyDump(&buf, copyBenchSize())
	data := buf.Bytes()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		s := NewSource(bytes.NewReader(data), SourceOptions{Name: "bench.sql"})
		w := NewWriter(io.Discard)
		rows := 0
		for {
			bt, err := s.Next(context.Background())
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				b.Fatal(err)
			}
			rows += len(bt.Rows)
			if err := w.Write(bt); err != nil {
				b.Fatal(err)
			}
		}
		if rows == 0 {
			b.Fatal("no rows")
		}
	}
}

// The generated COPY dump reads back byte for byte, and an untouched
// COPY row costs fewer than 4 allocations.
func TestGenCopyDump(t *testing.T) {
	var buf bytes.Buffer
	genCopyDump(&buf, 2_000_000)
	bs, _, err := drain(t, buf.Bytes(), SourceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(write(t, bs), buf.Bytes()) {
		t.Fatal("the generated dump doesn't round-trip")
	}
	if raceEnabled {
		return
	}
	rows := 0
	for _, b := range bs {
		rows += len(b.Rows)
	}
	data := buf.Bytes()
	allocs := testing.AllocsPerRun(3, func() {
		s := NewSource(bytes.NewReader(data), SourceOptions{Name: "bench.sql"})
		w := NewWriter(io.Discard)
		for {
			bt, err := s.Next(context.Background())
			if errors.Is(err, io.EOF) {
				return
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
		t.Fatalf("%.2f allocations per untouched COPY row, want under 4", per)
	}
}
