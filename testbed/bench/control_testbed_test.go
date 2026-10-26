// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build testbed

package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/RhythmShahriar/roadie/testbed/manifest"
)

// TestGoodInputsAreClean is the control pass (PRD 22.2): the real
// rhythm-roadie verifies every good input five times and any finding at
// or above low is a false alarm. The dump inputs (g01 to g03) need no
// Docker; g04 joins when the testbed runs (make -C testbed up) and g05
// is the benchmark's own (it verifies the copies' outputs).
func TestGoodInputsAreClean(t *testing.T) {
	f, err := manifest.Load("../scenarios.yaml")
	if err != nil {
		t.Fatal(err)
	}
	only := []string{"g01", "g02", "g03"}
	work := t.TempDir()
	h := &Harness{Runtime: filepath.Join(work, "runtime"), Fixtures: "../fixtures", Work: filepath.Join(work, "work"), Seed: 1, Rounds: 5}
	ctx := context.Background()
	if h.Bin, err = binary(ctx, options{root: "../.."}, h.Work); err != nil {
		t.Fatal(err)
	}
	// With the testbed up, g04 (a Laravel site on laravel-db) joins.
	withDocker := *h
	withDocker.Runtime, withDocker.NeedDocker = "../runtime", true
	if derr := withDocker.Init(ctx); derr == nil {
		h, only = &withDocker, append(only, "g04")
	} else if err := h.Init(ctx); err != nil {
		t.Fatal(err)
	} else {
		t.Log("g04 left out: ", derr)
	}
	res := h.RunAll(ctx, f, only)
	ctl := res.Scored.Control
	if len(ctl.Skipped) > 0 && ctl.Checks == 0 {
		t.Skip("needs the cli lane: rhythm-roadie printed no envelope (", ctl.Skipped[0], ")")
	}
	for _, s := range res.Scored.Scenarios {
		for _, c := range s.Cases {
			if c.Pending != "" {
				t.Skipf("%s: %s", s.ID, c.Pending)
			}
		}
		if s.Outcome != Caught {
			t.Errorf("%s: %s", s.ID, s.Outcome)
			for _, c := range s.Cases {
				for _, g := range c.Gates {
					if !g.Pass {
						t.Logf("  %s: %s", g.Name, g.Detail)
					}
				}
			}
		}
	}
	if ctl.FalseAlarms != 0 {
		t.Errorf("%d false alarms in %d checks: %v (by input %v)", ctl.FalseAlarms, ctl.Checks, ctl.IDs, ctl.ByInput)
	}
	if want := len(only) * 5; ctl.Checks != want {
		t.Errorf("%d checks, want %d", ctl.Checks, want)
	}
}
