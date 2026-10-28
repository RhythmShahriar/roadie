// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/RhythmShahriar/rhythm/kit/cli"
	"github.com/RhythmShahriar/rhythm/kit/ui"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/engine"
	"github.com/RhythmShahriar/roadie/roadiecfg"
	"github.com/RhythmShahriar/roadie/verify"
)

// The PRD 16.1 mode lines.
const (
	modeExact     = "exact: every value Roadie replaced was checked"
	modeHeuristic = "heuristic: this copy wasn't made by Roadie in this run"
)

// runVerify is verify [staging] (PRD 6.6): it builds the verify job with
// the engine (schema, profiles, rules and kill-switch checks from the
// target; with --from, production's values for exact mode), runs it,
// saves the signed report and shows the product.md 5.4 screen.
func runVerify(ctx context.Context, p *parsed, stdout, stderr io.Writer) error {
	cfg, cfgPath, err := p.loadConfig()
	if err != nil {
		return runError(err)
	}
	vj, ej, err := p.verifyJob(cfg, stderr)
	if err != nil {
		return err
	}
	out := newOutputs(p, stdout, stderr, cfg, cfgPath, true)
	vo := setupColor(&p.f.g, dep.stdoutTerminal(), dep.env)
	v := &view{w: io.Discard, o: vo}
	if out.live() {
		v.w = stdout
	}
	v.header("verify · " + ej.To.Display)

	started := dep.now()
	r := &core.Result{
		Kind: "verify", Started: started,
		Target:     core.EndpointInfo{Kind: ej.To.Kind, Display: ej.To.Display},
		Production: vj.prod, FailOn: ej.Opts.FailOn, NotCovered: []string{notCovered},
	}
	if vj.from != nil {
		r.Source = core.EndpointInfo{Kind: vj.from.Kind, Display: vj.from.Display}
	}
	job, err := dep.verifyJob(ctx, ej, ej.To, vj.from)
	if err == nil && job.Source != nil {
		// A copy read from stdin is spooled; closing its source removes
		// the spool, whatever verify found.
		defer func() { _ = job.Source.Close() }()
	}
	if err == nil {
		job.Opts = vj.opts
		job.Production = dedupe(append(job.Production, vj.prod...))
		bar := newProgress(io.Discard, false, true, dep.now)
		if out.live() {
			bar = newProgress(stdout, dep.stdoutTerminal() && !vo.plain, vo.quiet, dep.now)
		}
		job.Progress = bar
		r.Stack = job.Stack
		// Which rules, config and pack judged the copy, as a copy's
		// report says: the signature covers them.
		r.RulesetSHA256, r.ConfigSHA256, r.ProfilePack = job.RulesetSHA256, job.ConfigSHA256, job.ProfilePack
		if ej.Opts.URL == "" {
			// The screen names a site by the host it calls itself, as
			// copy and prepare do; a dump keeps its file name.
			ej.Opts.URL = job.URL
		}
		r.Verify, err = dep.verify(ctx, job)
		bar.Done()
		if r.Verify != nil {
			r.Input = r.Verify.Input
		}
	}
	r.Duration = dep.now().Sub(started)
	if err != nil {
		r.Err = verifyWording(asCoreError(err))
	}

	reportPath, werr := out.write(r)
	if out.live() {
		if r.Err != nil {
			v.stopView(r, engine.Job{Mode: "verify", To: ej.To})
		} else {
			v.verifyView(r, ej, vj.from != nil, reportPath)
		}
	}
	code := core.ExitFor(r, ej.Opts.FailOn)
	if werr != nil {
		fmt.Fprintf(stderr, "%s verify: %v\n", prog(), werr)
		if code == cli.ExitOK {
			code = cli.ExitUnavailable
		}
	}
	out.open(reportPath)
	switch {
	case code == cli.ExitOK:
		return nil
	case r.Err != nil && !out.live():
		return &cli.ExitError{Code: code, Err: r.Err}
	}
	return &cli.ExitError{Code: code}
}

// verifyWording words a stop for verify: verify only reads, so a message
// written for copy (the dump readers' "the target is untouched") loses
// that ending.
func verifyWording(e *core.Error) *core.Error {
	if e == nil {
		return nil
	}
	msg := strings.ReplaceAll(e.Msg, "; the target is untouched.", ".")
	msg = strings.ReplaceAll(msg, " The target is untouched.", "")
	if msg == e.Msg {
		return e
	}
	c := *e
	c.Msg = msg
	return &c
}

// verifyParts is what verifyJob gathers from the command line.
type verifyParts struct {
	opts verify.Options
	prod []string
	from *core.Endpoint
}

