// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package verify

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/codec/nested"
	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/detect"
	"github.com/RhythmShahriar/roadie/internal/testutil"
	"github.com/RhythmShahriar/roadie/keys"
	"github.com/RhythmShahriar/roadie/site/dotenv"
	"github.com/RhythmShahriar/roadie/site/wpconfig"
)

// TestMain swaps in stand-ins for the other lanes' parsers while they
// return core.ErrNotBuilt, so the orchestration is tested now; the
// stand-ins step aside once the real code answers.
func TestMain(m *testing.M) {
	if os.Getenv("ROADIE_FAKE_SOUNDCHECK") != "" {
		soundcheckMain()
	}
	if err := nested.Leaves([]byte(`a:1:{s:1:"a";s:1:"b";}`), func(string, []byte) {}, nested.Options{}); errors.Is(err, core.ErrNotBuilt) {
		formatOf = fakeFormat
		leavesOf = fakeLeaves
	}
	if _, err := wpconfig.Parse([]byte("<?php\n")); errors.Is(err, core.ErrNotBuilt) {
		parsePairs = fakePairs
	} else if _, err := dotenv.Parse([]byte("A=1\n")); errors.Is(err, core.ErrNotBuilt) {
		parsePairs = fakePairs
	}
	realURLFinder = newURLFinder
	if realURLFinder([]string{"shop.com"}) == nil {
		newURLFinder = fakeURLFinder
	}
	os.Exit(m.Run())
}

// fakeFormat and fakeLeaves walk flat serialized and JSON settings with
// keys.Settings, with the path format of codec/nested.
func fakeFormat(b []byte) string {
	if _, ok := keys.Settings(b); ok {
		if b[0] == '{' || b[0] == '[' {
			return "json"
		}
		return "serialized"
	}
	return "text"
}

func fakeLeaves(b []byte, fn func(string, []byte), _ nested.Options) error {
	m, ok := keys.Settings(b)
	if !ok {
		fn("", b)
		return nil
	}
	prefix := "serialized:"
	if b[0] == '{' || b[0] == '[' {
		prefix = "json:"
	}
	for p, v := range m {
		fn(prefix+p, []byte(v))
	}
	return nil
}

// fakePairs reads define('NAME', 'value'); lines and NAME=value lines.
func fakePairs(name string, b []byte) ([]core.Pair, error) {
	var out []core.Pair
	for i, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(l)
		if name == ".env" {
			if k, v, ok := strings.Cut(l, "="); ok && !strings.HasPrefix(l, "#") {
				out = append(out, core.Pair{Name: k, Value: strings.Trim(v, `"`), Line: i + 1})
			}
			continue
		}
		rest, ok := strings.CutPrefix(l, "define(")
		if !ok {
			continue
		}
		rest = strings.TrimSuffix(strings.TrimSuffix(rest, ";"), ")")
		k, v, ok := strings.Cut(rest, ",")
		if !ok {
			continue
		}
		out = append(out, core.Pair{Name: strings.Trim(strings.TrimSpace(k), `'"`), Value: strings.Trim(strings.TrimSpace(v), `'"`), Line: i + 1})
	}
	return out, nil
}

// realURLFinder is the urlrw matcher, kept while a stand-in runs.
var realURLFinder func([]string) func([]byte) []core.Span

// fakeURLFinder finds "//host" for each production host.
func fakeURLFinder(production []string) func([]byte) []core.Span {
	if len(production) == 0 {
		return nil
	}
	return func(text []byte) []core.Span {
		var out []core.Span
		for _, p := range production {
			for off := 0; ; {
				i := bytes.Index(text[off:], []byte("//"+p))
				if i < 0 {
					break
				}
				end := off + i + 2 + len(p)
				if end < len(text) && (text[end] == '.' || text[end] >= 'a' && text[end] <= 'z') {
					off = end
					continue
				}
				out = append(out, core.Span{Start: off + i, End: end, Kind: core.KURL})
				off = end
			}
		}
		return out
	}
}

// table builds a table whose first column is the primary key.
func table(name string, cols ...string) *core.Table {
	t := &core.Table{Name: name, PrimaryKey: []string{cols[0]}}
	for i, c := range cols {
		typ := "longtext"
		if i == 0 {
			typ = "bigint unsigned"
		}
		t.Columns = append(t.Columns, core.Column{Name: c, Type: typ})
	}
	return t
}

// batch builds one batch of string rows; the first value of each row is
// a number.
func batch(t *core.Table, rows ...[]string) *core.Batch {
	b := &core.Batch{Table: t, First: true, Last: true}
	for _, r := range rows {
		var row core.Row
		for i, v := range r {
			kind := core.String
			if i == 0 {
				kind = core.Number
			}
			row.Values = append(row.Values, core.Value{Kind: kind, Raw: []byte(v)})
		}
		b.Rows = append(b.Rows, row)
	}
	return b
}

func source(batches ...*core.Batch) *testutil.Source {
	s := &core.Schema{Dialect: core.MySQL}
	seen := map[string]bool{}
	for _, b := range batches {
		if b.Table != nil && !seen[b.Table.Name] {
			seen[b.Table.Name] = true
			s.Tables = append(s.Tables, b.Table)
		}
	}
	return testutil.FakeSource(s, batches)
}

func runJob(t *testing.T, j Job) *core.VerifyResult {
	t.Helper()
	if j.Opts.Jobs == 0 {
		j.Opts.Jobs = 4
	}
	res, err := Run(context.Background(), j)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return res
}

// checkByID returns the check id of res, or a zero Check.
func checkByID(res *core.VerifyResult, id string) core.Check {
	for _, c := range res.Checks {
		if c.ID == id {
			return c
		}
	}
	return core.Check{}
}

func find(res *core.VerifyResult, id string) *core.Finding {
	for i := range res.Findings {
		if res.Findings[i].ID == id {
			return &res.Findings[i]
		}
	}
	return nil
}

