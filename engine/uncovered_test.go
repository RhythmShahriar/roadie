// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package engine

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/profile"
	"github.com/RhythmShahriar/roadie/roadiecfg"
)

// detectCall is one call the fake detect.Uncovered saw.
type detectCall struct {
	table   string
	covered map[string]bool
	samples map[string][][]byte
}

// fakeUncovered replaces detect.Uncovered: a column whose name holds
// "email" or "phone" and isn't covered is uncovered, with the share of
// its samples that hold an @ as the rate.
func fakeUncovered(t *testing.T) *[]detectCall {
	t.Helper()
	var calls []detectCall
	old := detectUncovered
	detectUncovered = func(tb *core.Table, covered map[string]bool, s map[string][][]byte) []core.Uncovered {
		calls = append(calls, detectCall{tb.Name, covered, s})
		var out []core.Uncovered
		for _, c := range tb.Columns {
			if covered[c.Name] || !strings.Contains(c.Name, "email") && !strings.Contains(c.Name, "phone") {
				continue
			}
			rate := 0
			if n := len(s[c.Name]); n > 0 {
				at := 0
				for _, v := range s[c.Name] {
					if strings.Contains(string(v), "@") {
						at++
					}
				}
				rate = at * 100 / n
			}
			out = append(out, core.Uncovered{Table: tb.Name, Column: c.Name, Rate: rate})
		}
		return out
	}
	t.Cleanup(func() { detectUncovered = old })
	return &calls
}

// table builds a table of text columns, or typed ones as "name:type".
func table(name string, cols ...string) *core.Table {
	t := &core.Table{Name: name}
	for _, c := range cols {
		n, typ, ok := strings.Cut(c, ":")
		if !ok {
			typ = "varchar(255)"
		}
		t.Columns = append(t.Columns, core.Column{Name: n, Type: typ})
	}
	return t
}

