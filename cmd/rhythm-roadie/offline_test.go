// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/RhythmShahriar/roadie/internal/demo"
	"github.com/RhythmShahriar/roadie/internal/testutil"
)

// offlineCopyAndVerify copies the leaky demo shop from a dump file to a
// dump file, verifies the copy (PASS) and the shop (FAIL), all offline.
// run runs one command line: in process, or the binary under unshare.
func offlineCopyAndVerify(t *testing.T, run func(args ...string) (string, string, int)) {
	t.Helper()
	dir := t.TempDir()
	shop := filepath.Join(dir, "shop.sql")
	f, err := os.Create(shop) // #nosec G304 -- the test's own temp file
	if err != nil {
		t.Fatal(err)
	}
	if err := demo.Shop(f, demo.Options{Seed: demoSeed, Customers: 20, Leaky: true}); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	staging := filepath.Join(dir, "staging.sql")
	prod := demo.Production()
	args := []string{"copy", "--from", shop, "--to", staging, "--url", demoURL, "--yes", "--offline", "--no-config", "--plain",
		"-o", filepath.Join(dir, "copy.json")}
	// copy has no production flag: it reads production from the shop's
	// own siteurl and home.
	if _, errOut, code := run(args...); code != 0 {
		t.Fatalf("copy offline: exit %d\n%s", code, errOut)
	}
	verify := func(target string) int {
		a := []string{"verify", target, "--offline", "--no-config", "--plain", "-o", filepath.Join(dir, filepath.Base(target)+".json")}
		for _, p := range prod {
			a = append(a, "--production", p)
		}
		_, _, code := run(a...)
		return code
	}
	if code := verify(staging); code != 0 {
		t.Errorf("verify of the copy offline: exit %d, want 0", code)
	}
	if code := verify(shop); code != 1 {
		t.Errorf("verify of the shop offline: exit %d, want 1", code)
	}
}

// TestNoNetworkInProcess is TestNoNetwork for every CI run: the same
// copy and verify in process, with http's transport and the resolver
// failing the test on any use.
func TestNoNetworkInProcess(t *testing.T) {
	requireLanes(t, "io", "values", "site", "detect", "engine", "testbed")
	old := dep
	t.Cleanup(func() { dep = old })
	dep = realDeps()
	t.Chdir(t.TempDir())
	testutil.NoNetwork(t)
	offlineCopyAndVerify(t, func(args ...string) (string, string, int) { return runCmd(t, args[0], args[1:]...) })
}