// makeIBAN adds the mod-97 check digits to a country and BBAN.
func makeIBAN(country, bban string) string {
	digits := ""
	for _, c := range bban + country + "00" {
		if c >= 'A' && c <= 'Z' {
			digits += fmt.Sprint(int(c-'A') + 10)
		} else {
			digits += string(c)
		}
	}
	rem := 0
	for _, c := range digits {
		rem = (rem*10 + int(c-'0')) % 97
	}
	return fmt.Sprintf("%s%02d%s", country, 98-rem, bban)
}

// notUncovered drops the uncovered-column findings, which every test
// table without rules has.
func notUncovered(res *core.VerifyResult) []core.Finding {
	var out []core.Finding
	for _, f := range res.Findings {
		if f.ID != core.FindingUncoveredColumn {
			out = append(out, f)
		}
	}
	return out
}

func ids(res *core.VerifyResult) []string {
	var out []string
	for _, f := range res.Findings {
		out = append(out, f.ID)
	}
	return out
}

// keyBody returns n key characters starting with seed. A real key body
// is random, so it has more than three different characters; a body of
// fewer reads as a placeholder (xxxx, 0000).
func keyBody(seed string, n int) string {
	return strings.Repeat(seed+"Zk7Wm", n)[:n]
}

func liveStripe() string { return testutil.J("sk_", "live_", keyBody("Ab3", 24)) }

// ser writes a flat serialized array of strings.
func ser(kv ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "a:%d:{", len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		fmt.Fprintf(&b, "s:%d:\"%s\";s:%d:\"%s\";", len(kv[i]), kv[i], len(kv[i+1]), kv[i+1])
	}
	return b.String() + "}"
}

// One table per detector: each finds its own and only there.
func TestRunEveryDetector(t *testing.T) {
	users := table("wp_users", "ID", "user_email", "display_name")
	meta := table("wp_usermeta", "umeta_id", "meta_key", "meta_value")
	comments := table("wp_comments", "comment_ID", "comment_author_IP", "comment_content")
	orders := table("wp_wc_orders", "id", "billing_phone", "card_number", "iban")
	addr := table("wp_wc_order_addresses", "id", "address_1", "email")
	card := "4539 1488 0343 6467"
	iban := makeIBAN("GB", "NWBK60161331926819")
	res := runJob(t, Job{Source: source(
		batch(users, []string{"1", "ada.kowalczyk@gmail.com", "Ada Kowalczyk"}),
		batch(meta, []string{"7", "billing_phone", "+44 7911 123456"}),
		batch(comments, []string{"3", "81.2.69.160", "Nice shop"}),
		batch(orders, []string{"9", "+49 30 901820", card, iban}),
		batch(addr, []string{"2", "12 Mill Road", "ada.kowalczyk@gmail.com"}),
	)})
	for id, table := range map[string]string{
		core.FindingPIIEmail: "wp_users", core.FindingPIIPhone: "wp_usermeta", core.FindingPIIIP: "wp_comments",
		core.FindingPIICard: "wp_wc_orders", core.FindingPIIIBAN: "wp_wc_orders", core.FindingPIIAddress: "wp_wc_order_addresses",
	} {
		f := find(res, id)
		if f == nil {
			t.Errorf("no %s in %v", id, ids(res))
			continue
		}
		if f.Locations[0].Table != table {
			t.Errorf("%s in %s, want %s", id, f.Locations[0].Table, table)
		}
		if !core.KnownID(f.ID) || f.Title == "" {
			t.Errorf("%s: unknown ID or no title", f.ID)
		}
	}
	if res.Mode != "heuristic" || res.Coverage.RowsScanned != 5 || res.Coverage.TablesScanned != 5 || res.Coverage.TablesTotal != 5 {
		t.Errorf("mode %s coverage %+v", res.Mode, res.Coverage)
	}
	if f := find(res, core.FindingPIIEmail); f.Locations[0].Row != "ID=1" || f.Examples[0] != "ad•••@gm•••.com" {
		t.Errorf("email location %+v examples %v", f.Locations, f.Examples)
	}
}

func TestRunSampleSkipAndAggregation(t *testing.T) {
	users := table("wp_users", "ID", "user_email")
	var rows [][]string
	for i := range 10000 {
		rows = append(rows, []string{fmt.Sprint(i), fmt.Sprintf("member%d.kowalczyk@gmail.com", i)})
	}
	res := runJob(t, Job{Source: source(batch(users, rows[:5000]...), batch(users, rows[5000:]...))})
	f := find(res, core.FindingPIIEmail)
	if f == nil || f.Count != 10000 || len(f.Examples) > core.MaxExamples || len(f.Locations) > core.MaxExamples || len(notUncovered(res)) != 1 {
		t.Fatalf("aggregation: %+v", res.Findings)
	}
	if f.Locations[0].Row != "ID=0" {
		t.Errorf("first location %+v: results are not merged in input order", f.Locations[0])
	}

	res = runJob(t, Job{Source: source(batch(users, rows[:5000]...), batch(users, rows[5000:]...)), Opts: Options{Sample: 100}})
	if res.Coverage.Sample != 100 || res.Coverage.RowsScanned != 100 || find(res, core.FindingPIIEmail).Count != 100 {
		t.Errorf("sample: %+v", res.Coverage)
	}

	res = runJob(t, Job{Source: source(batch(users, rows[:10]...)), Opts: Options{Skip: []string{"emails"}}})
	if find(res, core.FindingPIIEmail) != nil {
		t.Errorf("skip emails: %v", ids(res))
	}
}

