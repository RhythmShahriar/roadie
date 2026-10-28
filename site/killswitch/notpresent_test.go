// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package killswitch

import (
	"context"
	"maps"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
	rdb "github.com/RhythmShahriar/roadie/db"
	"github.com/RhythmShahriar/roadie/keys"
)

// A database without AutomateWoo's queue table reports AutomateWoo as
// not present, not unknown: the screen would list it as still on. A
// missing wc_webhooks table is no webhooks.
func TestAutomateWooNotPresent(t *testing.T) {
	d := prodDB()
	delete(d, "wp_automatewoo_queue")
	delete(d, "wp_wc_webhooks")
	s := fixtureSite(prodFiles(), d)
	inner := s.SourceFunc
	s.SourceFunc = func(tables []string) (core.Source, error) {
		for _, n := range tables {
			if _, ok := d[n]; !ok {
				return nil, &rdb.NoTableError{DB: "staging", Table: n}
			}
		}
		return inner(tables)
	}
	status, ids := check(t, s, fixtureOptions())
	if status[core.FindingSideEffectAutomateWoo] != "not-present" || len(ids[core.FindingSideEffectAutomateWoo]) != 0 {
		t.Errorf("automatewoo: %q %v", status[core.FindingSideEffectAutomateWoo], ids[core.FindingSideEffectAutomateWoo])
	}
	if status[core.FindingSideEffectWebhooks] != "not-present" {
		t.Errorf("webhooks: %q", status[core.FindingSideEffectWebhooks])
	}
}

// planOf runs every WordPress step's Plan and Absent on s.
func planOf(t *testing.T, s core.Site, o Options) (edits []string, absent map[string]string) {
	t.Helper()
	absent = map[string]string{}
	for _, st := range Steps(core.StackWordPress, o) {
		es, err := st.Plan(context.Background(), s)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range es {
			edits = append(edits, e.File+": "+e.What)
		}
		if how, ok := Absent(context.Background(), st, s); ok {
			absent[st.ID()] = how
		}
	}
	return edits, absent
}

// plan knows from pass 1 what the source holds: a side effect the
// source lacks is not-present, as copy's read-back says; no edit to
// an option or a table the source lacks is planned. With no presence
// (nil Source) every step plans all it could change and none is absent.
func TestPlanSourcePresence(t *testing.T) {
	o := fixtureOptions()
	o.GatewayIDs = []string{"stripe"}
	o.Mailers = append(fixtureMailers(), keys.Mailer{ID: "fluentsmtp", Option: "fluentmail-settings", MailerPath: "misc.default_connection"})
	o.Source = &Presence{Options: map[string]bool{"siteurl": true, "woocommerce_stripe_settings": true}, Rows: map[string]int64{"wp_options": 2}, Prefix: "wp_"}
	s := fixtureSite(map[string]string{"wp-config.php": prodConfig()}, db{})
	edits, absent := planOf(t, s, o)
	want := map[string]string{
		core.FindingSideEffectSubscriptions: "WooCommerce Subscriptions isn't on this copy",
		core.FindingSideEffectJetpack:       "Jetpack isn't on this copy",
		core.FindingSideEffectWebhooks:      "no WooCommerce webhooks",
		core.FindingSideEffectAutomateWoo:   "AutomateWoo isn't on this copy",
	}
	if !maps.Equal(absent, want) {
		t.Errorf("absent %v, want %v", absent, want)
	}
	all := strings.Join(edits, "\n")
	for _, missing := range []string{"jetpack_private_options", "wc_webhooks", "automatewoo_queue", "wp_mail_smtp", "fluentmail-settings", "ppcp", "woocommerce_payments"} {
		if strings.Contains(all, missing) {
			t.Errorf("plans an edit to %s, which the source lacks:\n%s", missing, all)
		}
	}
	if !strings.Contains(all, "options woocommerce_stripe_settings: Stripe to test mode") {
		t.Errorf("no Stripe edit:\n%s", all)
	}

	// The source holds them all: nothing is absent and every edit is planned.
	o.Source = &Presence{Options: map[string]bool{"wc_subscriptions_siteurl": true, "jetpack_options": true, "jetpack_private_options": true,
		"fluentmail-settings": true, "woocommerce_stripe_settings": true},
		Rows: map[string]int64{"wp_wc_webhooks": 1, "wp_automatewoo_queue": 0}, Prefix: "wp_"}
	edits, absent = planOf(t, s, o)
	all = strings.Join(edits, "\n")
	if len(absent) != 0 {
		t.Errorf("absent %v", absent)
	}
	for _, e := range []string{"options jetpack_private_options", "wc_webhooks: status to paused", "automatewoo_queue: queue truncated",
		"options fluentmail-settings: misc.default_connection cleared (the default PHP mailer), credentials blanked"} {
		if !strings.Contains(all, e) {
			t.Errorf("no %q in:\n%s", e, all)
		}
	}
	// A plugin folder on the target counts, as the read-back's does.
	o.Source = &Presence{Prefix: "wp_"}
	if _, absent = planOf(t, fixtureSite(prodFiles(), db{}), o); absent[core.FindingSideEffectSubscriptions] != "" || absent[core.FindingSideEffectJetpack] != "" {
		t.Errorf("plugin on disk: absent %v", absent)
	}

	// Unknown presence: nothing absent, every option planned.
	o.Source = nil
	edits, absent = planOf(t, s, o)
	if len(absent) != 0 || !strings.Contains(strings.Join(edits, "\n"), "options wp_mail_smtp") {
		t.Errorf("no presence: absent %v edits %v", absent, edits)
	}
}

// A kill-switch finding's title starts with a capital and its fix names
// the target as the run does.
func TestFindingTitleAndFix(t *testing.T) {
	for in, want := range map[string]string{
		"the installed Echo connector differs": "The installed Echo connector differs",
		"active WooCommerce webhooks send":     "Active WooCommerce webhooks send",
		"wc_subscriptions_siteurl matches":     "wc_subscriptions_siteurl matches",
		"WP_HOME in wp-config.php":             "WP_HOME in wp-config.php",
		"3 queued jobs":                        "3 queued jobs",
		"":                                     "",
	} {
		if got := capital(in); got != want {
			t.Errorf("capital(%q) = %q", in, got)
		}
	}
	s := fixtureSite(map[string]string{"wp-config.php": prodConfig()}, prodDB())
	if got, want := prepareFix(s), "rhythm-roadie prepare "+s.Endpoint().Display+" --only side-effects"; got != want || s.Endpoint().Display == "" {
		t.Errorf("fix %q, want %q", got, want)
	}
	if got := prepareFix(nil); got != "rhythm-roadie prepare <staging> --only side-effects" {
		t.Errorf("no site: %q", got)
	}
	for _, st := range Steps(core.StackWordPress, fixtureOptions()) {
		_, found, err := st.Check(context.Background(), fixtureSite(prodFiles(), prodDB()))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range found {
			if f.Title[0] >= 'a' && f.Title[0] <= 'z' && !strings.ContainsAny(strings.Fields(f.Title)[0], "_.") {
				t.Errorf("%s title %q", f.ID, f.Title)
			}
			if strings.Contains(f.Fix, "<staging>") {
				t.Errorf("%s fix %q", f.ID, f.Fix)
			}
		}
	}
}
