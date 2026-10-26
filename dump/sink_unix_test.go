// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build linux || darwin

package dump

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// The temp file and the committed dump are 0600 whatever the umask.
func TestFileSinkMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.sql")
	s, err := NewFileSink(path, WriterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	tmp := tempFiles(t, dir)
	if fi, err := os.Stat(tmp[0]); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("temp file mode %v, %v", fi.Mode(), err)
	}
	if err := s.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("dump mode %v, %v", fi.Mode(), err)
	}
}

// The real free-space call answers on this platform.
func TestDiskFree(t *testing.T) {
	free, ok := diskFree(t.TempDir())
	if !ok || free <= 0 {
		t.Fatalf("diskFree = %d, %v", free, ok)
	}
	if _, ok := diskFree(filepath.Join(t.TempDir(), "missing")); ok {
		t.Error("a missing folder has free space")
	}
}
