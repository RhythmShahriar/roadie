// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package profile

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/internal/testutil"
	"github.com/RhythmShahriar/roadie/roadiecfg"
)

// testDefs parses the compiler test profiles in testdata/profiles.
func testDefs(t *testing.T) []Def {
	t.Helper()
	names, err := filepath.Glob(filepath.Join("testdata", "profiles", "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var defs []Def
	for _, n := range names {
		b, err := os.ReadFile(n)
		if err != nil {
			t.Fatal(err)
		}
		d, err := Parse(filepath.Base(n), b)
		if err != nil {
			t.Fatal(err)
		}
		defs = append(defs, d)
	}
	return defs
}

// schemaOf builds a schema of tables with the given columns; a table
// named "x" with no columns gets option_name and option_value when it
// ends in options.
func schemaOf(prefix string, tables ...string) *core.Schema {
	s := &core.Schema{Dialect: core.MySQL, Prefix: prefix}
	for _, n := range tables {
		t := &core.Table{Name: n}
		cols := []string{"id", "email", "meta_key", "meta_value", "user_email", "user_pass", "comment_content", "comment_author_email"}
		if strings.HasSuffix(n, "options") {
			cols = []string{"option_name", "option_value"}
		}
		if n == "migrations" {
			cols = []string{"id", "migration"}
		}
		for _, c := range cols {
			t.Columns = append(t.Columns, core.Column{Name: c, Type: "longtext"})
		}
		s.Tables = append(s.Tables, t)
	}
	return s
}

func ruleIDs(rs []core.Rule) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.ID
	}
	return out
}

func TestBuiltinParses(t *testing.T) {
	defs, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ByID(defs, "sql"); !ok {
		t.Error("no sql profile")
	}
	for _, d := range defs {
		for _, r := range d.Rules {
			if !strings.HasPrefix(r.ID, d.ID+".") || r.Profile != d.ID {
				t.Errorf("%s: rule ID %s", d.ID, r.ID)
			}
		}
	}
	got, version, err := Load(context.Background(), true)
	if err != nil || len(got) != len(defs) || version != BuiltinVersion {
		t.Errorf("Load offline: %d %s %v", len(got), version, err)
	}
}

