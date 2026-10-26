// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/engine"
	"github.com/RhythmShahriar/roadie/internal/testutil"
	"github.com/RhythmShahriar/roadie/report"
)

// Canned results in the shape of product.md 5.2 to 5.5, so the screens
// are tested without the engine. Every value is a Roadie fake or masked.

func wpStack() core.StackInfo {
	return core.StackInfo{Name: core.StackWordPress, Profiles: []string{"wordpress", "woocommerce", "woocommerce-hpos", "woocommerce-subscriptions", "gravity-forms"}, HPOS: true}
}

func sampleTables() []core.TableReport {
	return []core.TableReport{
		{Name: "wp_users", Rows: 18402, Action: string(core.Fake), Profile: "wordpress", Columns: []core.ColumnReport{
			{Name: "user_email", Rule: "wordpress.users.user_email", Changed: 18400, Kept: 2},
			{Name: "user_login", Rule: "wordpress.users.user_login", Changed: 18400, Kept: 2},
			{Name: "user_pass", Rule: "wordpress.users.user_pass", Changed: 18400, Kept: 2},
		}},
		{Name: "wp_wc_orders", Rows: 96220, Action: string(core.Fake), Profile: "woocommerce-hpos", Columns: []core.ColumnReport{
			{Name: "billing_email", Rule: "woocommerce-hpos.wc_orders.billing_email", Changed: 96220},
			{Name: "customer_note", Rule: "woocommerce-hpos.wc_orders.customer_note", Changed: 1204},
		}},
		{Name: "wp_wpmailsmtp_emails_log", Rows: 9812, Action: string(core.Truncate), Profile: "wp-mail-smtp"},
		{Name: "wp_options", Rows: 1044, Action: string(core.Fake), Profile: "wordpress", Columns: []core.ColumnReport{
			{Name: "option_value", Rule: "wordpress.options.option_value", Changed: 31},
		}},
		{Name: "wp_terms", Rows: 220, Action: string(core.Keep)},
	}
}

func sampleChanges() []core.Change {
	return []core.Change{
		{Rule: "wordpress.users.user_email", Table: "wp_users", Column: "user_email", Action: core.Fake, Rows: 18402, Values: 18400, Kept: 2},
		{Rule: "wordpress.users.user_login", Table: "wp_users", Column: "user_login", Action: core.Fake, Rows: 18402, Values: 18400, Kept: 2},
		{Rule: "wordpress.users.user_pass", Table: "wp_users", Column: "user_pass", Action: core.ResetPassword, Rows: 18402, Values: 18400, Kept: 2},
		{Rule: "woocommerce-hpos.wc_orders.billing_email", Table: "wp_wc_orders", Column: "billing_email", Action: core.Fake, Values: 96220},
		{Rule: "woocommerce-hpos.wc_orders.customer_note", Table: "wp_wc_orders", Column: "customer_note", Action: core.Scrub, Values: 1204},
		{Rule: "url.rewrite", Table: "wp_options", Column: "option_value", Action: core.RewriteURL, Values: 31},
		{Rule: "url.rewrite", Table: "wp_posts", Column: "post_content", Action: core.RewriteURL, Values: 3881},
	}
}

func sampleKeys(action string) []core.KeyHit {
	return []core.KeyHit{
		{ID: "key.stripe-live", Provider: "Stripe", Env: "live", Severity: core.SeverityCritical,
			At:     core.Location{Table: "wp_options", Row: "option_name=woocommerce_stripe_settings", Path: "serialized:secret_key"},
			Masked: core.MaskKey(testutil.J("sk_", "live_", "51Hx00000000000000000000")), Action: action},
		{ID: "key.sendgrid-live", Provider: "SendGrid", Env: "live", Severity: core.SeverityHigh,
			At: core.Location{File: "wp-config.php", Line: 41}, Masked: "SG.•••••", Action: "blanked"},
		{ID: "key.stripe-live", Provider: "Stripe", Env: "test", Severity: core.SeverityNone,
			At: core.Location{Table: "wp_options", Row: "option_name=woocommerce_stripe_settings", Path: "serialized:test_secret_key"}, Masked: "sk_t•••••", Action: "kept"},
	}
}

func sampleEffects(status string) []core.SideEffect {
	return []core.SideEffect{
		{ID: core.FindingSideEffectMail, Status: status, How: "wp-content/mu-plugins/rhythm-echo.php", Undo: "delete the file"},
		{ID: core.FindingConfigEnvironment, Status: "off", How: "'staging' in wp-config.php (the seal needs it)"},
		{ID: core.FindingSideEffectCron, Status: "off", How: "DISABLE_WP_CRON true; 38 events kept, none run", Undo: "set DISABLE_WP_CRON to false"},
		{ID: core.FindingSideEffectWebhooks, Status: "off", How: "6 active → paused"},
	}
}

