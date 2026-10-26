// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/internal/testutil"
	"github.com/RhythmShahriar/roadie/testbed/manifest"
	"github.com/RhythmShahriar/roadie/testbed/oracle"
)

// cannedResults is a small benchmark run: a caught copy, a missed copy,
// a pending scenario, a harness error, two verify scenarios, a good
// input, r17's performance and the other tools.
func cannedResults() Results {
	held := func(names ...string) []Gate {
		var gs []Gate
		for _, n := range names {
			gs = append(gs, Gate{Name: n, Pass: true})
		}
		return gs
	}
	r := Results{Version: ResultsVersion}
	r.Scored.Scenarios = []ScenarioResult{
		{ID: "r01", Kind: "copy", Title: "WooCommerce HPOS shop with PII in orders, addresses, meta, lookup and notes", Outcome: Caught, Cases: []CaseResult{{
			Outcome: Caught, Exit: 0, Canaries: 1715, LiveKeys: 5,
			Gates:  append(held("envelope", "exit", "finding-ids"), Gate{Name: "canaries", Pass: true, Detail: "0 of 1715 survived (0 hits) "}, Gate{Name: "sinkhole", Pass: true, Detail: "0 connections, 0 stub calls with a live key, 0 captures to real recipients"}),
			Oracle: &oracle.Report{ByEncoding: map[string]int{}, Integrity: &oracle.Integrity{Checked: 412}},
		}}},
		{ID: "r04", Kind: "copy", Title: "Action Scheduler args", Outcome: Missed, Cases: []CaseResult{{
			Outcome: Missed, Exit: 0, Canaries: 280,
			Gates: append(held("envelope", "exit"), Gate{Name: "canaries", Pass: false, Detail: "2 of 280 survived (3 hits) r04-email-0003 (md5), r04-email-0007 (raw)"}),
			Oracle: &oracle.Report{Survivors: []oracle.Survivor{
				{ID: "r04-email-0003", Kind: "email", Encoding: "md5", Where: "db:wp_actionscheduler_actions", Count: 2},
				{ID: "r04-email-0007", Kind: "email", Encoding: "raw", Where: "db:wp_actionscheduler_actions", Count: 1},
			}, ByEncoding: map[string]int{"md5": 2, "raw": 1}},
		}}},
		{ID: "r15", Kind: "copy", Title: "Laravel MySQL", Outcome: Pending, Pending: "the Laravel framework isn't installed in the fixture app", Cases: []CaseResult{}},
		{ID: "r20", Kind: "env", Title: "Ctrl-C, SSH cut, disk full", Outcome: Errored, Cases: []CaseResult{
			{Name: "interrupt", Outcome: Caught, Exit: 130, Gates: held("exit", "no-finished-output")},
			{Name: "disk-full", Outcome: Errored, Detail: "setup: the runner image is missing"},
		}},
		{ID: "v01", Kind: "verify", Title: "staging made by wp search-replace only", Outcome: Caught, Cases: []CaseResult{{
			Outcome: Caught, Exit: 1, IDs: []string{"key.stripe-live", "pii.email", "url.production"}, Gates: held("exit", "finding-ids"),
		}}},
		{ID: "v02", Kind: "verify", Title: "generic tool skipped serialized and JSON", Outcome: Missed, Cases: []CaseResult{{
			Outcome: Missed, Exit: 0, Gates: []Gate{{Name: "exit", Pass: false, Detail: "exit 0, want 1"}},
		}}},
		{ID: "g01", Kind: "verify", Title: "clean small business", Outcome: Caught, Cases: []CaseResult{{Outcome: Caught, Gates: held("exit")}}},
	}
	r.Scored.Control = Control{Rounds: 5, Checks: 5, ByInput: map[string]int{"g01": 0}}
	summarize(&r.Scored, map[string]manifest.Scenario{"v01": {Kind: "verify"}, "v02": {Kind: "verify"}, "g01": {Kind: "verify", Good: true}})
	r.Run = RunInfo{
		Meta: Meta{Date: "2026-10-26T09:00:00Z", RoadieVersion: "1.0.0", GoVersion: "go1.26.0", OS: "linux", Arch: "amd64", CPU: "Test CPU 8-Core", Cores: 8, RAMMB: 31869, Disk: "NVMe SSD", Docker: "28.5.1", WallMS: 2_754_000},
		Scenarios: map[string]ScenarioRun{
			"r01": {WallMS: 341_000, Cases: map[string]CaseRun{"-": {RoadieMS: 4200, Gate3: &Gate3{}, Drill: &DrillResult{Rounds: 9}, Traffic: &TrafficStats{Requests: 1290}}}},
			"r04": {WallMS: 12_400},
			"r20": {WallMS: 61_000},
			"v01": {WallMS: 900},
			"v02": {WallMS: 850},
			"g01": {WallMS: 4_100},
		},
		Perf: &Perf{Runs: []PerfRun{
			{Input: "db-small.sql.gz", InputBytes: 200_000_000, WallMS: 2_100, MBps: 95.2, MaxRSSKB: 96_000, PhasesMS: map[string]int64{"index": 400, "transform": 1500}},
			{Input: "db.sql.gz", InputBytes: 2_000_000_255, WallMS: 19_800, MBps: 101.0, MaxRSSKB: 120_000},
		}, RSSRatio: 1.25, Pass: true, Compared: []PerfCompare{{Input: "db.sql.gz", Baseline: 104.5, Now: 101.0, DropPct: 3.3}}},
	}
	r.Tools = []ToolResult{
		{Name: ToolWPCLI, Title: "WP-CLI search-replace", Version: "2.12.0", Judged: []string{MeasureURLs}, Inputs: []ToolInput{
			{Input: "r09", Outcome: Missed, Detail: "273 replacements in 7 passes; 1 of 274 production URLs left; 60 of 181 lookalike links and addresses changed", URLs: &Count{1, 274}, Collateral: &Count{60, 181}},
		}},
		{Name: ToolReplibyte, Title: "Replibyte", Version: "0.10.0", Judged: []string{MeasureCanaries, MeasureLiveKeys, MeasureIntegrity}, Inputs: []ToolInput{
			{Input: "r01", Outcome: Missed, Detail: "900 of 1715 personal values and 5 of 5 live keys left (encodings: raw)", Canaries: &Count{900, 1715}, LiveKeys: &Count{5, 5}, Integrity: &Count{0, 412}},
			{Input: "r12", Outcome: Errored, Detail: "replibyte: exit status 1"},
		}},
		{Name: ToolGreenmask, Title: "Greenmask", Version: "0.2.26", Judged: []string{MeasureCanaries, MeasureLiveKeys, MeasureIntegrity}, NotRun: greenmaskReason("0.2.26")},
		{Name: ToolStagingShield, Title: "Staging Shield", Version: "1.0.2", Judged: []string{MeasureSideEffects}, Inputs: []ToolInput{
			{Input: "r13", Outcome: Missed, Detail: "4 connections, 0 stub calls with a live key, 1 captures to real recipients", SideEffects: &Count{Left: 5}},
		}},
	}
	return r
}

