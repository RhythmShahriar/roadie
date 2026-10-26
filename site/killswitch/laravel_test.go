// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package killswitch

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/internal/testutil"
	"github.com/RhythmShahriar/roadie/roadiecfg"
)

var wantLaravelSteps = []string{
	core.FindingConfigEnvironment, core.FindingConfigSalts, core.FindingSideEffectMail,
	core.FindingSideEffectQueue, core.FindingKeyGeneric,
}

// prodAppKey is production's APP_KEY in the fixture (not a real key).
const prodAppKey = "base64:Zml4dHVyZS1hcHAta2V5LW5vdC1yZWFsLTAwMDAwMDA="

// stagingAppKey is the fixed key the tests rotate to.
const stagingAppKey = "base64:c3RhZ2luZy1maXh0dXJlLWtleS0wMDAwMDAwMDAwMDA="

// prodEnv is a production .env with every switch off.
func prodEnv() string {
	return "# Shop settings (fixture, no real values)\n" +
		"APP_NAME=\"Roadie Shop\"\n" +
		"APP_ENV=production\n" +
		"APP_KEY=" + prodAppKey + "\n" +
		"APP_DEBUG=false\n" +
		"APP_URL=https://shop.example\n" +
		"\n" +
		"DB_CONNECTION=mysql\n" +
		"DB_HOST=127.0.0.1\n" +
		"DB_DATABASE=shop\n" +
		"DB_USERNAME=shop\n" +
		"DB_PASSWORD='fixture#pass'\n" +
		"\n" +
		"MAIL_MAILER=smtp\n" +
		"MAIL_HOST=smtp.mail.example # the production relay\n" +
		"MAIL_PORT=587\n" +
		"MAIL_USERNAME=postmaster@shop.example\n" +
		"MAIL_PASSWORD=\"fixture mail pass\"\n" +
		"MAIL_ENCRYPTION=tls\n" +
		"MAIL_FROM_ADDRESS=\"hello@shop.example\"\n" +
		"\n" +
		"QUEUE_CONNECTION=database\n" +
		"\n" +
		"STRIPE_KEY=pk_test_fixture\n" +
		"STRIPE_SECRET=" + liveStripe + "\n" +
		"SENTRY_LARAVEL_DSN=https://fixture@sentry.example/1\n"
}

// laravelSite is a Laravel site holding env as .env and n queued jobs.
func laravelSite(env string, jobs int) *testutil.Site {
	s := testutil.FakeSite(map[string][]byte{".env": []byte(env), "artisan": []byte("#!/usr/bin/env php")})
	s.StackName = core.StackLaravel
	s.Identity.Prefix = ""
	s.SourceFunc = func(tables []string) (core.Source, error) {
		if !slices.Contains(tables, "jobs") || jobs == 0 {
			return testutil.FakeSource(nil, nil), nil
		}
		tb := &core.Table{Name: "jobs", Columns: []core.Column{{Name: "id"}, {Name: "queue"}}}
		b := &core.Batch{Seq: 1, Table: tb}
		for i := range jobs {
			b.Rows = append(b.Rows, core.Row{Values: []core.Value{{Kind: core.String, Raw: []byte(strconv.Itoa(i + 1))}, {Kind: core.String, Raw: []byte("default")}}})
		}
		return testutil.FakeSource(nil, []*core.Batch{b}), nil
	}
	return s
}

func laravelOptions() Options {
	o := fixtureOptions()
	o.URL = "https://staging.shop.test/"
	o.AppKey = func() string { return stagingAppKey }
	o.Keys = func(pairs []core.Pair) []core.KeyHit {
		var out []core.KeyHit
		for _, p := range pairs {
			if strings.HasPrefix(p.Value, "sk_"+"live_") {
				out = append(out, core.KeyHit{ID: core.KeyLiveID("stripe"), Provider: "stripe", Env: "live", Severity: core.SeverityCritical,
					At: core.Location{File: ".env", Line: p.Line}, Masked: core.MaskKey(p.Value)})
			}
		}
		return out
	}
	return o
}

func checkLaravel(t *testing.T, s core.Site, o Options) (map[string]string, map[string][]core.Finding) {
	t.Helper()
	status, out := map[string]string{}, map[string][]core.Finding{}
	for _, st := range Steps(core.StackLaravel, o) {
		se, fs, err := st.Check(context.Background(), s)
		if err != nil {
			t.Fatalf("%s: %v", st.ID(), err)
		}
		if se.ID != st.ID() {
			t.Errorf("%s: side effect ID %s", st.ID(), se.ID)
		}
		status[st.ID()] = se.Status
		for _, f := range fs {
			if !core.KnownID(f.ID) || f.Title == "" || len(f.Locations) == 0 {
				t.Errorf("%s: finding %+v", st.ID(), f)
			}
		}
		out[st.ID()] = fs
	}
	return status, out
}

