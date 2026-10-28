// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build unix

package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/internal/testutil"
)

// spoolFiles counts the files left in the spool folder under cache.
func spoolFiles(t *testing.T, cache string) int {
	t.Helper()
	n := 0
	_ = filepath.WalkDir(cache, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.Contains(p, string(filepath.Separator)+"spool"+string(filepath.Separator)) {
			n++
		}
		return nil
	})
	return n
}

// A dump read from stdin is spooled encrypted; the spool is gone after a
// verify - that passed and after copy --to - whose reader closed the
// pipe early (| head), which stops the run as an interrupt.
func TestStdinSpoolRemoved(t *testing.T) {
	requireLanes(t, "io", "values", "site", "detect", "engine")
	dir := t.TempDir()
	canaries := testutil.Canaries(20261029)
	key := testutil.J("sk_", "live_", "51Pp", "ip3aTq2wN8eLm3VbXq")
	in := []byte(canaryDump(canaries, key))
	in = append(in, bytes.Repeat([]byte("-- padding so the copy outlives the reader\n"), 20000)...)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cache := t.TempDir()
	child := func(args ...string) *exec.Cmd {
		cmd := exec.Command(exe, args...) // #nosec G204 -- the test binary itself
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "ROADIE_TEST_MAIN=1", "RHYTHM_OFFLINE=1", "RHYTHM_INVOKED_AS=", "CI=1",
			"HOME="+t.TempDir(), "XDG_CONFIG_HOME="+t.TempDir(), "RHYTHM_CONFIG_DIR="+t.TempDir(), "RHYTHM_CACHE_DIR="+cache)
		return cmd
	}
	cp := child("copy", "--from", "-", "--to", "-", "--url", "https://staging.canary-shop.example", "--yes", "--no-config")
	cp.Stdin = bytes.NewReader(in)
	var copyErr bytes.Buffer
	cp.Stderr = &copyErr
	out, err := cp.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cp.Start(); err != nil {
		t.Fatal(err)
	}
	head := make([]byte, 10)
	if _, err := io.ReadFull(out, head); err != nil {
		t.Fatal(err)
	}
	_ = out.Close()
	err = cp.Wait()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 130 || !strings.Contains(copyErr.String(), "interrupted") {
		t.Errorf("copy | head: %v\n%s", err, copyErr.String())
	}
	if n := spoolFiles(t, cache); n != 0 {
		t.Errorf("copy | head left %d spool files", n)
	}

	if err := os.WriteFile(filepath.Join(dir, "shop.sql"), in, 0o600); err != nil {
		t.Fatal(err)
	}
	dump, errOut, code := runPiped(t, dir, nil, "copy", "--from", "shop.sql", "--to", "-", "--url", "https://staging.canary-shop.example", "--yes", "--no-config")
	if code != 0 {
		t.Fatalf("copy: exit %d\n%s", code, errOut)
	}
	v := child("verify", "-", "--production", "canary-shop.example", "--no-config", "--plain")
	v.Stdin = strings.NewReader(dump)
	if b, err := v.CombinedOutput(); err != nil {
		t.Fatalf("verify -: %v\n%s", err, b)
	}
	if n := spoolFiles(t, cache); n != 0 {
		t.Errorf("verify - left %d spool files", n)
	}
}
