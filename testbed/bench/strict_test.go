// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/roadie/testbed/manifest"
)

func TestStrictProblems(t *testing.T) {
	kinds := map[string]manifest.Scenario{
		"r01": {ID: "r01"},
		"g01": {ID: "g01", Good: true},
		"g02": {ID: "g02", Good: true, Pending: "later"},
	}
	notBuilt := CaseResult{Name: "a", Pending: "not-built", NotBuilt: true}
	notBuilt.score()
	planned := CaseResult{Name: "b", Pending: "the manifest says so"}
	planned.score()
	for _, c := range []struct {
		name string
		sc   Scored
		want []string
	}{
		{"clean", Scored{
			Scenarios: []ScenarioResult{{ID: "r01"}, {ID: "g01"}},
			Control:   Control{Checks: 5},
		}, nil},
		{"pending in the manifest", Scored{
			Scenarios: []ScenarioResult{{ID: "r01", Cases: []CaseResult{planned}}, {ID: "g02", Pending: "later"}},
		}, nil},
		{"not-built", Scored{
			Scenarios: []ScenarioResult{{ID: "r01", Cases: []CaseResult{notBuilt, notBuilt}}, {ID: "g01"}},
			Control:   Control{Checks: 5},
		}, []string{"2 case(s) stopped at not-built"}},
		{"empty control pass", Scored{
			Scenarios: []ScenarioResult{{ID: "g01", Cases: []CaseResult{notBuilt}}},
		}, []string{"1 case(s) stopped at not-built", "the control pass checked nothing"}},
	} {
		summarize(&c.sc, kinds)
		if got := StrictProblems(c.sc, kinds); !slices.Equal(got, c.want) {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	if notBuilt.Outcome != Pending {
		t.Errorf("a not-built case scores %s, want pending", notBuilt.Outcome)
	}
}

// fakeSoundcheck writes a script that prints a line and exits with code.
func fakeSoundcheck(t *testing.T, code int) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script")
	}
	p := filepath.Join(t.TempDir(), "rhythm-soundcheck")
	script := "#!/bin/sh\necho \"args: $*\" > \"$PWD/args\"\necho \"home: $HOME no-update: $RHYTHM_NO_UPDATE_CHECK\" >> \"$PWD/args\"\necho \"2 issues at error level\"\nexit " + string(rune('0'+code)) + "\n"
	if err := os.WriteFile(p, []byte(script), 0o700); err != nil { // #nosec G306 -- a test script must be executable
		t.Fatal(err)
	}
	return p
}

func TestSoundcheckHealth(t *testing.T) {
	ctx := context.Background()
	paths := []string{"/", "/shop/"}
	for _, c := range []struct {
		code int
		want string
	}{
		{0, "soundcheck: 2 pages healthy"},
		{1, "soundcheck: issues at error level: 2 issues at error level"},
		{3, "soundcheck: not run (exit 3: 2 issues at error level)"},
	} {
		dir := filepath.Join(t.TempDir(), "sc")
		s := Soundcheck{Bin: fakeSoundcheck(t, c.code), Base: "http://127.0.0.1:33139", Timeout: 10 * time.Second}
		if got := s.Health(ctx, dir, paths); got != c.want {
			t.Errorf("exit %d: %q, want %q", c.code, got, c.want)
		}
		b, err := os.ReadFile(filepath.Join(dir, "args")) // #nosec G304 -- the test's own file
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"run http://127.0.0.1:33139/ ", "--no-download", "--fail-on error", "--page / --page /shop/", "home: " + filepath.Join(dir, "home"), "no-update: 1"} {
			if !strings.Contains(string(b), want) {
				t.Errorf("exit %d: %q lacks %q", c.code, b, want)
			}
		}
	}
	if got := (Soundcheck{}).Health(ctx, t.TempDir(), paths); got != "soundcheck: not run (rhythm-soundcheck isn't on PATH)" {
		t.Errorf("no binary: %q", got)
	}
	if got := (Soundcheck{Bin: "x"}).Health(ctx, t.TempDir(), nil); got != "soundcheck: not run (no pages to check)" {
		t.Errorf("no pages: %q", got)
	}
	if got := bootPaths([]string{"/", "/wp-admin/", "/wp-cron.php", "/cart/"}); !slices.Equal(got, []string{"/", "/cart/"}) {
		t.Errorf("bootPaths %q", got)
	}
}