func TestWriteDocGolden(t *testing.T) {
	f, err := manifest.Load("../scenarios.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := WriteDoc(&b, cannedResults(), f); err != nil {
		t.Fatal(err)
	}
	testutil.Golden(t, "benchmark.md", b.Bytes())
	checkDoc(t, b.String())
}

// checkDoc holds the page to PRD 22.3 and the writing rules.
func checkDoc(t *testing.T, doc string) {
	t.Helper()
	first, _, _ := strings.Cut(doc, "\n")
	if first != "<!-- Generated by go run -tags testbed ./testbed/bench. Don't edit by hand: rerun the benchmark. -->" {
		t.Errorf("first line %q", first)
	}
	for _, h := range []string{"# Roadie benchmark", "## Run", "## Scenarios", "## Canary survival", "## Side effects", "## verify", "## Performance", "## Other tools", "## What this does not show", "## Details", "## Run it"} {
		if !strings.Contains(doc, "\n"+h+"\n") {
			t.Errorf("no section %q", h)
		}
	}
	if !strings.Contains(doc, "\n"+Framing+"\n") {
		t.Error("the framing paragraph must be there word for word")
	}
	if !regexp.MustCompile(`\*\*Roadie left [\d,]+ of [\d,]+ seeded personal values and [\d,]+ of [\d,]+ live keys; verify caught \d+ of \d+ unsafe stagings with [\d,]+ false alarms in [\d,]+ checks of clean ones\.\*\*`).MatchString(doc) {
		t.Error("no headline")
	}
	for _, m := range []string{"date", "Roadie", "OS and arch", "CPU", "RAM", "Disk", "Docker", "Wall time"} {
		if !strings.Contains(strings.ToLower(doc), "| "+strings.ToLower(m)+" |") {
			t.Errorf("run metadata lacks %s", m)
		}
	}
	// The wording rules (PRD 19.2): "anonymous" only in the framing, no
	// banned word, no em dash, no comma before and or but.
	rest := strings.Replace(doc, Framing, "", 1)
	for _, w := range []string{"anonymous", "anonymised", "anonymized", "anonymise", "anonymize", "gdpr compliant", "gdpr-compliant", "compliant", "safe to share", string(rune(0x2014))} {
		if strings.Contains(strings.ToLower(rest), w) {
			t.Errorf("the page says %q", w)
		}
	}
	if m := regexp.MustCompile(`, (and|but)\b`).FindString(doc); m != "" {
		t.Errorf("comma before and/but: %q", m)
	}
}

