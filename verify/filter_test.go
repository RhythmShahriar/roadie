// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package verify

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/roadie/core"
)

// writeReport saves a minimal report.json with the given findings.
func writeReport(t *testing.T, schema string, findings ...core.Finding) string {
	t.Helper()
	b := []byte(`{"schema":"` + schema + `","kind":"verify","data":{"findings":[`)
	for i, f := range findings {
		if i > 0 {
			b = append(b, ',')
		}
		j, err := f.MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
		b = append(b, j...)
	}
	b = append(b, `],"signature":{"alg":"ed25519"}}}`...)
	p := filepath.Join(t.TempDir(), "report.json")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// people is a source with emails in two tables and a phone.
func people() *core.Batch {
	return batch(table("wp_users", "ID", "user_email"),
		[]string{"1", "ada.kowalczyk@gmail.com"}, []string{"2", "bo.lindqvist@gmail.com"})
}

func comments() *core.Batch {
	return batch(table("wp_comments", "comment_ID", "comment_author_email", "comment_author_IP"),
		[]string{"5", "cy.okafor@gmail.com", "81.2.69.160"})
}

func at(table, column string) []core.LocationCount {
	return []core.LocationCount{{Location: core.Location{Table: table, Column: column, Row: "ID=9"}, Count: 1}}
}

func TestBaselineHidesExactlyTheMatchingFindings(t *testing.T) {
	base := writeReport(t, core.EnvelopeSchema,
		core.Finding{ID: core.FindingPIIEmail, Severity: core.SeverityHigh, Count: 2, Locations: at("wp_users", "user_email")},
		// Same table and column, another ID: hides nothing of pii.email.
		core.Finding{ID: core.FindingPIIPhone, Severity: core.SeverityHigh, Count: 1, Locations: at("wp_comments", "comment_author_email")},
	)
	res := runJob(t, Job{Source: source(people(), comments()), Opts: Options{Baseline: base}})
	var emails []string
	for _, f := range res.Findings {
		if f.ID == core.FindingPIIEmail {
			emails = append(emails, f.Locations[0].Table)
		}
	}
	if !slices.Equal(emails, []string{"wp_comments"}) {
		t.Errorf("pii.email left in %v, want only wp_comments", emails)
	}
	if find(res, core.FindingPIIIP) == nil {
		t.Errorf("pii.ip was hidden: %v", ids(res))
	}
	if res.Hidden != 2 {
		t.Errorf("Hidden %d, want the 2 hidden hits", res.Hidden)
	}

	// Without the baseline both tables report.
	res = runJob(t, Job{Source: source(people(), comments())})
	n := 0
	for _, f := range res.Findings {
		if f.ID == core.FindingPIIEmail {
			n++
		}
	}
	if res.Hidden != 0 || n != 2 {
		t.Errorf("no baseline: hidden %d, %+v", res.Hidden, res.Findings)
	}
}

func TestBaselineRefusesWhatIsNoReport(t *testing.T) {
	dir := t.TempDir()
	notJSON := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(notJSON, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, p := range map[string]string{
		"missing":      filepath.Join(dir, "nope.json"),
		"not json":     notJSON,
		"other schema": writeReport(t, "rhythm-tuner/v1"),
		"no schema":    writeReport(t, ""),
	} {
		_, err := Run(context.Background(), Job{Source: source(people()), Opts: Options{Baseline: p}})
		var ce *core.Error
		if !errors.As(err, &ce) || ce.Code != core.ErrConfig || ce.Exit != 2 || !strings.HasPrefix(ce.Msg, "--baseline ") {
			t.Errorf("%s: got %v", name, err)
		}
	}
	// A report with no findings is a valid baseline that hides nothing.
	res := runJob(t, Job{Source: source(people()), Opts: Options{Baseline: writeReport(t, core.EnvelopeSchema)}})
	if res.Hidden != 0 || find(res, core.FindingPIIEmail) == nil {
		t.Errorf("empty baseline hid %d", res.Hidden)
	}
}

func TestIgnoreHidesUntilItExpires(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	opts := func(until time.Time) Options {
		return Options{Clock: clock, Ignore: []Ignore{{
			ID: core.FindingPIIEmail, Location: "wp_users 1", Reason: "the owner's own address", Until: until,
		}}}
	}

	// In force (today is the last day): row 1 is hidden, row 2 is not.
	res := runJob(t, Job{Source: source(people()), Opts: opts(time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC))})
	f := find(res, core.FindingPIIEmail)
	if f == nil || f.Count != 1 || f.Locations[0].Row != "ID=2" || res.Hidden != 1 {
		t.Fatalf("in force: hidden %d, %+v", res.Hidden, f)
	}
	if find(res, core.FindingInfoIgnoreExpired) != nil {
		t.Error("an entry in force is reported as expired")
	}
	// No until: in force for good.
	if res := runJob(t, Job{Source: source(people()), Opts: opts(time.Time{})}); res.Hidden != 1 {
		t.Errorf("no until: hidden %d", res.Hidden)
	}

	// Expired yesterday: hides nothing and says so.
	res = runJob(t, Job{Source: source(people()), Opts: opts(time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC))})
	if f := find(res, core.FindingPIIEmail); f == nil || f.Count != 2 || res.Hidden != 0 {
		t.Errorf("expired: hidden %d, %+v", res.Hidden, f)
	}
	e := find(res, core.FindingInfoIgnoreExpired)
	if e == nil || e.Severity != core.SeverityInfo || !core.KnownID(e.ID) {
		t.Fatalf("no info.ignore-expired: %v", ids(res))
	}
	if want := "An ignore entry for pii.email at wp_users 1 expired on 2026-10-25, so it hides nothing now"; e.Title != want {
		t.Errorf("title %q", e.Title)
	}
	if !slices.IsSortedFunc(res.Findings, func(a, b core.Finding) int { return int(b.Severity) - int(a.Severity) }) {
		t.Errorf("findings not sorted by severity: %v", ids(res))
	}
}

