// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/engine"
	"github.com/RhythmShahriar/roadie/internal/demo"
	"github.com/RhythmShahriar/roadie/internal/testutil"
	"github.com/RhythmShahriar/roadie/profile"
	"github.com/RhythmShahriar/roadie/pseudo"
	"github.com/RhythmShahriar/roadie/roadiecfg"
	"github.com/RhythmShahriar/roadie/verify"

	"gopkg.in/yaml.v3"
)

// verifyFixture fakes engine.VerifyJob and verify.Run: the verify jobs
// they were given are recorded and results answers each run in turn.
type verifyFixture struct {
	built   []engine.Job
	from    []*core.Endpoint
	ran     []verify.Job
	results []*core.VerifyResult
	err     error
}

func useVerify(t *testing.T, fx *verifyFixture, set func(d *deps)) {
	t.Helper()
	useFixture(t, nil, nil, func(d *deps) {
		d.verifyJob = func(_ context.Context, j engine.Job, target core.Endpoint, from *core.Endpoint) (verify.Job, error) {
			fx.built = append(fx.built, j)
			fx.from = append(fx.from, from)
			return verify.Job{Target: target, Production: []string{"shop.com"}}, nil
		}
		d.verify = func(_ context.Context, j verify.Job) (*core.VerifyResult, error) {
			fx.ran = append(fx.ran, j)
			if fx.err != nil {
				return nil, fx.err
			}
			r := fx.results[0]
			if len(fx.results) > 1 {
				fx.results = fx.results[1:]
			}
			c := *r
			return &c, nil
		}
		if set != nil {
			set(d)
		}
	})
}

func heuristicPass() *core.VerifyResult {
	v := passVerify()
	v.Mode = "heuristic"
	v.Checks = v.Checks[1:]
	return v
}

func heuristicFail() *core.VerifyResult {
	v := copyFail().Verify
	v.Mode = "heuristic"
	v.Findings = append(v.Findings, core.Finding{ID: core.FindingKeyLiveMode, Severity: core.SeverityHigh, Title: "Stripe in live mode",
		Locations: []core.LocationCount{{Location: core.Location{Table: "wp_options", Row: "option_name=woocommerce_stripe_settings", Path: "serialized:testmode"}}}})
	v.Keys = sampleKeys("manual")
	return v
}

func TestVerifyScreens(t *testing.T) {
	for _, c := range []struct {
		name string
		res  *core.VerifyResult
		args []string
		code int
	}{
		{"verify-pass", heuristicPass(), []string{"/var/www/old-staging", "--production", "shop.com", "--no-config"}, 0},
		{"verify-fail", heuristicFail(), []string{"/var/www/old-staging", "--production", "shop.com", "--no-config"}, 1},
		{"verify-exact", passVerify(), []string{"staging.sql", "--from", "prod.sql.gz", "--no-config"}, 0},
		{"verify-sample", func() *core.VerifyResult { v := heuristicPass(); v.Coverage.Sample = 100; return v }(),
			[]string{"staging.sql", "--sample", "100", "--no-config"}, 0},
	} {
		for _, color := range []bool{false, true} {
			name := c.name
			fx := &verifyFixture{results: []*core.VerifyResult{c.res}}
			useVerify(t, fx, func(d *deps) {
				if color {
					d.env = colorEnv
				}
			})
			if color {
				name += "-color"
			}
			got := screen(t, "verify", c.args...)
			testutil.Golden(t, "screen/"+name+".txt", []byte(got))
			if !strings.HasSuffix(got, "--- exit "+string(rune('0'+c.code))+"\n") {
				t.Errorf("%s: want exit %d\n%s", name, c.code, got)
			}
		}
	}
}

func TestVerifyJobAndOptions(t *testing.T) {
	fx := &verifyFixture{results: []*core.VerifyResult{heuristicPass()}}
	useVerify(t, fx, nil)
	_, errOut, code := runCmd(t, "verify", "staging.sql", "--from", "prod.sql.gz", "--production", "*.shop.com", "--sample", "50",
		"--skip", "names", "--fail-on", "medium", "--echo", "--site", "https://staging.shop.test", "--no-config")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if len(fx.built) != 1 || fx.built[0].To.Raw != "staging.sql" || fx.from[0] == nil || fx.from[0].Raw != "prod.sql.gz" ||
		!slices.Equal(fx.built[0].Opts.Production, []string{"*.shop.com"}) || fx.built[0].Opts.FailOn != core.SeverityMedium {
		t.Errorf("VerifyJob input: %+v from %+v", fx.built, fx.from)
	}
	j := fx.ran[0]
	if !slices.Equal(j.Production, []string{"shop.com", "*.shop.com"}) || j.Opts.Sample != 50 || !slices.Equal(j.Opts.Skip, []string{"names"}) ||
		j.Opts.FailOn != core.SeverityMedium || !j.Opts.Echo || j.Opts.EchoURL != "http://127.0.0.1:8025" || j.Opts.SiteURL != "https://staging.shop.test" ||
		j.Opts.Clock == nil || j.Progress == nil {
		t.Errorf("verify job: %+v", j)
	}
	// The standalone verify without --from has no production endpoint.
	fx = &verifyFixture{results: []*core.VerifyResult{heuristicPass()}}
	useVerify(t, fx, nil)
	runCmd(t, "verify", "staging.sql", "--no-config")
	if fx.from[0] != nil {
		t.Error("verify without --from passed a source")
	}
}