func TestWriteDocValues(t *testing.T) {
	f, err := manifest.Load("../scenarios.yaml")
	if err != nil {
		t.Fatal(err)
	}
	r := cannedResults()
	var b bytes.Buffer
	if err := WriteDoc(&b, r, f); err != nil {
		t.Fatal(err)
	}
	doc := b.String()
	for _, want := range []string{
		"**Roadie left 2 of 1,995 seeded personal values and 0 of 5 live keys; verify caught 1 of 2 unsafe stagings with 0 false alarms in 5 checks of clean ones.**",
		"| r04 | copy | woocommerce | Action Scheduler args | missed | 2 of 3 held; failed: canaries |",
		"| r15 | copy | laravel | Laravel MySQL | pending | pending |",
		"| md5 | 2 |", "| sha256 | 0 |",
		"| r01 | 0 | 0 | 0 | 9 | 1,290 |",
		"| db.sql.gz | 2,000 MB | 0 | 19.8s | 101.0 | 123 MB | - |",
		"index 400, transform 1,500",
		"| WP-CLI search-replace | 2.12.0 | r09 | 1 of 274 left; broke 60 of 181 lookalikes and addresses | can't see | can't see | can't see | can't see | missed |",
		"| Replibyte | 0.10.0 | r12 | can't see | error | error | error | can't see | error |",
		"| Greenmask | 0.2.26 |  | not run | not run | not run | not run | not run | not run |",
		"- **Greenmask** not run: Greenmask 0.2.26 dumps and restores PostgreSQL only",
		"| Staging Shield | 1.0.2 | r13 | can't see | can't see | can't see | can't see | 5 got out | missed |",
		"- disk-full: error, setup: the runner image is missing.",
		"r04-email-0003 (md5)",
		"| v01 | staging made by wp search-replace only | caught | key.stripe-live, pii.email, url.production |",
		"Run on", // never: the metadata is a table
	} {
		if want == "Run on" {
			if strings.Contains(doc, want) {
				t.Errorf("unexpected %q", want)
			}
			continue
		}
		if !strings.Contains(doc, want) {
			t.Errorf("the page lacks %q", want)
		}
	}

	// No perf, no tools.
	r.Run.Perf, r.Tools = nil, nil
	b.Reset()
	if err := WriteDoc(&b, r, f); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"r17 didn't run in this benchmark.", "No other tool ran in this benchmark."} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("lacks %q", want)
		}
	}
}

func TestDocHelpers(t *testing.T) {
	for n, want := range map[int]string{0: "0", 999: "999", 1000: "1,000", 1715: "1,715", 2000000255: "2,000,000,255", -1234: "-1,234"} {
		if got := num(n); got != want {
			t.Errorf("num(%d) = %q, want %q", n, got, want)
		}
	}
	for ms, want := range map[int64]string{350: "350ms", 4230: "4.2s", 637_400: "10m37s"} {
		if got := dur(ms); got != want {
			t.Errorf("dur(%d) = %q, want %q", ms, got, want)
		}
	}
	var b docWriter
	b.row("a|b", "c\nd")
	if got := b.String(); got != "| a\\|b | c d |\n" {
		t.Errorf("row %q", got)
	}
	for o, want := range map[options]string{
		{}: "docs/benchmark.md", {only: "r01"}: "", {only: "r01", doc: "x.md"}: "x.md", {doc: "none"}: "", {only: "r01", render: "r.json"}: "docs/benchmark.md",
	} {
		if got := docPath(o); got != want {
			t.Errorf("docPath(%+v) = %q, want %q", o, got, want)
		}
	}
}

// TestRender writes the page from a results.json, as the nightly and
// -render do.
func TestRender(t *testing.T) {
	dir := t.TempDir()
	res := filepath.Join(dir, "results.json")
	if err := WriteResults(res, cannedResults()); err != nil {
		t.Fatal(err)
	}
	doc := filepath.Join(dir, "docs", "benchmark.md")
	var out, errb bytes.Buffer
	if code := run([]string{"-root", "../..", "-render", res, "-doc", doc}, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	b, err := os.ReadFile(doc)
	if err != nil {
		t.Fatal(err)
	}
	checkDoc(t, string(b))
	if code := run([]string{"-root", "../..", "-render", filepath.Join(dir, "missing.json")}, io.Discard, io.Discard); code != 3 {
		t.Errorf("a missing results.json: exit %d, want 3", code)
	}
	if code := run([]string{"-root", "../..", "-render", res, "-doc", "none"}, io.Discard, io.Discard); code != 2 {
		t.Errorf("-render with -doc none: exit %d, want 2", code)
	}
}
