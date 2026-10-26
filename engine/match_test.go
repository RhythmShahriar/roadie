// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package engine

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/profile"
)

// naiveMatch tries every rule in order: what ruleSet.match must give.
func naiveMatch(rules []core.Rule, get func(string) ([]byte, bool)) (*core.Rule, []core.Rule) {
	var whole *core.Rule
	var paths []core.Rule
	for i := range rules {
		if !profile.MatchWhereBytes(rules[i], get) {
			continue
		}
		if rules[i].Path != "" {
			paths = append(paths, rules[i])
		} else if whole == nil {
			whole = &rules[i]
		}
	}
	return whole, paths
}

// randomRules mixes the shapes a meta table's value column has: exact
// meta keys (some repeated, some with a path), globs, two conditions,
// a condition on another column and no condition at all.
func randomRules(r *rand.Rand, n int) []core.Rule {
	keys := []string{"_billing_email", "_billing_phone", "_shipping_city", "nickname", "description"}
	var out []core.Rule
	for i := range n {
		rule := core.Rule{ID: fmt.Sprint(i), Column: "meta_value", Action: core.Fake}
		switch r.IntN(7) {
		case 0, 1, 2:
			rule.Where = map[string]string{"meta_key": keys[r.IntN(len(keys))]}
		case 3:
			rule.Where = map[string]string{"meta_key": "_billing_*"}
		case 4:
			rule.Where = map[string]string{"meta_key": keys[r.IntN(len(keys))], "post_id": "7"}
		case 5:
			rule.Where = map[string]string{"post_id": "7"}
		}
		if r.IntN(3) == 0 {
			rule.Path = "json:a"
		}
		out = append(out, rule)
	}
	return out
}

// An indexed rule set matches exactly as trying every rule does, for
// small and large sets and rows with and without the where column.
func TestRuleSetMatchesNaive(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	rows := []map[string]string{
		{"meta_key": "_billing_email", "post_id": "7"}, {"meta_key": "_billing_phone", "post_id": "8"},
		{"meta_key": "nickname", "post_id": "7"}, {"meta_key": "other"}, {"post_id": "7"}, {},
	}
	indexed := 0
	for range 300 {
		rules := randomRules(r, r.IntN(30))
		s := newRuleSet(rules)
		if s.byVal != nil {
			indexed++
		}
		for _, row := range rows {
			get := func(c string) ([]byte, bool) { v, ok := row[c]; return []byte(v), ok }
			w1, p1 := s.match(get)
			w2, p2 := naiveMatch(rules, get)
			if w1 != w2 || !slices.EqualFunc(p1, p2, func(a, b core.Rule) bool { return a.ID == b.ID }) {
				t.Fatalf("rules %v row %v: got %v %v, want %v %v", rules, row, w1, p1, w2, p2)
			}
			var all, want []string
			s.each(get, func(r *core.Rule) { all = append(all, r.ID) })
			for _, r := range rules {
				if profile.MatchWhereBytes(r, get) {
					want = append(want, r.ID)
				}
			}
			if !slices.Equal(all, want) {
				t.Fatalf("each: %v, want %v", all, want)
			}
		}
	}
	if indexed == 0 {
		t.Error("no rule set was indexed")
	}
	var none *ruleSet
	if w, p := none.match(nil); w != nil || p != nil {
		t.Error("nil rule set matched")
	}
}

// A rule path with a format matches only that format; one without (the
// kill switch's option rules) matches the leaf in any format.
func TestLeafMatch(t *testing.T) {
	for _, c := range []struct {
		pattern, leaf string
		want          bool
	}{
		{"serialized:testmode", "serialized:testmode", true},
		{"testmode", "serialized:testmode", true},
		{"testmode", "json:testmode", true},
		{"mail.mailer", "serialized:mail.mailer", true},
		{"secret_key", "base64:serialized:secret_key", true},
		{"*", "serialized:a.b", true},
		{"serialized:*", "serialized:a", true},
		{"json:testmode", "serialized:testmode", false},
		{"testmode", "serialized:test_mode", false},
		{"mode", "serialized:testmode", false},
		{"a.json:b", "serialized:a.json:b", true},
		{"b", "serialized:a.json:b", false},
	} {
		if got := leafMatch(c.pattern, c.leaf); got != c.want {
			t.Errorf("leafMatch(%q, %q) = %v, want %v", c.pattern, c.leaf, got, c.want)
		}
	}
}
