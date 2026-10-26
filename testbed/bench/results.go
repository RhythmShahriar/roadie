// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/RhythmShahriar/roadie/testbed/manifest"
	"github.com/RhythmShahriar/roadie/testbed/oracle"
)

// Outcomes of a scenario or a case.
const (
	Caught  = "caught"  // every gate held and nothing forbidden appeared
	Missed  = "missed"  // a gate failed
	Errored = "error"   // the harness couldn't run it (setup, Docker)
	Pending = "pending" // the manifest says why it can't run yet
)

// ResultsVersion is the results.json version.
const ResultsVersion = 1

// Results is results.json. Scored is the same in two runs of the same
// code (make reproduce diffs it): no times, sorted lists, no wording that
// varies. Run holds what varies: dates, durations, versions, performance.
type Results struct {
	Version int     `json:"version"`
	Scored  Scored  `json:"scored"`
	Run     RunInfo `json:"run"`
	// Tools are the other tools judged on the same inputs (tools.go);
	// -diff compares their outcomes and counts, not their side effects.
	Tools []ToolResult `json:"tools,omitempty"`
}

// Scored is the part of results.json that is the score.
type Scored struct {
	Summary   Summary          `json:"summary"`
	Scenarios []ScenarioResult `json:"scenarios"`
	Control   Control          `json:"control"`
}

// Summary is the headline (PRD 22.3).
type Summary struct {
	Scenarios int            `json:"scenarios"`
	Outcomes  map[string]int `json:"outcomes"`
	// Canaries and LiveKeys are what the scored scenarios seeded (each
	// case that checked them); the survivors are the distinct canaries
	// the oracle found of them.
	Canaries          int            `json:"canaries"`
	CanarySurvivors   int            `json:"canary_survivors"`
	ByEncoding        map[string]int `json:"survivors_by_encoding"`
	LiveKeys          int            `json:"live_keys"`
	LiveKeysSurvived  int            `json:"live_keys_survived"`
	TestKeysChanged   int            `json:"test_keys_changed"`
	IntegrityChecked  int            `json:"integrity_checked"`
	IntegrityFailures int            `json:"integrity_failures"`
	RowsDiffer        int            `json:"scenarios_rows_differ"`
	Sinkhole          int            `json:"sinkhole_connections"`
	StubLive          int            `json:"stub_calls_live"`
	EchoReal          int            `json:"echo_real_recipients"`
	BootFailures      int            `json:"boot_failures"`
	VerifyRecall      int            `json:"verify_caught"`
	VerifyBad         int            `json:"verify_bad"`
	FalseAlarms       int            `json:"false_alarms"`
	ControlChecks     int            `json:"control_checks"`
	// NotBuilt counts cases Roadie answered with not-built (pending
	// without the manifest saying so); -strict fails on any.
	NotBuilt int `json:"not_built,omitempty"`
}

// ScenarioResult is one scenario's score.
type ScenarioResult struct {
	ID      string       `json:"id"`
	Kind    string       `json:"kind"`
	Title   string       `json:"title"`
	Outcome string       `json:"outcome"`
	Pending string       `json:"pending,omitempty"`
	Partial []string     `json:"partial,omitempty"`
	Cases   []CaseResult `json:"cases"`
}

// CaseResult is one case's gates.
type CaseResult struct {
	Name    string `json:"name,omitempty"`
	Outcome string `json:"outcome"`
	Pending string `json:"pending,omitempty"`
	// NotBuilt is set when the case is pending because Roadie answered
	// not-built, not because the manifest marks it pending.
	NotBuilt bool `json:"not_built,omitempty"`
	// Detail says why the harness couldn't run the case (Errored).
	Detail string `json:"detail,omitempty"`
	Gates  []Gate `json:"gates"`

	Exit      int            `json:"exit"`
	ErrorCode string         `json:"error_code,omitempty"`
	IDs       []string       `json:"ids,omitempty"`
	Canaries  int            `json:"canaries,omitempty"`
	LiveKeys  int            `json:"live_keys,omitempty"`
	Oracle    *oracle.Report `json:"oracle,omitempty"`
	// Gate3 feeds the summary; its counts vary from run to run (the
	// traffic generator), so results.json keeps it under run.
	Gate3      *Gate3 `json:"-"`
	OutputHash string `json:"output_sha256,omitempty"`
}

// Gate is one check of a case. Detail never holds a canary value or an
// unmasked key: IDs, counts and codes only.
type Gate struct {
	Name   string `json:"name"`
	Pass   bool   `json:"pass"`
	Detail string `json:"detail,omitempty"`
}

