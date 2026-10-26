// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/RhythmShahriar/roadie/core"
)

// Run is one rhythm-roadie process: its exit code, what it printed and
// how long it took. Stdout holds the --json envelope.
type Run struct {
	Args     []string
	Exit     int
	Stdout   []byte
	Stderr   []byte
	Wall     time.Duration
	MaxRSSKB int64 // peak resident memory (host runs on Linux), 0 when unknown
	// Signaled says the harness interrupted or killed the run itself.
	Signaled string
}

// Envelope is the part of Roadie's --json document the harness reads
// (PRD 16.2). Matching is by ID, never by wording.
type Envelope struct {
	Schema   string       `json:"schema"`
	Kind     string       `json:"kind"`
	Version  string       `json:"version"`
	Complete bool         `json:"complete"`
	Verdict  string       `json:"verdict"`
	Summary  core.Summary `json:"summary"`
	Data     EnvelopeData `json:"data"`
}

// EnvelopeData is the envelope's data object.
type EnvelopeData struct {
	Result      string         `json:"result"`
	VerifyMode  string         `json:"verify_mode"`
	TargetState string         `json:"target_state"`
	DurationMS  int64          `json:"duration_ms"`
	Findings    []core.Finding `json:"findings"`
	Keys        []struct {
		ID     string `json:"id"`
		Env    string `json:"env"`
		Action string `json:"action"`
	} `json:"keys"`
	Checks []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"checks"`
	// Tables is what the run did to each table: the gates take a
	// truncated table or deleted rows as meant, not as lost.
	Tables []struct {
		Name    string `json:"name"`
		Rows    int64  `json:"rows"`
		Action  string `json:"action"`
		Columns []struct {
			Changed int64 `json:"changed"`
		} `json:"columns"`
	} `json:"tables"`
	Phases map[string]int64 `json:"phases_ms,omitempty"`
	Error  *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Exit    int    `json:"exit"`
	} `json:"error"`
	Signature json.RawMessage `json:"signature"`
}

// ParseEnvelope reads the envelope from a run's stdout. Roadie prints one
// JSON document; anything before its first { (a stray line) is skipped.
func ParseEnvelope(stdout []byte) (*Envelope, error) {
	i := bytes.IndexByte(stdout, '{')
	if i < 0 {
		return nil, fmt.Errorf("no JSON envelope on stdout (%d bytes)", len(stdout))
	}
	var e Envelope
	dec := json.NewDecoder(bytes.NewReader(stdout[i:]))
	if err := dec.Decode(&e); err != nil {
		return nil, fmt.Errorf("the JSON envelope: %w", err)
	}
	if e.Schema != core.EnvelopeSchema {
		return nil, fmt.Errorf("envelope schema %q, want %s", e.Schema, core.EnvelopeSchema)
	}
	return &e, nil
}

// ErrorCode returns data.error.code, or "".
func (e *Envelope) ErrorCode() string {
	if e == nil || e.Data.Error == nil {
		return ""
	}
	return e.Data.Error.Code
}

// NotBuilt reports whether the run stopped at a frozen API stub another
// lane hasn't replaced yet (core.ErrNotBuilt).
func (e *Envelope) NotBuilt() bool { return e.ErrorCode() == core.ErrCodeNotBuilt }

// IDs returns every ID the run reported, sorted and unique: finding IDs,
// key finding IDs, failed check IDs and the error code.
func (e *Envelope) IDs() []string {
	if e == nil {
		return nil
	}
	set := map[string]bool{}
	for _, f := range e.Data.Findings {
		set[f.ID] = true
	}
	for _, k := range e.Data.Keys {
		if k.ID != "" && k.Env == "live" && k.Action != "swapped" && k.Action != "blanked" {
			set[k.ID] = true
		}
	}
	for _, c := range e.Data.Checks {
		if c.Status == "fail" {
			set[c.ID] = true
		}
	}
	if c := e.ErrorCode(); c != "" {
		set[c] = true
	}
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Locations returns every finding location as "table.column" or
// "file:<path>", sorted and unique.
func (e *Envelope) Locations() []string {
	if e == nil {
		return nil
	}
	set := map[string]bool{}
	for _, f := range e.Data.Findings {
		for _, l := range f.Locations {
			switch {
			case l.File != "":
				set["file:"+l.File] = true
			case l.Table != "" && l.Column != "":
				set[l.Table+"."+l.Column] = true
			case l.Table != "":
				set[l.Table] = true
			}
		}
	}
	out := make([]string, 0, len(set))
	for l := range set {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

// AtOrAbove counts findings at or above sev (a control pass's false
// alarms at low).
func (e *Envelope) AtOrAbove(sev core.Severity) int {
	if e == nil {
		return 0
	}
	n := 0
	for _, f := range e.Data.Findings {
		if f.Severity >= sev {
			n++
		}
	}
	return n
}

// FindingsAtOrAbove lists the IDs of findings at or above sev, sorted.
func (e *Envelope) FindingsAtOrAbove(sev core.Severity) []string {
	var out []string
	if e == nil {
		return out
	}
	for _, f := range e.Data.Findings {
		if f.Severity >= sev && !slices.Contains(out, f.ID) {
			out = append(out, f.ID)
		}
	}
	sort.Strings(out)
	return out
}

// describe sums up a run for a gate's detail: the exit code and the
// error code, never a message (messages may quote paths with values).
func describe(r *Run, e *Envelope) string {
	var b strings.Builder
	fmt.Fprintf(&b, "exit %d", r.Exit)
	if r.Signaled != "" {
		fmt.Fprintf(&b, " after %s", r.Signaled)
	}
	if c := e.ErrorCode(); c != "" {
		fmt.Fprintf(&b, ", error %s", c)
	}
	if e == nil && len(r.Stdout) > 0 {
		b.WriteString(", no envelope")
	}
	return b.String()
}