func TestRunNothingScanned(t *testing.T) {
	src := source()
	src.Err = errors.New("access denied for user staging")
	_, err := Run(context.Background(), Job{Source: src, Target: core.Endpoint{Display: "staging@localhost"}})
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Code != core.ErrNothingScanned || ce.Exit != 3 {
		t.Fatalf("got %v", err)
	}
	if !strings.HasPrefix(ce.Msg, "nothing-scanned: verify read 0 rows from staging@localhost (") {
		t.Errorf("message %s", ce.Msg)
	}
	empty := source(&core.Batch{Table: table("wp_posts", "ID", "post_content")})
	if _, err := Run(context.Background(), Job{Source: empty}); !errors.Is(err, &core.Error{Code: core.ErrNothingScanned}) {
		t.Errorf("empty table: %v", err)
	}
}

// A failing table is listed under coverage with its reason.
func TestRunSkippedTable(t *testing.T) {
	src := source(batch(table("wp_users", "ID", "user_login"), []string{"1", "admin"}))
	src.Err = errors.New("wp_secret: SELECT command denied")
	res := runJob(t, Job{Source: src})
	want := core.Skipped{Table: "(input after wp_users)", Reason: "wp_secret: SELECT command denied; the tables after it weren't read"}
	if len(res.Coverage.Skipped) != 1 || res.Coverage.Skipped[0] != want {
		t.Errorf("skipped %+v", res.Coverage.Skipped)
	}
}

// A source that fails before its first table names the whole input.
func TestRunSkippedFirstTable(t *testing.T) {
	src := source()
	src.Err = errors.New("dump truncated")
	_, err := Run(context.Background(), Job{Source: src})
	if err == nil || !strings.Contains(err.Error(), "(every read failed: dump truncated)") {
		t.Fatalf("got %v, want nothing-scanned naming the read error", err)
	}
}

func options(rows ...[]string) *core.Batch {
	return batch(table("wp_options", "option_id", "option_name", "option_value"), rows...)
}

func TestRunGatewayLiveModeAndKeys(t *testing.T) {
	res := runJob(t, Job{Source: source(options(
		[]string{"1", "woocommerce_stripe_settings", ser("enabled", "yes", "testmode", "no", "secret_key", "", "test_secret_key", "")},
	))})
	f := find(res, core.FindingKeyLiveMode)
	if f == nil || f.Severity != core.SeverityHigh || f.Locations[0].Path != "serialized:testmode" {
		t.Fatalf("live mode: %+v", res.Findings)
	}

	// A live key in a disabled gateway is low; in an enabled test-mode
	// one it is the key's own severity.
	res = runJob(t, Job{Source: source(options(
		[]string{"1", "woocommerce_stripe_settings", ser("enabled", "no", "testmode", "no", "secret_key", liveStripe())},
	))})
	f = find(res, "key.stripe-live")
	if f == nil || f.Severity != core.SeverityLow || find(res, core.FindingKeyLiveMode) != nil {
		t.Fatalf("disabled gateway: %+v", res.Findings)
	}
	if len(res.Keys) != 1 || res.Keys[0].Provider != "stripe" || strings.Contains(res.Keys[0].Masked, liveStripe()[6:]) {
		t.Errorf("keys %+v", res.Keys)
	}

	// A Square token with no environment beside it is key.unknown-env.
	sq := testutil.J("EAAA", keyBody("Zx9", 60))
	res = runJob(t, Job{Source: source(options([]string{"1", "my_square_token", sq}))})
	if f := find(res, core.FindingKeyUnknownEnv); f == nil || f.Severity != core.SeverityMedium {
		t.Errorf("unknown env: %+v", res.Findings)
	}

	// Test keys are no finding at all.
	res = runJob(t, Job{Source: source(options([]string{"1", "woocommerce_stripe_settings",
		ser("enabled", "yes", "testmode", "yes", "test_secret_key", testutil.J("sk_", "test_", keyBody("Ab3", 24)))}))})
	if len(res.Findings) != 0 {
		t.Errorf("test mode: %v", ids(res))
	}
}

// WooPayments in dev (sandbox) mode only uses a test account, so its
// test_mode setting no longer flags live mode.
func TestRunWooPaymentsDevMode(t *testing.T) {
	opt := func() *core.Batch {
		return options([]string{"1", "woocommerce_woocommerce_payments_settings", ser("enabled", "yes", "test_mode", "no")})
	}
	if f := find(runJob(t, Job{Source: source(opt())}), core.FindingKeyLiveMode); f == nil || f.Title != "WooPayments is in live mode" {
		t.Fatalf("no wp-config: want key.live-mode, got %+v", f)
	}
	for _, c := range []struct {
		config string
		live   bool
	}{
		{"define( 'WCPAY_DEV_MODE', true );", false},
		{"define( 'WP_ENVIRONMENT_TYPE', 'staging' );", false},
		{"define( 'WP_ENVIRONMENT_TYPE', 'development' );", false},
		{"define( 'WP_DEVELOPMENT_MODE', 'plugin' );", false},
		{"define( 'WP_ENVIRONMENT_TYPE', 'production' );", true},
		{"define( 'WCPAY_DEV_MODE', false );", true},
	} {
		st := testutil.FakeSite(map[string][]byte{"wp-config.php": []byte("<?php\n" + c.config + "\n")})
		res := runJob(t, Job{Site: st, Source: source(opt())})
		if got := find(res, core.FindingKeyLiveMode) != nil; got != c.live {
			t.Errorf("%s: live mode %v, want %v", c.config, got, c.live)
		}
	}
	// Dev mode is WooPayments' own: Stripe still counts.
	st := testutil.FakeSite(map[string][]byte{"wp-config.php": []byte("<?php\ndefine( 'WP_ENVIRONMENT_TYPE', 'staging' );\n")})
	res := runJob(t, Job{Site: st, Source: source(options([]string{"1", "woocommerce_stripe_settings", ser("enabled", "yes", "testmode", "no")}))})
	if find(res, core.FindingKeyLiveMode) == nil {
		t.Errorf("stripe on a staging site: want key.live-mode, got %v", ids(res))
	}
}