func applyLaravel(t *testing.T, s core.Site, o Options) {
	t.Helper()
	for _, st := range Steps(core.StackLaravel, o) {
		if err := st.Apply(context.Background(), s); err != nil {
			t.Fatalf("%s: %v", st.ID(), err)
		}
	}
}

func TestLaravelStepsAndRules(t *testing.T) {
	var got []string
	for _, st := range Steps(core.StackLaravel, laravelOptions()) {
		got = append(got, st.ID())
		if !core.KnownID(st.ID()) || st.Phase() != "files" {
			t.Errorf("step %s phase %s", st.ID(), st.Phase())
		}
	}
	if !slices.Equal(got, wantLaravelSteps) {
		t.Fatalf("steps %v\nwant %v", got, wantLaravelSteps)
	}
	var tables []string
	for _, r := range DataRules(core.StackLaravel, laravelOptions()) {
		if r.Profile != Profile || r.Action != core.Truncate {
			t.Errorf("rule %+v", r)
		}
		tables = append(tables, r.Table)
	}
	if !slices.Equal(tables, []string{"jobs", "job_batches"}) {
		t.Fatalf("tables %v", tables)
	}
}

// TestLaravelEnvGolden pins the .env edits as a diff, then checks a
// second run changes nothing.
func TestLaravelEnvGolden(t *testing.T) {
	o := laravelOptions()
	o.Cfg = &roadiecfg.Config{}
	o.Cfg.SideEffects.Echo.Install = true
	o.Cfg.Laravel.Env = map[string]string{"LOG_CHANNEL": "stderr", "SESSION_DRIVER": "file"}
	s := laravelSite(prodEnv(), 2)
	applyLaravel(t, s, o)
	got := string(s.Files()[".env"])
	if strings.Contains(got, liveStripe) {
		t.Fatal("the live key is still in .env")
	}
	// The golden file names the live key: push protection refuses a
	// literal one.
	testutil.Golden(t, "laravel-env.diff", []byte(strings.ReplaceAll(lineDiff(prodEnv(), got), liveStripe, "<live Stripe key>")))
	applyLaravel(t, s, o)
	if again := string(s.Files()[".env"]); again != got {
		t.Fatalf("a second run changed .env:\n%s", lineDiff(got, again))
	}
}

func TestLaravelPlanGolden(t *testing.T) {
	s := laravelSite(prodEnv(), 2)
	var b strings.Builder
	for _, st := range Steps(core.StackLaravel, laravelOptions()) {
		edits, err := st.Plan(context.Background(), s)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range edits {
			if e.Step != st.ID() {
				t.Errorf("edit step %s in %s", e.Step, st.ID())
			}
			fmt.Fprintf(&b, "%s | %s | %s\n", e.Step, e.File, e.What)
		}
	}
	plan := b.String()
	if strings.Contains(plan, liveStripe) {
		t.Fatal("the plan shows a live key")
	}
	for _, want := range []string{
		"side-effect.mail | composer.json | " + ManualPrefix + ComposerLine,
		"side-effect.queue | queue workers | " + ManualPrefix + WorkersLine,
		"encrypted columns and old sessions from production become unreadable",
	} {
		if !strings.Contains(plan, want) {
			t.Errorf("plan lacks %q", want)
		}
	}
	testutil.Golden(t, "laravel-plan.txt", []byte(plan))
	if len(s.Writes()) != 0 {
		t.Fatalf("plan wrote %v", s.Writes())
	}
}

