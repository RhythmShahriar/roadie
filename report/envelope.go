// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/RhythmShahriar/rhythm/kit/cli"

	"github.com/RhythmShahriar/roadie/core"
)

// The verdicts of an envelope (Echo's).
const (
	VerdictOK       = "ok"       // nothing at low or above
	VerdictFindings = "findings" // findings; --fail-on decides whether they fail the run
	VerdictError    = "error"    // the run stopped or couldn't check
)

// The values of data.result.
const (
	ResultPass  = "pass"
	ResultFail  = "fail"
	ResultError = "error"
)

// Envelope builds the rhythm-roadie/v1 envelope of a result. Every value
// in r is already masked (core.Mask*), so the envelope copies strings as
// they are; BasicAuthPassword, Backup and Restore are left out. complete
// is false when verify read a sample, when part of its input couldn't be
// read, when the run stopped and when it scanned nothing.
func Envelope(r *core.Result, tool, version string) (*core.Envelope, error) {
	if r == nil {
		return nil, errors.New("report: no result")
	}
	d := buildData(r)
	data, err := marshal(d)
	if err != nil {
		return nil, fmt.Errorf("report: %w", err)
	}
	e := &core.Envelope{
		Schema:   core.EnvelopeSchema,
		Kind:     r.Kind,
		Tool:     tool,
		Version:  version,
		Complete: d.Result != ResultError && (r.Verify == nil || r.Verify.Coverage.Sample == 0 && !r.Verify.Coverage.Unread()),
		Summary:  r.Summary(),
		Verdict:  VerdictOK,
		Data:     data,
	}
	switch {
	case d.Result == ResultError:
		e.Verdict = VerdictError
	case slices.ContainsFunc(r.AllFindings(), func(f core.Finding) bool { return f.Severity >= core.SeverityLow }):
		e.Verdict = VerdictFindings
	}
	return e, nil
}

// buildData copies a result into the PRD 16.2 data object. Lists are
// never null, so a reader can range over every one of them.
func buildData(r *core.Result) *Data {
	d := &Data{
		Result:              resultOf(r),
		VerifyMode:          "none",
		FailOn:              r.FailOn.String(),
		DurationMS:          r.Duration.Milliseconds(),
		Source:              endpointData(r.Source),
		Target:              TargetData{EndpointData: endpointData(r.Target), State: string(cmp.Or(r.Target.State, r.TargetState))},
		TargetState:         string(r.TargetState),
		Production:          nonNil(r.Production),
		URL:                 r.URL,
		Stack:               StackData{Name: string(r.Stack.Name), Profiles: nonNil(r.Stack.Profiles), Multisite: r.Stack.Multisite, HPOS: r.Stack.HPOS, Sites: r.Stack.Sites},
		Consistency:         ConsistencyData{Mode: r.Consistency.Mode},
		Input:               fingerprint(r.Input),
		Output:              fingerprint(r.Output),
		RulesetSHA256:       r.RulesetSHA256,
		ConfigSHA256:        r.ConfigSHA256,
		ProfilePack:         r.ProfilePack,
		EchoConnectorSHA256: r.EchoConnectorSHA256,
		Tables:              []TableData{},
		Covered:             rates(r.Covered),
		Uncovered:           rates(r.Uncovered),
		Keys:                []KeyData{},
		SideEffects:         []SideEffect{},
		Findings:            nonNil(r.AllFindings()),
		Checks:              []CheckData{},
		RemoteCommands:      nonNil(r.RemoteCommands),
		NotCovered:          notCoveredList(r.NotCovered),
		Error:               r.Err,
	}
	if !r.Started.IsZero() {
		d.Started = r.Started.UTC().Format(time.RFC3339)
	}
	if fp := r.Consistency.KeyFingerprint; fp != "" {
		d.Consistency.KeyFingerprint = &fp
	}
	for _, t := range r.Tables {
		td := TableData{Name: t.Name, Rows: t.Rows, Action: t.Action, Profile: t.Profile, Columns: []ColumnData{}}
		for _, c := range t.Columns {
			td.Columns = append(td.Columns, ColumnData{Name: c.Name, Rule: c.Rule, Changed: c.Changed, Kept: c.Kept})
		}
		d.Tables = append(d.Tables, td)
	}
	// Keys and side effects come from the run, or from verify when the
	// run had none (a standalone verify), as Result.Summary counts them.
	keys, effects := r.Keys, r.SideEffects
	if v := r.Verify; v != nil {
		if len(keys) == 0 {
			keys = v.Keys
		}
		if len(effects) == 0 {
			effects = v.SideEffects
		}
	}
	for _, k := range keys {
		d.Keys = append(d.Keys, KeyData{ID: k.ID, Severity: k.Severity.String(), Provider: k.Provider, Env: k.Env,
			Location: k.At, Masked: k.Masked, Action: k.Action})
	}
	for _, e := range effects {
		d.SideEffects = append(d.SideEffects, SideEffect(e))
	}
	if v := r.Verify; v != nil {
		d.VerifyMode = v.Mode
		if d.VerifyMode == "" {
			d.VerifyMode = "heuristic"
		}
		for _, c := range v.Checks {
			d.Checks = append(d.Checks, CheckData(c))
		}
		c := v.Coverage
		d.Coverage = &CoverageData{TablesScanned: c.TablesScanned, TablesTotal: c.TablesTotal, RowsScanned: c.RowsScanned,
			BytesScanned: c.BytesScanned, Skipped: []SkippedData{}, Sample: c.Sample}
		for _, s := range c.Skipped {
			d.Coverage.Skipped = append(d.Coverage.Skipped, SkippedData(s))
		}
		seen := map[[2]string]bool{}
		for _, u := range d.Uncovered {
			seen[[2]string{u.Table, u.Column}] = true
		}
		for _, u := range v.Uncovered {
			if !seen[[2]string{u.Table, u.Column}] {
				d.Uncovered = append(d.Uncovered, ColumnRate(u))
			}
		}
		if s := v.Soundcheck; s != nil {
			counts := s.Counts
			if counts == nil {
				counts = map[string]int64{}
			}
			d.Soundcheck = &SoundcheckData{Ran: s.Ran, ExitCode: s.ExitCode, Schema: s.Schema, Counts: counts, Note: s.Note}
		}
	}
	return d
}

