// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/codec/urlrw"
	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/internal/testutil"
	"github.com/RhythmShahriar/roadie/verify"
)

// recSource notes when a source of the staging closes.
type recSource struct {
	core.Source
	rec *recorder
}

func (s *recSource) Close() error {
	s.rec.add("source close")
	return s.Source.Close()
}

// prepare reads the staging once, into the spool, and closes that read
// before the import opens: a read still open on the database the import
// replaces waits on its own metadata locks for ever (MySQL, MariaDB).
func TestPrepareClosesItsReadBeforeTheImport(t *testing.T) {
	f := useRunFakes(t)
	inner := f.staging.SourceFunc
	f.staging.SourceFunc = func(tables []string) (core.Source, error) {
		f.rec.add("source open")
		src, err := inner(tables)
		if err != nil {
			return nil, err
		}
		return &recSource{Source: src, rec: f.rec}, nil
	}
	if _, err := Run(context.Background(), f.job(Prepare)); err != nil {
		t.Fatal(err)
	}
	log := f.rec.all()
	opens, sink := 0, slices.Index(log, "sink open")
	if sink < 0 {
		t.Fatalf("no import: %v", log)
	}
	for i, l := range log {
		switch {
		case l == "source open":
			opens++
			if i > sink && i < slices.Index(log, "sink commit") {
				t.Errorf("the staging was read while the import ran: %v", log)
			}
		case l == "source close" && i > sink && i < slices.Index(log, "sink commit"):
			t.Errorf("a read closed only after the import opened: %v", log)
		}
	}
	if opens != 1 {
		t.Errorf("the staging was read %d times, want once: %v", opens, log)
	}
	if len(f.stagSink.Batches()) == 0 {
		t.Error("nothing imported")
	}
}

// The spool gives pass 2 the batches pass 1 read, with the tables of the
// schema; --one-pass (pass 2 first) drains the database before replaying.
func TestSelfInputReplays(t *testing.T) {
	td := wpUsers()
	sch := schemaOf("wp_", td)
	for _, onePass := range []bool{false, true} {
		s := testutil.FakeSite(nil)
		reads := 0
		s.SourceFunc = func([]string) (core.Source, error) {
			reads++
			return testutil.FakeSource(sch, batches(td)), nil
		}
		in := &selfInput{in: &siteInput{s: s}, dir: t.TempDir()}
		ctx := context.Background()
		var want []*core.Batch
		if !onePass {
			src, err := in.open(ctx, 1)
			if err != nil {
				t.Fatal(err)
			}
			want = drainAll(t, src)
			if err := src.Close(); err != nil {
				t.Fatal(err)
			}
		} else {
			want = batches(td)
		}
		src, err := in.open(ctx, 2)
		if err != nil {
			t.Fatal(err)
		}
		got := drainAll(t, src)
		_ = src.Close()
		if reads != 1 {
			t.Errorf("one-pass %v: read the database %d times", onePass, reads)
		}
		if len(got) != len(want) {
			t.Fatalf("one-pass %v: %d batches, want %d", onePass, len(got), len(want))
		}
		for i := range got {
			g, w := got[i], want[i]
			if g.Seq != w.Seq || (g.Table == nil) != (w.Table == nil) || len(g.Rows) != len(w.Rows) {
				t.Fatalf("batch %d: %+v, want %+v", i, g, w)
			}
			if g.Table != nil && g.Table != sch.Table(w.Table.Name) {
				t.Errorf("batch %d: table %s isn't the schema's", i, g.Table.Name)
			}
			for ri := range g.Rows {
				for vi := range g.Rows[ri].Values {
					gv, wv := g.Rows[ri].Values[vi], w.Rows[ri].Values[vi]
					if gv.Kind != wv.Kind || string(gv.Raw) != string(wv.Raw) {
						t.Errorf("batch %d row %d value %d: %q, want %q", i, ri, vi, gv.Raw, wv.Raw)
					}
				}
			}
		}
		if err := in.close(); err != nil {
			t.Fatal(err)
		}
	}
}

func drainAll(t *testing.T, src core.Source) []*core.Batch {
	t.Helper()
	var out []*core.Batch
	for {
		b, err := src.Next(context.Background())
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, b)
	}
}

// Re-preparing a staging copy names its own URL with --url: the staging's
// siteurl is no production URL, so it doesn't map to itself.
func TestOwnURLs(t *testing.T) {
	site := []string{"https://staging.shop.test", "https://staging.shop.test"}
	if got := ownURLs(site, "https://staging.shop.test", []string{"https://shop.com"}); len(got) != 0 {
		t.Errorf("staging kept: %v", got)
	}
	// Without --url, a known production drops a siteurl it doesn't name.
	if got := ownURLs(site, "", []string{"shop.com"}); len(got) != 0 {
		t.Errorf("no --url: %v", got)
	}
	// A fresh restore still calls itself production: that one is rewritten.
	prod := []string{"https://shop.com", "https://www.shop.com"}
	if got := ownURLs(prod, "https://staging.shop.test", []string{"https://shop.com"}); !slices.Equal(got, prod) {
		t.Errorf("production dropped: %v", got)
	}
	if got := ownURLs(prod, "https://staging.shop.test", []string{"*.shop.com"}); !slices.Equal(got, prod) {
		t.Errorf("glob: %v", got)
	}
	if got := ownURLs(prod, "https://staging.shop.test", []string{"*.other.com"}); len(got) != 0 {
		t.Errorf("other glob: %v", got)
	}
	// Production unknown: the siteurl may be production's (the guard
	// asks), so it stays unless it is --url's.
	if got := ownURLs(prod, "https://staging.shop.test", nil); !slices.Equal(got, prod) {
		t.Errorf("unknown production: %v", got)
	}
	plan, err := buildURLPlan(urlSources{SiteURLs: ownURLs(site, "https://staging.shop.test", []string{"https://shop.com"}),
		Production: []string{"https://shop.com"}}, "https://staging.shop.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Pairs) != 1 || plan.Pairs[0] != (urlrw.Pair{From: "https://shop.com", To: "https://staging.shop.test"}) {
		t.Errorf("pairs %v", plan.Pairs)
	}
	if err := checkPairs(plan, parts{}); err != nil {
		t.Errorf("checkPairs: %v", err)
	}
}

