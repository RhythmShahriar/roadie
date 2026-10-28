// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package verify

import (
	"context"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/site/killswitch"
)

// shopBatches is a WooCommerce dump's data: one webhook in status, two
// pending actions and the Subscriptions URL beside siteurl.
func shopBatches(webhook, subsURL string) []*core.Batch {
	return []*core.Batch{
		options(
			[]string{"1", "siteurl", "https://staging.shop.test"},
			[]string{"2", "wc_subscriptions_siteurl", subsURL},
			[]string{"3", "_transient_feed", "a long cached value no check reads"},
		),
		batch(table("wp_wc_webhooks", "webhook_id", "status", "delivery_url"), []string{"1", webhook, "https://hooks.example.test/x"}),
		batch(table("wp_actionscheduler_actions", "action_id", "status", "hook"),
			[]string{"1", "pending", "woocommerce_cleanup"}, []string{"2", "complete", "x"}, []string{"3", "pending", "y"}),
	}
}

func sideEffect(res *core.VerifyResult, id string) core.SideEffect {
	for _, se := range res.SideEffects {
		if se.ID == id {
			return se
		}
	}
	return core.SideEffect{}
}

// A dump has no files, yet its rows say whether a webhook is active: the
// data checks run on it and the file checks are listed as skipped.
func TestRunDataStepsOnDump(t *testing.T) {
	steps := killswitch.Steps(core.StackWordPress, killswitch.Options{})
	target := core.Endpoint{Kind: core.DumpFile, Path: "hook.sql", Display: "hook.sql"}

	res := runJob(t, Job{Target: target, Source: source(shopBatches("active", "https://shop.example.com")...), Steps: steps})
	f := find(res, core.FindingSideEffectWebhooks)
	if f == nil || f.Severity != core.SeverityHigh || f.Count != 1 || f.Locations[0].Table != "wp_wc_webhooks" {
		t.Fatalf("active webhook: %v", ids(res))
	}
	if f.Fix == "" || f.Fix == "rhythm-roadie prepare <staging> --only side-effects" {
		t.Errorf("a dump's fix can't be prepare on files: %q", f.Fix)
	}
	if c := checkByID(res, core.FindingSideEffectWebhooks); c.Status != "fail" {
		t.Errorf("webhooks check %+v", c)
	}
	if se := sideEffect(res, core.FindingSideEffectActionScheduler); se.Status != "unknown" || se.How == "" || se.How[:2] != "2 " {
		t.Errorf("pending actions %+v", se)
	}
	// Unknown is not a pass: the check is a skip with the reason, as
	// the side effects table says unknown.
	if c := checkByID(res, core.FindingSideEffectActionScheduler); c.Status != "skip" || c.Detail == "" {
		t.Errorf("action scheduler check %+v", c)
	}
	if se := sideEffect(res, core.FindingSideEffectSubscriptions); se.Status != "off" {
		t.Errorf("subscriptions %+v", se)
	}
	if se := sideEffect(res, core.FindingSideEffectAutomateWoo); se.Status != "not-present" {
		t.Errorf("automatewoo %+v", se)
	}
	for _, id := range []string{core.FindingSideEffectCron, core.FindingSideEffectMail, core.FindingSideEffectJetpack, core.FindingConfigEnvironment} {
		if c := checkByID(res, id); c.Status != "skip" || c.Detail != killswitch.NoFilesReason {
			t.Errorf("%s on a dump: %+v", id, c)
		}
	}

	// Paused, with the Subscriptions URL equal to the copy's own: the
	// webhook passes and Subscriptions would charge renewals.
	res = runJob(t, Job{Target: target, Source: source(shopBatches("paused", "https://staging.shop.test/")...), Steps: steps})
	if find(res, core.FindingSideEffectWebhooks) != nil {
		t.Errorf("paused webhook flagged: %v", ids(res))
	}
	if f := find(res, core.FindingSideEffectSubscriptions); f == nil || f.Severity != core.SeverityCritical {
		t.Errorf("subscriptions URL of this copy: %v", ids(res))
	}

	// Without the kill-switch steps nothing is captured or checked.
	res = runJob(t, Job{Target: target, Source: source(shopBatches("active", "")...)})
	if len(res.SideEffects) != 0 || find(res, core.FindingSideEffectWebhooks) != nil {
		t.Errorf("no steps: %+v", res.SideEffects)
	}
}

// The capture keeps only what the checks read, with a prefix of its own.
func TestCaptureKeepsOnlyCheckedRows(t *testing.T) {
	c := newCapture()
	opts := batch(table("shop_options", "option_id", "option_name", "option_value"),
		[]string{"1", "siteurl", "https://a.test"}, []string{"2", "_transient_x", "big"})
	acts := batch(table("shop_actionscheduler_actions", "action_id", "status"),
		[]string{"1", "pending"}, []string{"2", "complete"})
	other := batch(table("shop_posts", "ID", "post_title"), []string{"1", "Hello"})
	odd := batch(table("shop_plugin_options", "id", "blob"), []string{"1", "x"})
	for _, b := range []*core.Batch{opts, acts, other, odd} {
		c.add(b)
	}
	if n := len(c.batches["shop_options"][0].Rows); n != 1 {
		t.Errorf("options rows %d", n)
	}
	if n := len(c.batches["shop_actionscheduler_actions"][0].Rows); n != 1 {
		t.Errorf("pending rows %d", n)
	}
	if len(c.batches["shop_posts"]) != 0 || len(c.batches["shop_plugin_options"]) != 0 {
		t.Errorf("kept a table no check reads: %v", c.batches)
	}
	s := newDataSite(core.Endpoint{Display: "x.sql"}, core.StackWordPress, []*core.Table{opts.Table, acts.Table, other.Table, odd.Table}, c)
	if id, _ := s.DBIdentity(context.Background()); id.Prefix != "shop_" {
		t.Errorf("prefix %q", id.Prefix)
	}
	if _, err := s.Source(context.Background(), []string{"shop_wc_webhooks"}); err == nil {
		t.Error("a table the dump lacks has a source")
	}
	if _, err := s.ReadFile(context.Background(), "wp-config.php"); err == nil {
		t.Error("a dump has files")
	}
}

// A WordPress database (a DSN) keeps its side-effect.not-applicable note
// and runs the data checks beside it.
func TestRunDataStepsOnDSN(t *testing.T) {
	steps := append(killswitch.Steps(core.StackSQL, killswitch.Options{}), killswitch.Steps(core.StackWordPress, killswitch.Options{})...)
	target := core.Endpoint{Kind: core.DSN, Display: "mysql://staging"}
	res := runJob(t, Job{Target: target, Source: source(shopBatches("active", "")...), Steps: steps})
	if se := sideEffect(res, core.FindingSideEffectNotApplicable); se.ID == "" {
		t.Errorf("no not-applicable note: %+v", res.SideEffects)
	}
	// The note reads nothing: a skipped check with nothing checked.
	if c := checkByID(res, core.FindingSideEffectNotApplicable); c.Status != "skip" || c.Checked != 0 || c.Detail != killswitch.DatabaseOnlyLine {
		t.Errorf("not-applicable check %+v", c)
	}
	if find(res, core.FindingSideEffectWebhooks) == nil {
		t.Errorf("active webhook on a DSN: %v", ids(res))
	}
	if c := checkByID(res, core.FindingSideEffectGateways); c.Status != "skip" || c.Detail == killswitch.NoFilesReason {
		t.Errorf("gateways on a DSN %+v", c)
	}
}
