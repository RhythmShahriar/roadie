// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package keys

import (
	"bytes"
	"slices"
	"strings"

	"github.com/RhythmShahriar/rhythm/kit/cli"

	"github.com/RhythmShahriar/roadie/core"
)

// OptionPrefix starts a secret path that names a whole option instead of
// a path inside the gateway's own option, for plugins such as Mollie
// that keep each setting in an option of its own.
const OptionPrefix = "option:"

// gateways is the catalog of PRD 13.1. Each entry names the plugin
// source its option and field names were read from.
var gateways = []Gateway{
	// WooCommerce Stripe Gateway: includes/admin/stripe-settings.php
	// https://github.com/woocommerce/woocommerce-gateway-stripe
	{ID: "stripe", Name: "Stripe", Option: "woocommerce_stripe_settings",
		EnabledPath: "enabled", ModePath: "testmode", TestValue: "yes", LiveValue: "no",
		LiveSecretPaths: []string{"secret_key", "publishable_key", "webhook_secret"},
		TestSecretPaths: []string{"test_secret_key", "test_publishable_key", "test_webhook_secret"}},
	// WooCommerce PayPal Payments: modules/ppcp-wc-gateway (Settings) and
	// modules/ppcp-onboarding; sandbox_on is stored as a boolean.
	// https://github.com/woocommerce/woocommerce-paypal-payments
	{ID: "paypal", Name: "PayPal Payments", Option: "woocommerce-ppcp-settings",
		EnabledPath: "enabled", ModePath: "sandbox_on", TestValue: "1", LiveValue: "0",
		LiveSecretPaths: []string{"client_id_production", "client_secret_production"},
		TestSecretPaths: []string{"client_id_sandbox", "client_secret_sandbox"}},
	// The PayPal gateway's own WooCommerce settings hold its enabled flag.
	{ID: "paypal-gateway", Name: "PayPal Payments (gateway)", Option: "woocommerce_ppcp-gateway_settings",
		EnabledPath: "enabled"},
	// WooPayments: includes/class-wc-payment-gateway-wcpay.php (test_mode);
	// the account itself is in wcpay_account_data.
	// https://github.com/Automattic/woocommerce-payments
	{ID: "woopayments", Name: "WooPayments", Option: "woocommerce_woocommerce_payments_settings",
		EnabledPath: "enabled", ModePath: "test_mode", TestValue: "yes", LiveValue: "no"},
	// Square for WooCommerce: includes/Settings.php (enable_sandbox,
	// sandbox_token); production tokens come from the Square connection.
	// Square keeps its on switch in another option
	// (woocommerce_square_credit_card_settings) and the scan reads one
	// option at a time, so there is no EnabledPath: sandbox off flags live
	// mode even when the gateway is off, which errs on the safe side.
	// https://github.com/woocommerce/woocommerce-square
	{ID: "square", Name: "Square", Option: "wc_square_settings",
		ModePath: "enable_sandbox", TestValue: "yes", LiveValue: "no",
		LiveSecretPaths: []string{"production_token", "production_application_id"},
		TestSecretPaths: []string{"sandbox_token", "sandbox_application_id"}},
	// Braintree for WooCommerce: includes/class-wc-braintree-api-gateway
	// settings (environment, sandbox_* fields).
	// https://github.com/woocommerce/woocommerce-gateway-paypal-powered-by-braintree
	{ID: "braintree", Name: "Braintree", Option: "woocommerce_braintree_credit_card_settings",
		EnabledPath: "enabled", ModePath: "environment", TestValue: "sandbox", LiveValue: "production",
		LiveSecretPaths: []string{"public_key", "private_key"},
		TestSecretPaths: []string{"sandbox_public_key", "sandbox_private_key"}},
	// Mollie Payments for WooCommerce keeps each setting in its own
	// option: src/Settings/Settings.php.
	// https://github.com/mollie/WooCommerce
	{ID: "mollie", Name: "Mollie", Option: "mollie-payments-for-woocommerce_test_mode_enabled",
		TestValue: "yes", LiveValue: "no",
		LiveSecretPaths: []string{OptionPrefix + "mollie-payments-for-woocommerce_live_api_key"},
		TestSecretPaths: []string{OptionPrefix + "mollie-payments-for-woocommerce_test_api_key"}},
	// Razorpay for WooCommerce: woo-razorpay.php (key_id, key_secret); it
	// has no mode switch, the key ID's prefix is the mode.
	// https://github.com/razorpay/razorpay-woocommerce
	{ID: "razorpay", Name: "Razorpay", Option: "woocommerce_razorpay_settings",
		EnabledPath: "enabled", ModePath: "key_id", TestValue: "rzp_test_", LiveValue: "rzp_live_",
		LiveSecretPaths: []string{"key_id", "key_secret"}},
	// Klarna Payments for WooCommerce: classes/class-kp-form-fields.php.
	// https://github.com/krokedil/klarna-payments-for-woocommerce
	{ID: "klarna", Name: "Klarna Payments", Option: "woocommerce_klarna_payments_settings",
		EnabledPath: "enabled", ModePath: "testmode", TestValue: "yes", LiveValue: "no",
		LiveSecretPaths: []string{"merchant_id_eu", "shared_secret_eu", "merchant_id_us", "shared_secret_us"},
		TestSecretPaths: []string{"test_merchant_id_eu", "test_shared_secret_eu", "test_merchant_id_us", "test_shared_secret_us"}},
	// Authorize.Net for WooCommerce (SkyVerge framework): environment and
	// test_ prefixed credentials.
	// https://woocommerce.com/document/authorize-net-cim/
	{ID: "authorize-net", Name: "Authorize.net", Option: "woocommerce_authorize_net_cim_credit_card_settings",
		EnabledPath: "enabled", ModePath: "environment", TestValue: "test", LiveValue: "production",
		LiveSecretPaths: []string{"api_login_id", "api_transaction_key", "api_signature_key"},
		TestSecretPaths: []string{"test_api_login_id", "test_api_transaction_key", "test_api_signature_key"}},
}