func TestRunEncrypted(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString([]byte(`{"iv":"abc","value":"def","mac":"0123","tag":""}`))
	plain := base64.StdEncoding.EncodeToString([]byte(`{"iv":"abc","value":"def","other":"x"}`))
	res := runJob(t, Job{Source: source(batch(table("users", "id", "ssn", "note"),
		[]string{"1", payload, plain}, []string{"2", payload, plain}))})
	f := find(res, core.FindingDataEncrypted)
	if f == nil || f.Count != 2 || f.Locations[0].Column != "ssn" || len(notUncovered(res)) != 1 {
		t.Fatalf("encrypted: %+v", res.Findings)
	}
	// An info finding fails its check only when --fail-on is info.
	if c := checkByID(res, core.FindingDataEncrypted); c.Status != "pass" || c.Checked != 2 {
		t.Errorf("data.encrypted check %+v", c)
	}
	res = runJob(t, Job{Source: source(batch(table("users", "id", "ssn"), []string{"1", payload})), Opts: Options{FailOn: core.SeverityInfo}})
	if c := checkByID(res, core.FindingDataEncrypted); c.Status != "fail" || c.Detail != "1 hit" {
		t.Errorf("data.encrypted check at --fail-on info %+v", c)
	}
}

// A ShouldBeEncrypted job keeps its command encrypted inside the JSON
// payload of the jobs table.
func TestRunEncryptedJobPayload(t *testing.T) {
	cmd := base64.StdEncoding.EncodeToString([]byte(`{"iv":"aXY=","value":"dmFsdWU=","mac":"0123456789abcdef","tag":""}`))
	payload := `{"uuid":"7d3c","displayName":"App\\Jobs\\SendInvoice","data":{"commandName":"App\\Jobs\\SendInvoice","command":"` + cmd + `"}}`
	res := runJob(t, Job{Source: source(batch(table("jobs", "id", "payload"), []string{"1", payload}))})
	f := find(res, core.FindingDataEncrypted)
	if f == nil || f.Locations[0].Column != "payload" {
		t.Fatalf("job payload: %+v", res.Findings)
	}
	plain := `{"uuid":"7d3c","data":{"commandName":"App\\Jobs\\SendInvoice","command":"O:19:\"App\\Jobs\\SendInvoice\":0:{}"}}`
	res = runJob(t, Job{Source: source(batch(table("jobs", "id", "payload"), []string{"1", plain}))})
	if find(res, core.FindingDataEncrypted) != nil {
		t.Errorf("plain job: %v", ids(res))
	}
}

func TestRunURLs(t *testing.T) {
	res := runJob(t, Job{Production: []string{"shop.com"}, Source: source(
		options([]string{"1", "siteurl", "https://shop.com"}, []string{"2", "blogdescription", "Visit https://shop.com/sale or https://shop.com.au"}),
	)})
	if f := find(res, core.FindingURLProductionSiteURL); f == nil || f.Severity != core.SeverityCritical {
		t.Errorf("siteurl: %v", ids(res))
	}
	if f := find(res, core.FindingURLProduction); f == nil || f.Count != 1 {
		t.Errorf("url: %+v", res.Findings)
	}
	// Subscriptions keeps production's URL on purpose; the same URL in
	// another option is still reported.
	res = runJob(t, Job{Production: []string{"shop.com"}, Source: source(
		options([]string{"1", "wc_subscriptions_siteurl", "https://shop.com"}),
	)})
	if len(res.Findings) != 0 {
		t.Errorf("wc_subscriptions_siteurl: %v", ids(res))
	}
	res = runJob(t, Job{Production: []string{"shop.com"}, Source: source(
		options([]string{"1", "wc_subscriptions_siteurl_backup", "https://shop.com"}),
	)})
	if find(res, core.FindingURLProduction) == nil {
		t.Errorf("other option: %v", ids(res))
	}
	res = runJob(t, Job{Production: []string{"shop.com"}, Opts: Options{Skip: []string{"urls"}},
		Source: source(options([]string{"1", "siteurl", "https://shop.com"}))})
	if len(res.Findings) != 0 {
		t.Errorf("skip urls: %v", ids(res))
	}
}

func TestRunUncovered(t *testing.T) {
	members := table("wp_loyalty_members", "id", "email", "notes", "phone")
	var rows [][]string
	for i := range 1000 {
		email := fmt.Sprintf("member%d.kowalczyk@gmail.com", i)
		if i >= 980 {
			email = "n/a"
		}
		note := "called back"
		if i < 50 {
			note = fmt.Sprintf("wrote from member%d.kowalczyk@gmail.com", i)
		}
		rows = append(rows, []string{fmt.Sprint(i), email, note, ""})
	}
	rules := []core.Rule{{Table: "wp_users", Column: "user_email", Action: core.Fake, Fake: core.KEmail}}
	res := runJob(t, Job{Source: source(batch(members, rows...)), Rules: rules, Opts: Options{Skip: []string{"emails"}}})
	want := []core.Uncovered{{Table: "wp_loyalty_members", Column: "email", Rate: 98}, {Table: "wp_loyalty_members", Column: "phone", Rate: 0}}
	if len(res.Uncovered) != 2 || res.Uncovered[0] != want[0] || res.Uncovered[1] != want[1] {
		t.Fatalf("uncovered %+v", res.Uncovered)
	}
	var titles []string
	for _, f := range res.Findings {
		if f.ID == core.FindingUncoveredColumn {
			titles = append(titles, f.Title)
		}
	}
	if len(titles) != 2 || titles[0] != "wp_loyalty_members.email looks personal (98% of 1,000 sampled values are emails) and no rule covers it" {
		t.Errorf("titles %q", titles)
	}
	// A covered column is never listed.
	rules = append(rules, core.Rule{Table: "wp_loyalty_*", Column: "email", Action: core.Fake, Fake: core.KEmail},
		core.Rule{Table: "wp_loyalty_members", Column: "phone", Action: core.Blank})
	res = runJob(t, Job{Source: source(batch(members, rows...)), Rules: rules, Opts: Options{Skip: []string{"emails"}}})
	if len(res.Uncovered) != 0 {
		t.Errorf("covered: %+v", res.Uncovered)
	}
}

