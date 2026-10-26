// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build linux || darwin

package dump

import (
	"os"
	"path/filepath"
	"testing"
)

// The spool folder is 0700 and the file 0600 whatever the umask.
func TestSpoolMode(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spool")
	s, err := NewSpool(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Remove()
	if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("folder mode %v, %v", fi.Mode(), err)
	}
	if fi, err := os.Stat(spoolFile(t, dir)); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %v, %v", fi.Mode(), err)
	}
}

// An existing spool folder that others can read is set back to 0700; a
// folder with other files in it keeps its mode.
func TestSpoolTightensFolder(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spool")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "0011223344556677.spool"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := NewSpool(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Remove()
	if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("folder mode %v, %v", fi.Mode(), err)
	}

	shared := t.TempDir()
	if err := os.Chmod(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shared, "notes.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s2, err := NewSpool(shared)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Remove()
	if fi, err := os.Stat(shared); err != nil || fi.Mode().Perm() != 0o755 {
		t.Fatalf("shared folder mode %v, %v", fi.Mode(), err)
	}
}
