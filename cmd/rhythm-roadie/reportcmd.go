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
	"os"
	"path/filepath"
	"strings"

	"github.com/RhythmShahriar/rhythm/kit/cache"
	"github.com/RhythmShahriar/rhythm/kit/cli"
	"github.com/RhythmShahriar/rhythm/kit/term"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/report"
)

// maxReportFile is the largest report.json report reads. A report holds
// counts and at most three masked examples per finding, so even a large
// site's is a few MB.
const maxReportFile = 64 << 20

// runReport renders saved reports (PRD 6.9): one or more report.json
// files to -o files by extension, or to stdout with --html, --md or
// --json. A report whose signature fails is not rendered: a view of an
// edited report would look like Roadie's. report check checks one.
func runReport(ctx context.Context, p *parsed, stdout, stderr io.Writer) error {
	if len(p.args) > 0 && p.args[0] == "check" {
		return runReportCheck(ctx, p, stdout, stderr)
	}
	g := &p.f.g
	switch {
	case p.given("input"):
		return usageError(stderr, p.cmd, "--input is for report check")
	case len(p.args) == 0:
		return usageError(stderr, p.cmd, "name the report.json files to render, or check one with rhythm-roadie report check <report.json>")
	case len(g.outputs) == 0 && !g.json && !g.md && !g.html:
		return usageError(stderr, p.cmd, "say where the view goes: -o <file> (.html .md .sarif .xml .json), or --html, --md or --json for stdout")
	}
	pinned, err := readPinned(p.f.key)
	if err != nil {
		return usageError(stderr, p.cmd, "%v", err)
	}
	var envs []*core.Envelope
	for _, path := range p.args {
		b, err := readLimited(path, maxReportFile)
		if err != nil {
			fmt.Fprintf(stderr, "%s report: %v\n", prog(), err)
			return &cli.ExitError{Code: cli.ExitUnavailable}
		}
		name := filepath.Base(path)
		if _, err := report.CheckReport(name, b, pinned, nil); err != nil {
			var ce *core.Error
			if !errors.As(err, &ce) || ce.Code != core.ErrReportUnsigned {
				return reportCheckError(stderr, err)
			}
			// An unsigned report renders; its footer says it isn't signed.
		}
		e, err := report.ReadEnvelope(b)
		if err != nil {
			fmt.Fprintf(stderr, "%s report: %s %v\n", prog(), term.SafeText(name), err)
			return &cli.ExitError{Code: cli.ExitFindings}
		}
		envs = append(envs, e)
	}
	brand := report.Brand{Name: p.f.brandName}
	if p.f.brandLogo != "" {
		if brand.Logo, err = readLimited(p.f.brandLogo, report.MaxLogoSize); err != nil {
			return usageError(stderr, p.cmd, "--brand-logo: %v", err)
		}
		if _, err := report.LogoDataURI(brand.Logo); err != nil {
			return usageError(stderr, p.cmd, "--brand-logo %s: %v", term.SafeText(p.f.brandLogo), err)
		}
	}
	type target struct{ path, format string }
	var targets []target
	for _, f := range []struct {
		on     bool
		format string
	}{{g.json, "json"}, {g.md, "md"}, {g.html, "html"}} {
		if f.on {
			targets = append(targets, target{"", f.format})
		}
	}
	for _, o := range g.outputs {
		format, _ := formatOf(o) // checked by parse
		targets = append(targets, target{o, format})
	}
	// Render everything first, so a bad format writes no file at all.
	outs := make([][]byte, len(targets))
	for i, t := range targets {
		b, err := renderMany(envs, t.format, brand)
		if err != nil {
			if t.path != "" {
				return usageError(stderr, p.cmd, "-o %s: %v", t.path, err)
			}
			return usageError(stderr, p.cmd, "%v", err)
		}
		outs[i] = b
	}
	for i, t := range targets {
		if t.path == "" {
			if _, err := stdout.Write(outs[i]); err != nil {
				return err
			}
			continue
		}
		if err := cache.WriteFile(t.path, outs[i]); err != nil {
			fmt.Fprintf(stderr, "%s report: -o %s: %v\n", prog(), t.path, err)
			return &cli.ExitError{Code: cli.ExitUnavailable}
		}
		if !g.quiet && !g.json && !g.md && !g.html {
			fmt.Fprintf(stdout, "Wrote %s (%s).\n", term.SafeText(t.path), plural(int64(len(envs)), "report", "reports"))
		}
	}
	return nil
}

// errOneReport is a format that holds one report given several.
var errOneReport = errors.New("this format holds one report; .html and .md make one view of several")

