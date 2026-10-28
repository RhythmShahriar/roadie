// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package core

import (
	"strings"
	"time"
)

// TableReport is what a run did to one table.
type TableReport struct {
	Name    string
	Rows    int64
	Action  string
	Profile string
	Columns []ColumnReport
}

// ColumnReport is what a run did to one column.
type ColumnReport struct {
	Name    string
	Rule    string
	Changed int64
	Kept    int64
}

// Coverage says how much of the copy verify read.
type Coverage struct {
	TablesScanned int
	TablesTotal   int
	RowsScanned   int64
	BytesScanned  int64
	Skipped       []Skipped
	Sample        int // rows sampled per table; 0 = every row
}

// Skipped is a table verify couldn't or didn't read, with why.
type Skipped struct {
	Table  string
	Reason string
}

// SkippedInput starts the Table of a Skipped entry for input verify
// couldn't read: "(input)", or "(input after wp_users)" for the tables
// after a read error.
const SkippedInput = "(input"

// Unread reports whether part of verify's input couldn't be read. Such a
// run is not complete and never passes (PRD 15.3); other skipped entries
// (values read as text, files a site can't list) don't count.
func (c Coverage) Unread() bool {
	for _, s := range c.Skipped {
		if strings.HasPrefix(s.Table, SkippedInput) {
			return true
		}
	}
	return false
}

// Uncovered is a column that looks personal, with the share of sampled
// values that did; a rule-covered column has Rate 100 in Result.Covered.
type Uncovered struct {
	Table  string
	Column string
	Rate   int // percent
}

// VerifyResult is everything verify found.
type VerifyResult struct {
	Mode        string // "exact", "heuristic"
	Findings    []Finding
	Checks      []Check
	Keys        []KeyHit
	SideEffects []SideEffect
	Coverage    Coverage
	Uncovered   []Uncovered
	Hidden      int64 // hits hidden by a baseline or an ignore entry
	// HiddenFindings counts the findings those hits make, grouped as
	// Findings groups the shown ones.
	HiddenFindings int64
	Soundcheck     *SoundcheckResult
	// Input fingerprints what verify read, once it read it whole: the
	// dump's SHA-256 or the database's tables. A report check --input
	// ties the report to the file it checked.
	Input *Fingerprint
}

// SoundcheckResult is the outcome of verify --soundcheck.
type SoundcheckResult struct {
	Ran      bool
	ExitCode int
	Schema   string
	Counts   map[string]int64
	Note     string
}

// Result is everything a renderer needs, filled by engine and verify.
type Result struct {
	Kind        string // "plan", "copy", "prepare", "verify"
	Started     time.Time
	Duration    time.Duration
	Source      EndpointInfo
	Target      EndpointInfo
	TargetState TargetState
	Production  []string
	// TargetChecked are the production hosts the target guard compared
	// the target with; nil when there was no site or database to check.
	TargetChecked []string
	URL           string
	Stack         StackInfo
	Consistency   ConsistencyInfo
	Input         *Fingerprint
	Output        *Fingerprint

	RulesetSHA256       string
	ConfigSHA256        string
	ProfilePack         string
	EchoConnectorSHA256 string

	Tables         []TableReport
	Changes        []Change
	Keys           []KeyHit
	SideEffects    []SideEffect
	PlannedEdits   []PlannedEdit
	RemoteCommands []string
	Verify         *VerifyResult
	FailOn         Severity
	Err            *Error
	NotCovered     []string
	// Findings are the engine's findings outside verify: uncovered-column,
	// data.unparseable, data.encrypted, info.kept-users, kill-switch;
	// plan --fail-on reads these.
	Findings []Finding
	// Covered and Uncovered are data.covered[] and data.uncovered[].
	Covered   []Uncovered
	Uncovered []Uncovered
	// Backup and Restore are the backup path and restore command of
	// prepare, for the screen; the JSON has them only in data.error of a
	// run that stopped.
	Backup  string
	Restore string
	// KeptUsers and FirstAdmin feed the no-kept-user login line of PRD
	// 11.1; FirstAdmin is the fake login.
	KeptUsers  int64
	FirstAdmin string
	// BasicAuthPassword is printed once by the CLI, never in a report.
	BasicAuthPassword string `json:"-"`
}

