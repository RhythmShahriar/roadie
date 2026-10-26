// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package engine

import (
	"bytes"
	"context"
	"crypto/md5" // #nosec G501 -- checks Action Scheduler's lookup hash
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/codec/nested"
	"github.com/RhythmShahriar/roadie/codec/phpser"
	"github.com/RhythmShahriar/roadie/codec/urlrw"
	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/detect"
	"github.com/RhythmShahriar/roadie/internal/testutil"
	"github.com/RhythmShahriar/roadie/pseudo"
	"github.com/RhythmShahriar/roadie/roadiecfg"
)

// run runs a fixture pipeline and fails the test on an error.
func run(t *testing.T, p *pipeline) *output {
	t.Helper()
	out, err := p.run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// actionsTable is a generic table with a column per rule under test.
func actionsTable() []tableData {
	tb := table("t", "id:int(11)", "kind", "e", "b", "n", "nn", "f", "k", "s", "u", "pw", "j", "domain")
	tb.Columns[4].Nullable = true // n takes NULL, nn doesn't
	tb.PrimaryKey = []string{"id"}
	return []tableData{{tb, rows{
		{1, "row", "anna.lee@example.com", "secret", "x", "x", "x", "keep me " + liveKey, "mail anna.lee@example.com", "https://shop.com/a", "$P$Bhash",
			`{"billing":{"email":"anna.lee@example.com","name":"Anna"},"note":"ben@example.com"}`, "shop.com"},
		{2, "drop", "a@example.com", "", nil, "", "", "", "", "", "", "", ""},
		{3, "row", "", "", "", "", "", "", "", "", "", "", ""},
	}}}
}

// Every action of a columns: rule, on a generic SQL table.
func TestTransformActions(t *testing.T) {
	useFakes(t)
	cfg := roadiecfg.Default()
	cfg.Stack = "sql"
	cfg.Production = []string{"https://shop.com"}
	cfg.Keys.TestKeys = map[string]string{"stripe": testKey}
	for _, c := range [][3]string{{"e", "email"}, {"b", "blank"}, {"n", "null"}, {"nn", "null"}, {"f", "fixed:paused"}, {"k", "keep"},
		{"s", "text"}, {"u", "rewrite-url"}, {"pw", "password"}, {"j", "json:billing.email=email"}, {"domain", "rewrite-url"}} {
		cfg.Columns = append(cfg.Columns, roadiecfg.ColumnRule{Table: "t", Column: c[0], Rule: c[1]})
	}
	cfg.Columns = append(cfg.Columns, roadiecfg.ColumnRule{Table: "t", Column: "kind", Where: map[string]string{"kind": "drop"}, Rule: "delete-row"})
	p, sink, _ := fixture(t, Copy, "", actionsTable, cfg, Options{URL: "https://staging.shop.test"})
	out := run(t, p)
	email, _ := hashFaker{key: string(make([]byte, 32))}.Fake(core.KEmail, []byte("anna.lee@example.com"), core.FakeOpts{})
	want := map[string]string{
		"e": string(email), "b": "", "f": "paused", "k": "keep me " + testKey, "s": "mail " + string(email),
		"u": "https://staging.shop.test/a", "pw": "$P$roadieRESET", "domain": "staging.shop.test",
	}
	for col, w := range want {
		if got, _ := valueOf(t, sink, "t", 0, col); got != w {
			t.Errorf("%s: %q, want %q", col, got, w)
		}
	}
	if _, notNull := valueOf(t, sink, "t", 0, "n"); notNull {
		t.Error("n isn't NULL")
	}
	if got, notNull := valueOf(t, sink, "t", 0, "nn"); !notNull || got != "" {
		t.Errorf("a NOT NULL column got NULL or %q", got)
	}
	// The path rule fakes billing.email; the other leaves are no rule's
	// and get uncovered-values: the billing name (a person's name under
	// billing) and the address in note.
	var j struct {
		Billing map[string]string `json:"billing"`
		Note    string            `json:"note"`
	}
	got, _ := valueOf(t, sink, "t", 0, "j")
	if err := json.Unmarshal([]byte(got), &j); err != nil {
		t.Fatalf("j %q: %v", got, err)
	}
	if j.Billing["email"] != string(email) || j.Billing["name"] == "Anna" || j.Billing["name"] == "" || j.Note == "ben@example.com" || !strings.HasSuffix(j.Note, "@example.invalid") {
		t.Errorf("path rule: %s", got)
	}
	if v, _ := valueOf(t, sink, "t", 1, "e"); v != "<dropped>" {
		t.Errorf("delete-row kept row 2: %q", v)
	}
	if v, _ := valueOf(t, sink, "t", 2, "e"); v != "" {
		t.Errorf("an empty value got a fake: %q", v)
	}
	if len(out.Result.Keys) != 1 || out.Result.Keys[0].Action != "swapped" || out.Result.Keys[0].At.Table != "t" ||
		out.Result.Keys[0].At.Column != "k" || out.Result.Keys[0].At.Row != "id=1" {
		t.Errorf("keys %+v", out.Result.Keys)
	}
	changes := map[string]core.Change{}
	for _, c := range out.Result.Changes {
		changes[c.Rule+" "+c.Column] = c
	}
	if c := changes["config.t.e e"]; c.Values != 1 || c.Kept != 1 || c.Action != core.Fake {
		t.Errorf("email change %+v", c)
	}
	if c := changes["urls u"]; c.Values != 1 || c.Action != core.RewriteURL {
		t.Errorf("url change %+v (%v)", c, out.Result.Changes)
	}
	if c := changes["config.t.kind.drop kind"]; c.Rows != 1 {
		t.Errorf("delete-row change %+v", c)
	}
	// keys.mode blank blanks the live key whatever test key there is.
	cfg.Keys.Mode = "blank"
	p, sink, _ = fixture(t, Copy, "", actionsTable, cfg, Options{URL: "https://staging.shop.test"})
	run(t, p)
	if got, _ := valueOf(t, sink, "t", 0, "k"); got != "keep me " {
		t.Errorf("blank mode: %q", got)
	}
}

// A rule that replaces the whole value wins over a path rule of another
// where clause on the same row (the path rule's exact where sorts first);
// on a row only the path rule matches, the path rule still runs.
func TestWholeRuleBeatsPathRule(t *testing.T) {
	useFakes(t)
	cfg := roadiecfg.Default()
	cfg.Stack = "sql"
	cfg.Columns = []roadiecfg.ColumnRule{
		{Table: "t", Column: "j", Where: map[string]string{"kind": "row"}, Rule: "json:billing.email=email"},
		{Table: "t", Column: "j", Where: map[string]string{"kind": "r*"}, Rule: "blank"},
		{Table: "t", Column: "j", Where: map[string]string{"kind": "path"}, Rule: "json:billing.email=email"},
	}
	tables := func() []tableData {
		tb := table("t", "id:int(11)", "kind", "j")
		tb.PrimaryKey = []string{"id"}
		js := `{"billing":{"email":"anna.lee@example.com"},"note":"x"}`
		return []tableData{{tb, rows{{1, "row", js}, {2, "path", js}}}}
	}
	p, sink, _ := fixture(t, Copy, "", tables, cfg, Options{})
	run(t, p)
	if got, _ := valueOf(t, sink, "t", 0, "j"); got != "" {
		t.Errorf("the blank rule lost to the path rule: %q", got)
	}
	email, _ := hashFaker{key: string(make([]byte, 32))}.Fake(core.KEmail, []byte("anna.lee@example.com"), core.FakeOpts{})
	if got, _ := valueOf(t, sink, "t", 1, "j"); !strings.Contains(got, string(email)) || strings.Contains(got, "anna.lee") {
		t.Errorf("the path rule alone: %q", got)
	}
}

// The default fields of Contact Form 7 (your-name, your-email) and of
// Fluent Forms (names, email, phone) get their own rules: the values are
// faked rather than scrubbed; the names reach Known for the free text.
func TestFormDefaultFields(t *testing.T) {
	f := useFakes(t)
	tables := func() []tableData {
		pm := table("wp_postmeta", "meta_id:bigint(20)", "post_id:bigint(20)", "meta_key", "meta_value")
		sub := table("wp_fluentform_submissions", "id:bigint(20)", "form_id:bigint(20)", "response")
		det := table("wp_fluentform_entry_details", "id:bigint(20)", "submission_id:bigint(20)", "field_name", "sub_field_name", "field_value")
		return []tableData{wpBase([]any{4, "wpcf7", "a:0:{}"}),
			{pm, rows{{1, 9, "_field_your-name", "Anna Lee"}, {2, 9, "_field_your-email", "anna.lee@example.com"}, {3, 9, "_field_your-message", "hi"}}},
			{sub, rows{{1, 1, `{"names":{"first_name":"Bea","last_name":"Stone"},"email":"bea@example.com","message":"x"}`}}},
			{det, rows{{1, 1, "names", "first_name", "Bea"}, {2, 1, "email", nil, "bea@example.com"}, {3, 1, "message", nil, "x"}}},
		}
	}
	p, sink, _ := fixture(t, Copy, "wp_", tables, nil, Options{})
	out := run(t, p)
	fake := func(k core.FakeKind, v string) string {
		b, _ := hashFaker{key: string(make([]byte, 32))}.Fake(k, []byte(v), core.FakeOpts{})
		return string(b)
	}
	for _, c := range []struct {
		table string
		row   int
		col   string
		want  string
	}{
		{"wp_postmeta", 0, "meta_value", fake(core.KFullName, "Anna Lee")},
		{"wp_postmeta", 1, "meta_value", fake(core.KEmail, "anna.lee@example.com")},
		{"wp_postmeta", 2, "meta_value", "hi"},
		{"wp_fluentform_entry_details", 0, "field_value", fake(core.KFirstName, "Bea")},
		{"wp_fluentform_entry_details", 1, "field_value", fake(core.KEmail, "bea@example.com")},
		{"wp_fluentform_entry_details", 2, "field_value", "x"},
	} {
		if got, _ := valueOf(t, sink, c.table, c.row, c.col); got != c.want {
			t.Errorf("%s row %d: %q, want %q", c.table, c.row, got, c.want)
		}
	}
	got, _ := valueOf(t, sink, "wp_fluentform_submissions", 0, "response")
	for _, w := range []string{fake(core.KFirstName, "Bea"), fake(core.KLastName, "Stone"), fake(core.KEmail, "bea@example.com"), `"message":"x"`} {
		if !strings.Contains(got, w) {
			t.Errorf("response %s lacks %s", got, w)
		}
	}
	for _, name := range []string{"Anna Lee", "Bea", "Stone"} {
		if _, _, ok := f.known.Contains([]byte(name)); !ok {
			t.Errorf("%s isn't in Known", name)
		}
	}
	if !slices.Contains(out.Result.Stack.Profiles, "fluent-forms") || !slices.Contains(out.Result.Stack.Profiles, "contact-form-7-flamingo") {
		t.Errorf("profiles %v", out.Result.Stack.Profiles)
	}
}

// wpBase is the options table every WordPress fixture needs.
func wpBase(extra ...[]any) tableData {
	rs := rows{{1, "siteurl", "https://shop.com"}, {2, "home", "https://shop.com"}, {3, "woocommerce_version", "9.8.0"}}
	return tableData{table("wp_options", "option_id:bigint(20)", "option_name", "option_value"), append(rs, extra...)}
}

// asJSON builds Action Scheduler args JSON of exactly n bytes holding an
// email (n > 40) or none.
func asJSON(n int, email string) string {
	head := `{"email":"` + email + `","pad":"`
	if email == "" {
		head = `{"pad":"`
	}
	return head + strings.Repeat("p", n-len(head)-2) + `"}`
}

func md5hex(s string) string { sum := md5.Sum([]byte(s)); return hex.EncodeToString(sum[:]) } // #nosec G401 -- test

// Action Scheduler args move between args and extended_args as the JSON
// crosses 191 bytes, with args holding the md5 when it is long.
func TestActionSchedulerArgs(t *testing.T) {
	useFakes(t)
	small := asJSON(60, "a@ex.com")
	exact := asJSON(191, "")
	long := asJSON(300, "anna.lee@example.com")
	crossing := asJSON(185, "a@ex.com") // the fake email is longer: over 191 after
	tables := func() []tableData {
		return []tableData{wpBase(), {table("wp_actionscheduler_actions", "action_id:bigint(20)", "hook", "args:varchar(191)", "extended_args:varchar(8000)"), rows{
			{1, "h", small, nil}, {2, "h", exact, nil}, {3, "h", md5hex(long), long}, {4, "h", crossing, nil},
		}}}
	}
	for _, onePass := range []bool{false, true} {
		t.Run(fmt.Sprintf("one-pass=%v", onePass), func(t *testing.T) {
			p, sink, _ := fixture(t, Copy, "wp_", tables, nil, Options{OnePass: onePass})
			run(t, p)
			checkActionSchedulerArgs(t, sink, small, exact, long, crossing)
		})
	}
}

func checkActionSchedulerArgs(t *testing.T, sink *testutil.Sink, small, exact, long, crossing string) {
	t.Helper()
	get := func(row int) (string, string, bool) {
		a, _ := valueOf(t, sink, "wp_actionscheduler_actions", row, "args")
		e, notNull := valueOf(t, sink, "wp_actionscheduler_actions", row, "extended_args")
		return a, e, notNull
	}
	if a, _, ext := get(0); ext || len(a) > asArgsMax || !strings.Contains(a, "@example.invalid") {
		t.Errorf("small: args %q ext %v", a, ext)
	}
	if a, _, ext := get(1); ext || a != exact {
		t.Errorf("191 bytes untouched: args %q ext %v", a, ext)
	}
	if a, e, ext := get(2); !ext || a != md5hex(e) || strings.Contains(e, "anna.lee") || len(e) <= asArgsMax {
		t.Errorf("long: args %q ext %q", a, e)
	}
	if a, e, ext := get(3); !ext || len(e) <= asArgsMax || a != md5hex(e) || !strings.Contains(e, "@example.invalid") {
		t.Errorf("crossing: args %q ext %q", a, e)
	}
	for _, b := range sink.Batches() {
		if b.Table != nil && b.Table.Name == "wp_actionscheduler_actions" && (b.Rows[1].Values[2].Dirty || b.Rows[1].Values[3].Dirty) {
			t.Error("the untouched row is dirty")
		}
	}
}

// billing holds an order's billing parts in WooCommerce's order.
var billing = []string{"Anna", "Lee", "", "1 High Street", "", "Leeds", "WYK", "LS1 4AP", "GB", "anna.lee@example.com", "+44 7700 900123"}

// In HPOS compatibility mode the order is in the posts tables and the
// HPOS tables; both get identical fakes; the address index in each
// is rebuilt from the fake parts.
func TestHPOSCompatibility(t *testing.T) {
	useFakes(t)
	index := strings.Join(billing, " ")
	tables := func() []tableData {
		pm := rows{}
		for i, part := range addrParts[true] {
			pm = append(pm, []any{i + 1, 10, "_billing_" + part, billing[i]})
		}
		pm = append(pm, []any{50, 10, "_billing_address_index", index})
		addr := []any{1, 10, "billing"}
		for _, v := range billing {
			addr = append(addr, v)
		}
		return []tableData{
			wpBase([]any{4, "woocommerce_custom_orders_table_enabled", "yes"}, []any{5, "woocommerce_custom_orders_table_data_sync_enabled", "yes"}),
			{table("wp_postmeta", "meta_id:bigint(20)", "post_id:bigint(20)", "meta_key", "meta_value:longtext"), pm},
			{table("wp_wc_order_addresses", append([]string{"id:bigint(20)", "order_id:bigint(20)", "address_type"}, addrParts[true]...)...), rows{addr}},
			{table("wp_wc_orders", "id:bigint(20)", "customer_id:bigint(20)", "billing_email"), rows{{10, 0, billing[9]}}},
			{table("wp_wc_orders_meta", "id:bigint(20)", "order_id:bigint(20)", "meta_key", "meta_value:longtext"), rows{{1, 10, "_billing_address_index", index}}},
		}
	}
	p, sink, _ := fixture(t, Copy, "wp_", tables, nil, Options{})
	out := run(t, p)
	if !out.Result.Stack.HPOS || !slices.Contains(out.Result.Stack.Profiles, "woocommerce-hpos") {
		t.Fatalf("stack %+v", out.Result.Stack)
	}
	var fakes []string
	for i, part := range addrParts[true] {
		pmv, _ := valueOf(t, sink, "wp_postmeta", i, "meta_value")
		hv, _ := valueOf(t, sink, "wp_wc_order_addresses", 0, part)
		if pmv != hv {
			t.Errorf("%s: postmeta %q, HPOS %q", part, pmv, hv)
		}
		if billing[i] != "" && (part == "first_name" || part == "email" || part == "city") && pmv == billing[i] {
			t.Errorf("%s not faked", part)
		}
		if (part == "country" || part == "state") && pmv != billing[i] {
			t.Errorf("%s changed: %q", part, pmv)
		}
		fakes = append(fakes, pmv)
	}
	if v, _ := valueOf(t, sink, "wp_wc_orders", 0, "billing_email"); v != fakes[9] {
		t.Errorf("wc_orders.billing_email %q, address %q", v, fakes[9])
	}
	want := strings.Join(fakes, " ")
	for _, tb := range []string{"wp_postmeta", "wp_wc_orders_meta"} {
		row := 0
		if tb == "wp_postmeta" {
			row = len(addrParts[true])
		}
		if v, _ := valueOf(t, sink, tb, row, "meta_value"); v != want {
			t.Errorf("%s index %q, want %q", tb, v, want)
		}
	}
	// --one-pass has no part lengths (the reader fills them while the
	// workers run): the index keeps its own text rule, which still takes
	// the email out; -race sees no shared map.
	p, sink, _ = fixture(t, Copy, "wp_", tables, nil, Options{OnePass: true})
	run(t, p)
	for _, tb := range []string{"wp_postmeta", "wp_wc_orders_meta"} {
		row := 0
		if tb == "wp_postmeta" {
			row = len(addrParts[true])
		}
		if v, _ := valueOf(t, sink, tb, row, "meta_value"); strings.Contains(v, billing[9]) {
			t.Errorf("one pass: %s index kept the email: %q", tb, v)
		}
	}
}

// A kept user (keep_users) is left as is in users, usermeta and orders,
// counted once as info.kept-users; --admin-password-env sets their
// password; their values never count as survivors.
func TestKeptUsers(t *testing.T) {
	f := useFakes(t)
	oldEnv := lookupEnv
	lookupEnv = func(n string) (string, bool) { return "s3cret-pass", n == "ROADIE_ADMIN_PW" }
	t.Cleanup(func() { lookupEnv = oldEnv })
	tables := func() []tableData {
		return []tableData{
			wpBase(),
			{table("wp_usermeta", "umeta_id:bigint(20)", "user_id:bigint(20)", "meta_key", "meta_value:longtext"), rows{
				{1, 1, "first_name", "Anna"}, {2, 2, "first_name", "Ben"}, {3, 2, "wp_capabilities", `a:1:{s:13:"administrator";b:1;}`},
				{4, 3, "wp_capabilities", `a:1:{s:13:"administrator";b:1;}`}, {5, 2, "session_tokens", "a:0:{}"}}},
			{table("wp_users", "ID:bigint(20)", "user_login", "user_email", "user_pass"), rows{
				{1, "anna", "anna.lee@example.com", "$P$Banna"}, {2, "ben", "ben@example.com", "$P$Bben"}, {3, "cat", "cat@example.com", "$P$Bcat"}}},
			{table("wp_wc_order_addresses", "id:bigint(20)", "order_id:bigint(20)", "address_type", "first_name", "email"), rows{
				{1, 10, "billing", "Anna", "anna.lee@example.com"}, {2, 11, "billing", "Ben", "ben@example.com"}}},
			{table("wp_wc_orders", "id:bigint(20)", "customer_id:bigint(20)", "billing_email"), rows{{10, 1, "anna.lee@example.com"}, {11, 2, "ben@example.com"}}},
			{table("wp_wc_orders_meta", "id:bigint(20)", "order_id:bigint(20)", "meta_key", "meta_value:longtext"), rows{
				{1, 10, "_billing_address_index", "Anna anna.lee@example.com"}, {2, 11, "_billing_address_index", "Ben ben@example.com"}}},
		}
	}
	cfg := roadiecfg.Default()
	cfg.Pseudonymise.KeepUsers = []string{"ANNA"}
	p, sink, _ := fixture(t, Copy, "wp_", tables, cfg, Options{AdminPasswordEnv: "ROADIE_ADMIN_PW"})
	out := run(t, p)
	for _, c := range []struct {
		table string
		row   int
		col   string
		want  string
	}{{"wp_users", 0, "user_login", "anna"}, {"wp_users", 0, "user_email", "anna.lee@example.com"}, {"wp_users", 0, "user_pass", "$P$hash-of-11"},
		{"wp_usermeta", 0, "meta_value", "Anna"}, {"wp_wc_orders", 0, "billing_email", "anna.lee@example.com"},
		{"wp_wc_order_addresses", 0, "email", "anna.lee@example.com"}, {"wp_wc_order_addresses", 0, "first_name", "Anna"},
		{"wp_wc_orders_meta", 0, "meta_value", "Anna anna.lee@example.com"}} {
		if got, _ := valueOf(t, sink, c.table, c.row, c.col); got != c.want {
			t.Errorf("kept %s.%s: %q, want %q", c.table, c.col, got, c.want)
		}
	}
	if got, _ := valueOf(t, sink, "wp_users", 1, "user_email"); got == "ben@example.com" {
		t.Error("ben wasn't faked")
	}
	if got, _ := valueOf(t, sink, "wp_wc_order_addresses", 1, "email"); got == "ben@example.com" {
		t.Error("ben's order address wasn't faked")
	}
	if got, _ := valueOf(t, sink, "wp_usermeta", 4, "meta_value"); got != "<dropped>" {
		t.Errorf("session_tokens row kept: %q", got)
	}
	benLogin, _ := valueOf(t, sink, "wp_users", 1, "user_login")
	if out.Result.KeptUsers != 1 || out.Result.FirstAdmin != benLogin || benLogin == "ben" {
		t.Errorf("kept %d, first admin %q (ben is %q)", out.Result.KeptUsers, out.Result.FirstAdmin, benLogin)
	}
	i := slices.IndexFunc(out.Result.Findings, func(f core.Finding) bool { return f.ID == core.FindingInfoKeptUsers })
	if i < 0 || out.Result.Findings[i].Count != 1 || out.Result.Findings[i].Locations[0].Table != "wp_users" {
		t.Errorf("findings %+v", out.Result.Findings)
	}
	// usermeta comes before users, so Anna's first name reached Known in
	// pass 1; the run's Known still doesn't count it.
	if _, _, ok := f.known.Contains([]byte("Anna")); !ok {
		t.Fatal("fixture: Anna isn't in the inner Known")
	}
	if _, _, ok := out.Known.Contains([]byte("anna")); ok {
		t.Error("a kept user's value counts as a survivor")
	}
	if _, _, ok := out.Known.Contains([]byte("Ben")); !ok {
		t.Error("Known lost a pseudonymised value")
	}
	// The password env var must be a ROADIE_ one and set.
	for _, name := range []string{"ADMIN_PW", "ROADIE_UNSET"} {
		p, _, _ := fixture(t, Copy, "wp_", tables, cfg, Options{AdminPasswordEnv: name})
		if _, err := p.run(context.Background()); !isCode(err, core.ErrConfig) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// A serialized-looking value that doesn't parse and holds an email is
// blanked and reported (on_unparseable: blank), or stops the run (fail).
// One without personal-looking text is left alone.
func TestUnparseable(t *testing.T) {
	useFakes(t)
	broken := `a:1:{s:5:"email";s:3:"anna.lee@example.com";}`
	tables := func() []tableData {
		return []tableData{wpBase([]any{4, "widget_contact", broken}, []any{5, "widget_other", `a:1:{s:1:"x";s:9:"y";}`})}
	}
	p, sink, _ := fixture(t, Copy, "wp_", tables, nil, Options{})
	out := run(t, p)
	if v, _ := valueOf(t, sink, "wp_options", 3, "option_value"); v != "" {
		t.Errorf("not blanked: %q", v)
	}
	if v, _ := valueOf(t, sink, "wp_options", 4, "option_value"); v != `a:1:{s:1:"x";s:9:"y";}` {
		t.Errorf("changed without PII: %q", v)
	}
	i := slices.IndexFunc(out.Result.Findings, func(f core.Finding) bool { return f.ID == core.FindingDataUnparseable })
	if i < 0 || out.Result.Findings[i].Severity != core.SeverityMedium || strings.Contains(out.Result.Findings[i].Title, "anna") {
		t.Errorf("findings %+v", out.Result.Findings)
	}
	cfg := roadiecfg.Default()
	cfg.Pseudonymise.OnUnparseable = "fail"
	p, _, _ = fixture(t, Copy, "wp_", tables, cfg, Options{})
	_, err := p.run(context.Background())
	var ce *core.Error
	if !isCode(err, core.FindingDataUnparseable) || !asError(err, &ce) || ce.Exit != 3 || strings.Contains(ce.Msg, "anna") ||
		!strings.Contains(ce.Msg, "at wp_options widget_contact looks serialized") {
		t.Errorf("fail: %v", err)
	}
	if !core.KnownID(core.FindingDataUnparseable) {
		t.Error("unknown ID")
	}
}

// wc_subscriptions_siteurl is never rewritten: the mismatch is what puts
// Subscriptions in staging mode.
func TestSubscriptionsSiteURL(t *testing.T) {
	useFakes(t)
	clone := "https://shop.com/_[wc_subscriptions_siteurl]_"
	tables := func() []tableData {
		return []tableData{wpBase([]any{4, "wc_subscriptions_siteurl", clone}, []any{5, "other", "https://shop.com/x"})}
	}
	// --one-pass knows the prefix only from the batch's rules; the clone
	// URL must stay all the same.
	for _, onePass := range []bool{false, true} {
		p, sink, _ := fixture(t, Copy, "wp_", tables, nil, Options{URL: "https://staging.shop.test", OnePass: onePass})
		p.cfg.Production = []string{"shop.com"}
		run(t, p)
		if v, _ := valueOf(t, sink, "wp_options", 3, "option_value"); v != clone {
			t.Errorf("one pass %v: wc_subscriptions_siteurl %q", onePass, v)
		}
		if v, _ := valueOf(t, sink, "wp_options", 4, "option_value"); v != "https://staging.shop.test/x" {
			t.Errorf("one pass %v: other option %q", onePass, v)
		}
	}
}

// Two runs with one key give byte-identical output and the same result,
// with eight workers and many batches.
func TestDeterminism(t *testing.T) {
	useFakes(t)
	runOnce := func() ([]byte, []byte) {
		p, sink := genPipeline(t, 200, 0, sqlConfig("text"))
		in := p.in.(*funcInput)
		in.fn = func(int) (core.Source, error) {
			g := newGenSource(200, 0)
			g.value = func(i int) []byte {
				return []byte("mail anna" + string(rune('a'+i%26)) + "@example.com or https://shop.com/" + liveKey)
			}
			return g, nil
		}
		p.cfg.Production = []string{"shop.com"}
		p.cfg.Keys.TestKeys = map[string]string{"stripe": testKey}
		p.opts.URL = "https://staging.shop.test"
		out := run(t, p)
		res, err := json.Marshal(out.Result)
		if err != nil {
			t.Fatal(err)
		}
		return sink.Bytes(), res
	}
	b1, r1 := runOnce()
	b2, r2 := runOnce()
	if !bytes.Equal(b1, b2) || !bytes.Equal(r1, r2) {
		t.Error("two runs differ")
	}
	if bytes.Contains(b1, []byte("anna")) || bytes.Contains(b1, []byte("shop.com/")) || bytes.Contains(b1, []byte(liveKey)) {
		t.Errorf("output keeps production data: %.200s", b1)
	}
}

func asError(err error, target **core.Error) bool {
	ce, ok := err.(*core.Error) //nolint:errorlint // the run returns the *core.Error itself
	if ok {
		*target = ce
	}
	return ok
}

func TestLooksSerialized(t *testing.T) {
	for in, want := range map[string]bool{`a:1:{i:0;s:1:"x";}`: true, `s:3:"abc";`: true, `i:5;`: true, `b:1;`: true, `N;`: true,
		`O:8:"stdClass":0:{}`: true, "hello": false, "a:b": false, `{"a":1}`: false, "": false} {
		if got := looksSerialized([]byte(in)); got != want {
			t.Errorf("%q: %v", in, got)
		}
	}
}

// A fake fits the column's byte limit and says whether the column is
// unique; a leaf inside a structure has no limit of its own.
func TestApplyFakeOpts(t *testing.T) {
	var got []core.FakeOpts
	tr := newTransformer(&shared{cfg: roadiecfg.Default()}, optsFaker{&got})
	col := core.Column{Name: "user_login", Type: "varchar(60)", MaxLen: 60, Unique: true}
	if _, _, _, err := tr.apply(core.Rule{Action: core.Fake, Fake: core.KUsername}, []byte("anna"), col); err != nil {
		t.Fatal(err)
	}
	if _, _, err := tr.applyLeaf(core.Rule{Action: core.Fake, Fake: core.KEmail, Path: "json:a"}, []byte("a@example.com"), col); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].MaxLen != 60 || !got[0].Unique || got[0].Locale != "en" || got[1].MaxLen != 0 {
		t.Errorf("opts %+v", got)
	}
}

// optsFaker records the options it is called with.
type optsFaker struct{ got *[]core.FakeOpts }

func (f optsFaker) Fake(_ core.FakeKind, _ []byte, o core.FakeOpts) ([]byte, error) {
	*f.got = append(*f.got, o)
	return []byte("fake"), nil
}
func (f optsFaker) Clone() core.Faker { return f }

// The same transform through the real values and detect packages, which
// turns on by itself once those lanes land (lanes.md 2.3).
func TestTransformRealPackages(t *testing.T) {
	if _, err := pseudo.NewFaker(pseudo.Key{}, pseudo.Options{}); errors.Is(err, core.ErrNotBuilt) {
		t.Skip("needs the values lane")
	}
	if _, _, err := nested.Rewrite([]byte("x"), nil, nested.Options{}); errors.Is(err, core.ErrNotBuilt) {
		t.Skip("needs the values lane")
	}
	if _, err := urlrw.NewMap([]urlrw.Pair{{From: "https://shop.com", To: "https://staging.shop.test"}}); errors.Is(err, core.ErrNotBuilt) {
		t.Skip("needs the values lane")
	}
	if detect.TextSpans() == nil {
		t.Skip("needs the detect lane")
	}
	key := pseudo.Key{1, 2, 3}
	runOnce := func() ([]byte, *output) {
		p, sink, _ := fixture(t, Copy, "wp_", func() []tableData {
			return append(wpUsers(), tableData{table("wp_actionscheduler_actions", "action_id:bigint(20)", "hook", "args:varchar(191)", "extended_args:varchar(8000)"),
				rows{{1, "h", `[{"email":"anna.lee@example.com"}]`, nil}}})
		}, nil, Options{URL: "https://staging.shop.test", Jobs: 4})
		p.key = key
		out := run(t, p)
		return sink.Bytes(), out
	}
	b1, out := runOnce()
	b2, _ := runOnce()
	if !bytes.Equal(b1, b2) {
		t.Error("two runs with one key differ")
	}
	if bytes.Contains(b1, []byte("anna.lee@example.com")) || bytes.Contains(b1, []byte("https://shop.com")) {
		t.Errorf("production data left: %s", b1)
	}
	if out.Result.RulesetSHA256 == "" || len(out.Result.Changes) == 0 {
		t.Errorf("result %+v", out.Result)
	}
}

// A whole leaf that is a production email, phone or personal URL gets its
// fake where no rule covers it; a name word, production's own URL and a
// system author stay. A free-text field holding one known value whole
// gets that value's fake.
func TestKnownValueSweep(t *testing.T) {
	if _, err := pseudo.NewFaker(pseudo.Key{}, pseudo.Options{}); errors.Is(err, core.ErrNotBuilt) {
		t.Skip("needs the values lane")
	}
	settings := `{"notify":"anna.lee@shop-mail.com","color":"Green","home":"https://shop.com","site":"https://anna.example/blog","by":"WooCommerce"}`
	p, sink, _ := fixture(t, Copy, "wp_", func() []tableData {
		return []tableData{
			{table("wp_options", "option_id:bigint(20)", "option_name", "option_value"), rows{
				{1, "siteurl", "https://shop.com"}, {2, "home", "https://shop.com"}, {3, "plugin_settings", settings}}},
			{table("wp_users", "ID:bigint(20)", "user_login", "user_email", "user_url"), rows{
				{1, "anna", "anna.lee@shop-mail.com", "https://anna.example/blog"}}},
			{table("wp_usermeta", "umeta_id:bigint(20)", "user_id:bigint(20)", "meta_key", "meta_value"), rows{
				{1, 1, "last_name", "Green"}, {2, 1, "billing_address_1", "12 Wexcombe Road"}}},
			{table("wp_comments", "comment_ID:bigint(20)", "comment_author", "comment_content"), rows{
				{1, "WooCommerce", "12 Wexcombe Road"}, {2, "WooCommerce", "Green"}}},
		}
	}, nil, Options{URL: "https://staging.shop.test"})
	out := run(t, p)
	v, _ := valueOf(t, sink, "wp_options", 2, "option_value")
	for _, gone := range []string{"anna.lee@shop-mail.com", "https://anna.example/blog", "https://shop.com"} {
		if strings.Contains(v, gone) {
			t.Errorf("settings keep %q: %s", gone, v)
		}
	}
	for _, kept := range []string{`"color":"Green"`, `"home":"https://staging.shop.test"`, `"by":"WooCommerce"`, "@example.invalid"} {
		if !strings.Contains(v, kept) {
			t.Errorf("settings lack %q: %s", kept, v)
		}
	}
	if c, _ := valueOf(t, sink, "wp_comments", 0, "comment_content"); c == "12 Wexcombe Road" || c == "" {
		t.Errorf("a known street as a whole comment stayed: %q", c)
	}
	swept := false
	for _, c := range out.Result.Changes {
		swept = swept || c.Rule == changeKnown && c.Table == "wp_options" && c.Values == 2
	}
	if !swept {
		t.Errorf("changes %+v lack two known-values on wp_options", out.Result.Changes)
	}
}

// The kill switch writes its option rules with bare paths ("testmode");
// they still change the leaf of a serialized option.
func TestKillSwitchOptionRuleOnSerialized(t *testing.T) {
	settings := `a:2:{s:7:"enabled";s:3:"yes";s:8:"testmode";s:2:"no";}`
	p, sink, _ := fixture(t, Copy, "wp_", func() []tableData {
		return []tableData{
			{table("wp_options", "option_id:bigint(20)", "option_name", "option_value"), rows{
				{1, "siteurl", "https://shop.com"}, {2, "woocommerce_stripe_settings", settings}}},
		}
	}, nil, Options{URL: "https://staging.shop.test"})
	p.dataRules = func(core.Stack) []core.Rule {
		return []core.Rule{{ID: "killswitch.options.option_value.woocommerce_stripe_settings.testmode", Profile: "killswitch",
			Table: "{prefix}options", Column: "option_value", Where: map[string]string{"option_name": "woocommerce_stripe_settings"},
			Path: "testmode", Action: core.Fixed, Value: "yes"}}
	}
	run(t, p)
	if v, _ := valueOf(t, sink, "wp_options", 1, "option_value"); v != `a:2:{s:7:"enabled";s:3:"yes";s:8:"testmode";s:3:"yes";}` {
		t.Errorf("stripe settings: %s", v)
	}
}

// A known street and postcode inside free text get their fakes; the
// city, a number on its own and other text stay.
func TestSweepTextAddresses(t *testing.T) {
	if _, err := pseudo.NewFaker(pseudo.Key{}, pseudo.Options{}); errors.Is(err, core.ErrNotBuilt) {
		t.Skip("needs the values lane")
	}
	p, sink, _ := fixture(t, Copy, "wp_", func() []tableData {
		return []tableData{
			{table("wp_options", "option_id:bigint(20)", "option_name", "option_value"), rows{{1, "siteurl", "https://shop.com"}}},
			{table("wp_usermeta", "umeta_id:bigint(20)", "user_id:bigint(20)", "meta_key", "meta_value"), rows{
				{1, 1, "billing_address_1", "111 Kingsmead Road"}, {2, 1, "billing_city", "Dublin"}, {3, 1, "billing_postcode", "D06 F2X3"}}},
			{table("wp_comments", "comment_ID:bigint(20)", "comment_author", "comment_content"), rows{
				{1, "WooCommerce", "Ship to 111 Kingsmead Road, Dublin D06 F2X3."},
				{2, "WooCommerce", "Room 111 on Kingsmead Road is closed; 7 days."}}},
		}
	}, nil, Options{URL: "https://staging.shop.test"})
	run(t, p)
	c, _ := valueOf(t, sink, "wp_comments", 0, "comment_content")
	for _, gone := range []string{"111 Kingsmead Road", "D06 F2X3"} {
		if strings.Contains(c, gone) {
			t.Errorf("note keeps %q: %q", gone, c)
		}
	}
	if !strings.HasPrefix(c, "Ship to ") || !strings.Contains(c, ", Dublin ") || !strings.HasSuffix(c, ".") {
		t.Errorf("note lost its other words: %q", c)
	}
	if c, _ := valueOf(t, sink, "wp_comments", 1, "comment_content"); c != "Room 111 on Kingsmead Road is closed; 7 days." {
		t.Errorf("a note without a known value changed: %q", c)
	}
}

func TestTextWords(t *testing.T) {
	text := []byte("Ship to 12/3 St.-Denis Rd., Dublin (D06 F2X3)!")
	var got []string
	for _, w := range textWords(text) {
		got = append(got, string(text[w[0]:w[1]]))
	}
	want := []string{"Ship", "to", "12/3", "St.-Denis", "Rd", "Dublin", "D06", "F2X3"}
	if !slices.Equal(got, want) {
		t.Errorf("textWords = %q, want %q", got, want)
	}
	if len(textWords([]byte(" ,;. "))) != 0 {
		t.Error("punctuation alone made words")
	}
}

// The words of a production host are the business's: a name holding one
// is not looked up; other names and other kinds are.
func TestHostWordKnown(t *testing.T) {
	words := hostWords([]string{"shop.example.com", "*.my-store.co", "www.shop.example.com:8443", "10.0.0.1"})
	for _, w := range []string{"shop", "example", "store"} {
		if !words[w] {
			t.Errorf("hostWords lacks %q: %v", w, words)
		}
	}
	for _, w := range []string{"com", "co", "www", "my"} {
		if words[w] {
			t.Errorf("hostWords has %q", w)
		}
	}
	k := testutil.FakeKnown()
	k.Add(core.KFullName, []byte("Shop Owner"))
	k.Add(core.KFirstName, []byte("Shop"))
	k.Add(core.KLastName, []byte("Lindqvist"))
	k.Add(core.KCompany, []byte("Example Ltd"))
	k.Seal()
	hk := &hostWordKnown{Known: k, words: words}
	for tok, want := range map[string]bool{"Shop Owner": false, "shop": false, "Lindqvist": true, "example ltd": true} {
		if _, _, ok := hk.Contains([]byte(tok)); ok != want {
			t.Errorf("Contains(%q) = %v, want %v", tok, ok, want)
		}
	}
}

// A value the dump wrote in hex gets its rules like a quoted one; an
// expression in a pseudonymised column is blanked and reported, in a
// column no rule changes it stays.
func TestTransformHexAndExpr(t *testing.T) {
	if _, err := pseudo.NewFaker(pseudo.Key{}, pseudo.Options{}); errors.Is(err, core.ErrNotBuilt) {
		t.Skip("needs the values lane")
	}
	hexEmail := core.Value{Kind: core.Hex, Raw: []byte("0x" + strings.ToUpper(hex.EncodeToString([]byte("anna.lee@shop-mail.com"))))}
	expr := core.Value{Kind: core.Expr, Raw: []byte("'ben' '@shop-mail.com'")}
	exprKept := core.Value{Kind: core.Expr, Raw: []byte("CONCAT('a','b')")}
	p, sink, _ := fixture(t, Copy, "wp_", func() []tableData {
		return []tableData{
			{table("wp_options", "option_id:bigint(20)", "option_name", "option_value"), rows{{1, "siteurl", "https://shop.com"}}},
			{table("wp_users", "ID:bigint(20)", "user_login", "user_email", "user_status"), rows{
				{1, "anna", hexEmail, "0"}, {2, "ben", expr, exprKept}, {3, "cy", core.Value{Kind: core.Hex, Raw: []byte("0xZZ")}, "0"}}},
		}
	}, nil, Options{URL: "https://staging.shop.test"})
	out := run(t, p)
	if v, _ := valueOf(t, sink, "wp_users", 0, "user_email"); strings.Contains(v, "anna") || !strings.HasSuffix(v, "@example.invalid") {
		t.Errorf("hex email: %q", v)
	}
	if v, _ := valueOf(t, sink, "wp_users", 1, "user_email"); v != "" {
		t.Errorf("an expression in user_email: %q", v)
	}
	if v, _ := valueOf(t, sink, "wp_users", 1, "user_status"); v != "CONCAT('a','b')" {
		t.Errorf("an expression no rule changes: %q", v)
	}
	if v, _ := valueOf(t, sink, "wp_users", 2, "user_email"); v != "0xZZ" {
		t.Errorf("hex that isn't hex: %q", v)
	}
	found := false
	for _, f := range out.Result.Findings {
		found = found || f.ID == core.FindingDataUnparseable
	}
	if !found {
		t.Error("no data.unparseable finding for the expression")
	}
	// on_unparseable: fail stops instead.
	cfg := roadiecfg.Default()
	cfg.Pseudonymise.OnUnparseable = "fail"
	p2, _, _ := fixture(t, Copy, "wp_", func() []tableData {
		return []tableData{
			{table("wp_options", "option_id:bigint(20)", "option_name", "option_value"), rows{{1, "siteurl", "https://shop.com"}}},
			{table("wp_users", "ID:bigint(20)", "user_login", "user_email"), rows{{2, "ben", expr}}},
		}
	}, cfg, Options{URL: "https://staging.shop.test"})
	if _, err := p2.run(context.Background()); !isCode(err, core.FindingDataUnparseable) {
		t.Errorf("on_unparseable: fail: %v", err)
	}
}

// A fixed rule reaches a boolean or integer leaf of a serialized value:
// PayPal Payments stores sandbox_on as b:0;. A leaf already right is
// kept, a value that isn't the leaf's type leaves it alone and a string
// leaf stays the string rewrite's.
func TestFixedScalars(t *testing.T) {
	rule := func(path, value string) core.Rule {
		return core.Rule{ID: "ks." + path, Path: path, Action: core.Fixed, Value: value}
	}
	cases := []struct {
		in    string
		rules []core.Rule
		want  string // "" = unchanged
	}{
		{`a:2:{s:7:"enabled";b:1;s:10:"sandbox_on";b:0;}`, []core.Rule{rule("sandbox_on", "1")}, `a:2:{s:7:"enabled";b:1;s:10:"sandbox_on";b:1;}`},
		{`a:1:{s:10:"sandbox_on";b:1;}`, []core.Rule{rule("sandbox_on", "yes")}, ""},
		{`a:1:{s:7:"enabled";b:1;}`, []core.Rule{rule("enabled", "no")}, `a:1:{s:7:"enabled";b:0;}`},
		{`a:1:{s:4:"mode";i:1;}`, []core.Rule{rule("serialized:mode", "0")}, `a:1:{s:4:"mode";i:0;}`},
		{`a:1:{s:11:"environment";b:0;}`, []core.Rule{rule("environment", "sandbox")}, ""},
		{`a:1:{s:10:"sandbox_on";s:1:"0";}`, []core.Rule{rule("sandbox_on", "1")}, ""},
		{`a:1:{s:10:"sandbox_on";b:0;}`, []core.Rule{{ID: "x", Path: "sandbox_on", Action: core.Blank}}, ""},
		{`{"sandbox_on":false}`, []core.Rule{rule("sandbox_on", "1")}, ""},
	}
	for _, c := range cases {
		cnt := &counter{out: &batchOut{}, table: "wp_options", col: "option_value"}
		got, ok := fixedScalars([]byte(c.in), c.rules, cnt)
		if c.want == "" {
			if ok {
				t.Errorf("%s: changed to %s", c.in, got)
			}
			continue
		}
		if !ok || string(got) != c.want {
			t.Errorf("%s: got %s %v, want %s", c.in, got, ok, c.want)
		}
	}
}

// A leaf no rule covers gets fakes for what it plainly holds: a phone or
// a name by its key in a nested value, a known name under "name", and
// personal emails (a URL's %40 too), phones and IPs anywhere. A
// product's name, a short code under "phone", a reserved address and
// production's own host stay.
func TestUncoveredValues(t *testing.T) {
	if _, err := pseudo.NewFaker(pseudo.Key{}, pseudo.Options{}); errors.Is(err, core.ErrNotBuilt) {
		t.Skip("needs the values lane")
	}
	mobile := "+44 7700 " + "981234"
	inner := "a:2:{" + serStr("phone") + serStr(mobile) + serStr("city") + serStr("Leeds") + "}"
	profile := "a:2:{" + serStr("profile") + serStr(inner) + serStr("name") + serStr("Anna Lee") + "}"
	cron := "a:1:{i:1767225720;a:1:{" + serStr("send_newsletter") + "a:1:{" + serStr("args") + "a:1:{i:0;" + serStr("mara.ostby@gmail.com") + "}}}}"
	plugin := `{"name":"Stripe","phone":"n/a","first_name":"Zebulonne","support":"help@example.com","ip":"81.2.69.142"}`
	entry := "https://shop.com/checkout/?ref=crm&contact=mara.ostby%40gmail.com"
	p, sink, _ := fixture(t, Copy, "wp_", func() []tableData {
		return []tableData{
			{table("wp_options", "option_id:bigint(20)", "option_name", "option_value"), rows{
				{1, "siteurl", "https://shop.com"}, {2, "home", "https://shop.com"}, {3, "cron", cron}, {4, "plugin_settings", plugin}}},
			{table("wp_users", "ID:bigint(20)", "user_login", "user_email", "display_name"), rows{
				{1, "anna", "anna.lee@shop-mail.com", "Anna Lee"}}},
			{table("wp_usermeta", "umeta_id:bigint(20)", "user_id:bigint(20)", "meta_key", "meta_value"), rows{
				{1, 1, "plugin_profile", profile}, {2, 1, "_session_entry", entry}}},
		}
	}, nil, Options{URL: "https://staging.shop.test"})
	out := run(t, p)
	v, _ := valueOf(t, sink, "wp_usermeta", 0, "meta_value")
	if strings.Contains(v, mobile) || strings.Contains(v, "Anna") || !strings.Contains(v, "Leeds") {
		t.Errorf("profile: %s", v)
	}
	if !phpser.Valid([]byte(v)) {
		t.Errorf("profile no longer serialized: %s", v)
	}
	if v, _ := valueOf(t, sink, "wp_usermeta", 1, "meta_value"); strings.Contains(v, "mara.ostby") || !strings.Contains(v, "%40") ||
		!strings.HasPrefix(v, "https://staging.shop.test/checkout/") {
		t.Errorf("session entry: %s", v)
	}
	if v, _ := valueOf(t, sink, "wp_options", 2, "option_value"); strings.Contains(v, "mara.ostby") || !phpser.Valid([]byte(v)) {
		t.Errorf("cron: %s", v)
	}
	v, _ = valueOf(t, sink, "wp_options", 3, "option_value")
	for _, kept := range []string{`"name":"Stripe"`, `"phone":"n/a"`, `"support":"help@example.com"`} {
		if !strings.Contains(v, kept) {
			t.Errorf("plugin settings lack %s: %s", kept, v)
		}
	}
	if strings.Contains(v, `"first_name":"Zebulonne"`) || strings.Contains(v, "81.2.69.142") {
		t.Errorf("plugin settings: %s", v)
	}
	n := int64(0)
	for _, c := range out.Result.Changes {
		if c.Rule == changeUncovered {
			n += c.Values
		}
	}
	if n < 5 {
		t.Errorf("uncovered-values changes %d, want 5 or more: %+v", n, out.Result.Changes)
	}
}

// serStr is PHP's serialize() of a string.
func serStr(v string) string { return fmt.Sprintf(`s:%d:"%s";`, len(v), v) }

// An address index written without the empty parts (an import, a
// fixture) is rebuilt part by part too, keeping its layout, so it holds
// the parts' own fakes: the same names, street and city.
func TestAddressIndexWithoutEmptyParts(t *testing.T) {
	useFakes(t)
	var nonEmpty []string
	for _, v := range billing {
		if v != "" {
			nonEmpty = append(nonEmpty, v)
		}
	}
	index := strings.Join(nonEmpty, " ")
	tables := func() []tableData {
		addr := []any{1, 10, "billing"}
		for _, v := range billing {
			addr = append(addr, v)
		}
		return []tableData{
			wpBase([]any{4, "woocommerce_custom_orders_table_enabled", "yes"}),
			{table("wp_wc_order_addresses", append([]string{"id:bigint(20)", "order_id:bigint(20)", "address_type"}, addrParts[true]...)...), rows{addr}},
			{table("wp_wc_orders", "id:bigint(20)", "customer_id:bigint(20)", "billing_email"), rows{{10, 0, billing[9]}}},
			{table("wp_wc_orders_meta", "id:bigint(20)", "order_id:bigint(20)", "meta_key", "meta_value:longtext"), rows{
				{1, 10, "_billing_address_index", index}, {2, 10, "_billing_address_index", index + " extra"}}},
		}
	}
	p, sink, _ := fixture(t, Copy, "wp_", tables, nil, Options{})
	run(t, p)
	var fakes []string
	for _, part := range addrParts[true] {
		if v, _ := valueOf(t, sink, "wp_wc_order_addresses", 0, part); v != "" {
			fakes = append(fakes, v)
		}
	}
	if v, _ := valueOf(t, sink, "wp_wc_orders_meta", 0, "meta_value"); v != strings.Join(fakes, " ") {
		t.Errorf("index %q, want %q", v, strings.Join(fakes, " "))
	}
	// Lengths that fit neither layout: the index keeps its text rule.
	if v, _ := valueOf(t, sink, "wp_wc_orders_meta", 1, "meta_value"); strings.Contains(v, billing[9]) || v == strings.Join(fakes, " ")+" extra" {
		t.Errorf("odd index %q", v)
	}
}

// An address index's text keeps no known city; other free text keeps a
// city on its own, which points at no one.
func TestSweepTextCity(t *testing.T) {
	k := testutil.FakeKnown()
	k.Add(core.KCity, []byte("Leeds"))
	k.Add(core.KPostcode, []byte("LS1 4AP"))
	k.Seal()
	tr := newTransformer(&shared{cfg: roadiecfg.Default(), known: k}, testutil.FakeFaker())
	text := []byte("Anna Lee 1 High Street Leeds LS1 4AP GB")
	got, ok := tr.sweepText(text, indexKinds)
	if !ok || strings.Contains(string(got), "Leeds") || strings.Contains(string(got), "LS1 4AP") {
		t.Errorf("index: %s", got)
	}
	got, ok = tr.sweepText(text, textKinds)
	if !ok || !strings.Contains(string(got), "Leeds") || strings.Contains(string(got), "LS1 4AP") {
		t.Errorf("text: %s", got)
	}
	if got, ok := tr.sweepText([]byte("Made in Leeds"), textKinds); ok {
		t.Errorf("a city in prose: %s", got)
	}
}

// keyedLeaf fakes a nested leaf by its key: a person's name under a
// person's key or in usermeta, address parts and phones; a product's
// name, a short code under "phone" and a text leaf stay.
func TestKeyedLeaf(t *testing.T) {
	useFakes(t)
	tr := newTransformer(&shared{cfg: roadiecfg.Default(), known: testutil.FakeKnown()}, testutil.FakeFaker())
	for _, c := range []struct {
		table, path, leaf string
		kind              core.FakeKind // "" = unchanged
	}{
		{"wp_actionscheduler_actions", "json:customer.name", "Disipoh Rifivolic", core.KFullName},
		{"wp_actionscheduler_actions", "json:customer.address", "166 Bamelacok Road", core.KAddress},
		{"wp_usermeta", "serialized:name", "Zoë Taperijeta", core.KFullName},
		{"wp_usermeta", "serialized:profile.serialized:phone", "+44 7911 668109", core.KPhone},
		{"wp_options", "serialized:origin.postcode", "LS1 4AP", core.KPostcode},
		{"wp_options", "json:product.name", "Coffee Grinder", ""},
		{"wp_options", "json:name", "Stripe", ""},
		{"wp_options", "json:phone", "n/a", ""},
		{"wp_options", "", "+44 7911 668109", ""},
	} {
		got, ch, err := tr.keyedLeaf(c.table, c.path, []byte(c.leaf))
		if err != nil {
			t.Fatal(err)
		}
		want := c.leaf
		if c.kind != "" {
			b, _ := testutil.FakeFaker().Fake(c.kind, []byte(c.leaf), core.FakeOpts{})
			want = string(b)
		}
		if ch != (c.kind != "") || c.kind == "" && string(got) != c.leaf || c.kind != "" && !strings.HasPrefix(string(got), "fake-"+string(c.kind)) {
			t.Errorf("%s %s %q: %q %v, want %q", c.table, c.path, c.leaf, got, ch, want)
		}
	}
}
