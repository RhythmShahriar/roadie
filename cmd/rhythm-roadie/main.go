// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Command rhythm-roadie makes pseudonymised staging copies of production
// with live keys swapped and side effects off, then proves it with a
// signed report.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/RhythmShahriar/rhythm/kit/cli"
)

// version and commit are set at release time with -ldflags -X.
var version, commit string

func main() { os.Exit(cli.Main(tool())) }

func tool() cli.Tool {
	return cli.Tool{
		Name:    "rhythm-roadie",
		Version: version,
		Commit:  commit,
		Summary: "Is my staging a safe copy of production? Make pseudonymised staging copies with live keys swapped and side effects off, then prove it with a signed report.",
		Commands: []cli.Command{
			stub("copy", "--from <source> --to <target> [flags]", "copy production to staging: pseudonymise, swap keys, turn side effects off, verify"),
			stub("prepare", "<staging> [flags]", "make an existing staging copy safe in place"),
			stub("verify", "[staging] [flags]", "look for real people, live keys and production URLs; signed pass/fail report"),
			stub("plan", "--from <source> [--to <target>] | <staging>", "dry run: every change copy or prepare would make"),
			stub("config", "init [--stack s] | check | show | key show|rotate|forget", "write, validate or print .roadie/config.yaml and its key"),
			stub("report", "<report.json>... -o <file> | check <report.json>", "render saved reports or check a report's signature"),
			stub("profiles", "[name] [--json]", "list the stack profiles and what each rule changes"),
			stub("explain", "<id>", "a rule's or finding's full explanation, offline"),
			stub("demo", "[--keep]", "try the whole loop on a built-in shop"),
		},
		Update:       nil, // set by the cli lane to profile.Update
		UpdatePinned: nil,
	}
}

// stub is a command the cli lane hasn't built yet: it answers --help and
// otherwise says it isn't built and exits 3.
func stub(name, args, summary string) cli.Command {
	synopsis := "Usage: rhythm-roadie " + name + " " + args + "\n\n" + summary + "."
	flags := func(stderr io.Writer) *flag.FlagSet { return cli.NewFlagSet(name, synopsis, stderr) }
	return cli.Command{
		Name:    name,
		Args:    args,
		Summary: summary,
		Flags:   func() *flag.FlagSet { return flags(io.Discard) },
		Run: func(_ context.Context, argv []string, _, stderr io.Writer) error {
			if _, err := cli.ParseArgs(flags(stderr), argv); err != nil {
				return err
			}
			fmt.Fprintf(stderr, "rhythm-roadie %s: not built yet\n", name)
			return &cli.ExitError{Code: cli.ExitUnavailable}
		},
	}
}
