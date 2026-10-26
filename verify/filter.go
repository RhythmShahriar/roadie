// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package verify

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/RhythmShahriar/rhythm/kit/cli"

	"github.com/RhythmShahriar/roadie/core"
)

// maxBaseline caps the size of a --baseline report; a report holds
// grouped findings, so a real one is far smaller.
const maxBaseline = 64 << 20

// filter hides accepted findings: those already in a --baseline report and
// those an active ignore entry names. It is read-only once built, so the
// workers share it.
type filter struct {
	// baseline holds ID plus table, column and file of every location
	// of every finding in the baseline report.
	baseline map[aggKey]bool
	ignore   []Ignore // the entries still in force
}

// aggKey is how core.Aggregator groups a finding.
type aggKey struct{ id, table, column, file string }

// keyOf returns the group of a finding at location l.
func keyOf(id string, l core.Location) aggKey {
	return aggKey{id, l.Table, l.Column, l.File}
}

// newFilter loads the baseline and splits the ignore entries into those
// in force and those past their date. It returns a nil filter when there
// is nothing to hide.
func newFilter(o Options, now time.Time) (*filter, []Ignore, error) {
	f := &filter{}
	if o.Baseline != "" {
		b, err := loadBaseline(o.Baseline)
		if err != nil {
			return nil, nil, err
		}
		f.baseline = b
	}
	var expired []Ignore
	for i, e := range o.Ignore {
		if err := checkIgnore(i, e); err != nil {
			return nil, nil, err
		}
		if ignoreExpired(e, now) {
			expired = append(expired, e)
			continue
		}
		f.ignore = append(f.ignore, e)
	}
	if len(f.baseline) == 0 && len(f.ignore) == 0 {
		f = nil
	}
	return f, expired, nil
}

// checkIgnore refuses an entry without an ID, a location or a reason, or
// with an ID Roadie never reports, so a typo can't hide nothing silently.
func checkIgnore(i int, e Ignore) error {
	switch {
	case strings.TrimSpace(e.ID) == "" || strings.TrimSpace(e.Location) == "" || strings.TrimSpace(e.Reason) == "":
		return core.Errorf(core.ErrConfig, cli.ExitUsage,
			"verify ignore entry %d needs id, location and reason, e.g. {id: pii.phone, location: \"wp_options shop_phone\", reason: \"the shop's own number\"}", i+1)
	case !core.KnownID(e.ID):
		return core.Errorf(core.ErrConfig, cli.ExitUsage,
			"verify ignore entry %d: %q isn't a finding ID; rhythm-roadie explain lists them", i+1, e.ID)
	}
	return nil
}

// ignoreExpired reports whether an entry is past its until date. A date
// without a time of day (midnight UTC) is in force for the whole day.
func ignoreExpired(e Ignore, now time.Time) bool {
	if e.Until.IsZero() {
		return false
	}
	end := e.Until
	if u := e.Until.UTC(); u.Hour() == 0 && u.Minute() == 0 && u.Second() == 0 && u.Nanosecond() == 0 {
		end = u.AddDate(0, 0, 1)
	}
	return !now.Before(end)
}

// baselineReport is the part of a saved report --baseline reads. The
// signature is not checked: a baseline only hides what it lists.
type baselineReport struct {
	Schema string `json:"schema"`
	Data   struct {
		Findings []struct {
			ID        string          `json:"id"`
			Locations []core.Location `json:"locations"`
		} `json:"findings"`
	} `json:"data"`
}

// loadBaseline reads a rhythm-roadie/v1 report and returns the groups of
// its findings.
func loadBaseline(name string) (map[aggKey]bool, error) {
	bad := func(why string) error {
		return core.Errorf(core.ErrConfig, cli.ExitUsage, "--baseline %s: %s", name, why)
	}
	f, err := os.Open(name) // #nosec G304 -- the report the user named
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, bad("no such file; give a report.json from .roadie/reports or a --json run")
		}
		return nil, bad(err.Error())
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, maxBaseline+1))
	if err != nil {
		return nil, bad(err.Error())
	}
	if len(b) > maxBaseline {
		return nil, bad(fmt.Sprintf("larger than %d MiB, which no report is", maxBaseline>>20))
	}
	var r baselineReport
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, bad("isn't a Roadie JSON report: " + err.Error())
	}
	if r.Schema != core.EnvelopeSchema {
		return nil, bad(fmt.Sprintf("schema is %q, not %s", r.Schema, core.EnvelopeSchema))
	}
	out := map[aggKey]bool{}
	for _, fd := range r.Data.Findings {
		if fd.ID == "" {
			continue
		}
		if len(fd.Locations) == 0 {
			out[keyOf(fd.ID, core.Location{})] = true
		}
		for _, l := range fd.Locations {
			out[keyOf(fd.ID, l)] = true
		}
	}
	return out, nil
}

