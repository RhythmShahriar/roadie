// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"io"
	"io/fs"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/engine"
	"github.com/RhythmShahriar/roadie/roadiecfg"
	"github.com/RhythmShahriar/roadie/verify"
)

// built is a command line turned into what it runs.
type built struct {
	p    *parsed
	job  engine.Job
	vo   verify.Options
	prod []string
}

// baseArgs are the arguments a command needs before the flag under test.
var baseArgs = map[string][]string{
	"copy":    {"--from", "prod.sql", "--to", "out.sql", "--url", "https://staging.shop.test"},
	"prepare": {"/var/www/staging"},
	"plan":    {"--from", "prod.sql"},
	"verify":  {"out.sql"},
}

func buildFor(t *testing.T, cfg *roadiecfg.Config, cmd string, args ...string) built {
	t.Helper()
	p := mustParse(t, cmd, append(slices.Clone(baseArgs[cmd]), args...)...)
	b := built{p: p}
	switch cmd {
	case "copy", "prepare", "plan":
		j, err := p.buildJob(p.mode(), cfg, io.Discard)
		if err != nil {
			t.Fatalf("buildJob %q: %v", args, err)
		}
		b.job = j
	case "verify":
		vo, prod, err := p.verifyOptions(cfg, dep.env, testNow)
		if err != nil {
			t.Fatalf("verifyOptions %q: %v", args, err)
		}
		b.vo, b.prod = vo, prod
	}
	return b
}

