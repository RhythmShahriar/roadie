// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Command oracle judges one testbed output with the independent oracle
// (package testbed/oracle) and prints its report as JSON:
//
//	go run ./testbed/oracle/cmd/oracle -canaries testbed/runtime/canaries.json \
//	    -scenario r01 -dump out.sql -tree out/ -input in.sql \
//	    -serialized testbed/runtime/fixtures/r01/serialized.json -php php
//
// With -list-serialized it instead writes the serialized.json of an input
// dump (the seeder does this at seed time). Exit codes: 0 clean, 1 the
// output failed a gate, 2 a usage or read error. The report never holds a
// canary value, only IDs.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/RhythmShahriar/roadie/testbed/oracle"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("oracle", flag.ContinueOnError)
	fl.SetOutput(stderr)
	canaries := fl.String("canaries", "testbed/runtime/canaries.json", "the seeded canaries")
	scenario := fl.String("scenario", "", "only this scenario's canaries (default: all)")
	dump := fl.String("dump", "", "the output database dump (plain or gzip)")
	tree := fl.String("tree", "", "the output file tree")
	input := fl.String("input", "", "the input dump, for row counts (and for -list-serialized)")
	serialized := fl.String("serialized", "", "the input's serialized.json, for the PHP integrity check")
	php := fl.String("php", "php", "the PHP command, split on spaces (e.g. \"docker run --rm -i php:8.3-cli php\")")
	list := fl.Bool("list-serialized", false, "write the serialized.json of -input to stdout and exit")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	ctx := context.Background()
	p := oracle.PHP{Cmd: strings.Fields(*php)}
	if *list {
		if *input == "" {
			fmt.Fprintln(stderr, "oracle: -list-serialized needs -input")
			return 2
		}
		rc, err := oracle.OpenDump(*input)
		if err != nil {
			fmt.Fprintln(stderr, "oracle:", err)
			return 2
		}
		defer func() { _ = rc.Close() }()
		cells, err := oracle.SerializedCells(ctx, rc, p)
		if err != nil {
			fmt.Fprintln(stderr, "oracle:", err)
			return 2
		}
		return write(stdout, stderr, oracle.SerializedFile{Version: 1, Cells: cells}, 0)
	}
	if *dump == "" && *tree == "" {
		fmt.Fprintln(stderr, "oracle: give -dump, -tree or both")
		return 2
	}
	cs, err := oracle.LoadCanaries(*canaries, *scenario)
	if err != nil {
		fmt.Fprintln(stderr, "oracle:", err)
		return 2
	}
	o := oracle.Options{Canaries: cs, Dump: *dump, Tree: *tree, Input: *input, PHP: p}
	if *serialized != "" {
		if o.Serialized, err = oracle.LoadSerialized(*serialized); err != nil {
			fmt.Fprintln(stderr, "oracle:", err)
			return 2
		}
		if o.Serialized == nil {
			o.Serialized = []oracle.Location{}
		}
	}
	rep, err := oracle.Run(ctx, o)
	if err != nil {
		fmt.Fprintln(stderr, "oracle:", err)
		return 2
	}
	code := 0
	if !rep.Clean() {
		code = 1
	}
	return write(stdout, stderr, rep, code)
}

func write(stdout, stderr io.Writer, v any, code int) int {
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintln(stderr, "oracle:", err)
		return 2
	}
	return code
}
