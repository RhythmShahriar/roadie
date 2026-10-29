// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/roadiecfg"
	"github.com/RhythmShahriar/roadie/site"
)

// Once pass 1 has read the source's siteurl, a copy runs the target
// guard again with it, before anything is written: a target that answers
// to production's URL is refused even when production: is empty. A
// prepare (the source is its target) doesn't; the plan's target
// check names only the hosts the guard compared.
func TestRunGuardsSourceSiteURL(t *testing.T) {
	f := useRunFakes(t)
	f.guardErr = func(in site.GuardInput) error {
		if len(in.SourceURLs) > 0 {
			return core.Errorf(core.ErrTargetIsProduction, 3, "target-is-production: /srv/staging is shop.com, the source's own site URL.")
		}
		return nil
	}
	j := f.job(Copy)
	j.Config.Production = nil
	res, err := Run(context.Background(), j)
	if code, exit := errCode(err); code != core.ErrTargetIsProduction || exit != 3 {
		t.Fatalf("err %v", err)
	}
	if len(f.guards) != 2 || !slices.Contains(f.guards[1].SourceURLs, "https://shop.com") || f.guards[1].Mode != string(Copy) {
		t.Fatalf("guards %+v", f.guards)
	}
	if f.rec.index("sink") >= 0 || len(f.applied()) != 0 || len(f.staging.Writes()) != 0 {
		t.Errorf("refused copy wrote: %v", f.rec.all())
	}
	if res.Err == nil || res.Err.State != core.Untouched {
		t.Errorf("result %+v", res.Err)
	}

	// A plan stops where the copy would.
	f = useRunFakes(t)
	f.guardErr = func(in site.GuardInput) error {
		if len(in.SourceURLs) > 0 {
			return core.Errorf(core.ErrTargetIsProduction, 3, "target-is-production: x")
		}
		return nil
	}
	j = f.job(Plan)
	j.Config.Production = nil
	if _, err := Run(context.Background(), j); !isCode(err, core.ErrTargetIsProduction) {
		t.Errorf("plan: %v", err)
	}

	// The guard passes: the plan names production: and the source's host.
	f = useRunFakes(t)
	j = f.job(Plan)
	j.Config.Production = []string{"*.shop.com"}
	res, err = Run(context.Background(), j)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.TargetChecked, []string{"*.shop.com", "shop.com"}) {
		t.Errorf("target check %v", res.TargetChecked)
	}

	// A prepare compares production's URL only; no second guard runs.
	f = useRunFakes(t)
	res, err = Run(context.Background(), f.job(Prepare))
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range f.guards {
		if len(g.SourceURLs) > 0 {
			t.Errorf("prepare guarded with the source's URLs: %+v", g)
		}
	}
	if !slices.Equal(res.TargetChecked, []string{"shop.com"}) {
		t.Errorf("prepare target check %v", res.TargetChecked)
	}

	// A dump-file target has no host to check.
	f = useRunFakes(t)
	j = f.job(Copy)
	j.To = core.Endpoint{Kind: core.DumpFile, Raw: "out.sql", Display: "out.sql", Path: "out.sql"}
	res, err = Run(context.Background(), j)
	if err != nil {
		t.Fatal(err)
	}
	if res.TargetChecked != nil {
		t.Errorf("dump target check %v", res.TargetChecked)
	}
}

// --one-pass from a database or a dump file reads the source's siteurl
// before it writes, so the guard against a target answering to it still
// runs (an ssh tunnel to production's database looks like 127.0.0.1).
// From a stream into a database it can't, so production: must name the
// hosts.
func TestRunGuardsSourceSiteURLOnePass(t *testing.T) {
	f := useRunFakes(t)
	f.guardErr = func(in site.GuardInput) error {
		if len(in.SourceURLs) > 0 {
			return core.Errorf(core.ErrTargetIsProduction, 3, "target-is-production: x")
		}
		return nil
	}
	for _, mode := range []Mode{Copy, Plan} {
		j := f.job(mode)
		j.Config.Production = nil
		j.Opts.OnePass = true
		if _, err := Run(context.Background(), j); !isCode(err, core.ErrTargetIsProduction) {
			t.Errorf("%s --one-pass: %v", mode, err)
		}
	}
	if f.rec.index("sink") >= 0 || len(f.staging.Writes()) != 0 {
		t.Errorf("refused copy wrote: %v", f.rec.all())
	}

	r := &runner{j: Job{From: core.Endpoint{Kind: core.Stdio, Display: "stdin"}, To: stagingEP},
		opts: Options{OnePass: true}, cfg: roadiecfg.Default(), mode: Copy}
	if err := r.oneReadGuard(&streamInput{onePass: true}); !isCode(err, core.ErrConfig) {
		t.Errorf("stream into a database: %v", err)
	}
	r.cfg.Production = []string{"shop.com"}
	if err := r.oneReadGuard(&streamInput{onePass: true}); err != nil {
		t.Errorf("stream with production: %v", err)
	}
	r.cfg.Production = nil
	r.j.To = core.Endpoint{Kind: core.DumpFile, Path: "out.sql", Display: "out.sql"}
	if err := r.oneReadGuard(&streamInput{onePass: true}); err != nil {
		t.Errorf("stream into a file: %v", err)
	}
}

// After a prepare the URL findings say to run prepare again with --url,
// not to copy again.
func TestPrepareURLFixes(t *testing.T) {
	fs := []core.Finding{{ID: core.FindingURLProductionSiteURL, Fix: "Copy again with --url"},
		{ID: core.FindingURLProduction, Fix: "Copy again with --url"}, {ID: core.FindingPIIEmail, Fix: "Cover the column"}}
	prepareFixes(fs)
	for _, f := range fs[:2] {
		if !strings.HasPrefix(f.Fix, "Run prepare again with --url <staging URL>") {
			t.Errorf("%s: %q", f.ID, f.Fix)
		}
	}
	if fs[2].Fix != "Cover the column" {
		t.Errorf("other fix %q", fs[2].Fix)
	}
}