// flagRows names, for every flag of PRD 6.3 to 6.10 (plus
// --max-output-ratio and --schema-only), a command line that sets it and
// the setting it lands in. TestEveryFlagHasARow fails on a flag without one.
var flagRows = []struct {
	flag, cmd string
	args      []string
	ok        func(b built) bool
}{
	// Output and Settings (PRD 6.3).
	{"json", "copy", []string{"--json"}, func(b built) bool { return b.p.f.g.json }},
	{"md", "copy", []string{"--md"}, func(b built) bool { return b.p.f.g.md }},
	{"html", "copy", []string{"--html"}, func(b built) bool { return b.p.f.g.html }},
	{"sarif", "verify", []string{"--sarif"}, func(b built) bool { return b.p.f.g.sarif }},
	{"junit", "verify", []string{"--junit"}, func(b built) bool { return b.p.f.g.junit }},
	{"o", "copy", []string{"-o", "r.json"}, func(b built) bool { return slices.Equal(b.p.f.g.outputs, []string{"r.json"}) }},
	{"output", "copy", []string{"--output", "r.md"}, func(b built) bool { return slices.Equal(b.p.f.g.outputs, []string{"r.md"}) }},
	{"open", "copy", []string{"--open"}, func(b built) bool { return b.p.f.g.open }},
	{"q", "copy", []string{"-q"}, func(b built) bool { return b.p.f.g.quiet }},
	{"quiet", "copy", []string{"--quiet"}, func(b built) bool { return b.p.f.g.quiet }},
	{"plain", "copy", []string{"--plain"}, func(b built) bool { return b.p.f.g.plain }},
	{"no-color", "copy", []string{"--no-color"}, func(b built) bool { return b.p.f.g.noColor }},
	{"v", "copy", []string{"-v"}, func(b built) bool { return b.p.f.g.verbose }},
	{"verbose", "copy", []string{"--verbose"}, func(b built) bool { return b.p.f.g.verbose }},
	{"debug", "copy", []string{"--debug"}, func(b built) bool { return b.p.f.g.debug }},
	{"config", "copy", []string{"--config", "x.yaml"}, func(b built) bool { return b.p.f.g.config == "x.yaml" }},
	{"no-config", "copy", []string{"--no-config"}, func(b built) bool { return b.p.f.g.noConfig }},
	{"offline", "copy", []string{"--offline"}, func(b built) bool { return b.job.Opts.Offline }},

	// copy: Where (PRD 6.4).
	{"from", "copy", nil, func(b built) bool { return b.job.From.Raw == "prod.sql" }},
	{"to", "copy", nil, func(b built) bool { return b.job.To.Raw == "out.sql" }},
	{"url", "copy", nil, func(b built) bool { return b.job.Opts.URL == "https://staging.shop.test" }},
	{"url-map", "copy", []string{"--url-map", "fr.shop.com=fr.staging.shop.test", "--url-map", "cdn.shop.com=cdn.staging.shop.test"},
		func(b built) bool {
			return reflect.DeepEqual(b.job.Opts.URLMap, map[string]string{"fr.shop.com": "fr.staging.shop.test", "cdn.shop.com": "cdn.staging.shop.test"})
		}},
	{"stack", "copy", []string{"--stack", "laravel"}, func(b built) bool { return b.job.Opts.Stack == "laravel" }},
	{"site", "copy", []string{"--site", "2", "--site", "3,2"}, func(b built) bool { return slices.Equal(b.job.Opts.Sites, []int{2, 3}) }},
	{"schema", "copy", []string{"--schema", "schema.sql"}, func(b built) bool { return b.job.Opts.SchemaFile == "schema.sql" }},
	{"dry-run", "copy", []string{"--dry-run"}, func(b built) bool { return b.job.Mode == engine.Plan }},

	// prepare (PRD 6.5).
	{"only", "prepare", []string{"--only", "keys,side-effects"}, func(b built) bool { return slices.Equal(b.job.Opts.Only, []string{"keys", "side-effects"}) }},
	{"production-url", "prepare", []string{"--production-url", "https://shop.com"}, func(b built) bool { return b.job.Opts.ProductionURL == "https://shop.com" }},
	{"no-backup", "prepare", []string{"--no-backup"}, func(b built) bool { return b.job.Opts.NoBackup }},

	// Pseudonymise.
	{"keep-users", "copy", []string{"--keep-users", "admin,*@agency.example"}, func(b built) bool {
		return slices.Equal(b.job.Opts.KeepUsers, []string{"admin", "*@agency.example"})
	}},
	{"keep-key", "copy", []string{"--keep-key"}, func(b built) bool { return b.job.Opts.KeepKey }},
	{"email-domain", "copy", []string{"--email-domain", "shop.test"}, func(b built) bool { return b.job.Opts.EmailDomain == "shop.test" }},
	{"admin-password-env", "copy", []string{"--admin-password-env", "ROADIE_ADMIN_PASSWORD"}, func(b built) bool {
		return b.job.Opts.AdminPasswordEnv == "ROADIE_ADMIN_PASSWORD"
	}},
	{"allow-uncovered", "copy", []string{"--allow-uncovered"}, func(b built) bool { return b.job.Opts.AllowUncovered }},
	{"drop", "copy", []string{"--drop", "wp_wfls_*"}, func(b built) bool { return slices.Equal(b.job.Opts.Drop, []string{"wp_wfls_*"}) }},
	{"truncate", "copy", []string{"--truncate", "wp_actionscheduler_logs"}, func(b built) bool {
		return slices.Equal(b.job.Opts.Truncate, []string{"wp_actionscheduler_logs"})
	}},
	{"one-pass", "copy", []string{"--one-pass"}, func(b built) bool { return b.job.Opts.OnePass }},
	{"keep-routines", "copy", []string{"--keep-routines"}, func(b built) bool { return b.job.Opts.KeepRoutines }},
	{"keep-app-key", "copy", []string{"--keep-app-key"}, func(b built) bool { return b.job.Opts.KeepAppKey }},

	// Keys and side effects.
	{"keys", "copy", []string{"--keys", "blank"}, func(b built) bool { return b.job.Opts.Keys == "blank" }},
	{"no-kill-switch", "copy", []string{"--no-kill-switch"}, func(b built) bool { return b.job.Opts.NoKillSwitch }},
	{"echo-url", "copy", []string{"--echo-url", "http://127.0.0.1:9025"}, func(b built) bool { return b.job.Opts.EchoURL == "http://127.0.0.1:9025" }},
	{"echo-proxy", "copy", []string{"--echo-proxy", "127.0.0.1:8026"}, func(b built) bool { return b.job.Opts.EchoProxy == "127.0.0.1:8026" }},
	{"noindex", "copy", []string{"--noindex=off"}, func(b built) bool { return b.job.Opts.NoIndex }},
	{"basic-auth", "copy", []string{"--basic-auth", "team"}, func(b built) bool { return b.job.Opts.BasicAuth == "team" }},

	// Target safety.
	{"confirm-target", "copy", []string{"--confirm-target", "staging.shop.test"}, func(b built) bool { return b.job.Opts.ConfirmTarget == "staging.shop.test" }},
	{"target-is-staging", "copy", []string{"--target-is-staging", "staging.shop.test"}, func(b built) bool {
		return b.job.Opts.TargetIsStaging == "staging.shop.test"
	}},
	{"y", "copy", []string{"-y"}, func(b built) bool { return b.job.Opts.Yes }},
	{"yes", "copy", []string{"--yes"}, func(b built) bool { return b.job.Opts.Yes }},
	{"fix-collation", "copy", []string{"--fix-collation"}, func(b built) bool { return b.job.Opts.FixCollation }},
	{"jobs", "copy", []string{"--jobs", "3"}, func(b built) bool { return b.job.Opts.Jobs == 3 }},
	{"max-memory", "copy", []string{"--max-memory", "512MiB"}, func(b built) bool { return b.job.Opts.MaxMemory == 512<<20 }},
	{"max-value", "copy", []string{"--max-value", "1GiB"}, func(b built) bool { return b.job.Opts.MaxValue == 1<<30 }},
	{"max-output-ratio", "copy", []string{"--max-output-ratio", "50"}, func(b built) bool { return b.job.Opts.MaxOutputRatio == 50 }},

	// After the copy.
	{"no-verify", "copy", []string{"--no-verify"}, func(b built) bool { return b.job.Opts.NoVerify }},
	{"fail-on", "copy", []string{"--fail-on", "critical"}, func(b built) bool { return b.job.Opts.FailOn == core.SeverityCritical }},
	{"soundcheck", "copy", []string{"--soundcheck"}, func(b built) bool { return b.job.Opts.Soundcheck }},
	{"sign", "copy", []string{"--sign=off"}, func(b built) bool { s, _ := b.p.signing(nil); return !s }},
	{"key", "copy", []string{"--key", "ci.key"}, func(b built) bool { _, k := b.p.signing(nil); return k == "ci.key" }},

	// plan (PRD 6.7).
	{"schema-only", "plan", []string{"--schema-only"}, func(b built) bool { return b.job.Opts.SchemaOnly }},

	// verify (PRD 6.6).
	{"production", "verify", []string{"--production", "shop.com,*.shop.com"}, func(b built) bool { return slices.Equal(b.prod, []string{"shop.com", "*.shop.com"}) }},
	{"from", "verify", []string{"--from", "prod.sql"}, func(b built) bool { return b.p.f.from == "prod.sql" }},
	{"allow-emails", "verify", []string{"--allow-emails", "*@agency.example"}, func(b built) bool { return slices.Equal(b.vo.Allow.Emails, []string{"*@agency.example"}) }},
	{"allow-phones", "verify", []string{"--allow-phones", "+44*"}, func(b built) bool { return slices.Equal(b.vo.Allow.Phones, []string{"+44*"}) }},
	{"site", "verify", []string{"--site", "https://staging.shop.test"}, func(b built) bool { return b.vo.SiteURL == "https://staging.shop.test" }},
	{"insecure-tls", "verify", []string{"--insecure-tls", "staging.shop.test"}, func(b built) bool { return b.vo.InsecureTLS == "staging.shop.test" }},
	{"echo", "verify", []string{"--echo=http://127.0.0.1:9025"}, func(b built) bool { return b.vo.Echo && b.vo.EchoURL == "http://127.0.0.1:9025" }},
	{"sample", "verify", []string{"--sample", "100"}, func(b built) bool { return b.vo.Sample == 100 }},
	{"skip", "verify", []string{"--skip", "names,addresses"}, func(b built) bool { return slices.Equal(b.vo.Skip, []string{"names", "addresses"}) }},
	{"fail-on", "verify", []string{"--fail-on", "medium"}, func(b built) bool { return b.vo.FailOn == core.SeverityMedium }},
	{"baseline", "verify", []string{"--baseline", "old.json"}, func(b built) bool { return b.vo.Baseline == "old.json" }},
	{"sign", "verify", []string{"--sign=off"}, func(b built) bool { s, _ := b.p.signing(nil); return !s }},
	{"key", "verify", []string{"--key", "ci.key"}, func(b built) bool { _, k := b.p.signing(nil); return k == "ci.key" }},

	// config, report, demo (PRD 6.8 to 6.10).
	{"stack", "config", []string{"--stack", "woocommerce"}, func(b built) bool { return b.p.f.stack == "woocommerce" }},
	{"from", "config", []string{"--from", "prod.sql"}, func(b built) bool { return b.p.f.from == "prod.sql" }},
	{"force", "config", []string{"--force"}, func(b built) bool { return b.p.f.force }},
	{"brand-name", "report", []string{"--brand-name", "Agency"}, func(b built) bool { return b.p.f.brandName == "Agency" }},
	{"brand-logo", "report", []string{"--brand-logo", "logo.png"}, func(b built) bool { return b.p.f.brandLogo == "logo.png" }},
	{"key", "report", []string{"--key", "pub.pem"}, func(b built) bool { return b.p.f.key == "pub.pem" }},
	{"input", "report", []string{"--input", "prod.sql"}, func(b built) bool { return b.p.f.input == "prod.sql" }},
	{"keep", "demo", []string{"--keep"}, func(b built) bool { return b.p.f.keep }},
}

