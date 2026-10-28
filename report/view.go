// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"cmp"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/RhythmShahriar/roadie/core"
)

// The helpers every view shares: the verdict, names, counts and the
// order of findings. They read only the envelope, so a view can't show
// what the signed JSON doesn't hold.

// report is one envelope with its data, as the views read it.
type report struct {
	env  *core.Envelope
	data *Data
}

func readReport(e *core.Envelope) (*report, error) {
	d, err := ParseData(e)
	if err != nil {
		return nil, err
	}
	return &report{env: e, data: d}, nil
}

// name is how a report names its target: the URL's host, else the last
// part of a path or file name, else the display form (the screen's
// shortName).
func (r *report) name() string {
	if h := hostOf(r.data.URL); h != "" {
		return h
	}
	t := r.data.Target
	d := strings.TrimRight(t.Display, "/")
	switch core.EndpointKind(t.Kind) {
	case core.DumpFile, core.LocalSite:
		return path.Base(strings.ReplaceAll(d, "\\", "/"))
	case core.Stdio:
		return "stdout"
	}
	if d == "" {
		return "the copy"
	}
	return d
}

// hostOf returns the host of a URL, or "" when it has none.
func hostOf(raw string) string {
	i := strings.Index(raw, "://")
	if i < 0 {
		return ""
	}
	h, _, _ := strings.Cut(raw[i+3:], "/")
	if at := strings.LastIndex(h, "@"); at >= 0 {
		h = h[at+1:]
	}
	return h
}

// verdict is the word and sentence of the result box (PRD 16.1).
func (r *report) verdict() (word, line string) {
	d := r.data
	switch {
	case d.Error != nil:
		code := cmp.Or(d.Error.Code, "the run stopped")
		state := "the target is untouched"
		switch core.TargetState(cmp.Or(d.TargetState, d.Target.State)) {
		case core.Mixed:
			state = "the target is in a mixed state"
		case core.Replaced:
			state = "the target was replaced"
		}
		return "STOPPED", code + "; " + state
	case r.env.Kind == "plan":
		what := "copy"
		if d.Source.Display == d.Target.Display && d.Source.Kind != string(core.DumpFile) {
			what = "prepare"
		}
		if n := len(d.Uncovered); n > 0 {
			return "PLAN", what + " would stop: " + plural(int64(n), "uncovered column", "uncovered columns")
		}
		return "PLAN", "ready: nothing changes until you run " + what
	case d.Result == ResultError && d.Coverage != nil && slices.ContainsFunc(d.Coverage.Skipped, func(s SkippedData) bool {
		return strings.HasPrefix(s.Table, core.SkippedInput)
	}):
		return "INCOMPLETE", r.name() + " couldn't be read whole; coverage lists what wasn't"
	case d.Result == ResultError:
		return "STOPPED", "verify couldn't check " + r.name()
	case d.VerifyMode == "none" && r.env.Kind != "verify":
		return "DONE", "copied to " + r.name() + "; not checked (--no-verify)"
	case d.Result == ResultFail:
		return "FAIL", r.name() + " is not a safe copy yet"
	case d.Coverage != nil && d.Coverage.Sample > 0:
		return "PASS (sample)", r.name() + " is a pseudonymised copy"
	}
	return "PASS", r.name() + " is a pseudonymised copy"
}

// rank orders reports worst first: stopped, failed, then by counts.
func (r *report) rank() []int64 {
	word, _ := r.verdict()
	order := map[string]int64{"STOPPED": 0, "FAIL": 1, "PLAN": 2, "DONE": 3}
	w, ok := order[word]
	if !ok {
		w = 4
	}
	s := r.env.Summary
	return []int64{w, -s.Critical, -s.High, -s.Medium, -s.Low, -s.Info}
}

// worstFirst sorts reports worst first, keeping the given order for ties.
func worstFirst(rs []*report) {
	slices.SortStableFunc(rs, func(a, b *report) int { return slices.Compare(a.rank(), b.rank()) })
}

// counts is the PRD 16.1 count line.
func counts(s core.Summary) string {
	return fmt.Sprintf("%d critical · %d high · %d medium · %d low · %d info", s.Critical, s.High, s.Medium, s.Low, s.Info)
}

