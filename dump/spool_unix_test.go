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
