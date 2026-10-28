// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/report"
)

// UNCOVERED in plan and on copy's stop screen reads as the finding does
// in every other view: the sampled rate from the run's finding, else the
// rate alone, else "by its name".
func TestUncoveredTitleEveryView(t *testing.T) {
	u := core.Uncovered{Table: "contacts", Column: "emergency_contact", Rate: 100}
	f := core.Finding{ID: core.FindingUncoveredColumn, Title: "contacts.emergency_contact looks personal (100% of 100 sampled values are phone numbers) and no rule covers it",
		Locations: []core.LocationCount{{Location: core.Location{Table: "contacts", Column: "emergency_contact"}}}}
	if got := uncoveredTitle(u, []core.Finding{f}); got != f.Title {
		t.Errorf("with the finding: %q", got)
	}
	if got := uncoveredTitle(u, nil); got != "contacts.emergency_contact looks personal (100% of sampled values) and no rule covers it" {
		t.Errorf("rate only: %q", got)
	}
	if got := uncoveredTitle(core.Uncovered{Table: "t", Column: "first_name"}, nil); got != "t.first_name looks personal (by its name) and no rule covers it" {
		t.Errorf("by name: %q", got)
	}
}

// The result box claims a pseudonymised copy only when the checks found
// nothing; findings below --fail-on and ones a baseline hid are named.
func TestVerdictBelowGate(t *testing.T) {
	high := core.Finding{ID: core.FindingPIIEmail, Severity: core.SeverityHigh}
	info := core.Finding{ID: core.FindingInfoKeptUsers, Severity: core.SeverityInfo}
	for _, c := range []struct {
		findings []core.Finding
		hidden   int64
		failOn   core.Severity
		word     string
		line     string
	}{
		{nil, 0, core.SeverityHigh, "PASS", "x.sql is a pseudonymised copy"},
		{[]core.Finding{info}, 0, core.SeverityHigh, "PASS", "x.sql is a pseudonymised copy"},
		{[]core.Finding{high, high}, 0, core.SeverityNone, "PASS", "x.sql: 2 findings, none fails the run (--fail-on none)"},
		{[]core.Finding{{Severity: core.SeverityMedium}}, 0, core.SeverityHigh, "PASS", "x.sql: 1 finding below --fail-on high"},
		{nil, 27, core.SeverityHigh, "PASS", "x.sql: 27 findings hidden by --baseline or ignore entries"},
		{[]core.Finding{high}, 0, core.SeverityHigh, "FAIL", "x.sql is not a safe copy yet"},
	} {
		r := &core.Result{Kind: "verify", Verify: &core.VerifyResult{Findings: c.findings, Hidden: c.hidden}}
		word, line, _ := verdict(r, c.failOn, "x.sql")
		if word != c.word || line != c.line {
			t.Errorf("%v hidden %d fail-on %v: %s %q", c.findings, c.hidden, c.failOn, word, line)
		}
	}
}

// report check --key names the machine's signing key as a private key,
// with exit 2, instead of comparing its bytes as a public key.
func TestReportCheckSigningKeyFile(t *testing.T) {
	testDeps(t, nil)
	dir := t.TempDir()
	path, _ := signedReport(t, dir, "report.json", copyFail(), 1)
	cfgDir := os.Getenv("RHYTHM_CONFIG_DIR")
	key := report.DefaultKeyPath(cfgDir)
	if err := os.MkdirAll(filepath.Dir(key), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(key); err != nil {
		if err := os.WriteFile(key, []byte("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Remove(key) })
	}
	_, errOut, code := runCmd(t, "report", "check", path, "--key", key)
	if code != 2 || !strings.Contains(errOut, "is Roadie's private signing key, not a public key") {
		t.Errorf("exit %d %q", code, errOut)
	}
}
