// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package engine

import (
	"strings"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/profile"
)

// minIndexed is the rule count from which a column's rules are indexed
// by their where value; fewer are cheaper to try one by one.
const minIndexed = 8

// ruleSet is the rules of one column of a batch, ready to match a row.
// A meta table's value column has a rule per meta key (where meta_key:
// _billing_email and so on), so trying each for every row was most of
// the transform's time; the rules whose one where condition is an exact
// value of the same column are found by that value instead.
type ruleSet struct {
	rules []core.Rule
	col   string           // the where column of the indexed rules, "" for none
	byVal map[string][]int // where value -> positions in rules, ascending
	rest  []int            // positions of the other rules, ascending
}

// newRuleSet indexes rules, which stay in their order.
func newRuleSet(rules []core.Rule) *ruleSet {
	s := &ruleSet{rules: rules}
	if len(rules) < minIndexed {
		return s
	}
	for _, r := range rules {
		if len(r.Where) != 1 {
			continue
		}
		for c, g := range r.Where {
			if s.col == "" && !strings.ContainsAny(g, `*?[\`) {
				s.col = c
			}
		}
		if s.col != "" {
			break
		}
	}
	if s.col == "" {
		return s
	}
	s.byVal = map[string][]int{}
	for i, r := range rules {
		g, ok := r.Where[s.col]
		if ok && len(r.Where) == 1 && !strings.ContainsAny(g, `*?[\`) {
			s.byVal[g] = append(s.byVal[g], i)
			continue
		}
		s.rest = append(s.rest, i)
	}
	return s
}

// match picks the rules that apply to a value of this row: the first
// whole-value rule (the most specific, as Rules sorts them) and every
// path rule.
func (s *ruleSet) match(get func(string) ([]byte, bool)) (whole *core.Rule, paths []core.Rule) {
	s.each(get, func(r *core.Rule) {
		if r.Path != "" {
			paths = append(paths, *r)
		} else if whole == nil {
			whole = r
		}
	})
	return whole, paths
}

// each calls fn with every rule whose where clause the row meets, in
// the rules' own order: the indexed rules of the row's where value and
// the rest, merged, so the calls are what trying every rule gives.
func (s *ruleSet) each(get func(string) ([]byte, bool), fn func(*core.Rule)) {
	if s == nil {
		return
	}
	try := func(i int) {
		if r := &s.rules[i]; profile.MatchWhereBytes(*r, get) {
			fn(r)
		}
	}
	if s.byVal == nil {
		for i := range s.rules {
			try(i)
		}
		return
	}
	var keyed []int
	if v, ok := get(s.col); ok {
		keyed = s.byVal[string(v)]
	}
	a, b := 0, 0
	for a < len(keyed) || b < len(s.rest) {
		if b >= len(s.rest) || a < len(keyed) && keyed[a] < s.rest[b] {
			try(keyed[a])
			a++
		} else {
			try(s.rest[b])
			b++
		}
	}
}