// renderMany writes envs in one format: HTML and Markdown put several on
// one page, worst first; JSON, SARIF and JUnit take one report.
func renderMany(envs []*core.Envelope, format string, brand report.Brand) ([]byte, error) {
	switch format {
	case "html":
		return dep.html(envs, brand)
	case "md":
		if len(envs) > 1 {
			return report.MarkdownAll(envs)
		}
	}
	if len(envs) > 1 {
		return nil, errOneReport
	}
	return render(envs[0], format)
}

// runReportCheck is report check <report.json> [--key k] [--input dump].
func runReportCheck(_ context.Context, p *parsed, stdout, stderr io.Writer) error {
	const cmd = "report check"
	switch {
	case len(p.args) != 2:
		return usageError(stderr, cmd, "check one report at a time: rhythm-roadie report check <report.json>")
	case len(p.f.g.outputs) > 0 || p.given("brand-name", "brand-logo"):
		return usageError(stderr, cmd, "report check prints its answer; -o and --brand-* are for rendering")
	}
	path := p.args[1]
	b, err := readLimited(path, maxReportFile)
	if err != nil {
		fmt.Fprintf(stderr, "%s %s: %v\n", prog(), cmd, err)
		return &cli.ExitError{Code: cli.ExitUnavailable}
	}
	pinned, err := readPinned(p.f.key)
	if err != nil {
		return usageError(stderr, cmd, "%v", err)
	}
	var input io.Reader
	if p.f.input != "" {
		f, err := os.Open(p.f.input)
		if err != nil {
			return usageError(stderr, cmd, "--input: %v", err)
		}
		defer func() { _ = f.Close() }()
		input = f
	}
	keyID, err := report.CheckReport(filepath.Base(path), b, pinned, input)
	if p.f.g.json {
		if jerr := writeCheckEnvelope(stdout, keyID, p.f.input != "", err); jerr != nil {
			return jerr
		}
		if err != nil {
			return &cli.ExitError{Code: exitOf(err)}
		}
		return nil
	}
	if err != nil {
		return reportCheckError(stderr, err)
	}
	fmt.Fprintln(stdout, report.CheckedLine(keyID))
	if input != nil {
		fmt.Fprintf(stdout, "%s is the input it describes (the sha256 matches).\n", term.SafeText(filepath.Base(p.f.input)))
	}
	return nil
}

// reportCheckError prints a check's message and returns its exit code.
func reportCheckError(stderr io.Writer, err error) error {
	fmt.Fprintf(stderr, "%s %s\n", prog(), err.Error())
	return &cli.ExitError{Code: exitOf(err)}
}

// exitOf is a check error's exit code: its own, or 3 when it couldn't run.
func exitOf(err error) int {
	var ce *core.Error
	if errors.As(err, &ce) {
		return ce.Exit
	}
	return cli.ExitUnavailable
}

// checkJSON is the data of report check --json.
type checkJSON struct {
	Result       string      `json:"result"` // valid, edited, unsigned, error
	KeyID        string      `json:"key_id"`
	InputChecked bool        `json:"input_checked"`
	Error        *core.Error `json:"error,omitempty"`
}

// writeCheckEnvelope writes report check --json: kind report-check.
func writeCheckEnvelope(w io.Writer, keyID string, input bool, err error) error {
	d := checkJSON{Result: "valid", KeyID: keyID, InputChecked: input && err == nil}
	verdict := "ok"
	if err != nil {
		var ce *core.Error
		if !errors.As(err, &ce) {
			ce = &core.Error{Exit: cli.ExitUnavailable, Msg: err.Error()}
		}
		d.Error, verdict = ce, "error"
		switch ce.Code {
		case core.ErrReportEdited:
			d.Result, verdict = "edited", "findings"
		case core.ErrReportUnsigned:
			d.Result = "unsigned"
		default:
			d.Result = "error"
		}
	}
	data, merr := json.Marshal(d)
	if merr != nil {
		return merr
	}
	e := core.Envelope{Schema: core.EnvelopeSchema, Kind: "report-check", Tool: toolName, Version: displayVersion(),
		Complete: err == nil, Verdict: verdict, Data: data}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(e)
}

// readPinned reads --key for report and report check: a key ID as it is
// ("roadie:" and 16 characters), else the key file.
func readPinned(key string) ([]byte, error) {
	if key == "" {
		return nil, nil
	}
	if strings.HasPrefix(key, "roadie:") {
		return []byte(key), nil
	}
	b, err := readLimited(key, 64<<10)
	if err != nil {
		return nil, fmt.Errorf("--key: %w", err)
	}
	return b, nil
}

// readLimited reads a file the user named, refusing one over limit bytes.
func readLimited(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path) // #nosec G304 -- a file the user named on the command line
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var b bytes.Buffer
	if _, err := b.ReadFrom(io.LimitReader(f, limit+1)); err != nil {
		return nil, err
	}
	if int64(b.Len()) > limit {
		return nil, fmt.Errorf("%s is larger than %s", term.SafeText(path), size(limit))
	}
	return b.Bytes(), nil
}
