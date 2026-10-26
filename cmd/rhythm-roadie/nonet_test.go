// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build nonet

package main

import "testing"

// TestNoNetwork runs under make no-network, in a namespace with no
// network at all (security.md L1): the binary starts and answers, then
// copy and verify run on dump files. A raw dial anywhere fails there
// even where testutil.NoNetwork can't hook it.
func TestNoNetwork(t *testing.T) {
	if _, errOut, code := run(t, "version", "--json"); code != 0 {
		t.Fatalf("version --json without a network: exit %d: %s", code, errOut)
	}
	requireLanes(t, "io", "values", "site", "detect", "engine", "testbed")
	offlineCopyAndVerify(t, func(args ...string) (string, string, int) { return run(t, args...) })
}