// siteFixture is a local WordPress site with a theme holding a live key,
// config files and personal uploads.
func siteFixture(kind core.EndpointKind) *testutil.Site {
	s := testutil.FakeSite(map[string][]byte{
		"wp-config.php": []byte("<?php\ndefine('DB_HOST', 'db.shop.com');\ndefine('AUTH_KEY', 'same-as-production-salt');\n" +
			"define('WP_HOME', 'https://shop.com');\ndefine('STRIPE_SECRET', '" + liveStripe() + "');\n"),
		"wp-content/themes/shop/functions.php":       []byte("<?php\n// settings\n$stripe = '" + liveStripe() + "';\n"),
		"wp-content/themes/shop/style.css":           []byte(liveStripe()),
		"wp-content/plugins/x/test.php":              []byte("<?php $k = '" + testutil.J("sk_", "test_", keyBody("Ab3", 24)) + "';"),
		"wp-content/debug.log":                       []byte("PHP Notice"),
		"wp-content/uploads/gravity_forms/1/a.pdf":   []byte("%PDF"),
		"wp-content/uploads/gravity_forms/index.php": []byte("<?php"),
		"wp-content/updraft/backup_db.gz":            []byte("x"),
		"prod.sql.gz":                                []byte("x"),
		"empty.sql":                                  nil,
	})
	s.EP.Kind = kind
	return s
}

func TestRunSiteFiles(t *testing.T) {
	st := siteFixture(core.LocalSite)
	res := runJob(t, Job{Site: st, Source: source(options([]string{"1", "blogname", "Shop"})),
		Production: []string{"shop.com"}, ProductionPairs: []core.Pair{{Name: "AUTH_KEY", Value: "same-as-production-salt"}, {Name: "DB_HOST", Value: "db.shop.com"}}})
	var code *core.Finding
	for i, f := range res.Findings {
		if f.ID == "key.stripe-live" && f.Locations[0].File == "wp-content/themes/shop/functions.php" {
			code = &res.Findings[i]
		}
	}
	if code == nil || code.Locations[0].Line != 3 {
		t.Fatalf("code key: %+v", res.Findings)
	}
	want := "key.stripe-live: wp-content/themes/shop/functions.php line 3 holds a live Stripe key. Roadie only edits the database, wp-config.php and .env. Move it into wp-config.php or blank it by hand, then run verify."
	if code.Fix != want {
		t.Errorf("fix\n got %s\nwant %s", code.Fix, want)
	}
	manual := 0
	for _, k := range res.Keys {
		if k.Action == "manual" {
			manual++
		}
	}
	if manual != 1 {
		t.Errorf("manual keys %+v", res.Keys)
	}
	for _, id := range []string{core.FindingConfigSalts, core.FindingConfigPointsAtProduction, core.FindingURLProductionSiteURL} {
		if f := find(res, id); f == nil || f.Locations[0].File != "wp-config.php" {
			t.Errorf("no %s from wp-config.php: %v", id, ids(res))
		}
	}
	wpKey := false
	for _, f := range res.Findings {
		if f.ID == "key.stripe-live" && f.Locations[0].File == "wp-config.php" && f.Locations[0].Line == 5 {
			wpKey = true
		}
	}
	if !wpKey {
		t.Errorf("no key from wp-config.php line 5: %+v", res.Findings)
	}
	uploads := map[string]core.Severity{}
	for _, f := range res.Findings {
		if f.ID == core.FindingUploadsPersonal {
			uploads[f.Locations[0].File] = f.Severity
		}
	}
	wantUp := map[string]core.Severity{"wp-content/debug.log": core.SeverityHigh, "wp-content/uploads/gravity_forms": core.SeverityMedium,
		"wp-content/updraft": core.SeverityCritical, "prod.sql.gz": core.SeverityCritical}
	if fmt.Sprint(uploads) != fmt.Sprint(wantUp) {
		t.Errorf("uploads %v, want %v", uploads, wantUp)
	}

	// Over ssh, code files are listed as skipped and never read.
	res = runJob(t, Job{Site: siteFixture(core.SSHSite), Source: source(options([]string{"1", "blogname", "Shop"}))})
	skipped := false
	for _, s := range res.Coverage.Skipped {
		skipped = skipped || s.Table == "code files" && s.Reason == "not read over ssh (PRD 6.13 allowlist)"
	}
	if !skipped {
		t.Errorf("ssh: skipped %+v", res.Coverage.Skipped)
	}
	for _, f := range res.Findings {
		if strings.HasPrefix(f.Locations[0].File, "wp-content/themes") {
			t.Errorf("ssh read a code file: %+v", f)
		}
	}
	// Without production pairs (heuristic mode) salts are not compared.
	res = runJob(t, Job{Site: siteFixture(core.LocalSite), Source: source(options([]string{"1", "blogname", "Shop"})), Opts: Options{Skip: []string{"uploads", "keys"}}})
	if find(res, core.FindingConfigSalts) != nil || find(res, core.FindingUploadsPersonal) != nil || find(res, "key.stripe-live") != nil {
		t.Errorf("heuristic or skipped: %v", ids(res))
	}
}

