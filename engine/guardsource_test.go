// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"slices"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
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
