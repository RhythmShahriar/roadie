// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build nonet

package main

import "testing"

// TestNoNetwork runs under make no-network, in a namespace with no
// network at all. Until copy and verify are built it checks the binary
// starts and answers without one; the cli lane extends it to copy and
// verify on dump files.
func TestNoNetwork(t *testing.T) {
	if _, errOut, code := run(t, "version", "--json"); code != 0 {
		t.Fatalf("version --json without a network: exit %d: %s", code, errOut)
	}
}
