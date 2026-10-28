// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/dump"
	"github.com/RhythmShahriar/roadie/internal/testutil"
	"github.com/RhythmShahriar/roadie/pseudo"
	"github.com/RhythmShahriar/roadie/roadiecfg"
	"github.com/RhythmShahriar/roadie/site"
	"github.com/RhythmShahriar/roadie/site/killswitch"
	"github.com/RhythmShahriar/roadie/site/remote"
	"github.com/RhythmShahriar/roadie/verify"
)

// recorder keeps the order of what a run did across the steps, the sink
// and the site.
type recorder struct {
	mu  sync.Mutex
	log []string
}

func (r *recorder) add(s string) { r.mu.Lock(); r.log = append(r.log, s); r.mu.Unlock() }
func (r *recorder) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.log)
}

// index is the position of the first entry starting with prefix, -1 when
// none.
func (r *recorder) index(prefix string) int {
	return slices.IndexFunc(r.all(), func(s string) bool { return strings.HasPrefix(s, prefix) })
}

// fakeStep is a kill-switch step that records its calls.
type fakeStep struct {
	id, phase string
	rec       *recorder
	err       error
}

func (s *fakeStep) ID() string    { return s.id }
func (s *fakeStep) Phase() string { return s.phase }
func (s *fakeStep) Plan(context.Context, core.Site) ([]core.PlannedEdit, error) {
	s.rec.add("plan " + s.id)
	return []core.PlannedEdit{{Step: s.id, File: "wp-config.php", What: "turn " + s.id + " off"}}, nil
}
func (s *fakeStep) Apply(context.Context, core.Site) error {
	s.rec.add("apply " + s.id)
	return s.err
}
func (s *fakeStep) Check(context.Context, core.Site) (core.SideEffect, []core.Finding, error) {
	return core.SideEffect{}, nil, nil
}

// logSink is a recording sink that notes its first write and its commit.
type logSink struct {
	*testutil.Sink
	rec   *recorder
	wrote bool
}

func (s *logSink) Write(ctx context.Context, b *core.Batch) error {
	if !s.wrote {
		s.wrote = true
		s.rec.add("sink write")
	}
	return s.Sink.Write(ctx, b)
}

func (s *logSink) Commit(ctx context.Context) error {
	s.rec.add("sink commit")
	return s.Sink.Commit(ctx)
}

// The endpoints of the run tests.
var (
	prodEP    = core.Endpoint{Kind: core.SSHSite, Raw: "ssh://deploy@shop.com/var/www/shop", Display: "deploy@shop.com:/var/www/shop", Host: "shop.com", User: "deploy", Path: "/var/www/shop"}
	stagingEP = core.Endpoint{Kind: core.LocalSite, Raw: "/srv/staging", Display: "/srv/staging", Path: "/srv/staging"}
)

// runFakes is the world of a run test: a production site holding wpUsers,
// a staging site, recording steps and fakes for every other lane.
type runFakes struct {
	*fakes
	rec       *recorder
	prod      *testutil.Site
	staging   *testutil.Site
	stagSink  *logSink
	fileSink  *testutil.Sink
	steps     []*fakeStep
	guards    []site.GuardInput
	guardErr  func(in site.GuardInput) error
	opened    []site.Options
	verifyJob *verify.Job
	dataRules int
	tables    [][]string
	dir       string
}

func useRunFakes(t *testing.T) *runFakes {
	t.Helper()
	f := &runFakes{fakes: useFakes(t), rec: &recorder{}, dir: t.TempDir()}
	f.prod = testutil.FakeSite(map[string][]byte{"wp-config.php": []byte("<?php define('DB_NAME', 'shop');")})
	f.prod.EP, f.prod.RO = prodEP, true
	f.prod.SourceFunc = func([]string) (core.Source, error) {
		f.prod.RecordCommand("wp db export -")
		td := wpUsers()
		return testutil.FakeSource(schemaOf("wp_", td), batches(td)), nil
	}
	f.staging = testutil.FakeSite(nil)
	f.staging.EP = stagingEP
	f.stagSink = &logSink{Sink: testutil.FakeSink(), rec: f.rec}
	f.staging.SinkFunc = func() (core.Sink, error) { f.rec.add("sink open"); return f.stagSink, nil }
	f.staging.SourceFunc = func(tables []string) (core.Source, error) {
		f.tables = append(f.tables, tables)
		td := wpUsers()
		if tables != nil {
			td = slices.DeleteFunc(td, func(d tableData) bool { return !slices.Contains(tables, d.t.Name) })
		}
		return testutil.FakeSource(schemaOf("wp_", td), batches(td)), nil
	}
	f.fileSink = testutil.FakeSink()
	f.steps = []*fakeStep{
		{id: core.FindingSideEffectMail, phase: "files", rec: f.rec},
		{id: core.FindingSideEffectCron, phase: "after", rec: f.rec},
	}
	exp := &fakeStep{id: core.FindingSearchIndexable, phase: "files", rec: f.rec}

	swap := func(restore func()) { t.Cleanup(restore) }
	{
		old := openSite
		openSite = func(_ context.Context, e core.Endpoint, o site.Options) (core.Site, error) {
			f.opened = append(f.opened, o)
			switch e.Raw {
			case prodEP.Raw:
				return f.prod, nil
			case stagingEP.Raw:
				return f.staging, nil
			}
			return nil, fmt.Errorf("no fake site %s", e.Display)
		}
		swap(func() { openSite = old })
	}
	{
		old := guardTarget
		guardTarget = func(_ context.Context, in site.GuardInput) error {
			f.guards = append(f.guards, in)
			f.rec.add("guard")
			if f.guardErr != nil {
				return f.guardErr(in)
			}
			return nil
		}
		swap(func() { guardTarget = old })
	}
	{
		old := lockTarget
		lockTarget = func(core.Endpoint) (func(), error) {
			f.rec.add("lock")
			return func() { f.rec.add("unlock") }, nil
		}
		swap(func() { lockTarget = old })
	}
	{
		old1, old2, old3 := killSteps, killDataRules, exposureSteps
		killSteps = func(core.Stack, killswitch.Options) []core.Step {
			out := make([]core.Step, 0, len(f.steps))
			for _, s := range f.steps {
				out = append(out, s)
			}
			return out
		}
		killDataRules = func(core.Stack, killswitch.Options) []core.Rule {
			f.dataRules++
			return []core.Rule{{ID: "killswitch.options.option_value.admin_email", Profile: "killswitch",
				Table: "{prefix}options", Column: "option_value", Where: map[string]string{"option_name": "admin_email"},
				Action: core.Fixed, Value: "staging@example.invalid"}}
		}
		exposureSteps = func(killswitch.Options) []core.Step { return []core.Step{exp} }
		swap(func() { killSteps, killDataRules, exposureSteps = old1, old2, old3 })
	}
	{
		old := basicAuthFiles
		basicAuthFiles = func(user string) (map[string][]byte, string, error) {
			return map[string][]byte{".htpasswd": []byte(user + ":$2y$hash\n")}, "pw-" + user, nil
		}
		swap(func() { basicAuthFiles = old })
	}
	{
		old1, old2 := runVerify, runSoundcheck
		runVerify = func(_ context.Context, j verify.Job) (*core.VerifyResult, error) {
			f.rec.add("verify")
			f.verifyJob = &j
			mode := "heuristic"
			if j.Known != nil {
				mode = "exact"
			}
			return &core.VerifyResult{Mode: mode}, nil
		}
		runSoundcheck = func(context.Context, string, string) *core.SoundcheckResult {
			return &core.SoundcheckResult{Ran: true}
		}
		swap(func() { runVerify, runSoundcheck = old1, old2 })
	}
	{
		old1, old2 := newFileSink, newStreamSink
		newFileSink = func(string, dump.WriterOptions) (core.Sink, error) { return f.fileSink, nil }
		newStreamSink = func(_ io.Writer, _ dump.WriterOptions) core.Sink { return f.fileSink }
		swap(func() { newFileSink, newStreamSink = old1, old2 })
	}
	{
		old1, old2 := runKey, workDir
		runKey = func(string, *roadiecfg.Config, bool, func(string) (string, bool)) (pseudo.Key, string, bool, error) {
			return pseudo.Key{1, 2, 3}, roadiecfg.KeyFromNew, false, nil
		}
		workDir = func() (string, error) { return f.dir, nil }
		old3 := excludeGit
		excludeGit = func(string) {}
		swap(func() { runKey, workDir, excludeGit = old1, old2, old3 })
	}
	{
		old := wpConfigPairs
		wpConfigPairs = func([]byte) ([]core.Pair, error) {
			return []core.Pair{{Name: "DB_NAME", Value: "shop", Line: 1, Quoted: true}}, nil
		}
		swap(func() { wpConfigPairs = old })
	}
	return f
}

