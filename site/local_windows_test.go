// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build windows

package site

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestWindowsRenameRetry holds wp-config.php open the way an editor or a
// virus scanner does: the write waits for it, then gives a clear error.
func TestWindowsRenameRetry(t *testing.T) {
	ctx := context.Background()
	dir := writeTree(t, map[string]string{"wp-config.php": "old"})
	s := openLocalT(t, dir, Options{})
	f, err := os.Open(filepath.Join(dir, "wp-config.php"))
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(500 * time.Millisecond)
		_ = f.Close()
	}()
	if err := s.WriteFile(ctx, "wp-config.php", []byte("new"), 0o644); err != nil {
		t.Fatalf("write after the file was closed: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "wp-config.php")); string(b) != "new" {
		t.Fatalf("content %q", b)
	}

	held, err := os.Open(filepath.Join(dir, "wp-config.php"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Close() }()
	start := time.Now()
	err = s.WriteFile(ctx, "wp-config.php", []byte("newer"), 0o644)
	if err == nil || !strings.Contains(err.Error(), "another program holds it open") {
		t.Fatalf("%v, want the held-open error", err)
	}
	if d := time.Since(start); d < 1500*time.Millisecond {
		t.Fatalf("gave up after %v, want about 2 s", d)
	}
}
