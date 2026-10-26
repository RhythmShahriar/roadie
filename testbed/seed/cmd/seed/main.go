// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Command seed fills the testbed's scenario templates with canaries and
// fake keys (package testbed/seed):
//
//	go run ./testbed/seed/cmd/seed -fixtures testbed/fixtures -out testbed/runtime [-only r01,r04] [-php php]
//
// It prints how many canaries each fixture got, never a value.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/RhythmShahriar/roadie/testbed/oracle"
	"github.com/RhythmShahriar/roadie/testbed/seed"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("seed", flag.ContinueOnError)
	fl.SetOutput(stderr)
	fixtures := fl.String("fixtures", "testbed/fixtures", "the scenario templates")
	out := fl.String("out", "testbed/runtime", "where the seeded files go (gitignored)")
	seedFlag := fl.Int64("seed", 1, "the seed every canary and key comes from")
	only := fl.String("only", "", "comma-separated fixture IDs to reseed (default: all)")
	php := fl.String("php", "php", `the PHP command for serialized.json, split on spaces; "" skips it`)
	if err := fl.Parse(args); err != nil {
		return 2
	}
	o := seed.Options{Fixtures: *fixtures, Out: *out, Seed: *seedFlag, PHP: oracle.PHP{Cmd: strings.Fields(*php)}}
	if *only != "" {
		o.Only = strings.Split(*only, ",")
	}
	res, err := seed.Run(context.Background(), o)
	if err != nil {
		fmt.Fprintln(stderr, "seed:", err)
		return 1
	}
	ids := make([]string, 0, len(res.PerFixture))
	for id := range res.PerFixture {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		fmt.Fprintf(stdout, "%-6s %5d canaries and keys\n", id, res.PerFixture[id])
	}
	fmt.Fprintf(stdout, "total  %5d in %s/canaries.json\n", len(res.Canaries), *out)
	return 0
}
