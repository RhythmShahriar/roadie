// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package engine

import (
	"bytes"
	"fmt"
	"slices"
	"strings"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/detect"
	"github.com/RhythmShahriar/roadie/profile"
)

// detectUncovered is detect.Uncovered, a variable so a unit test can
// swap in a fake; production code never assigns it.
var detectUncovered = detect.Uncovered

// The uncovered-column sample (PRD 11.3): up to maxSamples non-empty
// values per column, each cut to maxSampleBytes so a column of long
// texts can't hold megabytes; an email or a phone is far shorter.
const (
	maxSamples     = 1000
	maxSampleBytes = 1024
)

// samples holds the values pass 1 keeps for the uncovered-column scan,
// by table and column. Pass 1 is one goroutine; samples isn't safe for
// concurrent use.
type samples struct {
	m map[string]map[string][][]byte
}

func newSamples() *samples { return &samples{m: map[string]map[string][][]byte{}} }

// add keeps v (a copy, at most maxSampleBytes) when the column has room
// and v isn't empty or blank.
func (s *samples) add(table, column string, v []byte) {
	if len(bytes.TrimSpace(v)) == 0 {
		return
	}
	cols := s.m[table]
	if cols == nil {
		cols = map[string][][]byte{}
		s.m[table] = cols
	}
	if len(cols[column]) >= maxSamples {
		return
	}
	cols[column] = append(cols[column], bytes.Clone(v[:min(len(v), maxSampleBytes)]))
}

// full reports whether the column already has every sample it can keep,
// so pass 1 can skip the work of looking at more of its values.
func (s *samples) full(table, column string) bool {
	return len(s.m[table][column]) >= maxSamples
}

// textColumn reports whether a column holds text the scan reads: char,
// varchar, the text types and JSON. Binary columns and numbers can't
// hold an email the way the scan looks for one. A column with no type
// (a data-only dump names its columns in INSERT but never types them)
// may hold anything, so it is read as text: skipping it would let a
// personal column through with no uncovered stop.
func textColumn(c core.Column) bool {
	if c.Binary || c.Generated {
		return false
	}
	t := strings.ToLower(strings.TrimSpace(c.Type))
	if t == "" {
		return true
	}
	for _, p := range []string{"char", "varchar", "nchar", "nvarchar", "character varying", "character", "tinytext",
		"text", "mediumtext", "longtext", "json", "jsonb", "citext"} {
		if t == p || strings.HasPrefix(t, p+"(") || strings.HasPrefix(t, p+" ") {
			return true
		}
	}
	return false
}

// scanTable reports whether the uncovered scan looks at a table at all:
// not one the rules truncate or drop, nor one a profile keeps on purpose.
func scanTable(c *profile.Compiled, table string) bool {
	switch c.TableAction(table) {
	case core.Truncate, core.DropTable:
		return false
	}
	return !c.Kept(table)
}

// coveredColumns returns the columns of t that some rule names.
func coveredColumns(c *profile.Compiled, t *core.Table) map[string]bool {
	out := map[string]bool{}
	for _, r := range c.Rules(t.Name) {
		for _, col := range t.Columns {
			if strings.EqualFold(col.Name, r.Column) {
				out[col.Name] = true
			}
		}
	}
	return out
}

// uncoveredResult is what the scan found.
type uncoveredResult struct {
	Covered, Uncovered []core.Uncovered
	Findings           []core.Finding
}

// scanUncovered applies detect.Uncovered (the detect lane owns the PRD
// 11.3 rule) to every table the scan reads, with the columns the final
// rules cover and the values pass 1 sampled. It also lists the covered
// columns for data.covered[].
func scanUncovered(schema *core.Schema, c *profile.Compiled, s *samples) uncoveredResult {
	var res uncoveredResult
	if schema == nil {
		return res
	}
	tables := slices.Clone(schema.Tables)
	slices.SortFunc(tables, func(a, b *core.Table) int { return strings.Compare(a.Name, b.Name) })
	for _, t := range tables {
		if !scanTable(c, t.Name) {
			continue
		}
		covered := coveredColumns(c, t)
		for _, col := range t.Columns {
			if covered[col.Name] {
				res.Covered = append(res.Covered, core.Uncovered{Table: t.Name, Column: col.Name, Rate: 100})
			}
		}
		if !slices.ContainsFunc(t.Columns, func(col core.Column) bool { return textColumn(col) && !covered[col.Name] }) {
			continue
		}
		// Only uncovered text columns reach detect; a column a rule covers
		// was never meant to be sampled, whatever pass 1 kept for it.
		sampled := map[string][][]byte{}
		for col, vs := range s.m[t.Name] {
			if !covered[col] {
				sampled[col] = vs
			}
		}
		found := detectUncovered(t, covered, sampled)
		slices.SortFunc(found, func(a, b core.Uncovered) int { return strings.Compare(a.Column, b.Column) })
		for _, u := range found {
			if covered[u.Column] {
				continue // detect must not report a covered column; never trust it to
			}
			res.Uncovered = append(res.Uncovered, u)
			res.Findings = append(res.Findings, uncoveredFinding(u, sampled[u.Column]))
		}
	}
	return res
}

// uncoveredFinding is the uncovered-column finding of PRD 11.3. Its
// title and fix are verify's own (detect.UncoveredTitle and
// core.UncoveredFix), so the two paths read the same and
// Result.AllFindings can drop verify's copy of a column the run named.
func uncoveredFinding(u core.Uncovered, samples [][]byte) core.Finding {
	return core.Finding{
		ID:        core.FindingUncoveredColumn,
		Severity:  core.SeverityMedium,
		Title:     detect.UncoveredTitle(u, samples),
		Count:     1,
		Locations: []core.LocationCount{{Location: core.Location{Table: u.Table, Column: u.Column}, Count: 1}},
		Fix:       core.UncoveredFix,
	}
}

// uncoveredError is the PRD 17 uncovered stop: copy and prepare write
// nothing while a personal-looking column has no rule.
func uncoveredError(us []core.Uncovered) *core.Error {
	return uncoveredErrorIn(us, core.Untouched)
}

// uncoveredErrorIn is uncoveredError for a target in state st. With
// --one-pass the check runs after pass 2 has written every row, so a
// database target the sink couldn't take back is already Mixed and may
// hold the uncovered columns as they were; the message must say so and
// never claim the target is untouched.
func uncoveredErrorIn(us []core.Uncovered, st core.TargetState) *core.Error {
	names := make([]string, len(us))
	for i, u := range us {
		names[i] = u.Table + "." + u.Column
	}
	what, them, it, list := "columns look", "them", "them", "them as findings"
	if len(us) == 1 {
		what, them, it, list = "column looks", "it", "it", "it as a finding"
	}
	tail := "The target is untouched."
	if st != core.Untouched {
		held, has := "the columns as they were", "they have"
		if len(us) == 1 {
			held, has = "the column as it was", "it has"
		}
		tail = fmt.Sprintf("The target is in a %s state and may hold %s in production: run the same command again once %s a rule, or without --one-pass to check before writing.",
			st, held, has)
	}
	return &core.Error{Code: core.ErrUncovered, Exit: 1, State: st, Msg: fmt.Sprintf(
		"uncovered: %d %s personal and no rule covers %s: %s. Add %s under columns: (rhythm-roadie explain uncovered-column), or --allow-uncovered to copy and list %s. %s",
		len(us), what, them, strings.Join(names, ", "), it, list, tail)}
}

// thousands writes n with comma separators, as the PRD's messages do.
func thousands(n int64) string {
	s := fmt.Sprint(n)
	if n < 0 {
		return "-" + thousands(-n)
	}
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
