// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package profile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path"
	"slices"
	"strings"

	"github.com/RhythmShahriar/rhythm/kit/cli"
	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/roadiecfg"
)

// The profile names of rules that come from the settings file rather
// than a profile: columns: entries and laravel.tables.
const (
	ProfileConfig  = "config"
	ProfileLaravel = "laravel"
)

// compiled holds the fields of Compiled. A rule's Table is an exact name
// or, when the profile wrote a glob, a pattern matched with path.Match.
type compiled struct {
	stack     core.Stack
	profiles  []string
	prefix    string
	sites     []string
	exact     map[string][]core.Rule // table -> rules, sorted
	globs     []core.Rule            // rules whose table is a glob
	userRules map[string]bool        // tables a columns: or laravel.tables rule names
	userGlobs []string
	cfgDrop   []string
	cfgTrunc  []string
	drop      []string // profile lists, expanded
	truncate  []string
	keep      []string
	rowFixes  []string
	keepUsers []string
	// user holds the columns: and laravel.tables rules, so rules added
	// after the compile (WithRules) lose to them as profile rules do.
	user []core.Rule
	// cfg is the settings the policies (keep, free_text, passwords) come
	// from, for rules added after the compile.
	cfg *roadiecfg.Config
}

// hasGlob reports whether a table name is a pattern.
func hasGlob(s string) bool { return strings.ContainsAny(s, "*?[\\") }

// matchAny reports whether table matches one of the names or patterns.
func matchAny(table string, patterns []string) bool {
	for _, p := range patterns {
		if p == table {
			return true
		}
		if hasGlob(p) {
			if ok, _ := path.Match(p, table); ok {
				return true
			}
		}
	}
	return false
}

// pick resolves the profile IDs of a run: "auto" (or none) adds what
// Detect finds for the stack; a named profile must exist and suit it.
func pick(ids []string, defs []Def, s *core.Schema, stack core.Stack, h Hints) ([]Def, error) {
	if len(ids) == 0 {
		ids = []string{"auto"}
	}
	known := make([]string, 0, len(defs))
	for _, d := range defs {
		known = append(known, d.ID)
	}
	var want []string
	for _, id := range ids {
		if id == "auto" {
			for _, d := range DetectWithOptions(h.context(), s, h.Site, defs, h.Options) {
				if def, _ := ByID(defs, d); def.Stack == stack {
					want = append(want, d)
				}
			}
			continue
		}
		d, ok := ByID(defs, id)
		if !ok {
			hint := "."
			if m := cli.Suggest(id, known); m != "" {
				hint = "; did you mean " + m + "?"
			}
			return nil, core.Errorf(core.ErrConfig, 2, "profiles names %q, which isn't a profile%s rhythm-roadie profiles lists them.", id, hint)
		}
		if d.Stack != stack {
			return nil, core.Errorf(core.ErrConfig, 2, "profiles names %s, which is for %s; this is a %s schema. Remove it or set stack:.", id, d.Stack, stack)
		}
		want = append(want, id)
	}
	// The stack's base profile always applies: the core tables are there
	// whatever plugins are detected.
	want = append(want, string(stack))
	slices.Sort(want)
	want = slices.Compact(want)
	out := make([]Def, 0, len(want))
	for _, id := range want {
		if d, ok := ByID(defs, id); ok {
			out = append(out, d)
		}
	}
	return out, nil
}

// Hints are what a run knows beyond the schema: the option names pass 1
// saw (for profiles an option gives away) and the site, for site.DetectStack
// and the files: detectors. The zero value means a dump and nothing more.
type Hints struct {
	Options map[string]bool
	Site    core.Site
	Ctx     context.Context
}

func (h Hints) context() context.Context {
	if h.Ctx != nil {
		return h.Ctx
	}
	return context.Background()
}

// CompileWith is Compile with what pass 1 learned: auto then also picks
// the profiles an option name or a site file gives away.
func CompileWith(ids []string, defs []Def, s *core.Schema, cfg *roadiecfg.Config, h Hints) (*Compiled, error) {
	return compileHints(ids, defs, s, cfg, h)
}

