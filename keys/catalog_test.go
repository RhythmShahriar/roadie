// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package keys

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/internal/testutil"
)

// ser writes a flat PHP serialized array; a value of "b:0" or "b:1" is
// written as a boolean.
func ser(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	fmt.Fprintf(&b, "a:%d:{", len(m))
	for _, k := range keys {
		fmt.Fprintf(&b, "s:%d:\"%s\";", len(k), k)
		if v := m[k]; v == "b:0" || v == "b:1" {
			b.WriteString(v + ";")
		} else {
			fmt.Fprintf(&b, "s:%d:\"%s\";", len(v), v)
		}
	}
	b.WriteString("}")
	return b.String()
}

// modeValue writes a gateway's live or test value as its option stores it.
func modeValue(g Gateway, env string) string {
	v := g.LiveValue
	if env == EnvTest {
		v = g.TestValue
	}
	if g.ID == "paypal" {
		return "b:" + v
	}
	if strings.HasSuffix(v, "_") {
		return v + strings.Repeat("a1", 7)
	}
	return v
}

// Every gateway reads as live in live mode, test in test mode and off
// when disabled, from its own option written as the plugin writes it.
func TestGatewaysLiveTestAndDisabled(t *testing.T) {
	ids := map[string]bool{}
	for _, g := range Gateways() {
		if ids[g.ID] {
			t.Errorf("gateway ID %s twice", g.ID)
		}
		ids[g.ID] = true
		if g.Option == "" || g.Name == "" {
			t.Errorf("%s: no option or name", g.ID)
		}
		if g.ModePath == "" && g.EnabledPath != "" && g.TestValue == "" {
			continue // the PayPal gateway option holds only its enabled flag
		}
		for _, env := range []string{EnvLive, EnvTest} {
			opt := map[string]string{}
			if g.EnabledPath != "" {
				opt[g.EnabledPath] = "yes"
				if g.ID == "paypal" {
					opt[g.EnabledPath] = "b:1"
				}
			}
			var settings map[string]string
			if g.ModePath == "" {
				settings = map[string]string{"": modeValue(g, env)}
			} else {
				opt[g.ModePath] = modeValue(g, env)
				var ok bool
				if settings, ok = Settings([]byte(ser(opt))); !ok {
					t.Fatalf("%s: fixture doesn't parse", g.ID)
				}
			}
			if !g.Enabled(settings) {
				t.Errorf("%s %s: not enabled", g.ID, env)
			}
			if got := g.Mode(settings); got != env {
				t.Errorf("%s: Mode = %s, want %s", g.ID, got, env)
			}
		}
		if g.EnabledPath != "" {
			off := map[string]string{g.EnabledPath: "no"}
			if g.ID == "paypal" {
				off[g.EnabledPath] = "0"
			}
			if g.Enabled(off) {
				t.Errorf("%s: enabled when off", g.ID)
			}
		}
		if g.ModePath != "" {
			if got := g.Mode(map[string]string{g.ModePath: "maybe"}); got != EnvUnknown {
				t.Errorf("%s: Mode(maybe) = %s", g.ID, got)
			}
		}
	}
	for _, want := range []string{"stripe", "paypal", "woopayments", "square", "braintree", "mollie", "razorpay", "klarna", "authorize-net"} {
		if !ids[want] {
			t.Errorf("no gateway %s", want)
		}
		if g, ok := GatewayFor(gatewayOption(want)); !ok || g.ID != want {
			t.Errorf("GatewayFor(%s) = %v", want, g.ID)
		}
	}
}

func gatewayOption(id string) string {
	for _, g := range Gateways() {
		if g.ID == id {
			return g.Option
		}
	}
	return ""
}