func baseResult(kind string) *core.Result {
	return &core.Result{
		Kind: kind, Started: testNow, Duration: 82 * time.Second,
		Source:     core.EndpointInfo{Kind: core.SSHSite, Display: "ssh://deploy@shop.com/var/www/shop", Engine: "MySQL", Server: "8.0", Bytes: 2_400_000_000},
		Target:     core.EndpointInfo{Kind: core.LocalSite, Display: "/var/www/staging", Engine: "MariaDB", Server: "10.11"},
		Production: []string{"shop.com", "*.shop.com"}, URL: "https://shop.com",
		Stack: wpStack(), Consistency: core.ConsistencyInfo{Mode: "per-run"},
		Input:  &core.Fingerprint{Kind: "sha256-file", Value: "ab12", Bytes: 2_400_000_000},
		Tables: sampleTables(), Changes: sampleChanges(), FailOn: core.SeverityHigh,
		NotCovered: []string{notCovered},
	}
}

func planResult() *core.Result {
	r := baseResult("plan")
	r.Duration = 3 * time.Second
	r.TargetState = core.Untouched
	r.Keys = sampleKeys("swapped")
	r.SideEffects = sampleEffects("off")
	r.PlannedEdits = []core.PlannedEdit{
		{Step: core.FindingConfigEnvironment, File: "wp-config.php", What: "define WP_ENVIRONMENT_TYPE as 'staging'"},
		{Step: core.FindingSideEffectMail, File: "wp-content/mu-plugins/rhythm-echo.php", What: "install the Echo connector in seal mode"},
	}
	r.RemoteCommands = []string{"wp core version --skip-plugins --skip-themes", "wp db export - --skip-plugins --skip-themes --single-transaction --skip-triggers"}
	r.Uncovered = []core.Uncovered{{Table: "wp_loyalty_members", Column: "email", Rate: 98}}
	r.Findings = []core.Finding{{ID: core.FindingUncoveredColumn, Severity: core.SeverityMedium, Title: "wp_loyalty_members.email looks personal (98% of 1,000 sampled values are emails) and no rule covers it", Count: 1,
		Locations: []core.LocationCount{{Location: core.Location{Table: "wp_loyalty_members", Column: "email"}, Count: 1}}}}
	return r
}

func passVerify() *core.VerifyResult {
	return &core.VerifyResult{
		Mode: "exact",
		Checks: []core.Check{
			{ID: core.FindingExactSurvivors, Status: "pass", Checked: 1_210_000, Detail: "1.21 M hashes, every table and text"},
			{ID: core.FindingPIIEmail, Status: "pass", Checked: 18402, Detail: "18,402 checked, all @example.invalid or kept"},
			{ID: core.FindingPIIPhone, Status: "pass", Checked: 41077},
			{ID: "keys", Status: "pass", Detail: "DB, wp-config.php, .env"},
			{ID: core.FindingURLProduction, Status: "pass", Detail: "0 left of 3,912"},
			{ID: "side-effects", Status: "pass", Detail: "4 of 4"},
		},
		Coverage: core.Coverage{TablesScanned: 61, TablesTotal: 61, RowsScanned: 2_100_000, BytesScanned: 2_400_000_000},
	}
}

func copyPass() *core.Result {
	r := baseResult("copy")
	r.TargetState = core.Replaced
	r.Keys = sampleKeys("swapped")
	r.SideEffects = sampleEffects("off")
	r.Verify = passVerify()
	r.KeptUsers = 2
	r.Findings = []core.Finding{{ID: core.FindingInfoKeptUsers, Severity: core.SeverityInfo, Title: "2 users kept as they are (admin, op•••@ag•••.example)", Count: 2}}
	r.Consistency = core.ConsistencyInfo{Mode: "per-run"}
	return r
}

func copyFail() *core.Result {
	r := copyPass()
	r.SideEffects = sampleEffects("on")
	r.Verify.Checks[1] = core.Check{ID: core.FindingPIIEmail, Status: "fail", Checked: 18402, Detail: "17,880 look real"}
	r.Verify.Checks[5] = core.Check{ID: "side-effects", Status: "fail", Detail: "3 of 4"}
	r.Verify.Findings = []core.Finding{
		{ID: core.FindingSideEffectMail, Severity: core.SeverityCritical, Title: "Mail goes out for real", Count: 1,
			Detail: "no Echo connector, no SMTP override: wp_mail uses WP Mail SMTP (SendGrid)",
			Fix:    "rhythm-roadie prepare /var/www/staging --only side-effects"},
		{ID: core.FindingPIIEmail, Severity: core.SeverityHigh, Title: "real-looking emails", Count: 17880,
			Locations: []core.LocationCount{
				{Location: core.Location{Table: "wp_users", Column: "user_email", Row: "ID=1042"}, Count: 17880},
				{Location: core.Location{Table: "wp_wc_order_addresses", Column: "email"}, Count: 16412}},
			Examples: []string{core.MaskEmail("ada.lovelace@gmail.com")}},
		{ID: core.FindingURLProduction, Severity: core.SeverityMedium, Title: "production URLs", Count: 412,
			Locations: []core.LocationCount{{Location: core.Location{Table: "wp_posts"}, Count: 390}, {Location: core.Location{Table: "wp_options"}, Count: 22}}},
	}
	return r
}

