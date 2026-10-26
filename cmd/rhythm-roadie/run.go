// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/RhythmShahriar/rhythm/kit/cache"
	"github.com/RhythmShahriar/rhythm/kit/cli"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/engine"
	"github.com/RhythmShahriar/roadie/report"
	"github.com/RhythmShahriar/roadie/roadiecfg"
)

// tool name as reports record it.
const toolName = "rhythm-roadie"

// runCopy, runPrepare and runPlan share one code path (PRD 4.3): the mode
// comes from the command and --dry-run.
func runCopy(ctx context.Context, p *parsed, stdout, stderr io.Writer) error {
	return runEngine(ctx, p, stdout, stderr)
}

func runPrepare(ctx context.Context, p *parsed, stdout, stderr io.Writer) error {
	return runEngine(ctx, p, stdout, stderr)
}

func runPlan(ctx context.Context, p *parsed, stdout, stderr io.Writer) error {
	return runEngine(ctx, p, stdout, stderr)
}

// runEngine builds the job, runs the engine, writes the reports and the
// screen and returns the exit code as an error for kit.
func runEngine(ctx context.Context, p *parsed, stdout, stderr io.Writer) error {
	cfg, cfgPath, err := p.loadConfig()
	if err != nil {
		return runError(err)
	}
	mode := p.mode()
	job, err := p.buildJob(mode, cfg, stderr)
	if err != nil {
		return err
	}
	lr := newLineReader(dep.stdin)
	if mode == engine.Copy && job.Opts.URL == "" {
		const need = "copy needs --url, the staging site's URL (or url: in .roadie/config.yaml)"
		if !dep.terminal() {
			return usageError(stderr, p.cmd, need)
		}
		fmt.Fprint(stderr, "The copy's URL? ")
		url, ok := lr.read(ctx)
		if !ok || url == "" {
			fmt.Fprintln(stderr)
			return usageError(stderr, p.cmd, need)
		}
		job.Opts.URL = url
	}

	out := newOutputs(p, stdout, stderr, cfg, cfgPath, mode != engine.Plan)
	vo := setupColor(&p.f.g, dep.stdoutTerminal(), dep.env)
	v := &view{w: io.Discard, o: vo}
	if out.live() {
		v.w = stdout
	}
	v.before(job)

	// Progress goes with the live view; with a report on stdout it goes
	// nowhere.
	progressW, progressTerm := stdout, dep.stdoutTerminal() && !vo.plain
	if !out.live() {
		progressW, progressTerm = io.Discard, false
	}
	bar := newProgress(progressW, progressTerm, vo.quiet, dep.now)
	job.Progress = bar
	if dep.terminal() {
		job.Confirm = confirmer(ctx, lr, stderr, string(mode), job.To.Display)
	}

	r, err := dep.run(ctx, job)
	bar.Done()
	r = settle(r, err, string(mode), job)

	reportPath, werr := out.write(r)
	if out.live() {
		switch {
		case r.Err != nil:
			v.stopView(r, job)
		case mode == engine.Plan:
			v.planView(r, job, bar.timings())
		default:
			v.resultView(r, job, reportPath, bar.timings())
		}
	}
	if r.BasicAuthPassword != "" {
		w := stdout
		if !out.live() {
			w = stderr // never on stdout next to a report
		}
		(&view{w: w, o: vo}).basicAuthLine(job.Opts.BasicAuth, r.BasicAuthPassword)
	}

	code := core.ExitFor(r, job.Opts.FailOn)
	if mode == engine.Plan && r.Err == nil && job.Opts.FailOn != core.SeverityNone && wouldStop(r, job) {
		code = cli.ExitFindings
	}
	if werr != nil {
		fmt.Fprintf(stderr, "%s %s: %v\n", prog(), p.cmd, werr)
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

// settle makes one result out of what the engine returned: a run error
// becomes Result.Err; a missing result becomes an empty one carrying it.
func settle(r *core.Result, err error, kind string, j engine.Job) *core.Result {
	if r == nil {
		r = &core.Result{Kind: kind, Started: dep.now(), TargetState: core.Untouched,
			Source: core.EndpointInfo{Kind: j.From.Kind, Display: j.From.Display},
			Target: core.EndpointInfo{Kind: j.To.Kind, Display: j.To.Display}}
		if err == nil {
			err = errors.New("the run ended without a result")
		}
	}
	if err != nil && r.Err == nil {
		r.Err = asCoreError(err)
	}
	if r.Err != nil && r.Err.State != "" && r.TargetState == "" {
		r.TargetState = r.Err.State
	}
	if r.FailOn == core.SeverityNone {
		r.FailOn = j.Opts.FailOn
	}
	return r
}

// asCoreError returns err as a *core.Error; anything else couldn't run.
func asCoreError(err error) *core.Error {
	var ce *core.Error
	if errors.As(err, &ce) {
		return ce
	}
	return &core.Error{Exit: cli.ExitUnavailable, Msg: err.Error()}
}

// runError turns an error before the run (a bad config) into kit's exit:
// a *core.Error keeps its exit code; anything else couldn't run.
func runError(err error) error {
	ce := asCoreError(err)
	return &cli.ExitError{Code: ce.Exit, Err: ce}
}

// outputs writes a run's reports: the saved signed report.json and
// report.html, the format asked for on stdout and every -o file.
type outputs struct {
	p              *parsed
	stdout, stderr io.Writer
	format         string // stdout's format: "", json, md, html, sarif or junit
	sign           bool
	keyFile        string
	save           bool   // copy, prepare and verify keep their reports
	dir            string // the project folder: .roadie/ is under it
}

func newOutputs(p *parsed, stdout, stderr io.Writer, cfg *roadiecfg.Config, cfgPath string, save bool) *outputs {
	g := &p.f.g
	o := &outputs{p: p, stdout: stdout, stderr: stderr, save: save, dir: "."}
	for name, on := range map[string]bool{"json": g.json, "md": g.md, "html": g.html, "sarif": g.sarif, "junit": g.junit} {
		if on {
			o.format = name
		}
	}
	o.sign, o.keyFile = p.signing(cfg)
	if cfgPath != "" {
		// .roadie/config.yaml's folder's parent holds .roadie/reports too.
		o.dir = filepath.Dir(filepath.Dir(cfgPath))
	}
	return o
}

// live reports whether the terminal view goes to stdout: it does unless
// stdout carries a report.
func (o *outputs) live() bool { return o.format == "" }

// write builds the envelope, signs it, saves the report folder and writes
// stdout's format and the -o files. It returns the saved report.html's
// path, "" when nothing was saved.
func (o *outputs) write(r *core.Result) (string, error) {
	g := &o.p.f.g
	if !o.save && o.format == "" && len(g.outputs) == 0 {
		return "", nil
	}
	env, err := dep.envelope(r, toolName, displayVersion())
	if err != nil {
		return "", fmt.Errorf("couldn't build the report: %w", err)
	}
	if o.sign {
		s, err := loadReportSigner(o.keyFile, o.stderr)
		if err != nil {
			return "", fmt.Errorf("couldn't sign the report: %w", err)
		}
		if err := s.Sign(env); err != nil {
			return "", fmt.Errorf("couldn't sign the report: %w", err)
		}
	}
	var reportPath string
	if o.save {
		html, err := dep.html([]*core.Envelope{env}, report.Brand{})
		if err != nil {
			return "", fmt.Errorf("couldn't render the report: %w", err)
		}
		dir, err := dep.save(o.dir, env, html)
		if err != nil {
			return "", fmt.Errorf("couldn't save the report: %w", err)
		}
		reportPath = filepath.Join(dir, "report.html")
	}
	if o.format != "" {
		b, err := render(env, o.format)
		if err != nil {
			return reportPath, err
		}
		if _, err := o.stdout.Write(b); err != nil {
			return reportPath, err
		}
	}
	for _, f := range g.outputs {
		format, _ := formatOf(f) // checked by parse
		b, err := render(env, format)
		if err != nil {
			return reportPath, err
		}
		// Replaces an existing file only once the new one is complete
		// (PRD 6.3); 0600, since reports describe production.
		if err := cache.WriteFile(f, b); err != nil {
			return reportPath, fmt.Errorf("-o %s: %w", f, err)
		}
	}
	return reportPath, nil
}

// render writes the envelope in one format.
func render(env *core.Envelope, format string) ([]byte, error) {
	var b []byte
	var err error
	switch format {
	case "json":
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		err = enc.Encode(env)
		b = buf.Bytes()
	case "md":
		b, err = dep.markdown(env)
	case "html":
		b, err = dep.html([]*core.Envelope{env}, report.Brand{})
	case "sarif":
		b, err = dep.sarif(env)
	case "junit":
		b, err = dep.junit(env)
	default:
		err = fmt.Errorf("no %s format", format)
	}
	if err != nil {
		return nil, fmt.Errorf("couldn't write the %s report: %w", format, err)
	}
	return b, nil
}

// open is --open: the HTML report in the system's viewer, only on a
// terminal and never in CI.
func (o *outputs) open(reportPath string) {
	if !o.p.f.g.open || reportPath == "" {
		return
	}
	if ci, _ := dep.env("CI"); ci != "" || !dep.stdoutTerminal() {
		return
	}
	if err := dep.open(reportPath); err != nil {
		fmt.Fprintf(o.stderr, "%s: couldn't open %s: %v\n", prog(), reportPath, err)
	}
}

// openFile opens path with the system's opener.
func openFile(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path) // #nosec G204 -- a report path Roadie wrote
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", path) // #nosec G204 -- as above
	default:
		cmd = exec.Command("xdg-open", path) // #nosec G204 -- as above
	}
	cmd.Stdout, cmd.Stderr = nil, nil
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