// mailers is the catalog of SMTP and mail API plugins PRD 13.1 switches
// to the PHP mailer.
var mailers = []Mailer{
	// WP Mail SMTP: src/Options.php lists every mailer's fields.
	// https://github.com/awesomemotive/WP-Mail-SMTP
	{ID: "wp-mail-smtp", Option: "wp_mail_smtp", MailerPath: "mail.mailer", DefaultMailer: "mail",
		SecretPaths: []string{"smtp.pass", "sendgrid.api_key", "mailgun.api_key", "sendinblue.api_key",
			"postmark.api_token", "sparkpost.api_key", "amazonses.client_secret", "mailjet.api_key",
			"mailjet.secret_key", "smtpcom.api_key", "elasticemail.api_key", "smtp2go.api_key",
			"sendlayer.api_key", "resend.api_key", "gmail.client_secret", "outlook.client_secret",
			"zoho.client_secret"}},
	// Post SMTP: Postman/PostmanOptions.php.
	// https://github.com/yehudah/Post-SMTP
	{ID: "post-smtp", Option: "postman_options", MailerPath: "transport_type", DefaultMailer: "default",
		SecretPaths: []string{"basic_auth_password", "mandrill_api_key", "sendgrid_api_key", "mailgun_api_key",
			"sendinblue_api_key", "postmark_api_key", "sparkpost_api_key", "elasticemail_api_key",
			"mailjet_api_key", "mailjet_secret_key", "oauth_client_secret"}},
	// FluentSMTP: app/Services/Mailer/Manager (connections keyed by a
	// hash, each with provider_settings). An empty default connection
	// leaves FluentSMTP on the PHP mailer.
	// https://github.com/WPManageNinja/fluent-smtp
	{ID: "fluent-smtp", Option: "fluentmail-settings", MailerPath: "misc.default_connection", DefaultMailer: "",
		SecretPaths: []string{"connections.*.provider_settings.password", "connections.*.provider_settings.api_key",
			"connections.*.provider_settings.secret_key", "connections.*.provider_settings.client_secret",
			"connections.*.provider_settings.access_token"}},
}

