// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package remotetest builds and reads the ssh shim the site tests put
// first on PATH, so no test ever reaches a real host. Only tests import it.
package remotetest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
)

// Install builds the shim as "ssh" in a new folder, puts the folder
// first on PATH and returns it. Call it from TestMain; cleanup removes
// the folder.
func Install() (dir string, cleanup func(), err error) {
	dir, err = os.MkdirTemp("", "roadie-sshshim-")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { _ = os.RemoveAll(dir) }
	_, self, _, _ := runtime.Caller(0)
	src := filepath.Join(filepath.Dir(self), "..", "testdata", "sshshim")
	name := "ssh"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", filepath.Join(dir, name), ".") // #nosec G204 -- builds the test shim
	cmd.Dir = src
	cmd.Env = append(os.Environ(), "GOWORK=off", "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("build the ssh shim: %w\n%s", err, out)
	}
	if err := os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH")); err != nil {
		cleanup()
		return "", nil, err
	}
	return dir, cleanup, nil
}

// Shim is one test's view of the shim: its log folder and canned answer.
type Shim struct {
	t   testing.TB
	Dir string
}

// New gives the test its own log folder and clears any canned answer.
func New(t testing.TB) *Shim {
	t.Helper()
	s := &Shim{t: t, Dir: t.TempDir()}
	t.Setenv("SHIM_DIR", s.Dir)
	t.Setenv("SHIM_STDOUT", "")
	t.Setenv("SHIM_STDERR", "")
	t.Setenv("SHIM_EXIT", "0")
	t.Setenv("SHIM_CANARY", "")
	t.Setenv("SHIM_MATCH", "")
	return s
}

// Reply sets what the next calls print and exit with.
func (s *Shim) Reply(stdout []byte, stderr string, exit int) {
	s.t.Helper()
	p := filepath.Join(s.Dir, "stdout")
	if err := os.WriteFile(p, stdout, 0o600); err != nil {
		s.t.Fatal(err)
	}
	s.t.Setenv("SHIM_STDOUT", p)
	s.t.Setenv("SHIM_STDERR", stderr)
	s.t.Setenv("SHIM_EXIT", strconv.Itoa(exit))
}

// ReplyFor sets what calls whose remote command starts with prefix
// print and exit with; every other call keeps the Reply answer.
func (s *Shim) ReplyFor(prefix string, stdout []byte, stderr string, exit int) {
	s.t.Helper()
	p := filepath.Join(s.Dir, "match-stdout")
	if err := os.WriteFile(p, stdout, 0o600); err != nil {
		s.t.Fatal(err)
	}
	s.t.Setenv("SHIM_MATCH", prefix)
	s.t.Setenv("SHIM_MATCH_STDOUT", p)
	s.t.Setenv("SHIM_MATCH_STDERR", stderr)
	s.t.Setenv("SHIM_MATCH_EXIT", strconv.Itoa(exit))
}

// Canary makes the shim fail with exit 99 when pw is in its argv.
func (s *Shim) Canary(pw string) { s.t.Setenv("SHIM_CANARY", pw) }

// Calls returns the argv of every call so far (without "ssh").
func (s *Shim) Calls() [][]string {
	s.t.Helper()
	f, err := os.Open(filepath.Join(s.Dir, "calls.jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		s.t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var out [][]string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		var argv []string
		if err := json.Unmarshal(sc.Bytes(), &argv); err != nil {
			s.t.Fatal(err)
		}
		out = append(out, argv)
	}
	return out
}

// Stdin returns what call n (0-based) read on stdin.
func (s *Shim) Stdin(n int) []byte {
	s.t.Helper()
	b, err := os.ReadFile(filepath.Join(s.Dir, "stdin-"+strconv.Itoa(n)))
	if err != nil {
		s.t.Fatal(err)
	}
	return b
}