func TestVerifyErrors(t *testing.T) {
	fx := &verifyFixture{err: core.Errorf(core.ErrNothingScanned, 3, "nothing-scanned: verify read 0 rows from staging@localhost (58 tables, access denied on every one). A verify that sees nothing can't pass; check the user's SELECT rights.")}
	useVerify(t, fx, nil)
	out, _, code := runCmd(t, "verify", "mysql://staging@localhost/staging", "--no-config", "--plain")
	if code != 3 || !strings.Contains(out, "nothing-scanned: verify read 0 rows") {
		t.Errorf("exit %d\n%s", code, out)
	}
	// A verify that scanned no row exits 3 even without an error.
	fx = &verifyFixture{results: []*core.VerifyResult{{Mode: "heuristic"}}}
	useVerify(t, fx, nil)
	if _, _, code := runCmd(t, "verify", "x.sql", "--no-config"); code != 3 {
		t.Errorf("nothing scanned: exit %d", code)
	}
	useVerify(t, &verifyFixture{}, nil)
	if _, errOut, code := runCmd(t, "verify", "--no-config"); code != 2 || !strings.Contains(errOut, "verify needs the staging copy") {
		t.Errorf("no target: exit %d %q", code, errOut)
	}
	if _, errOut, code := runCmd(t, "verify", "a", "b", "--no-config"); code != 2 || !strings.Contains(errOut, "one staging at a time") {
		t.Errorf("two targets: exit %d %q", code, errOut)
	}
	// SARIF and JUnit come on stdout or in -o files for verify.
	fx = &verifyFixture{results: []*core.VerifyResult{heuristicPass()}}
	useVerify(t, fx, nil)
	t.Chdir(t.TempDir())
	out, _, code = runCmd(t, "verify", "x.sql", "--sarif", "-o", "r.xml", "--no-config")
	if code != 0 || !strings.Contains(out, `"version":"2.1.0"`) {
		t.Errorf("--sarif: exit %d %q", code, out)
	}
	if b, err := os.ReadFile("r.xml"); err != nil || !strings.Contains(string(b), "<testsuites") {
		t.Errorf("-o r.xml: %q %v", b, err)
	}
}

// configTemplate stands in for roadiecfg.InitTemplate: the lines config
// init and the picker touch.
func configTemplate(stack string) []byte {
	return []byte("# Roadie settings.\n\nsource: \"\"            # where production is\ntarget: \"\"\nurl: \"\"\nstack: " + stack + "\n\ncolumns: []\n#  - table: wp_loyalty_members\n")
}

func TestConfigInit(t *testing.T) {
	var b bytes.Buffer
	for _, stack := range []string{"wordpress", "woocommerce", "laravel", "sql"} {
		t.Run(stack, func(t *testing.T) { // its own folder, given back before the golden file is read
			t.Chdir(t.TempDir())
			var excluded [][]string
			testDeps(t, func(d *deps) {
				d.initTemplate = configTemplate
				d.excludeFromGit = func(dir string, paths []string) (bool, error) { excluded = append(excluded, paths); return true, nil }
			})
			out, errOut, code := runCmd(t, "config", "init", "--stack", stack)
			if code != 0 {
				t.Fatalf("%s: exit %d %s", stack, code, errOut)
			}
			file, _ := os.ReadFile(filepath.Join(".roadie", "config.yaml"))
			b.WriteString("$ rhythm-roadie config init --stack " + stack + "\n" + out + "--- .roadie/config.yaml\n" + string(file) + "\n")
			if len(excluded) != 1 || !slices.Equal(excluded[0], []string{".roadie/reports/", ".roadie/key", ".roadie/backups/", ".roadie/basic-auth/"}) {
				t.Errorf("%s: excluded %q", stack, excluded)
			}
			// A second init refuses to overwrite; --force replaces.
			if _, errOut, code := runCmd(t, "config", "init", "--stack", stack); code != 2 || !strings.Contains(errOut, ".roadie/config.yaml exists; pass --force to replace it") {
				t.Errorf("%s again: exit %d %q", stack, code, errOut)
			}
			if _, _, code := runCmd(t, "config", "init", "--stack", stack, "--force"); code != 0 {
				t.Errorf("%s --force: exit %d", stack, code)
			}
		})
	}
	testutil.Golden(t, "config-init.txt", b.Bytes())
	if !strings.Contains(b.String(), "Wrote .roadie/config.yaml for a WooCommerce site.\nSet source:, target: and url: in it, then run rhythm-roadie plan.\nAdded .roadie/reports, .roadie/key, .roadie/backups and .roadie/basic-auth to .git/info/exclude.\n") {
		t.Error("config init lost the PRD 16.1 lines")
	}
	// Outside a git work tree there is nothing to exclude and no line.
	t.Chdir(t.TempDir())
	testDeps(t, func(d *deps) {
		d.initTemplate = configTemplate
		d.excludeFromGit = func(string, []string) (bool, error) { return false, nil }
	})
	if out, _, _ := runCmd(t, "config", "init"); strings.Contains(out, ".git/info/exclude") || !strings.Contains(out, "for a WordPress site") {
		t.Errorf("no git: %q", out)
	}
}

