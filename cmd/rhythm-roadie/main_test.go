// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The tests run the binary as a child process: the test binary itself,
// re-executed with ROADIE_TEST_MAIN=1 so TestMain calls main.
//
// In-process tests get their own config and cache folders, so a first-run
// marker or a signing key never lands in the real ones.
func TestMain(m *testing.M) {
	if os.Getenv("ROADIE_TEST_MAIN") == "1" {
		os.Args = append([]string{"rhythm-roadie"}, os.Args[1:]...)
		version = os.Getenv("ROADIE_TEST_VERSION")
		main()
		return
	}
	dir, err := os.MkdirTemp("", "roadie-cmd-test-")
	if err != nil {
		panic(err)
	}
	os.Setenv("RHYTHM_CONFIG_DIR", filepath.Join(dir, "config"))
	os.Setenv("RHYTHM_CACHE_DIR", filepath.Join(dir, "cache"))
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// run runs rhythm-roadie with args offline and returns stdout, stderr and the exit code.
func run(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	return runEnv(t, nil, args...)
}

// runEnv is run with extra environment entries, which win over the
// defaults (os/exec keeps the last value of a repeated name).
func runEnv(t *testing.T, env []string, args ...string) (string, string, int) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, args...) // #nosec G204 -- the test binary itself
	cmd.Env = append(os.Environ(), "ROADIE_TEST_MAIN=1", "RHYTHM_OFFLINE=1", "RHYTHM_INVOKED_AS=", "CI=1",
		"XDG_CONFIG_HOME="+t.TempDir(), "HOME="+t.TempDir(), "APPDATA="+t.TempDir(),
		"RHYTHM_CONFIG_DIR="+t.TempDir(), "RHYTHM_CACHE_DIR="+t.TempDir())
	cmd.Env = append(cmd.Env, env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	var ee *exec.ExitError
	switch {
	case errors.As(err, &ee):
		return stdout.String(), stderr.String(), ee.ExitCode()
	case err != nil:
		t.Fatal(err)
	}
	return stdout.String(), stderr.String(), 0
}

func TestVersionJSON(t *testing.T) {
	out, errOut, code := run(t, "version", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	var v struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil || v.Name != "rhythm-roadie" {
		t.Fatalf("version --json = %q (%v)", out, err)
	}
}

func TestHelpCopy(t *testing.T) {
	out, errOut, code := run(t, "help", "copy")
	if code != 0 || !strings.Contains(out+errOut, "rhythm-roadie copy") {
		t.Fatalf("exit %d\nstdout %s\nstderr %s", code, out, errOut)
	}
}

func TestComplete(t *testing.T) {
	out, _, code := run(t, "__complete", "co")
	if code != 0 || !strings.Contains(out, "copy") || !strings.Contains(out, "config") {
		t.Fatalf("exit %d: %q", code, out)
	}
}

func TestUnknownCommand(t *testing.T) {
	_, errOut, code := run(t, "cpy")
	if code != 2 || !strings.Contains(errOut, "copy") {
		t.Fatalf("exit %d: %q", code, errOut)
	}
}

// No arguments without a terminal prints the top help and exits 0, the
// same text as help (PRD 6.1).
func TestNoArgumentsPrintsHelp(t *testing.T) {
	out, errOut, code := run(t)
	help, _, _ := run(t, "help")
	if code != 0 || errOut != "" || out != help || !strings.HasPrefix(out, "♪ rhythm roadie  dev\n") {
		t.Fatalf("exit %d\nstdout %s\nstderr %s", code, out, errOut)
	}
}

func TestUpdateClear(t *testing.T) {
	out, errOut, code := run(t, "update", "--clear")
	if code != 0 || !strings.Contains(out, "Removed the cached profile pack") {
		t.Fatalf("exit %d\nstdout %s\nstderr %s", code, out, errOut)
	}
}
