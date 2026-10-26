// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"time"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/internal/testutil"
)

// liveKey is a live-format Stripe key, built at run time.
func liveKey() string { return testutil.J("sk_", "live_", "51Hq", "Zx0aTq2wN8eLm3Vb") }

// The results the report tests render: canned, with a fixed clock, so
// the goldens never depend on another lane's code.

var started = time.Date(2026, 10, 26, 14, 2, 11, 0, time.UTC)

// copyPass is a WooCommerce copy from ssh to a staging site that passed
// an exact verify.
func copyPass() *core.Result {
	return &core.Result{
		Kind:     "copy",
		Started:  started,
		Duration: 82*time.Second + 400*time.Millisecond,
		Source:   core.EndpointInfo{Kind: core.SSHSite, Display: "ssh://deploy@shop.com/var/www/shop", Engine: "mysql", Server: "8.0.36", Bytes: 2_400_000_000},
		Target: core.EndpointInfo{Kind: core.LocalSite, Display: "/var/www/staging", Engine: "mysql", Server: "8.0.36",
			Bytes: 2_390_000_000, State: core.Replaced},
		TargetState: core.Replaced,
		Production:  []string{"shop.com"},
		URL:         "https://staging.shop.test",
		Stack:       core.StackInfo{Name: core.StackWordPress, Profiles: []string{"wordpress", "woocommerce", "woocommerce-hpos"}, HPOS: true, Sites: 1},
		Consistency: core.ConsistencyInfo{Mode: "per-run"},
		Input:       &core.Fingerprint{Kind: "sha256-file", Value: "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08", Bytes: 2_400_000_000},

		RulesetSHA256:       "4b227777d4dd1fc61c6f884f48641d02b4d121d3fd328cb08b5531fcacdabf8a",
		ConfigSHA256:        "ef2d127de37b942baad06145e54b0c619a1f22327b2ebbcfbec78f5564afe39d",
		ProfilePack:         "1.0.0",
		EchoConnectorSHA256: "e7f6c011776e8db7cd330b54174fd76f7d0216b612387a5ffcfb81e6f0919683",

		Tables: []core.TableReport{
			{Name: "wp_users", Rows: 18402, Action: "pseudonymise", Profile: "wordpress", Columns: []core.ColumnReport{
				{Name: "user_email", Rule: "wordpress.users.user_email", Changed: 18401, Kept: 1},
				{Name: "user_pass", Rule: "wordpress.users.user_pass", Changed: 18401, Kept: 1},
			}},
			{Name: "wp_wc_orders", Rows: 41077, Action: "pseudonymise", Profile: "woocommerce-hpos", Columns: []core.ColumnReport{
				{Name: "billing_email", Rule: "woocommerce.wc_orders.billing_email", Changed: 41077},
			}},
			{Name: "wp_woocommerce_sessions", Rows: 912, Action: "truncate", Profile: "woocommerce"},
		},
		Changes: []core.Change{
			{Rule: "wordpress.users.user_email", Table: "wp_users", Column: "user_email", Action: core.Fake, Rows: 18402, Values: 18401, Kept: 1},
			{Rule: "woocommerce.wc_orders.billing_email", Table: "wp_wc_orders", Column: "billing_email", Action: core.Fake, Rows: 41077, Values: 41077},
			{Rule: "url", Table: "wp_options", Column: "option_value", Action: core.RewriteURL, Values: 3912},
		},
		Keys: []core.KeyHit{{ID: "key.stripe-live", Provider: "Stripe", Env: "live", Severity: core.SeverityCritical,
			At:     core.Location{Table: "wp_options", Row: "option_name=woocommerce_stripe_settings", Path: "serialized:secret_key"},
			Masked: core.MaskKey(liveKey()), Action: "swapped"}},
		SideEffects: []core.SideEffect{
			{ID: core.FindingSideEffectMail, Status: "off", How: "Echo connector sealed in wp-content/mu-plugins", Undo: "rhythm echo up on the staging host"},
			{ID: core.FindingSideEffectCron, Status: "off", How: "DISABLE_WP_CRON in wp-config.php", Undo: "wp cron event run --due-now"},
			{ID: core.FindingSideEffectJetpack, Status: "not-present"},
		},
		Covered: []core.Uncovered{{Table: "wp_users", Column: "user_email", Rate: 100}},
		Verify: &core.VerifyResult{
			Mode: "exact",
			Checks: []core.Check{
				{ID: core.FindingExactSurvivors, Status: "pass", Checked: 1_210_044},
				{ID: core.FindingPIIEmail, Status: "pass", Checked: 18402, Detail: "18,402 checked, all @example.invalid or kept"},
				{ID: "keys", Status: "pass", Detail: "database, wp-config.php"},
				{ID: "side-effects", Status: "pass", Checked: 2, Detail: "2 of 2"},
			},
			Coverage: core.Coverage{TablesScanned: 61, TablesTotal: 61, RowsScanned: 1_210_044, BytesScanned: 2_390_000_000},
		},
		Findings: []core.Finding{{ID: core.FindingInfoKeptUsers, Severity: core.SeverityInfo, Title: "1 user kept as it is", Count: 1,
			Locations: []core.LocationCount{{Location: core.Location{Table: "wp_users", Column: "user_email"}, Count: 1}}}},
		FailOn:     core.SeverityHigh,
		NotCovered: []string{NotCoveredSentence},
		KeptUsers:  1,

		BasicAuthPassword: "never-in-a-report",
		Backup:            "/tmp/never-in-a-report.sql.gz",
	}
}

