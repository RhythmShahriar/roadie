// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Soundcheck runs 'rhythm-soundcheck run' health on the staging site
// after the boot check, when the binary is on PATH. Its verdict is a run
// note, not a scored gate: the staging site's own URL (http://staging-wp)
// only resolves inside the sealed network, so a browser on this machine
// can't load the site's assets and would report errors the copy didn't
// cause. The boot check stays the scored gate.
type Soundcheck struct {
	Bin     string        // rhythm-soundcheck; empty skips the check
	Base    string        // http://127.0.0.1:18921
	Timeout time.Duration // default 3 minutes
}

// findSoundcheck returns rhythm-soundcheck on PATH, or "" when there is
// none (the check is then left out with a note).
func findSoundcheck() string {
	p, err := exec.LookPath("rhythm-soundcheck")
	if err != nil {
		return ""
	}
	return p
}

// Health checks paths once in dir with an isolated home and returns the
// note to record. It never downloads a browser (--no-download) and
// fails on issues at error level (--fail-on error).
func (s Soundcheck) Health(ctx context.Context, dir string, paths []string) string {
	if s.Bin == "" {
		return "soundcheck: not run (rhythm-soundcheck isn't on PATH)"
	}
	if len(paths) == 0 {
		return "soundcheck: not run (no pages to check)"
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "soundcheck: not run (" + err.Error() + ")"
	}
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = 3 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	args := []string{"run", s.Base + "/", "--quick", "--modules", "health", "--no-download",
		"--fail-on", "error", "--plain", "-q", "--limit", fmt.Sprint(len(paths))}
	for _, p := range paths {
		args = append(args, "--page", p)
	}
	cmd := exec.CommandContext(ctx, s.Bin, args...) // #nosec G204 -- rhythm-soundcheck from PATH, fixed flags
	cmd.Dir = dir
	home := filepath.Join(dir, "home")
	cmd.Env = append(os.Environ(), "HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"XDG_CACHE_HOME="+filepath.Join(home, ".cache"), "RHYTHM_CONFIG_DIR="+filepath.Join(home, "rhythm"),
		"RHYTHM_CACHE_DIR="+filepath.Join(home, "cache"), "RHYTHM_NO_UPDATE_CHECK=1")
	out, err := cmd.CombinedOutput()
	_ = os.WriteFile(filepath.Join(dir, "soundcheck.log"), out, 0o600)
	var ee *exec.ExitError
	switch {
	case err == nil:
		return fmt.Sprintf("soundcheck: %d pages healthy", len(paths))
	case ctx.Err() != nil:
		return "soundcheck: not run (timed out after " + timeout.String() + ")"
	case errors.As(err, &ee) && ee.ExitCode() == 1:
		return "soundcheck: issues at error level: " + orDash(string(lastLine(out)))
	case errors.As(err, &ee):
		return fmt.Sprintf("soundcheck: not run (exit %d: %s)", ee.ExitCode(), orDash(string(lastLine(out))))
	}
	return "soundcheck: not run (" + err.Error() + ")"
}

// bootPaths are the staging pages the boot check and Soundcheck load:
// every page the traffic generator knows but the admin and cron ones.
func bootPaths(paths []string) []string {
	var out []string
	for _, p := range paths {
		if !strings.HasPrefix(p, "/wp-admin") && !strings.HasPrefix(p, "/wp-cron") {
			out = append(out, p)
		}
	}
	return out
}