// gatewayList is Gateways, copied so callers can't change the catalog.
func gatewayList() []Gateway {
	out := slices.Clone(gateways)
	for i := range out {
		out[i].LiveSecretPaths = slices.Clone(out[i].LiveSecretPaths)
		out[i].TestSecretPaths = slices.Clone(out[i].TestSecretPaths)
	}
	return out
}

// mailerList is Mailers, copied.
func mailerList() []Mailer {
	out := slices.Clone(mailers)
	for i := range out {
		out[i].SecretPaths = slices.Clone(out[i].SecretPaths)
	}
	return out
}

// Enabled reports whether the gateway is switched on in settings (as
// Settings reads its option). Only an explicit off (no, 0, false) counts
// as off: a gateway with no EnabledPath or without the setting is on, so
// a mode switch alone still flags live mode.
func (g Gateway) Enabled(settings map[string]string) bool {
	if g.EnabledPath == "" {
		return true
	}
	v, ok := settings[g.EnabledPath]
	v = strings.ToLower(strings.TrimSpace(v))
	return !ok || v == "" || !falsy(v)
}

// Mode returns "live" or "test" for the gateway's mode setting, or
// "unknown" when it is missing or holds neither value. settings is the
// option as Settings reads it; for an entry with no ModePath it holds
// the option's whole value under "". A TestValue or LiveValue ending in
// '_' is a prefix (Razorpay's key ID). Booleans read as 1 and 0 match
// yes and no too.
func (g Gateway) Mode(settings map[string]string) string {
	v, ok := settings[g.ModePath]
	if !ok {
		return EnvUnknown
	}
	v = strings.TrimSpace(v)
	same := func(want string) bool {
		if strings.HasSuffix(want, "_") {
			return strings.HasPrefix(v, want)
		}
		l := strings.ToLower(v)
		w := strings.ToLower(want)
		return l == w || truthy(w) && truthy(l) || falsy(w) && falsy(l) && l != ""
	}
	switch {
	case g.TestValue != "" && same(g.TestValue):
		return EnvTest
	case g.LiveValue != "" && same(g.LiveValue):
		return EnvLive
	}
	return EnvUnknown
}

// GatewayFor returns the catalog entry for an option name.
func GatewayFor(option string) (Gateway, bool) {
	for _, g := range gateways {
		if g.Option == option {
			return g, true
		}
	}
	return Gateway{}, false
}

// MailerFor returns the catalog entry for an option name.
func MailerFor(option string) (Mailer, bool) {
	for _, m := range mailers {
		if m.Option == option {
			return m, true
		}
	}
	return Mailer{}, false
}

// PathMatch reports whether a settings path matches a catalog path,
// where a "*" segment stands for any one key or index:
// "connections.*.provider_settings.password" matches
// "connections.ab12.provider_settings.password" and
// "connections[0].provider_settings.password".
func PathMatch(pattern, path string) bool {
	ps, xs := pathSegments(pattern), pathSegments(path)
	if len(ps) != len(xs) {
		return false
	}
	for i := range ps {
		if ps[i] != "*" && ps[i] != xs[i] {
			return false
		}
	}
	return true
}

// pathSegments splits a settings path into its keys, an index [n] as n.
func pathSegments(p string) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		out = append(out, cur.String())
		cur.Reset()
	}
	for i := 0; i < len(p); i++ {
		switch c := p[i]; {
		case c == '\\' && i+1 < len(p) && p[i+1] == '.':
			cur.WriteByte('.')
			i++
		case c == '.':
			flush()
		case c == '[':
			if cur.Len() > 0 || i > 0 && p[i-1] != '.' && p[i-1] != ']' {
				flush()
			}
			end := strings.IndexByte(p[i:], ']')
			if end < 0 {
				cur.WriteString(p[i:])
				i = len(p)
				continue
			}
			cur.WriteString(p[i+1 : i+end])
			flush()
			i += end
			if i+1 < len(p) && p[i+1] == '.' {
				i++
			}
		default:
			cur.WriteByte(c)
		}
	}
	if cur.Len() > 0 || len(out) == 0 {
		flush()
	}
	return out
}

// testKeyNames are the keys.test_keys entries Roadie swaps live keys for.
var testKeyNames = []string{
	"stripe", "stripe_publishable", "paypal_client_id", "paypal_client_secret",
	"mollie", "razorpay_key_id", "razorpay_key_secret", "square",
}