func TestParseRefuses(t *testing.T) {
	head := "id: x\nname: X\nstack: wordpress\nchecked_against: \"docs: https://example.com\"\n"
	for name, body := range map[string]string{
		"unknown field":   head + "rulez: []\n",
		"bad row fix":     head + "row_fixes: [make-coffee]\n",
		"no explain":      head + "rules: [{table: t, column: c, rule: email}]\n",
		"bad rule":        head + "rules: [{table: t, column: c, rule: emial, explain: e}]\n",
		"no column":       head + "rules: [{table: t, rule: email, explain: e}]\n",
		"duplicate id":    head + "rules: [{table: t, column: c, rule: email, explain: e}, {table: t, column: c, rule: blank, explain: e}]\n",
		"site first":      head + "rules: [{table: \"{site}{prefix}t\", column: c, rule: email, explain: e}]\n",
		"bad placeholder": head + "rules: [{table: \"{blog}t\", column: c, rule: email, explain: e}]\n",
		"bad where glob":  head + "rules: [{table: t, column: c, where: {k: \"a[\"}, rule: email, explain: e}]\n",
		"bad part":        head + "rules: [{table: t, column: c, part: planet, rule: blank, explain: e}]\n",
		"two paths":       head + "rules: [{table: t, column: c, path: \"json:a\", rule: \"json:b=email\", explain: e}]\n",
		"bad stack":       "id: x\nname: X\nstack: drupal\nchecked_against: \"docs: x\"\n",
		"bad checked":     "id: x\nname: X\nstack: sql\nchecked_against: \"memory\"\n",
		"bad id":          "id: \"a b\"\nname: X\nstack: sql\nchecked_against: \"docs: x\"\n",
	} {
		if _, err := Parse(name, []byte(body)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestCompileBasics(t *testing.T) {
	defs := testDefs(t)
	s := schemaOf("wp_", "wp_options", "wp_users", "wp_comments", "wp_postmeta", "wp_sessions", "wp_old_log", "wp_stats", "wp_other")
	c, err := Compile([]string{"auto"}, defs, s, roadiecfg.Default())
	if err != nil {
		t.Fatal(err)
	}
	if c.Stack() != core.StackWordPress || c.Prefix() != "wp_" || !slices.Equal(c.Profiles(), []string{"mini-core"}) {
		t.Errorf("stack %s prefix %q profiles %v", c.Stack(), c.Prefix(), c.Profiles())
	}
	for table, want := range map[string]core.Action{
		"wp_users": core.Fake, "wp_sessions": core.Truncate, "wp_old_log": core.DropTable, "wp_stats": core.Keep,
		"wp_other": core.Keep, "wp_options": core.Fake,
	} {
		if got := c.TableAction(table); got != want {
			t.Errorf("TableAction(%s) = %s, want %s", table, got, want)
		}
	}
	if c.Rules("wp_sessions") != nil || c.Rules("wp_other") != nil {
		t.Error("a table without fake action has rules")
	}
	// Where rules come first, the catch-all billing rule after the exact ones.
	got := ruleIDs(c.Rules("wp_postmeta"))
	want := []string{"mini-core.postmeta.meta_value._billing_city", "mini-core.postmeta.meta_value._billing_country",
		"mini-core.postmeta.meta_value._billing_email", "mini-core.postmeta.meta_value.billing-rest"}
	if !slices.Equal(got, want) {
		t.Errorf("postmeta rules %v", got)
	}
	if !slices.Equal(c.RowFixes(), []string{"hpos-address-index"}) {
		t.Errorf("row fixes %v", c.RowFixes())
	}
	// The default keep list keeps the country and fakes the city: the
	// city part isn't listed.
	for _, r := range c.Rules("wp_postmeta") {
		switch r.ID {
		case "mini-core.postmeta.meta_value._billing_country":
			if r.Action != core.Keep {
				t.Errorf("country %s", r.Action)
			}
		case "mini-core.postmeta.meta_value._billing_city":
			if r.Action != core.Fake || r.Fake != core.KCity {
				t.Errorf("city %s %s", r.Action, r.Fake)
			}
		}
	}
}

// Multisite: blogs 1, 2 and 11 each get their own rules by exact name,
// so wp_1_ never takes wp_11_ or wp_111_ tables.
func TestMultisiteExpansion(t *testing.T) {
	defs := testDefs(t)
	s := schemaOf("wp_", "wp_options", "wp_users", "wp_comments", "wp_1_options", "wp_1_comments", "wp_2_options",
		"wp_2_comments", "wp_11_options", "wp_11_comments", "wp_111_comments", "wp_404_log", "wp_1_sessions", "wp_11_sessions")
	c, err := Compile(nil, defs, s, roadiecfg.Default())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(c.Sites(), []string{"1_", "2_", "11_"}) {
		t.Fatalf("sites %v", c.Sites())
	}
	for _, table := range []string{"wp_comments", "wp_1_comments", "wp_2_comments", "wp_11_comments"} {
		rs := c.Rules(table)
		if len(rs) != 2 {
			t.Errorf("%s: %v", table, ruleIDs(rs))
		}
		for _, r := range rs {
			if r.Table != table {
				t.Errorf("%s got the rule of %s", table, r.Table)
			}
		}
	}
	if rs := c.Rules("wp_111_comments"); len(rs) != 0 {
		t.Errorf("wp_111_comments (no blog 111) has rules %v", ruleIDs(rs))
	}
	if c.TableAction("wp_1_sessions") != core.Truncate || c.TableAction("wp_11_sessions") != core.Truncate {
		t.Error("per-blog truncate missing")
	}
	// Network tables are never per blog.
	if len(c.Rules("wp_1_users")) != 0 {
		t.Error("users expanded per blog")
	}
	if len(Sites(s, "wp_")) != 3 {
		t.Error("wp_404_log made a blog")
	}
}

func TestOverridePrecedence(t *testing.T) {
	defs := testDefs(t)
	s := schemaOf("wp_", "wp_options", "wp_users", "wp_comments", "wp_postmeta", "wp_sessions", "wp_stats", "wp_loyalty")
	cfg := roadiecfg.Default()
	cfg.Columns = []roadiecfg.ColumnRule{
		{Table: "wp_postmeta", Column: "meta_value", Where: map[string]string{"meta_key": "_billing_email"}, Rule: "blank"},
		{Table: "wp_users", Column: "user_email", Rule: "keep"},
		{Table: "wp_loyalty", Column: "email", Rule: "email"},
		{Table: "wp_stats", Column: "email", Rule: "email"},
		{Table: "wp_loyalty", Column: "email", Rule: "phone"}, // the later entry wins
	}
	cfg.Truncate = []string{"wp_comments"}
	cfg.Drop = []string{"wp_sess*"}
	cfg.Pseudonymise.FreeText = "blank"
	cfg.Pseudonymise.Passwords = "keep"
	cfg.Pseudonymise.Keep = []string{"city"}
	c, err := Compile(nil, defs, s, cfg)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]core.Rule{}
	for _, table := range []string{"wp_postmeta", "wp_users", "wp_loyalty", "wp_stats"} {
		for _, r := range c.Rules(table) {
			byID[r.ID] = r
		}
	}
	checks := []struct {
		id     string
		action core.Action
		fake   core.FakeKind
	}{
		{"config.wp_postmeta.meta_value._billing_email", core.Blank, ""},
		{"config.wp_users.user_email", core.Keep, ""},
		{"config.wp_loyalty.email", core.Fake, core.KPhone},
		{"config.wp_stats.email", core.Fake, core.KEmail},
		{"mini-core.users.user_pass", core.Keep, ""},
		{"mini-core.postmeta.meta_value.billing-rest", core.Blank, ""},
		{"mini-core.postmeta.meta_value._billing_city", core.Keep, ""},
		{"mini-core.postmeta.meta_value._billing_country", core.Blank, ""},
	}
	for _, ch := range checks {
		r, ok := byID[ch.id]
		if !ok || r.Action != ch.action || r.Fake != ch.fake {
			t.Errorf("%s: %+v (found %v), want %s %s", ch.id, r, ok, ch.action, ch.fake)
		}
	}
	for _, gone := range []string{"mini-core.postmeta.meta_value._billing_email", "mini-core.users.user_email"} {
		if _, ok := byID[gone]; ok {
			t.Errorf("%s survived its override", gone)
		}
	}
	if c.TableAction("wp_comments") != core.Truncate || c.TableAction("wp_sessions") != core.DropTable {
		t.Error("config truncate or drop lost to the profile")
	}
	// A columns: rule on a table the profile keeps makes it a fake table.
	if c.TableAction("wp_stats") != core.Fake {
		t.Errorf("wp_stats: %s", c.TableAction("wp_stats"))
	}
	// A rule without where replaces every profile rule on its column.
	cfg = roadiecfg.Default()
	cfg.Columns = []roadiecfg.ColumnRule{{Table: "wp_postmeta", Column: "meta_value", Rule: "text"}}
	c, err = Compile(nil, defs, s, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := ruleIDs(c.Rules("wp_postmeta")); !slices.Equal(got, []string{"config.wp_postmeta.meta_value"}) {
		t.Errorf("whole-column override: %v", got)
	}
}

func TestStackResolution(t *testing.T) {
	defs := append(testDefs(t), mustBuiltin(t)...)
	plain := schemaOf("", "customers", "orders")
	_, err := Compile(nil, defs, plain, roadiecfg.Default())
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Code != core.ErrStack || ce.Exit != 2 ||
		ce.Msg != "stack: no profile matches this dump (no wp_options or migrations table). For your own schema, list the tables under columns: and set stack: sql." {
		t.Fatalf("no stack: %v", err)
	}
	cfg := roadiecfg.Default()
	cfg.Stack = "sql"
	cfg.Columns = []roadiecfg.ColumnRule{{Table: "customers", Column: "email", Rule: "email"}}
	c, err := Compile(nil, defs, plain, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if c.Stack() != core.StackSQL || !slices.Equal(c.Profiles(), []string{"sql"}) {
		t.Errorf("sql: %s %v", c.Stack(), c.Profiles())
	}
	if got := ruleIDs(c.All()); !slices.Equal(got, []string{"config.customers.email"}) {
		t.Errorf("sql rules %v", got)
	}
	// auto with columns and nothing detected runs as sql too.
	cfg.Stack = "auto"
	if c, err := Compile(nil, defs, plain, cfg); err != nil || c.Stack() != core.StackSQL {
		t.Errorf("auto with columns: %v", err)
	}
	// Laravel from its migrations table.
	lar := schemaOf("", "migrations", "users", "sessions", "pulse_entries")
	c, err = Compile(nil, defs, lar, roadiecfg.Default())
	if err != nil {
		t.Fatal(err)
	}
	users := ruleIDs(c.Rules("users"))
	if c.Stack() != core.StackLaravel || c.TableAction("pulse_entries") != core.Truncate ||
		!slices.Contains(users, "mini-laravel.users.email") || !slices.Contains(users, "laravel.users.password") {
		t.Errorf("laravel: %s %v", c.Stack(), ruleIDs(c.All()))
	}
	// laravel.tables rules join for a Laravel run.
	cfg = roadiecfg.Default()
	cfg.Laravel.Tables = map[string]map[string]string{"users": {"email": "keep", "phone": "phone"}}
	c, err = Compile(nil, defs, lar, cfg)
	if err != nil {
		t.Fatal(err)
	}
	// The config's users.email replaces both profiles' rules on it.
	var email []core.Rule
	for _, r := range c.Rules("users") {
		if r.Column == "email" {
			email = append(email, r)
		}
	}
	got := ruleIDs(c.Rules("users"))
	if len(email) != 1 || email[0].Action != core.Keep || email[0].Explain != "laravel.tables in .roadie/config.yaml" ||
		!slices.Contains(got, "laravel.users.phone") {
		t.Errorf("laravel.tables: %v", got)
	}
	for _, bad := range []string{"drupal"} {
		cfg := roadiecfg.Default()
		cfg.Stack = bad
		if _, err := Compile(nil, defs, plain, cfg); err == nil {
			t.Errorf("stack %s accepted", bad)
		}
	}
}

func mustBuiltin(t *testing.T) []Def {
	t.Helper()
	d, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestResolveStack(t *testing.T) {
	wp := schemaOf("", "wp_options")
	ctx := context.Background()
	for _, tc := range []struct {
		want    string
		s       *core.Schema
		cols    bool
		stack   core.Stack
		errCode string
	}{
		{"wordpress", nil, false, core.StackWordPress, ""},
		{"laravel", wp, false, core.StackLaravel, ""},
		{"auto", wp, false, core.StackWordPress, ""},
		{"", schemaOf("", "migrations"), false, core.StackLaravel, ""},
		{"auto", schemaOf("", "t"), true, core.StackSQL, ""},
		{"auto", schemaOf("", "t"), false, "", core.ErrStack},
		{"joomla", wp, false, "", core.ErrConfig},
	} {
		got, err := ResolveStack(ctx, tc.want, tc.s, nil, tc.cols)
		var ce *core.Error
		if tc.errCode != "" {
			if !errors.As(err, &ce) || ce.Code != tc.errCode || ce.Exit != 2 {
				t.Errorf("%s: %v, want %s", tc.want, err, tc.errCode)
			}
			continue
		}
		if err != nil || got != tc.stack {
			t.Errorf("%s: %s %v, want %s", tc.want, got, err, tc.stack)
		}
	}
	// A site that can't tell yet (the site lane's stub) falls back to the schema.
	if got, err := ResolveStack(ctx, "auto", wp, testutil.FakeSite(nil), false); err != nil || got != core.StackWordPress {
		t.Errorf("with a site: %s %v", got, err)
	}
}

func TestProfileErrors(t *testing.T) {
	defs := testDefs(t)
	s := schemaOf("wp_", "wp_options")
	_, err := Compile([]string{"mini-cor"}, defs, s, roadiecfg.Default())
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Exit != 2 || ce.Msg != `profiles names "mini-cor", which isn't a profile; did you mean mini-core? rhythm-roadie profiles lists them.` {
		t.Errorf("unknown: %v", err)
	}
	_, err = Compile([]string{"mini-laravel"}, defs, s, roadiecfg.Default())
	if !errors.As(err, &ce) || ce.Exit != 2 || !strings.Contains(ce.Msg, "which is for laravel; this is a wordpress schema") {
		t.Errorf("wrong stack: %v", err)
	}
}

func TestDetect(t *testing.T) {
	defs := testDefs(t)
	s := schemaOf("wp_", "wp_options", "wp_2_options", "wp_2_mini_entries")
	if got := Detect(s, nil, defs); !slices.Equal(got, []string{"mini-core", "mini-plugin"}) {
		t.Errorf("by a blog's table: %v", got)
	}
	bare := schemaOf("wp_", "wp_options")
	if got := Detect(bare, nil, defs); !slices.Equal(got, []string{"mini-core"}) {
		t.Errorf("bare: %v", got)
	}
	if got := DetectWithOptions(context.Background(), bare, nil, defs, map[string]bool{"mini_version": true}); !slices.Contains(got, "mini-plugin") {
		t.Errorf("by option: %v", got)
	}
	site := testutil.FakeSite(map[string][]byte{"wp-content/plugins/mini/mini.php": []byte("<?php")})
	if got := Detect(bare, site, defs); !slices.Contains(got, "mini-plugin") {
		t.Errorf("by file: %v", got)
	}
	if got := Detect(nil, nil, defs); len(got) != 0 {
		t.Errorf("nil schema: %v", got)
	}
}

// The hash is the same however the inputs are ordered.
func TestSHA256Stable(t *testing.T) {
	defs := testDefs(t)
	s := schemaOf("wp_", "wp_options", "wp_users", "wp_comments", "wp_postmeta", "wp_2_options", "wp_2_comments")
	mk := func(rev bool) *roadiecfg.Config {
		cfg := roadiecfg.Default()
		where := map[string]string{}
		keys := []string{"a", "b", "c", "meta_key"}
		if rev {
			slices.Reverse(keys)
		}
		for _, k := range keys {
			where[k] = "x" + k
		}
		cfg.Columns = []roadiecfg.ColumnRule{{Table: "wp_postmeta", Column: "meta_value", Where: where, Rule: "blank"}}
		cfg.Drop = []string{"b", "a"}
		if rev {
			cfg.Drop = []string{"a", "b"}
		}
		return cfg
	}
	c1, err := Compile(nil, defs, s, mk(false))
	if err != nil {
		t.Fatal(err)
	}
	rdefs := slices.Clone(defs)
	slices.Reverse(rdefs)
	rs := &core.Schema{Dialect: s.Dialect, Prefix: s.Prefix, Tables: slices.Clone(s.Tables)}
	slices.Reverse(rs.Tables)
	c2, err := Compile(nil, rdefs, rs, mk(true))
	if err != nil {
		t.Fatal(err)
	}
	if c1.SHA256() != c2.SHA256() || len(c1.SHA256()) != 64 {
		t.Errorf("hash differs: %s %s", c1.SHA256(), c2.SHA256())
	}
	cfg := mk(false)
	cfg.Pseudonymise.FreeText = "keep"
	c3, _ := Compile(nil, defs, s, cfg)
	if c3.SHA256() == c1.SHA256() {
		t.Error("a policy change kept the hash")
	}
}

func TestMatchWhere(t *testing.T) {
	r := core.Rule{Where: map[string]string{"meta_key": "_billing_*", "type": "order"}}
	row := map[string]string{"meta_key": "_billing_phone", "type": "order"}
	get := func(c string) (string, bool) { v, ok := row[c]; return v, ok }
	if !MatchWhere(r, get) {
		t.Error("match")
	}
	row["type"] = "orders"
	if MatchWhere(r, get) {
		t.Error("type differs")
	}
	delete(row, "type")
	if MatchWhere(r, get) {
		t.Error("NULL matched")
	}
	if !MatchWhere(core.Rule{}, get) {
		t.Error("no where")
	}
}