// compile builds the rule set of one run from the schema alone.
func compile(ids []string, defs []Def, s *core.Schema, cfg *roadiecfg.Config) (*Compiled, error) {
	return compileHints(ids, defs, s, cfg, Hints{})
}

// compileHints builds the rule set of one run.
func compileHints(ids []string, defs []Def, s *core.Schema, cfg *roadiecfg.Config, h Hints) (*Compiled, error) {
	if cfg == nil {
		cfg = roadiecfg.Default()
	}
	stack, err := ResolveStack(h.context(), cfg.Stack, s, h.Site, len(cfg.Columns) > 0)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		ids = cfg.Profiles
	}
	picked, err := pick(ids, defs, s, stack, h)
	if err != nil {
		return nil, err
	}
	c := &compiled{stack: stack, exact: map[string][]core.Rule{}, userRules: map[string]bool{},
		cfgDrop: cfg.Drop, cfgTrunc: cfg.Truncate, keepUsers: cfg.Pseudonymise.KeepUsers, cfg: cfg}
	if stack == core.StackWordPress {
		c.prefix, _ = WordPressPrefix(s)
		c.sites = Sites(s, c.prefix)
	}
	exists := func(t string) bool { return s == nil || len(s.Tables) == 0 || s.Table(t) != nil }
	expand := func(t string) []string {
		var out []string
		for _, name := range Expand(t, c.prefix, c.sites) {
			if hasGlob(name) || exists(name) {
				out = append(out, name)
			}
		}
		return out
	}
	var rules []core.Rule
	for _, d := range picked {
		c.profiles = append(c.profiles, d.ID)
		for _, r := range d.Rules {
			r = applyPolicies(r, d.Parts[r.ID], cfg)
			for _, name := range expand(r.Table) {
				rr := r
				rr.Table = name
				rules = append(rules, rr)
			}
		}
		for _, t := range d.Drop {
			c.drop = append(c.drop, expand(t)...)
		}
		for _, t := range d.Truncate {
			c.truncate = append(c.truncate, expand(t)...)
		}
		for _, t := range d.Keep {
			c.keep = append(c.keep, expand(t)...)
		}
		c.rowFixes = append(c.rowFixes, d.RowFixes...)
	}
	user, err := userRules(cfg, stack, expand)
	if err != nil {
		return nil, err
	}
	for _, u := range user {
		rules = slices.DeleteFunc(rules, func(r core.Rule) bool { return overrides(u, r) })
		if hasGlob(u.Table) {
			c.userGlobs = append(c.userGlobs, u.Table)
		} else {
			c.userRules[u.Table] = true
		}
	}
	rules = append(rules, user...)
	c.user = user
	for _, r := range rules {
		if hasGlob(r.Table) {
			c.globs = append(c.globs, r)
		} else {
			c.exact[r.Table] = append(c.exact[r.Table], r)
		}
	}
	for t := range c.exact {
		sortRules(c.exact[t])
	}
	sortRules(c.globs)
	for _, l := range []*[]string{&c.drop, &c.truncate, &c.keep, &c.rowFixes} {
		slices.Sort(*l)
		*l = slices.Compact(*l)
	}
	return &Compiled{c: c}, nil
}

// applyPolicies changes a profile rule as the settings ask: an address
// part listed in pseudonymise.keep is kept, free_text blank or keep
// replaces the scrubber, passwords keep leaves hashes alone.
func applyPolicies(r core.Rule, part string, cfg *roadiecfg.Config) core.Rule {
	switch {
	case part != "" && slices.Contains(cfg.Pseudonymise.Keep, part):
		r.Action, r.Fake, r.Value = core.Keep, "", ""
	case r.Action == core.Scrub && cfg.Pseudonymise.FreeText == "blank":
		r.Action, r.Fake = core.Blank, ""
	case r.Action == core.Scrub && cfg.Pseudonymise.FreeText == "keep":
		r.Action, r.Fake = core.Keep, ""
	case r.Action == core.ResetPassword && cfg.Pseudonymise.Passwords == "keep":
		r.Action = core.Keep
	}
	return r
}

