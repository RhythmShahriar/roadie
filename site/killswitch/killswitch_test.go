// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package killswitch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/RhythmShahriar/echo/connectors/wordpress"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/internal/testutil"
	"github.com/RhythmShahriar/roadie/roadiecfg"
)

// wantSteps is the WordPress step order.
var wantSteps = []string{
	core.FindingConfigEnvironment, core.FindingSideEffectCron, core.FindingSideEffectSubscriptions,
	core.FindingKeyGeneric, core.FindingConfigSalts, core.FindingSideEffectMail,
	core.FindingSideEffectActionScheduler, core.FindingSideEffectJetpack, core.FindingSideEffectWebhooks,
	core.FindingSideEffectGateways, core.FindingSideEffectAutomateWoo,
}

func TestStepOrderAndIDs(t *testing.T) {
	var got []string
	for _, st := range Steps(core.StackWordPress, fixtureOptions()) {
		got = append(got, st.ID())
		if !core.KnownID(st.ID()) {
			t.Errorf("unknown step ID %s", st.ID())
		}
		if st.Phase() != "files" {
			t.Errorf("%s: phase %s", st.ID(), st.Phase())
		}
	}
	if !slices.Equal(got, wantSteps) {
		t.Fatalf("steps %v\nwant %v", got, wantSteps)
	}
	if DataRules(core.StackSQL, Options{}) != nil {
		t.Fatal("data rules for sql")
	}
	for _, r := range DataRules(core.StackWordPress, fixtureOptions()) {
		if r.Profile != Profile || !strings.HasPrefix(r.ID, Profile+".") || !strings.HasPrefix(r.Table, "{prefix}") {
			t.Errorf("rule %+v", r)
		}
	}
}

// check runs every step's Check and returns status and finding IDs by step.
func check(t *testing.T, s core.Site, o Options) (map[string]string, map[string][]string) {
	t.Helper()
	status, ids := map[string]string{}, map[string][]string{}
	for _, st := range Steps(core.StackWordPress, o) {
		se, fs, err := st.Check(context.Background(), s)
		if err != nil {
			t.Fatalf("%s: %v", st.ID(), err)
		}
		if se.ID != st.ID() {
			t.Errorf("%s: side effect ID %s", st.ID(), se.ID)
		}
		status[st.ID()] = se.Status
		for _, f := range fs {
			if !core.KnownID(f.ID) {
				t.Errorf("%s: unknown finding ID %s", st.ID(), f.ID)
			}
			if f.Title == "" || len(f.Locations) == 0 {
				t.Errorf("%s: finding %+v", st.ID(), f)
			}
			ids[st.ID()] = append(ids[st.ID()], f.ID)
		}
	}
	return status, ids
}

// TestChecksPositiveNegative: every step has a fixture where the switch
// is off (production: a finding or an "on" status) and one where it is on
// (the same site after prepare: no finding).
func TestChecksPositiveNegative(t *testing.T) {
	o := fixtureOptions()
	prod := fixtureSite(prodFiles(), prodDB())
	status, ids := check(t, prod, o)
	wantFinding := map[string]string{
		core.FindingConfigEnvironment:         core.FindingConfigEnvironment,
		core.FindingSideEffectCron:            core.FindingSideEffectCron,
		core.FindingSideEffectSubscriptions:   core.FindingSideEffectSubscriptions,
		core.FindingKeyGeneric:                core.KeyLiveID("stripe"),
		core.FindingSideEffectMail:            core.FindingSideEffectMail,
		core.FindingSideEffectActionScheduler: core.FindingSideEffectActionScheduler,
		core.FindingSideEffectJetpack:         core.FindingSideEffectJetpack,
		core.FindingSideEffectWebhooks:        core.FindingSideEffectWebhooks,
		core.FindingSideEffectGateways:        core.FindingSideEffectGateways,
		core.FindingSideEffectAutomateWoo:     core.FindingSideEffectAutomateWoo,
	}
	for _, id := range wantSteps {
		if status[id] != "on" {
			t.Errorf("production %s: status %q, want on", id, status[id])
		}
		if want, ok := wantFinding[id]; ok && !slices.Contains(ids[id], want) {
			t.Errorf("production %s: findings %v, want %s", id, ids[id], want)
		}
	}
	if len(ids[core.FindingConfigSalts]) != 0 {
		t.Error("config.salts reports status only")
	}
	// Four gateways are live: Stripe, PayPal, WooPayments and the unknown one.
	if n := len(ids[core.FindingSideEffectGateways]); n != 4 {
		t.Errorf("gateway findings %d, want 4", n)
	}

	d := prodDB()
	staging := fixtureSite(prodFiles(), d)
	applyAll(t, staging, d, o)
	status, ids = check(t, staging, o)
	for _, id := range wantSteps {
		if status[id] != "off" || len(ids[id]) != 0 {
			t.Errorf("staging %s: status %q findings %v, want off and none", id, status[id], ids[id])
		}
	}
}