// A URL pair the rewrite refuses stops the run in pass 1: no step edits
// a file and the target stays untouched.
func TestURLMapErrorStopsBeforeFileEdits(t *testing.T) {
	f := useRunFakes(t)
	old := newURLMap
	newURLMap = func([]urlrw.Pair) (urlRewriter, error) {
		return nil, core.Errorf(core.ErrConfig, 2, "url map: staging.shop.test maps to itself; the staging URL needs a host of its own")
	}
	t.Cleanup(func() { newURLMap = old })
	res, err := Run(context.Background(), f.job(Prepare))
	if err == nil || !strings.Contains(err.Error(), "maps to itself") {
		t.Fatalf("err %v", err)
	}
	if got := f.applied(); len(got) != 0 {
		t.Errorf("applied %v before the stop", got)
	}
	if res.TargetState != core.Untouched || slices.Contains(f.rec.all(), "sink open") {
		t.Errorf("state %s, log %v", res.TargetState, f.rec.all())
	}
}

// The side effects the kill switch turns off are on the result for plan
// and for a copy, so the screen can name them; verify's read-back
// replaces them, without the config steps it checks alongside.
func TestResultSideEffects(t *testing.T) {
	f := useRunFakes(t)
	res, err := Run(context.Background(), f.job(Plan))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, se := range res.SideEffects {
		ids = append(ids, se.ID+" "+se.Status+" "+se.How)
	}
	want := []string{"side-effect.mail off turn side-effect.mail off", "side-effect.cron off turn side-effect.cron off"}
	if !slices.Equal(ids, want) {
		t.Errorf("plan side effects %q", ids)
	}
	f = useRunFakes(t)
	j := f.job(Copy)
	j.Opts.NoVerify = true
	if res, err = Run(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	if len(res.SideEffects) != 2 || res.Summary().SideEffectsOff != 2 {
		t.Errorf("copy side effects %+v", res.SideEffects)
	}
	f = useRunFakes(t)
	old := runVerify
	runVerify = func(context.Context, verify.Job) (*core.VerifyResult, error) {
		return &core.VerifyResult{Mode: "exact", SideEffects: []core.SideEffect{
			{ID: core.FindingConfigSalts, Status: "off"},
			{ID: core.FindingSideEffectMail, Status: "on", How: "mail sends"},
		}}, nil
	}
	t.Cleanup(func() { runVerify = old })
	if res, err = Run(context.Background(), f.job(Copy)); err != nil {
		t.Fatal(err)
	}
	if len(res.SideEffects) != 1 || res.SideEffects[0].Status != "on" {
		t.Errorf("after verify %+v", res.SideEffects)
	}
}

// Uncovered columns of a truncated table are no finding (PRD 15.2), and
// prepare --only side-effects, which ran no column rule, scans none.
func TestVerifyScanTable(t *testing.T) {
	f := useRunFakes(t)
	j := f.job(Copy)
	j.Opts.Truncate = []string{"wp_usermeta"}
	if _, err := Run(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	if f.verifyJob == nil || f.verifyJob.ScanTable == nil {
		t.Fatal("no ScanTable")
	}
	if f.verifyJob.ScanTable("wp_usermeta") || !f.verifyJob.ScanTable("wp_users") {
		t.Error("ScanTable doesn't follow the truncate")
	}
	f = useRunFakes(t)
	j = f.job(Prepare)
	j.Opts.Only = []string{"side-effects"}
	if _, err := Run(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	if f.verifyJob == nil || f.verifyJob.ScanTable == nil || f.verifyJob.ScanTable("wp_users") {
		t.Error("--only side-effects scans for uncovered columns")
	}
}

// prepare without --url takes the host the staging calls itself, as copy
// does from --url, and names it as the host to confirm by; a siteurl that
// is still production's is never offered.
func TestPrepareConfirmBySiteHost(t *testing.T) {
	for _, c := range []struct {
		siteURL, confirm, wantErr string
	}{
		{"https://staging.shop.test", "staging.shop.test", ""},
		{"https://staging.shop.test", "nope", "Pass --confirm-target staging.shop.test (the site's host)"},
		{"https://shop.com", "shop.com", "Pass --confirm-target /srv/staging"},
	} {
		f := useRunFakes(t)
		old := siteURLOf
		siteURLOf = func(context.Context, core.Site) string { return c.siteURL }
		j := f.job(Prepare)
		j.Opts.URL, j.Opts.ConfirmTarget = "", c.confirm
		_, err := Run(context.Background(), j)
		siteURLOf = old
		switch {
		case c.wantErr == "" && err != nil:
			t.Errorf("%s: %v", c.confirm, err)
		case c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)):
			t.Errorf("%s: %v, want %q", c.confirm, err, c.wantErr)
		}
	}
}
