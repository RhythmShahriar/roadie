// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Command demodump writes the demo shop's MySQL dump to a file, for the
// Action's own tests (.github/workflows/action-test.yml). The shop holds
// fake customers only; -leaky leaves them, a live-looking key and
// production URLs where a careless copy would, so verify must fail on it.
//
//	go run ./scripts/demodump -o shop.sql [-leaky] [-seed n] [-customers n]
//	go run ./scripts/demodump -production   prints the demo's production hosts
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/RhythmShahriar/roadie/internal/demo"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

// shop writes the dump; the tests swap it for one that fails.
var shop = demo.Shop

// run is main with its arguments and streams, for the tests.
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("demodump", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("o", "", "write the dump to this file")
	leaky := fs.Bool("leaky", false, "leave the fake customers, key and URLs in place")
	seed := fs.Int64("seed", 1, "seed for the fake customers")
	customers := fs.Int("customers", 20, "how many fake customers")
	production := fs.Bool("production", false, "print the demo's production hosts, comma-separated")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	switch {
	case *production:
		fmt.Fprintln(stdout, strings.Join(demo.Production(), ","))
		return 0
	case *out == "" || fs.NArg() > 0:
		fmt.Fprintln(stderr, "usage: demodump -o <file> [-leaky] [-seed n] [-customers n] | -production")
		return 2
	case *customers < 1:
		fmt.Fprintln(stderr, "demodump: -customers wants 1 or more")
		return 2
	}
	if err := write(*out, demo.Options{Seed: *seed, Customers: *customers, Leaky: *leaky}); err != nil {
		fmt.Fprintf(stderr, "demodump: %v\n", err)
		return 1
	}
	return 0
}

// write writes the dump to path, 0600 like every file Roadie writes, and
// removes a half-written file on failure.
func write(path string, o demo.Options) (err error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			err = errors.Join(err, os.Remove(path))
		}
	}()
	w := bufio.NewWriter(f)
	if err := shop(w, o); err != nil {
		return err
	}
	return w.Flush()
}
