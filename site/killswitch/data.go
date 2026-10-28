// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package killswitch

import (
	"context"
	"strconv"
	"strings"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/site/internal/wpdb"
)

// NoFilesReason is why a check that reads a site's files is skipped on a
// dump or a database target.
const NoFilesReason = "a database or dump target has no files to read"

// NoFilesReasonFor is why check id, a step with no data check, is skipped
// on a dump or a database target.
func NoFilesReasonFor(id string) string {
	if id == core.FindingSideEffectGateways {
		return "on a database or dump target the data scan's key.live-mode reads the gateways"
	}
	return NoFilesReason
}

// dataStep is a step whose check can also run on a target with no files:
// data reads only the target's rows (core.DataStep).
type dataStep struct {
	*step
	data func(ctx context.Context, s core.Site) (core.SideEffect, []core.Finding, error)
}

// CheckData runs the check on the target's rows alone.
func (st *dataStep) CheckData(ctx context.Context, s core.Site) (core.SideEffect, []core.Finding, error) {
	return st.data(ctx, s)
}

// withData gives st a check over the rows alone; nil uses its own check,
// which then must read nothing but the database.
func withData(st *step, data func(context.Context, core.Site) (core.SideEffect, []core.Finding, error)) core.Step {
	if data == nil {
		data = st.check
	}
	return &dataStep{step: st, data: data}
}

// dataFix is the fix of a data-check finding: a dump or a database has
// no files for prepare --only side-effects to change.
const dataFix = "copy again with rhythm-roadie, which turns it off in the data, or turn it off where this data is imported"

// dataFindings swaps the fix of each finding for dataFix.
func dataFindings(found []core.Finding) []core.Finding {
	for i := range found {
		found[i].Fix = dataFix
	}
	return found
}

// onlyData wraps a database-only check so its findings carry dataFix.
func onlyData(check func(context.Context, core.Site) (core.SideEffect, []core.Finding, error)) func(context.Context, core.Site) (core.SideEffect, []core.Finding, error) {
	return func(ctx context.Context, s core.Site) (core.SideEffect, []core.Finding, error) {
		se, found, err := check(ctx, s)
		return se, dataFindings(found), err
	}
}

// schedulerData counts the pending actions of a target with no files: the
// runner is paused by the mu-plugin, which a dump or database can't hold,
// so the side effect is unknown and the count says what waits.
func schedulerData(ctx context.Context, s core.Site) (core.SideEffect, []core.Finding, error) {
	id := core.FindingSideEffectActionScheduler
	table := wpdb.Prefix(ctx, s) + "actionscheduler_actions"
	se := core.SideEffect{ID: id, Status: "unknown", Undo: "none needed: Roadie changed nothing here"}
	if has, err := wpdb.Has(ctx, s, table); err == nil && !has {
		se.Status, se.How, se.Undo = "not-present", "Action Scheduler isn't on this copy", ""
		return se, nil, nil
	}
	pending, err := wpdb.Count(ctx, s, table, []string{"status"}, func(v [][]byte) bool { return string(v[0]) == "pending" })
	if err != nil {
		se.How = "actionscheduler_actions can't be read"
		return se, nil, nil
	}
	if pending == 0 {
		se.Status, se.How = "off", "no pending actions"
		return se, nil, nil
	}
	se.How = strconv.FormatInt(pending, 10) + " pending actions; " + NoFilesReason + ", so pause Action Scheduler where the app runs"
	return se, nil, nil
}

