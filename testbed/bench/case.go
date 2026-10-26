// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/RhythmShahriar/roadie/testbed/manifest"
)

// caseRun is one case in progress.
type caseRun struct {
	st    *scenarioState
	c     manifest.Case
	work  string // the case's local work folder
	env   RunEnv
	vars  map[string]string // as Roadie sees them
	local map[string]string // as the harness sees them
	res   CaseResult
	run   CaseRun
	key   string
}

// runCase runs one case and checks its gates.
func (h *Harness) runCase(ctx context.Context, st *scenarioState, c manifest.Case) (CaseResult, CaseRun) {
	cr := &caseRun{st: st, c: c, work: st.work, key: h.keys[0]}
	cr.res = CaseResult{Name: c.Name}
	if c.Pending != "" {
		cr.res.Pending = c.Pending
		return cr.res, cr.run
	}
	if len(st.s.Cases) > 0 {
		cr.work = filepath.Join(st.work, c.Name)
		if err := os.MkdirAll(cr.work, 0o700); err != nil {
			cr.res.Detail = err.Error()
			return cr.res, cr.run
		}
	}
	if err := h.prepareCase(ctx, cr); err != nil {
		cr.res.Detail = err.Error()
		return cr.res, cr.run
	}
	if c.Action.Name == manifest.ActionVerifyAll {
		h.verifyAll(ctx, cr)
		return cr.res, cr.run
	}
	args, err := manifest.ExpandAll(c.Command, cr.vars)
	if err != nil {
		cr.res.Detail = err.Error()
		return cr.res, cr.run
	}
	args = h.withStableKey(args)

	// Gate 3's window opens before the run when traffic runs during it.
	if st.traffic || st.cron || st.s.Drill && c.Expect.Sinkhole != nil {
		st.window = &Window{Logs: h.logs(), Echo: Echo{Base: gateEcho, Token: h.echoTok}, Real: st.real,
			EchoDown: slices.Contains(slices.Concat(st.s.Setup, c.Setup), manifest.SetupEchoDown)}
		t := Traffic{}
		if st.traffic {
			paths, cookies := stagingPages(ctx, h.Ex)
			t = Traffic{Base: gateStaging, Host: stagingHost, Paths: paths, Admin: []string{"/wp-admin/"}, Cookies: cookies, Every: 500 * time.Millisecond}
		}
		var cron time.Duration
		if st.cron {
			cron = 30 * time.Second
		}
		if err := st.window.Start(ctx, t, cron); err != nil {
			cr.res.Detail = err.Error()
			return cr.res, cr.run
		}
	}

	run, err := h.act(ctx, cr, args)
	if st.window != nil && (st.traffic || st.cron) {
		ts := st.window.Stop()
		cr.run.Traffic = &ts
	}
	if err != nil {
		cr.res.Detail = "running rhythm-roadie: " + err.Error()
		return cr.res, cr.run
	}
	cr.run.RoadieMS = run.Wall.Milliseconds()
	cr.run.MaxRSSKB = run.MaxRSSKB
	_ = os.WriteFile(filepath.Join(cr.work, "stdout.json"), run.Stdout, 0o600)
	_ = os.WriteFile(filepath.Join(cr.work, "stderr.log"), run.Stderr, 0o600)
	env, perr := ParseEnvelope(run.Stdout)
	if env.NotBuilt() {
		cr.res.Pending = "rhythm-roadie stopped at a part another lane hasn't built yet (not-built)"
		cr.res.NotBuilt = true
		return cr.res, cr.run
	}
	if err := cr.env.Collect(ctx); err != nil {
		cr.run.Notes = append(cr.run.Notes, "collect: "+err.Error())
	}
	h.checkRun(cr, run, env, perr)
	if st.s.Perf {
		h.measure(ctx, cr, run, env)
	}
	h.checkOutputs(ctx, cr)
	h.checkStaging(ctx, cr)
	h.checkVerify(ctx, cr)
	if st.window != nil {
		h.checkGate3(ctx, cr)
		st.window = nil
	}
	h.checkAbsent(ctx, cr)
	if cr.res.Outcome == "" && st.s.Good {
		h.controlRounds(ctx, cr, args, run, env)
	}
	return cr.res, cr.run
}

// prepareCase sets up the case's run environment, its own setup steps
// and the files its make asks for.
func (h *Harness) prepareCase(ctx context.Context, cr *caseRun) error {
	if cr.c.Make != "" {
		if err := makeFile(cr.st.in, cr.c.Make); err != nil {
			return fmt.Errorf("make %s: %w", cr.c.Make, err)
		}
	}
	cr.env = h.runEnv(cr.st, cr.c, cr.work)
	if err := cr.env.Prepare(ctx); err != nil {
		return fmt.Errorf("preparing %s: %w", cr.env.Name(), err)
	}
	cr.vars = cr.env.Vars()
	cr.local = localVars(cr.st, cr.work)
	for _, step := range cr.c.Setup {
		if err := h.setupStep(ctx, cr.st, step, cr.work); err != nil {
			return fmt.Errorf("%s: %w", step, err)
		}
	}
	steps := slices.Concat(cr.st.s.Setup, cr.c.Setup)
	if slices.Contains(steps, manifest.SetupLaravelSite) {
		if err := h.overlay(ctx, ctrLaravel, ctrRoot+"/site", filepath.Join(cr.st.in, "files"), h.dbValues(true, true)); err != nil {
			return err
		}
		if _, err := h.collectTree(ctx, manifest.OutLaravelApp, filepath.Join(cr.work, "input-site")); err != nil {
			return err
		}
	}
	// A config file the steps wrote locally goes in with the work folder.
	if ce, ok := cr.env.(*containerEnv); ok {
		for _, name := range []string{"roadie.yaml"} {
			p := filepath.Join(cr.st.work, name)
			if _, err := os.Stat(p); err == nil {
				if _, err := docker(ctx, nil, "cp", p, ce.ctr+":"+ctrWork+"/"+name); err != nil {
					return err
				}
				if _, err := h.Ex.Exec(ctx, ce.ctr, "root", nil, nil, "chown", ce.user, ctrWork+"/"+name); err != nil {
					return err
				}
			}
		}
	}
	if _, ok := cr.env.(*hostEnv); ok && slices.ContainsFunc(cr.c.Command, func(a string) bool { return strings.Contains(a, "${ssh_source}") }) {
		if err := h.sshFiles(cr.work, slices.Contains(steps, manifest.SetupUnknownHost)); err != nil {
			return err
		}
	}
	return nil
}

// logs are the runtime logs gate 3 reads.
func (h *Harness) logs() Logs {
	return Logs{Sinkhole: filepath.Join(h.Runtime, "log", "sinkhole.jsonl"), Stubs: filepath.Join(h.Runtime, "log", "stubs.jsonl")}
}

// roadie runs rhythm-roadie in the case's environment.
func (h *Harness) roadie(ctx context.Context, cr *caseRun, args []string, sig func(func(os.Signal) error)) (*Run, error) {
	return cr.env.Roadie(ctx, args, h.roadieEnv(cr.key), sig)
}