// userRules turns columns: (and laravel.tables for a Laravel run) into
// rules. Their tables are written as they are in the database, or with
// {prefix} and {site} like a profile's.
func userRules(cfg *roadiecfg.Config, stack core.Stack, expand func(string) []string) ([]core.Rule, error) {
	var out []core.Rule
	add := func(profile, table, column string, where map[string]string, rule, explain string) error {
		spec, err := roadiecfg.ParseRule(rule)
		if err != nil {
			return core.Errorf(core.ErrConfig, 2, "%s.%s: %v", table, column, err)
		}
		for _, name := range expand(table) {
			out = append(out, core.Rule{ID: profile + "." + ruleSuffix(table, column, where, spec.Path), Profile: profile,
				Table: name, Column: column, Where: where, Path: spec.Path, Action: spec.Action, Fake: spec.Fake,
				Value: spec.Value, Explain: explain})
		}
		return nil
	}
	if stack == core.StackLaravel {
		for _, t := range sortedKeys(cfg.Laravel.Tables) {
			for _, col := range sortedKeys(cfg.Laravel.Tables[t]) {
				if err := add(ProfileLaravel, t, col, nil, cfg.Laravel.Tables[t][col], "laravel.tables in .roadie/config.yaml"); err != nil {
					return nil, err
				}
			}
		}
	}
	for _, r := range cfg.Columns {
		if err := add(ProfileConfig, r.Table, r.Column, r.Where, r.Rule, "columns: in .roadie/config.yaml"); err != nil {
			return nil, err
		}
	}
	// A later entry for the same table, column, where and path wins, as a
	// later line of a settings file usually does.
	var dedup []core.Rule
	for i := len(out) - 1; i >= 0; i-- {
		if !slices.ContainsFunc(dedup, func(r core.Rule) bool { return sameTarget(r, out[i]) }) {
			dedup = append(dedup, out[i])
		}
	}
	slices.Reverse(dedup)
	return dedup, nil
}

// sameTarget reports whether two rules change the same values.
func sameTarget(a, b core.Rule) bool {
	return a.Table == b.Table && strings.EqualFold(a.Column, b.Column) && a.Path == b.Path && equalWhere(a.Where, b.Where)
}

// overrides reports whether user rule u replaces profile rule r: the
// same table and column with the same where clause (or none, which means
// the whole column) and path (or none).
func overrides(u, r core.Rule) bool {
	if u.Table != r.Table || !strings.EqualFold(u.Column, r.Column) {
		return false
	}
	if len(u.Where) > 0 && !equalWhere(u.Where, r.Where) {
		return false
	}
	return u.Path == "" || u.Path == r.Path
}