func mustCompile(t *testing.T, s *core.Schema, cfg *roadiecfg.Config) *profile.Compiled {
	t.Helper()
	defs, err := profile.Builtin()
	if err != nil {
		t.Fatal(err)
	}
	if cfg == nil {
		cfg = roadiecfg.Default()
	}
	c, err := profile.Compile(nil, defs, s, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// A column keeps 1,000 non-empty samples at most, each cut to 1 KiB.
func TestSamplesCap(t *testing.T) {
	s := newSamples()
	for i := range 2500 {
		s.add("t", "c", []byte(fmt.Sprintf("v%d", i)))
		s.add("t", "c", []byte("   "))
		s.add("t", "c", nil)
	}
	if got := len(s.m["t"]["c"]); got != maxSamples || !s.full("t", "c") || s.full("t", "d") {
		t.Fatalf("kept %d samples", got)
	}
	if string(s.m["t"]["c"][999]) != "v999" {
		t.Errorf("the samples aren't the first 1,000 values: %q", s.m["t"]["c"][999])
	}
	long := []byte(strings.Repeat("x", 5000))
	s.add("t", "long", long)
	long[0] = 'y' // the sample is a copy, not the batch's bytes
	if got := s.m["t"]["long"][0]; len(got) != maxSampleBytes || got[0] != 'x' {
		t.Errorf("long sample %d bytes, first %q", len(got), got[0])
	}
}

// Only uncovered text columns of tables that aren't truncated, dropped
// or kept on purpose reach detect.Uncovered; the result names each
// column with its rate and the covered ones are listed at 100.
func TestScanUncovered(t *testing.T) {
	calls := fakeUncovered(t)
	s := &core.Schema{Dialect: core.MySQL, Prefix: "wp_", Tables: []*core.Table{
		table("wp_options", "option_name", "option_value"),
		table("wp_users", "ID:bigint(20)", "user_email", "user_login", "note"),
		table("wp_loyalty_members", "id:bigint(20)", "email", "phone", "points:int(11)", "photo:blob"),
		table("wp_woocommerce_sessions", "session_key", "session_value", "email"),
		table("wp_woocommerce_order_items", "order_item_id:bigint(20)", "order_item_name"),
		table("wp_learndash_user_activity", "activity_id:bigint(20)", "activity_type", "email"),
		table("wp_old_customers", "email"),
		table("wp_counts", "id:int(11)"),
	}}
	s.Tables[2].Columns[4].Binary = true
	cfg := roadiecfg.Default()
	cfg.Drop = []string{"wp_old_*"}
	c := mustCompile(t, s, cfg)
	if c.TableAction("wp_woocommerce_sessions") != core.Truncate || !c.Kept("wp_learndash_user_activity") || c.Kept("wp_loyalty_members") {
		t.Fatalf("fixture compiled wrong: %s %v", c.TableAction("wp_woocommerce_sessions"), c.Profiles())
	}
	sm := newSamples()
	for i := range 50 {
		sm.add("wp_loyalty_members", "email", []byte(fmt.Sprintf("m%d@example.com", i)))
		sm.add("wp_users", "user_email", []byte("x@example.com")) // covered: must not reach detect
	}
	res := scanUncovered(s, c, sm)
	var tables []string
	for _, call := range *calls {
		tables = append(tables, call.table)
		if call.table == "wp_users" {
			if _, ok := call.samples["user_email"]; ok || !call.covered["user_email"] {
				t.Errorf("a covered column's samples reached detect: %v", call.covered)
			}
		}
	}
	if !slices.Equal(tables, []string{"wp_loyalty_members", "wp_options", "wp_users", "wp_woocommerce_order_items"}) {
		t.Errorf("detect saw %v", tables)
	}
	want := []core.Uncovered{{Table: "wp_loyalty_members", Column: "email", Rate: 100}, {Table: "wp_loyalty_members", Column: "phone", Rate: 0}}
	if !slices.Equal(res.Uncovered, want) {
		t.Errorf("uncovered %v", res.Uncovered)
	}
	if len(res.Findings) != 2 || res.Findings[0].ID != core.FindingUncoveredColumn || res.Findings[0].Severity != core.SeverityMedium ||
		res.Findings[0].Title != "wp_loyalty_members.email looks personal (100% of 50 sampled values look like emails or phones) and no rule covers it" ||
		res.Findings[1].Title != "wp_loyalty_members.phone looks personal (its name) and no rule covers it" {
		t.Errorf("findings %+v", res.Findings)
	}
	if !slices.Contains(res.Covered, core.Uncovered{Table: "wp_users", Column: "user_email", Rate: 100}) ||
		slices.ContainsFunc(res.Covered, func(u core.Uncovered) bool { return u.Table == "wp_woocommerce_sessions" }) {
		t.Errorf("covered %v", res.Covered)
	}
	for _, f := range res.Findings {
		if !core.KnownID(f.ID) {
			t.Errorf("unknown ID %s", f.ID)
		}
	}
}

// The PRD 17 uncovered message, for one column and for two.
func TestUncoveredError(t *testing.T) {
	two := uncoveredError([]core.Uncovered{{Table: "wp_loyalty_members", Column: "email"}, {Table: "wp_loyalty_members", Column: "phone"}})
	want := "uncovered: 2 columns look personal and no rule covers them: wp_loyalty_members.email, wp_loyalty_members.phone. Add them under columns: (rhythm-roadie explain uncovered-column), or --allow-uncovered to copy and list them as findings. The target is untouched."
	if two.Msg != want || two.Exit != 1 || two.Code != core.ErrUncovered || two.State != core.Untouched {
		t.Errorf("got %+v", two)
	}
	one := uncoveredError([]core.Uncovered{{Table: "customers", Column: "email"}})
	if !strings.HasPrefix(one.Msg, "uncovered: 1 column looks personal and no rule covers it: customers.email. Add it under columns:") {
		t.Errorf("got %q", one.Msg)
	}
	mixed := uncoveredErrorIn([]core.Uncovered{{Table: "customers", Column: "email"}}, core.Mixed)
	if mixed.State != core.Mixed || strings.Contains(mixed.Msg, "untouched") ||
		!strings.HasSuffix(mixed.Msg, "The target is in a mixed state and may hold the column as it was in production: run the same command again once it has a rule, or without --one-pass to check before writing.") {
		t.Errorf("mixed: %+v", mixed)
	}
	if !errors.Is(two, &core.Error{Code: core.ErrUncovered}) {
		t.Error("errors.Is by code")
	}
	if exit, _ := core.CodeExit(two.Code); exit != two.Exit {
		t.Errorf("exit %d, PRD 17 says %d", two.Exit, exit)
	}
}

func TestTextColumn(t *testing.T) {
	for typ, want := range map[string]bool{"varchar(100)": true, "char(32)": true, "longtext": true, "text": true, "json": true,
		"character varying(20)": true, "int(11)": false, "bigint unsigned": false, "datetime": false, "textual": false, "blob": false} {
		if got := textColumn(core.Column{Type: typ}); got != want {
			t.Errorf("%s: %v", typ, got)
		}
	}
	if textColumn(core.Column{Type: "varchar(10)", Binary: true}) {
		t.Error("binary varchar counted as text")
	}
	if thousands(1000) != "1,000" || thousands(12) != "12" || thousands(1234567) != "1,234,567" {
		t.Error("thousands")
	}
}