// verifyJob parses the staging and --from and builds the engine job that
// engine.VerifyJob turns into the verify job, with verify's options.
func (p *parsed) verifyJob(cfg *roadiecfg.Config, stderr io.Writer) (verifyParts, engine.Job, error) {
	var parts verifyParts
	target := ""
	if cfg != nil {
		target = cfg.Target
	}
	switch len(p.args) {
	case 0:
	case 1:
		target = p.args[0]
	default:
		return parts, engine.Job{}, usageError(stderr, p.cmd, "one staging at a time; got %d", len(p.args))
	}
	if target == "" {
		return parts, engine.Job{}, usageError(stderr, p.cmd,
			"verify needs the staging copy: a site path, ssh://..., @alias, DSN or dump file (or target: in .roadie/config.yaml)")
	}
	warned := map[string]bool{}
	warn := func(msg string) {
		if !warned[msg] {
			warned[msg] = true
			fmt.Fprintf(stderr, "%s %s: %s\n", prog(), p.cmd, msg)
		}
	}
	to, err := parseEndpoint(target, "the staging", "ROADIE_TO_PASSWORD", warn)
	if err != nil {
		return parts, engine.Job{}, endpointError(stderr, p.cmd, err)
	}
	if p.f.from != "" {
		from, err := parseEndpoint(p.f.from, "--from", "ROADIE_FROM_PASSWORD", warn)
		if err != nil {
			return parts, engine.Job{}, endpointError(stderr, p.cmd, err)
		}
		parts.from = &from
	}
	vo, prod, err := p.verifyOptions(cfg, dep.env, dep.now())
	if err != nil {
		return parts, engine.Job{}, runError(err)
	}
	vo.Clock = dep.now
	// A dump that compresses better than 20 times, or holds one value
	// over 256 MiB, is verified as copy reads it: with the same flags.
	vo.MaxValue, vo.MaxRatio = defaultMaxValue, defaultMaxOutputRatio
	if p.set["max-value"] {
		vo.MaxValue, _ = parseSize(p.f.maxValue) // checked by parse
	}
	if p.set["max-output-ratio"] {
		vo.MaxRatio = p.f.maxOutputRatio
	}
	parts.opts, parts.prod = vo, prod
	ej := engine.Job{
		To: to, Config: cfg, Clock: dep.now,
		Opts: engine.Options{Production: prod, Offline: p.offline(dep.env), FailOn: vo.FailOn,
			MaxValue: vo.MaxValue, MaxOutputRatio: vo.MaxRatio},
	}
	if parts.from != nil {
		ej.From = *parts.from
	}
	return parts, ej, nil
}

// verifyView is verify's screen (product.md 5.4).
func (v *view) verifyView(r *core.Result, j engine.Job, exact bool, reportPath string) {
	vr := r.Verify
	mode := modeHeuristic
	if exact || vr.Mode == "exact" {
		mode = modeExact
	}
	v.setting("Mode", mode)
	what := "every row"
	if vr.Coverage.Sample > 0 {
		what = fmt.Sprintf("a sample of %s rows per table", num(int64(vr.Coverage.Sample)))
	}
	v.setting("Database", fmt.Sprintf("%s, %s of %s, %s", j.To.Display,
		plural(int64(vr.Coverage.TablesScanned), "table", "tables"), num(int64(vr.Coverage.TablesTotal)), what))
	if len(r.Production) > 0 {
		v.setting("Production", strings.Join(r.Production, ", "))
	}
	for _, s := range vr.Coverage.Skipped {
		v.setting("Skipped", s.Table+": "+s.Reason)
	}
	if vr.Hidden > 0 {
		v.setting("Hidden", plural(vr.Hidden, "finding", "findings")+" by --baseline or ignore entries")
	}
	if len(vr.Checks) > 0 {
		v.section("CHECKS", len(vr.Checks), "", ui.Signal)
		v.checkLines(vr.Checks)
	}
	v.findingGroups(r.AllFindings())
	if sc := vr.Soundcheck; sc != nil && sc.Note != "" {
		v.setting("Soundcheck", sc.Note)
	}
	name := shortName(j.Opts.URL, r.Target)
	if r.Target.Kind == core.Stdio {
		name = "stdin" // verify - reads the copy from stdin
	}
	word, line, color := verdict(r, r.FailOn, name)
	v.box(word, line, counts(r.Summary()), color)
	v.underBox(r, nil)
	v.para(honestyLine)
	v.para(notCovered)
	v.blank()
	failed := word == "FAIL"
	if failed && isSite(j.To.Kind) {
		v.next("rhythm-roadie prepare " + j.To.Display + " fixes what it can of these")
	}
	if failed {
		if fs := r.AllFindings(); len(fs) > 0 {
			v.next("rhythm-roadie explain " + fs[0].ID)
		}
	}
	if reportPath != "" {
		v.next("signed report: " + reportPath)
	}
}