func TestIgnoreEntriesAreChecked(t *testing.T) {
	for name, e := range map[string]Ignore{
		"no reason":   {ID: core.FindingPIIEmail, Location: "wp_users 1"},
		"no location": {ID: core.FindingPIIEmail, Reason: "x"},
		"unknown id":  {ID: "pii.emails", Location: "wp_users 1", Reason: "x"},
	} {
		_, err := Run(context.Background(), Job{Source: source(people()), Opts: Options{Ignore: []Ignore{e}}})
		var ce *core.Error
		if !errors.As(err, &ce) || ce.Code != core.ErrConfig || ce.Exit != 2 || !strings.Contains(ce.Msg, "ignore entry 1") {
			t.Errorf("%s: got %v", name, err)
		}
	}
}

func TestLocationMatches(t *testing.T) {
	row := core.Location{Table: "wp_options", Column: "option_value", Row: "option_id=12,option_name=shop_phone", Path: "serialized:phone"}
	file := core.Location{File: "wp-config.php", Line: 41}
	for _, tc := range []struct {
		want string
		l    core.Location
		ok   bool
	}{
		{"wp_options shop_phone", row, true},
		{"wp_options 12 shop_phone", row, true},
		{"wp_options option_name=shop_phone", row, true},
		{"wp_options.option_value", row, true},
		{"wp_options shop_phone → serialized:phone", row, true},
		{"wp_options shop_*", row, true},
		{"wp-config.php", file, true},
		{"wp-config.php line 41", file, true},
		{"wp-config.php line 4", file, false},
		{"wp_options shop_email", row, false},
		{"wp_options", row, false},
		{"wp_usermeta.option_value", row, false},
		{"", row, false},
		{"wp_options [", row, false},
	} {
		if got := locationMatches(tc.want, tc.l); got != tc.ok {
			t.Errorf("locationMatches(%q, %+v) = %v, want %v", tc.want, tc.l, got, tc.ok)
		}
	}
}

func TestChecksListIsStableAndSorted(t *testing.T) {
	job := func() Job {
		return Job{Source: source(people(), comments()), Opts: Options{Skip: []string{"phones"}}}
	}
	res := runJob(t, job())
	var got []string
	status := map[string]core.Check{}
	for _, c := range res.Checks {
		got = append(got, c.ID)
		status[c.ID] = c
	}
	if !slices.IsSorted(got) {
		t.Errorf("checks not sorted: %v", got)
	}
	for _, id := range append(slices.Clone(dataChecks), core.FindingUncoveredColumn, core.FindingConfigSalts, core.FindingSearchIndexable) {
		if _, ok := status[id]; !ok {
			t.Errorf("no check %s in %v", id, got)
		}
	}
	for id, want := range map[string]string{
		core.FindingPIIEmail:        "fail",
		core.FindingPIIIP:           "fail",
		core.FindingPIIPhone:        "skip",
		core.FindingExactSurvivors:  "skip",
		core.FindingURLProduction:   "skip",
		core.FindingConfigSalts:     "skip",
		core.FindingSearchIndexable: "skip",
		CheckKeyLive:                "pass",
	} {
		if status[id].Status != want {
			t.Errorf("%s: %+v, want %s", id, status[id], want)
		}
	}
	if c := status[core.FindingPIIEmail]; c.Checked != 3 || c.Detail != "3 findings (pii.email)" {
		t.Errorf("pii.email %+v", c)
	}
	if c := status[core.FindingPIIPhone]; c.Detail != "left out with --skip phones" || c.Checked != 0 {
		t.Errorf("pii.phone %+v", c)
	}
	if c := status[core.FindingExactSurvivors]; !strings.HasPrefix(c.Detail, "heuristic mode") {
		t.Errorf("exact.survivors %+v", c)
	}
	if c := status[core.FindingSearchIndexable]; c.Detail != "--site not given" {
		t.Errorf("search.indexable %+v", c)
	}
	for range 3 {
		if again := runJob(t, job()); !reflect.DeepEqual(again.Checks, res.Checks) {
			t.Fatalf("checks changed between runs:\n%+v\n%+v", res.Checks, again.Checks)
		}
	}

	// Hidden findings don't fail their check; --fail-on raises the bar.
	res = runJob(t, Job{Source: source(people()), Opts: Options{Ignore: []Ignore{{ID: core.FindingPIIEmail, Location: "wp_users.user_email", Reason: "seed data"}}}})
	for _, c := range res.Checks {
		if c.ID == core.FindingPIIEmail && c.Status != "pass" {
			t.Errorf("hidden findings failed the check: %+v", c)
		}
	}
	// Findings under --fail-on warn: the check doesn't fail, nor does it
	// pass as if there were none.
	res = runJob(t, Job{Source: source(people()), Opts: Options{FailOn: core.SeverityCritical}})
	for _, c := range res.Checks {
		if c.ID == core.FindingPIIEmail && (c.Status != "warn" || c.Detail != "2 findings below --fail-on critical (pii.email)") {
			t.Errorf("--fail-on critical: %+v", c)
		}
	}
}

