// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package profile

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/roadiecfg"
	"gopkg.in/yaml.v3"
)

// fixtureRow is one row of a fixture: the table it is in and its values.
type fixtureRow struct {
	Table string            `yaml:"table"`
	Row   map[string]string `yaml:"row"`
}

// fixturePair is the row a rule must change and the row it must not.
type fixturePair struct {
	Positive *fixtureRow `yaml:"positive"`
	Negative *fixtureRow `yaml:"negative"`
}

// tablePair is a table a truncate, drop or keep entry must take and a
// lookalike it must leave alone.
type tablePair struct {
	Positive string `yaml:"positive"`
	Negative string `yaml:"negative"`
}

// fixtureFile is profile/testdata/fixtures/<profile>.yaml.
type fixtureFile struct {
	// Prefix is the WordPress table prefix of every table in the file
	// ("wp_" when empty). Tables lists extra tables the schema must have,
	// such as the blogs' options tables that make a multisite.
	Prefix string                 `yaml:"prefix"`
	Tables []string               `yaml:"tables"`
	Rules  map[string]fixturePair `yaml:"rules"`
	// Lists keys "truncate:<entry>", "drop:<entry>" and "keep:<entry>".
	Lists map[string]tablePair `yaml:"lists"`
}

// loadFixtures reads the fixtures of one profile; a profile with nothing
// to test may have none.
func loadFixtures(t *testing.T, id string) (fixtureFile, bool) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "fixtures", id+".yaml"))
	if errors.Is(err, os.ErrNotExist) {
		return fixtureFile{}, false
	}
	if err != nil {
		t.Fatal(err)
	}
	var f fixtureFile
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		t.Fatalf("fixtures %s: %v", id, err)
	}
	if f.Prefix == "" {
		f.Prefix = "wp_"
	}
	return f, true
}

// fixtureSchema builds a schema holding every table and column the
// fixture rows and the profile's rules name.
func fixtureSchema(f fixtureFile, d Def) *core.Schema {
	cols := map[string]map[string]bool{}
	addTable := func(name string) {
		if name != "" && cols[name] == nil {
			cols[name] = map[string]bool{}
		}
	}
	addRow := func(r *fixtureRow) {
		if r == nil {
			return
		}
		addTable(r.Table)
		for c := range r.Row {
			cols[r.Table][c] = true
		}
	}
	for _, p := range f.Rules {
		addRow(p.Positive)
		addRow(p.Negative)
	}
	for _, p := range f.Lists {
		addTable(p.Positive)
		addTable(p.Negative)
	}
	for _, t := range f.Tables {
		addTable(t)
	}
	if d.Stack == core.StackWordPress {
		addTable(f.Prefix + "options")
		cols[f.Prefix+"options"]["option_name"] = true
		cols[f.Prefix+"options"]["option_value"] = true
	}
	s := &core.Schema{Dialect: core.MySQL, Prefix: f.Prefix}
	names := make([]string, 0, len(cols))
	for n := range cols {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		t := &core.Table{Name: n}
		cs := make([]string, 0, len(cols[n]))
		for c := range cols[n] {
			cs = append(cs, c)
		}
		sort.Strings(cs)
		for _, c := range cs {
			t.Columns = append(t.Columns, core.Column{Name: c, Type: "longtext"})
		}
		s.Tables = append(s.Tables, t)
	}
	if d.Stack != core.StackWordPress {
		s.Prefix = ""
	}
	return s
}

// applies reports whether the rule with this ID takes a value of row.
func applies(c *Compiled, id string, r *fixtureRow) bool {
	for _, rule := range c.Rules(r.Table) {
		if rule.ID != id {
			continue
		}
		if _, ok := r.Row[rule.Column]; !ok {
			continue
		}
		if MatchWhere(rule, func(col string) (string, bool) { v, ok := r.Row[col]; return v, ok }) {
			return true
		}
	}
	return false
}