// job is a copy from production to the staging site, confirmed by flag.
func (f *runFakes) job(mode Mode) Job {
	j := Job{Mode: mode, From: prodEP, To: stagingEP, Config: roadiecfg.Default(),
		Opts: Options{URL: "https://staging.shop.test", ConfirmTarget: "staging.shop.test", FailOn: core.SeverityHigh}}
	if mode == Prepare {
		j.From = core.Endpoint{}
		j.Opts.ProductionURL = "https://shop.com"
	}
	return j
}

func (f *runFakes) applied() []string {
	var out []string
	for _, s := range f.rec.all() {
		if strings.HasPrefix(s, "apply ") {
			out = append(out, s)
		}
	}
	return out
}

func errCode(err error) (string, int) {
	var ce *core.Error
	if errors.As(err, &ce) {
		return ce.Code, ce.Exit
	}
	return "", 0
}

// plan reads both sites, plans each step and writes nothing.
func TestRunPlanWritesNothing(t *testing.T) {
	f := useRunFakes(t)
	f.staging.SinkFunc = func() (core.Sink, error) { t.Error("plan opened the target's sink"); return nil, errors.New("no") }
	res, err := Run(context.Background(), f.job(Plan))
	if err != nil {
		t.Fatal(err)
	}
	if w := f.staging.Writes(); len(w) != 0 || f.staging.Backups() != 0 {
		t.Errorf("plan wrote %v, %d backups", w, f.staging.Backups())
	}
	if len(f.applied()) != 0 || f.verifyJob != nil || f.rec.index("lock") >= 0 {
		t.Errorf("plan did %v", f.rec.all())
	}
	for _, o := range f.opened {
		if !o.ReadOnly {
			t.Errorf("plan opened a site writable: %+v", o)
		}
	}
	if len(res.PlannedEdits) != 3 || res.Kind != "plan" || res.TargetState != core.Untouched {
		t.Errorf("planned %+v kind %s state %s", res.PlannedEdits, res.Kind, res.TargetState)
	}
	if len(res.Tables) == 0 || !slices.Contains(res.RemoteCommands, "wp db export -") {
		t.Errorf("tables %d remote %v", len(res.Tables), res.RemoteCommands)
	}
	// The source is reached over ssh: the plan lists the source's
	// allowlist too; the local staging adds no target commands.
	for _, c := range remote.List(false) {
		if !slices.Contains(res.RemoteCommands, c) {
			t.Errorf("plan lacks %q", c)
		}
	}
	if len(res.RemoteCommands) != len(remote.List(false))+1 {
		t.Errorf("remote commands %q", res.RemoteCommands)
	}
}

