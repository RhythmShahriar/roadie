// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package keys finds payment, mail and API keys in data and config files,
// tells live keys from test keys (PRD 12.2) and swaps or blanks the live
// ones. It also holds the gateway and mailer lists the kill switch uses.
//
// The exported names in this file are the cross-lane API other lanes
// build on; new fields are only ever added.
package keys

import "github.com/RhythmShahriar/roadie/core"

// Hit is one key found.
type Hit = core.KeyHit

// ScanValue checks one string leaf; siblings are the other settings of the
// same option (testmode, environment and the like).
//
// Roadie's own rules run first: key formats with a marker anywhere in the
// value, then (when none matched) credentials known by the setting that
// holds them, named by at.Path's last key or at.Row's option_name or
// meta_key. When neither matched, kit's generic rules run on the value
// and on name=value built from the setting's name, reported as
// key.generic. Placeholders never hit. A hit's ID is key.<provider>-live
// whatever its Env; Severity is the rule's when live, medium when the
// environment is unknown, none for a test key and low for a live key in a
// gateway whose enabled setting is off.
func ScanValue(v []byte, at core.Location, siblings map[string]string) []Hit {
	return scanValue(v, at, siblings)
}

// ScanPairs checks the defines of wp-config.php or the lines of .env in file.
// Every pair is a sibling of every other, so MAIL_PASSWORD reads MAIL_HOST
// and PAYPAL_CLIENT_SECRET reads PAYPAL_MODE.
func ScanPairs(file string, pairs []core.Pair) []Hit {
	return scanPairs(file, pairs)
}

// Classify returns "live", "test" or "unknown" for a provider's value.
//
// A value with a marker (sk_live_, rzp_test_, access_token$sandbox$...)
// is classified by it; otherwise siblings decide (testmode, sandbox_on,
// environment, is_live, mode), after the field name the caller may pass
// as siblings["name"]. provider is a key.<provider>-live provider or a
// keys.test_keys name such as paypal_client_secret.
func Classify(provider string, value string, siblings map[string]string) (env string) {
	return classify(provider, value, siblings)
}

// Replace swaps or blanks a live key per mode ("swap" or "blank") and
// returns the new value and the action taken.
//
// A live key whose provider has a keys.test_keys entry (stripe,
// stripe_publishable, paypal_client_id, paypal_client_secret, mollie,
// razorpay_key_id, razorpay_key_secret, square; underscore or hyphen
// names) is swapped for it in swap mode when Classify reads the entry as
// test; an entry whose provider has no test marker (PayPal, the Razorpay
// secret, Square tokens) is swapped unless it reads as live. Otherwise
// the key is blanked. A key inside a longer value is replaced alone; one
// known by its field is the whole value. A test or unknown key comes back
// unchanged with action "kept". An unknown mode is a config error.
func Replace(h Hit, value []byte, mode string, testKeys map[string]string) (out []byte, action string, err error) {
	return replace(h, value, mode, testKeys)
}

// Gateway describes one payment gateway's settings option.
type Gateway struct {
	ID, Name, Option                 string // "stripe", "Stripe", "woocommerce_stripe_settings"
	EnabledPath, ModePath            string // "enabled", "testmode"
	TestValue, LiveValue             string // "yes", "no"
	LiveSecretPaths, TestSecretPaths []string
}

// Gateways returns the gateway list: Stripe, PayPal Payments (its
// settings and its gateway option), WooPayments, Square, Braintree,
// Mollie, Razorpay, Klarna Payments and Authorize.net.
func Gateways() []Gateway { return gatewayList() }

// Mailer describes one SMTP or mail API plugin's settings option.
type Mailer struct {
	ID, Option, MailerPath, DefaultMailer string
	SecretPaths                           []string
}

// Mailers returns the mailer list: WP Mail SMTP, Post SMTP and FluentSMTP.
// SecretPaths may hold a "*" segment (see PathMatch).
func Mailers() []Mailer { return mailerList() }

// ValidateTestKey returns a *core.Error test-key-is-live when value is a live key.
// provider is a keys.test_keys name, with underscores or hyphens. An
// empty value is accepted (the entry blanks).
func ValidateTestKey(provider, value string) error {
	return validateTestKey(provider, value)
}
