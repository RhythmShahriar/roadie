// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package seed

import (
	"encoding/base64"
	"fmt"
	"slices"
	"strings"
)

// keyShape builds one fake key. Every key is joined from parts at seed
// time, so no literal live-format key is in the repository: push
// protection blocks them and a scanner reading the repository would flag
// them. Live says whether the key is a live one (it must not survive a
// copy) or a test one (it must survive byte for byte).
type keyShape struct {
	live  bool
	parts func(g *generator) []string
}

func (g *generator) chars(n int, set string) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = set[g.r.IntN(len(set))]
	}
	return string(b)
}

const (
	alnum = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	hexLo = "0123456789abcdef"
	upNum = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
)

// keyShapes lists the keys a template can ask for with {{key "<name>"}}.
var keyShapes = map[string]keyShape{
	"stripe_live_secret":      {true, func(g *generator) []string { return []string{"sk", "_", "live", "_", g.chars(24, alnum)} }},
	"stripe_live_publishable": {true, func(g *generator) []string { return []string{"pk", "_", "live", "_", g.chars(24, alnum)} }},
	"stripe_live_restricted":  {true, func(g *generator) []string { return []string{"rk", "_", "live", "_", g.chars(24, alnum)} }},
	"stripe_webhook_secret":   {true, func(g *generator) []string { return []string{"wh", "sec", "_", g.chars(32, alnum)} }},
	"stripe_test_secret":      {false, func(g *generator) []string { return []string{"sk", "_", "test", "_", g.chars(24, alnum)} }},
	"stripe_test_publishable": {false, func(g *generator) []string { return []string{"pk", "_", "test", "_", g.chars(24, alnum)} }},
	"paypal_live_client_id":   {true, func(g *generator) []string { return []string{"A", g.chars(79, alnum)} }},
	"paypal_live_secret":      {true, func(g *generator) []string { return []string{"E", g.chars(79, alnum)} }},
	"paypal_sandbox_client":   {false, func(g *generator) []string { return []string{"A", "b", g.chars(78, alnum)} }},
	"mailgun_key":             {true, func(g *generator) []string { return []string{"key", "-", g.chars(32, hexLo)} }},
	"twilio_sid":              {true, func(g *generator) []string { return []string{"A", "C", g.chars(32, hexLo)} }},
	"twilio_token":            {true, func(g *generator) []string { return []string{g.chars(32, hexLo)} }},
	"aws_access_key":          {true, func(g *generator) []string { return []string{"AK", "IA", g.chars(16, upNum)} }},
	"aws_secret_key":          {true, func(g *generator) []string { return []string{g.chars(40, alnum)} }},
	"mailchimp_key":           {true, func(g *generator) []string { return []string{g.chars(32, hexLo), "-", "us", "21"} }},
	"sendgrid_key":            {true, func(g *generator) []string { return []string{"SG", ".", g.chars(22, alnum), ".", g.chars(43, alnum)} }},
	"smtp_password":           {true, func(g *generator) []string { return []string{g.chars(20, alnum)} }},
	"laravel_app_key": {true, func(g *generator) []string {
		return []string{"base64", ":", base64.StdEncoding.EncodeToString([]byte(g.chars(32, alnum)))}
	}},
}

// KeyNames lists the key names, sorted.
func KeyNames() []string {
	var out []string
	for k := range keyShapes {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func (g *generator) key(name string) (string, bool, error) {
	s, ok := keyShapes[name]
	if !ok {
		return "", false, fmt.Errorf("unknown key %q (keys: %s)", name, strings.Join(KeyNames(), ", "))
	}
	return strings.Join(s.parts(g), ""), s.live, nil
}
