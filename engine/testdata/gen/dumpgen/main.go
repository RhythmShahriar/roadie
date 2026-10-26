// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Command dumpgen writes a generated WooCommerce dump for benchmarks and
// manual runs:
//
//	go run ./engine/testdata/gen/dumpgen -size 200MiB -o shop.sql
//
// The gateway key in it is live-shaped but made up, assembled here so no
// literal key sits in the repository.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/RhythmShahriar/roadie/engine/testdata/gen"
)

func main() {
	size := flag.String("size", "200MiB", "about how big the dump gets: bytes, or with KiB, MiB or GiB")
	seed := flag.Uint64("seed", 42, "the same seed gives the same dump")
	out := flag.String("o", "", "the file to write (default stdout)")
	flag.Parse()
	n, err := parseSize(*size)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dumpgen:", err)
		os.Exit(2)
	}
	w := os.Stdout
	if *out != "" {
		f, err := os.OpenFile(*out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			fmt.Fprintln(os.Stderr, "dumpgen:", err)
			os.Exit(1)
		}
		defer func() { _ = f.Close() }()
		w = f
	}
	bw := bufio.NewWriterSize(w, 1<<20)
	key := strings.Join([]string{"sk_", "live_", "51Hgen", "erated0000000000"}, "")
	if _, err := gen.Write(bw, gen.Options{Size: n, Seed: *seed, LiveKey: key}); err != nil {
		fmt.Fprintln(os.Stderr, "dumpgen:", err)
		os.Exit(1)
	}
	if err := bw.Flush(); err != nil {
		fmt.Fprintln(os.Stderr, "dumpgen:", err)
		os.Exit(1)
	}
}

// parseSize reads 1048576, 512KiB, 200MiB or 2GiB.
func parseSize(s string) (int64, error) {
	mult := int64(1)
	for _, u := range []struct {
		suffix string
		m      int64
	}{{"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}} {
		if v, ok := strings.CutSuffix(s, u.suffix); ok {
			s, mult = v, u.m
			break
		}
	}
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("-size %q isn't a size like 200MiB", s)
	}
	return n * mult, nil
}
