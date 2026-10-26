// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/charmbracelet/huh"

	"github.com/RhythmShahriar/rhythm/kit/cli"

	"github.com/RhythmShahriar/roadie/core"
)

// scriptAsker answers the picker's questions from a script and records
// what was asked.
type scriptAsker struct {
	answers []string
	asked   []string
	err     error // returned once the script runs out
}

func (s *scriptAsker) next(title string) (string, error) {
	s.asked = append(s.asked, title)
	if len(s.answers) == 0 {
		if s.err != nil {
			return "", s.err
		}
		return "", errors.New("script ran out at " + title)
	}
	a := s.answers[0]
	s.answers = s.answers[1:]
	return a, nil
}

func (s *scriptAsker) choose(title string, options []choice) (string, error) {
	a, err := s.next(title)
	if err == nil && !slices.ContainsFunc(options, func(c choice) bool { return c.value == a }) {
		return "", fmt.Errorf("%q isn't an option of %s", a, title)
	}
	return a, err
}

func (s *scriptAsker) input(title, placeholder string, _ bool) (string, error) {
	return s.next(title + " (" + placeholder + ")")
}

// recordRuns replaces pickRun for one test; codes are the exit codes of
// the runs in order (0 after the list ends).
func recordRuns(t *testing.T, codes ...int) *[][]string {
	t.Helper()
	var runs [][]string
	old := pickRun
	t.Cleanup(func() { pickRun = old })
	pickRun = func(_ context.Context, args []string, _, _ io.Writer) int {
		runs = append(runs, args)
		if len(runs) <= len(codes) {
			return codes[len(runs)-1]
		}
		return 0
	}
	return &runs
}

func TestPickerChoicesArePRDs(t *testing.T) {
	var labels []string
	for _, c := range pickerChoices {
		labels = append(labels, c.label)
	}
	want := []string{
		"Make a staging copy from production (copy)",
		"Make an existing staging site safe (prepare)",
		"Check a staging copy (verify)",
		"See what a copy would change (plan)",
		"Try it on a built-in shop (demo)",
		"Write a config file to edit (config init)",
	}
	if !slices.Equal(labels, want) {
		t.Errorf("picker:\n%q\nwant\n%q", labels, want)
	}
}

func TestPickerPaths(t *testing.T) {
	for _, c := range []struct {
		answers []string
		want    []string
	}{
		{[]string{"verify", "/var/www/staging"}, []string{"verify", "/var/www/staging"}},
		{[]string{"prepare", "@staging"}, []string{"prepare", "@staging"}},
		{[]string{"plan", "prod.sql.gz", ""}, []string{"plan", "--from", "prod.sql.gz"}},
		{[]string{"plan", "prod.sql.gz", "/var/www/staging"}, []string{"plan", "--from", "prod.sql.gz", "--to", "/var/www/staging"}},
		{[]string{"demo"}, []string{"demo"}},
		{[]string{"config init", "laravel"}, []string{"config", "init", "--stack", "laravel"}},
	} {
		runs := recordRuns(t)
		a := &scriptAsker{answers: c.answers}
		code := pick(context.Background(), a, newLineReader(strings.NewReader("")), io.Discard, io.Discard)
		if code != 0 || len(*runs) != 1 || !slices.Equal((*runs)[0], c.want) {
			t.Errorf("%q: exit %d, ran %q, want %q", c.answers, code, *runs, c.want)
		}
	}
}