// TestLaravelChecksPositiveNegative: production has every switch on;
// after the steps every one is off with no finding.
func TestLaravelChecksPositiveNegative(t *testing.T) {
	o := laravelOptions()
	s := laravelSite(prodEnv(), 2)
	status, fs := checkLaravel(t, s, o)
	want := map[string]string{
		core.FindingConfigEnvironment: core.FindingConfigEnvironment,
		core.FindingSideEffectMail:    core.FindingSideEffectMail,
		core.FindingSideEffectQueue:   core.FindingSideEffectQueue,
		core.FindingKeyGeneric:        core.KeyLiveID("stripe"),
	}
	for step, id := range want {
		if status[step] != "on" || len(fs[step]) != 1 || fs[step][0].ID != id {
			t.Errorf("production %s: %q %+v, want on with %s", step, status[step], fs[step], id)
		}
	}
	if q := fs[core.FindingSideEffectQueue]; len(q) == 1 && (q[0].Count != 2 || q[0].Severity != core.SeverityHigh) {
		t.Errorf("queue finding %+v", q[0])
	}
	if status[core.FindingConfigSalts] != "off" || len(fs[core.FindingConfigSalts]) != 0 {
		t.Errorf("config.salts: %q %v (status only)", status[core.FindingConfigSalts], fs[core.FindingConfigSalts])
	}

	applyLaravel(t, s, o)
	status, fs = checkLaravel(t, s, o)
	for _, step := range wantLaravelSteps {
		if status[step] != "off" || len(fs[step]) != 0 {
			t.Errorf("staging %s: %q %v, want off and none", step, status[step], fs[step])
		}
	}
	env := string(s.Files()[".env"])
	if strings.Contains(env, prodAppKey) || !strings.Contains(env, "APP_KEY=\""+stagingAppKey+"\"") {
		t.Errorf("APP_KEY not rotated:\n%s", env)
	}
}

func TestLaravelKeepAppKey(t *testing.T) {
	o := laravelOptions()
	o.KeepAppKey = true
	o.AppKey = func() string { t.Fatal("AppKey called with --keep-app-key"); return "" }
	s := laravelSite(prodEnv(), 0)
	plan, err := Steps(core.StackLaravel, o)[1].Plan(context.Background(), s)
	if err != nil || len(plan) != 1 || !strings.Contains(plan[0].What, "encrypted columns and old sessions stay readable only with it") {
		t.Fatalf("plan %+v %v", plan, err)
	}
	applyLaravel(t, s, o)
	if !strings.Contains(string(s.Files()[".env"]), "APP_KEY="+prodAppKey+"\n") {
		t.Fatal("APP_KEY changed with --keep-app-key")
	}
	status, _ := checkLaravel(t, s, o)
	if status[core.FindingConfigSalts] != "on" {
		t.Fatalf("config.salts with a kept key: %q", status[core.FindingConfigSalts])
	}
}

func TestLaravelAppKeyNeedsValuesLane(t *testing.T) {
	o := laravelOptions()
	o.AppKey = func() string { return "" }
	err := Steps(core.StackLaravel, o)[1].Apply(context.Background(), laravelSite(prodEnv(), 0))
	if !errors.Is(err, core.ErrNotBuilt) {
		t.Fatalf("got %v, want core.ErrNotBuilt", err)
	}
}

