// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"bytes"
	_ "embed"
	"fmt"
	"html/template"
	"strings"

	"github.com/RhythmShahriar/rhythm/kit/term"

	"github.com/RhythmShahriar/roadie/core"
)

//go:embed html.tmpl
var htmlSource string

// htmlTemplate is parsed once. html/template escapes every value for
// where it lands; each one went through term.SafeText first, so a table
// name or a comment shows as text and never as markup (security.md M3).
var htmlTemplate = template.Must(template.New("report").Parse(htmlSource))

// htmlPage is what the template renders: one report, or several with a
// summary table on top.
type htmlPage struct {
	Title   string
	Brand   string
	Logo    template.URL // a data: URI from LogoDataURI, never a remote URL
	Many    bool
	Reports []htmlReport
}

// htmlReport is one envelope's sections, ready for the template.
type htmlReport struct {
	Anchor      string
	Kind        string
	Word, Line  string
	Class       string // pass, fail, stop, plan or done
	Counts      string
	Started     string
	KeyShort    string
	Target      string
	Error       string
	Card        []htmlPair
	Checks      []htmlCheck
	Groups      []htmlGroup
	Uncovered   []string
	Tables      []htmlTable
	Keys        []htmlKey
	Effects     []htmlEffect
	NotStopped  string
	NotCovered  string
	Footer      []string
	Version     string
	Pack        string
	Signature   *Signature
	ViewNote    string
	FindingsAny bool
}

type htmlPair struct{ Key, Value string }

type htmlCheck struct{ Label, ID, Status, Result string }

type htmlGroup struct {
	Severity string
	Findings []htmlFinding
}

type htmlFinding struct {
	ID, Title, Where, Examples, Fix, Detail string
}

type htmlTable struct{ Name, Rows, Action, Profile, What string }

type htmlKey struct{ Provider, Env, Severity, Where, Masked, Action string }

type htmlEffect struct{ Label, Status, Class, How, Undo string }

// HTML renders one or more envelopes as one self-contained page (PRD
// 16.3): html/template only, no scripts, no external assets and a CSP
// that allows nothing else. Several envelopes make one page, worst first,
// with a summary table on top. brand puts a name and a logo (PNG, SVG or
// JPEG under 512 KiB, embedded as a data URI) at the top.
func HTML(e []*core.Envelope, brand Brand) ([]byte, error) {
	rs, err := readReports(e)
	if err != nil {
		return nil, err
	}
	page := htmlPage{Title: "Roadie report", Brand: term.SafeText(brand.Name), Many: len(rs) > 1}
	if len(brand.Logo) > 0 {
		uri, err := LogoDataURI(brand.Logo)
		if err != nil {
			return nil, err
		}
		page.Logo = template.URL(uri) // #nosec G203 -- built by LogoDataURI from a checked PNG, JPEG or rebuilt SVG
	}
	for i, r := range rs {
		page.Reports = append(page.Reports, r.htmlReport(i+1))
	}
	if !page.Many {
		page.Title = "Roadie: " + page.Reports[0].Word + ", " + page.Reports[0].Line
	}
	var b bytes.Buffer
	if err := htmlTemplate.Execute(&b, page); err != nil {
		return nil, fmt.Errorf("report: %w", err)
	}
	return b.Bytes(), nil
}

// safe is term.SafeText, short for the many fields below.
func safe(s string) string { return term.SafeText(s) }

