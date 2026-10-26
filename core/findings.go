// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package core

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/RhythmShahriar/rhythm/kit/cli"
)

// Severity is kit's scale, none to critical.
type Severity = cli.Severity

// The kit severities, so callers need not import kit/cli for them.
const (
	SeverityNone     = cli.SeverityNone
	SeverityInfo     = cli.SeverityInfo
	SeverityLow      = cli.SeverityLow
	SeverityMedium   = cli.SeverityMedium
	SeverityHigh     = cli.SeverityHigh
	SeverityCritical = cli.SeverityCritical
)

// MaxExamples is the most masked examples and row locations a grouped
// finding keeps (PRD 15.3).
const MaxExamples = 3

// Finding is one grouped finding: every hit of one ID in one column.
type Finding struct {
	ID        string          `json:"id"`
	Severity  Severity        `json:"-"`
	SevName   string          `json:"severity"` // filled by MarshalJSON from Severity
	Title     string          `json:"title"`
	Count     int64           `json:"count"`
	Locations []LocationCount `json:"locations"`
	Examples  []string        `json:"examples,omitempty"` // masked, at most MaxExamples
	Fix       string          `json:"fix,omitempty"`
	Detail    string          `json:"detail,omitempty"`
}

// finding has Finding's fields without its methods, so the JSON methods
// don't call themselves.
type finding Finding

// MarshalJSON writes severity by name and locations as [] when empty.
func (f Finding) MarshalJSON() ([]byte, error) {
	f.SevName = f.Severity.String()
	if f.Locations == nil {
		f.Locations = []LocationCount{}
	}
	return json.Marshal(finding(f))
}

// UnmarshalJSON reads a finding back (as --baseline does) and sets
// Severity from the name.
func (f *Finding) UnmarshalJSON(b []byte) error {
	var v finding
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	sev, err := cli.ParseFailOn(v.SevName)
	if err != nil {
		return fmt.Errorf("finding %s: %w", v.ID, err)
	}
	v.Severity = sev
	*f = Finding(v)
	return nil
}

// LocationCount is a location with the number of hits there.
type LocationCount struct {
	Location
	Count int64 `json:"count"`
}

// KeyHit is one live, test or unknown key found in the data or a config file.
type KeyHit struct {
	ID       string // "key.stripe-live"
	Provider string
	Env      string // "live", "test", "unknown"
	Severity Severity
	At       Location
	Masked   string
	Action   string // "swapped", "blanked", "kept", "manual"
}

// SideEffect is the state of one thing a staging copy could do to the real
// world, such as sending mail or charging cards.
type SideEffect struct {
	ID     string // a side-effect finding ID, "side-effect.cron"
	Status string // "off", "on", "unknown", "not-present"
	How    string // what Roadie did
	Undo   string // how to turn it back on for a test
}

// Check is one verify check and its outcome.
type Check struct {
	ID      string
	Status  string // "pass", "fail", "skip"
	Checked int64
	Detail  string
}

// Aggregator groups hits by ID and location (table and column, or file),
// adds up their counts and keeps the first MaxExamples masked examples and
// row locations of each group. The zero value is ready to use; it is not
// safe for concurrent use.
type Aggregator struct {
	groups map[aggKey]*Finding
	order  []aggKey
}

type aggKey struct{ id, table, column, file string }

// Add merges f into its group. A finding with Count 0 counts as one hit.
// Locations beyond MaxExamples add to Count but aren't listed.
// The group keeps the highest severity and the first non-empty title,
// fix and detail.
func (a *Aggregator) Add(f Finding) {
	var first Location
	if len(f.Locations) > 0 {
		first = f.Locations[0].Location
	}
	k := aggKey{f.ID, first.Table, first.Column, first.File}
	if f.Count == 0 {
		f.Count = 1
	}
	if a.groups == nil {
		a.groups = map[aggKey]*Finding{}
	}
	g, ok := a.groups[k]
	if !ok {
		g = &Finding{ID: f.ID, Severity: f.Severity, Title: f.Title, Fix: f.Fix, Detail: f.Detail}
		a.groups[k] = g
		a.order = append(a.order, k)
	}
	g.Count += f.Count
	g.Severity = max(g.Severity, f.Severity)
	g.Title = cmp.Or(g.Title, f.Title)
	g.Fix = cmp.Or(g.Fix, f.Fix)
	g.Detail = cmp.Or(g.Detail, f.Detail)
	for _, l := range f.Locations {
		if l.Count == 0 {
			// A single location without its own count holds every hit.
			l.Count = 1
			if len(f.Locations) == 1 {
				l.Count = f.Count
			}
		}
		i := slices.IndexFunc(g.Locations, func(x LocationCount) bool { return x.Location == l.Location })
		switch {
		case i >= 0:
			g.Locations[i].Count += l.Count
		case len(g.Locations) < MaxExamples:
			g.Locations = append(g.Locations, l)
		}
	}
	for _, e := range f.Examples {
		if len(g.Examples) < MaxExamples && !slices.Contains(g.Examples, e) {
			g.Examples = append(g.Examples, e)
		}
	}
}

// Findings returns the groups sorted by severity (worst first), then ID,
// table, column and file.
func (a *Aggregator) Findings() []Finding {
	out := make([]Finding, 0, len(a.order))
	keys := slices.Clone(a.order)
	slices.SortStableFunc(keys, func(x, y aggKey) int {
		return cmp.Or(
			cmp.Compare(a.groups[y].Severity, a.groups[x].Severity),
			cmp.Compare(x.id, y.id),
			cmp.Compare(x.table, y.table),
			cmp.Compare(x.column, y.column),
			cmp.Compare(x.file, y.file),
		)
	})
	for _, k := range keys {
		out = append(out, *a.groups[k])
	}
	return out
}