func TestPickerCopyFlow(t *testing.T) {
	t.Chdir(t.TempDir())
	runs := recordRuns(t)
	a := &scriptAsker{answers: []string{"copy", "ssh://deploy@shop.com/var/www/shop", "/var/www/staging", "https://staging.shop.test"}}
	var stderr bytes.Buffer
	code := pick(context.Background(), a, newLineReader(strings.NewReader("y\n\n")), io.Discard, &stderr)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	wantAsked := []string{
		"What should Roadie do?",
		"Where is production? (ssh://deploy@shop.com/var/www/shop or prod.sql.gz)",
		"Where should the copy go? (" + targetPlaceholder + ")",
		"The copy's URL? (https://staging.shop.test)",
	}
	if !slices.Equal(a.asked, wantAsked) {
		t.Errorf("asked %q", a.asked)
	}
	where := []string{"--from", "ssh://deploy@shop.com/var/www/shop", "--to", "/var/www/staging", "--url", "https://staging.shop.test"}
	if len(*runs) != 2 || !slices.Equal((*runs)[0], append([]string{"plan"}, where...)) ||
		!slices.Equal((*runs)[1], append(append([]string{"copy"}, where...), "--yes")) {
		t.Errorf("ran %q", *runs)
	}
	if got := stderr.String(); !strings.Contains(got, "Copy now? [y/N] ") || !strings.Contains(got, "Save these answers to .roadie/config.yaml? [Y/n] ") {
		t.Errorf("questions: %q", got)
	}
	b, err := os.ReadFile(filepath.Join(".roadie", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	want := "source: \"ssh://deploy@shop.com/var/www/shop\"\ntarget: \"/var/www/staging\"\nurl: \"https://staging.shop.test\"\n"
	if !strings.HasPrefix(string(b), want) {
		t.Errorf("config:\n%s", b)
	}

	// A second run: the config exists, so it isn't offered again.
	runs = recordRuns(t)
	stderr.Reset()
	a = &scriptAsker{answers: []string{"copy", "prod.sql", "out.sql", "https://s.test"}}
	pick(context.Background(), a, newLineReader(strings.NewReader("yes\n")), io.Discard, &stderr)
	if strings.Contains(stderr.String(), "Save these answers") || len(*runs) != 2 {
		t.Errorf("second run asked to save or ran %q: %q", *runs, stderr.String())
	}
}

func TestPickerCopyDeclined(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, input := range []string{"\n", "n\n", ""} { // empty line, no and EOF all mean no
		runs := recordRuns(t)
		a := &scriptAsker{answers: []string{"copy", "prod.sql", "out.sql", "https://s.test"}}
		if code := pick(context.Background(), a, newLineReader(strings.NewReader(input)), io.Discard, io.Discard); code != 0 || len(*runs) != 1 {
			t.Errorf("input %q: exit %d, ran %q", input, code, *runs)
		}
	}
	// A plan that would stop never gets to the question.
	runs := recordRuns(t, 1)
	var stderr bytes.Buffer
	a := &scriptAsker{answers: []string{"copy", "prod.sql", "out.sql", "https://s.test"}}
	if code := pick(context.Background(), a, newLineReader(strings.NewReader("y\n")), io.Discard, &stderr); code != 1 || len(*runs) != 1 ||
		strings.Contains(stderr.String(), "Copy now?") {
		t.Errorf("stopping plan: exit %d, ran %q, %q", code, *runs, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(".roadie", "config.yaml")); err == nil {
		t.Error("a declined copy saved the answers")
	}
}

func TestPickerAborted(t *testing.T) {
	recordRuns(t)
	for _, err := range []error{huh.ErrUserAborted, context.Canceled} {
		a := &scriptAsker{err: err}
		if code := pick(context.Background(), a, newLineReader(strings.NewReader("")), io.Discard, io.Discard); code != cli.ExitInterrupted {
			t.Errorf("%v: exit %d, want 130", err, code)
		}
	}
	a := &scriptAsker{answers: []string{"copy"}, err: errors.New("no terminal")}
	var stderr bytes.Buffer
	if code := pick(context.Background(), a, newLineReader(strings.NewReader("")), io.Discard, &stderr); code != cli.ExitUnavailable ||
		!strings.Contains(stderr.String(), "no terminal") {
		t.Errorf("failed question: exit %d %q", code, stderr.String())
	}
}

func TestFillTemplate(t *testing.T) {
	tmpl := []byte("# Roadie settings.\n\nsource: \"\"                                   # ssh://deploy@shop.com/var/www/shop, prod.sql.gz\n" +
		"target: \"\"                                   # /var/www/staging\nurl: \"\"                                      # the copy's URL\nstack: auto\n")
	got := string(fillTemplate(tmpl, map[string]string{"source": "prod.sql.gz", "target": "/var/www/staging", "url": "https://staging.shop.test"}))
	want := "# Roadie settings.\n\nsource: \"prod.sql.gz\"                        # ssh://deploy@shop.com/var/www/shop, prod.sql.gz\n" +
		"target: \"/var/www/staging\"                   # /var/www/staging\nurl: \"https://staging.shop.test\"             # the copy's URL\nstack: auto\n"
	if got != want {
		t.Errorf("fillTemplate:\n%s\nwant\n%s", got, want)
	}
	// A value with a quote or a # stays one YAML string.
	if got := string(fillTemplate(nil, map[string]string{"source": `a"b#c`, "target": "t", "url": "u"})); !strings.HasPrefix(got, `source: "a\"b#c"`) {
		t.Errorf("quoting: %s", got)
	}
}

func TestConfirm(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		input, expect string
		want          bool
	}{
		{"staging.shop.test\n", "staging.shop.test", true},
		{"STAGING.Shop.Test\n", "staging.shop.test", true},
		{"  staging.shop.test  \n", "staging.shop.test", true},
		{"shop.com\n", "staging.shop.test", false},
		{"y\n", "staging.shop.test", false},
		{"", "staging.shop.test", false}, // EOF
		{"y\n", "", true},
		{"YES\n", "", true},
		{"\n", "", false},
		{"", "", false},
	} {
		var out bytes.Buffer
		confirm := confirmer(ctx, newLineReader(strings.NewReader(c.input)), &out, "prepare", "/var/www/staging")
		got, err := confirm("", c.expect)
		if err != nil || got != c.want {
			t.Errorf("input %q expect %q: %v %v", c.input, c.expect, got, err)
		}
		want := prepareQuestion(c.expect) + " "
		if c.expect == "" {
			want = "Replace the database of /var/www/staging? [y/N] "
		}
		if !strings.HasPrefix(out.String(), want) {
			t.Errorf("asked %q, want %q", out.String(), want)
		}
	}
	if got := prepareQuestion("staging.shop.test"); got != "prepare rewrites this site's database while it runs. Visitors see a maintenance page for about a minute. Type the site's host to go on (staging.shop.test):" {
		t.Errorf("PRD 16.1 question: %q", got)
	}
	// The engine's own question is printed as given.
	var out bytes.Buffer
	confirmer(ctx, newLineReader(strings.NewReader("y\n")), &out, "copy", "x")("Go on? [y/N]", "")
	if out.String() != "Go on? [y/N] " {
		t.Errorf("own question: %q", out.String())
	}
	// Ctrl-C at the question answers no at once.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	pr, pw := io.Pipe()
	defer pw.Close()
	if ok, _ := confirmer(cctx, newLineReader(pr), io.Discard, "prepare", "x")("", "x"); ok {
		t.Error("a canceled question said yes")
	}
}

func TestFirstRunNote(t *testing.T) {
	t.Setenv("RHYTHM_CONFIG_DIR", t.TempDir())
	testDeps(t, nil)
	_, errOut, _ := runCmd(t, "explain", "uncovered-column", "--json")
	if strings.Contains(errOut, "Note: use Roadie") {
		t.Errorf("first-run note with --json: %q", errOut)
	}
	_, errOut, _ = runCmd(t, "explain", "uncovered-column")
	if !strings.HasPrefix(errOut, firstRunNote) {
		t.Errorf("first run: %q", errOut)
	}
	_, errOut, _ = runCmd(t, "explain", "uncovered-column")
	if strings.Contains(errOut, "Note: use Roadie") {
		t.Errorf("second run showed the note again: %q", errOut)
	}
	if !strings.Contains(firstRunNote, "Pseudonymised data is still\npersonal data") {
		t.Error("the note lost the PRD 6.11 wording")
	}
}

// fakeSigner is a signer with a fixed key ID.
type fakeSigner struct{ id string }

func (f fakeSigner) Sign(*core.Envelope) error { return nil }
func (f fakeSigner) KeyID() string             { return f.id }

func TestSigningKeyLine(t *testing.T) {
	created := true
	testDeps(t, func(d *deps) {
		d.loadSigner = func(string, func(string) (string, bool)) (signer, bool, error) {
			return fakeSigner{"roadie:7Q3kXc2abcdefghi"}, created, nil
		}
	})
	var stderr bytes.Buffer
	if _, err := loadReportSigner("", &stderr); err != nil {
		t.Fatal(err)
	}
	dir := os.Getenv("RHYTHM_CONFIG_DIR")
	want := "Created a signing key for Roadie reports: roadie:7Q3kXc2… (keep " + filepath.Join(dir, "roadie", "signing.key") +
		" private; set ROADIE_SIGNING_KEY in CI to sign with one identity).\n"
	if stderr.String() != want {
		t.Errorf("got  %q\nwant %q", stderr.String(), want)
	}
	created = false
	stderr.Reset()
	if _, err := loadReportSigner("", &stderr); err != nil || stderr.Len() != 0 {
		t.Errorf("an existing key printed %q (%v)", stderr.String(), err)
	}
	dep.loadSigner = func(string, func(string) (string, bool)) (signer, bool, error) {
		return nil, false, errors.New("bad key")
	}
	if _, err := loadReportSigner("x", &stderr); err == nil {
		t.Error("a bad key should fail")
	}
}

func TestShortKeyID(t *testing.T) {
	for in, want := range map[string]string{"roadie:7Q3kXc2abcdefghi": "roadie:7Q3kXc2…", "roadie:abc": "roadie:abc", "other": "other"} {
		if got := shortKeyID(in); got != want {
			t.Errorf("shortKeyID(%q) = %q", in, got)
		}
	}
}

// The update check runs against a release fixture on 127.0.0.1: the run
// never asks to update with --json or without a terminal; with CI set
// it doesn't even check.
func TestUpdatePrompt(t *testing.T) {
	var hits atomic.Int32
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"tag_name":"v9.9.9","draft":false,"prerelease":false,"published_at":"2026-01-01T00:00:00Z","assets":[]}`)
	})}
	var ln net.Listener
	var err error
	for port := 33100; port <= 33119; port++ {
		if ln, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port)); err == nil {
			break
		}
	}
	if ln == nil {
		t.Skipf("no free port in 33100-33119: %v", err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })

	cfg := t.TempDir()
	env := []string{"RHYTHM_OFFLINE=", "CI=", "ROADIE_TEST_VERSION=1.0.0", "RHYTHM_GITHUB_API=http://" + ln.Addr().String(),
		"RHYTHM_CONFIG_DIR=" + cfg, "RHYTHM_UPDATE_PROMPT=ask"}
	// The first run asks the fixture and keeps the answer for a day, so
	// the runs below read it at once.
	runEnv(t, env, "explain", "uncovered-column")
	if hits.Load() == 0 {
		t.Fatal("the release check didn't reach the fixture")
	}
	out, errOut, _ := runEnv(t, env, "explain", "uncovered-column", "--json")
	if strings.Contains(errOut, "Update now?") || strings.Contains(out, "9.9.9") {
		t.Errorf("--json: stdout %q stderr %q", out, errOut)
	}
	_, errOut, _ = runEnv(t, env, "explain", "uncovered-column")
	if strings.Contains(errOut, "Update now?") {
		t.Errorf("asked without a terminal: %q", errOut)
	}
	before := hits.Load()
	_, errOut, _ = runEnv(t, append(env, "CI=1", "RHYTHM_CONFIG_DIR="+t.TempDir()), "explain", "uncovered-column")
	if strings.Contains(errOut, "9.9.9") || hits.Load() != before {
		t.Errorf("CI=1 checked for updates: %q (%d requests)", errOut, hits.Load()-before)
	}
}
