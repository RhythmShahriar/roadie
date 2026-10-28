// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package engine

import (
	"slices"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/internal/testutil"
	"github.com/RhythmShahriar/roadie/site/killswitch"
)

// A dump or a database target gets the kill-switch steps of the stack it
// holds; a site with files, a sql stack or no stack gets none.
func TestNoFilesSteps(t *testing.T) {
	old := killSteps
	t.Cleanup(func() { killSteps = old })
	killSteps = killswitch.Steps
	ko := killswitch.Options{}
	if got := noFilesSteps(nil, core.StackWordPress, ko); len(got) == 0 {
		t.Error("a dump of a WordPress site has no steps")
	}
	dsn := testutil.FakeSite(nil)
	dsn.StackName = core.StackSQL
	if got := noFilesSteps(dsn, core.StackWordPress, ko); len(got) == 0 {
		t.Error("a WordPress database has no steps")
	}
	files := testutil.FakeSite(nil)
	files.StackName = core.StackWordPress
	if got := noFilesSteps(files, core.StackWordPress, ko); got != nil {
		t.Errorf("a site with files got %d more steps", len(got))
	}
	for _, st := range []core.Stack{core.StackSQL, ""} {
		if got := noFilesSteps(nil, st, ko); got != nil {
			t.Errorf("stack %q: %d steps", st, len(got))
		}
	}
}

// A dump target's side effects are the data rules that matched a value or
// emptied a table; verify's read-back replaces them, an unknown aside.
func TestDataEffects(t *testing.T) {
	ko := killswitch.Options{}
	tables := []core.TableReport{
		{Name: "wp_wc_webhooks", Columns: []core.ColumnReport{
			{Name: "status", Rule: "killswitch.wc_webhooks.status", Changed: 1},
			{Name: "secret", Rule: "killswitch.wc_webhooks.secret", Kept: 1},
		}},
		{Name: "wp_options", Columns: []core.ColumnReport{
			{Name: "option_value", Rule: "killswitch.options.option_value.woocommerce_stripe_settings.testmode"},
			{Name: "option_value", Rule: "wordpress.options.option_value.siteurl", Changed: 1},
		}},
		{Name: "wp_automatewoo_queue", Profile: killswitch.Profile, Action: string(core.Truncate)},
	}
	got := dataEffects(tables, ko)
	var ids []string
	for _, se := range got {
		ids = append(ids, se.ID)
		if se.Status != "off" || se.How == "" {
			t.Errorf("effect %+v", se)
		}
	}
	if want := []string{core.FindingSideEffectWebhooks, core.FindingSideEffectAutomateWoo}; !slices.Equal(ids, want) {
		t.Fatalf("effects %v, want %v (a rule that matched nothing isn't one)", ids, want)
	}
	merged := mergeEffects(got, []core.SideEffect{
		{ID: core.FindingSideEffectWebhooks, Status: "on"},
		{ID: core.FindingSideEffectActionScheduler, Status: "unknown"},
		{ID: core.FindingSideEffectSubscriptions, Status: "not-present"},
		{ID: core.FindingSideEffectAutomateWoo, Status: "off", How: "read back"},
	})
	if len(merged) != 3 || merged[0].Status != "on" || merged[1].How == "read back" || merged[2].ID != core.FindingSideEffectSubscriptions {
		t.Errorf("merged %+v", merged)
	}
}