// verifyFail is a standalone heuristic verify of a dump that still
// holds real-looking emails and a live key.
func verifyFail() *core.Result {
	return &core.Result{
		Kind:        "verify",
		Started:     started.Add(8 * time.Minute),
		Duration:    41 * time.Second,
		Target:      core.EndpointInfo{Kind: core.DumpFile, Display: "staging.sql.gz", Engine: "mysql", Bytes: 1_200_000},
		TargetState: core.Untouched,
		Production:  []string{"shop.com"},
		Stack:       core.StackInfo{Name: core.StackWordPress, Profiles: []string{"wordpress", "woocommerce"}},
		Consistency: core.ConsistencyInfo{Mode: "stable", KeyFingerprint: "roadie-key:ABCDEFGHJKLM"},
		Input:       &core.Fingerprint{Kind: "sha256-file", Value: "2c26b46b68ffc68ff99b453c1d30413413422d706483bfa0f98a5e886266e7ae", Bytes: 1_200_000},
		Verify: &core.VerifyResult{
			Mode: "heuristic",
			Findings: []core.Finding{
				{ID: core.FindingPIIEmail, Severity: core.SeverityHigh, Title: "real-looking emails", Count: 3,
					Locations: []core.LocationCount{
						{Location: core.Location{Table: "wp_users", Column: "user_email", Row: "ID=42"}, Count: 2},
						{Location: core.Location{Table: "wp_postmeta", Column: "meta_value", Row: "meta_key=_billing_email"}, Count: 1},
					},
					Examples: []string{core.MaskEmail("ada.lovelace@gmail.com")},
					Fix:      "rhythm-roadie prepare staging.sql.gz"},
				{ID: "key.stripe-live", Severity: core.SeverityCritical, Title: "Stripe live key", Count: 1,
					Locations: []core.LocationCount{{Location: core.Location{Table: "wp_options", Column: "option_value",
						Row: "option_name=woocommerce_stripe_settings", Path: "serialized:secret_key"}, Count: 1}},
					Examples: []string{core.MaskKey(liveKey())}},
				{ID: core.FindingConfigEnvironment, Severity: core.SeverityMedium, Title: "WP_ENVIRONMENT_TYPE is unset", Count: 1,
					Locations: []core.LocationCount{{Location: core.Location{File: "wp-config.php", Line: 41}, Count: 1}}},
			},
			Checks: []core.Check{
				{ID: core.FindingPIIEmail, Status: "fail", Checked: 18402},
				{ID: core.FindingPIIPhone, Status: "pass", Checked: 41077},
				{ID: core.FindingConfigEnvironment, Status: "warn", Checked: 1, Detail: "1 hit under --fail-on high"},
				{ID: "keys", Status: "fail", Detail: "database"},
				{ID: core.FindingUploadsPersonal, Status: "skip", Detail: "a dump has no uploads"},
			},
			Keys: []core.KeyHit{{ID: "key.stripe-live", Provider: "Stripe", Env: "live", Severity: core.SeverityCritical,
				At:     core.Location{Table: "wp_options", Row: "option_name=woocommerce_stripe_settings", Path: "serialized:secret_key"},
				Masked: core.MaskKey(liveKey()), Action: "kept"}},
			Coverage: core.Coverage{TablesScanned: 58, TablesTotal: 61, RowsScanned: 980_211, BytesScanned: 1_200_000,
				Skipped: []core.Skipped{{Table: "wp_wc_admin_notes", Reason: "a table Roadie was told to keep"}}},
			Uncovered: []core.Uncovered{{Table: "wp_loyalty_members", Column: "email", Rate: 92}},
			Soundcheck: &core.SoundcheckResult{Ran: true, ExitCode: 0, Schema: "rhythm-soundcheck/v1",
				Counts: map[string]int64{"pages": 12, "broken": 0}},
		},
		FailOn:     core.SeverityHigh,
		NotCovered: []string{NotCoveredSentence, "uploads on the staging host"},
	}
}

// copyError is a copy that stopped before it wrote anything.
func copyError() *core.Result {
	return &core.Result{
		Kind:        "copy",
		Started:     started,
		Duration:    1200 * time.Millisecond,
		Source:      core.EndpointInfo{Kind: core.DumpFile, Display: "prod.sql.gz"},
		Target:      core.EndpointInfo{Kind: core.LocalSite, Display: "/var/www/shop"},
		TargetState: core.Untouched,
		Production:  []string{"shop.com"},
		URL:         "https://staging.shop.test",
		FailOn:      core.SeverityHigh,
		Err: core.Errorf(core.ErrTargetIsProduction, 3,
			"target-is-production: /var/www/shop is shop.com, which production: lists. Roadie never writes to production. Point --to at the staging copy."),
	}
}
