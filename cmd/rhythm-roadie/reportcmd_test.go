// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/report"
)

// signedReport writes r's envelope, signed with a key from seed byte b
// unless b is 0, to dir/name and returns the path and the key's ID.
func signedReport(t *testing.T, dir, name string, r *core.Result, b byte) (string, string) {
	t.Helper()
	e, err := report.Envelope(r, toolName, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	id := ""
	if b != 0 {
		s := testReportSigner(t, b)
		if err := s.Sign(e); err != nil {
			t.Fatal(err)
		}
		id = s.KeyID()
	}
	js, _ := json.MarshalIndent(e, "", "  ")
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, js, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, id
}

func testReportSigner(t *testing.T, b byte) *report.Signer {
	t.Helper()
	seed := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{b}, 32))
	s, _, err := report.LoadSigner("", func(k string) (string, bool) { return seed, k == report.EnvSigningKey })
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestReportCheck(t *testing.T) {
	testDeps(t, nil)
	dir := t.TempDir()
	dump := []byte("-- MySQL dump\n")
	sum := sha256.Sum256(dump)
	r := copyFail()
	r.Input = &core.Fingerprint{Kind: "sha256-file", Value: hex.EncodeToString(sum[:]), Bytes: int64(len(dump))}
	path, id := signedReport(t, dir, "report.json", r, 1)
	dumpPath := filepath.Join(dir, "prod.sql")
	os.WriteFile(dumpPath, dump, 0o600)

	out, errOut, code := runCmd(t, "report", "check", path)
	if code != 0 || out != report.CheckedLine(id)+"\nKey ID "+id+": pin it with --key "+id+" to accept only this signer.\n" || errOut != "" {
		t.Fatalf("valid: exit %d %q %q", code, out, errOut)
	}
	if !strings.HasPrefix(out, "signed by "+id[:14]+"… and unchanged since. It shows this Roadie run found these results; it doesn't prove the checks covered everything.") {
		t.Errorf("PRD 6.9 sentence: %q", out)
	}
	// --input and --key, by ID or by the public key.
	out, _, code = runCmd(t, "report", "check", path, "--input", dumpPath, "--key", id)
	if code != 0 || !strings.Contains(out, "prod.sql is the input it describes (the sha256 matches).") {
		t.Errorf("input: exit %d %q", code, out)
	}
	other := filepath.Join(dir, "other.sql")
	os.WriteFile(other, []byte("another"), 0o600)
	_, errOut, code = runCmd(t, "report", "check", path, "--input", other)
	if code != 1 || errOut != "rhythm-roadie report check: report.json describes another input (sha256 "+hex.EncodeToString(sum[:])[:12]+"…).\n" {
		t.Errorf("other input: exit %d %q", code, errOut)
	}
	theirs := testReportSigner(t, 2).KeyID()
	_, errOut, code = runCmd(t, "report", "check", path, "--key", theirs)
	if code != 1 || errOut != "rhythm-roadie report check: report.json is signed by "+id+", not by the key you pinned ("+theirs+").\n" {
		t.Errorf("pinned: exit %d %q", code, errOut)
	}

	// An edited report and an unsigned one.
	b, _ := os.ReadFile(path)
	edited := bytes.Replace(b, []byte(`"high": 1`), []byte(`"high": 0`), 1)
	os.WriteFile(filepath.Join(dir, "edited.json"), edited, 0o600)
	_, errOut, code = runCmd(t, "report", "check", filepath.Join(dir, "edited.json"))
	if code != 1 || errOut != "rhythm-roadie report check: edited.json was changed after it was signed (summary.high 1 → 0).\n" {
		t.Errorf("edited: exit %d %q", code, errOut)
	}
	unsigned, _ := signedReport(t, dir, "unsigned.json", copyFail(), 0)
	_, errOut, code = runCmd(t, "report", "check", unsigned)
	if code != 3 || errOut != "rhythm-roadie report check: unsigned.json has no signature (it was written with --sign=off).\n" {
		t.Errorf("unsigned: exit %d %q", code, errOut)
	}

	// --json says the same as one envelope.
	out, _, code = runCmd(t, "report", "check", filepath.Join(dir, "edited.json"), "--json")
	var env struct {
		Kind, Verdict string
		Data          struct {
			Result string `json:"result"`
			Error  struct {
				Code string `json:"code"`
			} `json:"error"`
		}
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil || code != 1 || env.Kind != "report-check" || env.Data.Result != "edited" || env.Data.Error.Code != core.ErrReportEdited {
		t.Errorf("--json: exit %d %v %+v\n%s", code, err, env, out)
	}

	// Usage errors.
	for _, args := range [][]string{{"check"}, {"check", path, path}, {"check", path, "-o", "x.html"}, {"check", path, "--key", "roadie:short"}} {
		if _, _, code := runCmd(t, "report", args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
	if _, _, code := runCmd(t, "report", "check", filepath.Join(dir, "missing.json")); code != 3 {
		t.Errorf("missing file: exit %d, want 3", code)
	}
}

func TestReportRender(t *testing.T) {
	testDeps(t, nil)
	dir := t.TempDir()
	pass, _ := signedReport(t, dir, "pass.json", copyPass(), 1)
	fail, _ := signedReport(t, dir, "fail.json", copyFail(), 1)
	html := filepath.Join(dir, "out", "all.html")
	md := filepath.Join(dir, "all.md")
	logo := filepath.Join(dir, "logo.png")
	os.WriteFile(logo, append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...), 0o600)

	out, errOut, code := runCmd(t, "report", pass, fail, "-o", html, "-o", md, "--brand-name", "Acme", "--brand-logo", logo)
	if code != 0 || errOut != "" {
		t.Fatalf("exit %d %q", code, errOut)
	}
	if out != "Wrote "+html+" (2 reports).\nWrote "+md+" (2 reports).\n" {
		t.Errorf("stdout %q", out)
	}
	page, _ := os.ReadFile(html)
	s := string(page)
	if !strings.Contains(s, "Acme") || !strings.Contains(s, "Summary, worst first") || strings.Index(s, "FAIL") > strings.Index(s, "PASS") {
		t.Error("the page isn't one summary, worst first, with the brand")
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(html); fi.Mode().Perm() != 0o600 {
			t.Errorf("mode %v", fi.Mode().Perm())
		}
	}
	// One report to stdout as Markdown.
	out, _, code = runCmd(t, "report", pass, "--md")
	if code != 0 || !strings.HasPrefix(out, "## Roadie: PASS, shop.com is a pseudonymised copy") {
		t.Errorf("--md: exit %d %q", code, out)
	}
	// SARIF of one verify report; several are refused before any file is written.
	sarif := filepath.Join(dir, "one.sarif")
	if _, errOut, code := runCmd(t, "report", fail, "-o", sarif); code != 0 {
		t.Errorf("sarif: exit %d %s", code, errOut)
	}
	two := filepath.Join(dir, "two.sarif")
	if _, errOut, code := runCmd(t, "report", pass, fail, "-o", two, "-o", filepath.Join(dir, "x.html")); code != 2 || !strings.Contains(errOut, "this format holds one report") {
		t.Errorf("two sarif: exit %d %q", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(dir, "x.html")); err == nil {
		t.Error("a file was written although another format failed")
	}
	// An edited report isn't rendered.
	b, _ := os.ReadFile(fail)
	edited := filepath.Join(dir, "edited.json")
	os.WriteFile(edited, bytes.Replace(b, []byte(`"verdict": "findings"`), []byte(`"verdict": "ok"`), 1), 0o600)
	if _, errOut, code := runCmd(t, "report", edited, "-o", filepath.Join(dir, "e.html")); code != 1 || !strings.Contains(errOut, "report: edited.json was changed after it was signed") || strings.Contains(errOut, "report check") {
		t.Errorf("edited: exit %d %q", code, errOut)
	}
	// An unsigned report renders and says so.
	unsigned, _ := signedReport(t, dir, "unsigned.json", copyPass(), 0)
	out, _, code = runCmd(t, "report", unsigned, "--md")
	if code != 0 || !strings.Contains(out, "This report is not signed (--sign=off)") {
		t.Errorf("unsigned: exit %d %q", code, out)
	}
	// Usage errors.
	bad := filepath.Join(dir, "bad.svg")
	os.WriteFile(bad, []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script/></svg>`), 0o600)
	for _, args := range [][]string{{}, {pass}, {pass, "--input", "x.sql", "--md"}, {pass, "-o", html, "--brand-logo", bad}} {
		if _, _, code := runCmd(t, "report", args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
	notJSON := filepath.Join(dir, "x.json")
	os.WriteFile(notJSON, []byte("{"), 0o600)
	if _, errOut, code := runCmd(t, "report", notJSON, "--md"); code != 1 || strings.Contains(errOut, "report check") || !strings.Contains(errOut, " report: x.json") {
		t.Errorf("not a report: exit %d %q, want 1 naming report", code, errOut)
	}
}
