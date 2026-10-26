// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package core_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/internal/testutil"
)

func TestExitFor(t *testing.T) {
	scanned := &core.VerifyResult{Coverage: core.Coverage{TablesScanned: 3, RowsScanned: 10}}
	high := core.Finding{ID: core.FindingPIIEmail, Severity: core.SeverityHigh}
	cases := []struct {
		name   string
		r      *core.Result
		failOn core.Severity
		want   int
	}{
		{"nil result", nil, core.SeverityHigh, 3},
		{"clean", &core.Result{Verify: scanned}, core.SeverityHigh, 0},
		{"error wins", &core.Result{Err: core.Errorf(core.ErrStack, 2, "stack: x"), Findings: []core.Finding{high}}, core.SeverityHigh, 2},
		{"engine finding at fail-on", &core.Result{Findings: []core.Finding{high}}, core.SeverityHigh, 1},
		{"verify finding above fail-on", &core.Result{Verify: &core.VerifyResult{Findings: []core.Finding{high}, Coverage: scanned.Coverage}}, core.SeverityMedium, 1},
		{"finding below fail-on", &core.Result{Findings: []core.Finding{high}}, core.SeverityCritical, 0},
		{"fail-on none never fails", &core.Result{Findings: []core.Finding{high}}, core.SeverityNone, 0},
		{"nothing scanned", &core.Result{Verify: &core.VerifyResult{Coverage: core.Coverage{TablesScanned: 2}}}, core.SeverityHigh, 3},
		{"plan without verify", &core.Result{Kind: "plan"}, core.SeverityHigh, 0},
		{"input not read whole", &core.Result{Verify: &core.VerifyResult{Coverage: core.Coverage{TablesScanned: 3, RowsScanned: 10,
			Skipped: []core.Skipped{{Table: core.SkippedInput + " after wp_users)", Reason: "dump-truncated"}}}}}, core.SeverityHigh, 3},
		{"values read as text", &core.Result{Verify: &core.VerifyResult{Coverage: core.Coverage{TablesScanned: 3, RowsScanned: 10,
			Skipped: []core.Skipped{{Table: "wp_options.option_value", Reason: "serialized values that don't parse"}}}}}, core.SeverityHigh, 0},
	}
	for _, c := range cases {
		if got := core.ExitFor(c.r, c.failOn); got != c.want {
			t.Errorf("%s: ExitFor = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestMasks(t *testing.T) {
	cases := []struct{ got, want string }{
		{core.MaskEmail("anna@gmail.com"), "an•••@gm•••.com"},
		{core.MaskEmail("ab@x.co.uk"), "a•••@x.•••.uk"},
		{core.MaskEmail("a@b.c0m"), "•••@•••"},
		{core.MaskEmail("no-at-sign"), "•••"},
		{core.MaskPhone("+44 7700 900123"), "+44 •••"},
		{core.MaskPhone("+1 (415) 555-2671"), "+1 •••"},
		{core.MaskPhone("+353 87 123 4567"), "+353 •••"},
		{core.MaskPhone("07700 900123"), "•••"},
		{core.MaskPhone("+4"), "•••"},
		{core.MaskName("Jane Smith"), "J•••"},
		{core.MaskName("Ö"), "•••"},
		{core.MaskIP("81.2.69.160"), "81.•••"},
		{core.MaskIP("::ffff:81.2.69.160"), "81.•••"},
		{core.MaskIP("2a00:1450::1"), "2a00:•••"},
		{core.MaskIP("::1"), "0:•••"},
		{core.MaskIP("not an ip"), "•••"},
		{core.MaskKey(testutil.J("sk_", "live_", "abcdefghijklmnop")), "sk_l•••••"},
		{core.MaskValue(core.KCity, "Manchester"), "M•••"},
		{core.MaskValue(core.KText, "call bob@shop.com"), "ca•••@sh•••.com"},
		{core.MaskValue(core.KPostcode, "M1 1AE"), "•••"},
		{core.MaskRow("wp_options", map[string]string{"option_name": "woocommerce_stripe_settings"}), "option_name=woocommerce_stripe_settings"},
		{core.MaskRow("t", map[string]string{"id": "4", "email": "anna@gmail.com"}), "email=an•••@gm•••.com,id=4"},
	}
	for i, c := range cases {
		if c.got != c.want {
			t.Errorf("case %d: got %q, want %q", i, c.got, c.want)
		}
	}
}

// No mask leaves more than two characters of any part of a value in
// view (the top-level domain of an email aside, which is public).
func TestMasksHideParts(t *testing.T) {
	for _, s := range []string{"alexandra.kowalczyk@correo-privado.es", "maría.lópez@correo.es", "ab@cd.com"} {
		local, domain, _ := strings.Cut(s, "@")
		name := domain[:strings.LastIndexByte(domain, '.')]
		m := core.MaskEmail(s)
		for _, part := range []string{local, name} {
			r := []rune(part)
			if len(r) > 2 && strings.Contains(m, string(r[:3])) {
				t.Errorf("MaskEmail(%q) = %q shows 3 characters of %q", s, m, part)
			}
		}
	}
	for _, s := range []string{"Bartholomew", "Li"} {
		if m := core.MaskName(s); strings.Contains(m, s[:2]) {
			t.Errorf("MaskName(%q) = %q", s, m)
		}
	}
	if m := core.MaskPhone("+44 7911 123456"); strings.Contains(m, "791") {
		t.Errorf("MaskPhone shows the number: %q", m)
	}
}

func TestReservedEmailDomain(t *testing.T) {
	for _, d := range []string{"example.com", "EXAMPLE.org", "mail.example.net", "shop.example", "x.invalid", "staging.test", "localhost", "box.local", "example.invalid."} {
		if !core.ReservedEmailDomain(d) {
			t.Errorf("positive %q not reserved", d)
		}
	}
	for _, d := range []string{"gmail.com", "example.co.uk", "myexample.com", "examples.com", "test.com", "", "invalid.com"} {
		if core.ReservedEmailDomain(d) {
			t.Errorf("negative %q reserved", d)
		}
	}
}

func TestFictionalPhone(t *testing.T) {
	for _, p := range []string{"+1 212 555 0123", "12125550199", "+1 555 010 1234", "+44 7700 900000", "447700900999", "+49 30 23125 000", "+49 89 99998 999", "+49 152 28817386", "+49 171 39200 42", "+49 176 040690 07", "+44 113 496 0123", "+44 20 7946 0999", "+44 28 9649 6000", "+44 808 157 0500"} {
		if !core.FictionalPhone(p) {
			t.Errorf("positive %q not fictional", p)
		}
	}
	for _, p := range []string{"+1 212 555 0200", "+1 212 555 1234", "+44 7700 901000", "+44 7911 123456", "+49 30 23126 000", "+49 152 28817387", "+49 30 23125 0000", "", "555-0123", "+44 113 496 1000", "+44 20 7946 1000", "+44 113 496 012"} {
		if core.FictionalPhone(p) {
			t.Errorf("negative %q fictional", p)
		}
	}
}

func TestDocumentationIP(t *testing.T) {
	for _, s := range []string{"192.0.2.1", "198.51.100.200", "203.0.113.9", "2001:db8::1", "::ffff:192.0.2.5", "3fff::1"} {
		if !core.DocumentationIP(netip.MustParseAddr(s)) {
			t.Errorf("positive %s", s)
		}
	}
	for _, s := range []string{"81.2.69.160", "10.0.0.1", "192.0.3.1", "2001:db9::1", "127.0.0.1"} {
		if core.DocumentationIP(netip.MustParseAddr(s)) {
			t.Errorf("negative %s", s)
		}
	}
}

func TestTestCardAndIBAN(t *testing.T) {
	for _, c := range []string{"4242 4242 4242 4242", "4111-1111-1111-1111", "5555555555554444", "378282246310005"} {
		if !core.TestCard(c) {
			t.Errorf("positive card %q", c)
		}
	}
	for _, c := range []string{"4242 4242 4242 4241", "4539 1488 0343 6467", ""} {
		if core.TestCard(c) {
			t.Errorf("negative card %q", c)
		}
	}
	for _, s := range []string{"GB82 WEST 1234 5698 7654 32", "de89370400440532013000"} {
		if !core.ExampleIBAN(s) {
			t.Errorf("positive IBAN %q", s)
		}
	}
	for _, s := range []string{"GB29NWBK60161331926819", ""} {
		if core.ExampleIBAN(s) {
			t.Errorf("negative IBAN %q", s)
		}
	}
}

func TestAggregator(t *testing.T) {
	var a core.Aggregator
	at := func(row string) []core.LocationCount {
		return []core.LocationCount{{Location: core.Location{Table: "wp_users", Column: "user_email", Row: row}}}
	}
	for i := range 10 {
		a.Add(core.Finding{ID: core.FindingPIIEmail, Severity: core.SeverityHigh, Title: "real email", Locations: at(fmt.Sprintf("ID=%d", i)), Examples: []string{fmt.Sprintf("e%d", i)}})
	}
	a.Add(core.Finding{ID: core.FindingPIIEmail, Severity: core.SeverityHigh, Locations: at("ID=0"), Count: 4})
	a.Add(core.Finding{ID: core.FindingPIIName, Severity: core.SeverityMedium})
	a.Add(core.Finding{ID: core.FindingConfigSalts, Severity: core.SeverityMedium})
	a.Add(core.Finding{ID: "key.stripe-live", Severity: core.SeverityCritical, Locations: []core.LocationCount{{Location: core.Location{File: "wp-config.php", Line: 4}}}})
	got := a.Findings()
	if len(got) != 4 {
		t.Fatalf("%d groups: %+v", len(got), got)
	}
	ids := []string{got[0].ID, got[1].ID, got[2].ID, got[3].ID}
	if strings.Join(ids, " ") != "key.stripe-live pii.email config.salts pii.name" {
		t.Fatalf("order %v", ids)
	}
	e := got[1]
	if e.Count != 14 || len(e.Examples) != core.MaxExamples || len(e.Locations) != core.MaxExamples || e.Locations[0].Count != 5 {
		t.Fatalf("pii.email group: %+v", e)
	}
}

func TestKnownID(t *testing.T) {
	for _, id := range append(core.FindingIDs(), core.ErrorCodes()...) {
		if !core.KnownID(id) {
			t.Errorf("%q not known", id)
		}
	}
	for _, p := range core.KeyProviders() {
		if !core.KnownID(core.KeyLiveID(p)) {
			t.Errorf("%q not known", core.KeyLiveID(p))
		}
	}
	for _, id := range []string{"", "pii.mail", "key.nobody-live", "key.stripe", "key.stripe-test"} {
		if core.KnownID(id) {
			t.Errorf("%q known", id)
		}
	}
	for _, c := range core.ErrorCodes() {
		if exit, _ := core.CodeExit(c); exit < 1 || exit > 3 {
			t.Errorf("%s: exit %d", c, exit)
		}
	}
	if exit, _ := core.CodeExit(core.ErrDiskFull); exit != 3 {
		t.Errorf("disk-full exit %d", exit)
	}
}

func TestLocationString(t *testing.T) {
	cases := []struct {
		l    core.Location
		want string
	}{
		{core.Location{Table: "wp_options", Column: "option_value", Row: "option_name=woocommerce_stripe_settings", Path: "serialized:secret_key"}, "wp_options woocommerce_stripe_settings → serialized:secret_key"},
		{core.Location{File: "wp-config.php", Line: 41}, "wp-config.php line 41"},
		{core.Location{Table: "wp_users", Column: "user_email"}, "wp_users.user_email"},
		{core.Location{}, ""},
	}
	for _, c := range cases {
		if got := c.l.String(); got != c.want {
			t.Errorf("%+v: %q, want %q", c.l, got, c.want)
		}
	}
}

func TestErrorIs(t *testing.T) {
	copyOf := *core.ErrNotBuilt
	if !errors.Is(fmt.Errorf("wrap: %w", &copyOf), core.ErrNotBuilt) {
		t.Fatal("a wrapped copy of ErrNotBuilt doesn't match")
	}
	if errors.Is(core.Errorf(core.ErrStack, 2, "x"), core.ErrNotBuilt) {
		t.Fatal("another code matches ErrNotBuilt")
	}
	if core.ErrNotBuilt.Exit != 3 || core.ErrNotBuilt.Error() != "not built yet" {
		t.Fatal("ErrNotBuilt changed")
	}
}

func TestEndpointNeverPrintsSecrets(t *testing.T) {
	pw := testutil.J("hunter", "2-", "secret")
	e := core.Endpoint{Kind: core.DSN, Raw: "mysql://u:" + pw + "@db/x", Display: "mysql://u:•••@db/x", Password: pw}
	b, _ := json.Marshal(e)
	for _, s := range []string{fmt.Sprint(e), fmt.Sprintf("%v %+v %#v %s", e, e, e, e), string(b)} {
		if strings.Contains(s, pw) {
			t.Fatalf("password printed: %s", s)
		}
	}
}

func TestSchema(t *testing.T) {
	tbl := &core.Table{Name: "wp_users", Columns: []core.Column{{Name: "ID"}, {Name: "user_email"}, {Name: "Name"}, {Name: "name"}}}
	s := &core.Schema{Tables: []*core.Table{tbl}}
	if s.Table("wp_users") != tbl || s.Table("WP_USERS") != nil || (*core.Schema)(nil).Table("x") != nil {
		t.Fatal("Schema.Table")
	}
	if tbl.Col("id") != 0 || tbl.Col("name") != 3 || tbl.Col("Name") != 2 || tbl.Col("nope") != -1 {
		t.Fatal("Table.Col")
	}
}

func TestSummary(t *testing.T) {
	r := &core.Result{
		Tables:      []core.TableReport{{Name: "a", Rows: 3, Columns: []core.ColumnReport{{Changed: 2}}}, {Name: "b", Rows: 1}},
		Changes:     []core.Change{{Action: core.Fake, Values: 5}, {Action: core.RewriteURL, Values: 2}, {Action: core.Keep, Values: 9}},
		Keys:        []core.KeyHit{{Action: "swapped"}, {Action: "blanked"}, {Action: "kept"}},
		SideEffects: []core.SideEffect{{Status: "off"}, {Status: "on"}, {Status: "not-present"}},
		Findings:    []core.Finding{{Severity: core.SeverityMedium}},
		Uncovered:   []core.Uncovered{{Table: "t", Column: "c"}},
		Verify:      &core.VerifyResult{Findings: []core.Finding{{Severity: core.SeverityCritical}}, Uncovered: []core.Uncovered{{Table: "t", Column: "c"}, {Table: "t", Column: "d"}}},
	}
	want := core.Summary{Critical: 1, Medium: 1, Tables: 2, TablesPseudonymised: 1, Rows: 4, ValuesPseudonymised: 5, URLsRewritten: 2,
		KeysFound: 3, KeysSwapped: 1, KeysBlanked: 1, SideEffectsOff: 1, SideEffectsTotal: 2, UncoveredColumns: 2}
	if got := r.Summary(); got != want {
		t.Fatalf("Summary = %+v\nwant %+v", got, want)
	}
	// A standalone verify counts what it read.
	v := &core.Result{Kind: "verify", Verify: &core.VerifyResult{Coverage: core.Coverage{TablesScanned: 13, RowsScanned: 1536}}}
	if got := v.Summary(); got.Tables != 13 || got.Rows != 1536 {
		t.Errorf("verify summary %+v", got)
	}
}

// The JSON field names are a public contract (PRD 16.2).
func TestJSONFieldNames(t *testing.T) {
	f := core.Finding{ID: core.FindingPIIEmail, Severity: core.SeverityHigh, Title: "t", Count: 2,
		Locations: []core.LocationCount{{Location: core.Location{File: "f", Line: 1, Table: "t", Column: "c", Row: "id=1", Path: "json:a"}, Count: 2}},
		Examples:  []string{"an•••@gm•••.com"}, Fix: "fix", Detail: "d"}
	env := core.Envelope{Schema: core.EnvelopeSchema, Kind: "verify", Tool: "rhythm-roadie", Version: "dev", Complete: true,
		Summary: core.Summary{Critical: 1}, Verdict: "findings", Data: json.RawMessage(`{}`)}
	errJSON := core.Errorf(core.ErrLock, 3, "lock: busy")
	var b strings.Builder
	for _, v := range []any{env, f, core.Location{Table: "t"}, errJSON, core.Finding{ID: "x"}} {
		out, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		b.Write(out)
		b.WriteString("\n")
	}
	testutil.Golden(t, "json.golden", []byte(b.String()))

	var back core.Finding
	raw, _ := json.Marshal(f)
	if err := json.Unmarshal(raw, &back); err != nil || back.Severity != core.SeverityHigh || back.Locations[0].Row != "id=1" {
		t.Fatalf("round trip: %v %+v", err, back)
	}
	if err := json.Unmarshal([]byte(`{"id":"x","severity":"severe"}`), &back); err == nil {
		t.Fatal("bad severity accepted")
	}
}

func TestFixedTypesCompile(t *testing.T) {
	_ = core.Batch{Seq: 1, Pass: &core.Statement{Kind: core.StmtSet}}
	_ = core.Clock(time.Now)
	if core.MaxBatchBytes != 4<<20 || len(core.FakeKinds()) != 17 {
		t.Fatal("constants changed")
	}
}