// EndpointInfo describes a source or target in a result.
type EndpointInfo struct {
	Kind    EndpointKind
	Display string
	Engine  string
	Server  string
	Bytes   int64
	State   TargetState
}

// StackInfo is the detected stack and profiles.
type StackInfo struct {
	Name      Stack
	Profiles  []string
	Multisite bool
	HPOS      bool
	Sites     int
}

// ConsistencyInfo says how fakes were keyed. An empty KeyFingerprint (a
// per-run key) renders as null.
type ConsistencyInfo struct {
	Mode           string
	KeyFingerprint string
}

// UncoveredFix is the fix line of every uncovered-column finding, the
// run's and verify's alike.
const UncoveredFix = "Add the column under columns: in .roadie/config.yaml (rhythm-roadie explain uncovered-column)."

// AllFindings returns the engine's findings followed by verify's. Copy
// and prepare report an uncovered column themselves (--allow-uncovered)
// and verify scans for the same columns, so verify's finding for a
// column the run already named is dropped: one column, one finding.
func (r *Result) AllFindings() []Finding {
	out := append([]Finding(nil), r.Findings...)
	if r.Verify == nil {
		return out
	}
	named := map[Location]bool{}
	for _, f := range r.Findings {
		if f.ID == FindingUncoveredColumn {
			for _, l := range f.Locations {
				named[Location{Table: l.Table, Column: l.Column}] = true
			}
		}
	}
	for _, f := range r.Verify.Findings {
		if f.ID == FindingUncoveredColumn && len(f.Locations) > 0 && allNamed(f.Locations, named) {
			continue
		}
		out = append(out, f)
	}
	return out
}

// allNamed reports whether every location's table and column is in named.
func allNamed(ls []LocationCount, named map[Location]bool) bool {
	for _, l := range ls {
		if !named[Location{Table: l.Table, Column: l.Column}] {
			return false
		}
	}
	return true
}

// Summary counts the result for the envelope. Severity counts are grouped
// findings, not hits. Keys and side effects come from the run itself, or
// from verify when the run had none (a standalone verify).
func (r *Result) Summary() Summary {
	var s Summary
	for _, f := range r.AllFindings() {
		switch f.Severity {
		case SeverityCritical:
			s.Critical++
		case SeverityHigh:
			s.High++
		case SeverityMedium:
			s.Medium++
		case SeverityLow:
			s.Low++
		case SeverityInfo:
			s.Info++
		}
	}
	s.Tables = len(r.Tables)
	for _, t := range r.Tables {
		s.Rows += t.Rows
		for _, c := range t.Columns {
			if c.Changed > 0 {
				s.TablesPseudonymised++
				break
			}
		}
	}
	if len(r.Tables) == 0 && r.Verify != nil {
		// A standalone verify lists no tables of its own: what it read is
		// its coverage.
		s.Tables = r.Verify.Coverage.TablesScanned
		s.Rows = r.Verify.Coverage.RowsScanned
	}
	for _, c := range r.Changes {
		switch c.Action {
		case RewriteURL:
			s.URLsRewritten += c.Values
		case Keep, SwapKey:
		default:
			s.ValuesPseudonymised += c.Values
		}
	}
	keys, effects := r.Keys, r.SideEffects
	if r.Verify != nil {
		if len(keys) == 0 {
			keys = r.Verify.Keys
		}
		if len(effects) == 0 {
			effects = r.Verify.SideEffects
		}
	}
	s.KeysFound = len(keys)
	for _, k := range keys {
		switch k.Action {
		case "swapped":
			s.KeysSwapped++
		case "blanked":
			s.KeysBlanked++
		}
	}
	for _, e := range effects {
		if e.Status == "not-present" {
			continue
		}
		s.SideEffectsTotal++
		if e.Status == "off" {
			s.SideEffectsOff++
		}
	}
	seen := map[[2]string]bool{}
	add := func(us []Uncovered) {
		for _, u := range us {
			seen[[2]string{u.Table, u.Column}] = true
		}
	}
	add(r.Uncovered)
	if r.Verify != nil {
		add(r.Verify.Uncovered)
	}
	s.UncoveredColumns = len(seen)
	return s
}