// checkFixtures runs one profile's fixtures and returns what's wrong.
func checkFixtures(t *testing.T, d Def, defs []Def) []string {
	t.Helper()
	f, ok := loadFixtures(t, d.ID)
	if !ok {
		if len(d.Rules)+len(d.Truncate)+len(d.Drop)+len(d.Keep) == 0 {
			return nil
		}
		return []string{d.ID + ": no testdata/fixtures/" + d.ID + ".yaml"}
	}
	var problems []string
	cfg := roadiecfg.Default()
	cfg.Stack = string(d.Stack)
	cfg.Pseudonymise.Keep = nil // a kept part becomes keep, which still "applies"; test the rule as written
	c, err := Compile([]string{d.ID}, defs, fixtureSchema(f, d), cfg)
	if err != nil {
		return []string{d.ID + ": compile: " + err.Error()}
	}
	ids := map[string]bool{}
	for _, r := range d.Rules {
		ids[r.ID] = true
		p, ok := f.Rules[r.ID]
		switch {
		case !ok:
			problems = append(problems, r.ID+": no fixture")
			continue
		case p.Positive == nil || p.Negative == nil:
			problems = append(problems, r.ID+": needs a positive and a negative row")
			continue
		}
		if !applies(c, r.ID, p.Positive) {
			problems = append(problems, r.ID+": doesn't take its positive row in "+p.Positive.Table)
		}
		if applies(c, r.ID, p.Negative) {
			problems = append(problems, r.ID+": takes its negative row in "+p.Negative.Table)
		}
	}
	for id := range f.Rules {
		if !ids[id] {
			problems = append(problems, id+": fixture for a rule the profile doesn't have")
		}
	}
	lists := map[string]core.Action{}
	for kind, entries := range map[string][]string{"truncate": d.Truncate, "drop": d.Drop, "keep": d.Keep} {
		want := map[string]core.Action{"truncate": core.Truncate, "drop": core.DropTable, "keep": core.Keep}[kind]
		for _, e := range entries {
			key := kind + ":" + e
			lists[key] = want
			p, ok := f.Lists[key]
			if !ok || p.Positive == "" || p.Negative == "" {
				problems = append(problems, d.ID+" "+key+": needs a positive and a negative table")
				continue
			}
			// Kept is also what a table without rules gets, so membership
			// of the expanded list is what tells a keep entry's match.
			list := map[string][]string{"truncate": c.c.truncate, "drop": c.c.drop, "keep": c.c.keep}[kind]
			if got := c.TableAction(p.Positive); got != want || !matchAny(p.Positive, list) {
				problems = append(problems, d.ID+" "+key+": "+p.Positive+" is "+string(got))
			}
			if matchAny(p.Negative, list) {
				problems = append(problems, d.ID+" "+key+": lookalike "+p.Negative+" is "+kind+" too")
			}
		}
	}
	for key := range f.Lists {
		if _, ok := lists[key]; !ok {
			problems = append(problems, d.ID+" "+key+": fixture for an entry the profile doesn't have")
		}
	}
	slices.Sort(problems)
	return problems
}

// TestEveryRuleHasFixtures holds every built-in profile to its fixtures:
// each rule takes its positive row and leaves its negative one, each
// truncate, drop and keep entry takes its table and not a lookalike.
func TestEveryRuleHasFixtures(t *testing.T) {
	defs, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range defs {
		for _, p := range checkFixtures(t, d, defs) {
			t.Error(p)
		}
	}
}

// The harness itself fails a profile whose fixture is missing or wrong.
func TestFixtureHarnessCatchesGaps(t *testing.T) {
	defs := testDefs(t)
	mc, _ := ByID(defs, "mini-core")
	problems := checkFixtures(t, mc, defs)
	want := []string{
		"mini-core truncate:{prefix}{site}sessions: lookalike wp_2_sessions is truncate too",
		"mini-core.comments.comment_author_email: no fixture",
		"mini-core.users.user_email: takes its negative row in wp_users",
	}
	for _, w := range want {
		if !slices.Contains(problems, w) {
			t.Errorf("missing problem %q in\n%s", w, strings.Join(problems, "\n"))
		}
	}
}