func (r *report) htmlReport(n int) htmlReport {
	d := r.data
	word, line := r.verdict()
	h := htmlReport{
		Anchor:     fmt.Sprintf("r%d", n),
		Kind:       safe(r.env.Kind),
		Word:       word,
		Line:       safe(line),
		Class:      verdictClass(word),
		Counts:     counts(r.env.Summary),
		Started:    safe(d.Started),
		Target:     safe(r.name()),
		Version:    safe(r.env.Version),
		Pack:       safe(orDash(d.ProfilePack)),
		Signature:  d.Signature,
		NotStopped: KillSwitchLimits,
		NotCovered: safe(NotCoveredLine(d.NotCovered)),
		ViewNote:   ViewNote,
	}
	if d.Error != nil {
		h.Error = safe(d.Error.Msg)
	}
	if id := r.keyID(); id != "" {
		h.KeyShort = safe(ShortKeyID(id))
	}
	// One paragraph: the lines break where PRD 19.1 breaks them for text.
	h.Footer = []string{strings.Join(footerLines(h.KeyShort), " ")}
	pair := func(k, v string) {
		if v != "" {
			h.Card = append(h.Card, htmlPair{k, safe(v)})
		}
	}
	pair("Source", d.Source.Display)
	pair("Target", d.Target.Display)
	pair("Copy", d.URL)
	pair("Production", strings.Join(d.Production, ", "))
	pair("Stack and profiles", r.stackLine())
	pair("Verify", r.coverageLine())
	if c := d.Coverage; c != nil {
		for _, s := range c.Skipped {
			pair("Skipped", s.Table+": "+s.Reason)
		}
	}
	pair("Consistency", consistencyText(d.Consistency))
	if in := d.Input; in != nil {
		pair("Input", in.Kind+" "+in.Value+" ("+size(in.Bytes)+")")
	}
	if out := d.Output; out != nil {
		pair("Output", out.Kind+" "+out.Value+" ("+size(out.Bytes)+")")
	}
	pair("Fail on", d.FailOn)
	if s := d.Soundcheck; s != nil && s.Ran {
		pair("Soundcheck", fmt.Sprintf("exit %d (%s)", s.ExitCode, s.Schema))
	}
	for _, c := range d.Checks {
		res := checkResult(c)
		if c.Status == "fail" || c.Status == "warn" {
			var titles []string
			for _, f := range r.checkFindings(c.ID) {
				titles = append(titles, findingTitle(f))
			}
			if len(titles) > 0 {
				res = strings.Join(titles, "; ")
			}
		}
		h.Checks = append(h.Checks, htmlCheck{Label: safe(CheckTitle(c.ID, c.Status)), ID: safe(c.ID), Status: safe(c.Status), Result: safe(res)})
	}
	for _, s := range severities {
		fs := r.bySeverity(s)
		if len(fs) == 0 {
			continue
		}
		g := htmlGroup{Severity: s.String()}
		for _, f := range fs {
			g.Findings = append(g.Findings, htmlFinding{
				ID: safe(f.ID), Title: safe(findingTitle(f)), Where: safe(locationLine(f)),
				Examples: safe(strings.Join(f.Examples, ", ")), Fix: safe(f.Fix), Detail: safe(f.Detail),
			})
		}
		h.Groups = append(h.Groups, g)
	}
	for _, u := range d.Uncovered {
		h.Uncovered = append(h.Uncovered, safe(fmt.Sprintf("%s.%s looks personal (%d%% of sampled values) and no rule covers it", u.Table, u.Column, u.Rate)))
	}
	h.FindingsAny = len(h.Groups)+len(h.Uncovered) > 0
	for _, t := range r.changedTables() {
		h.Tables = append(h.Tables, htmlTable{Name: safe(t.Name), Rows: num(t.Rows), Action: safe(t.Action), Profile: safe(t.Profile), What: safe(tableWhat(t))})
	}
	for _, k := range d.Keys {
		h.Keys = append(h.Keys, htmlKey{Provider: safe(k.Provider), Env: safe(k.Env), Severity: safe(k.Severity),
			Where: safe(k.Location.String()), Masked: safe(k.Masked), Action: safe(k.Action)})
	}
	for _, s := range d.SideEffects {
		class := "muted"
		switch s.Status {
		case "off":
			class = "ok"
		case "on":
			class = "bad"
		}
		h.Effects = append(h.Effects, htmlEffect{Label: safe(EffectLabel(s.ID, false)), Status: effectStatus(safe(s.Status)), Class: class,
			How: safe(s.How), Undo: safe(s.Undo)})
	}
	return h
}

func verdictClass(word string) string {
	switch word {
	case "FAIL":
		return "fail"
	case "STOPPED":
		return "stop"
	case "PLAN":
		return "plan"
	case "DONE":
		return "done"
	}
	return "pass"
}

// consistencyText says how fakes were keyed.
func consistencyText(c ConsistencyData) string {
	switch {
	case c.KeyFingerprint != nil:
		return "stable key " + *c.KeyFingerprint + ": the same fake for each real person on every refresh"
	case c.Mode != "":
		return "per-run key: each real person gets one fake, across every table"
	}
	return ""
}