func TestCheckNotPresent(t *testing.T) {
	o := fixtureOptions()
	s := fixtureSite(map[string]string{"wp-config.php": prodConfig()}, db{"wp_options": {{"option_name": "siteurl", "option_value": "https://shop.com"}}})
	status, _ := check(t, s, o)
	for _, id := range []string{core.FindingSideEffectSubscriptions, core.FindingSideEffectJetpack, core.FindingSideEffectWebhooks, core.FindingSideEffectGateways} {
		if status[id] != "not-present" {
			t.Errorf("%s: %q, want not-present", id, status[id])
		}
	}
	// No Action Scheduler rows: no finding, even unpaused.
	_, ids := check(t, s, o)
	if len(ids[core.FindingSideEffectActionScheduler]) != 0 || len(ids[core.FindingSideEffectAutomateWoo]) != 0 {
		t.Errorf("findings without rows: %v", ids)
	}
}

// TestEachWorksAlone removes the Echo connector after prepare: the other
// switches stay off and mail falls back to the mu-plugin's block.
func TestEachWorksAlone(t *testing.T) {
	o := fixtureOptions()
	d := prodDB()
	s := fixtureSite(prodFiles(), d)
	applyAll(t, s, d, o)
	if err := s.Remove(context.Background(), EchoPluginPath); err != nil {
		t.Fatal(err)
	}
	status, ids := check(t, s, o)
	for _, id := range wantSteps {
		if status[id] != "off" || len(ids[id]) != 0 {
			t.Errorf("%s without the connector: %q %v", id, status[id], ids[id])
		}
	}
	// Without the mu-plugin too, mail is on; gateways, webhooks and cron stay off.
	if err := s.Remove(context.Background(), MuPluginPath); err != nil {
		t.Fatal(err)
	}
	status, _ = check(t, s, o)
	if status[core.FindingSideEffectMail] != "on" {
		t.Error("mail off with neither connector nor mu-plugin")
	}
	for _, id := range []string{core.FindingSideEffectGateways, core.FindingSideEffectWebhooks, core.FindingSideEffectCron, core.FindingConfigEnvironment, core.FindingSideEffectAutomateWoo} {
		if status[id] != "off" {
			t.Errorf("%s depends on the mu-plugin: %q", id, status[id])
		}
	}
}