// The real config parsers, once the site lane lands them.
func TestRunRealConfigParsers(t *testing.T) {
	if _, err := wpconfig.Parse([]byte("<?php\n")); errors.Is(err, core.ErrNotBuilt) {
		t.Skip("needs the site lane")
	}
	st := testutil.FakeSite(map[string][]byte{"wp-config.php": []byte("<?php\ndefine( 'DB_HOST', 'db.shop.com' );\n")})
	res := runJob(t, Job{Site: st, Source: source(options([]string{"1", "blogname", "Shop"})), Production: []string{"shop.com"}})
	if find(res, core.FindingConfigPointsAtProduction) == nil {
		t.Errorf("real wpconfig: %v", ids(res))
	}
}

// The real nested walker, once the values lane lands it.
func TestRunRealNested(t *testing.T) {
	if err := nested.Leaves([]byte(`a:1:{s:1:"a";s:1:"b";}`), func(string, []byte) {}, nested.Options{}); errors.Is(err, core.ErrNotBuilt) {
		t.Skip("needs the values lane")
	}
	formatOf, leavesOf = nested.Format, nested.Leaves
	defer func() { formatOf, leavesOf = fakeFormat, fakeLeaves }()
	res := runJob(t, Job{Source: source(options([]string{"1", "woocommerce_stripe_settings", ser("enabled", "yes", "testmode", "no", "secret_key", liveStripe())}))})
	f := find(res, "key.stripe-live")
	if f == nil || f.Locations[0].Path != "serialized:secret_key" {
		t.Errorf("real nested: %+v", res.Findings)
	}
}

type fakeStep struct {
	id  string
	err error
}

func (s fakeStep) ID() string    { return s.id }
func (s fakeStep) Phase() string { return "after" }
func (s fakeStep) Plan(context.Context, core.Site) ([]core.PlannedEdit, error) {
	return nil, nil
}
func (s fakeStep) Apply(context.Context, core.Site) error { return nil }
func (s fakeStep) Check(context.Context, core.Site) (core.SideEffect, []core.Finding, error) {
	if s.err != nil {
		return core.SideEffect{}, nil, s.err
	}
	return core.SideEffect{ID: s.id, Status: "on"}, []core.Finding{{ID: s.id, Severity: core.SeverityHigh, Title: "Cron is on",
		Locations: []core.LocationCount{{Location: core.Location{File: "wp-config.php"}}}}}, nil
}

func TestRunSteps(t *testing.T) {
	st := testutil.FakeSite(nil)
	steps := []core.Step{fakeStep{id: core.FindingSideEffectCron}, fakeStep{id: core.FindingSideEffectWebhooks, err: errors.New("no table")}}
	res := runJob(t, Job{Site: st, Source: source(options([]string{"1", "blogname", "Shop"})), Steps: steps})
	if len(res.SideEffects) != 2 || res.SideEffects[0].Status != "on" || res.SideEffects[1].Status != "unknown" {
		t.Errorf("side effects %+v", res.SideEffects)
	}
	if find(res, core.FindingSideEffectCron) == nil {
		t.Errorf("findings %v", ids(res))
	}
	if c := checkByID(res, core.FindingSideEffectCron); c.Status != "fail" || c.Checked != 1 {
		t.Errorf("cron check %+v", c)
	}
	if c := checkByID(res, core.FindingSideEffectWebhooks); c.Status != "pass" || c.Checked != 1 {
		t.Errorf("webhooks check %+v", c)
	}
	if c := checkByID(res, core.FindingUploadsPersonal); c.Status != "pass" || c.Checked == 0 {
		t.Errorf("uploads check on a site %+v", c)
	}
	res = runJob(t, Job{Site: st, Source: source(options([]string{"1", "blogname", "Shop"})), Steps: steps, Opts: Options{Skip: []string{"side-effects"}}})
	if len(res.SideEffects) != 0 {
		t.Errorf("skip side-effects: %+v", res.SideEffects)
	}
	if c := checkByID(res, core.FindingSideEffectCron); c.Status != "skip" || c.Detail != "left out with --skip side-effects" {
		t.Errorf("skipped cron check %+v", c)
	}
}

// Exact mode finds a planted original; Roadie's own fakes never hit.
func TestRunExact(t *testing.T) {
	known := testutil.FakeKnown()
	known.Add(core.KEmail, []byte("ada.kowalczyk@gmail.com"))
	known.Seal()
	res := runJob(t, Job{Known: known, Source: source(batch(table("wp_posts", "ID", "post_content"),
		[]string{"1", "Order note from ada.kowalczyk@gmail.com"}))})
	if res.Mode != "exact" || find(res, core.FindingExactSurvivors) == nil {
		t.Errorf("exact: %s %v", res.Mode, ids(res))
	}
}

// No canary value reaches the result, in any mode, through any finding.
func TestRunCanaries(t *testing.T) {
	c := testutil.Canaries(42)
	cols := []string{"id"}
	var kinds []core.FakeKind
	for _, k := range core.FakeKinds() {
		if len(c[k]) > 0 {
			cols = append(cols, string(k))
			kinds = append(kinds, k)
		}
	}
	tbl := table("wp_customers", cols...)
	var rows [][]string
	known := testutil.FakeKnown()
	for i := range testutil.CanaryCount {
		row := []string{fmt.Sprint(i + 1)}
		for _, k := range kinds {
			row = append(row, c[k][i])
			known.Add(k, []byte(c[k][i]))
		}
		rows = append(rows, row)
	}
	known.Seal()
	opts := testutil.J("a:1:{s:10:\"secret_key\";s:32:\"", liveStripe(), "\";}")
	for _, k := range []core.Known{nil, known} {
		res := runJob(t, Job{Known: k, Source: source(batch(tbl, rows...), options([]string{"1", "woocommerce_stripe_settings", opts})),
			Rules: []core.Rule{{Table: "wp_customers", Column: "phone", Fake: core.KPhone, Action: core.Fake}}})
		if len(res.Findings) == 0 {
			t.Fatal("no findings: the canaries were not scanned")
		}
		out, err := json.Marshal(res)
		if err != nil {
			t.Fatal(err)
		}
		for kind, vals := range c {
			for _, v := range vals {
				if bytes.Contains(out, []byte(v)) {
					t.Errorf("canary %s %q in the result", kind, core.MaskValue(kind, v))
				}
			}
		}
		if bytes.Contains(out, []byte(liveStripe())) {
			t.Error("the live key is in the result")
		}
	}
}