// fakeEnvelope stands in for report.Envelope: the result as JSON, which
// leaves out what core.Result marks json:"-".
func fakeEnvelope(r *core.Result, tool, version string) (*core.Envelope, error) {
	data, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	verdict := "ok"
	if r.Err != nil {
		verdict = "error"
	}
	return &core.Envelope{Schema: core.EnvelopeSchema, Kind: r.Kind, Tool: tool, Version: version, Complete: r.Err == nil,
		Summary: r.Summary(), Verdict: verdict, Data: data}, nil
}

// runFixture is a fake engine and fake reports for one test: run returns
// result (or err), the saved report goes under dir.
type runFixture struct {
	jobs  []engine.Job
	saved []string // folders Save wrote
	opens []string
}

func useFixture(t *testing.T, result *core.Result, runErr error, set func(d *deps)) *runFixture {
	t.Helper()
	fx := &runFixture{}
	dir := t.TempDir()
	testDeps(t, func(d *deps) {
		d.run = func(_ context.Context, j engine.Job) (*core.Result, error) {
			fx.jobs = append(fx.jobs, j)
			if j.Confirm != nil {
				host := j.Opts.ConfirmTarget
				if host == "" {
					host = "staging.shop.test"
				}
				if ok, err := j.Confirm("", host); err != nil || !ok {
					return &core.Result{Kind: string(j.Mode), TargetState: core.Untouched,
						Err: core.Errorf(core.ErrPrepareConfirm, 2, "prepare-confirm: the host typed doesn't match; nothing was changed.")}, nil
				}
			}
			if result == nil {
				return nil, runErr
			}
			c := *result
			return &c, runErr
		}
		d.loadSigner = func(string, func(string) (string, bool)) (signer, bool, error) {
			return fakeSigner{"roadie:7Q3kXc2abcdefghi"}, false, nil
		}
		d.envelope = fakeEnvelope
		d.markdown = func(e *core.Envelope) ([]byte, error) { return append([]byte("## Roadie report\n\n"), e.Data...), nil }
		d.html = func(es []*core.Envelope, _ report.Brand) ([]byte, error) {
			return append([]byte("<!doctype html><title>Roadie</title>"), es[0].Data...), nil
		}
		d.sarif = func(e *core.Envelope) ([]byte, error) { return append([]byte(`{"version":"2.1.0"}`), '\n'), nil }
		d.junit = func(e *core.Envelope) ([]byte, error) { return []byte("<testsuites/>\n"), nil }
		d.save = func(_ string, e *core.Envelope, html []byte) (string, error) {
			folder := filepath.Join(dir, "reports", "2026-10-26T14-02-11")
			if err := os.MkdirAll(folder, 0o700); err != nil {
				return "", err
			}
			b, _ := json.Marshal(e)
			os.WriteFile(filepath.Join(folder, "report.json"), b, 0o600)
			os.WriteFile(filepath.Join(folder, "report.html"), html, 0o600)
			fx.saved = append(fx.saved, folder)
			return ".roadie/reports/2026-10-26T14-02-11", nil
		}
		d.open = func(p string) error { fx.opens = append(fx.opens, p); return nil }
		if set != nil {
			set(d)
		}
	})
	return fx
}

// screen runs a command and returns stdout, stderr and the exit code as
// one text for a golden file.
func screen(t *testing.T, cmd string, args ...string) string {
	t.Helper()
	out, errOut, code := runCmd(t, cmd, args...)
	var b bytes.Buffer
	fmt.Fprintf(&b, "$ rhythm-roadie %s %s\n%s", cmd, joinArgs(args), out)
	if errOut != "" {
		fmt.Fprintf(&b, "--- stderr\n%s", errOut)
	}
	fmt.Fprintf(&b, "--- exit %d\n", code)
	return b.String()
}

func joinArgs(args []string) string {
	var b bytes.Buffer
	for i, a := range args {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(a)
	}
	return b.String()
}

// colorEnv forces color into the test's pipe, as CLICOLOR_FORCE=1 does.
func colorEnv(name string) (string, bool) {
	if name == "CLICOLOR_FORCE" {
		return "1", true
	}
	return "", false
}