// TestGateways (security K4): Stripe, PayPal and WooPayments stay enabled
// in test mode; the unknown gateway is disabled.
func TestGateways(t *testing.T) {
	o := fixtureOptions()
	d := prodDB()
	applyRules(t, d, DataRules(core.StackWordPress, o))
	got := map[string]map[string]string{}
	for _, r := range d["wp_options"] {
		if strings.Contains(r["option_name"], "settings") {
			got[r["option_name"]] = flat(t, r["option_value"])
		}
	}
	for opt, mode := range map[string]string{"woocommerce_stripe_settings": "testmode", "woocommerce-ppcp-settings": "sandbox_on", "woocommerce_woocommerce_payments_settings": "test_mode"} {
		if got[opt]["enabled"] != "yes" || got[opt][mode] != "yes" {
			t.Errorf("%s: %v", opt, got[opt])
		}
	}
	if got["woocommerce_stripe_settings"]["secret_key"] != "" || got["woocommerce-ppcp-settings"]["client_secret_production"] != "" {
		t.Error("live secrets kept")
	}
	if got["woocommerce_fancypay_settings"]["enabled"] != "no" {
		t.Errorf("unknown gateway: %v", got["woocommerce_fancypay_settings"])
	}
	// Emails and other WooCommerce settings are not gateways.
	if got["woocommerce_new_order_settings"]["enabled"] != "yes" {
		t.Error("an email setting was disabled")
	}
	ids, err := GatewayIDs([]byte(prodDB()["wp_options"][2]["option_value"]))
	if err != nil || !slices.Equal(ids, []string{"stripe", "ppcp-gateway", "woocommerce_payments", "fancypay"}) {
		t.Fatalf("GatewayIDs %v %v", ids, err)
	}
}

func TestWPConfigGolden(t *testing.T) {
	o := fixtureOptions()
	d := prodDB()
	s := fixtureSite(prodFiles(), d)
	applyAll(t, s, d, o)
	files := s.Files()
	cfg := files["wp-config.php"]
	if !bytes.Contains(cfg, []byte("\r\n")) || bytes.Contains(bytes.ReplaceAll(cfg, []byte("\r\n"), nil), []byte("\n")) {
		t.Fatal("line endings changed")
	}
	if bytes.Contains(cfg, []byte(liveStripe)) {
		t.Fatal("the live key is still in wp-config.php")
	}
	testutil.Golden(t, "wp-config.php", bytes.ReplaceAll(cfg, []byte("\r\n"), []byte("\n")))
	if _, err := exec.LookPath("php"); err == nil || os.Getenv("ROADIE_REQUIRE_PHP") == "1" {
		php := testutil.RequirePHP(t)
		p := writeTemp(t, "wp-config.php", cfg)
		if out, err := exec.Command(php, "-l", p).CombinedOutput(); err != nil { // #nosec G204 -- the test's own php
			t.Fatalf("php -l on the edited wp-config.php: %v\n%s", err, out)
		}
	}
	testutil.Golden(t, "settings.php", files["wp-content/"+wordpress.SettingsPath])
	if !bytes.Equal(files[EchoPluginPath], wordpress.Plugin()) {
		t.Fatal("connector differs from Echo's")
	}
	if !bytes.Equal(files[MuPluginPath], MuPlugin(o)) {
		t.Fatal("mu-plugin differs")
	}
	// A second prepare changes nothing.
	before := s.Files()
	o2 := fixtureOptions()
	o2.Salt = func() string { return "" } // the salts would change; skip that step
	for _, st := range Steps(core.StackWordPress, o2) {
		if st.ID() == core.FindingConfigSalts {
			continue
		}
		if err := st.Apply(context.Background(), s); err != nil {
			t.Fatal(err)
		}
	}
	for k, v := range s.Files() {
		if !bytes.Equal(v, before[k]) {
			t.Errorf("%s changed on a second run", k)
		}
	}
}

func TestPlanGolden(t *testing.T) {
	o := fixtureOptions()
	s := fixtureSite(prodFiles(), prodDB())
	var b strings.Builder
	for _, st := range Steps(core.StackWordPress, o) {
		edits, err := st.Plan(context.Background(), s)
		if err != nil {
			t.Fatal(err)
		}
		if len(edits) == 0 {
			t.Errorf("%s plans nothing", st.ID())
		}
		for _, e := range edits {
			if e.Step != st.ID() {
				t.Errorf("edit step %s in %s", e.Step, st.ID())
			}
			fmt.Fprintf(&b, "%s | %s | %s\n", e.Step, e.File, e.What)
		}
	}
	if strings.Contains(b.String(), liveStripe) {
		t.Fatal("the plan shows a live key")
	}
	if !strings.Contains(b.String(), "side-effect.mail | php.ini | "+ManualPrefix+SendmailLine) {
		t.Fatal("no manual sendmail_path line")
	}
	testutil.Golden(t, "plan.txt", []byte(b.String()))
	if len(s.Writes()) != 0 {
		t.Fatalf("plan wrote %v", s.Writes())
	}
}