func TestCheckOf(t *testing.T) {
	for id, want := range map[string]string{
		core.KeyLiveID("stripe"):              CheckKeyLive,
		core.FindingKeyLiveMode:               core.FindingKeyLiveMode,
		core.FindingKeyGeneric:                core.FindingKeyGeneric,
		core.FindingPIIEmail:                  core.FindingPIIEmail,
		core.FindingSideEffectConnectorChange: core.FindingSideEffectMail,
	} {
		if got := checkOf(id); got != want {
			t.Errorf("checkOf(%s) = %s, want %s", id, got, want)
		}
	}
}

// everything is a run with data, config, code, upload and Step findings.
func everything(code string) Job {
	st := siteFixture(core.LocalSite)
	if code != "" {
		if err := st.WriteFile(context.Background(), "wp-content/themes/shop/functions.php", []byte(code), 0o600); err != nil {
			panic(err)
		}
	}
	return Job{Site: st, Source: source(people(), comments(), options([]string{"1", "woocommerce_stripe_settings",
		ser("enabled", "yes", "testmode", "no", "secret_key", liveStripe())})),
		Production: []string{"shop.com"}, Steps: []core.Step{fakeStep{id: core.FindingSideEffectCron}}}
}

// A report of a run, given back as its baseline, hides every finding of
// the same copy; a key that moved to another line of the same file stays
// hidden; a new table is still reported.
func TestBaselineOfTheSameRunHidesEverything(t *testing.T) {
	first := runJob(t, everything(""))
	var total int64
	kinds := map[string]bool{}
	for _, f := range first.Findings {
		total += f.Count
		l := f.Locations[0]
		kinds[fmt.Sprint(l.File != "", l.Table != "")] = true
	}
	if len(first.Findings) < 8 || len(kinds) < 2 || len(first.Keys) == 0 || len(first.Uncovered) == 0 {
		t.Fatalf("the fixture should report data and file findings, keys and uncovered columns: %v", ids(first))
	}
	base := writeReport(t, core.EnvelopeSchema, first.Findings...)

	moved := "<?php\n// settings\n\n\n$stripe = '" + liveStripe() + "';\n"
	j := everything(moved)
	j.Opts.Baseline = base
	res := runJob(t, j)
	if len(res.Findings) != 0 || len(res.Keys) != 0 || len(res.Uncovered) != 0 {
		t.Errorf("left: %v keys %+v uncovered %+v", ids(res), res.Keys, res.Uncovered)
	}
	if res.Hidden != total {
		t.Errorf("Hidden %d, want %d", res.Hidden, total)
	}
	// The hidden hits are counted as the findings they make too, grouped
	// as the run without the baseline grouped them.
	if res.HiddenFindings != int64(len(first.Findings)) || res.HiddenFindings >= res.Hidden {
		t.Errorf("HiddenFindings %d, want %d (of %d hits)", res.HiddenFindings, len(first.Findings), res.Hidden)
	}

	j = everything("")
	j.Source = source(people(), comments(), batch(table("wp_wc_orders", "id", "billing_email"), []string{"1", "dee.novak@gmail.com"}))
	j.Opts.Baseline = base
	res = runJob(t, j)
	f := find(res, core.FindingPIIEmail)
	if f == nil || f.Locations[0].Table != "wp_wc_orders" || f.Count != 1 {
		t.Errorf("a new table was hidden: %+v", res.Findings)
	}
}
