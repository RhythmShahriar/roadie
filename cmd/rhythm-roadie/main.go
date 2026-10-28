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
	"github.com/RhythmShahriar/rhythm/kit/sigverify"

	"github.com/RhythmShahriar/roadie/profile"
)

// version and commit are set at release time with -ldflags -X.
var version, commit string

func main() {
	// A closed pipe on stdout (copy --to - | head) fails the write with
	// EPIPE instead of killing the process, so the run stops like an
	// interrupt and removes its spool and temp files.
	catchSIGPIPE()
	os.Exit(entry(os.Args[1:], os.Stdout, os.Stderr))
}

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
	case args[0] == "update" && helpAsked(args[1:]), len(args) == 2 && args[0] == "help" && args[1] == "update":
		// update is kit's command; its help is Roadie's, with --clear.
		fmt.Fprint(stdout, updateHelpText())
		return cli.ExitOK
	case args[0] == "update" && cli.HasFlag(args[1:], "clear"):
		return clearProfiles(stdout, stderr)
	case args[0] == "update" && !helpAsked(args[1:]) && offlineUpdate(args[1:]):
		return updateOffline(args[1:], stdout, stderr)
	}
	return cli.Main(tool())
}

func tool() cli.Tool {
	setupProfilePack()
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
		// The release check runs before the command parses its flags, so
		// it reads them directly.
		Offline: func() bool { return skipReleaseCheck(os.Args[1:]) },
	}
}

// setupProfilePack tells the profile package this binary's version (the
// built-in profiles carry it) and the signature check rhythm-roadie
// update runs over the pack's checksums.txt: kit sigverify, the same
// trust path as the binary's own update. New does no network I/O.
func setupProfilePack() {
	profile.SetBinaryVersion(version)
	if v, err := sigverify.New(sigverify.Options{}); err == nil {
		profile.SetVerifier(v)
	}
}

// skipReleaseCheck reports whether the background release check must not
// start: --offline keeps it off the network; --json, -q, --quiet, CI or
// a stderr that isn't a terminal get no release notice. kit v0.8.0
// still prints the notice for --json, -q and a piped stderr once the
// check ran, so not starting it is the only way to keep machine-read and
// piped output clean.
func skipReleaseCheck(args []string) bool {
	if cli.HasFlag(args, "offline") || cli.QuietRun(args) {
		return true
	}
	if v, _ := dep.env("CI"); v != "" {
		return true
	}
	return !dep.stderrTerminal()
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