func TestConfigKeep(t *testing.T) {
	cfg := &roadiecfg.Config{}
	cfg.SideEffects.Cron, cfg.SideEffects.Webhooks, cfg.SideEffects.Gateways = "keep", "keep", "keep"
	cfg.SideEffects.ActionScheduler = "cancel"
	cfg.SideEffects.Echo.Install = true
	o := fixtureOptions()
	o.Cfg = cfg
	rules := DataRules(core.StackWordPress, o)
	for _, r := range rules {
		if strings.Contains(r.ID, "wc_webhooks") || strings.Contains(r.ID, "woocommerce_stripe") {
			t.Errorf("kept side effect has rule %s", r.ID)
		}
	}
	if !slices.ContainsFunc(rules, func(r core.Rule) bool { return r.Value == "canceled" && r.Where["status"] == "pending" }) {
		t.Error("no cancel rule")
	}
	s := fixtureSite(prodFiles(), prodDB())
	for _, st := range Steps(core.StackWordPress, o) {
		if st.ID() == core.FindingSideEffectCron {
			if err := st.Apply(context.Background(), s); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(s.Writes()) != 0 {
		t.Fatal("cron: keep still edited wp-config.php")
	}
}

// evalSite is an ssh-like site: the connector goes in through wp eval-file.
type evalSite struct {
	*testutil.Site
	scripts [][]byte
}

func (e *evalSite) EvalFile(_ context.Context, b []byte) error {
	e.scripts = append(e.scripts, b)
	return nil
}

func TestMailOverSSH(t *testing.T) {
	o := fixtureOptions()
	es := &evalSite{Site: fixtureSite(prodFiles(), prodDB())}
	for _, st := range Steps(core.StackWordPress, o) {
		if st.ID() == core.FindingSideEffectMail {
			if err := st.Apply(context.Background(), es); err != nil {
				t.Fatal(err)
			}
		}
	}
	want, _ := wordpress.Script(wordpress.Settings{Mode: "seal", URL: o.EchoURL})
	if len(es.scripts) != 1 || string(es.scripts[0]) != want {
		t.Fatalf("scripts %d", len(es.scripts))
	}
	if len(es.Writes()) != 0 {
		t.Fatalf("wrote files over ssh: %v", es.Writes())
	}
}

func TestForeignFilesAndWriteErrors(t *testing.T) {
	ctx := context.Background()
	o := fixtureOptions()
	stepByID := func(id string) core.Step {
		for _, st := range Steps(core.StackWordPress, o) {
			if st.ID() == id {
				return st
			}
		}
		t.Fatalf("no step %s", id)
		return nil
	}
	files := prodFiles()
	files[EchoPluginPath] = "<?php // someone else's"
	files[MuPluginPath] = "<?php // someone else's"
	s := fixtureSite(files, prodDB())
	err := stepByID(core.FindingSideEffectMail).Apply(ctx, s)
	want := "side-effect.mail: " + EchoPluginPath + " exists and wasn't written by Echo, so mail from this copy isn't sealed. Remove it, then: rhythm-roadie prepare /srv/staging --only side-effects."
	if !errors.Is(err, &core.Error{Code: core.ErrKillSwitch}) || err.Error() != want {
		t.Fatalf("%v\nwant %s", err, want)
	}
	if err := stepByID(core.FindingSideEffectActionScheduler).Apply(ctx, s); !errors.Is(err, &core.Error{Code: core.ErrKillSwitch}) {
		t.Fatalf("foreign mu-plugin: %v", err)
	}
	if len(s.Writes()) != 0 {
		t.Fatal("a foreign file was replaced")
	}
	// A write that fails is kill-switch with the PRD 17 shape.
	ro := fixtureSite(prodFiles(), prodDB())
	ro.RO = true
	err = stepByID(core.FindingSideEffectMail).Apply(ctx, ro)
	want = "side-effect.mail: couldn't write wp-content/mu-plugins/rhythm-echo.php (" + core.ErrReadOnly.Error() + "), so mail from this copy isn't sealed. Fix it, then: rhythm-roadie prepare /srv/staging --only side-effects."
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Code != core.ErrKillSwitch || ce.Exit != 1 || ce.Msg != want {
		t.Fatalf("%v\nwant %s", err, want)
	}
	// A changed connector is side-effect.connector-changed.
	d := prodDB()
	ok := fixtureSite(prodFiles(), d)
	applyAll(t, ok, d, o)
	_ = ok.WriteFile(ctx, EchoPluginPath, append(wordpress.Plugin(), []byte("\n// edited")...), 0o644)
	_, fs, _ := stepByID(core.FindingSideEffectMail).Check(ctx, ok)
	if len(fs) != 1 || fs[0].ID != core.FindingSideEffectConnectorChange || fs[0].Severity != core.SeverityHigh {
		t.Fatalf("changed connector: %+v", fs)
	}
}

func TestSaltsNeedValuesLane(t *testing.T) {
	o := fixtureOptions()
	o.Salt = nil
	s := fixtureSite(prodFiles(), prodDB())
	for _, st := range Steps(core.StackWordPress, o) {
		if st.ID() != core.FindingConfigSalts {
			continue
		}
		err := st.Apply(context.Background(), s)
		if errors.Is(err, core.ErrNotBuilt) {
			t.Skip("needs the values lane")
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestEnvironmentChecks(t *testing.T) {
	ctx := context.Background()
	st := environmentStep()
	cases := map[string]struct {
		cfg    string
		status string
	}{
		"staging":        {"<?php define( 'WP_ENVIRONMENT_TYPE', 'staging' ); require_once ABSPATH . 'wp-settings.php';", "off"},
		"local":          {"<?php define( 'WP_ENVIRONMENT_TYPE', 'local' );", "off"},
		"production":     {"<?php define( 'WP_ENVIRONMENT_TYPE', 'production' );", "on"},
		"after settings": {"<?php require_once ABSPATH . 'wp-settings.php'; define( 'WP_ENVIRONMENT_TYPE', 'staging' );", "on"},
		"getenv":         {"<?php define( 'WP_ENVIRONMENT_TYPE', getenv( 'WP_ENV' ) );", "on"},
		"unset":          {"<?php", "on"},
	}
	for name, c := range cases {
		s := testutil.FakeSite(map[string][]byte{"wp-config.php": []byte(c.cfg)})
		se, fs, _ := st.Check(ctx, s)
		if se.Status != c.status || (c.status == "on") != (len(fs) == 1) {
			t.Errorf("%s: %q %v", name, se.Status, fs)
		}
	}
	if se, _, _ := st.Check(ctx, testutil.FakeSite(nil)); se.Status != "unknown" {
		t.Errorf("no wp-config: %q", se.Status)
	}
}

// TestGatewaysWithoutOrder: a store that never saved its gateway order
// has no woocommerce_gateway_order. The unknown gateway is still found by
// its settings option, checked and disabled; emails and shipping methods
// with an enabled flag are not gateways.
func TestGatewaysWithoutOrder(t *testing.T) {
	ser := func(m map[string]string) string { return phpSer(m) }
	noOrder := func() db {
		d := prodDB()
		var rows []map[string]string
		for _, r := range d["wp_options"] {
			if r["option_name"] != "woocommerce_gateway_order" {
				rows = append(rows, r)
			}
		}
		add := func(name string, v map[string]string) {
			rows = append(rows, map[string]string{"option_id": strconv.Itoa(100 + len(rows)), "option_name": name, "option_value": ser(v), "autoload": "yes"})
		}
		add("woocommerce_customer_processing_order_settings", map[string]string{"enabled": "yes", "subject": "Thanks", "heading": "Order"})
		add("woocommerce_failed_order_settings", map[string]string{"enabled": "yes", "recipient": "shop@example.org", "email_type": "html"})
		add("woocommerce_flat_rate_settings", map[string]string{"enabled": "yes", "title": "Flat rate"})
		add("woocommerce_flat_rate_3_settings", map[string]string{"title": "Flat rate", "cost": "5"})
		add("woocommerce_analytics_settings", map[string]string{"enabled": "yes"})
		d["wp_options"] = rows
		return d
	}
	opts := map[string][]byte{}
	for _, r := range noOrder()["wp_options"] {
		if GatewayOptionsMatch(r["option_name"]) {
			opts[r["option_name"]] = []byte(r["option_value"])
		}
	}
	ids := SourceGatewayIDs(opts)
	if !slices.Equal(ids, []string{"fancypay", "stripe", "woocommerce_payments"}) {
		t.Fatalf("SourceGatewayIDs %v", ids)
	}

	o := fixtureOptions()
	o.GatewayIDs = ids
	status, found := check(t, fixtureSite(prodFiles(), noOrder()), o)
	if status[core.FindingSideEffectGateways] != "on" || len(found[core.FindingSideEffectGateways]) != 4 {
		t.Fatalf("production: %q %v, want on with 4 findings", status[core.FindingSideEffectGateways], found[core.FindingSideEffectGateways])
	}

	d := noOrder()
	staging := fixtureSite(prodFiles(), d)
	applyAll(t, staging, d, o)
	status, found = check(t, staging, o)
	if status[core.FindingSideEffectGateways] != "off" || len(found[core.FindingSideEffectGateways]) != 0 {
		t.Errorf("staging: %q %v, want off", status[core.FindingSideEffectGateways], found[core.FindingSideEffectGateways])
	}
	for _, r := range d["wp_options"] {
		switch r["option_name"] {
		case "woocommerce_fancypay_settings":
			if v := flat(t, r["option_value"]); v["enabled"] != "no" {
				t.Errorf("unknown gateway left enabled: %v", v)
			}
		case "woocommerce_customer_processing_order_settings", "woocommerce_failed_order_settings", "woocommerce_flat_rate_settings", "woocommerce_analytics_settings":
			if flat(t, r["option_value"])["enabled"] != "yes" {
				t.Errorf("%s is not a gateway but was disabled", r["option_name"])
			}
		}
	}

	// Without the IDs from the source the check still sees the unknown
	// gateway, so it never reports a live gateway as off.
	o.GatewayIDs = nil
	d = noOrder()
	staging = fixtureSite(prodFiles(), d)
	applyAll(t, staging, d, o)
	status, found = check(t, staging, o)
	if status[core.FindingSideEffectGateways] != "on" || len(found[core.FindingSideEffectGateways]) != 1 {
		t.Errorf("no GatewayIDs: %q %v, want on with the unknown gateway", status[core.FindingSideEffectGateways], found[core.FindingSideEffectGateways])
	}
}

func TestGatewayShaped(t *testing.T) {
	for _, c := range []struct {
		id   string
		v    map[string]string
		want bool
	}{
		{"fancypay", map[string]string{"enabled": "yes", "mode": "live"}, true},
		{"bankpay", map[string]string{"enabled": "no", "title": "Bank", "description": "Pay"}, true},
		{"acme", map[string]string{"enabled": "yes", "api_login": "x"}, true},
		{"acme", map[string]string{"enabled": "yes", "merchant_id": "x"}, true},
		{"acme", map[string]string{"title": "Acme"}, false},
		{"acme", map[string]string{"enabled": "yes"}, false},
		{"new_order", map[string]string{"enabled": "yes", "subject": "x"}, false},
		{"customer_note", map[string]string{"enabled": "yes", "title": "x"}, false},
		{"free_shipping", map[string]string{"enabled": "yes", "title": "Free"}, false},
		{"flat_rate_12", map[string]string{"enabled": "yes", "title": "Flat"}, false},
	} {
		if got := gatewayShaped(c.id, []byte(phpSer(c.v))); got != c.want {
			t.Errorf("gatewayShaped(%s, %v) = %v, want %v", c.id, c.v, got, c.want)
		}
	}
	if gatewayShaped("acme", []byte("not serialized")) {
		t.Error("a value that isn't serialized PHP is not a gateway")
	}
}