// Control is the control pass: verify run Rounds times on every good
// input; any finding at or above low is a false alarm.
type Control struct {
	Rounds      int            `json:"rounds"`
	Checks      int            `json:"checks"`
	FalseAlarms int            `json:"false_alarms"`
	ByInput     map[string]int `json:"by_input"`
	// IDs are the finding IDs that were false alarms, sorted.
	IDs []string `json:"ids,omitempty"`
	// Skipped are inputs the control pass couldn't verify, with why.
	Skipped []string `json:"skipped,omitempty"`
}

// RunInfo is the part of results.json that changes from run to run.
type RunInfo struct {
	Meta      Meta                   `json:"meta"`
	Scenarios map[string]ScenarioRun `json:"scenarios"`
	Perf      *Perf                  `json:"perf,omitempty"`
}

// ScenarioRun is what one scenario's run took.
type ScenarioRun struct {
	WallMS int64              `json:"wall_ms"`
	Cases  map[string]CaseRun `json:"cases,omitempty"`
	// Notes are problems after the cases ran (Echo not coming back).
	Notes []string `json:"notes,omitempty"`
}

// CaseRun is what one case's Roadie run took and what the drill did.
type CaseRun struct {
	RoadieMS int64         `json:"roadie_ms"`
	MaxRSSKB int64         `json:"max_rss_kb,omitempty"`
	Traffic  *TrafficStats `json:"traffic,omitempty"`
	Gate3    *Gate3        `json:"gate3,omitempty"`
	Drill    *DrillResult  `json:"drill,omitempty"`
	Notes    []string      `json:"notes,omitempty"`
}

// add appends a gate.
func (c *CaseResult) add(name string, pass bool, format string, a ...any) {
	g := Gate{Name: name, Pass: pass}
	if format != "" {
		g.Detail = fmt.Sprintf(format, a...)
	}
	c.Gates = append(c.Gates, g)
}

// score sets the case's outcome from its gates.
func (c *CaseResult) score() {
	if c.Gates == nil {
		c.Gates = []Gate{}
	}
	switch {
	case c.Pending != "":
		c.Outcome = Pending
	case c.Detail != "":
		c.Outcome = Errored
	case len(c.Gates) == 0:
		c.Outcome = Errored
		c.Detail = "no gate was checked"
	default:
		c.Outcome = Caught
		for _, g := range c.Gates {
			if !g.Pass {
				c.Outcome = Missed
				break
			}
		}
	}
}

// scoreScenario sets a scenario's outcome from its cases: pending when
// the scenario is, missed when any case missed, error when any case
// errored (and none missed), caught when every case that ran was caught.
func scoreScenario(s *ScenarioResult) {
	if s.Pending != "" {
		s.Outcome = Pending
		return
	}
	outs := map[string]int{}
	for _, c := range s.Cases {
		outs[c.Outcome]++
	}
	switch {
	case outs[Missed] > 0:
		s.Outcome = Missed
	case outs[Errored] > 0:
		s.Outcome = Errored
	case outs[Caught] > 0:
		s.Outcome = Caught
	default:
		s.Outcome = Pending
	}
}

// summarize fills the summary from the scenarios and the control pass.
// bad verify scenarios count toward recall.
func summarize(sc *Scored, kinds map[string]manifest.Scenario) {
	s := Summary{Outcomes: map[string]int{}, ByEncoding: map[string]int{}}
	for _, r := range sc.Scenarios {
		s.Scenarios++
		s.Outcomes[r.Outcome]++
		m := kinds[r.ID]
		if m.Kind == manifest.KindVerify && !m.Good {
			s.VerifyBad++
			if r.Outcome == Caught {
				s.VerifyRecall++
			}
		}
		for _, c := range r.Cases {
			if c.NotBuilt {
				s.NotBuilt++
			}
			s.Canaries += c.Canaries
			s.LiveKeys += c.LiveKeys
			if o := c.Oracle; o != nil {
				ids, _ := survivorIDs(o.Survivors)
				s.CanarySurvivors += len(ids)
				for enc, n := range o.ByEncoding {
					s.ByEncoding[enc] += n
				}
				s.LiveKeysSurvived += o.LiveKeys
				s.TestKeysChanged += len(o.TestKeysMissing)
				if o.Integrity != nil {
					s.IntegrityChecked += o.Integrity.Checked
					s.IntegrityFailures += o.Integrity.Failures + o.Integrity.Missing
				}
			}
			for _, g := range c.Gates {
				if g.Name == "rows" && !g.Pass {
					s.RowsDiffer++
				}
				if strings.HasPrefix(g.Name, "boot") && !g.Pass {
					s.BootFailures++
				}
			}
			if g := c.Gate3; g != nil {
				s.Sinkhole += g.Sinkhole
				s.StubLive += g.StubLive
				s.EchoReal += g.EchoRealRecips
			}
		}
	}
	s.FalseAlarms = sc.Control.FalseAlarms
	s.ControlChecks = sc.Control.Checks
	sc.Summary = s
}