func TestEncrypted(t *testing.T) {
	enc := func(s string) []byte { return []byte(base64.StdEncoding.EncodeToString([]byte(s))) }
	if !encrypted(enc(`{"iv":"a","value":"b","mac":"c"}`)) || !encrypted(enc(`{"iv":"a","value":"b","mac":"c","tag":""}`)) {
		t.Error("Laravel payloads not seen")
	}
	for _, v := range [][]byte{enc(`{"iv":"a","value":"b"}`), enc(`{"iv":"a","value":"b","mac":"c","x":1}`), []byte("eyJ not base64 at all, really not"), enc(`[1,2,3,4,5,6,7,8,9,10]`)} {
		if encrypted(v) {
			t.Errorf("%s seen as encrypted", v)
		}
	}
}

func TestHostHelpers(t *testing.T) {
	hosts := productionHosts([]string{"https://shop.com/", "*.shop.de", "Admin.Shop.NL:8443"})
	for h, want := range map[string]bool{"shop.com": true, "www.shop.com": true, "fr.shop.de": true, "shop.de": false,
		"admin.shop.nl": true, "myshop.com": false, "": false} {
		if got := matchHost(hosts, h); got != want {
			t.Errorf("matchHost(%s) = %v", h, got)
		}
	}
	for v, want := range map[string]string{"db.shop.com:3306": "db.shop.com", "localhost:/var/run/mysqld.sock": "localhost",
		"https://shop.com/x": "shop.com", "[::1]:3306": "::1", "DB.Shop.com": "db.shop.com"} {
		if got := hostOf(v); got != want {
			t.Errorf("hostOf(%s) = %s, want %s", v, got, want)
		}
	}
	src := `serialize(array('provider' => 'aws', 'bucket' => "shop-media", 'access-key-id' => 'AKIAX'))`
	if phpArrayValue(src, "bucket") != "shop-media" || phpArrayValue(src, "access-key-id") != "AKIAX" || phpArrayValue(src, "nope") != "" {
		t.Error("phpArrayValue")
	}
}

func TestAllowListsReachDetectors(t *testing.T) {
	users := table("wp_users", "ID", "user_email")
	res := runJob(t, Job{Source: source(batch(users, []string{"1", "info@shop.co.uk"})), Opts: Options{Allow: detect.Allow{Emails: []string{"*@shop.co.uk"}}}})
	if len(notUncovered(res)) != 0 {
		t.Errorf("allow list: %v", ids(res))
	}
}

// The real URL matcher, once the values lane lands it.
func TestRunRealURLs(t *testing.T) {
	saved := newURLFinder
	newURLFinder = realURLFinder
	defer func() { newURLFinder = saved }()
	if newURLFinder([]string{"shop.com"}) == nil {
		t.Skip("needs the values lane")
	}
	res := runJob(t, Job{Production: []string{"shop.com"}, Source: source(options([]string{"1", "siteurl", "https://shop.com"}))})
	if find(res, core.FindingURLProductionSiteURL) == nil {
		t.Errorf("real urlrw: %v", ids(res))
	}
}

// lockedSite fails to list plugins and to read one theme file, as a site
// with unreadable folders does.
type lockedSite struct{ *testutil.Site }

func (s lockedSite) List(ctx context.Context, rel string, depth int) ([]core.FileInfo, error) {
	if rel == "wp-content/plugins" {
		return nil, &fs.PathError{Op: "open", Path: rel, Err: fs.ErrPermission}
	}
	return s.Site.List(ctx, rel, depth)
}

func (s lockedSite) ReadFile(ctx context.Context, rel string) ([]byte, error) {
	if rel == "wp-content/themes/shop/functions.php" {
		return nil, &fs.PathError{Op: "open", Path: rel, Err: fs.ErrPermission}
	}
	return s.Site.ReadFile(ctx, rel)
}

// Code files verify couldn't list or read are a coverage gap, not a pass;
// a missing themes or plugins folder is not.
func TestRunCodeFilesUnreadable(t *testing.T) {
	res := runJob(t, Job{Site: lockedSite{siteFixture(core.LocalSite)}, Source: source(options([]string{"1", "blogname", "Shop"}))})
	want := []core.Skipped{
		{Table: "code files", Reason: "1 file in wp-content/themes not read; first wp-content/themes/shop/functions.php: open wp-content/themes/shop/functions.php: permission denied"},
		{Table: "code files", Reason: "wp-content/plugins not listed: open wp-content/plugins: permission denied"},
	}
	var got []core.Skipped
	for _, s := range res.Coverage.Skipped {
		if s.Table == "code files" {
			got = append(got, s)
		}
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("skipped %+v\nwant %+v", got, want)
	}

	bare := testutil.FakeSite(map[string][]byte{"wp-config.php": []byte("<?php\n")})
	res = runJob(t, Job{Site: bare, Source: source(options([]string{"1", "blogname", "Shop"}))})
	for _, s := range res.Coverage.Skipped {
		if s.Table == "code files" {
			t.Errorf("no code folders: %+v", s)
		}
	}
}