// subscriptionsData checks wc_subscriptions_siteurl against the copy's own
// siteurl. Subscriptions charges renewals only where the two match, so a
// stored URL that differs is the staging mode the mu-plugin also forces.
func subscriptionsData(o Options) func(context.Context, core.Site) (core.SideEffect, []core.Finding, error) {
	return func(ctx context.Context, s core.Site) (core.SideEffect, []core.Finding, error) {
		id := core.FindingSideEffectSubscriptions
		se := core.SideEffect{ID: id, Status: "off", How: "wc_subscriptions_siteurl isn't this copy's URL, so Subscriptions charges no renewals", Undo: "none needed: Roadie never changes wc_subscriptions_siteurl"}
		opts, err := wpdb.Options(ctx, s, "wc_subscriptions_siteurl", "siteurl")
		if err != nil {
			se.Status, se.How = "unknown", "the options table can't be read"
			return se, nil, nil
		}
		stored, present := opts["wc_subscriptions_siteurl"]
		if !present {
			se.Status, se.How, se.Undo = "not-present", "WooCommerce Subscriptions isn't in this copy's data", ""
			return se, nil, nil
		}
		here := o.stagingURL()
		if here == "" {
			here = strings.TrimSuffix(string(opts["siteurl"]), "/")
		}
		if here == "" {
			se.Status, se.How = "unknown", "this copy has no siteurl to compare wc_subscriptions_siteurl with"
			return se, nil, nil
		}
		if !sameSite(strings.Replace(string(stored), "_[wc_subscriptions_siteurl]_", "", 1), here) {
			return se, nil, nil
		}
		se.Status, se.How = "on", "wc_subscriptions_siteurl matches this copy's URL, so Subscriptions treats it as the live site"
		return se, []core.Finding{finding(id, core.SeverityCritical, se.How, "restore wc_subscriptions_siteurl from production", optionAt(ctx, s, "wc_subscriptions_siteurl", ""))}, nil
	}
}

// DataTables are the WordPress tables the data checks read, without the
// table prefix.
var DataTables = []string{"options", "wc_webhooks", "actionscheduler_actions", "automatewoo_queue"}

// DataRead says what the data checks read of a table named <prefix><base>
// (base one of DataTables): the columns and, given their values in that
// order, which rows. A verify of a dump keeps only these.
func DataRead(base string) (cols []string, keep func(vals [][]byte) bool) {
	switch base {
	case "options":
		known := map[string]bool{"siteurl": true, "home": true, "wc_subscriptions_siteurl": true}
		return []string{"option_name", "option_value"}, func(v [][]byte) bool { return known[string(v[0])] }
	case "wc_webhooks":
		return []string{"status"}, func([][]byte) bool { return true }
	case "actionscheduler_actions":
		return []string{"status"}, func(v [][]byte) bool { return string(v[0]) == "pending" }
	case "automatewoo_queue":
		return nil, func([][]byte) bool { return true }
	}
	return nil, nil
}

// RuleEffect names the side effect a kill-switch data rule turns off and
// says how, by the rule's ID; ok is false for a rule that isn't one. A
// dump or stdout target has no files, so these rules are all the kill
// switch does there.
func RuleEffect(o Options, id string) (effect, how string, ok bool) {
	rest, isKill := strings.CutPrefix(id, Profile+".")
	if !isKill {
		return "", "", false
	}
	switch {
	case strings.HasPrefix(rest, "wc_webhooks."):
		return core.FindingSideEffectWebhooks, "WooCommerce webhooks paused, their secrets blanked", true
	case strings.HasPrefix(rest, "automatewoo_queue"):
		return core.FindingSideEffectAutomateWoo, "AutomateWoo's queued workflows dropped", true
	case strings.HasPrefix(rest, "actionscheduler_actions."):
		return core.FindingSideEffectActionScheduler, "pending actions marked canceled", true
	case rest == "jobs" || rest == "job_batches":
		return core.FindingSideEffectQueue, "production's queued jobs dropped", true
	}
	opt, isOpt := strings.CutPrefix(rest, "options.option_value.")
	if !isOpt {
		return "", "", false
	}
	// The leaf may hold dots (smtp.pass); WordPress option names the kill
	// switch reads don't.
	opt, _, _ = strings.Cut(opt, ".")
	for _, g := range o.gateways() {
		if g.Option == opt {
			return core.FindingSideEffectGateways, "payment gateways to test mode, live keys swapped or blanked", true
		}
	}
	if GatewayOptionsMatch(opt) {
		return core.FindingSideEffectGateways, "payment gateways to test mode, live keys swapped or blanked", true
	}
	for _, m := range o.mailers() {
		if m.Option == opt {
			return core.FindingSideEffectMail, "mail plugins to the default PHP mailer, their credentials blanked", true
		}
	}
	for _, j := range jetpackTokens {
		if j.option == opt {
			return core.FindingSideEffectJetpack, "Jetpack connection tokens blanked", true
		}
	}
	return "", "", false
}

// TruncatedTables are the tables the kill switch empties, without a
// table prefix, each with the ID of its rule.
var TruncatedTables = map[string]string{
	"automatewoo_queue":      Profile + ".automatewoo_queue",
	"automatewoo_queue_meta": Profile + ".automatewoo_queue_meta",
	"jobs":                   Profile + ".jobs",
	"job_batches":            Profile + ".job_batches",
}