func TestFlagRows(t *testing.T) {
	testDeps(t, nil)
	for _, r := range flagRows {
		b := buildFor(t, nil, r.cmd, r.args...)
		if !r.ok(b) {
			t.Errorf("%s --%s %q didn't land in its setting", r.cmd, r.flag, r.args)
		}
	}
}

// TestEveryFlagHasARow fails on a registered flag without a row, on a row
// for a flag nobody registers and on a flag its command's help leaves out.
func TestEveryFlagHasARow(t *testing.T) {
	rows := map[string]map[string]bool{} // flag -> commands with a row
	for _, r := range flagRows {
		if rows[r.flag] == nil {
			rows[r.flag] = map[string]bool{}
		}
		rows[r.flag][r.cmd] = true
	}
	registered := map[string]bool{}
	for cmd := range commandGroups {
		help := helpFor(cmd)
		if cmd == "plan" { // plan's help sends the reader to copy's and prepare's
			help += helpFor("copy") + helpFor("prepare")
		}
		newFlags().flagSet(cmd).VisitAll(func(fl *flag.Flag) {
			registered[fl.Name] = true
			if rows[fl.Name] == nil {
				t.Errorf("%s --%s has no row in flagRows", cmd, fl.Name)
			}
			if !strings.Contains(help, dashed(fl.Name)) {
				t.Errorf("%s registers %s but its help doesn't name it", cmd, dashed(fl.Name))
			}
		})
	}
	for name := range rows {
		if !registered[name] {
			t.Errorf("flagRows has --%s, which no command registers", name)
		}
	}
}