// TestKeyNames returns the keys.test_keys names Replace reads.
func TestKeyNames() []string { return slices.Clone(testKeyNames) }

// markerless are the test_keys entries whose test values carry no test
// marker: the user's word that they are sandbox keys is all there is, so
// they are swapped in unless they read as live.
var markerless = []string{"paypal_client_id", "paypal_client_secret", "razorpay_key_secret", "square"}

// testKeyName returns the keys.test_keys entry for a hit, or "" when
// Roadie never swaps that provider.
func testKeyName(h Hit, value []byte) string {
	_, field := LeafKey(h.At.Path)
	switch h.Provider {
	case "stripe", "mollie", "square":
		return h.Provider
	case "stripe-publishable":
		return "stripe_publishable"
	case "paypal":
		if strings.Contains(strings.ToLower(field), "secret") {
			return "paypal_client_secret"
		}
		return "paypal_client_id"
	case "razorpay":
		if bytes.Contains(value, []byte("rzp_")) {
			return "razorpay_key_id"
		}
		return "razorpay_key_secret"
	}
	return ""
}

// lookupTestKey reads a test_keys entry under its underscore or hyphen name.
func lookupTestKey(testKeys map[string]string, name string) string {
	if v := strings.TrimSpace(testKeys[name]); v != "" {
		return v
	}
	return strings.TrimSpace(testKeys[strings.ReplaceAll(name, "_", "-")])
}

// replace is Replace.
func replace(h Hit, value []byte, mode string, testKeys map[string]string) ([]byte, string, error) {
	switch mode {
	case "", "swap", "blank":
	default:
		return nil, "", core.Errorf(core.ErrConfig, cli.ExitUsage,
			".roadie/config.yaml: keys.mode is %q; use swap or blank", mode)
	}
	if h.Env != EnvLive {
		return value, "kept", nil
	}
	with, action := []byte{}, "blanked"
	if mode != "blank" {
		if name := testKeyName(h, value); name != "" {
			tk := lookupTestKey(testKeys, name)
			env := classify(name, tk, nil)
			if tk != "" && (env == EnvTest || env == EnvUnknown && slices.Contains(markerless, name) && !anyLive(tk)) {
				with, action = []byte(tk), "swapped"
			}
		}
	}
	// A key inside a longer value (a JSON blob, a line of text) is
	// replaced alone; a key known by its field is the whole value.
	var out []byte
	last, found := 0, false
	for _, t := range findTokens(value, func() string { return strings.ToLower(h.At.Path + " " + h.At.Row) }) {
		if t.rule.provider != h.Provider || t.env == EnvTest {
			continue
		}
		out = append(out, value[last:t.start]...)
		out = append(out, with...)
		last, found = t.end, true
	}
	if !found {
		return with, action, nil
	}
	return append(out, value[last:]...), action, nil
}

// anyLive reports whether any rule of the table reads v as a live key.
func anyLive(v string) bool {
	for _, t := range findTokens([]byte(v), func() string { return "adyen" }) {
		if t.env == EnvLive {
			return true
		}
	}
	return false
}

// testKeyHints say what a test key of each entry looks like.
var testKeyHints = map[string]string{
	"stripe": "sk_test_...", "stripe_publishable": "pk_test_...", "mollie": "test_...",
	"razorpay_key_id": "rzp_test_...", "razorpay_key_secret": "the secret of a rzp_test_ key",
	"paypal_client_id": "a sandbox client ID", "paypal_client_secret": "a sandbox client secret",
	"square": "a sandbox- token",
}

// validateTestKey is ValidateTestKey.
func validateTestKey(provider, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	name := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(provider)), "-", "_")
	if classify(name, value, nil) != EnvLive && !anyLive(value) {
		return nil
	}
	hint := testKeyHints[name]
	if hint == "" {
		hint = "a test key"
	}
	return core.Errorf(core.ErrTestKeyIsLive, cli.ExitUsage,
		"test-key-is-live: keys.test_keys.%s reads a live key (%s). Give a test key (%s) or remove the entry to blank it.",
		name, core.MaskKey(value), hint)
}