// resultOf is data.result: error when the run stopped or couldn't check
// (exit 2 or 3), fail when a finding is at or above --fail-on, else pass.
func resultOf(r *core.Result) string {
	if r.Err != nil {
		return ResultError
	}
	switch core.ExitFor(r, r.FailOn) {
	case cli.ExitOK:
		return ResultPass
	case cli.ExitFindings:
		return ResultFail
	}
	return ResultError
}

func endpointData(e core.EndpointInfo) EndpointData {
	return EndpointData{Kind: string(e.Kind), Display: e.Display, Engine: e.Engine, Server: e.Server, Bytes: e.Bytes}
}

func fingerprint(f *core.Fingerprint) *Fingerprint {
	if f == nil {
		return nil
	}
	return &Fingerprint{Kind: f.Kind, Value: f.Value, Bytes: f.Bytes}
}

func rates(us []core.Uncovered) []ColumnRate {
	out := make([]ColumnRate, 0, len(us))
	for _, u := range us {
		out = append(out, ColumnRate(u))
	}
	return out
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// notCoveredList is data.not_covered: the PRD 19.1 list, then anything
// the run added. The whole sentence (as the CLI passes it) is the list
// itself, so it isn't repeated.
func notCoveredList(extra []string) []string {
	out := slices.Clone(notCoveredItems)
	for _, s := range extra {
		if s != "" && s != NotCoveredSentence && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

// marshal writes v as JSON without HTML escaping, so "<" in a table name
// stays "<" in the signed bytes and every view escapes it for its own
// format.
func marshal(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

// encodeEnvelope writes an envelope as report.json and --json do:
// indented two spaces, no HTML escaping, a newline at the end. Indenting
// doesn't change what the signature covers: that is the canonical form.
func encodeEnvelope(e *core.Envelope) ([]byte, error) { return marshalIndent(e) }

// ParseData reads an envelope's data object back. An envelope of another
// kind (profiles, explain) reads as mostly empty data.
func ParseData(e *core.Envelope) (*Data, error) {
	if e == nil {
		return nil, errors.New("report: no envelope")
	}
	d := &Data{}
	if len(e.Data) == 0 {
		return d, nil
	}
	if err := json.Unmarshal(e.Data, d); err != nil {
		return nil, fmt.Errorf("report: the data object doesn't read: %w", err)
	}
	return d, nil
}

// ReadEnvelope reads a saved report.json. It must be a rhythm-roadie/v1
// envelope; the signature is report check's business.
func ReadEnvelope(b []byte) (*core.Envelope, error) {
	var e core.Envelope
	if err := json.Unmarshal(b, &e); err != nil {
		return nil, fmt.Errorf("isn't a Roadie report: %w", err)
	}
	if e.Schema != core.EnvelopeSchema {
		return nil, fmt.Errorf("isn't a Roadie report (schema %q, want %s)", e.Schema, core.EnvelopeSchema)
	}
	return &e, nil
}
