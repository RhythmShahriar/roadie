// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/internal/testutil"
)

// runPiped runs the binary as a child process in dir with stdin and
// returns stdout, stderr and the exit code.
func runPiped(t *testing.T, dir string, stdin []byte, args ...string) (string, string, int) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, args...) // #nosec G204 -- the test binary itself
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "ROADIE_TEST_MAIN=1", "RHYTHM_OFFLINE=1", "RHYTHM_INVOKED_AS=", "CI=1",
		"XDG_CONFIG_HOME="+t.TempDir(), "HOME="+t.TempDir(), "APPDATA="+t.TempDir(),
		"RHYTHM_CONFIG_DIR="+t.TempDir(), "RHYTHM_CACHE_DIR="+t.TempDir())
	var stdout, stderr bytes.Buffer
	cmd.Stdin, cmd.Stdout, cmd.Stderr = bytes.NewReader(stdin), &stdout, &stderr
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

// copy --to - writes only the dump to stdout (PRD 6.2): the screen goes
// to stderr, so the output loads in verify -. A report format on stdout
// is refused, since it would land in the dump.
func TestCopyToStdoutPipes(t *testing.T) {
	requireLanes(t, "io", "values", "site", "detect", "engine")
	dir := t.TempDir()
	canaries := testutil.Canaries(20261027)
	key := testutil.J("sk_", "live_", "51Pp", "ip3aTq2wN8eLm3VbXq")
	if err := os.WriteFile(filepath.Join(dir, "shop.sql"), []byte(canaryDump(canaries, key)), 0o600); err != nil {
		t.Fatal(err)
	}
	base := []string{"copy", "--from", "shop.sql", "--to", "-", "--url", "https://staging.canary-shop.example", "--yes", "--no-config"}
	for _, extra := range [][]string{nil, {"-q"}, {"--plain"}} {
		dump, errOut, code := runPiped(t, dir, nil, append(base, extra...)...)
		if code != 0 {
			t.Fatalf("copy %v: exit %d\n%s", extra, code, errOut)
		}
		if strings.Contains(dump, "RHYTHM") || strings.Contains(dump, "DONE") || strings.Contains(dump, "report") {
			t.Errorf("copy %v: the screen is in the dump:\n%.400s", extra, dump)
		}
		if !strings.Contains(errOut, "written to stdout; not checked") || strings.Contains(errOut, "--no-verify") {
			t.Errorf("copy %v: stderr %q", extra, errOut)
		}
		out, verr, vcode := runPiped(t, dir, []byte(dump), "verify", "-", "--production", "canary-shop.example", "--no-config", "--plain")
		if vcode != 0 || strings.Contains(out+verr, "dump-refused") {
			t.Errorf("copy %v | verify -: exit %d\n%s\n%s", extra, vcode, out, verr)
		}
	}
	for _, f := range []string{"--json", "--md", "--html"} {
		out, errOut, code := runPiped(t, dir, nil, append(base, f)...)
		if code != 2 || out != "" || strings.Count(errOut, "carries the dump") != 1 {
			t.Errorf("copy --to - %s: exit %d stdout %.80q stderr %q", f, code, out, errOut)
		}
	}
}

// Usage errors found while copy, prepare and plan build their job exit 2
// with the message once.
func TestRunUsageErrorsExit2(t *testing.T) {
	for _, c := range []struct {
		cmd  string
		args []string
		msg  string
	}{
		{"copy", nil, "copy needs --from and --to"},
		{"plan", nil, "plan needs --from"},
		{"prepare", []string{"a.sql", "b.sql"}, "one staging at a time"},
		{"copy", []string{"stray", "--from", "a.sql", "--to", "b.sql"}, `unexpected argument "stray"`},
	} {
		testDeps(t, nil)
		_, errOut, code := runCmd(t, c.cmd, append(c.args, "--no-config")...)
		if code != 2 || strings.Count(errOut, c.msg) != 1 {
			t.Errorf("%s %q: exit %d, stderr %q", c.cmd, c.args, code, errOut)
		}
	}
}