// TestHelpNamesOnlyRegisteredFlags reads each command's help for flag
// lines and checks the command parses every flag it lists.
func TestHelpNamesOnlyRegisteredFlags(t *testing.T) {
	line := regexp.MustCompile(`(?m)^  (?:-([a-z]), |    )--([a-z][a-z-]*)`)
	for cmd := range commandGroups {
		fs := newFlags().flagSet(cmd)
		for _, m := range line.FindAllStringSubmatch(helpFor(cmd), -1) {
			for _, name := range m[1:] {
				if name != "" && fs.Lookup(name) == nil {
					t.Errorf("help %s lists %s, which it doesn't parse", cmd, dashed(name))
				}
			}
		}
	}
}

func TestDefaults(t *testing.T) {
	testDeps(t, nil)
	o := buildFor(t, nil, "copy").job.Opts
	if o.EmailDomain != "example.invalid" || o.Keys != "swap" || o.EchoURL != "http://127.0.0.1:8025" ||
		o.Jobs < 1 || o.Jobs > 8 || o.MaxMemory != 0 || o.MaxValue != 256<<20 || o.MaxOutputRatio != 20 ||
		o.FailOn != core.SeverityHigh || o.NoIndex || o.Offline {
		t.Errorf("copy defaults: %+v", o)
	}
	if s, k := buildFor(t, nil, "copy").p.signing(nil); !s || k != "" {
		t.Errorf("signing default: %v %q", s, k)
	}
	if o := buildFor(t, nil, "plan").job.Opts; o.FailOn != core.SeverityNone {
		t.Errorf("plan --fail-on default = %v, want none", o.FailOn)
	}
	vo := buildFor(t, nil, "verify").vo
	if vo.FailOn != core.SeverityHigh || vo.Echo || vo.EchoURL != "http://127.0.0.1:8025" || vo.Sample != 0 {
		t.Errorf("verify defaults: %+v", vo)
	}
	if vo := buildFor(t, nil, "verify", "--echo").vo; !vo.Echo || vo.EchoURL != "http://127.0.0.1:8025" {
		t.Errorf("--echo alone: %+v", vo)
	}
}

func TestEnvironmentDefaults(t *testing.T) {
	testDeps(t, func(d *deps) {
		d.env = func(name string) (string, bool) {
			v, ok := map[string]string{"RHYTHM_ECHO_URL": "http://127.0.0.1:7025", "RHYTHM_OFFLINE": "1"}[name]
			return v, ok
		}
	})
	o := buildFor(t, nil, "copy").job.Opts
	if o.EchoURL != "http://127.0.0.1:7025" || !o.Offline {
		t.Errorf("RHYTHM_ECHO_URL and RHYTHM_OFFLINE: %+v", o)
	}
}

