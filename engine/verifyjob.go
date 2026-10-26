// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"time"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/profile"
	"github.com/RhythmShahriar/roadie/pseudo"
	"github.com/RhythmShahriar/roadie/verify"
)

// VerifyJob builds a standalone verify: opens the target read-only for its schema, detects stack and
// profiles, compiles the covering rules and the kill-switch Steps; with from set it runs pass 1 over
// production read-only to fill Known and ProductionPairs (exact mode).
//
// The returned job's Site is nil: verify.Run opens the target itself. A
// target read from stdin can't be read twice, so it is spooled encrypted
// while the rules are compiled and the job's Source reads the spool back;
// closing that source removes the spool.
func VerifyJob(ctx context.Context, j Job, target core.Endpoint, from *core.Endpoint) (verify.Job, error) {
	r := &runner{j: j, mode: j.Mode, now: j.Clock}
	if r.now == nil {
		r.now = time.Now
	}
	cfg, err := effective(j.Config, j.Opts)
	if err != nil {
		return verify.Job{}, err
	}
	r.cfg, r.opts = cfg, j.Opts
	if r.opts.URL == "" {
		r.opts.URL = cfg.URL
	}
	// verify only reads: a trigger or a function in the copy (kept with
	// copy --keep-routines) or in production runs nowhere, so it is
	// read past instead of refused. verify has no --keep-routines.
	r.opts.KeepRoutines = true
	if err := needConfig(cfg, j.Opts, core.Endpoint{}, target, true); err != nil {
		return verify.Job{}, err
	}
	defer func() {
		for _, s := range r.opened {
			_ = s.Close()
		}
	}()
	if from != nil && from.Kind == core.Stdio && target.Kind == core.Stdio {
		return verify.Job{}, core.Errorf(core.ErrConfig, 2,
			"verify can't read both the copy and --from from stdin (-); name a file or a site for one of them")
	}
	defs, _, err := loadProfiles(ctx, r.opts.Offline)
	if err != nil {
		return verify.Job{}, err
	}
	switch {
	case isSite(target.Kind):
		s, err := openSite(ctx, target, r.siteOptions(true))
		if err != nil {
			return verify.Job{}, err
		}
		r.opened = append(r.opened, s)
		r.to = s
	case target.Kind == core.DumpFile || target.Kind == core.Stdio:
	default:
		return verify.Job{}, core.Errorf(core.ErrConfig, 2, "verify needs a staging site, database or dump file to check")
	}

	// The target's own rules: one read-only index pass over it.
	in, err := r.inputFor(ctx, target, r.to, false, false)
	if err != nil {
		return verify.Job{}, err
	}
	p := &pipeline{mode: Plan, cfg: cfg, opts: r.opts, defs: defs, in: in, site: r.to, progress: j.Progress}
	c, err := p.compileOnly(ctx)
	if err != nil {
		_ = in.close()
		return verify.Job{}, err
	}
	vj := verify.Job{Target: target, Rules: c.All(), Production: production(cfg, nil), Opts: r.verifyOptions(), Progress: j.Progress}
	if si, ok := in.(*streamInput); ok && target.Kind == core.Stdio {
		// verify reads the spool pass 1 wrote; closing it removes it.
		src, err := si.open(ctx, 2)
		if err != nil {
			_ = in.close()
			return verify.Job{}, err
		}
		vj.Source = &closingSource{Source: src, c: closerFunc(si.close)}
	} else if err := in.close(); err != nil {
		return verify.Job{}, err
	}
	if r.to != nil {
		ko := r.killOptions()
		vj.Steps = append(killSteps(r.to.Stack(), ko), exposureSteps(ko)...)
	}
	if from == nil {
		return vj, nil
	}
	known, found, pairs, err := r.productionIndex(ctx, *from, defs)
	if err != nil {
		if vj.Source != nil {
			_ = vj.Source.Close()
		}
		return verify.Job{}, err
	}
	vj.Known, vj.ProductionPairs = known, pairs
	vj.Production = production(cfg, found)
	return vj, nil
}

// closerFunc turns a func into an io.Closer.
type closerFunc func() error

func (f closerFunc) Close() error { return f() }

var _ io.Closer = closerFunc(nil)

// compileOnly runs pass 1 without Known and compiles the rules the whole
// schema shows: what verify needs to know of a copy.
func (p *pipeline) compileOnly(ctx context.Context) (*profile.Compiled, error) {
	idx := newIndexer(ctx, p.cfg, p.defs, p.site, nil)
	if p.progress != nil {
		p.progress.Phase("index", 0)
	}
	_, schema, err := p.index(ctx, idx)
	if err != nil {
		return nil, err
	}
	return p.finalCompile(idx, schema)
}

// productionIndex is a read-only pass 1 over production for exact mode:
// it fills and seals Known, reads the production URLs and, for a site,
// its wp-config.php and .env pairs. It reads production once, so a
// stream is never spooled.
func (r *runner) productionIndex(ctx context.Context, from core.Endpoint, defs []profile.Def) (core.Known, []string, []core.Pair, error) {
	var s core.Site
	if isSite(from.Kind) {
		var err error
		if s, err = openSite(ctx, from, r.siteOptions(true)); err != nil {
			return nil, nil, nil, err
		}
		r.opened = append(r.opened, s)
	}
	in, err := r.inputFor(ctx, from, s, false, true)
	if err != nil {
		return nil, nil, nil, err
	}
	defer func() { _ = in.close() }()
	// Known only has to match within this run, so any key does: a new
	// random one keeps the hashes useless outside it.
	var key pseudo.Key
	if _, err := rand.Read(key[:]); err != nil {
		return nil, nil, nil, err
	}
	known := newKnown(key)
	if known == nil {
		return nil, nil, nil, core.ErrNotBuilt
	}
	p := &pipeline{mode: Plan, cfg: r.cfg, opts: r.opts, defs: defs, in: in, site: s, progress: r.j.Progress}
	idx := newIndexer(ctx, p.cfg, defs, s, known)
	if p.progress != nil {
		p.progress.Phase("index", 0)
	}
	_, schema, err := p.index(ctx, idx)
	if err != nil {
		return nil, nil, nil, err
	}
	// The final rules first: they add the form entry values to Known.
	// Verify needs production's hosts, not rewrite pairs: without a
	// staging URL the plan never stops on a blog host url_map lacks.
	p.opts.URL = ""
	var found []string
	if c, err := p.finalCompile(idx, schema); err == nil {
		if plan, err := p.urlPlan(ctx, idx, c); err == nil {
			found = plan.Production
		}
	} else if !errors.Is(err, profile.ErrNoStack) {
		return nil, nil, nil, err
	}
	known.Seal()
	return known, found, productionPairs(ctx, s, r.cfg), nil
}