// hides reports whether f is accepted. Findings reach it one hit at a
// time, so an ignore entry for one row hides that row only and the rest
// of the column is still reported.
func (f *filter) hides(fd core.Finding) bool {
	if f == nil {
		return false
	}
	var l core.Location
	if len(fd.Locations) > 0 {
		l = fd.Locations[0].Location
	}
	if f.baseline[keyOf(fd.ID, l)] {
		return true
	}
	for _, e := range f.ignore {
		if e.ID == fd.ID && locationMatches(strings.TrimSpace(e.Location), l) {
			return true
		}
	}
	return false
}

// locationMatches reports whether an ignore entry's location names l. It
// takes the forms a report shows ("wp_options shop_phone",
// "wp_users.user_email", "wp-config.php line 41") and a few shorter ones
// people write: the table and one row key value, the table and column,
// or the file alone. A * in the entry matches like a shell glob.
func locationMatches(want string, l core.Location) bool {
	if want == "" {
		return false
	}
	glob := strings.Contains(want, "*")
	for _, c := range locationForms(l) {
		if c == want {
			return true
		}
		if glob {
			if ok, err := path.Match(want, c); err == nil && ok {
				return true
			}
		}
	}
	return false
}

// locationForms lists the ways a location can be written, each with and
// without the path inside a nested value.
func locationForms(l core.Location) []string {
	var out []string
	add := func(s string) {
		if s == "" {
			return
		}
		out = append(out, s)
		if l.Path != "" {
			out = append(out, s+" → "+l.Path)
		}
	}
	switch {
	case l.File != "":
		add(l.File)
		if l.Line > 0 {
			add(l.File + " line " + strconv.Itoa(l.Line))
		}
	case l.Table != "":
		bare := l
		bare.Path = ""
		add(bare.String())
		if l.Column != "" {
			add(l.Table + "." + l.Column)
		}
		for p := range strings.SplitSeq(l.Row, ",") {
			if k, v, ok := strings.Cut(p, "="); ok {
				add(l.Table + " " + v)
				add(l.Table + " " + k + "=" + v)
			}
		}
	}
	return slices.Compact(out)
}

// sink takes findings; add reports whether it kept f.
type sink interface {
	add(f core.Finding) bool
}

// filtered adds the findings its filter doesn't hide to an aggregator and
// counts the hits it hides.
type filtered struct {
	agg    *core.Aggregator
	f      *filter
	hidden *int64
}

func (s filtered) add(f core.Finding) bool {
	if s.f.hides(f) {
		*s.hidden += max(f.Count, 1)
		return false
	}
	s.agg.Add(f)
	return true
}

// expiredFindings are the info.ignore-expired findings, one per entry.
func expiredFindings(expired []Ignore) []core.Finding {
	out := make([]core.Finding, 0, len(expired))
	for _, e := range expired {
		out = append(out, core.Finding{
			ID: core.FindingInfoIgnoreExpired, Severity: core.SeverityInfo, Count: 1,
			Title:  fmt.Sprintf("An ignore entry for %s at %s expired on %s, so it hides nothing now", e.ID, e.Location, e.Until.UTC().Format(time.DateOnly)),
			Detail: "reason: " + e.Reason,
			Fix:    "Remove the entry, or move its until date if the value may stay.",
		})
	}
	return out
}

// sortFindings orders findings as core.Aggregator does: worst first, then
// ID, table, column and file.
func sortFindings(fs []core.Finding) {
	slices.SortStableFunc(fs, func(x, y core.Finding) int {
		var a, b core.Location
		if len(x.Locations) > 0 {
			a = x.Locations[0].Location
		}
		if len(y.Locations) > 0 {
			b = y.Locations[0].Location
		}
		return cmp.Or(
			cmp.Compare(y.Severity, x.Severity),
			cmp.Compare(x.ID, y.ID),
			cmp.Compare(a.Table, b.Table),
			cmp.Compare(a.Column, b.Column),
			cmp.Compare(a.File, b.File),
		)
	})
}
