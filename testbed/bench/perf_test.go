// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestPerfRun(t *testing.T) {
	r := &Run{Exit: 0, Wall: 10 * time.Second, MaxRSSKB: 200 << 10}
	pr := perfRun("db.sql.gz", 2_000_000_000, r, nil)
	if pr.MBps != 200 || pr.WallMS != 10000 {
		t.Errorf("wall time: %+v", pr)
	}
	e := &Envelope{Data: EnvelopeData{DurationMS: 8000, Phases: map[string]int64{"transform": 6000}}}
	pr = perfRun("db.sql.gz", 2_000_000_000, r, e)
	if pr.MBps != 250 || pr.RoadieMS != 8000 || pr.PhasesMS["transform"] != 6000 {
		t.Errorf("Roadie's own duration wins: %+v", pr)
	}
}

func TestPerfJudge(t *testing.T) {
	flat := &Perf{Runs: []PerfRun{{Input: "db-small.sql.gz", MaxRSSKB: 200000}, {Input: "db.sql.gz", MaxRSSKB: 290000}}}
	flat.judge()
	if !flat.Pass || flat.RSSRatio != 1.45 {
		t.Errorf("1.45x must pass: %+v", flat)
	}
	grows := &Perf{Runs: []PerfRun{{Input: "db-small.sql.gz", MaxRSSKB: 100000}, {Input: "db.sql.gz", MaxRSSKB: 160000}}}
	grows.judge()
	if grows.Pass || !strings.Contains(strings.Join(grows.Notes, " "), "grew 1.60x") {
		t.Errorf("1.6x must fail: %+v", grows)
	}
	failed := &Perf{Runs: []PerfRun{{Input: "db.sql.gz", Exit: 3}}}
	failed.judge()
	if failed.Pass || len(failed.Notes) != 2 {
		t.Errorf("a failed copy fails and one size isn't compared: %+v", failed)
	}
}

func TestCompareBaseline(t *testing.T) {
	base := filepath.Join(t.TempDir(), "results.json")
	r := Results{Version: ResultsVersion, Run: RunInfo{Perf: &Perf{Runs: []PerfRun{{Input: "db-small.sql.gz", MBps: 100}, {Input: "db.sql.gz", MBps: 120}}}}}
	if err := WriteResults(base, r); err != nil {
		t.Fatal(err)
	}
	ok := &Perf{Pass: true, Runs: []PerfRun{{Input: "db-small.sql.gz", MBps: 85}, {Input: "db.sql.gz", MBps: 130}}}
	if err := CompareBaseline(ok, base); err != nil || !ok.Pass || len(ok.Compared) != 2 {
		t.Errorf("a 15%% drop passes: %v %+v", err, ok)
	}
	slow := &Perf{Pass: true, Runs: []PerfRun{{Input: "db.sql.gz", MBps: 90}}}
	if err := CompareBaseline(slow, base); err != nil || slow.Pass || slow.Compared[0].DropPct != 25 {
		t.Errorf("a 25%% drop fails: %v %+v", err, slow)
	}
	if err := CompareBaseline(ok, filepath.Join(t.TempDir(), "none.json")); err == nil {
		t.Error("a missing baseline is an error")
	}
	if err := CompareBaseline(nil, base); err != nil {
		t.Error("no perf record (r17 not run) compares nothing")
	}
}

func TestSwapInput(t *testing.T) {
	got := swapInput([]string{"copy", "--from", "${in}/db.sql.gz", "--to", "${work}/out.sql.gz", "--json"}, "db-small.sql.gz")
	want := []string{"copy", "--from", "${in}/db-small.sql.gz", "--to", "${work}/out-db-small.sql.gz", "--json"}
	if !slices.Equal(got, want) {
		t.Errorf("got %q", got)
	}
}