func TestConfigInitFrom(t *testing.T) {
	t.Chdir(t.TempDir())
	r := planResult()
	fx := useFixture(t, r, nil, func(d *deps) {
		d.initTemplate = configTemplate
		d.excludeFromGit = func(string, []string) (bool, error) { return false, nil }
	})
	out, errOut, code := runCmd(t, "config", "init", "--from", "prod.sql.gz")
	if code != 0 {
		t.Fatalf("exit %d %s", code, errOut)
	}
	file, _ := os.ReadFile(filepath.Join(".roadie", "config.yaml"))
	want := "columns: []\n# Suggested by config init --from: these columns look personal and no profile covers them.\n" +
		"# Check each rule, then uncomment it (and set columns: to the list).\n#  - table: wp_loyalty_members\n#    column: email\n#    rule: email\n"
	if !strings.Contains(string(file), want) || !strings.Contains(string(file), "stack: woocommerce") ||
		!strings.Contains(out, "for a WooCommerce site") || !strings.Contains(out, "Listed 1 column that look personal") {
		t.Errorf("out %q\nfile:\n%s", out, file)
	}
	if len(fx.jobs) != 1 || fx.jobs[0].Mode != engine.Plan || !fx.jobs[0].Opts.SchemaOnly || fx.jobs[0].From.Raw != "prod.sql.gz" {
		t.Errorf("schema read: %+v", fx.jobs)
	}
	for in, want := range map[string]string{"email": "email", "billing_phone": "phone", "first_name": "first_name", "LastName": "last_name",
		"display_name": "full_name", "street_1": "street", "zip": "postcode", "ip_address": "ip", "date_of_birth": "dob", "notes": "blank"} {
		if got := ruleGuess(in); got != want {
			t.Errorf("ruleGuess(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestAddSuggestionsHostileNames feeds names with every line break YAML
// reads: the file must parse to the template's keys alone.
func TestAddSuggestionsHostileNames(t *testing.T) {
	tmpl := configTemplate("wordpress")
	var us []core.Uncovered
	for _, br := range []string{"\n", "\r", "\r\n", "\u0085", "\u2028", "\u2029"} {
		us = append(us, core.Uncovered{Table: "t" + br + "fail_on: none", Column: "c" + br + "keep: [wp_users]" + br + "columns: [x]"})
	}
	out := addSuggestions(tmpl, us)
	var got, want map[string]any
	if err := yaml.Unmarshal(out, &got); err != nil {
		t.Fatalf("parse: %v\n%s", err, out)
	}
	if err := yaml.Unmarshal(tmpl, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("hostile names added keys: got %v, want %v\n%s", got, want, out)
	}
	// Positive: a plain name still reads as written.
	if out := addSuggestions(tmpl, []core.Uncovered{{Table: "wp_members", Column: "email"}}); !strings.Contains(string(out), "#  - table: wp_members\n#    column: email\n") {
		t.Errorf("plain name changed:\n%s", out)
	}
}

func TestConfigCheck(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte("source: \"\"\nkeys:\n  test_keys: {stripe: \"${ROADIE_STRIPE_TEST}\", paypal: \"${ROADIE_SET}\"}\n# ${ROADIE_IN_A_COMMENT}\nurl: \"https://s.test/#${ROADIE_SET}\" # ${ROADIE_AFTER_A_VALUE}\n"), 0o600)
	testDeps(t, func(d *deps) {
		d.loadConfig = func(p string, _ func(string) (string, bool)) (*roadiecfg.Config, error) {
			if p == "bad.yaml" {
				return nil, core.Errorf(core.ErrConfig, 2, ".roadie/config.yaml line 14: keys.test_keys.stripe reads ${STRIPE_TEST}; only ROADIE_ variables are read. Rename it ROADIE_STRIPE_TEST.")
			}
			return &roadiecfg.Config{}, nil
		}
		d.env = func(n string) (string, bool) { return "x", n == "ROADIE_SET" }
	})
	out, _, code := runCmd(t, "config", "check", "--config", path)
	if code != 0 || out != path+" is valid.\nUnset: ROADIE_STRIPE_TEST (line 3); set it before a run that reads it.\n" {
		t.Errorf("exit %d %q", code, out)
	}
	_, errOut, code := runCmd(t, "config", "check", "--config", "bad.yaml")
	if code != 2 || !strings.Contains(errOut, "only ROADIE_ variables are read") {
		t.Errorf("bad file: exit %d %q", code, errOut)
	}
	if _, errOut, code := runCmd(t, "config", "check"); code != 2 || !strings.Contains(errOut, "no .roadie/config.yaml here or above") {
		t.Errorf("no file: exit %d %q", code, errOut)
	}
	if _, errOut, code := runCmd(t, "config", "chek"); code != 2 || !strings.Contains(errOut, "did you mean: rhythm-roadie config check") {
		t.Errorf("typo: exit %d %q", code, errOut)
	}
	if _, errOut, code := runCmd(t, "config"); code != 2 || !strings.Contains(errOut, "say what to do: init, check, show or key") {
		t.Errorf("no subcommand: exit %d %q", code, errOut)
	}
}

func TestYAMLCode(t *testing.T) {
	for in, want := range map[string]string{
		"# all comment":                 "",
		"a: b # note":                   "a: b ",
		"a: b#c":                        "a: b#c",
		`a: "x # y" # z`:                `a: "x # y" `,
		`a: 'it''s # x'`:                `a: 'it''s # x'`,
		"a: ${ROADIE_A}\t# ${ROADIE_B}": "a: ${ROADIE_A}\t",
	} {
		if got := yamlCode(in); got != want {
			t.Errorf("yamlCode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestConfigShow(t *testing.T) {
	testDeps(t, func(d *deps) { d.defaultConfig = func() *roadiecfg.Config { return &roadiecfg.Config{} } })
	_, _, code := runCmd(t, "config", "show", "--no-config")
	if code == 3 {
		t.Skip("needs the engine lane: roadiecfg.Config.Show isn't built yet")
	}
	if code != 0 {
		t.Errorf("exit %d", code)
	}
}

// config show expands the rules: the file's columns: and each profile
// of the stack; --json is a rhythm-roadie/v1 envelope of kind config.
// Without a .roadie/config.yaml, production: of .soundcheck/ and .echo/
// in the folder still counts.
func TestConfigShowExpandsRules(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.MkdirAll(filepath.Join(dir, ".soundcheck"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".soundcheck", "config.yaml"), []byte("production: [\"shop.example.com\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, code := run(t, "config", "show")
	if code != 0 || !strings.Contains(out, "# Rules, expanded:") || !strings.Contains(out, "#   {prefix}users.user_email: fake email") ||
		!strings.Contains(out, "shop.example.com") {
		t.Errorf("text: exit %d\n%s", code, out)
	}
	cfg := filepath.Join(dir, "crm.yaml")
	if err := os.WriteFile(cfg, []byte("stack: sql\ncolumns:\n  - {table: contacts, column: email, rule: email}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, code = run(t, "config", "show", "--config", cfg, "--json")
	var env struct {
		Schema, Kind string
		Data         struct {
			Rules struct {
				Note  string      `json:"note"`
				Rules []shownRule `json:"rules"`
			} `json:"rules"`
			Config map[string]any `json:"config"`
		}
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil || code != 0 {
		t.Fatalf("json: exit %d %v\n%s", code, err, out)
	}
	want := []shownRule{{From: "columns", Table: "contacts", Column: "email", Action: "fake", Fake: "email"}}
	if env.Schema != "rhythm-roadie/v1" || env.Kind != "config" || !reflect.DeepEqual(env.Data.Rules.Rules, want) || env.Data.Config == nil {
		t.Errorf("envelope %+v", env)
	}
	out, _, code = run(t, "config", "check", "--config", cfg, "--json")
	if code != 0 || !strings.Contains(out, `"kind": "config-check"`) || !strings.Contains(out, `"valid": true`) {
		t.Errorf("check --json: exit %d\n%s", code, out)
	}
}

func TestConfigKey(t *testing.T) {
	t.Chdir(t.TempDir())
	var saved, forgot int
	source := "file"
	envKey := false
	testDeps(t, func(d *deps) {
		d.loadKey = func(string, func(string) (string, bool)) (pseudo.Key, string, error) {
			return pseudo.Key{}, source, nil
		}
		d.saveKey = func(dir string, _ pseudo.Key) error { saved++; return nil }
		d.forgetKey = func(string) error { forgot++; return nil }
		d.newKey = func() (pseudo.Key, error) { return pseudo.Key{1}, nil }
		d.env = func(n string) (string, bool) { return "k", n == "ROADIE_KEY" && envKey }
	})
	keyFile := filepath.Join(".", ".roadie", "key")
	// No key file: ROADIE_KEY wins over nothing, forget has nothing to do.
	source = "env"
	if out, _, code := runCmd(t, "config", "key", "show"); code != 0 || !strings.HasSuffix(out, " from ROADIE_KEY\n") {
		t.Errorf("show from env without a file: exit %d %q", code, out)
	}
	if out, _, code := runCmd(t, "config", "key", "forget"); code != 0 || forgot != 0 || out != "No .roadie/key to remove.\n" {
		t.Errorf("forget without a file: exit %d %q", code, out)
	}
	if err := os.MkdirAll(".roadie", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, []byte("k"), 0o600); err != nil {
		t.Fatal(err)
	}
	for src, want := range map[string]string{"file": " from " + keyFile + "\n", "env": " from ROADIE_KEY (it wins over " + keyFile + ")\n",
		"new": "No consistency key is kept: each copy uses a new one (consistency: per-run). --keep-key or consistency: stable keeps one in .roadie/key.\n"} {
		source = src
		if out, _, code := runCmd(t, "config", "key", "show"); code != 0 || !strings.HasSuffix(out, want) {
			t.Errorf("show from %s: exit %d %q", src, code, out)
		}
	}
	if _, errOut, code := runCmd(t, "config", "key", "rotate"); code != 2 || !strings.Contains(errOut, "pass --force") || saved != 0 {
		t.Errorf("rotate without a terminal: exit %d %q", code, errOut)
	}
	envKey = true
	if out, _, code := runCmd(t, "config", "key", "rotate", "--force"); code != 0 || saved != 1 || !strings.Contains(out, "ROADIE_KEY is set and wins") {
		t.Errorf("rotate --force: exit %d %q", code, out)
	}
	dep.terminal = func() bool { return true }
	dep.stdin = strings.NewReader("n\n")
	if _, errOut, _ := runCmd(t, "config", "key", "rotate"); saved != 1 || !strings.Contains(errOut, "Replace the consistency key?") {
		t.Errorf("rotate declined: saved %d %q", saved, errOut)
	}
	dep.stdin = strings.NewReader("y\n")
	if runCmd(t, "config", "key", "rotate"); saved != 2 {
		t.Errorf("rotate on a terminal: saved %d", saved)
	}
	if out, _, code := runCmd(t, "config", "key", "forget"); code != 0 || forgot != 1 || !strings.Contains(out, "Removed "+keyFile) {
		t.Errorf("forget: exit %d %q", code, out)
	}
	if _, errOut, code := runCmd(t, "config", "key", "rotat"); code != 2 || !strings.Contains(errOut, "did you mean: rhythm-roadie config key rotate") {
		t.Errorf("typo: exit %d %q", code, errOut)
	}
}

func fakeProfiles() []profile.Def {
	return []profile.Def{
		{ID: "wordpress", Name: "WordPress core", CheckedAgainst: "install (WordPress 6.8.x)",
			Detect: profile.DetectDef{TablesAny: []string{"{prefix}options"}},
			Rules: []core.Rule{{ID: "wordpress.users.user_email", Profile: "wordpress", Table: "{prefix}users", Column: "user_email", Action: core.Fake, Fake: core.KEmail},
				{ID: "wordpress.usermeta.billing_email", Table: "{prefix}usermeta", Column: "meta_value", Where: map[string]string{"meta_key": "billing_email"}, Action: core.Fake, Fake: core.KEmail}},
			Truncate: []string{"{prefix}woocommerce_sessions"}},
		{ID: "gravity-forms", Name: "Gravity Forms", CheckedAgainst: "docs (https://docs.gravityforms.com/database-storage-structure-reference/)"},
	}
}

func TestProfiles(t *testing.T) {
	testDeps(t, func(d *deps) {
		d.loadProfiles = func(context.Context, bool) ([]profile.Def, string, error) { return fakeProfiles(), "2026.10.26", nil }
	})
	out, _, code := runCmd(t, "profiles")
	one, _, code1 := runCmd(t, "profiles", "wordpress")
	if code != 0 || code1 != 0 {
		t.Fatalf("exit %d %d", code, code1)
	}
	testutil.Golden(t, "profiles.txt", []byte(out+"\n$ rhythm-roadie profiles wordpress\n"+one))
	js, _, _ := runCmd(t, "profiles", "--json")
	var e struct {
		Schema, Kind string
		Data         struct {
			Pack     string `json:"profile_pack"`
			Profiles []struct {
				ID             string `json:"id"`
				CheckedAgainst string `json:"checked_against"`
			}
		}
	}
	if err := json.Unmarshal([]byte(js), &e); err != nil || e.Schema != "rhythm-roadie/v1" || e.Kind != "profiles" || e.Data.Pack != "2026.10.26" ||
		len(e.Data.Profiles) != 2 || e.Data.Profiles[1].CheckedAgainst == "" {
		t.Errorf("--json: %v %s", err, js)
	}
	if _, errOut, code := runCmd(t, "profiles", "wordpres"); code != 2 || !strings.Contains(errOut, "did you mean: rhythm-roadie profiles wordpress") {
		t.Errorf("unknown: exit %d %q", code, errOut)
	}
	dep.loadProfiles = func(context.Context, bool) ([]profile.Def, string, error) { return nil, "", core.ErrNotBuilt }
	if _, _, code := runCmd(t, "profiles"); code != 3 {
		t.Errorf("profiles that can't load: exit %d", code)
	}
}

func TestExplainGolden(t *testing.T) {
	testDeps(t, nil)
	var b bytes.Buffer
	for _, id := range []string{"uncovered-column", "key.stripe-live", "target-is-production", "pseudonymised"} {
		out, _, code := runCmd(t, "explain", id)
		if code != 0 {
			t.Errorf("%s: exit %d", id, code)
		}
		b.WriteString("$ rhythm-roadie explain " + id + "\n" + out + "\n")
	}
	testutil.Golden(t, "explain.txt", b.Bytes())
	footer := "Pseudonymised data is still personal data under the GDPR (Article 4(5), Recital 26).\n" +
		"This report shows what Roadie's checks found in this copy; it is not a statement that\n" +
		"the copy holds no personal data. Signed by roadie:7Q3kXc2… (rhythm-roadie report check report.json):\n" +
		"the signature shows who ran Roadie, which version and rules, on which input and that\n" +
		"the report is unchanged since. It doesn't prove the data holds no personal data.\n"
	if out, _, _ := runCmd(t, "explain", "pseudonymised"); !strings.HasSuffix(out, footer) {
		t.Errorf("explain pseudonymised:\n%s", out)
	}
	// A rule ID reads its profile's text.
	if out, _, code := runCmd(t, "explain", "woocommerce.wc_orders.billing_email"); code != 0 || !strings.HasPrefix(out, "# woocommerce: ") {
		t.Errorf("rule family: exit %d %q", code, out)
	}
	// prepare's stop for a dump file points at explain config, so that
	// page says what each command takes; wp-alias has its own page.
	if out, _, code := runCmd(t, "explain", "config"); code != 0 || !strings.Contains(out, "prepare: a staging site or database only") || !strings.Contains(out, "on the command line") {
		t.Errorf("explain config: exit %d %q", code, out)
	}
	if out, _, code := runCmd(t, "explain", "wp-alias"); code != 0 || !strings.HasPrefix(out, "# wp-alias: ") {
		t.Errorf("explain wp-alias: exit %d %q", code, out)
	}
	if _, errOut, code := runCmd(t, "explain", "uncoverd-column"); code != 2 || !strings.Contains(errOut, "did you mean: rhythm-roadie explain uncovered-column") {
		t.Errorf("unknown: exit %d %q", code, errOut)
	}
	for _, bad := range [][]string{{}, {"a", "b"}, {"../main.go"}, {"explain/../help"}} {
		if _, _, code := runCmd(t, "explain", bad...); code != 2 {
			t.Errorf("explain %q: exit %d", bad, code)
		}
	}
	js, _, _ := runCmd(t, "explain", "pii.email", "--json")
	if !strings.Contains(js, `"kind": "explain"`) || !strings.Contains(js, `"id\": \"pii.email\"`) && !strings.Contains(js, "pii.email") {
		t.Errorf("--json: %s", js)
	}
}

// explain import names the export that refreshes a PostgreSQL target
// twice; explain dump-refused names the renames and drops it refuses.
func TestExplainImportAndRefused(t *testing.T) {
	testDeps(t, nil)
	out, _, code := runCmd(t, "explain", "import")
	if code != 0 || !strings.Contains(out, "pg_dump --clean --if-exists") || !strings.Contains(out, "42710") {
		t.Errorf("explain import: exit %d %q", code, out)
	}
	out, _, code = runCmd(t, "explain", "dump-refused")
	if code != 0 || !strings.Contains(out, "ALTER TABLE ... RENAME TO another table") || !strings.Contains(out, "DROP TABLE without IF EXISTS") {
		t.Errorf("explain dump-refused: exit %d %q", code, out)
	}
}

// Every ID of core/ids.go has an explain text and every text names a
// known ID, a profile or "pseudonymised".
func TestExplainCoversEveryID(t *testing.T) {
	ids := core.FindingIDs()
	ids = append(ids, core.ErrorCodes()...)
	for _, p := range core.KeyProviders() {
		ids = append(ids, core.KeyLiveID(p))
	}
	ids = append(ids, profileIDs...)
	ids = append(ids, engineRules...)
	// Every check ID verify lists (data.checks, JUnit) has a page too.
	ids = append(ids, verify.CheckIDs()...)
	for _, id := range ids {
		found, text, ok := explainText(id)
		if !ok || found != id {
			t.Errorf("no explain text for %s", id)
			continue
		}
		if !strings.HasPrefix(text, "# "+id+": ") || !strings.Contains(text, "By hand:") && id != "pseudonymised" {
			t.Errorf("%s: text doesn't follow the layout", id)
		}
	}
	for _, id := range explainIDs() {
		if !core.KnownID(id) && !slices.Contains(profileIDs, id) && !slices.Contains(engineRules, id) && !slices.Contains(verify.CheckIDs(), id) && id != "pseudonymised" {
			t.Errorf("explain/%s.md names no known ID", id)
		}
	}
	// A rule ID as data.changes names it finds its family's text.
	for id, want := range map[string]string{
		"killswitch.options.option_value.woocommerce_stripe_settings.testmode": "killswitch",
		"killswitch.wc_webhooks.secret":                                        "killswitch", "killswitch.wc_webhooks.status": "killswitch",
		"urls": "urls", "keys": "keys", "action-scheduler-args": "action-scheduler-args",
		"woocommerce.wc_orders.billing_email": "woocommerce",
	} {
		if found, _, ok := explainText(id); !ok || found != want {
			t.Errorf("explain %s found %q", id, found)
		}
	}
}

// Every rule name the engine and the kill switch write into data.changes
// has a text: the engine's change names are in engine/transform.go and
// engine/rowfix.go.
func TestExplainCoversEngineRules(t *testing.T) {
	n := 0
	for _, file := range []string{"../../engine/transform.go", "../../engine/rowfix.go"} {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range regexp.MustCompile(`(?m)^\s*change\w+\s*=\s*"([a-z_-]+)"|out\.change\("([a-z_-]+)"`).FindAllStringSubmatch(string(b), -1) {
			id := m[1] + m[2]
			n++
			if !slices.Contains(engineRules, id) {
				t.Errorf("%s: rule %s has no explain text", file, id)
			}
		}
	}
	if n < 8 {
		t.Errorf("found %d rule names; the pattern no longer matches the engine", n)
	}
}

// demoFixture fakes the demo's shop, engine and verify: the copy passes,
// the verify of the copy passes and the verify of the shop fails.
func useDemo(t *testing.T, set func(d *deps)) {
	t.Helper()
	fx := &verifyFixture{results: []*core.VerifyResult{heuristicPass(), heuristicFail()}}
	useVerify(t, fx, func(d *deps) {
		d.demoShop = func(w io.Writer, o demo.Options) error {
			if o.Seed != demoSeed || !o.Leaky {
				t.Errorf("demo options %+v", o)
			}
			_, err := io.WriteString(w, "-- the demo shop (fakes only)\n")
			return err
		}
		d.demoProduction = func() []string { return []string{"demo-shop.example"} }
		d.run = func(_ context.Context, j engine.Job) (*core.Result, error) {
			if j.Mode == engine.Plan {
				r := planResult()
				r.Uncovered, r.Findings = nil, nil
				return r, nil
			}
			if !j.Opts.Offline || !j.Opts.Yes || j.Opts.URL != demoURL {
				t.Errorf("demo copy job %+v", j.Opts)
			}
			return copyPass(), nil
		}
		if set != nil {
			set(d)
		}
	})
}

func TestDemo(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("RHYTHM_CACHE_DIR", cache)
	useDemo(t, nil)
	testutil.NoNetwork(t)
	out, errOut, code := runCmd(t, "demo", "--plain")
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, out, errOut)
	}
	pass, fail := strings.Index(out, "PASS   staging.sql is a pseudonymised copy"), strings.Index(out, "FAIL   shop.sql is not a safe copy yet")
	if pass < 0 || fail < pass || strings.Count(out, "report of the") != 2 || !strings.Contains(out, honestyLine) {
		t.Errorf("demo screen:\n%s", out)
	}
	if left, _ := filepath.Glob(filepath.Join(cache, "roadie", "demo-*")); len(left) != 0 {
		t.Errorf("demo left %q behind", left)
	}
	// --keep keeps the folder and says where.
	useDemo(t, nil)
	out, _, _ = runCmd(t, "demo", "--keep", "--plain")
	left, _ := filepath.Glob(filepath.Join(cache, "roadie", "demo-*"))
	if len(left) != 1 || !strings.Contains(out, "kept the demo's files in "+left[0]) {
		t.Errorf("--keep: %q\n%s", left, out)
	}
	if st, err := os.Stat(left[0]); err != nil || (os.PathSeparator == '/' && st.Mode().Perm() != 0o700) {
		t.Errorf("demo folder mode: %v %v", st, err)
	}
	// A shop that can't be made stops the demo with its code.
	useDemo(t, func(d *deps) { d.demoShop = func(io.Writer, demo.Options) error { return core.ErrNotBuilt } })
	if _, _, code := runCmd(t, "demo"); code != 3 {
		t.Errorf("no shop: exit %d", code)
	}
	// With --json there is no screen: the reason goes to stderr once.
	useDemo(t, func(d *deps) { d.demoShop = func(io.Writer, demo.Options) error { return core.ErrNotBuilt } })
	out, errOut, code = runCmd(t, "demo", "--json")
	if code != 3 || out != "" || strings.Count(errOut, core.ErrNotBuilt.Error()) != 1 {
		t.Errorf("--json stop: exit %d stdout %q stderr %q", code, out, errOut)
	}
}

// The demo end to end with the real engine, verify and demo shop: one
// PASS, one FAIL, under 10 seconds and no network. It turns on by itself
// once the engine, values, io, detect and testbed lanes have merged.
func TestDemoEndToEnd(t *testing.T) {
	if err := demo.Shop(io.Discard, demo.Options{Seed: demoSeed, Customers: 1, Leaky: true}); errors.Is(err, core.ErrNotBuilt) {
		t.Skip("needs the testbed lane: internal/demo.Shop isn't built yet")
	}
	if _, err := engine.Run(context.Background(), engine.Job{Mode: engine.Plan}); errors.Is(err, core.ErrNotBuilt) {
		t.Skip("needs the engine, values, io and detect lanes: engine.Run isn't built yet")
	}
	cache := t.TempDir()
	t.Setenv("RHYTHM_CACHE_DIR", cache)
	old := dep
	t.Cleanup(func() { dep = old })
	dep = realDeps()
	testutil.NoNetwork(t)
	start := time.Now()
	out, errOut, code := runCmd(t, "demo", "--plain", "--offline")
	if took := time.Since(start); took > 10*time.Second {
		t.Errorf("demo took %v, want under 10 s", took)
	}
	if code != 0 || strings.Count(out, "PASS   ") < 1 || !strings.Contains(out, "FAIL   ") {
		t.Fatalf("exit %d\n%s\n%s", code, out, errOut)
	}
	if left, _ := filepath.Glob(filepath.Join(cache, "roadie", "demo-*")); len(left) != 0 {
		t.Errorf("demo left %q behind", left)
	}
}

// Country and state rules are kept under the default keep: [country,
// state], so profiles and config show say so instead of "blank"; a
// config that keeps nothing shows the rule's action again.
func TestProfilesShowKeptParts(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	out, _, code := run(t, "profiles", "woocommerce", "--offline")
	if code != 0 || !regexp.MustCompile(`\(meta_key = _billing_country\)  +kept \(keep: country\)`).MatchString(out) ||
		!regexp.MustCompile(`\(meta_key = _shipping_state\)  +kept \(keep: state\)`).MatchString(out) {
		t.Errorf("profiles: exit %d\n%s", code, out)
	}
	// Every row's RULE starts in the same column, at least two spaces
	// after the longest COLUMN value.
	var starts []int
	for line := range strings.Lines(out) {
		if i := strings.Index(line, "fake "); i > 0 && strings.Contains(line, "meta_value") {
			starts = append(starts, utf8.RuneCountInString(line[:i]))
		}
	}
	if len(starts) < 2 || slices.Min(starts) != slices.Max(starts) {
		t.Errorf("RULE starts at %v", starts)
	}
	out, _, code = run(t, "config", "show", "--offline")
	if code != 0 || !strings.Contains(out, "(meta_key = _billing_country): kept (keep: country)") {
		t.Errorf("config show: exit %d\n%s", code, out)
	}
	cfg := filepath.Join(dir, "nokeep.yaml")
	if err := os.WriteFile(cfg, []byte("pseudonymise:\n  keep: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, _ = run(t, "profiles", "woocommerce", "--offline", "--config", cfg)
	if strings.Contains(out, "kept (keep:") || !regexp.MustCompile(`\(meta_key = _billing_country\)  +blank`).MatchString(out) {
		t.Errorf("keep: []:\n%s", out)
	}
}

// A standalone verify reports the stack it found (data.stack), not
// "not detected".
func TestVerifyReportsStack(t *testing.T) {
	fx := &verifyFixture{results: []*core.VerifyResult{heuristicPass()}}
	useVerify(t, fx, func(d *deps) {
		d.verifyJob = func(_ context.Context, _ engine.Job, target core.Endpoint, _ *core.Endpoint) (verify.Job, error) {
			return verify.Job{Target: target, Stack: core.StackInfo{Name: core.StackWordPress, Profiles: []string{"wordpress"}}}, nil
		}
	})
	out, errOut, code := runCmd(t, "verify", "staging.sql", "--no-config", "--md")
	if code != 0 || !strings.Contains(out, `"Stack":{"Name":"wordpress","Profiles":["wordpress"]`) {
		t.Errorf("exit %d %s\n%s", code, errOut, out)
	}
}

// The profiles list gives every name its own column: a name that fills
// the old fixed width ran into "docs:".
func TestProfilesListColumns(t *testing.T) {
	t.Chdir(t.TempDir())
	out, _, code := run(t, "profiles", "--offline")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	var starts []int
	for line := range strings.Lines(out) {
		if i := strings.Index(line, "docs:"); i > 0 {
			if line[i-2:i] != "  " {
				t.Errorf("no gap before docs: %q", line)
			}
			starts = append(starts, utf8.RuneCountInString(line[:i]))
		}
	}
	if len(starts) < 2 || slices.Min(starts) != slices.Max(starts) {
		t.Errorf("checked against starts at %v\n%s", starts, out)
	}
}

func TestWriteColumns(t *testing.T) {
	var b bytes.Buffer
	writeColumns(&b, [][]string{{"TABLE", "COLUMN", "RULE"}, {"wp_postmeta", "meta_value → a.b", "fake email"}, {"t", "c", ""}})
	want := "  TABLE        COLUMN            RULE\n  wp_postmeta  meta_value → a.b  fake email\n  t            c\n"
	if b.String() != want {
		t.Errorf("got\n%s\nwant\n%s", b.String(), want)
	}
}