// A serialized-looking value that doesn't parse is read as text and its
// column is listed under coverage; data.unparseable is copy's finding.
func TestRunUnparsedNoted(t *testing.T) {
	broken := `a:2:{s:5:"email";s:99:"ada.kowalczyk@gmail.com";}`
	res := runJob(t, Job{Source: source(options(
		[]string{"1", "widget_contact", broken},
		[]string{"2", "widget_text", broken},
		[]string{"3", "blogname", "a:b; not serialized at all"},
		[]string{"4", "shop_settings", ser("enabled", "yes")},
	))})
	want := []core.Skipped{{Table: "wp_options.option_value",
		Reason: "2 serialized-looking values didn't parse; read as plain text, so data nested inside may be missed"}}
	if fmt.Sprint(res.Coverage.Skipped) != fmt.Sprint(want) {
		t.Errorf("skipped %+v, want %+v", res.Coverage.Skipped, want)
	}
	if f := find(res, core.FindingPIIEmail); f == nil || f.Count != 2 {
		t.Errorf("the text read missed the email: %v", ids(res))
	}
	if find(res, core.FindingDataUnparseable) != nil {
		t.Errorf("verify reported data.unparseable: %v", ids(res))
	}
	res = runJob(t, Job{Source: source(options([]string{"1", "shop_settings", ser("enabled", "yes")}))})
	if len(res.Coverage.Skipped) != 0 {
		t.Errorf("a parsed value was noted: %+v", res.Coverage.Skipped)
	}
}

func TestLooksSerialized(t *testing.T) {
	for v, want := range map[string]bool{
		`a:1:{s:1:"a";s:1:"b";}`: true,
		`a:2:{broken`:            false,
		`O:8:"stdClass":0:{}`:    true,
		`s:5:"hello";`:           true,
		` i:42; `:                true,
		`b:1;`:                   true,
		`N;`:                     true,
		`s:5:"hello"}`:           false,
		`a:b; not serialized`:    false,
		`x:1;`:                   false,
		`a:`:                     false,
		`{"a":1}`:                false,
		`Time: 10:30; see you`:   false,
		"":                       false,
	} {
		if got := looksSerialized([]byte(v)); got != want {
			t.Errorf("looksSerialized(%q) = %v, want %v", v, got, want)
		}
	}
}

func FuzzLooksSerialized(f *testing.F) {
	for _, s := range []string{`a:1:{s:1:"a";s:1:"b";}`, `s:5:"hello";`, "N;", " i:", "s:\"", ""} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		if looksSerialized(b) && len(bytes.TrimSpace(b)) < 2 {
			t.Errorf("%q is too short to be serialized", b)
		}
	})
}

// A walk that fails after some leaves leaves the value to the plain-text
// read; the leaves seen before the failure are not counted as well.
func TestRunNestedFailsPartway(t *testing.T) {
	defer func(f func([]byte) string, l func([]byte, func(string, []byte), nested.Options) error) {
		formatOf, leavesOf = f, l
	}(formatOf, leavesOf)
	formatOf = func([]byte) string { return "serialized" }
	leavesOf = func(b []byte, fn func(string, []byte), _ nested.Options) error {
		fn("serialized:email", []byte("ada.kowalczyk@gmail.com"))
		return errors.New("truncated")
	}
	broken := `a:2:{s:5:"email";s:23:"ada.kowalczyk@gmail.com";s:3:"bad`
	res := runJob(t, Job{Source: source(options([]string{"1", "widget_contact", broken}))})
	f := find(res, core.FindingPIIEmail)
	if f == nil || f.Count != 1 || f.Locations[0].Path != "" {
		t.Errorf("partial walk: %+v", res.Findings)
	}
	// The same walk that succeeds reports the leaf at its path.
	leavesOf = func(b []byte, fn func(string, []byte), _ nested.Options) error {
		fn("serialized:email", []byte("ada.kowalczyk@gmail.com"))
		return nil
	}
	res = runJob(t, Job{Source: source(options([]string{"1", "widget_contact", broken}))})
	if f := find(res, core.FindingPIIEmail); f == nil || f.Count != 1 || f.Locations[0].Path != "serialized:email" {
		t.Errorf("whole walk: %+v", res.Findings)
	}
}

// A staging copy on production's server with production's database
// name is config.points-at-production, at the DB_NAME line.
func TestRunSameLocalDatabase(t *testing.T) {
	cfg := []byte("<?php\ndefine( 'DB_NAME', 'shop_wp' );\ndefine( 'DB_HOST', 'localhost' );\n")
	prod := []core.Pair{{Name: "DB_NAME", Value: "shop_wp"}, {Name: "DB_HOST", Value: "localhost"}}
	st := testutil.FakeSite(map[string][]byte{"wp-config.php": cfg})
	res := runJob(t, Job{Site: st, Source: source(options([]string{"1", "blogname", "Shop"})), ProductionPairs: prod})
	f := find(res, core.FindingConfigPointsAtProduction)
	if f == nil || f.Title != "DB_NAME is production's on the same local DB_HOST" || f.Locations[0].Line != 2 || f.Severity != core.SeverityCritical {
		t.Fatalf("same local database: %+v", res.Findings)
	}
	st = testutil.FakeSite(map[string][]byte{"wp-config.php": []byte("<?php\ndefine( 'DB_NAME', 'shop_staging' );\ndefine( 'DB_HOST', 'localhost' );\n")})
	res = runJob(t, Job{Site: st, Source: source(options([]string{"1", "blogname", "Shop"})), ProductionPairs: prod})
	if find(res, core.FindingConfigPointsAtProduction) != nil {
		t.Errorf("own database flagged: %+v", res.Findings)
	}
	res = runJob(t, Job{Site: testutil.FakeSite(map[string][]byte{"wp-config.php": cfg}), Source: source(options([]string{"1", "blogname", "Shop"})),
		ProductionPairs: prod, Opts: Options{Skip: []string{"config"}}})
	if find(res, core.FindingConfigPointsAtProduction) != nil {
		t.Errorf("skipped config still checked: %+v", res.Findings)
	}
}
