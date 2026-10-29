// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/testbed/manifest"
	"github.com/RhythmShahriar/roadie/testbed/seed"
)

// Testbed scenario r15 as scenarios.yaml runs it, to a dump target: the
// copy with the fixture's config doesn't stop on uncovered columns and an
// exact verify at --fail-on medium passes, without --allow-uncovered.
// Without the config the copy stops (the negative).
func TestTestbedR15Runs(t *testing.T) {
	root := repoRoot(t)
	fixtures := filepath.Join(root, "testbed", "fixtures")
	binaryEnv(t)
	rt := t.TempDir()
	if _, err := seed.Run(context.Background(), seed.Options{Fixtures: fixtures, Out: rt, Seed: 1, Only: []string{"r15"}}); err != nil {
		t.Fatal(err)
	}
	in := filepath.Join(rt, "fixtures", "r15")
	db := filepath.Join(in, "db.sql")
	out, errOut, code := runCmd(t, "copy", "--from", db, "--to", "plain.sql", "--stack", "laravel", "--url", "http://laravel", "--yes", "--offline", "--plain")
	if code != 1 || !strings.Contains(out+errOut, "uncovered") {
		t.Errorf("copy without the config: exit %d\n%s%s", code, out, errOut)
	}
	// The scenario's own arguments, with a dump in place of the site.
	f, err := manifest.Load(filepath.Join(root, "testbed", "scenarios.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(f.Scenarios, func(s manifest.Scenario) bool { return s.ID == "r15" })
	if i < 0 {
		t.Fatal("no r15 in scenarios.yaml")
	}
	sc := f.Scenarios[i]
	if sc.Expect.ExitCode == nil || sc.Expect.VerifyExit == nil {
		t.Fatal("r15 expects no exit codes")
	}
	vars := map[string]string{"in": in, "site": "out.sql"}
	args, err := manifest.ExpandAll(sc.Command, vars)
	if err != nil {
		t.Fatal(err)
	}
	if j := slices.Index(args, "--confirm-target"); j >= 0 {
		args = slices.Delete(args, j, j+2) // a dump target has no name to confirm
	}
	out, errOut, code = runCmd(t, args[0], append(args[1:], "--offline")...)
	if code != *sc.Expect.ExitCode {
		t.Fatalf("copy %v: exit %d, scenarios.yaml expects %d\n%s%s", args, code, *sc.Expect.ExitCode, out, errOut)
	}
	if args, err = manifest.ExpandAll(sc.Verify, vars); err != nil {
		t.Fatal(err)
	}
	out, errOut, code = runCmd(t, "verify", append(args, "--offline")...)
	if data := mustData(t, out); code != *sc.Expect.VerifyExit || findingIDs(data)["uncovered-column"] {
		t.Errorf("verify %v: exit %d %v\n%s", args, code, findingIDs(data), errOut)
	}
}

// repoRoot is the module root, two folders up from this package.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return dir
}