// A flag wins over the config and the config over the default.
func TestConfigFillsDefaults(t *testing.T) {
	testDeps(t, nil)
	cfg := &roadiecfg.Config{URL: "https://from-config.test", Stack: "auto", Production: []string{"shop.com"},
		URLMap: map[string]string{"cdn.shop.com": "cdn.staging.test", "fr.shop.com": "fr.config.test"}}
	cfg.Source, cfg.Target = "config.sql", "config-out.sql"
	cfg.Pseudonymise.KeepUsers = []string{"admin"}
	cfg.Pseudonymise.EmailDomain = "shop.test"
	cfg.Pseudonymise.Consistency = "stable"
	cfg.Drop = []string{"wp_wfls_*"}
	cfg.Keys.Mode = "blank"
	cfg.SideEffects.Echo.URL = "http://127.0.0.1:6025"
	cfg.Noindex = true
	cfg.BasicAuth = true
	cfg.Verify.FailOn = "medium"
	cfg.Verify.Sign = true
	cfg.Verify.AllowEmails = []string{"info@shop.com"}
	cfg.Verify.Skip = []string{"names"}
	cfg.Verify.Ignore = []roadiecfg.Ignore{{ID: "pii.phone", Location: "wp_options shop_phone", Reason: "shop's own", Until: "2027-01-31"}}

	p := mustParse(t, "copy", "--url-map", "fr.shop.com=fr.flag.test", "--keys", "swap")
	j, err := p.buildJob(engine.Copy, cfg, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	o := j.Opts
	switch {
	case j.From.Raw != "config.sql" || j.To.Raw != "config-out.sql":
		t.Errorf("endpoints from config: %q %q", j.From.Raw, j.To.Raw)
	case o.URL != "https://from-config.test" || o.Stack != "" || o.EmailDomain != "shop.test" || !o.KeepKey:
		t.Errorf("config values: %+v", o)
	case o.URLMap["fr.shop.com"] != "fr.flag.test" || o.URLMap["cdn.shop.com"] != "cdn.staging.test":
		t.Errorf("url_map merge: %v", o.URLMap)
	case o.Keys != "swap":
		t.Errorf("--keys should win over keys.mode: %q", o.Keys)
	case !slices.Equal(o.KeepUsers, []string{"admin"}) || !slices.Equal(o.Drop, []string{"wp_wfls_*"}) || !slices.Equal(o.Production, []string{"shop.com"}):
		t.Errorf("lists from config: %+v", o)
	case o.EchoURL != "http://127.0.0.1:6025" || o.NoIndex || o.BasicAuth != "staging" || o.FailOn != core.SeverityMedium:
		t.Errorf("side effects and fail_on from config: %+v", o)
	}

	vp := mustParse(t, "verify", "x.sql", "--production", "*.shop.com", "--allow-emails", "ops@agency.example")
	vo, prod, err := vp.verifyOptions(cfg, dep.env, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(prod, []string{"shop.com", "*.shop.com"}) || !slices.Equal(vo.Allow.Emails, []string{"info@shop.com", "ops@agency.example"}) ||
		!slices.Equal(vo.Skip, []string{"names"}) || vo.FailOn != core.SeverityMedium || len(vo.Ignore) != 1 ||
		vo.Ignore[0].Until.Format("2006-01-02") != "2027-01-31" || vo.Ignore[0].Until.Location().String() != "UTC" {
		t.Errorf("verify from config: %+v %v", vo, prod)
	}

	cfg.Verify.Ignore[0].Until = "31/01/2027"
	if _, _, err := vp.verifyOptions(cfg, dep.env, testNow); err == nil {
		t.Error("a bad until date should be a config error")
	}
	cfg.Verify.Sign, cfg.Noindex = false, false
	if s, _ := vp.signing(cfg); s {
		t.Error("verify.sign: false should turn signing off")
	}
	if j, _ := mustParse(t, "copy").buildJob(engine.Copy, cfg, io.Discard); !j.Opts.NoIndex {
		t.Error("noindex: false should turn noindex off")
	}

	// --production adds to production: on copy, prepare and plan, once each.
	for _, cmd := range []struct {
		name string
		mode engine.Mode
		args []string
	}{
		{"copy", engine.Copy, nil},
		{"prepare", engine.Prepare, []string{"/var/www/staging"}},
		{"plan", engine.Plan, nil},
	} {
		args := append(slices.Clone(cmd.args), "--production", "*.shop.com,shop.com")
		j, err := mustParse(t, cmd.name, args...).buildJob(cmd.mode, cfg, io.Discard)
		if err != nil {
			t.Fatalf("%s: %v", cmd.name, err)
		}
		if !slices.Equal(j.Opts.Production, []string{"shop.com", "*.shop.com"}) {
			t.Errorf("%s --production: %v, want the config's shop.com then *.shop.com", cmd.name, j.Opts.Production)
		}
	}
	if j, _ := mustParse(t, "copy").buildJob(engine.Copy, cfg, io.Discard); !slices.Equal(j.Opts.Production, []string{"shop.com"}) {
		t.Errorf("without --production: %v, want the config's list only", j.Opts.Production)
	}
}

func TestFailOnValues(t *testing.T) {
	testDeps(t, nil)
	for name, want := range map[string]core.Severity{
		"none": core.SeverityNone, "info": core.SeverityInfo, "low": core.SeverityLow,
		"medium": core.SeverityMedium, "HIGH": core.SeverityHigh, "critical": core.SeverityCritical,
	} {
		if got := buildFor(t, nil, "copy", "--fail-on", name).job.Opts.FailOn; got != want {
			t.Errorf("--fail-on %s = %v", name, got)
		}
	}
	_, errOut, code := runCmd(t, "copy", "--from", "a.sql", "--to", "b.sql", "--fail-on", "severe")
	if code != 2 || !strings.Contains(errOut, `--fail-on "severe": want none, info, low, medium, high or critical`) {
		t.Errorf("bad --fail-on: exit %d %q", code, errOut)
	}
}

func TestRepeatableOutput(t *testing.T) {
	testDeps(t, nil)
	b := buildFor(t, nil, "verify", "-o", "r.json", "--output", "r.html", "-o", "r.sarif")
	if !slices.Equal(b.p.f.g.outputs, []string{"r.json", "r.html", "r.sarif"}) {
		t.Errorf("-o: %q", b.p.f.g.outputs)
	}
}

func TestURLMapParsing(t *testing.T) {
	good, err := parseURLMap([]string{"fr.shop.com=fr.staging.shop.test", " cdn.shop.com = cdn.staging.shop.test "})
	if err != nil || len(good) != 2 || good["cdn.shop.com"] != "cdn.staging.shop.test" {
		t.Errorf("good map: %v %v", good, err)
	}
	for _, bad := range [][]string{{"fr.shop.com"}, {"=x"}, {"a="}, {"a=b", "a=c"}} {
		if _, err := parseURLMap(bad); err == nil {
			t.Errorf("%q should not parse", bad)
		}
	}
	testDeps(t, nil)
	_, errOut, code := runCmd(t, "copy", "--url-map", "fr.shop.com")
	if code != 2 || !strings.Contains(errOut, "--url-map:") {
		t.Errorf("bad --url-map: exit %d %q", code, errOut)
	}
}

func TestSizes(t *testing.T) {
	for in, want := range map[string]int64{"1GiB": 1 << 30, "256MiB": 256 << 20, "512KiB": 512 << 10, "2GB": 2e9, "10MB": 1e7, "4096": 4096, "8B": 8} {
		if got, err := parseSize(in); err != nil || got != want {
			t.Errorf("parseSize(%q) = %d, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "lots", "-1GiB", "0", "1.5GiB", "99999999999TiB"} {
		if _, err := parseSize(bad); err == nil {
			t.Errorf("parseSize(%q) should fail", bad)
		}
	}
}

// --dry-run runs plan with the same arguments: the same job.
func TestDryRunIsPlan(t *testing.T) {
	testDeps(t, nil)
	jobOf := func(cmd string, args ...string) engine.Job {
		p := mustParse(t, cmd, args...)
		j, err := p.buildJob(p.mode(), nil, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		j.Clock = nil // a func; the rest must be equal
		return j
	}
	args := []string{"--from", "prod.sql", "--to", "/var/www/staging", "--url", "https://staging.shop.test", "--keep-users", "admin", "--jobs", "2"}
	if a, b := jobOf("copy", append(args, "--dry-run")...), jobOf("plan", args...); !reflect.DeepEqual(a, b) {
		t.Errorf("copy --dry-run\n%+v\nplan\n%+v", a, b)
	}
	pargs := []string{"/var/www/staging", "--only", "side-effects", "--production-url", "https://shop.com"}
	a, b := jobOf("prepare", append(pargs, "--dry-run")...), jobOf("plan", pargs...)
	if !reflect.DeepEqual(a, b) || a.Mode != engine.Plan || a.From.Raw != a.To.Raw {
		t.Errorf("prepare --dry-run\n%+v\nplan\n%+v", a, b)
	}
}

func TestUnknownFlagSuggests(t *testing.T) {
	testDeps(t, nil)
	_, errOut, code := runCmd(t, "copy", "--frm", "prod.sql")
	if code != 2 || !strings.Contains(errOut, "rhythm-roadie copy: unknown flag --frm") ||
		!strings.Contains(errOut, "did you mean: rhythm-roadie copy --from") ||
		!strings.Contains(errOut, "run 'rhythm-roadie help copy' for usage") {
		t.Errorf("exit %d: %q", code, errOut)
	}
	_, errOut, code = runCmd(t, "verify", "--skip", "emials")
	if code != 2 || !strings.Contains(errOut, "did you mean: --skip emails") {
		t.Errorf("--skip typo: exit %d: %q", code, errOut)
	}
	_, errOut, code = runCmd(t, "copy", "--jobs", "many")
	if code != 2 || !strings.Contains(errOut, "--jobs") || strings.Contains(errOut, " -jobs") {
		t.Errorf("bad --jobs: exit %d: %q", code, errOut)
	}
}

func TestSarifOnlyOnVerify(t *testing.T) {
	testDeps(t, nil)
	for _, cmd := range []string{"copy", "prepare", "plan", "config", "report", "profiles", "explain", "demo"} {
		for _, fl := range []string{"--sarif", "--junit"} {
			_, errOut, code := runCmd(t, cmd, fl)
			want := "rhythm-roadie " + cmd + ": " + fl + " is for verify only"
			if code != 2 || !strings.Contains(errOut, want) {
				t.Errorf("%s %s: exit %d %q", cmd, fl, code, errOut)
			}
		}
	}
	_, errOut, code := runCmd(t, "copy", "-o", "r.sarif")
	if code != 2 || !strings.Contains(errOut, "SARIF is for verify only") {
		t.Errorf("copy -o r.sarif: exit %d %q", code, errOut)
	}
}

func TestUsageChecks(t *testing.T) {
	testDeps(t, nil)
	for _, c := range []struct {
		cmd  string
		args []string
		want string
	}{
		{"copy", []string{"--json", "--md"}, "pick one of --json"},
		{"copy", []string{"-o", "r.txt"}, "the extension picks the format"},
		{"copy", []string{"--config", "a.yaml", "--no-config"}, "can't go together"},
		{"copy", []string{"--stack", "drupal"}, "want wordpress, laravel or sql"},
		{"config", []string{"init", "--stack", "magento"}, "want wordpress, woocommerce, laravel or sql"},
		{"copy", []string{"--keys", "rotate"}, "want swap or blank"},
		{"copy", []string{"--admin-password-env", "ADMIN_PW"}, "only ROADIE_ variables are read"},
		{"copy", []string{"--jobs", "9"}, "want 1 to 8"},
		{"copy", []string{"--max-output-ratio", "0"}, "want 1 or more"},
		{"copy", []string{"--max-memory", "lots"}, "want a size"},
		{"copy", []string{"--site", "two"}, "isn't a blog ID"},
		{"prepare", []string{"--only", "everything"}, "want data, keys, side-effects, urls or noindex"},
		{"verify", []string{"--sample", "-1"}, "want 0 (every row) or more"},
		{"verify", []string{"--echo=localhost"}, "want Echo's URL"},
		{"verify", []string{"--sign=maybe"}, "want on or off"},
	} {
		_, errOut, code := runCmd(t, c.cmd, c.args...)
		if code != 2 || !strings.Contains(errOut, c.want) {
			t.Errorf("%s %q: exit %d %q, want %q", c.cmd, c.args, code, errOut, c.want)
		}
	}
	// These pass the flags and stop when the job is built.
	for _, c := range []struct {
		cmd  string
		args []string
		want string
	}{
		{"copy", []string{"--url", "https://s.test"}, "rhythm-roadie copy: copy needs --from and --to (or source: and target: in .roadie/config.yaml; rhythm-roadie config init writes one)"},
		{"copy", []string{"--from", "a.sql"}, "copy needs --from and --to"},
		{"copy", []string{"stray", "--from", "a.sql", "--to", "b.sql"}, `unexpected argument "stray"`},
		{"prepare", nil, "prepare needs the staging copy"},
		{"prepare", []string{"a", "b"}, "one staging at a time"},
		{"plan", nil, "plan needs --from <source> or a <staging>"},
		{"plan", []string{"/var/www/staging", "--from", "prod.sql"}, "give either <staging> or --from and --to"},
	} {
		p := mustParse(t, c.cmd, c.args...)
		var errb bytes.Buffer
		_, err := p.buildJob(p.mode(), nil, &errb)
		if code := exitCode(&errb, err); code != 2 || !strings.Contains(errb.String(), c.want) {
			t.Errorf("%s %q: exit %d %q, want %q", c.cmd, c.args, code, errb.String(), c.want)
		}
	}
	// An endpoint error keeps its own code and exit.
	p := mustParse(t, "copy", "--from", "bad:x", "--to", "b.sql")
	var errb bytes.Buffer
	if _, err := p.buildJob(engine.Copy, nil, &errb); exitCode(&errb, err) != 2 || !strings.Contains(errb.String(), "can't read this endpoint") {
		t.Errorf("endpoint error: %v %q", err, errb.String())
	}
}

// An inline DSN password is accepted with the PRD 6.2 warning, once; one
// from the environment fills the endpoint without a warning.
func TestDSNPasswords(t *testing.T) {
	testDeps(t, func(d *deps) {
		d.env = func(name string) (string, bool) {
			if name == "ROADIE_TO_PASSWORD" {
				return "from-env", true
			}
			return "", false
		}
	})
	var errb bytes.Buffer
	p := mustParse(t, "copy", "--from", "mysql://shop:pw1@db.shop.com/shop", "--to", "mysql://staging@localhost/staging", "--url", "https://s.test")
	j, err := p.buildJob(engine.Copy, nil, &errb)
	if err != nil {
		t.Fatal(err)
	}
	want := "rhythm-roadie copy: the password in --from is visible to other users of this machine; use ROADIE_FROM_PASSWORD\n"
	if errb.String() != want {
		t.Errorf("warning %q, want %q", errb.String(), want)
	}
	if j.To.Password != "from-env" || j.From.Password != "pw1" {
		t.Errorf("passwords not filled: from %q to %q", j.From.Password, j.To.Password)
	}
	if strings.Contains(errb.String(), "pw1") {
		t.Error("the warning printed the password")
	}
}

// --no-config skips the file, --config picks one and otherwise the
// nearest .roadie/config.yaml is read; none found gives the defaults.
func TestLoadConfig(t *testing.T) {
	file := &roadiecfg.Config{URL: "https://file.test"}
	defaults := &roadiecfg.Config{URL: "https://default.test"}
	var loaded []string
	testDeps(t, func(d *deps) {
		d.defaultConfig = func() *roadiecfg.Config { return defaults }
		d.findConfig = func(string) (string, error) { return "/project/.roadie/config.yaml", nil }
		d.loadConfig = func(path string, _ func(string) (string, bool)) (*roadiecfg.Config, error) {
			loaded = append(loaded, path)
			if path == "broken.yaml" {
				return nil, core.Errorf(core.ErrConfig, 2, "broken.yaml line 3: unknown key")
			}
			return file, nil
		}
	})
	for _, c := range []struct {
		args     []string
		want     *roadiecfg.Config
		wantPath string
	}{
		{[]string{"--no-config"}, defaults, ""},
		{[]string{"--config", "other.yaml"}, file, "other.yaml"},
		{nil, file, "/project/.roadie/config.yaml"},
	} {
		cfg, path, err := mustParse(t, "copy", c.args...).loadConfig()
		if err != nil || cfg != c.want || path != c.wantPath {
			t.Errorf("%q: %v %q %v", c.args, cfg, path, err)
		}
	}
	if !slices.Equal(loaded, []string{"other.yaml", "/project/.roadie/config.yaml"}) {
		t.Errorf("loaded %q", loaded)
	}
	if _, _, err := mustParse(t, "copy", "--config", "broken.yaml").loadConfig(); err == nil {
		t.Error("a broken config should be an error")
	}
	dep.findConfig = func(string) (string, error) { return "", fs.ErrNotExist }
	if cfg, path, err := mustParse(t, "copy").loadConfig(); err != nil || cfg != defaults || path != "" {
		t.Errorf("no config file: %v %q %v", cfg, path, err)
	}
}

// An endpoint error never shows an inline DSN password, raw or decoded.
func TestEndpointErrorHidesPassword(t *testing.T) {
	testDeps(t, nil)
	pw := "s3" + "cr%40t" // "s3cr@t" URL-encoded
	raw := "mysql://shop:" + pw + "@db.shop.com/shop"
	dep.parseEndpoint = func(s string) (core.Endpoint, error) {
		return core.Endpoint{}, core.Errorf(core.ErrSSH, 3, "can't reach %s (user shop, password s3cr@t)", s)
	}
	_, err := parseEndpoint(raw, "--from", "ROADIE_FROM_PASSWORD", func(string) {})
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Code != core.ErrSSH || ce.Exit != 3 {
		t.Fatalf("lost the code: %#v", err)
	}
	if msg := err.Error(); strings.Contains(msg, "s3cr") || !strings.Contains(msg, "mysql://shop@db.shop.com/shop") {
		t.Errorf("message %q", msg)
	}
	// A plain error is scrubbed too; one without a password is untouched.
	dep.parseEndpoint = func(s string) (core.Endpoint, error) { return core.Endpoint{}, errors.New("bad endpoint " + s) }
	if _, err := parseEndpoint(raw, "--from", "", func(string) {}); strings.Contains(err.Error(), pw) {
		t.Errorf("plain error %q", err)
	}
	orig := errors.New("bad endpoint ssh://deploy@shop.com:22/x")
	dep.parseEndpoint = func(string) (core.Endpoint, error) { return core.Endpoint{}, orig }
	if _, err := parseEndpoint("ssh://deploy@shop.com:22/x", "--from", "", func(string) {}); !errors.Is(err, orig) {
		t.Errorf("error without a password changed: %v", err)
	}
}

func TestInlinePassword(t *testing.T) {
	for in, want := range map[string]string{
		"mysql://u:pw@h/db":          "pw",
		"postgres://u:p@ss/w@h:5/db": "p@ss/w",
		"mysql://u@h/db":             "",
		"ssh://deploy@shop.com:22/x": "",
		"prod.sql.gz":                "",
		"mysql://u:@h/db":            "",
	} {
		if got := inlinePassword(in); got != want {
			t.Errorf("inlinePassword(%q) = %q, want %q", in, got, want)
		}
	}
}

// A run hands the engine the config's project folder for .roadie/key
// and the basic-auth files; without a config the engine uses the
// working folder.
func TestRunProjectDir(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{nil, filepath.FromSlash("/project")},
		{[]string{"--config", filepath.FromSlash("ci/roadie.yaml")}, "ci"},
		{[]string{"--no-config"}, ""},
	} {
		var got []string
		testDeps(t, func(d *deps) {
			d.defaultConfig = roadiecfg.Default
			d.findConfig = func(string) (string, error) { return filepath.FromSlash("/project/.roadie/config.yaml"), nil }
			d.loadConfig = func(string, func(string) (string, bool)) (*roadiecfg.Config, error) { return roadiecfg.Default(), nil }
			d.run = func(_ context.Context, j engine.Job) (*core.Result, error) {
				got = append(got, j.ProjectDir)
				return planResult(), nil
			}
		})
		args := append([]string{"--from", "prod.sql", "--to", "out.sql", "--url", "https://staging.shop.test"}, c.args...)
		runCmd(t, "plan", args...)
		if !slices.Equal(got, []string{c.want}) {
			t.Errorf("%q: ProjectDir %q, want %q", c.args, got, c.want)
		}
	}
}

// A verify that couldn't read part of its input is INCOMPLETE, never
// PASS, even with no finding.
func TestVerdictIncomplete(t *testing.T) {
	r := &core.Result{Verify: &core.VerifyResult{Coverage: core.Coverage{TablesScanned: 3, RowsScanned: 10,
		Skipped: []core.Skipped{{Table: core.SkippedInput + " after wp_users)", Reason: "dump-truncated"}}}}}
	if word, _, _ := verdict(r, core.SeverityHigh, "staging.sql"); word != "INCOMPLETE" {
		t.Errorf("verdict %q", word)
	}
	r.Verify.Coverage.Skipped = nil
	if word, _, _ := verdict(r, core.SeverityHigh, "staging.sql"); word != "PASS" {
		t.Errorf("verdict %q", word)
	}
}

// A plan line Roadie can't carry out itself reads as a step by hand; a
// line without a file has no leading colon.
func TestEditsBlockManual(t *testing.T) {
	var b bytes.Buffer
	v := &view{w: &b, o: viewOpts{plain: true}}
	v.editsBlock(&core.Result{PlannedEdits: []core.PlannedEdit{
		{Step: "side-effect.mail", File: "php.ini", What: "manual: sendmail_path = rhythm-echo sendmail"},
		{Step: "side-effect.queue", What: "manual: restart the queue workers"},
		{Step: "dump", What: "dropped 2 GRANT statements"},
		{Step: "side-effect.cron", File: "wp-config.php", What: "define( 'DISABLE_WP_CRON', true );"},
	}})
	out := b.String()
	for _, want := range []string{"by hand, php.ini: sendmail_path = rhythm-echo sendmail", "by hand: restart the queue workers",
		"dropped 2 GRANT statements", "wp-config.php: define( 'DISABLE_WP_CRON', true );"} {
		if !strings.Contains(out, want) {
			t.Errorf("edits lack %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, ": dropped") || strings.Contains(out, "manual:") {
		t.Errorf("edits:\n%s", out)
	}
}