// TestLaravelMailCheck holds positive and negative fixtures for each way
// a Laravel app sends mail.
func TestLaravelMailCheck(t *testing.T) {
	cases := []struct {
		env, status string
		finding     bool
	}{
		{"MAIL_MAILER=smtp\nMAIL_HOST=smtp.mail.example\nMAIL_USERNAME=u\nMAIL_PASSWORD=p\n", "on", true},
		{"MAIL_HOST=smtp.mail.example\nMAIL_PASSWORD=p\n", "on", true},
		{"MAIL_MAILER=smtp\nMAIL_HOST=127.0.0.1\nMAIL_URL=smtp://u:p@smtp.mail.example:587\n", "on", true},
		{"MAIL_MAILER=mailgun\nMAILGUN_SECRET=key-fixture\n", "on", true},
		{"MAIL_MAILER=ses\nAWS_ACCESS_KEY_ID=AKIAFIXTURE\n", "on", true},
		{"MAIL_MAILER=smtp\nMAIL_HOST=127.0.0.1\nRHYTHM_ECHO_MODE=pass\n", "on", true},

		{"MAIL_MAILER=smtp\nMAIL_HOST=127.0.0.1\nMAIL_PORT=1025\nMAIL_USERNAME=\nMAIL_PASSWORD=\nRHYTHM_ECHO_MODE=seal\n", "off", false},
		{"MAIL_MAILER=smtp\nMAIL_HOST=localhost\nMAIL_PASSWORD=p\n", "off", false},
		{"MAIL_MAILER=smtp\nMAIL_HOST=::1\nMAIL_URL=smtp://127.0.0.1:1025\n", "off", false},
		{"MAIL_MAILER=log\nMAIL_HOST=smtp.mail.example\nMAIL_PASSWORD=p\n", "off", false},
		{"MAIL_MAILER=array\n", "off", false},
		{"MAIL_MAILER=mailgun\n", "unknown", false},
		{"MAIL_MAILER=smtp\nMAIL_HOST=mailpit\n", "unknown", false},
		{"MAIL_MAILER=failover\n", "unknown", false},
	}
	st := Steps(core.StackLaravel, laravelOptions())[2]
	for _, c := range cases {
		se, fs, err := st.Check(context.Background(), laravelSite(c.env, 0))
		if err != nil {
			t.Fatal(err)
		}
		if se.Status != c.status || (len(fs) == 1) != c.finding {
			t.Errorf("%q: %q (%s) %v, want %q finding %v", c.env, se.Status, se.How, fs, c.status, c.finding)
		}
		for _, f := range fs {
			if f.Severity != core.SeverityCritical || f.Locations[0].File != ".env" || f.Locations[0].Line == 0 {
				t.Errorf("%q: finding %+v", c.env, f)
			}
			if strings.Contains(f.Title, "p@") || strings.Contains(f.Title, "key-fixture") {
				t.Errorf("finding shows a credential: %s", f.Title)
			}
		}
	}
	// MAIL_URL and MAIL_ENCRYPTION are blanked on apply; absent ones aren't added.
	s := laravelSite("MAIL_URL=smtp://u:p@smtp.mail.example:587\nMAIL_SCHEME=smtps\n", 0)
	applyLaravel(t, s, laravelOptions())
	env := string(s.Files()[".env"])
	if !strings.Contains(env, "MAIL_URL=\n") || !strings.Contains(env, "MAIL_SCHEME=\n") || strings.Contains(env, "MAIL_ENCRYPTION") {
		t.Errorf(".env\n%s", env)
	}
	if se, fs, _ := st.Check(context.Background(), s); se.Status != "off" || len(fs) != 0 {
		t.Errorf("after apply: %+v %v", se, fs)
	}
}

func TestLaravelQueueCheck(t *testing.T) {
	cases := []struct {
		env     string
		jobs    int
		status  string
		finding bool
	}{
		{"QUEUE_CONNECTION=database\n", 3, "on", true},
		{"APP_ENV=staging\n", 1, "on", true},
		{"QUEUE_CONNECTION=database\nDB_PREFIX=app_\n", 0, "on", false},
		{"QUEUE_CONNECTION=database\n", 0, "on", false},
		{"QUEUE_CONNECTION=sync\n", 3, "off", false},
		{"QUEUE_CONNECTION=null\n", 3, "off", false},
		{"QUEUE_CONNECTION=log\n", 3, "off", false},
		{"QUEUE_CONNECTION=redis\n", 3, "unknown", false},
	}
	st := Steps(core.StackLaravel, laravelOptions())[3]
	for _, c := range cases {
		se, fs, err := st.Check(context.Background(), laravelSite(c.env, c.jobs))
		if err != nil {
			t.Fatal(err)
		}
		if se.Status != c.status || (len(fs) == 1) != c.finding {
			t.Errorf("%q with %d jobs: %q (%s) %v, want %q finding %v", c.env, c.jobs, se.Status, se.How, fs, c.status, c.finding)
		}
	}
}

func TestLaravelEnvironmentCheck(t *testing.T) {
	cases := map[string]string{
		"APP_ENV=production\n":                  "on",
		"APP_NAME=x\n":                          "on",
		"APP_ENV=\n":                            "on",
		"APP_ENV=${DEPLOY_ENV}\n":               "on",
		"APP_ENV=staging\n":                     "off",
		"APP_ENV=local\n":                       "off",
		"APP_ENV=production\nAPP_ENV=testing\n": "off",
	}
	st := Steps(core.StackLaravel, laravelOptions())[0]
	for env, want := range cases {
		se, fs, err := st.Check(context.Background(), laravelSite(env, 0))
		if err != nil {
			t.Fatal(err)
		}
		if se.Status != want || (want == "on") != (len(fs) == 1) {
			t.Errorf("%q: %q %v, want %q", env, se.Status, fs, want)
		}
	}
	if se, _, _ := st.Check(context.Background(), testutil.FakeSite(nil)); se.Status != "unknown" {
		t.Errorf("no .env: %q", se.Status)
	}
}

