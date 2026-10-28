// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/RhythmShahriar/rhythm/kit/cli"
	"github.com/RhythmShahriar/rhythm/kit/config"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/engine"
	"github.com/RhythmShahriar/roadie/internal/demo"
)

// The demo shop's settings: a fixed seed so every demo shows the same
// numbers; the staging URL it copies to.
const (
	demoSeed      = 2026
	demoCustomers = 40
	demoURL       = "https://staging.demo-shop.test"
)

// runDemo is demo [--keep] (PRD 6.10): a built-in shop with fake "real"
// customers, a plan, a copy to a dump file, a verify of the copy (PASS)
// and a verify of the untouched shop (FAIL). Everything lives in a 0700
// folder in Roadie's cache, deleted at the end unless --keep. No network
// and no database server: the shop is a dump file.
func runDemo(ctx context.Context, p *parsed, stdout, stderr io.Writer) error {
	if len(p.args) > 0 {
		return usageError(stderr, "demo", "demo takes no arguments; got %q", p.args[0])
	}
	dir, err := demoDir()
	if err != nil {
		return &cli.ExitError{Code: cli.ExitUnavailable, Err: err}
	}
	cleanup := func() {
		if !p.f.keep {
			_ = os.RemoveAll(dir)
		}
	}
	defer cleanup()

	vo := setupColor(&p.f.g, dep.stdoutTerminal(), dep.env)
	v := &view{w: stdout, o: vo}
	if p.f.g.json {
		v.w = io.Discard
	}
	v.header("demo · a built-in shop")
	v.setting("Shop", fmt.Sprintf("%d fake customers, a live-looking Stripe key, an active webhook, production URLs in serialized and JSON-escaped forms", demoCustomers))

	input := filepath.Join(dir, "shop.sql")
	if err := writeDemoShop(input); err != nil {
		return demoStop(v, err)
	}
	from, err := dep.parseEndpoint(input)
	if err != nil {
		return demoStop(v, err)
	}
	to, err := dep.parseEndpoint(filepath.Join(dir, "staging.sql"))
	if err != nil {
		return demoStop(v, err)
	}
	opts := engine.Options{
		URL: demoURL, Production: dep.demoProduction(), Offline: true, Yes: true, FailOn: core.SeverityHigh,
		Keys: "swap", EmailDomain: defaultEmailDomain, EchoURL: defaultEchoURL, Jobs: defaultJobs(),
		MaxMemory: defaultMaxMemory, MaxValue: defaultMaxValue, MaxOutputRatio: defaultMaxOutputRatio,
	}
	out := newOutputs(p, stdout, stderr, nil, "", true)
	out.dir = dir
	out.format = ""
	out.p.f.g.outputs = nil

	// plan, then copy.
	plan, err := dep.run(ctx, engine.Job{Mode: engine.Plan, From: from, To: to, Opts: opts, Clock: dep.now})
	pr := settle(plan, err, "plan", engine.Job{From: from, To: to})
	if pr.Err != nil {
		return demoStop(v, pr.Err)
	}
	planWord, planLine := planBox(pr, engine.Job{From: from, To: to, Opts: opts})
	v.step("plan", planWord, planLine, "")
	copied, err := dep.run(ctx, engine.Job{Mode: engine.Copy, From: from, To: to, Opts: opts, Clock: dep.now})
	cr := settle(copied, err, "copy", engine.Job{From: from, To: to})
	if cr.Err != nil {
		return demoStop(v, cr.Err)
	}
	word, line, _ := verdict(cr, opts.FailOn, hostOf(demoURL))
	v.step("copy", word, line, counts(cr.Summary()))

	// verify the copy, then the untouched shop.
	pass := demoVerify(ctx, to, opts)
	passPath, err := out.write(pass)
	if err != nil {
		return demoStop(v, err)
	}
	if pass.Err != nil {
		return demoStop(v, pass.Err)
	}
	pw, pl, _ := verdict(pass, opts.FailOn, "staging.sql")
	v.step("verify the copy", pw, pl, counts(pass.Summary()))
	fail := demoVerify(ctx, from, opts)
	failPath, err := out.write(fail)
	if err != nil {
		return demoStop(v, err)
	}
	if fail.Err != nil {
		return demoStop(v, fail.Err)
	}
	fw, fl, _ := verdict(fail, opts.FailOn, "shop.sql")
	v.step("verify the shop", fw, fl, counts(fail.Summary()))

	v.para(honestyLine)
	v.blank()
	v.next("report of the copy: " + passPath)
	v.next("report of the shop: " + failPath)
	if p.f.keep {
		v.next("kept the demo's files in " + dir)
	} else {
		v.under("The demo's dumps and reports are deleted now; --keep keeps them.")
	}
	v.next("next: rhythm-roadie plan --from <your dump or ssh://...> --to <staging>")
	if p.f.g.json {
		if err := writeEnvelope(stdout, "demo", map[string]any{
			"copy": cr.Summary(), "verify_copy": pass.Summary(), "verify_shop": fail.Summary(),
			"verdicts": []string{word, pw, fw}, "kept": p.f.keep,
		}); err != nil {
			return err
		}
	}
	if word != "PASS" || pw != "PASS" || fw != "FAIL" {
		return &cli.ExitError{Code: cli.ExitFindings}
	}
	return nil
}

// demoDir makes the demo's 0700 folder in Roadie's cache.
func demoDir() (string, error) {
	base, err := config.CacheDir("roadie")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", err
	}
	return os.MkdirTemp(base, "demo-")
}

// writeDemoShop writes the built-in shop's dump, 0600. Every customer in
// it is a fake made from the fixed seed.
func writeDemoShop(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) // #nosec G304 G703 -- a file in the demo's own folder
	if err != nil {
		return err
	}
	if err := dep.demoShop(f, demo.Options{Seed: demoSeed, Customers: demoCustomers, Leaky: true}); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// demoVerify runs a standalone verify of one dump.
func demoVerify(ctx context.Context, target core.Endpoint, opts engine.Options) *core.Result {
	started := dep.now()
	r := &core.Result{Kind: "verify", Started: started, Target: core.EndpointInfo{Kind: target.Kind, Display: target.Display},
		Production: opts.Production, FailOn: opts.FailOn, NotCovered: []string{notCovered}}
	j, err := dep.verifyJob(ctx, engine.Job{To: target, Opts: opts, Clock: dep.now}, target, nil)
	if err == nil {
		j.Production = dedupe(append(j.Production, opts.Production...))
		j.Opts.FailOn, j.Opts.Clock, j.Opts.Jobs = opts.FailOn, dep.now, opts.Jobs
		r.Verify, err = dep.verify(ctx, j)
	}
	if err != nil {
		r.Err = verifyWording(asCoreError(err))
	}
	r.Duration = dep.now().Sub(started)
	return r
}

// step prints one stage of the demo with its box.
func (v *view) step(name, word, line, countLine string) {
	v.section(name, -1, "", muted.GetForeground())
	v.box(word, line, countLine, verdictColor(word))
}

// demoStop shows why the demo stopped and exits with its code. With
// --json there is no screen, so kit prints the message on stderr.
func demoStop(v *view, err error) error {
	ce := asCoreError(err)
	if v.w == io.Discard {
		return &cli.ExitError{Code: ce.Exit, Err: ce}
	}
	v.box("STOPPED", "the demo couldn't run", "", sevColor(core.SeverityCritical))
	v.para(ce.Msg)
	return &cli.ExitError{Code: ce.Exit}
}
