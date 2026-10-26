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
	"path/filepath"

	"github.com/RhythmShahriar/rhythm/kit/cli"
	"github.com/RhythmShahriar/rhythm/kit/config"

	"github.com/RhythmShahriar/roadie/profile"
)

// version and commit are set at release time with -ldflags -X.
var version, commit string

func main() { os.Exit(entry(os.Args[1:], os.Stdout, os.Stderr)) }

// entry answers what kit's cli.Main can't say Roadie's way, then hands
// over: no arguments on a terminal open the picker; the top help is PRD 6.1's text rather than kit's generic one;
// update --clear drops the cached profile pack and update offline reports
// the pack in use without a download. Everything else (commands,
// help <command>, version, update, __complete, Ctrl-C and the update
// prompt) is cli.Main's.
func entry(args []string, stdout, stderr io.Writer) int {
	switch {
	case len(args) == 0 && dep.terminal():
		return runPicker(stdout, stderr)
	case len(args) == 0:
		printTopHelp(stdout)
		return cli.ExitOK
	case len(args) == 1 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help"),
		len(args) == 2 && args[0] == "help" && args[1] == "help":
		printTopHelp(stdout)
		return cli.ExitOK
	case args[0] == "update" && cli.HasFlag(args[1:], "clear"):
		return clearProfiles(stdout, stderr)
	case args[0] == "update" && !helpAsked(args[1:]) && offlineUpdate(args[1:]):
		return updateOffline(args[1:], stdout, stderr)
	}
	return cli.Main(tool())
}

func tool() cli.Tool {
	return cli.Tool{
		Name:    "rhythm-roadie",
		Version: version,
		Commit:  commit,
		Summary: "Is my staging a safe copy of production? Make pseudonymised staging copies with live keys swapped and side effects off, then prove it with a signed report.",
		Commands: []cli.Command{
			command("copy", "--from <source> --to <target> [flags]", "copy production to staging: pseudonymise, swap keys, turn side effects off, verify", runCopy),
			command("prepare", "<staging> [flags]", "make an existing staging copy safe in place", runPrepare),
			command("verify", "[staging] [flags]", "look for real people, live keys and production URLs; signed pass/fail report", runVerify),
			command("plan", "--from <source> [--to <target>] | <staging>", "dry run: every change copy or prepare would make", runPlan),
			command("config", "init [--stack s] | check | show | key show|rotate|forget", "write, validate or print .roadie/config.yaml and its key", runConfig),
			command("report", "<report.json>... -o <file> | check <report.json>", "render saved reports or check a report's signature", runReport),
			command("profiles", "[name] [--json]", "list the stack profiles and what each rule changes", runProfiles),
			command("explain", "<id>", "a rule's or finding's full explanation, offline", runExplain),
			command("demo", "[--keep]", "try the whole loop on a built-in shop", runDemo),
		},
		Update:       profile.Update,
		UpdatePinned: profile.UpdatePinned,
		// --offline keeps the release check off the network too; it runs
		// before the command parses its flags, so it reads them directly.
		Offline: func() bool { return cli.HasFlag(os.Args[1:], "offline") },
	}
}

// runFunc is a command's work once its flags are parsed.
type runFunc func(ctx context.Context, p *parsed, stdout, stderr io.Writer) error

// command wires a command into kit: it parses the flags (answering
// --help with the command's own help) and runs.
func command(name, args, summary string, run runFunc) cli.Command {
	return cli.Command{
		Name:    name,
		Args:    args,
		Summary: summary,
		Flags:   func() *flag.FlagSet { return newFlags().flagSet(name) },
		Run: func(ctx context.Context, argv []string, stdout, stderr io.Writer) error {
			p, err := parse(name, argv, stdout, stderr)
			if err != nil {
				return err
			}
			showFirstRun(p, stderr)
			return run(ctx, p, stdout, stderr)
		},
	}
}

// clearProfiles is update --clear: it removes the cached profile pack, so
// the built-in profiles apply until the next update.
func clearProfiles(stdout, stderr io.Writer) int {
	dir, err := config.CacheDir("roadie")
	if err == nil {
		err = os.RemoveAll(filepath.Join(dir, "profiles"))
	}
	if err != nil {
		fmt.Fprintf(stderr, "%s update: %v\n", prog(), err)
		return cli.ExitUnavailable
	}
	fmt.Fprintln(stdout, "Removed the cached profile pack; Roadie uses its built-in profiles until rhythm-roadie update runs again.")
	return cli.ExitOK
}
