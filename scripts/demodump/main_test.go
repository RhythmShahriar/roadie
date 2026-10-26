// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/internal/demo"
)

// needsDemo skips while the testbed lane's demo.Shop is a stub.
func needsDemo(t *testing.T) {
	t.Helper()
	if errors.Is(demo.Shop(io.Discard, demo.Options{Seed: 1, Customers: 1}), core.ErrNotBuilt) {
		t.Skip("needs the testbed lane")
	}
}

func TestWritesTheDump(t *testing.T) {
	needsDemo(t)
	for _, leaky := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "shop.sql")
		args := []string{"-o", path, "-customers", "3"}
		if leaky {
			args = append(args, "-leaky")
		}
		var stderr bytes.Buffer
		if code := run(args, io.Discard, &stderr); code != 0 {
			t.Fatalf("leaky=%v: exit %d: %s", leaky, code, stderr.String())
		}
		b, err := os.ReadFile(path)
		if err != nil || !bytes.Contains(b, []byte("CREATE TABLE")) {
			t.Fatalf("leaky=%v: no dump in %s (%v)", leaky, path, err)
		}
		if fi, err := os.Stat(path); err == nil && fi.Mode().Perm()&0o077 != 0 && runtime.GOOS != "windows" {
			t.Errorf("leaky=%v: mode %v, want 0600", leaky, fi.Mode().Perm())
		}
	}
}

func TestProductionHosts(t *testing.T) {
	var stdout bytes.Buffer
	if code := run([]string{"-production"}, &stdout, io.Discard); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if got, want := strings.TrimSpace(stdout.String()), strings.Join(demo.Production(), ","); got != want {
		t.Errorf("printed %q, want %q", got, want)
	}
}

// A wrong command line is exit 2 and writes nothing.
func TestUsage(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{
		nil,
		{"-o", filepath.Join(dir, "a.sql"), "extra"},
		{"-o", filepath.Join(dir, "b.sql"), "-customers", "0"},
		{"-nope"},
	} {
		var stderr bytes.Buffer
		if code := run(args, io.Discard, &stderr); code != 2 || stderr.Len() == 0 {
			t.Errorf("%q: exit %d, stderr %q; want 2 with a message", args, code, stderr.String())
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("a usage error wrote %d files", len(entries))
	}
}

// A dump that can't be made leaves no half-written file behind.
func TestFailureRemovesTheFile(t *testing.T) {
	shop = func(w io.Writer, _ demo.Options) error {
		_, _ = io.WriteString(w, "CREATE TABLE half")
		return errors.New("stopped half way")
	}
	t.Cleanup(func() { shop = demo.Shop })
	path := filepath.Join(t.TempDir(), "shop.sql")
	if code := run([]string{"-o", path}, io.Discard, io.Discard); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("%s is still there: %v", path, err)
	}
}