func equalWhere(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

// sortRules orders rules by specificity: more where columns first, then
// fewer glob where values (meta_key _field_3 before _field_*), then a
// path rule before a whole-value one, then by ID so the order never
// depends on map iteration.
func sortRules(rs []core.Rule) {
	slices.SortStableFunc(rs, func(a, b core.Rule) int {
		if len(a.Where) != len(b.Where) {
			return len(b.Where) - len(a.Where)
		}
		if ga, gb := globWheres(a), globWheres(b); ga != gb {
			return ga - gb
		}
		if (a.Path == "") != (b.Path == "") {
			if a.Path != "" {
				return -1
			}
			return 1
		}
		if c := strings.Compare(a.ID, b.ID); c != 0 {
			return c
		}
		return strings.Compare(a.Table, b.Table)
	})
}

// globWheres counts the where values of a rule that are patterns.
func globWheres(r core.Rule) int {
	n := 0
	for _, v := range r.Where {
		if hasGlob(v) {
			n++
		}
	}
	return n
}

// withRules returns a copy of c with rules added after the compile, such
// as the per-form rules of the field-type row fixes. A columns: rule
// still wins over them and rule IDs already in the
// set are skipped, so adding the same rules twice changes nothing. The
// caller has applied the policies (FieldTypes does).
func (c *compiled) withRules(rs []core.Rule) *compiled {
	out := *c
	out.exact = make(map[string][]core.Rule, len(c.exact))
	ids := map[string]bool{}
	for t, list := range c.exact {
		out.exact[t] = slices.Clone(list)
		for _, r := range list {
			ids[r.Table+"\x00"+r.ID] = true
		}
	}
	out.globs = slices.Clone(c.globs)
	touched := map[string]bool{}
	for _, r := range rs {
		if ids[r.Table+"\x00"+r.ID] || slices.ContainsFunc(c.user, func(u core.Rule) bool { return overrides(u, r) }) {
			continue
		}
		ids[r.Table+"\x00"+r.ID] = true
		if hasGlob(r.Table) {
			out.globs = append(out.globs, r)
			sortRules(out.globs)
			continue
		}
		out.exact[r.Table] = append(out.exact[r.Table], r)
		touched[r.Table] = true
	}
	for t := range touched {
		sortRules(out.exact[t])
	}
	return &out
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// MatchWhere reports whether a rule's where clause holds for a row: each
// named column has a value matching its glob. get returns a column's
// value on the row, false for NULL or a column the row doesn't have.
func MatchWhere(r core.Rule, get func(column string) (string, bool)) bool {
	for col, glob := range r.Where {
		v, ok := get(col)
		if !ok {
			return false
		}
		if m, _ := path.Match(glob, v); !m {
			return false
		}
	}
	return true
}

// rulesFor returns the rules for table, before TableAction decides.
func (c *compiled) rulesFor(table string) []core.Rule {
	out := slices.Clone(c.exact[table])
	added := false
	for _, r := range c.globs {
		if ok, _ := path.Match(r.Table, table); ok {
			out = append(out, r)
			added = true
		}
	}
	if added {
		sortRules(out)
	}
	return out
}

func (c *compiled) action(table string) core.Action {
	switch {
	case matchAny(table, c.cfgDrop):
		return core.DropTable
	case matchAny(table, c.cfgTrunc):
		return core.Truncate
	case c.userRules[table] || matchAny(table, c.userGlobs):
		return core.Fake
	case matchAny(table, c.drop):
		return core.DropTable
	case matchAny(table, c.truncate):
		return core.Truncate
	case matchAny(table, c.keep):
		return core.Keep
	case len(c.rulesFor(table)) > 0:
		return core.Fake
	}
	return core.Keep
}

// canonical is what SHA256 hashes: every part of the compiled set in a
// fixed order, so the hash never depends on map order or on how the
// profiles were listed.
type canonical struct {
	Stack     core.Stack  `json:"stack"`
	Profiles  []string    `json:"profiles"`
	Prefix    string      `json:"prefix"`
	Sites     []string    `json:"sites"`
	Rules     []core.Rule `json:"rules"`
	CfgDrop   []string    `json:"config_drop"`
	CfgTrunc  []string    `json:"config_truncate"`
	Drop      []string    `json:"drop"`
	Truncate  []string    `json:"truncate"`
	Keep      []string    `json:"keep"`
	RowFixes  []string    `json:"row_fixes"`
	KeepUsers []string    `json:"keep_users"`
}

func (c *compiled) sha256() string {
	v := canonical{Stack: c.stack, Profiles: c.profiles, Prefix: c.prefix, Sites: c.sites, Rules: c.all(),
		CfgDrop: sortedCopy(c.cfgDrop), CfgTrunc: sortedCopy(c.cfgTrunc), Drop: c.drop, Truncate: c.truncate,
		Keep: c.keep, RowFixes: c.rowFixes, KeepUsers: sortedCopy(c.keepUsers)}
	b, err := json.Marshal(v) // map keys (Where) are sorted by encoding/json
	if err != nil {
		panic("profile: compiled rules don't marshal: " + err.Error()) // only strings and maps of strings
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// all returns every compiled rule: exact tables in name order, then globs.
func (c *compiled) all() []core.Rule {
	var out []core.Rule
	for _, t := range sortedKeys(c.exact) {
		out = append(out, c.exact[t]...)
	}
	return append(out, c.globs...)
}

func sortedCopy(s []string) []string {
	out := slices.Clone(s)
	slices.Sort(out)
	return out
}
