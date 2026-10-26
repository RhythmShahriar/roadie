// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
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
		s := Soundcheck{Bin: fakeSoundcheck(t, c.code), Base: "http://127.0.0.1:32949", Timeout: 10 * time.Second, ProxyAddr: portSeal}
		if got := s.Health(ctx, dir, paths); got != c.want {
			t.Errorf("exit %d: %q, want %q", c.code, got, c.want)
		}
		b, err := os.ReadFile(filepath.Join(dir, "args")) // #nosec G304 -- the test's own file
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"run http://127.0.0.1:32949/ ", "--no-download", "--proxy http://" + portSeal + " ", "--fail-on error", "--page / --page /shop/", "home: " + filepath.Join(dir, "home"), "no-update: 1"} {
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
	if got := (Soundcheck{Bin: "x", Base: "https://127.0.0.1:32949"}).Health(ctx, t.TempDir(), paths); !strings.HasPrefix(got, "soundcheck: not run (no seal proxy: ") {
		t.Errorf("an https site: %q", got)
	}
	if got := bootPaths([]string{"/", "/wp-admin/", "/wp-cron.php", "/cart/"}); !slices.Equal(got, []string{"/", "/cart/"}) {
		t.Errorf("bootPaths %q", got)
	}
}

// The seal proxy passes the staging site and refuses every other host,
// CONNECT included, naming the hosts but never a path.
func TestSeal(t *testing.T) {
	ln, err := net.Listen("tcp", portSealSite)
	if err != nil {
		t.Fatal(err)
	}
	site := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "staging")
	})}
	go func() { _ = site.Serve(ln) }()
	t.Cleanup(func() { _ = site.Close() })

	s, err := startSeal(portSeal, "http://"+portSealSite)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.note() != "" {
		t.Errorf("a note before any refusal: %q", s.note())
	}
	pu, _ := url.Parse(s.URL())
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(pu)}}
	get := func(u string) (int, string, error) {
		resp, err := client.Get(u)
		if err != nil {
			return 0, "", err
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b), nil
	}
	if code, body, err := get("http://" + portSealSite + "/shop/"); err != nil || code != http.StatusOK || body != "staging" {
		t.Errorf("the site: %d %q %v", code, body, err)
	}
	// .test never resolves: were the proxy to forward it, the request
	// would fail rather than reach anything.
	for _, u := range []string{"http://avatars.example.test/avatar/0123abcd?s=96", "http://staging-wp/wp-includes/x.js"} {
		if code, _, err := get(u); err != nil || code != http.StatusForbidden {
			t.Errorf("%s: %d %v, want 403", u, code, err)
		}
	}
	if _, _, err := get("https://emoji.example.test/twemoji.js"); err == nil {
		t.Error("CONNECT went through")
	}
	note := s.note()
	if note != "; 3 request(s) outside the site refused: avatars.example.test, emoji.example.test, staging-wp" {
		t.Errorf("note %q", note)
	}
	if strings.Contains(note, "0123abcd") {
		t.Error("the note carries a path")
	}

	if _, err := startSeal("127.0.0.1:0", "http://"); err == nil {
		t.Error("a site with no host was accepted")
	}
}