// num writes n with thousands separators: 18,402.
func num(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// plural writes "1 table" or "3 tables".
func plural(n int64, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return num(n) + " " + many
}

// size writes a byte count in decimal units: 2.4 GB.
func size(n int64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	f := float64(n)
	i := 0
	for f >= 1000 && i < len(units)-1 {
		f /= 1000
		i++
	}
	if i == 0 {
		return strconv.FormatInt(n, 10) + " B"
	}
	if f >= 100 {
		return strconv.FormatFloat(f, 'f', 0, 64) + " " + units[i]
	}
	return strconv.FormatFloat(f, 'f', 1, 64) + " " + units[i]
}

// coverageLine says how much verify read: "exact, every row of 61
// tables" or "heuristic, a sample of 1,000 rows per table of 58 tables".
func (r *report) coverageLine() string {
	d := r.data
	c := d.Coverage
	if c == nil || d.VerifyMode == "none" {
		return "not run"
	}
	tables := plural(int64(c.TablesScanned), "table", "tables")
	if c.TablesTotal > c.TablesScanned {
		tables = num(int64(c.TablesScanned)) + " of " + plural(int64(c.TablesTotal), "table", "tables")
	}
	if c.Sample > 0 {
		return d.VerifyMode + ", a sample of " + plural(int64(c.Sample), "row", "rows") + " per table of " + tables
	}
	return d.VerifyMode + ", every row of " + tables
}

// stackLine names the stack and its profiles: "wordpress (wordpress,
// woocommerce, woocommerce-hpos)".
func (r *report) stackLine() string {
	s := r.data.Stack
	out := cmp.Or(s.Name, "not detected")
	if len(s.Profiles) > 0 {
		out += " (" + strings.Join(s.Profiles, ", ") + ")"
	}
	if s.Multisite {
		out += ", multisite with " + plural(int64(s.Sites), "site", "sites")
	}
	return out
}

// checkResult is a check's outcome as a few words: "1,210,044 checked".
func checkResult(c CheckData) string {
	if c.Detail != "" {
		return c.Detail
	}
	switch {
	case c.Status == "skip":
		return "skipped"
	case c.Checked > 0:
		return num(c.Checked) + " checked"
	}
	return c.Status
}

// findingTitle is the title with the count in front when it is more
// than one and the title doesn't already start with it.
func findingTitle(f core.Finding) string {
	if f.Count > 1 && !strings.HasPrefix(f.Title, num(f.Count)) {
		return num(f.Count) + " " + f.Title
	}
	return cmp.Or(f.Title, f.ID)
}

// locationLine lists where a finding is: "wp_users.user_email 2 · wp-config.php line 41 1".
func locationLine(f core.Finding) string {
	parts := make([]string, 0, len(f.Locations))
	for _, l := range f.Locations {
		s := l.String()
		if l.Table != "" && l.Column != "" && l.Row != "" {
			s = l.Table + "." + l.Column + " " + l.Row
			if l.Path != "" {
				s += " → " + l.Path
			}
		}
		if l.Count > 0 {
			s += " " + num(l.Count)
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, " · ")
}

// severities lists the severities worst first, as the views group findings.
var severities = []core.Severity{core.SeverityCritical, core.SeverityHigh, core.SeverityMedium, core.SeverityLow, core.SeverityInfo}

// bySeverity returns the findings of one severity in the envelope's order.
func (r *report) bySeverity(s core.Severity) []core.Finding {
	var out []core.Finding
	for _, f := range r.data.Findings {
		if f.Severity == s {
			out = append(out, f)
		}
	}
	return out
}

// checkFindings are the findings a failed check stands for: its own ID,
// key.* for the keys check and side-effect.* for side effects.
func (r *report) checkFindings(id string) []core.Finding {
	var out []core.Finding
	for _, f := range r.data.Findings {
		switch {
		case f.ID == id,
			id == "keys" && strings.HasPrefix(f.ID, "key."),
			id == "side-effects" && strings.HasPrefix(f.ID, "side-effect."):
			out = append(out, f)
		}
	}
	return out
}

// checkTally sums a check's findings: "280 hits in 8 columns", or
// "3 hits" when they name no column. "" for none.
func checkTally(fs []core.Finding) string {
	if len(fs) == 0 {
		return ""
	}
	var hits int64
	places := map[string]bool{}
	columns := true
	for _, f := range fs {
		hits += max(f.Count, 1)
		for _, l := range f.Locations {
			switch {
			case l.Column != "":
				places[l.Table+"."+l.Column] = true
			case l.File != "":
				places[l.File], columns = true, false
			case l.Table != "":
				places[l.Table], columns = true, false
			}
		}
	}
	out := plural(hits, "hit", "hits")
	if n := int64(len(places)); n > 0 && columns {
		out += " in " + plural(n, "column", "columns")
	} else if n > 0 {
		out += " in " + plural(n, "place", "places")
	}
	return out
}

// keyID is the ID of the key that signed the report, "" when unsigned.
func (r *report) keyID() string {
	if r.data.Signature == nil {
		return ""
	}
	return r.data.Signature.KeyID
}

// effectStatus writes a side effect's status for a table cell.
func effectStatus(s string) string {
	switch s {
	case "off":
		return "off"
	case "on":
		return "still on"
	case "not-present":
		return "not on this site"
	}
	return cmp.Or(s, "unknown")
}

// changedTables are the tables a run changed, kept in the envelope's order.
func (r *report) changedTables() []TableData {
	var out []TableData
	for _, t := range r.data.Tables {
		if t.Action != "" && t.Action != string(core.Keep) && t.Action != "copy" {
			out = append(out, t)
		}
	}
	return out
}

// tableWhat says what happened to a table: "user_email (wordpress.users.user_email) 18,401 changed, 1 kept".
func tableWhat(t TableData) string {
	if len(t.Columns) == 0 {
		return t.Action
	}
	parts := make([]string, 0, len(t.Columns))
	for _, c := range t.Columns {
		s := c.Name
		if c.Rule != "" {
			s += " (" + c.Rule + ")"
		}
		s += " " + num(c.Changed) + " changed"
		if c.Kept > 0 {
			s += ", " + num(c.Kept) + " kept"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, "; ")
}
