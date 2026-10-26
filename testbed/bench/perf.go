// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/RhythmShahriar/roadie/testbed/manifest"
)

// Perf is r17's performance record (PRD 22.3): wall time, MB/s and peak
// memory per input size, the memory ratio between the sizes and the
// comparison with an earlier run (the nightly's regression guard). It
// lives under run in results.json: it varies from run to run.
type Perf struct {
	Runs []PerfRun `json:"runs"`
	// RSSRatio is the peak RSS of the largest input over the smallest;
	// PRD 22.3 wants it within 1.5.
	RSSRatio float64 `json:"rss_ratio,omitempty"`
	// Compared holds each input's MB/s against the baseline's.
	Compared []PerfCompare `json:"compared,omitempty"`
	Pass     bool          `json:"pass"`
	Notes    []string      `json:"notes,omitempty"`
}

// PerfRun is one timed copy.
type PerfRun struct {
	Input      string           `json:"input"`       // db-small.sql.gz, db.sql.gz
	InputBytes int64            `json:"input_bytes"` // SQL bytes before compression (gen.json)
	Exit       int              `json:"exit"`
	WallMS     int64            `json:"wall_ms"`
	RoadieMS   int64            `json:"roadie_ms,omitempty"` // data.duration_ms
	MBps       float64          `json:"mb_per_s"`
	MaxRSSKB   int64            `json:"max_rss_kb"`
	PhasesMS   map[string]int64 `json:"phases_ms,omitempty"`
}

// PerfCompare is one input's throughput against the baseline.
type PerfCompare struct {
	Input    string  `json:"input"`
	Baseline float64 `json:"baseline_mb_per_s"`
	Now      float64 `json:"mb_per_s"`
	DropPct  float64 `json:"drop_pct"`
}

// The memory and regression limits of PRD 22.3.
const (
	maxRSSRatio = 1.5
	maxDropPct  = 20.0
)

// perfInputs are r17's inputs, smallest first: the generator's
// db-small.sql.gz next to the main db.sql.gz.
var perfInputs = []string{"db-small.sql.gz", "db.sql.gz"}

// measure times the case's copy on every generated size. run is the
// case's own run on db.sql.gz; the others run here with the same
// arguments and their own output.
func (h *Harness) measure(ctx context.Context, cr *caseRun, main *Run, env *Envelope) {
	sizes := genSizes(cr.st.in)
	p := &Perf{}
	for _, in := range perfInputs {
		var r *Run
		var e *Envelope
		if in == "db.sql.gz" {
			r, e = main, env
		} else {
			if _, err := os.Stat(filepath.Join(cr.st.in, in)); err != nil {
				p.Notes = append(p.Notes, in+" not generated: "+err.Error())
				continue
			}
			args, err := manifest.ExpandAll(swapInput(cr.c.Command, in), cr.vars)
			if err != nil {
				p.Notes = append(p.Notes, err.Error())
				continue
			}
			if r, err = h.roadie(ctx, cr, h.withStableKey(args), nil); err != nil {
				p.Notes = append(p.Notes, in+": "+err.Error())
				continue
			}
			e, _ = ParseEnvelope(r.Stdout)
			_ = os.Remove(filepath.Join(cr.work, "out-"+strings.TrimSuffix(in, ".sql.gz")+".sql.gz"))
		}
		p.Runs = append(p.Runs, perfRun(in, sizes[in], r, e))
	}
	p.judge()
	h.perf = p
	cr.run.Notes = append(cr.run.Notes, p.Notes...)
}

// judge sets Pass: every copy exited 0 and peak memory grew at most 1.5x
// from the smallest input to the largest.
func (p *Perf) judge() {
	p.Pass = true
	if n := len(p.Runs); n >= 2 && p.Runs[0].MaxRSSKB > 0 {
		p.RSSRatio = float64(p.Runs[n-1].MaxRSSKB) / float64(p.Runs[0].MaxRSSKB)
		if p.RSSRatio > maxRSSRatio {
			p.Pass = false
			p.Notes = append(p.Notes, fmt.Sprintf("peak memory grew %.2fx from %s to %s (at most %.1fx)", p.RSSRatio, p.Runs[0].Input, p.Runs[n-1].Input, maxRSSRatio))
		}
	} else {
		p.Notes = append(p.Notes, "peak memory not compared: fewer than two sizes measured")
	}
	for _, r := range p.Runs {
		if r.Exit != 0 {
			p.Pass = false
			p.Notes = append(p.Notes, fmt.Sprintf("%s: the copy exited %d", r.Input, r.Exit))
		}
	}
}

// perfRun turns a run into its record. MB/s counts SQL bytes (before
// compression) over Roadie's own duration when it reports one, else the
// wall time; a copy that failed has none.
func perfRun(input string, bytes int64, r *Run, e *Envelope) PerfRun {
	pr := PerfRun{Input: input, InputBytes: bytes, Exit: r.Exit, WallMS: r.Wall.Milliseconds(), MaxRSSKB: r.MaxRSSKB}
	ms := pr.WallMS
	if e != nil {
		pr.RoadieMS, pr.PhasesMS = e.Data.DurationMS, e.Data.Phases
		if pr.RoadieMS > 0 {
			ms = pr.RoadieMS
		}
	}
	if ms > 0 && bytes > 0 && r.Exit == 0 {
		pr.MBps = float64(bytes) / 1e6 / (float64(ms) / 1000)
	}
	return pr
}

// swapInput points a copy command at another generated input and its
// own output.
func swapInput(args []string, input string) []string {
	out := make([]string, len(args))
	base := strings.TrimSuffix(input, ".sql.gz")
	for i, a := range args {
		a = strings.ReplaceAll(a, "${in}/db.sql.gz", "${in}/"+input)
		a = strings.ReplaceAll(a, "${work}/out.sql.gz", "${work}/out-"+base+".sql.gz")
		out[i] = a
	}
	return out
}

// genSizes reads the SQL size of each generated input from gen.json.
func genSizes(in string) map[string]int64 {
	out := map[string]int64{}
	b, err := os.ReadFile(filepath.Join(in, "gen.json")) // #nosec G304 -- the generator's own manifest
	if err != nil {
		return out
	}
	var m struct {
		Files map[string]struct {
			Bytes int64 `json:"bytes"`
		} `json:"files"`
	}
	if json.Unmarshal(b, &m) != nil {
		return out
	}
	for name, f := range m.Files {
		out[name] = f.Bytes
	}
	return out
}

// CompareBaseline checks p against the perf record of an earlier
// results.json: a drop of more than 20% in MB/s on any input fails
// (the nightly's regression guard).
func CompareBaseline(p *Perf, baseline string) error {
	if p == nil {
		return nil
	}
	r, err := ReadResults(baseline)
	if err != nil {
		return err
	}
	if r.Run.Perf == nil {
		p.Notes = append(p.Notes, "the baseline has no performance record")
		return nil
	}
	old := map[string]float64{}
	for _, run := range r.Run.Perf.Runs {
		old[run.Input] = run.MBps
	}
	for _, run := range p.Runs {
		b, ok := old[run.Input]
		if !ok || b <= 0 {
			continue
		}
		drop := (b - run.MBps) / b * 100
		p.Compared = append(p.Compared, PerfCompare{Input: run.Input, Baseline: b, Now: run.MBps, DropPct: drop})
		if drop > maxDropPct {
			p.Pass = false
			p.Notes = append(p.Notes, fmt.Sprintf("%s: %.1f MB/s, %.0f%% below the baseline's %.1f", run.Input, run.MBps, drop, b))
		}
	}
	return nil
}
