// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/rhythm/kit/cli"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/roadiecfg"
)

// testNow is the fixed clock of every in-process test.
var testNow = time.Date(2026, 10, 26, 14, 2, 11, 0, time.UTC)

// fakeEndpoint parses endpoints the way the site lane does for the shapes
// the tests use, so the CLI is tested without the real parser.
func fakeEndpoint(s string) (core.Endpoint, error) {
	e := core.Endpoint{Raw: s, Display: s, Path: s}
	switch {
	case s == "":
		return e, errors.New("empty endpoint")
	case s == "-":
		e.Kind = core.Stdio
	case strings.HasPrefix(s, "mysql://"):
		e.Kind, e.Driver, e.Path = core.DSN, core.MySQL, ""
		rest := strings.TrimPrefix(s, "mysql://")
		if at := strings.LastIndex(rest, "@"); at >= 0 {
			cred := rest[:at]
			if user, pw, ok := strings.Cut(cred, ":"); ok {
				e.User, e.Password = user, pw
				e.Display = "mysql://" + user + ":•••@" + rest[at+1:]
			}
			rest = rest[at+1:]
		}
		e.Host, e.DSN = strings.Split(rest, "/")[0], "mysql://"+rest
	case strings.HasPrefix(s, "ssh://"):
		host, path, _ := strings.Cut(strings.TrimPrefix(s, "ssh://"), "/")
		e.Kind, e.Path = core.SSHSite, "/"+path
		if user, h, ok := strings.Cut(host, "@"); ok {
			e.User, host = user, h
		}
		// A port is kept apart from the host, as site.ParseEndpoint does.
		if h, port, ok := strings.Cut(host, ":"); ok {
			if n, err := strconv.Atoi(port); err == nil {
				host, e.Port = h, n
			}
		}
		e.Host = host
	case strings.HasPrefix(s, "@"):
		e.Kind, e.Alias = core.WPAlias, s
	case strings.HasPrefix(s, "/") || strings.HasPrefix(s, "./"):
		e.Kind = core.LocalSite
		if strings.HasSuffix(s, ".sql") || strings.HasSuffix(s, ".sql.gz") {
			e.Kind = core.DumpFile
		}
	case strings.HasPrefix(s, "bad:"):
		return e, core.Errorf("endpoint", cli.ExitUsage, "can't read this endpoint")
	default:
		e.Kind = core.DumpFile
	}
	return e, nil
}

// testDeps replaces dep for one test with fakes and no config file, no
// terminal and the fixed clock; set changes more before the test runs.
func testDeps(t *testing.T, set func(d *deps)) {
	t.Helper()
	old := dep
	t.Cleanup(func() { dep = old })
	// A config folder of its own where the first-run note was shown
	// already, so it shows only in the test about it.
	cfgDir := t.TempDir()
	t.Setenv("RHYTHM_CONFIG_DIR", cfgDir)
	if err := os.MkdirAll(filepath.Join(cfgDir, "seen"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "seen", firstRunKey), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	d := realDeps()
	d.parseEndpoint = fakeEndpoint
	d.findConfig = func(string) (string, error) { return "", nil }
	d.loadConfig = func(string, func(string) (string, bool)) (*roadiecfg.Config, error) {
		return nil, errors.New("no config in this test")
	}
	d.defaultConfig = func() *roadiecfg.Config { return nil }
	d.now = func() time.Time { return testNow }
	d.env = func(string) (string, bool) { return "", false }
	d.stdin = strings.NewReader("")
	d.terminal = func() bool { return false }
	d.stdoutTerminal = func() bool { return false }
	if set != nil {
		set(&d)
	}
	dep = d
}

// runCmd runs one command in process and returns stdout, stderr and the
// exit code Main would give.
func runCmd(t *testing.T, cmd string, args ...string) (string, string, int) {
	t.Helper()
	var out, errb bytes.Buffer
	for _, c := range tool().Commands {
		if c.Name == cmd {
			err := c.Run(context.Background(), args, &out, &errb)
			code := exitCode(&errb, err) // prints first, as Main does
			return out.String(), errb.String(), code
		}
	}
	t.Fatalf("no command %s", cmd)
	return "", "", 0
}

// exitCode is kit's mapping of a command's error to an exit code; like
// Main it prints an error that isn't printed yet.
func exitCode(stderr io.Writer, err error) int { return exitFor(stderr, err) }

// mustParse parses a command line or fails the test.
func mustParse(t *testing.T, cmd string, args ...string) *parsed {
	t.Helper()
	var errb bytes.Buffer
	p, err := parse(cmd, args, io.Discard, &errb)
	if err != nil {
		t.Fatalf("parse %s %q: %v\n%s", cmd, args, err, errb.String())
	}
	return p
}