// copy to a dump file writes, commits and verifies the file in exact
// mode with production's wp-config.php pairs.
func TestRunCopyToDumpFile(t *testing.T) {
	f := useRunFakes(t)
	out := filepath.Join(t.TempDir(), "staging.sql")
	if err := os.WriteFile(out, []byte("-- written\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	j := f.job(Copy)
	j.To = core.Endpoint{Kind: core.DumpFile, Raw: out, Display: "staging.sql", Path: out}
	j.Opts.ConfirmTarget, j.Opts.Soundcheck = "", true
	res, err := Run(context.Background(), j)
	if err != nil {
		t.Fatal(err)
	}
	if res.TargetState != core.Replaced || !f.fileSink.Committed() {
		t.Errorf("state %s committed %v", res.TargetState, f.fileSink.Committed())
	}
	if v, _ := valueOf(t, f.fileSink, "wp_users", 0, "user_email"); !strings.HasSuffix(v, "@example.invalid") {
		t.Errorf("email %q", v)
	}
	v := f.verifyJob
	// A dump has no step of its own: it gets the stack's kill-switch
	// steps, for verify to run those that read only rows.
	if v == nil || v.Known == nil || v.Target.Raw != out || v.Site != nil || len(v.Rules) == 0 || len(v.Steps) == 0 {
		t.Fatalf("verify job %+v", v)
	}
	if len(v.ProductionPairs) != 1 || v.ProductionPairs[0].Name != "DB_NAME" {
		t.Errorf("production pairs %+v", v.ProductionPairs)
	}
	if res.Verify == nil || res.Verify.Mode != "exact" || res.Verify.Soundcheck == nil {
		t.Errorf("verify %+v", res.Verify)
	}
	for _, fd := range res.Verify.Findings {
		if fd.ID == core.FindingSoundcheckFailed {
			t.Errorf("a Soundcheck that exited 0 gave %+v", fd)
		}
	}
	if !v.Opts.EchoSince.Equal(res.Started) {
		t.Errorf("EchoSince %v, want the copy's start %v", v.Opts.EchoSince, res.Started)
	}
	if len(res.NotCovered) != 1 || res.NotCovered[0] != NotCovered {
		t.Errorf("not covered %q", res.NotCovered)
	}
	if res.Output == nil || res.Output.Kind != "sha256-file" || res.ConfigSHA256 == "" || res.RulesetSHA256 == "" || res.ProfilePack == "" {
		t.Errorf("output %+v config %q ruleset %q pack %q", res.Output, res.ConfigSHA256, res.RulesetSHA256, res.ProfilePack)
	}
	if res.EchoConnectorSHA256 != "" || res.Consistency.Mode != "per-run" {
		t.Errorf("echo %q consistency %+v", res.EchoConnectorSHA256, res.Consistency)
	}
	if got := f.rec.all(); !slices.Equal(got[:2], []string{"lock", "guard"}) || got[len(got)-1] != "unlock" {
		t.Errorf("order %v", got)
	}
	// --no-verify: no verify at all.
	f.verifyJob = nil
	j.Opts.NoVerify = true
	if res, err := Run(context.Background(), j); err != nil || res.Verify != nil || f.verifyJob != nil {
		t.Errorf("--no-verify: %v %+v", err, res.Verify)
	}
}

// A site target without a terminal needs --confirm-target naming it;
// --yes alone isn't enough.
func TestRunConfirmTarget(t *testing.T) {
	for _, mode := range []Mode{Copy, Prepare} {
		cases := []struct {
			name, target string
			yes, ok      bool
		}{
			{"none", "", false, false},
			{"yes alone", "", true, false},
			{"wrong", "shop.com", true, false},
			{"host", "staging.shop.test", false, true},
			{"case", "STAGING.shop.test", false, true},
		}
		for _, c := range cases {
			t.Run(string(mode)+" "+c.name, func(t *testing.T) {
				f := useRunFakes(t)
				j := f.job(mode)
				j.Opts.ConfirmTarget, j.Opts.Yes = c.target, c.yes
				res, err := Run(context.Background(), j)
				if c.ok {
					if err != nil {
						t.Fatal(err)
					}
					return
				}
				// copy has its own code: prepare-confirm would name the
				// wrong command.
				want := core.ErrPrepareConfirm
				if mode == Copy {
					want = core.ErrConfirmTarget
				}
				if code, exit := errCode(err); code != want || exit != 2 || !strings.HasPrefix(err.Error(), want+": ") {
					t.Fatalf("err %v", err)
				}
				if len(f.staging.Writes()) != 0 || f.staging.Backups() != 0 || f.rec.index("sink") >= 0 || len(f.applied()) != 0 {
					t.Errorf("refused run still did %v", f.rec.all())
				}
				if res.Err == nil || res.Err.State != core.Untouched {
					t.Errorf("result err %+v", res.Err)
				}
				if c.target == "" && !strings.Contains(err.Error(), "Pass --confirm-target staging.shop.test (the site's host) to go on.") {
					t.Errorf("message %q", err)
				}
			})
		}
	}
}

// On a terminal prepare asks PRD 16.1's question with the host as the
// expected answer, even with --yes; copy asks to replace unless --yes.
func TestRunConfirmQuestion(t *testing.T) {
	f := useRunFakes(t)
	var asked []string
	answer := true
	confirm := func(q, expect string) (bool, error) { asked = append(asked, q+"|"+expect); return answer, nil }
	j := f.job(Prepare)
	j.Confirm, j.Opts.ConfirmTarget, j.Opts.Yes = confirm, "", true
	if _, err := Run(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	want := "prepare rewrites this site's database while it runs. Visitors see a maintenance page for about a minute. Type the site's host to go on (staging.shop.test):|staging.shop.test"
	if len(asked) != 1 || asked[0] != want {
		t.Errorf("asked %q", asked)
	}
	asked, answer = nil, false
	if _, err := Run(context.Background(), j); !isCode(err, core.ErrPrepareConfirm) {
		t.Errorf("a wrong answer went on: %v", err)
	}
	j = f.job(Copy)
	j.Confirm, j.Opts.ConfirmTarget = confirm, ""
	asked = nil
	if _, err := Run(context.Background(), j); !isCode(err, core.ErrConfirmTarget) || len(asked) != 1 || asked[0] != "Replace the database of /srv/staging? [y/N]|" {
		t.Errorf("copy asked %q: %v", asked, err)
	}
	asked, j.Opts.Yes = nil, true
	if _, err := Run(context.Background(), j); err != nil || len(asked) != 0 {
		t.Errorf("--yes still asked %q: %v", asked, err)
	}
}

// prepare --only side-effects exports only the kill switch's tables,
// applies only the kill-switch steps and changes only what its data
// rules say.
func TestRunOnlySideEffects(t *testing.T) {
	f := useRunFakes(t)
	j := f.job(Prepare)
	j.Opts.Only = []string{"side-effects"}
	res, err := Run(context.Background(), j)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"wp_options", "wp_wc_webhooks", "wp_automatewoo_queue"}
	if len(f.tables) == 0 {
		t.Fatal("no export")
	}
	for _, tb := range f.tables {
		// The first read looks the multisite blogs up.
		if !slices.Equal(tb, want) && !slices.Equal(tb, []string{"wp_blogs"}) {
			t.Errorf("exported %v, want %v", tb, want)
		}
	}
	if got := f.applied(); !slices.Equal(got, []string{"apply side-effect.mail", "apply side-effect.cron"}) {
		t.Errorf("applied %v", got)
	}
	for _, b := range f.stagSink.Batches() {
		if b.Table != nil && b.Table.Name != "wp_options" {
			t.Errorf("wrote %s", b.Table.Name)
		}
	}
	if v, _ := valueOf(t, f.stagSink.Sink, "wp_options", 2, "option_value"); v != "staging@example.invalid" {
		t.Errorf("data rule: %q", v)
	}
	// No URL rewrite and no key swap in this part.
	if v, _ := valueOf(t, f.stagSink.Sink, "wp_options", 0, "option_value"); v != "https://shop.com" {
		t.Errorf("siteurl changed: %q", v)
	}
	if v, _ := valueOf(t, f.stagSink.Sink, "wp_options", 4, "option_value"); !strings.Contains(v, liveKey) {
		t.Errorf("key swapped: %q", v)
	}
	if len(res.Uncovered) != 0 || f.staging.Backups() != 1 {
		t.Errorf("uncovered %v backups %d", res.Uncovered, f.staging.Backups())
	}
	// --only noindex: no export, no import, only the exposure step.
	f = useRunFakes(t)
	j.Opts.Only = []string{"noindex"}
	if _, err := Run(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	if got := f.applied(); !slices.Equal(got, []string{"apply search.indexable"}) || len(f.tables) != 0 || f.staging.Backups() != 0 {
		t.Errorf("--only noindex did %v, exported %v", f.rec.all(), f.tables)
	}
	j.Opts.Only = []string{"data,nope"}
	if _, err := Run(context.Background(), j); !isCode(err, core.ErrConfig) {
		t.Errorf("bad part: %v", err)
	}
}

// On a multisite, prepare --only side-effects exports every blog's kill
// switch tables, so blog 2's live settings are switched off as well as
// the main site's.
func TestRunOnlySideEffectsMultisite(t *testing.T) {
	f := useRunFakes(t)
	killDataRules = func(core.Stack, killswitch.Options) []core.Rule {
		return []core.Rule{{ID: "killswitch.options.option_value.admin_email", Profile: "killswitch",
			Table: "{prefix}{site}options", Column: "option_value", Where: map[string]string{"option_name": "admin_email"},
			Action: core.Fixed, Value: "staging@example.invalid"}}
	}
	network := func() []tableData {
		td := blogTables()
		for i := range td {
			if strings.HasSuffix(td[i].t.Name, "options") {
				td[i].rows = append(td[i].rows, []any{9, "admin_email", "owner@shop.com"})
			}
		}
		return td
	}
	f.staging.SourceFunc = func(tables []string) (core.Source, error) {
		f.tables = append(f.tables, tables)
		td := slices.DeleteFunc(network(), func(d tableData) bool { return !slices.Contains(tables, d.t.Name) })
		return testutil.FakeSource(schemaOf("wp_", td), batches(td)), nil
	}
	j := f.job(Prepare)
	j.Opts.Only = []string{"side-effects"}
	if _, err := Run(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	want := []string{"wp_options", "wp_wc_webhooks", "wp_automatewoo_queue",
		"wp_2_options", "wp_2_wc_webhooks", "wp_2_automatewoo_queue",
		"wp_3_options", "wp_3_wc_webhooks", "wp_3_automatewoo_queue"}
	if !slices.ContainsFunc(f.tables, func(tb []string) bool { return slices.Equal(tb, want) }) {
		t.Errorf("exported %v, want %v", f.tables, want)
	}
	for _, tb := range []string{"wp_options", "wp_2_options", "wp_3_options"} {
		if v, _ := valueOf(t, f.stagSink.Sink, tb, 1, "option_value"); v != "staging@example.invalid" {
			t.Errorf("%s admin_email %q", tb, v)
		}
	}
	for _, b := range f.stagSink.Batches() {
		if b.Table != nil && strings.HasSuffix(b.Table.Name, "comments") {
			t.Errorf("wrote %s", b.Table.Name)
		}
	}
}

// A single site has no blogs table: only the main prefix is listed.
// A failing read is an error, not a silent single-site guess.
func TestBlogPrefixes(t *testing.T) {
	s := testutil.FakeSite(nil)
	s.SourceFunc = func(tables []string) (core.Source, error) {
		td := slices.DeleteFunc(wpUsers(), func(d tableData) bool { return !slices.Contains(tables, d.t.Name) })
		return testutil.FakeSource(schemaOf("wp_", td), batches(td)), nil
	}
	got, err := blogPrefixes(context.Background(), s, "wp_")
	if err != nil || !slices.Equal(got, []string{"wp_"}) {
		t.Errorf("single site %v %v", got, err)
	}
	s.SourceFunc = func([]string) (core.Source, error) {
		td := []tableData{{table("wp_blogs", "blog_id:bigint(20)", "domain"), rows{{11, "b.shop.com"}, {1, "shop.com"}, {2, "a.shop.com"}, {2, "dup.shop.com"}}}}
		return testutil.FakeSource(schemaOf("wp_", td), batches(td)), nil
	}
	got, err = blogPrefixes(context.Background(), s, "wp_")
	if err != nil || !slices.Equal(got, []string{"wp_", "wp_2_", "wp_11_"}) {
		t.Errorf("network %v %v", got, err)
	}
	s.SourceFunc = func([]string) (core.Source, error) { return nil, errors.New("connection refused") }
	if _, err := blogPrefixes(context.Background(), s, "wp_"); err == nil {
		t.Error("no error from a failing read")
	}
}

// The file-level kill switch runs before the first byte reaches the
// target, the after steps after the commit; prepare backs up first and
// shows the maintenance page around the import.
func TestRunStepOrder(t *testing.T) {
	f := useRunFakes(t)
	if _, err := Run(context.Background(), f.job(Copy)); err != nil {
		t.Fatal(err)
	}
	mail, write, commit, cron := f.rec.index("apply side-effect.mail"), f.rec.index("sink write"), f.rec.index("sink commit"), f.rec.index("apply side-effect.cron")
	if mail < 0 || write < 0 || mail > write || write > commit || commit > cron {
		t.Errorf("order %v", f.rec.all())
	}
	if slices.Contains(f.staging.Writes(), "write .maintenance") {
		t.Error("copy wrote .maintenance")
	}

	f = useRunFakes(t)
	f.staging.SinkFunc = func() (core.Sink, error) {
		return &maintenanceSink{logSink: f.stagSink, t: t, site: f.staging}, nil
	}
	res, err := Run(context.Background(), f.job(Prepare))
	if err != nil {
		t.Fatal(err)
	}
	if w := f.staging.Writes(); !slices.Equal(w, []string{"write .maintenance", "remove .maintenance"}) {
		t.Errorf("writes %v", w)
	}
	if res.Backup != f.staging.BackupPath || res.Restore != f.staging.BackupRestore {
		t.Errorf("backup %q restore %q", res.Backup, res.Restore)
	}
	if len(f.guards) != 1 || f.guards[0].Mode != "prepare" || f.guards[0].FromSite != f.guards[0].ToSite {
		t.Errorf("guard %+v", f.guards)
	}
	if len(res.NotCovered) != 2 || res.NotCovered[1] != "Run rhythm tuner scan /srv/staging for file-level leaks." {
		t.Errorf("not covered %q", res.NotCovered)
	}
	if res.EchoConnectorSHA256 == "" {
		t.Error("no Echo connector hash")
	}
	// --no-backup skips it; a failing files step stops before the import
	// and still removes nothing it didn't write.
	f = useRunFakes(t)
	f.steps[0].err = errors.New("permission denied")
	j := f.job(Prepare)
	j.Opts.NoBackup = true
	res, err = Run(context.Background(), j)
	if code, exit := errCode(err); code != core.ErrKillSwitch || exit != 1 {
		t.Fatalf("err %v", err)
	}
	if f.staging.Backups() != 0 || f.rec.index("sink write") >= 0 || !f.stagSink.Aborted() || res.TargetState != core.Untouched {
		t.Errorf("after a failed step: %v", f.rec.all())
	}
	if !strings.HasPrefix(err.Error(), "side-effect.mail: couldn't turn it off on /srv/staging (permission denied).") {
		t.Errorf("message %q", err)
	}
}

// backupSite is the staging fake with a Backup that fails.
type backupSite struct {
	*testutil.Site
	err error
}

func (s backupSite) Backup(context.Context) (string, string, error) { return "", "", s.err }

// A failed prepare backup stops the run with a typed error before
// anything is written: a plain cause as site-config, exit 3, a typed one
// with its own code.
func TestRunBackupFails(t *testing.T) {
	for _, tc := range []struct {
		cause     error
		code      string
		exit      int
		msgPrefix string
	}{
		{errors.New("disk quota exceeded"), core.ErrSiteConfig, 3, "backup of /srv/staging: disk quota exceeded. Fix it, or pass --no-backup"},
		{core.Errorf(core.ErrWPCLI, 3, "wp-cli: wp isn't on PATH."), core.ErrWPCLI, 3, "backup of /srv/staging: wp-cli: wp isn't on PATH. Fix it"},
	} {
		f := useRunFakes(t)
		old := openSite
		openSite = func(ctx context.Context, e core.Endpoint, o site.Options) (core.Site, error) {
			s, err := old(ctx, e, o)
			if s == core.Site(f.staging) {
				return backupSite{Site: f.staging, err: tc.cause}, err
			}
			return s, err
		}
		res, err := Run(context.Background(), f.job(Prepare))
		openSite = old
		if code, exit := errCode(err); code != tc.code || exit != tc.exit {
			t.Fatalf("err %v: code %q exit %d", err, code, exit)
		}
		if !strings.HasPrefix(err.Error(), tc.msgPrefix) {
			t.Errorf("message %q", err)
		}
		if res.Err == nil || res.Err.Code != tc.code || res.TargetState != core.Untouched {
			t.Errorf("result err %+v state %s", res.Err, res.TargetState)
		}
		if len(f.staging.Writes()) != 0 || f.rec.index("sink") >= 0 {
			t.Errorf("wrote after a failed backup: %v", f.rec.all())
		}
	}
}

// A guard that refuses leaves the target as it was.
func TestRunGuardRefusal(t *testing.T) {
	f := useRunFakes(t)
	f.guardErr = func(site.GuardInput) error {
		return core.Errorf(core.ErrTargetIsProduction, 3, "target-is-production: /srv/staging is shop.com, which production: lists.")
	}
	res, err := Run(context.Background(), f.job(Prepare))
	if code, exit := errCode(err); code != core.ErrTargetIsProduction || exit != 3 {
		t.Fatalf("err %v", err)
	}
	if len(f.staging.Writes()) != 0 || f.staging.Backups() != 0 || f.rec.index("sink") >= 0 || len(f.applied()) != 0 {
		t.Errorf("refused run did %v", f.rec.all())
	}
	if res.Err == nil || res.Err.State != core.Untouched || f.rec.index("unlock") < 0 {
		t.Errorf("result %+v log %v", res.Err, f.rec.all())
	}
}

// A lock held by another run stops with lock, exit 3.
func TestRunLockHeld(t *testing.T) {
	f := useRunFakes(t)
	held := core.Errorf(core.ErrLock, 3, "lock: another Roadie run is copying to /srv/staging (pid 4120, started 14:02).")
	lockTarget = func(core.Endpoint) (func(), error) { return nil, held }
	_, err := Run(context.Background(), f.job(Copy))
	if code, exit := errCode(err); code != core.ErrLock || exit != 3 {
		t.Fatalf("err %v", err)
	}
	if len(f.guards) != 0 || len(f.staging.Writes()) != 0 {
		t.Error("ran past the lock")
	}
	lockTarget = func(core.Endpoint) (func(), error) { return nil, errors.New("read-only file system") }
	if _, err := Run(context.Background(), f.job(Copy)); !isCode(err, core.ErrLock) {
		t.Errorf("plain lock error: %v", err)
	}
}

// target-holds-production asks once on a terminal: yes takes the URL as
// production's, anything else stops.
func TestRunHoldsProduction(t *testing.T) {
	holds := func(in site.GuardInput) error {
		if in.ProductionURL == "https://shop.com" {
			return nil
		}
		return core.Errorf(core.ErrTargetHoldsProduction, 3,
			"target-holds-production: /srv/staging calls itself https://shop.com; Roadie can't tell whether that is the live site. Pass --production-url <production URL> (the live site's URL) so Roadie knows which URL is live, or --url https://shop.com if it is this staging's own URL.")
	}
	for _, yes := range []bool{true, false} {
		f := useRunFakes(t)
		f.guardErr = holds
		var asked []string
		j := f.job(Prepare)
		j.Opts.ProductionURL = ""
		j.Confirm = func(q, expect string) (bool, error) {
			asked = append(asked, q+"|"+expect)
			if expect == "yes" {
				return yes, nil
			}
			return true, nil
		}
		res, err := Run(context.Background(), j)
		want := "/srv/staging calls itself https://shop.com. Is https://shop.com the live site? Type yes to go on:|yes"
		if len(asked) == 0 || asked[0] != want {
			t.Fatalf("asked %q", asked)
		}
		if yes {
			if err != nil || len(f.guards) != 2 || !slices.Contains(res.Production, "https://shop.com") && f.guards[1].ProductionURL != "https://shop.com" {
				t.Errorf("yes: %v guards %d", err, len(f.guards))
			}
			continue
		}
		if !isCode(err, core.ErrTargetHoldsProduction) || len(f.staging.Writes()) != 0 {
			t.Errorf("no: %v writes %v", err, f.staging.Writes())
		}
	}
	// Without a terminal the guard's refusal stands.
	f := useRunFakes(t)
	f.guardErr = holds
	j := f.job(Prepare)
	j.Opts.ProductionURL = ""
	if _, err := Run(context.Background(), j); !isCode(err, core.ErrTargetHoldsProduction) {
		t.Errorf("no terminal: %v", err)
	}
}

// A URL read from the target's own data loses control and format
// characters before the question shows it.
func TestRunHoldsProductionPrintable(t *testing.T) {
	esc := string(rune(0x1b))
	f := useRunFakes(t)
	f.guardErr = func(site.GuardInput) error {
		return core.Errorf(core.ErrTargetHoldsProduction, 3, "%s",
			"target-holds-production: /srv/staging still calls itself https://sh"+esc+"]0;pwned\a"+string(rune(0x202e))+"op.com. Pass --production-url.")
	}
	var asked string
	j := f.job(Prepare)
	j.Opts.ProductionURL = ""
	j.Confirm = func(q, expect string) (bool, error) {
		if expect == "yes" {
			asked = q
		}
		return false, nil
	}
	_, _ = Run(context.Background(), j)
	if asked != "/srv/staging calls itself https://sh]0;pwnedop.com. Is https://sh]0;pwnedop.com the live site? Type yes to go on:" {
		t.Errorf("asked %q", asked)
	}
	for in, want := range map[string]string{"https://a.test": "https://a.test", "  https://b.test\n": "https://b.test",
		"https://c.test x": "https://c.test", esc + "[31m" + esc + "[0m": "[31m[0m", "": "", strings.Repeat("é", 400): strings.Repeat("é", 256)} {
		if got := printable(in); got != want {
			t.Errorf("printable(%q) = %q, want %q", in, got, want)
		}
	}
}

// --no-kill-switch applies no step and joins no data rule; verify still
// checks every step.
func TestRunNoKillSwitch(t *testing.T) {
	f := useRunFakes(t)
	j := f.job(Copy)
	j.Opts.NoKillSwitch = true
	res, err := Run(context.Background(), j)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.applied()) != 0 || f.dataRules != 0 || res.EchoConnectorSHA256 != "" {
		t.Errorf("applied %v data rules %d", f.applied(), f.dataRules)
	}
	if f.verifyJob == nil || len(f.verifyJob.Steps) != 3 || f.verifyJob.Site == nil {
		t.Errorf("verify job %+v", f.verifyJob)
	}
	if v, _ := valueOf(t, f.stagSink.Sink, "wp_options", 2, "option_value"); v == "staging@example.invalid" {
		t.Error("a data rule ran with --no-kill-switch")
	}
}

// --basic-auth writes the snippets under .roadie/basic-auth/ and keeps
// the password for the screen only.
func TestRunBasicAuth(t *testing.T) {
	f := useRunFakes(t)
	j := f.job(Copy)
	j.Opts.BasicAuth = "team"
	res, err := Run(context.Background(), j)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(f.dir, ".roadie", "basic-auth", ".htpasswd")
	fi, err := os.Stat(p)
	if err != nil || res.BasicAuthPassword != "pw-team" {
		t.Fatalf("basic auth %v %v %q", fi, err, res.BasicAuthPassword)
	}
	// Windows reports 0666 for any writable file, so only Unix modes are
	// checked.
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf(".htpasswd mode %v, want 0600", fi.Mode().Perm())
	}
}

// The key and the basic-auth files go to Job.ProjectDir, the config's
// project, not the folder the run started in; both are kept out of git.
func TestRunProjectDir(t *testing.T) {
	f := useRunFakes(t)
	project := t.TempDir()
	var keyDirs, excluded []string
	old1, old2 := runKey, excludeGit
	runKey = func(dir string, _ *roadiecfg.Config, _ bool, _ func(string) (string, bool)) (pseudo.Key, string, bool, error) {
		keyDirs = append(keyDirs, dir)
		return pseudo.Key{1, 2, 3}, roadiecfg.KeyFromNew, true, nil
	}
	excludeGit = func(dir string) { excluded = append(excluded, dir) }
	t.Cleanup(func() { runKey, excludeGit = old1, old2 })
	j := f.job(Copy)
	j.Opts.BasicAuth = "team"
	j.ProjectDir = project
	if _, err := Run(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(keyDirs, []string{project}) || !slices.Equal(excluded, []string{project, project}) {
		t.Errorf("key in %q, excluded %q; want %s", keyDirs, excluded, project)
	}
	if _, err := os.Stat(filepath.Join(project, ".roadie", "basic-auth", ".htpasswd")); err != nil {
		t.Error(err)
	}
	if _, err := os.Stat(filepath.Join(f.dir, ".roadie", "basic-auth")); err == nil {
		t.Error("basic-auth files in the working folder")
	}
}

// Bad endpoints stop before anything opens.
func TestRunEndpoints(t *testing.T) {
	f := useRunFakes(t)
	cases := []Job{
		{Mode: Copy, To: stagingEP},
		{Mode: Copy, From: prodEP},
		{Mode: Plan},
		{Mode: Prepare, To: core.Endpoint{Kind: core.DumpFile, Display: "x.sql"}},
		{Mode: "bogus", From: prodEP, To: stagingEP},
	}
	for _, j := range cases {
		if _, err := Run(context.Background(), j); !isCode(err, core.ErrConfig) {
			t.Errorf("%s %v -> %v: %v", j.Mode, j.From.Kind, j.To.Kind, err)
		}
	}
	if len(f.opened) != 0 {
		t.Errorf("opened %d sites", len(f.opened))
	}
	_, err := Run(context.Background(), Job{Mode: Prepare, To: core.Endpoint{Kind: core.DumpFile, Display: "shop.sql"}})
	if want := "prepare needs a staging site or database (a site folder, ssh://user@host/path, @alias or DSN); shop.sql is a dump file. Use rhythm-roadie copy for dump files."; err == nil || err.Error() != want {
		t.Errorf("prepare a dump: %v", err)
	}
}

// VerifyJob compiles the target's rules and, with from, a sealed Known
// and production's config pairs; without from Known is nil.
func TestVerifyJob(t *testing.T) {
	f := useRunFakes(t)
	j := Job{Config: roadiecfg.Default(), Opts: Options{FailOn: core.SeverityHigh}}
	vj, err := VerifyJob(context.Background(), j, stagingEP, nil)
	if err != nil {
		t.Fatal(err)
	}
	if vj.Known != nil || vj.ProductionPairs != nil || len(vj.Rules) == 0 || len(vj.Steps) != 3 || vj.Site != nil || vj.Target.Raw != stagingEP.Raw {
		t.Errorf("heuristic job %+v", vj)
	}
	if len(f.staging.Writes()) != 0 {
		t.Error("verify wrote")
	}
	from := prodEP
	vj, err = VerifyJob(context.Background(), j, stagingEP, &from)
	if err != nil {
		t.Fatal(err)
	}
	if vj.Known == nil {
		t.Fatal("no Known")
	}
	if _, _, ok := vj.Known.Contains([]byte("ben@example.com")); !ok {
		t.Error("Known lacks production's email")
	}
	if len(vj.ProductionPairs) != 1 || !slices.Contains(vj.Production, "shop.com") {
		t.Errorf("pairs %+v production %v", vj.ProductionPairs, vj.Production)
	}
	for _, o := range f.opened {
		if !o.ReadOnly {
			t.Errorf("verify opened a site writable: %+v", o)
		}
	}
	if len(f.prod.Writes()) != 0 {
		t.Error("production written")
	}
}

// blogTables is a network with blogs 2 and 3, each with its own options
// and comments.
func blogTables() []tableData {
	out := []tableData{
		{table("wp_blogs", "blog_id:bigint(20)", "domain", "path"), rows{{1, "shop.com", "/"}, {2, "fr.shop.com", "/"}, {3, "de.shop.com", "/"}}},
		{table("wp_options", "option_id:bigint(20)", "option_name", "option_value"), rows{{1, "siteurl", "https://shop.com"}}},
	}
	for _, b := range []string{"2", "3"} {
		out = append(out,
			tableData{table("wp_"+b+"_options", "option_id:bigint(20)", "option_name", "option_value"), rows{{1, "siteurl", "https://" + map[string]string{"2": "fr", "3": "de"}[b] + ".shop.com"}}},
			tableData{table("wp_"+b+"_comments", "comment_ID:bigint(20)", "comment_author_email"), rows{{1, "anna.lee@example.com"}}})
	}
	return out
}

// copy --site drops every other blog's tables; a blog the network
// doesn't have is refused; --one-pass can't do it.
func TestPipelineSites(t *testing.T) {
	useFakes(t)
	p, sink, _ := fixture(t, Copy, "wp_", blogTables, nil, Options{URL: "https://staging.shop.test", Sites: []int{2}})
	out := run(t, p)
	for _, b := range sink.Batches() {
		if b.Table != nil && strings.HasPrefix(b.Table.Name, "wp_3_") {
			t.Errorf("blog 3 table %s written", b.Table.Name)
		}
		if b.Pass != nil && strings.Contains(string(b.Pass.Text), "wp_3_") {
			t.Errorf("blog 3 statement written: %s", b.Pass.Text)
		}
	}
	if v, _ := valueOf(t, sink, "wp_2_comments", 0, "comment_author_email"); !strings.HasSuffix(v, "@example.invalid") {
		t.Errorf("blog 2 comment %q", v)
	}
	actions := map[string]string{}
	for _, tr := range out.Result.Tables {
		actions[tr.Name] = tr.Action
	}
	if actions["wp_3_options"] != string(core.DropTable) || actions["wp_3_comments"] != string(core.DropTable) || actions["wp_2_options"] == string(core.DropTable) {
		t.Errorf("table actions %v", actions)
	}

	p, _, _ = fixture(t, Copy, "wp_", blogTables, nil, Options{URL: "https://staging.shop.test", Sites: []int{7}})
	_, err := p.run(context.Background())
	if !isCode(err, core.ErrConfig) || err.Error() != "--site 7: this network has no blog 7. Its blogs are 1, 2 and 3." {
		t.Errorf("unknown blog: %v", err)
	}
	p, _, _ = fixture(t, Copy, "wp_", blogTables, nil, Options{OnePass: true, Sites: []int{2}})
	if _, err := p.run(context.Background()); !isCode(err, core.ErrConfig) {
		t.Errorf("one-pass with --site: %v", err)
	}
}

// A copy whose source is its target goes to the guards as a copy, so
// target-is-source can refuse it; a plan of one staging is a prepare.
func TestRunSamePlace(t *testing.T) {
	f := useRunFakes(t)
	f.guardErr = func(in site.GuardInput) error {
		if in.Mode == "copy" && in.From.Raw == in.To.Raw {
			return core.Errorf(core.ErrTargetIsSource, 3, "target-is-source: --from and --to both reach /srv/staging.")
		}
		return nil
	}
	j := f.job(Copy)
	j.From = stagingEP
	if _, err := Run(context.Background(), j); !isCode(err, core.ErrTargetIsSource) {
		t.Errorf("copy onto its source: %v", err)
	}
	j = f.job(Plan)
	j.From = stagingEP
	if _, err := Run(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	if g := f.guards[len(f.guards)-1]; g.Mode != "prepare" || g.FromSite != g.ToSite {
		t.Errorf("plan of a staging guarded as %+v", g)
	}
}

// An uncovered column stops copy and prepare after pass 1 and before the
// kill switch, the maintenance page or the sink touch the target.
func TestRunUncoveredLeavesTarget(t *testing.T) {
	for _, mode := range []Mode{Copy, Prepare} {
		t.Run(string(mode), func(t *testing.T) {
			f := useRunFakes(t)
			detectUncovered = func(tb *core.Table, covered map[string]bool, _ map[string][][]byte) []core.Uncovered {
				for _, c := range tb.Columns {
					if !covered[c.Name] {
						return []core.Uncovered{{Table: tb.Name, Column: c.Name, Rate: 40}}
					}
				}
				return nil
			}
			res, err := Run(context.Background(), f.job(mode))
			if code, exit := errCode(err); code != core.ErrUncovered || exit != 1 {
				t.Fatalf("err %v", err)
			}
			if len(f.applied()) != 0 || f.rec.index("sink") >= 0 || len(f.staging.Writes()) != 0 {
				t.Errorf("an uncovered stop still did %v, writes %v", f.rec.all(), f.staging.Writes())
			}
			if res.TargetState != core.Untouched || res.Err == nil || res.Err.State != core.Untouched {
				t.Errorf("state %s err %+v", res.TargetState, res.Err)
			}
		})
	}
}

// A target database that can't be reached stops the run before a file
// of the site is edited: the target is untouched. A run that stops after
// a step changed the target says mixed: the kill switch edited the site.
func TestRunStoppedAfterStepIsMixed(t *testing.T) {
	f := useRunFakes(t)
	f.staging.SinkFunc = func() (core.Sink, error) { return nil, errors.New("access denied for user 'staging'") }
	res, err := Run(context.Background(), f.job(Copy))
	if err == nil || !strings.Contains(err.Error(), "access denied") {
		t.Fatalf("err %v", err)
	}
	if got := f.applied(); len(got) != 0 || len(f.staging.Writes()) != 0 || res.TargetState != core.Untouched {
		t.Errorf("unreachable database: applied %v writes %v state %s", got, f.staging.Writes(), res.TargetState)
	}

	f = useRunFakes(t)
	f.stagSink.WriteErr = errors.New("connection lost")
	res, err = Run(context.Background(), f.job(Copy))
	if err == nil || !strings.Contains(err.Error(), "connection lost") {
		t.Fatalf("err %v", err)
	}
	if got := f.applied(); len(got) == 0 || res.TargetState != core.Mixed || res.Target.State != core.Mixed {
		t.Errorf("applied %v state %s", got, res.TargetState)
	}
}

// maintenanceSink checks that .maintenance is up when the first batch
// reaches the target.
type maintenanceSink struct {
	*logSink
	t    *testing.T
	site *testutil.Site
}

func (s *maintenanceSink) Write(ctx context.Context, b *core.Batch) error {
	if _, err := s.site.ReadFile(ctx, ".maintenance"); err != nil {
		s.t.Error("no .maintenance during the import")
	}
	return s.logSink.Write(ctx, b)
}

// prepare reads the staging it writes, never a source: from the config.
func TestRunPrepareIgnoresSource(t *testing.T) {
	f := useRunFakes(t)
	f.prod.SourceFunc = func([]string) (core.Source, error) {
		t.Error("prepare read production")
		return nil, errors.New("no")
	}
	j := f.job(Prepare)
	j.From = prodEP
	res, err := Run(context.Background(), j)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.guards) != 1 || f.guards[0].From.Raw != stagingEP.Raw || f.guards[0].FromSite != f.guards[0].ToSite {
		t.Errorf("guard %+v", f.guards)
	}
	if len(f.opened) != 2 || res.Source.Display != stagingEP.Display {
		t.Errorf("opened %d sites, source %+v", len(f.opened), res.Source)
	}
}

// prepare --only noindex reads no data, yet a Laravel staging still gets
// PRD 13.2's noindex line.
func TestRunOnlyNoindexLaravel(t *testing.T) {
	f := useRunFakes(t)
	f.staging.StackName = core.StackLaravel
	j := f.job(Prepare)
	j.Opts.Only = []string{"noindex"}
	res, err := Run(context.Background(), j)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stack.Name != core.StackLaravel || !slices.Contains(res.NotCovered, notCoveredLaravel) {
		t.Errorf("stack %q not covered %q", res.Stack.Name, res.NotCovered)
	}
}

// A standalone verify --from keeps every production host, also a blog's
// custom domain that url_map doesn't name: verify rewrites nothing. Both
// sides from stdin is refused.
func TestVerifyJobProductionHosts(t *testing.T) {
	f := useRunFakes(t)
	f.prod.SourceFunc = func([]string) (core.Source, error) {
		td := blogTables()
		td[0].rows = append(td[0].rows, []any{4, "boutique.example", "/"})
		return testutil.FakeSource(schemaOf("wp_", td), batches(td)), nil
	}
	cfg := roadiecfg.Default()
	cfg.URL = "https://staging.shop.test"
	j := Job{Config: cfg, Opts: Options{FailOn: core.SeverityHigh}}
	from := prodEP
	vj, err := VerifyJob(context.Background(), j, stagingEP, &from)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"shop.com", "fr.shop.com", "boutique.example"} {
		if !slices.Contains(vj.Production, h) {
			t.Errorf("production %v lacks %s", vj.Production, h)
		}
	}
	stdio := core.Endpoint{Kind: core.Stdio, Raw: "-", Display: "-"}
	if _, err := VerifyJob(context.Background(), j, stdio, &stdio); !isCode(err, core.ErrConfig) {
		t.Errorf("stdin twice: %v", err)
	}
}

// loadConfig writes yaml as .roadie/config.yaml in a temp project and
// loads it with no ROADIE_ variables set.
func loadConfig(t *testing.T, yaml string) *roadiecfg.Config {
	t.Helper()
	dir := filepath.Join(t.TempDir(), ".roadie")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := roadiecfg.Load(path, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// A key the run uses that reads an unset ${ROADIE_*} stops the run with
// config, exit 2, before the lock: the literal placeholder is never used
// as a drop glob, a test key or a rewrite target. A key a flag replaces
// doesn't stop it.
func TestRunUnsetVariable(t *testing.T) {
	for _, tc := range []struct{ name, yaml, want string }{
		{"drop", "drop: ['${ROADIE_T}']\n", "drop[0] reads ${ROADIE_T}, which isn't set."},
		{"test key", "keys:\n  test_keys:\n    stripe: '${ROADIE_STRIPE_TEST}'\n", "keys.test_keys.stripe reads ${ROADIE_STRIPE_TEST}, which isn't set."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := useRunFakes(t)
			j := f.job(Copy)
			j.Config = loadConfig(t, tc.yaml)
			_, err := Run(context.Background(), j)
			var ce *core.Error
			if !errors.As(err, &ce) || ce.Code != core.ErrConfig || ce.Exit != 2 || !strings.Contains(ce.Msg, tc.want) ||
				!strings.HasSuffix(ce.Msg, "Set it in the environment, or remove the entry.") {
				t.Fatalf("err %v", err)
			}
			if f.rec.index("lock") >= 0 || len(f.opened) != 0 || len(f.staging.Writes()) != 0 {
				t.Errorf("ran past the check: %v", f.rec.all())
			}
			vj := Job{Config: j.Config, Opts: Options{FailOn: core.SeverityHigh}}
			if _, err := VerifyJob(context.Background(), vj, stagingEP, nil); !isCode(err, core.ErrConfig) {
				t.Errorf("verify: %v", err)
			}
		})
	}
	// --url replaces url:, so its unset variable is never read.
	f := useRunFakes(t)
	j := f.job(Copy)
	j.Config = loadConfig(t, "url: '${ROADIE_URL}'\n")
	if _, err := Run(context.Background(), j); err != nil {
		t.Errorf("url replaced by the flag: %v", err)
	}
	j.Opts.URL = ""
	if _, err := Run(context.Background(), j); !isCode(err, core.ErrConfig) {
		t.Errorf("url read from the file: %v", err)
	}
}

// countingSink is a staging sink that hears the copy's table count.
type countingSink struct {
	*logSink
	expected int
}

func (s *countingSink) ExpectTables(n int) { s.expected = n }

// A run hands the guards --url, the sites --fix-collation, the kill
// switch the version and the source's gateways; the sink hears the
// table count.
func TestRunWiring(t *testing.T) {
	f := useRunFakes(t)
	order := `a:2:{s:6:"stripe";i:0;s:11:"mystery_pay";i:1;}`
	f.prod.SourceFunc = func([]string) (core.Source, error) {
		td := wpUsers()
		td[0].rows = append(td[0].rows, []any{6, "woocommerce_gateway_order", order})
		return testutil.FakeSource(schemaOf("wp_", td), batches(td)), nil
	}
	cs := &countingSink{logSink: f.stagSink}
	f.staging.SinkFunc = func() (core.Sink, error) { return cs, nil }
	var ko []killswitch.Options
	old := killDataRules
	killDataRules = func(_ core.Stack, o killswitch.Options) []core.Rule { ko = append(ko, o); return nil }
	t.Cleanup(func() { killDataRules = old })
	j := f.job(Copy)
	j.Opts.FixCollation, j.Opts.Version = true, "1.2.3"
	if _, err := Run(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	if len(f.guards) == 0 || f.guards[0].URL != "https://staging.shop.test" {
		t.Errorf("guards %+v", f.guards)
	}
	for _, o := range f.opened {
		if !o.FixCollation {
			t.Errorf("site options %+v", o)
		}
	}
	if len(ko) == 0 || ko[0].Version != "1.2.3" || !slices.Equal(ko[0].GatewayIDs, []string{"stripe", "mystery_pay"}) {
		t.Errorf("kill switch options %+v", ko)
	}
	if cs.expected != 2 {
		t.Errorf("ExpectTables(%d), want 2", cs.expected)
	}
}

// The dump reader's notes (a dropped GRANT) reach the plan once, though
// both passes read the dump; a folder named as a dump gets dump-format.
func TestRunDumpNotes(t *testing.T) {
	useRunFakes(t)
	old := openDump
	openDump = func(ctx context.Context, r io.Reader, o dump.Options) (core.Source, error) {
		td := wpUsers()
		o.Progress.Note("dropped 1 GRANT statement (GRANT ALL ON shop.* TO …)")
		o.Progress.Table("wp_users", 0)
		return testutil.FakeSource(schemaOf("wp_", td), batches(td)), nil
	}
	t.Cleanup(func() { openDump = old })
	dir := t.TempDir()
	in := filepath.Join(dir, "shop.sql")
	if err := os.WriteFile(in, []byte("-- dump\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	j := Job{Mode: Plan, From: core.Endpoint{Kind: core.DumpFile, Raw: in, Display: "shop.sql", Path: in}, Config: roadiecfg.Default(),
		Opts: Options{URL: "https://staging.shop.test"}}
	res, err := Run(context.Background(), j)
	if err != nil {
		t.Fatal(err)
	}
	var notes []string
	for _, e := range res.PlannedEdits {
		if e.Step == "dump" {
			notes = append(notes, e.What)
		}
	}
	if !slices.Equal(notes, []string{"dropped 1 GRANT statement (GRANT ALL ON shop.* TO …)"}) {
		t.Errorf("dump notes %q", notes)
	}
	j.From.Path, j.From.Raw = dir, dir
	if _, err := Run(context.Background(), j); !isCode(err, core.ErrDumpFormat) {
		t.Errorf("a folder: %v", err)
	}
}

// A standalone verify reads a dump with a function in it: verify never
// runs the dump and has no --keep-routines to ask for.
func TestVerifyJobReadsRoutines(t *testing.T) {
	useRunFakes(t)
	old := openDump
	openDump = dump.Open
	t.Cleanup(func() { openDump = old })
	in := filepath.Join("..", "dump", "pgsql", "testdata", "pg16-prisma.sql")
	target := core.Endpoint{Kind: core.DumpFile, Raw: in, Display: "pg16-prisma.sql", Path: in}
	if _, err := VerifyJob(context.Background(), Job{Config: roadiecfg.Default(), Opts: Options{Stack: "sql"}}, target, nil); err != nil {
		t.Fatal(err)
	}
}

// A PostgreSQL dump into a WordPress site's MySQL database stops with
// dialect-mismatch before anything is written.
func TestRunDialectMismatch(t *testing.T) {
	f := useRunFakes(t)
	f.staging.StackName = core.StackWordPress
	old := openDump
	openDump = func(context.Context, io.Reader, dump.Options) (core.Source, error) {
		td := wpUsers()
		s := schemaOf("wp_", td)
		s.Dialect = core.Postgres
		return testutil.FakeSource(s, batches(td)), nil
	}
	t.Cleanup(func() { openDump = old })
	in := filepath.Join(t.TempDir(), "shop.sql")
	if err := os.WriteFile(in, []byte("-- dump\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	j := f.job(Copy)
	j.From = core.Endpoint{Kind: core.DumpFile, Raw: in, Display: "shop.sql", Path: in}
	res, err := Run(context.Background(), j)
	if !isCode(err, core.ErrDialectMismatch) || res.TargetState != core.Untouched || len(f.applied()) != 0 || f.stagSink.wrote {
		t.Errorf("err %v state %s applied %v", err, res.TargetState, f.applied())
	}
	// A MySQL dump goes on as before.
	openDump = func(context.Context, io.Reader, dump.Options) (core.Source, error) {
		td := wpUsers()
		s := schemaOf("wp_", td)
		s.Dialect = core.MySQL
		return testutil.FakeSource(s, batches(td)), nil
	}
	if _, err := Run(context.Background(), j); err != nil {
		t.Errorf("a MySQL dump: %v", err)
	}
}

// A dump-file target is told the size of the input, so a disk too full
// for the copy stops before the first byte.
func TestRunExpectBytes(t *testing.T) {
	f := useRunFakes(t)
	f.prod.SourceFunc = func([]string) (core.Source, error) {
		td := wpUsers()
		src := testutil.FakeSource(schemaOf("wp_", td), batches(td))
		src.FP.Bytes = 123456
		return src, nil
	}
	var got []int64
	old := newFileSink
	newFileSink = func(_ string, o dump.WriterOptions) (core.Sink, error) {
		got = append(got, o.ExpectBytes)
		return f.fileSink, nil
	}
	t.Cleanup(func() { newFileSink = old })
	out := filepath.Join(t.TempDir(), "staging.sql")
	j := f.job(Copy)
	j.To = core.Endpoint{Kind: core.DumpFile, Raw: out, Display: "staging.sql", Path: out}
	j.Opts.ConfirmTarget = ""
	if _, err := Run(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []int64{123456}) {
		t.Errorf("ExpectBytes %v, want [123456]", got)
	}
}

// A dump target is written in the source's dialect: a pg_dump copy is a
// pg_dump again. With --one-pass the dialect comes from the dump's head.
func TestSinkTakesSourceDialect(t *testing.T) {
	var got []dump.WriterOptions
	old := newFileSink
	newFileSink = func(_ string, o dump.WriterOptions) (core.Sink, error) {
		got = append(got, o)
		return testutil.FakeSink(), nil
	}
	t.Cleanup(func() { newFileSink = old })
	pg := filepath.Join("..", "dump", "pgsql", "testdata", "pg16-prisma.sql")
	out := filepath.Join(t.TempDir(), "out.sql")
	r := &runner{j: Job{From: core.Endpoint{Kind: core.DumpFile, Path: pg}, To: core.Endpoint{Kind: core.DumpFile, Path: out}}, res: &core.Result{}}
	for _, d := range []core.Dialect{core.Postgres, ""} {
		if _, err := r.sink(context.Background(), d); err != nil {
			t.Fatal(err)
		}
	}
	mysqlDump := filepath.Join(t.TempDir(), "shop.sql")
	if err := os.WriteFile(mysqlDump, []byte("-- MySQL dump 10.13  Distrib 8.0.36\nCREATE TABLE `t` (`id` int);\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.j.From.Path = mysqlDump
	if _, err := r.sink(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Target != core.Postgres || got[1].Target != core.Postgres || got[2].Target == core.Postgres {
		t.Errorf("writer options %+v", got)
	}
}

// A Soundcheck that exits 1 is the finding soundcheck.failed; one that
// isn't installed is only a note.
func TestRunSoundcheckFailed(t *testing.T) {
	for _, c := range []struct {
		name string
		sc   *core.SoundcheckResult
		want bool
	}{
		{"exit 1", &core.SoundcheckResult{Ran: true, ExitCode: 1}, true},
		{"exit 0", &core.SoundcheckResult{Ran: true}, false},
		{"not installed", &core.SoundcheckResult{Note: verify.SoundcheckNotRun}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := useRunFakes(t)
			runSoundcheck = func(context.Context, string, string) *core.SoundcheckResult { return c.sc }
			out := filepath.Join(t.TempDir(), "staging.sql")
			j := f.job(Copy)
			j.To = core.Endpoint{Kind: core.DumpFile, Raw: out, Display: "staging.sql", Path: out}
			j.Opts.ConfirmTarget, j.Opts.Soundcheck = "", true
			res, err := Run(context.Background(), j)
			if err != nil {
				t.Fatal(err)
			}
			got := false
			for _, fd := range res.AllFindings() {
				if fd.ID == core.FindingSoundcheckFailed && fd.Severity == core.SeverityMedium {
					got = true
				}
			}
			if got != c.want {
				t.Errorf("soundcheck.failed %v, want %v", got, c.want)
			}
		})
	}
}

// A SQLite source can't go to a dump; plan stops on it as copy does.
func TestCheckDialects(t *testing.T) {
	for _, c := range []struct {
		name   string
		source core.Dialect
		to     core.Endpoint
		stop   bool
	}{
		{"sqlite to dump", core.SQLite, core.Endpoint{Kind: core.DumpFile, Path: "out.sql"}, true},
		{"sqlite to stdout", core.SQLite, core.Endpoint{Kind: core.Stdio}, true},
		{"sqlite to sqlite", core.SQLite, core.Endpoint{Kind: core.DSN, Driver: core.SQLite}, false},
		{"mysql to dump", core.MySQL, core.Endpoint{Kind: core.DumpFile, Path: "out.sql"}, false},
		{"postgres to mysql", core.Postgres, core.Endpoint{Kind: core.DSN, Driver: core.MySQL}, true},
	} {
		r := &runner{j: Job{From: core.Endpoint{Kind: core.DumpFile, Display: "prod"}, To: c.to}}
		err := r.checkDialects(c.source, "")
		var ce *core.Error
		if got := errors.As(err, &ce) && ce.Code == core.ErrDialectMismatch; got != c.stop {
			t.Errorf("%s: %v", c.name, err)
		}
		if c.stop && c.source == core.SQLite && !strings.Contains(err.Error(), "--to sqlite:///") {
			t.Errorf("%s: no next step in %q", c.name, err)
		}
	}
}

// fakeKeyStep is a key step that blanked one live key in .env.
type fakeKeyStep struct{ fakeStep }

func (s *fakeKeyStep) AppliedKeys() []core.KeyHit {
	return []core.KeyHit{{ID: "key.stripe-live", Provider: "stripe", Env: "live", Severity: core.SeverityCritical,
		At: core.Location{File: ".env", Line: 4}, Masked: "sk_l•••••", Action: "blanked"}}
}

// The live keys a key step changed in a config file count with the keys
// in the data: data.keys and the summary name them.
func TestRunCountsFileKeys(t *testing.T) {
	f := useRunFakes(t)
	ks := &fakeKeyStep{fakeStep{id: core.FindingKeyGeneric, phase: "files", rec: f.rec}}
	killSteps = func(core.Stack, killswitch.Options) []core.Step { return []core.Step{ks} }
	res, err := Run(context.Background(), f.job(Copy))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, k := range res.Keys {
		if k.At.File == ".env" && k.Action == "blanked" {
			found = true
		}
	}
	if s := res.Summary(); !found || s.KeysFound < 1 || s.KeysBlanked < 1 {
		t.Errorf("keys %+v summary %+v", res.Keys, s)
	}
	// A plan applies no step, so it counts no file key.
	f = useRunFakes(t)
	killSteps = func(core.Stack, killswitch.Options) []core.Step { return []core.Step{ks} }
	res, err = Run(context.Background(), f.job(Plan))
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range res.Keys {
		if k.At.File == ".env" {
			t.Errorf("plan counted %+v", k)
		}
	}
}

// A prepare that stops mixed after its backup names the backup and says
// to restore it first: a new run would back up the mixed database.
func TestRunPrepareMixedNamesBackup(t *testing.T) {
	f := useRunFakes(t)
	mixed := core.Errorf(core.ErrImportMixed, 3,
		"import: table wp_users: MySQL error 1067 (42000): Invalid default value. The target is in a mixed state during table wp_users: some tables are pseudonymised, the rest still hold the earlier copy. Fix the cause and run the same command again.")
	mixed.State = core.Mixed
	f.stagSink.WriteErr = mixed
	res, err := Run(context.Background(), f.job(Prepare))
	if !isCode(err, core.ErrImportMixed) {
		t.Fatalf("err %v", err)
	}
	if res.Backup == "" || res.Err == nil || res.Err.State != core.Mixed {
		t.Fatalf("backup %q err %+v", res.Backup, res.Err)
	}
	want := "Restore the backup prepare took first: " + f.staging.BackupRestore + ". Then fix the cause and run the same command again; a new run backs up the database as it is now, not as it was."
	if !strings.HasSuffix(res.Err.Msg, want) || err.Error() != res.Err.Msg || strings.Contains(res.Err.Msg, "Fix the cause") {
		t.Errorf("message %q", res.Err.Msg)
	}

	// Negative: a copy takes no backup and keeps the advice as it is.
	f = useRunFakes(t)
	f.stagSink.WriteErr = core.Errorf(core.ErrImportMixed, 3, "import: x. Fix the cause and run the same command again.")
	res, _ = Run(context.Background(), f.job(Copy))
	if res.Err == nil || !strings.HasPrefix(res.Err.Msg, "import: x. Fix the cause and run the same command again.") || strings.Contains(res.Err.Msg, "Restore the backup") {
		t.Errorf("copy message %+v", res.Err)
	}
}

// A stop the import calls untouched after a kill-switch step changed
// the site's files says mixed in its message too, as the box does.
func TestRunSteppedRewordsUntouched(t *testing.T) {
	f := useRunFakes(t)
	f.stagSink.WriteErr = core.Errorf(core.ErrImport, 3, "import: table wp_users: refused. The target is untouched.")
	res, err := Run(context.Background(), f.job(Copy))
	if err == nil || res.TargetState != core.Mixed {
		t.Fatalf("state %s err %v", res.TargetState, err)
	}
	want := "import: table wp_users: refused. The database is untouched but the kill switch already changed the site's files, so the target is in a mixed state."
	if res.Err.Msg != want || err.Error() != want {
		t.Errorf("message %q / %q", res.Err.Msg, err)
	}
}

// A DSN target has no files: a step there changes nothing, so a stop in
// the import before any table leaves it untouched, as the message says.
func TestRunDSNTargetStepsLeaveUntouched(t *testing.T) {
	f := useRunFakes(t)
	f.stagSink.WriteErr = core.Errorf(core.ErrImport, 3, "import: table migrations: missing. The target is untouched.")
	j := f.job(Copy)
	j.To.Kind, j.To.Driver = core.DSN, core.MySQL
	j.Opts.ConfirmTarget = "staging"
	res, err := Run(context.Background(), j)
	if err == nil {
		t.Fatal("no error")
	}
	if res.TargetState != core.Untouched || res.Err == nil || res.Err.State != core.Untouched || !strings.HasSuffix(res.Err.Msg, "The target is untouched.") {
		t.Errorf("state %s err %+v", res.TargetState, res.Err)
	}
}

// A live PostgreSQL source into a database brings rows only; plan and
// copy say so up front.
func TestRowsOnly(t *testing.T) {
	pg := core.Endpoint{Kind: core.DSN, Driver: core.Postgres}
	for _, c := range []struct {
		from, to core.Endpoint
		want     bool
	}{
		{pg, core.Endpoint{Kind: core.DSN, Driver: core.Postgres}, true},
		{pg, core.Endpoint{Kind: core.LocalSite}, true},
		{pg, core.Endpoint{Kind: core.DumpFile}, false},
		{pg, core.Endpoint{Kind: core.Stdio}, false},
		{pg, core.Endpoint{}, false},
		{core.Endpoint{Kind: core.DSN, Driver: core.MySQL}, core.Endpoint{Kind: core.DSN, Driver: core.MySQL}, false},
		{core.Endpoint{Kind: core.DumpFile, Driver: core.Postgres}, core.Endpoint{Kind: core.DSN, Driver: core.Postgres}, false},
	} {
		if got := RowsOnly(c.from, c.to); got != c.want {
			t.Errorf("RowsOnly(%s %s, %s) = %v", c.from.Kind, c.from.Driver, c.to.Kind, got)
		}
	}
}
