// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package keys finds payment, mail and API keys in data and config files,
// tells live keys from test keys (PRD 12.2) and swaps or blanks the live
// ones. It also holds the gateway and mailer lists the kill switch uses.
//
// The exported names in this file are the frozen cross-lane API; the
// detect lane replaces the bodies, which return core.ErrNotBuilt or zero
// values until then.
package keys

import "github.com/RhythmShahriar/roadie/core"

// Hit is one key found.
type Hit = core.KeyHit

// ScanValue checks one string leaf; siblings are the other settings of the
// same option (testmode, environment and the like).
func ScanValue(v []byte, at core.Location, siblings map[string]string) []Hit {
	return nil
}

// ScanPairs checks the defines of wp-config.php or the lines of .env in file.
func ScanPairs(file string, pairs []core.Pair) []Hit {
	return nil
}

// Classify returns "live", "test" or "unknown" for a provider's value.
func Classify(provider string, value string, siblings map[string]string) (env string) {
	return ""
}

// Replace swaps or blanks a live key per mode ("swap" or "blank") and
// returns the new value and the action taken.
func Replace(h Hit, value []byte, mode string, testKeys map[string]string) (out []byte, action string, err error) {
	return nil, "", core.ErrNotBuilt
}

// Gateway describes one payment gateway's settings option.
type Gateway struct {
	ID, Name, Option                 string // "stripe", "Stripe", "woocommerce_stripe_settings"
	EnabledPath, ModePath            string // "enabled", "testmode"
	TestValue, LiveValue             string // "yes", "no"
	LiveSecretPaths, TestSecretPaths []string
}

// Gateways returns the gateway list.
func Gateways() []Gateway { return nil }

// Mailer describes one SMTP or mail API plugin's settings option.
type Mailer struct {
	ID, Option, MailerPath, DefaultMailer string
	SecretPaths                           []string
}

// Mailers returns the mailer list.
func Mailers() []Mailer { return nil }

// ValidateTestKey returns a *core.Error test-key-is-live when value is a live key.
func ValidateTestKey(provider, value string) error {
	return core.ErrNotBuilt
}