// TestLaravelConfigOverrides: laravel.env wins over Roadie's values and
// side_effects.mail: keep leaves MAIL_* alone.
func TestLaravelConfigOverrides(t *testing.T) {
	o := laravelOptions()
	o.Cfg = &roadiecfg.Config{}
	o.Cfg.SideEffects.Mail = "keep"
	o.Cfg.Laravel.EnvFile = "config/.env.staging"
	o.Cfg.Laravel.Env = map[string]string{"QUEUE_CONNECTION": "database"}
	s := testutil.FakeSite(map[string][]byte{"config/.env.staging": []byte(prodEnv())})
	s.StackName = core.StackLaravel
	applyLaravel(t, s, o)
	env := string(s.Files()["config/.env.staging"])
	for _, want := range []string{"QUEUE_CONNECTION=database\n", "MAIL_HOST=smtp.mail.example # the production relay\n", "APP_ENV=staging\n"} {
		if !strings.Contains(env, want) {
			t.Errorf(".env lacks %q:\n%s", want, env)
		}
	}
	plan, _ := Steps(core.StackLaravel, o)[3].Plan(context.Background(), s)
	if plan[0].What != "QUEUE_CONNECTION=database (laravel.env)" {
		t.Errorf("plan %+v", plan)
	}
	plan, _ = Steps(core.StackLaravel, o)[2].Plan(context.Background(), s)
	if len(plan) != 1 || plan[0].What != "left on (side_effects.mail: keep)" {
		t.Errorf("mail plan %+v", plan)
	}
}

func TestLaravelWriteError(t *testing.T) {
	s := laravelSite(prodEnv(), 0)
	if err := s.Remove(context.Background(), ".env"); err != nil {
		t.Fatal(err)
	}
	err := Steps(core.StackLaravel, laravelOptions())[0].Apply(context.Background(), s)
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Code != core.ErrKillSwitch {
		t.Fatalf("got %v", err)
	}
	want := "config.environment: couldn't write .env (file does not exist), so Laravel may still call this copy production. Fix it, then: rhythm-roadie prepare /srv/staging --only side-effects."
	if ce.Msg != want {
		t.Fatalf("message\n%s\nwant\n%s", ce.Msg, want)
	}
}

// TestDatabaseOnly: a sql target gets one check-only step and no rules.
func TestDatabaseOnly(t *testing.T) {
	steps := Steps(core.StackSQL, Options{})
	if len(steps) != 1 || steps[0].ID() != core.FindingSideEffectNotApplicable {
		t.Fatalf("steps %v", steps)
	}
	s := testutil.FakeSite(nil)
	s.StackName = core.StackSQL
	ctx := context.Background()
	plan, err := steps[0].Plan(ctx, s)
	if err != nil || len(plan) != 1 || plan[0].What != DatabaseOnlyLine {
		t.Fatalf("plan %+v %v", plan, err)
	}
	if err := steps[0].Apply(ctx, s); err != nil || len(s.Writes()) != 0 {
		t.Fatalf("apply %v %v", err, s.Writes())
	}
	se, fs, err := steps[0].Check(ctx, s)
	if err != nil || se.ID != core.FindingSideEffectNotApplicable || len(fs) != 1 || fs[0].Severity != core.SeverityInfo || fs[0].ID != core.FindingSideEffectNotApplicable {
		t.Fatalf("check %+v %+v %v", se, fs, err)
	}
	if DataRules(core.StackSQL, Options{}) != nil {
		t.Fatal("rules for sql")
	}
}

// lineDiff is a minimal line diff of a and b: " " kept, "-" removed, "+"
// added, from a longest common subsequence.
func lineDiff(a, b string) string {
	x, y := strings.SplitAfter(a, "\n"), strings.SplitAfter(b, "\n")
	n, m := len(x), len(y)
	l := make([][]int, n+1)
	for i := range l {
		l[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if x[i] == y[j] {
				l[i][j] = l[i+1][j+1] + 1
			} else {
				l[i][j] = max(l[i+1][j], l[i][j+1])
			}
		}
	}
	var out strings.Builder
	line := func(p, s string) {
		if s == "" {
			return
		}
		out.WriteString(p + strings.TrimSuffix(s, "\n") + "\n")
	}
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case x[i] == y[j]:
			line(" ", x[i])
			i, j = i+1, j+1
		case l[i+1][j] >= l[i][j+1]:
			line("-", x[i])
			i++
		default:
			line("+", y[j])
			j++
		}
	}
	for ; i < n; i++ {
		line("-", x[i])
	}
	for ; j < m; j++ {
		line("+", y[j])
	}
	return out.String()
}
