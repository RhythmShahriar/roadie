// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package roadiecfg

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/RhythmShahriar/roadie/core"
)

// RuleSpec is a parsed rule name of columns: or a profile file.
type RuleSpec struct {
	Action core.Action
	Fake   core.FakeKind // for Fake and Scrub
	Path   string        // "json:billing.email" or "serialized:secret_key"; "" = whole value
	Value  string        // for Fixed
}

// fakeRules maps a rule name to the kind of fake it makes. "name" is the
// short form of full_name the config comment offers.
var fakeRules = map[string]core.FakeKind{
	"email": core.KEmail, "phone": core.KPhone, "name": core.KFullName,
	"first_name": core.KFirstName, "last_name": core.KLastName, "full_name": core.KFullName,
	"address": core.KAddress, "street": core.KStreet, "city": core.KCity,
	"postcode": core.KPostcode, "ip": core.KIP, "dob": core.KDOB, "company": core.KCompany,
	"username": core.KUsername, "nicename": core.KNicename, "url": core.KURL,
	"user_agent": core.KUserAgent,
}

// plainRules are the rule names that take no argument and make no fake.
// password, delete-row, rewrite-url and swap-key are what the profiles
// use; the config accepts them too, so an override can say the same.
var plainRules = map[string]core.Action{
	"blank": core.Blank, "null": core.SetNull, "keep": core.Keep,
	"password": core.ResetPassword, "delete-row": core.DeleteRow,
	"rewrite-url": core.RewriteURL, "swap-key": core.SwapKey,
}

// RuleNames returns every rule name without an argument, sorted, plus the
// three forms that take one, for help and "did you mean" hints.
func RuleNames() []string {
	out := make([]string, 0, len(fakeRules)+len(plainRules)+4)
	for n := range fakeRules {
		out = append(out, n)
	}
	for n := range plainRules {
		out = append(out, n)
	}
	out = append(out, "text")
	slices.Sort(out)
	return append(out, "fixed:<value>", "json:<path>=<rule>", "serialized:<key>=<rule>")
}

// ParseRule reads one rule name: a fake kind (email, phone, ...), text
// (scrub), blank, null, keep, fixed:<value>, json:<path>=<rule> or
// serialized:<key>=<rule>, plus the profile rules password, delete-row,
// rewrite-url and swap-key. The rule inside json: or serialized: is a
// value rule, never another path.
func ParseRule(s string) (RuleSpec, error) {
	s = strings.TrimSpace(s)
	for _, format := range []string{"json", "serialized"} {
		rest, ok := strings.CutPrefix(s, format+":")
		if !ok {
			continue
		}
		path, inner, ok := strings.Cut(rest, "=")
		if !ok || strings.TrimSpace(path) == "" {
			return RuleSpec{}, fmt.Errorf("rule %q needs a path and a rule, e.g. %s:billing.email=email", s, format)
		}
		spec, err := parseValueRule(strings.TrimSpace(inner))
		if err != nil {
			return RuleSpec{}, err
		}
		if spec.Action == core.DeleteRow {
			return RuleSpec{}, fmt.Errorf("rule %q: delete-row applies to a whole row, not to a path", s)
		}
		spec.Path = format + ":" + strings.TrimSpace(path)
		return spec, nil
	}
	return parseValueRule(s)
}

// parseValueRule reads a rule without a path.
func parseValueRule(s string) (RuleSpec, error) {
	if v, ok := strings.CutPrefix(s, "fixed:"); ok {
		return RuleSpec{Action: core.Fixed, Value: v}, nil
	}
	if k, ok := fakeRules[s]; ok {
		return RuleSpec{Action: core.Fake, Fake: k}, nil
	}
	if s == "text" {
		return RuleSpec{Action: core.Scrub, Fake: core.KText}, nil
	}
	if a, ok := plainRules[s]; ok {
		return RuleSpec{Action: a}, nil
	}
	if s == "" {
		return RuleSpec{}, errors.New("the rule is empty; give one such as email, text or blank")
	}
	names := RuleNames()
	hint := ""
	if m := suggest(s, names); m != "" {
		hint = "; did you mean " + m + "?"
	}
	return RuleSpec{}, fmt.Errorf("%q isn't a rule%s Rules: %s", s, orDot(hint), strings.Join(names, ", "))
}

// orDot ends a sentence: the hint when there is one, else a full stop.
func orDot(hint string) string {
	if hint == "" {
		return "."
	}
	return hint
}