// WriteResults writes results.json, 0600 (it names canary IDs and
// locations, never values; it is test output all the same).
func WriteResults(path string, r Results) error {
	sort.Slice(r.Scored.Scenarios, func(i, j int) bool {
		return order(r.Scored.Scenarios[i].ID) < order(r.Scored.Scenarios[j].ID)
	})
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// order is a scenario's place in PRD 22.2 (r01..r28, v01..v07, g01..g05).
func order(id string) int {
	i := slices.Index(manifest.ScenarioIDs(), id)
	if i < 0 {
		return 1 << 20
	}
	return i
}

// ReadResults reads a results.json.
func ReadResults(path string) (Results, error) {
	var r Results
	b, err := os.ReadFile(path) // #nosec G304 -- a results.json the operator names
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return r, fmt.Errorf("%s: %w", path, err)
	}
	if r.Version != ResultsVersion {
		return r, fmt.Errorf("%s: version %d, this harness reads %d", path, r.Version, ResultsVersion)
	}
	return r, nil
}

// DiffResults compares the scored parts of two results.json files and
// writes each difference to w. Same is true when there is none. Two runs
// with no scenario prove nothing, so that is an error.
func DiffResults(a, b string, w io.Writer) (same bool, err error) {
	ra, err := ReadResults(a)
	if err != nil {
		return false, err
	}
	rb, err := ReadResults(b)
	if err != nil {
		return false, err
	}
	if len(ra.Scored.Scenarios) == 0 || len(rb.Scored.Scenarios) == 0 {
		return false, fmt.Errorf("no scenarios in %s or %s: was a run aborted?", a, b)
	}
	la, lb := scoredLines(ra.Scored), scoredLines(rb.Scored)
	toolLines(ra.Tools, la)
	toolLines(rb.Tools, lb)
	keys := map[string]bool{}
	for k := range la {
		keys[k] = true
	}
	for k := range lb {
		keys[k] = true
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Slice(sorted, func(i, j int) bool { return lessKey(sorted[i], sorted[j]) })
	same = true
	for _, k := range sorted {
		if la[k] != lb[k] {
			same = false
			fmt.Fprintf(w, "%s: %s | %s\n", k, orDash(la[k]), orDash(lb[k]))
		}
	}
	if same {
		fmt.Fprintf(w, "%d scenarios, the same score in both runs\n", len(ra.Scored.Scenarios))
	}
	return same, nil
}

// lessKey sorts diff keys by scenario order, then by the rest.
func lessKey(a, b string) bool {
	ia, _, _ := strings.Cut(a, "/")
	ib, _, _ := strings.Cut(b, "/")
	if oa, ob := order(ia), order(ib); oa != ob {
		return oa < ob
	}
	return a < b
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// scoredLines flattens the scored part into "key: value" pairs a diff
// can name: each case's outcome and gates, the output hash, the summary
// and the control pass.
func scoredLines(s Scored) map[string]string {
	out := map[string]string{}
	for _, r := range s.Scenarios {
		out[r.ID] = r.Outcome
		for _, c := range r.Cases {
			k := r.ID
			if c.Name != "" {
				k += "/" + c.Name
			}
			out[k+" outcome"] = c.Outcome
			for _, g := range c.Gates {
				out[k+" gate "+g.Name] = fmt.Sprintf("%v %s", g.Pass, g.Detail)
			}
			if c.OutputHash != "" {
				out[k+" output"] = c.OutputHash
			}
		}
	}
	b, _ := json.Marshal(s.Summary)
	out["~summary"] = string(b)
	b, _ = json.Marshal(s.Control)
	out["~control"] = string(b)
	return out
}

// StrictProblems lists why a run fails under -strict on top of the
// usual outcome checks: a case Roadie left pending with not-built or
// a control pass that checked nothing although a good scenario ran.
// Scenarios the manifest marks pending don't count.
func StrictProblems(sc Scored, kinds map[string]manifest.Scenario) []string {
	var out []string
	if n := sc.Summary.NotBuilt; n > 0 {
		out = append(out, fmt.Sprintf("%d case(s) stopped at not-built", n))
	}
	good := false
	for _, r := range sc.Scenarios {
		if kinds[r.ID].Good && r.Pending == "" {
			good = true
		}
	}
	if good && sc.Control.Checks == 0 {
		out = append(out, "the control pass checked nothing")
	}
	return out
}