// The live secret of each gateway with a field is found by ScanValue at
// its path in the gateway's option; the test secret is not live.
func TestGatewaySecretsAreFound(t *testing.T) {
	for _, g := range Gateways() {
		if len(g.LiveSecretPaths) == 0 || g.ID == "woopayments" {
			continue
		}
		found := false
		for _, p := range g.LiveSecretPaths {
			row, path := "option_name="+g.Option, "serialized:"+p
			if o, ok := strings.CutPrefix(p, OptionPrefix); ok {
				row, path = "option_name="+o, ""
			}
			v := liveValueFor(g.ID, p)
			hits := ScanValue([]byte(v), core.Location{Table: "wp_options", Row: row, Path: path},
				map[string]string{g.ModePath: g.LiveValue})
			if len(hits) == 1 && hits[0].Env == EnvLive {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: no live secret path reads as live", g.ID)
		}
	}
}

// liveValueFor builds a live-looking value for a gateway field.
func liveValueFor(id, path string) string {
	switch id {
	case "stripe":
		if strings.Contains(path, "publishable") {
			return testutil.J("pk_", "live_", strings.Repeat("Ab3", 8))
		}
		if strings.Contains(path, "webhook") {
			return testutil.J("whsec_", strings.Repeat("Ab3", 8))
		}
		return testutil.J("sk_", "live_", strings.Repeat("Ab3", 8))
	case "mollie":
		return testutil.J("live_", strings.Repeat("Ab3", 10))
	case "razorpay":
		return testutil.J("rzp_", "live_", strings.Repeat("Ab", 7))
	case "square":
		return testutil.J("EAAA", strings.Repeat("Ab3", 20))
	}
	return strings.Repeat("Zq7", 10)
}

func TestMailers(t *testing.T) {
	ids := []string{}
	for _, m := range Mailers() {
		ids = append(ids, m.ID)
		if m.Option == "" || m.MailerPath == "" || len(m.SecretPaths) == 0 {
			t.Errorf("%s: incomplete %+v", m.ID, m)
		}
		if got, ok := MailerFor(m.Option); !ok || got.ID != m.ID {
			t.Errorf("MailerFor(%s)", m.Option)
		}
	}
	if !slices.Equal(ids, []string{"wp-mail-smtp", "post-smtp", "fluent-smtp"}) {
		t.Errorf("mailers %v", ids)
	}
	// Each mailer's SMTP password, as the plugin stores it, is a live
	// smtp key with a real host and none on loopback.
	cases := []struct {
		option, opt, path string
	}{
		{"wp_mail_smtp", `a:2:{s:4:"mail";a:1:{s:6:"mailer";s:4:"smtp";}s:4:"smtp";a:2:{s:4:"host";s:%d:"%s";s:4:"pass";s:12:"Zq7Zq7Zq7Zq7";}}`, "smtp.pass"},
		{"postman_options", `a:3:{s:14:"transport_type";s:4:"smtp";s:8:"hostname";s:%d:"%s";s:19:"basic_auth_password";s:12:"Zq7Zq7Zq7Zq7";}`, "basic_auth_password"},
		{"fluentmail-settings", `a:1:{s:11:"connections";a:1:{s:4:"ab12";a:1:{s:17:"provider_settings";a:2:{s:4:"host";s:%d:"%s";s:8:"password";s:12:"Zq7Zq7Zq7Zq7";}}}}`, "connections.ab12.provider_settings.password"},
	}
	for _, c := range cases {
		m, _ := MailerFor(c.option)
		if !slices.ContainsFunc(m.SecretPaths, func(p string) bool { return PathMatch(p, c.path) }) {
			t.Errorf("%s: %s is not a secret path", c.option, c.path)
		}
		for host, want := range map[string]string{"smtp.mailprovider.net": EnvLive, "127.0.0.1": EnvTest} {
			settings, ok := Settings(fmt.Appendf(nil, c.opt, len(host), host))
			if !ok {
				t.Fatalf("%s: fixture doesn't parse", c.option)
			}
			hits := ScanValue([]byte(settings[c.path]), core.Location{Row: "option_name=" + c.option, Path: "serialized:" + c.path},
				Siblings(settings, c.path))
			if len(hits) != 1 || hits[0].Provider != "smtp" || hits[0].Env != want {
				t.Errorf("%s host %s: %+v", c.option, host, hits)
			}
		}
	}
}

func TestPathMatch(t *testing.T) {
	tests := []struct {
		pattern, path string
		want          bool
	}{
		{"connections.*.provider_settings.password", "connections.ab12.provider_settings.password", true},
		{"connections.*.provider_settings.password", "connections[0].provider_settings.password", true},
		{"connections.*.provider_settings.password", "connections.ab12.password", false},
		{"smtp.pass", "smtp.pass", true},
		{"smtp.pass", "smtp.password", false},
		{`a\.b`, `a\.b`, true},
		{`a\.b`, "a.b", false},
		{"*", "x", true},
	}
	for _, tt := range tests {
		if got := PathMatch(tt.pattern, tt.path); got != tt.want {
			t.Errorf("PathMatch(%q, %q) = %v", tt.pattern, tt.path, got)
		}
	}
}

func TestReplace(t *testing.T) {
	liveSK := testutil.J("sk_", "live_", strings.Repeat("Ab3", 8))
	testSK := testutil.J("sk_", "test_", strings.Repeat("Cd4", 8))
	livePK := testutil.J("pk_", "live_", strings.Repeat("Ab3", 8))
	testPK := testutil.J("pk_", "test_", strings.Repeat("Cd4", 8))
	liveRzp := testutil.J("rzp_", "live_", strings.Repeat("Ab", 7))
	testRzp := testutil.J("rzp_", "test_", strings.Repeat("Cd", 7))
	hit := func(v, path string) Hit {
		hs := ScanValue([]byte(v), core.Location{Row: "option_name=woocommerce-ppcp-settings,option_name=woocommerce_razorpay_settings", Path: path}, nil)
		if len(hs) != 1 {
			t.Fatalf("no single hit for %s at %s: %+v", core.MaskKey(v), path, hs)
		}
		return hs[0]
	}
	paypalLive := hit(strings.Repeat("Pp9", 20), "serialized:client_secret_production")
	tests := []struct {
		name       string
		h          Hit
		value      string
		mode       string
		testKeys   map[string]string
		want, act  string
		wantErrCod string
	}{
		{"swap stripe", hit(liveSK, ""), liveSK, "swap", map[string]string{"stripe": testSK}, testSK, "swapped", ""},
		{"swap publishable", hit(livePK, ""), livePK, "swap", map[string]string{"stripe_publishable": testPK}, testPK, "swapped", ""},
		{"hyphen name", hit(livePK, ""), livePK, "swap", map[string]string{"stripe-publishable": testPK}, testPK, "swapped", ""},
		{"no test key blanks", hit(liveSK, ""), liveSK, "swap", nil, "", "blanked", ""},
		{"live test key blanks", hit(liveSK, ""), liveSK, "swap", map[string]string{"stripe": liveSK}, "", "blanked", ""},
		{"blank mode", hit(liveSK, ""), liveSK, "blank", map[string]string{"stripe": testSK}, "", "blanked", ""},
		{"default mode swaps", hit(liveSK, ""), liveSK, "", map[string]string{"stripe": testSK}, testSK, "swapped", ""},
		{"test key kept", hit(testSK, ""), testSK, "swap", nil, testSK, "kept", ""},
		{"key in text", hit("key="+liveSK+";", ""), "key=" + liveSK + ";", "blank", nil, "key=;", "blanked", ""},
		{"key in text swapped", hit("key="+liveSK+";", ""), "key=" + liveSK + ";", "swap", map[string]string{"stripe": testSK}, "key=" + testSK + ";", "swapped", ""},
		{"paypal by field", paypalLive, strings.Repeat("Pp9", 20), "swap", map[string]string{"paypal_client_secret": "SandboxSecret123"}, "SandboxSecret123", "swapped", ""},
		{"paypal live given", paypalLive, strings.Repeat("Pp9", 20), "swap", map[string]string{"paypal_client_secret": liveSK}, "", "blanked", ""},
		{"razorpay id", hit(liveRzp, "serialized:key_id"), liveRzp, "swap", map[string]string{"razorpay_key_id": testRzp}, testRzp, "swapped", ""},
		{"razorpay id wrong", hit(liveRzp, "serialized:key_id"), liveRzp, "swap", map[string]string{"razorpay_key_id": liveRzp}, "", "blanked", ""},
		{"unknown env kept", Hit{Provider: "square", Env: EnvUnknown}, "EAAAx", "swap", nil, "EAAAx", "kept", ""},
		{"generic blanked", Hit{ID: core.FindingKeyGeneric, Provider: ProviderGeneric, Env: EnvLive}, "s3cr3tvalue", "swap", nil, "", "blanked", ""},
		{"bad mode", hit(liveSK, ""), liveSK, "rotate", nil, "", "", core.ErrConfig},
	}
	for _, tt := range tests {
		out, act, err := Replace(tt.h, []byte(tt.value), tt.mode, tt.testKeys)
		if tt.wantErrCod != "" {
			var ce *core.Error
			if !errors.As(err, &ce) || ce.Code != tt.wantErrCod || ce.Exit != 2 {
				t.Errorf("%s: err %v", tt.name, err)
			}
			continue
		}
		if err != nil || string(out) != tt.want || act != tt.act {
			t.Errorf("%s: got %s %s %v, want %s %s", tt.name, core.MaskKey(string(out)), act, err, core.MaskKey(tt.want), tt.act)
		}
	}
}

func TestValidateTestKey(t *testing.T) {
	live := testutil.J("sk_", "live_", strings.Repeat("Ab3", 8))
	err := ValidateTestKey("stripe", live)
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Code != core.ErrTestKeyIsLive || ce.Exit != 2 {
		t.Fatalf("live key: %v", err)
	}
	want := "test-key-is-live: keys.test_keys.stripe reads a live key (sk_l•••••). Give a test key (sk_test_...) or remove the entry to blank it."
	if ce.Msg != want {
		t.Errorf("message\n got %s\nwant %s", ce.Msg, want)
	}
	if strings.Contains(ce.Msg, live[8:]) {
		t.Error("message shows the key")
	}
	for p, v := range map[string]string{
		"stripe":               testutil.J("sk_", "test_", strings.Repeat("Ab3", 8)),
		"stripe-publishable":   testutil.J("pk_", "test_", strings.Repeat("Ab3", 8)),
		"paypal_client_secret": "SandboxSecret123",
		"mollie":               testutil.J("test_", strings.Repeat("Ab3", 10)),
		"square":               "",
	} {
		if err := ValidateTestKey(p, v); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
	for p, v := range map[string]string{
		"razorpay-key-id":      testutil.J("rzp_", "live_", strings.Repeat("Ab", 7)),
		"paypal_client_secret": live,
		"mollie":               testutil.J("live_", strings.Repeat("Ab3", 10)),
	} {
		if err := ValidateTestKey(p, v); !errors.Is(err, &core.Error{Code: core.ErrTestKeyIsLive}) {
			t.Errorf("%s: %v, want test-key-is-live", p, err)
		}
	}
	if len(TestKeyNames()) != 8 {
		t.Errorf("test key names %v", TestKeyNames())
	}
}
