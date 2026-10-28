// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/internal/testutil"
)

// indent writes an envelope as the saved report.json does.
func indent(t *testing.T, e *core.Envelope) []byte {
	t.Helper()
	b, err := encodeEnvelope(e)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func mustEnvelope(t *testing.T, r *core.Result) *core.Envelope {
	t.Helper()
	e, err := Envelope(r, "rhythm-roadie", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// The envelope of each kind of run is golden and the same on every
// build: maps and lists come out in one order.
func TestEnvelopeGolden(t *testing.T) {
	for name, r := range map[string]func() *core.Result{
		"envelope-copy-pass.json":   copyPass,
		"envelope-verify-fail.json": verifyFail,
		"envelope-copy-error.json":  copyError,
	} {
		t.Run(name, func(t *testing.T) {
			first := indent(t, mustEnvelope(t, r()))
			for range 3 {
				if again := indent(t, mustEnvelope(t, r())); !bytes.Equal(again, first) {
					t.Fatalf("the envelope changed between builds:\n%s\n%s", first, again)
				}
			}
			testutil.Golden(t, name, first)
		})
	}
}

func TestEnvelopeVerdicts(t *testing.T) {
	cases := []struct {
		name             string
		r                *core.Result
		verdict, result  string
		complete         bool
		kind, verifyMode string
	}{
		{"copy pass", copyPass(), VerdictOK, ResultPass, true, "copy", "exact"},
		{"verify fail", verifyFail(), VerdictFindings, ResultFail, true, "verify", "heuristic"},
		{"copy error", copyError(), VerdictError, ResultError, false, "copy", "none"},
	}
	sampled := copyPass()
	sampled.Verify.Coverage.Sample = 1000
	cases = append(cases, struct {
		name             string
		r                *core.Result
		verdict, result  string
		complete         bool
		kind, verifyMode string
	}{"sampled", sampled, VerdictOK, ResultPass, false, "copy", "exact"})
	skipped := copyPass()
	skipped.Verify.Coverage.SkippedChecks = []string{"emails", "keys"}
	cases = append(cases, struct {
		name             string
		r                *core.Result
		verdict, result  string
		complete         bool
		kind, verifyMode string
	}{"data checks skipped", skipped, VerdictOK, ResultPass, false, "copy", "exact"})
	nothing := verifyFail()
	nothing.Verify.Coverage.RowsScanned = 0
	nothing.FailOn = core.SeverityNone
	cases = append(cases, struct {
		name             string
		r                *core.Result
		verdict, result  string
		complete         bool
		kind, verifyMode string
	}{"nothing scanned", nothing, VerdictError, ResultError, false, "verify", "heuristic"})
	unread := copyPass()
	unread.Verify.Coverage.Skipped = []core.Skipped{{Table: core.SkippedInput + " after wp_users)", Reason: "dump-truncated"}}
	cases = append(cases, struct {
		name             string
		r                *core.Result
		verdict, result  string
		complete         bool
		kind, verifyMode string
	}{"input not read whole", unread, VerdictError, ResultError, false, "copy", "exact"})
	for _, c := range cases {
		e := mustEnvelope(t, c.r)
		d, err := ParseData(e)
		if err != nil {
			t.Fatal(err)
		}
		if e.Verdict != c.verdict || d.Result != c.result || e.Complete != c.complete || e.Kind != c.kind || d.VerifyMode != c.verifyMode {
			t.Errorf("%s: verdict %s result %s complete %v kind %s mode %s", c.name, e.Verdict, d.Result, e.Complete, e.Kind, d.VerifyMode)
		}
		if e.Schema != core.EnvelopeSchema || e.Tool != "rhythm-roadie" || e.Version != "1.0.0" {
			t.Errorf("%s: %s %s %s", c.name, e.Schema, e.Tool, e.Version)
		}
		if e.Summary != c.r.Summary() {
			t.Errorf("%s: summary %+v", c.name, e.Summary)
		}
	}
}

// What the CLI prints once and prepare's backup path never reach a
// report.
func TestEnvelopeLeavesOutScreenOnlyFields(t *testing.T) {
	b := indent(t, mustEnvelope(t, copyPass()))
	if bytes.Contains(b, []byte("never-in-a-report")) {
		t.Errorf("a screen-only field is in the report:\n%s", b)
	}
	if bytes.Contains(b, []byte(liveKey())) {
		t.Error("the raw key is in the report")
	}
}

// prdData is every data field PRD 16.2 lists, with the members of its
// objects; v1 adds coverage.skipped_checks.
var prdData = map[string][]string{
	"":             {"result", "verify_mode", "fail_on", "started", "duration_ms", "source", "target", "target_state", "production", "url", "stack", "consistency", "input", "output", "ruleset_sha256", "config_sha256", "profile_pack", "echo_connector_sha256", "tables", "covered", "uncovered", "keys", "side_effects", "findings", "checks", "coverage", "remote_commands", "not_covered", "soundcheck"},
	"source":       {"kind", "display", "engine", "server", "bytes"},
	"target":       {"kind", "display", "engine", "server", "bytes", "state"},
	"stack":        {"name", "profiles", "multisite", "hpos", "sites"},
	"consistency":  {"mode", "key_fingerprint"},
	"input":        {"kind", "value", "bytes"},
	"tables":       {"name", "rows", "action", "profile", "columns"},
	"columns":      {"name", "rule", "changed", "kept"},
	"uncovered":    {"table", "column", "rate"},
	"keys":         {"id", "severity", "provider", "env", "location", "masked", "action"},
	"side_effects": {"id", "status", "how", "undo"},
	"findings":     {"id", "severity", "title", "count", "locations", "examples", "fix"},
	"locations":    {"table", "column", "row", "path", "file", "line", "count"},
	"checks":       {"id", "status", "checked"},
	"coverage":     {"tables_scanned", "tables_total", "rows_scanned", "bytes_scanned", "skipped", "sample", "skipped_checks"},
	"soundcheck":   {"ran", "exit_code", "schema", "counts"},
	"error":        {"code", "message", "exit"},
	"summary":      {"critical", "high", "medium", "low", "info", "tables", "tables_pseudonymised", "rows", "values_pseudonymised", "urls_rewritten", "keys_found", "keys_swapped", "keys_blanked", "side_effects_off", "side_effects_total", "uncovered_columns"},
	"envelope":     {"schema", "kind", "tool", "version", "complete", "summary", "verdict", "data"},
}

// extraData are the fields the envelope writes beyond the PRD list:
// added in v1 as fields only ever are.
var extraData = map[string][]string{
	"checks":     {"detail"},
	"findings":   {"detail"},
	"soundcheck": {"note"},
	"error":      {"backup", "restore"},
}

// The field names are the PRD's: every one is written and nothing else
// is, so a renamed field fails here before a reader breaks.
func TestEnvelopeFieldNames(t *testing.T) {
	var env map[string]any
	if err := json.Unmarshal(indent(t, mustEnvelope(t, verifyFail())), &env); err != nil {
		t.Fatal(err)
	}
	keys := func(v any) []string {
		m, _ := v.(map[string]any)
		out := make([]string, 0, len(m))
		for k := range m {
			out = append(out, k)
		}
		slices.Sort(out)
		return out
	}
	first := func(v any) any {
		if a, ok := v.([]any); ok && len(a) > 0 {
			return a[0]
		}
		return v
	}
	want := func(name string) []string {
		out := append(slices.Clone(prdData[name]), extraData[name]...)
		slices.Sort(out)
		return out
	}
	data := env["data"].(map[string]any)
	// The fixture leaves some fields empty; fill the ones whose members
	// the test reads from another fixture.
	pass := map[string]any{}
	b, _ := json.Marshal(mustEnvelope(t, copyPass()))
	_ = json.Unmarshal(b, &pass)
	passData := pass["data"].(map[string]any)
	got := map[string]any{
		"envelope":     env,
		"summary":      env["summary"],
		"":             data,
		"source":       passData["source"],
		"target":       data["target"],
		"stack":        data["stack"],
		"consistency":  data["consistency"],
		"input":        data["input"],
		"tables":       first(passData["tables"]),
		"columns":      first(first(passData["tables"]).(map[string]any)["columns"]),
		"uncovered":    first(data["uncovered"]),
		"keys":         first(data["keys"]),
		"side_effects": first(passData["side_effects"]),
		"findings":     first(data["findings"]),
		"checks":       first(passData["checks"]),
		"coverage":     data["coverage"],
		"soundcheck":   data["soundcheck"],
	}
	for name, v := range got {
		g, w := keys(v), want(name)
		if name == "checks" || name == "findings" || name == "soundcheck" {
			// detail and note are omitted when empty.
			g = slices.DeleteFunc(g, func(k string) bool { return slices.Contains(extraData[name], k) })
			w = slices.DeleteFunc(w, func(k string) bool { return slices.Contains(extraData[name], k) })
		}
		if !slices.Equal(g, w) {
			t.Errorf("%q: fields %v, want %v", name, g, w)
		}
	}
	// Every location member the PRD names is written when set.
	var loc map[string]any
	lb, _ := json.Marshal(core.LocationCount{Location: core.Location{File: "f", Line: 1, Table: "t", Column: "c", Row: "ID=1", Path: "json:a"}, Count: 1})
	_ = json.Unmarshal(lb, &loc)
	if g, w := keys(loc), want("locations"); !slices.Equal(g, w) {
		t.Errorf("locations: %v, want %v", g, w)
	}
	var errEnv map[string]any
	_ = json.Unmarshal(indent(t, mustEnvelope(t, copyError())), &errEnv)
	// backup and restore are omitted when prepare took no backup.
	notExtra := func(k string) bool { return slices.Contains(extraData["error"], k) }
	if g, w := keys(errEnv["data"].(map[string]any)["error"]), slices.DeleteFunc(want("error"), notExtra); !slices.Equal(g, w) {
		t.Errorf("error: %v, want %v", g, w)
	}
	// not_covered is always there, starting with PRD 19.1's list.
	nc := data["not_covered"].([]any)
	if len(nc) != len(notCoveredItems)+1 || nc[len(nc)-1] != "uploads on the staging host" {
		t.Errorf("not_covered: %v", nc)
	}
	if data["consistency"].(map[string]any)["key_fingerprint"] != "roadie-key:ABCDEFGHJKLM" ||
		passData["consistency"].(map[string]any)["key_fingerprint"] != nil {
		t.Error("key_fingerprint: the stable key's fingerprint, or null for a per-run key")
	}
	if passData["started"] != "2026-10-26T14:02:11Z" || passData["duration_ms"] != float64(82400) {
		t.Errorf("started %v duration %v", passData["started"], passData["duration_ms"])
	}
}

// A prepare that stopped after its backup names the backup and its
// restore command in data.error: it may be the one copy of the target as
// it was. A run that finished keeps them out of the report.
func TestEnvelopeErrorNamesBackup(t *testing.T) {
	r := copyError()
	r.Kind, r.TargetState = "prepare", core.Mixed
	r.Backup, r.Restore = "/srv/x/.roadie/backups/2026-10-28T15-05-04.sql.gz", "gunzip -c /srv/x/.roadie/backups/2026-10-28T15-05-04.sql.gz | mysql staging"
	var env struct {
		Data struct {
			Error map[string]any `json:"error"`
		} `json:"data"`
	}
	if err := json.Unmarshal(indent(t, mustEnvelope(t, r)), &env); err != nil {
		t.Fatal(err)
	}
	e := env.Data.Error
	if e["backup"] != r.Backup || e["restore"] != r.Restore || e["code"] != r.Err.Code || e["message"] != r.Err.Msg {
		t.Errorf("data.error %v", e)
	}
	// Read back, the report keeps them.
	b, _ := json.Marshal(mustEnvelope(t, r))
	var back struct {
		Data Data `json:"data"`
	}
	if err := json.Unmarshal(b, &back); err != nil || back.Data.Error == nil || back.Data.Error.Backup != r.Backup || back.Data.Error.Code != r.Err.Code {
		t.Errorf("read back %+v %v", back.Data.Error, err)
	}
}

// An empty result still writes every list as [], never null.
func TestEnvelopeEmptyLists(t *testing.T) {
	b := indent(t, mustEnvelope(t, &core.Result{Kind: "plan"}))
	for _, f := range []string{"production", "tables", "covered", "uncovered", "keys", "side_effects", "findings", "checks", "remote_commands"} {
		if !bytes.Contains(b, []byte(`"`+f+`": []`)) {
			t.Errorf("%s is not []:\n%s", f, b)
		}
	}
	if !bytes.Contains(b, []byte(`"not_covered": [`)) || !bytes.Contains(b, []byte(`"coverage": null`)) {
		t.Errorf("not_covered or coverage:\n%s", b)
	}
	if _, err := Envelope(nil, "rhythm-roadie", "dev"); err == nil {
		t.Error("a nil result gave an envelope")
	}
}

func TestReadEnvelope(t *testing.T) {
	b := indent(t, mustEnvelope(t, verifyFail()))
	e, err := ReadEnvelope(b)
	if err != nil {
		t.Fatal(err)
	}
	d, err := ParseData(e)
	if err != nil || len(d.Findings) != 3 || d.Findings[0].Severity != core.SeverityHigh {
		t.Fatalf("%v %+v", err, d)
	}
	for _, bad := range []string{`{`, `{"schema":"rhythm-echo/v1"}`, `[]`} {
		if _, err := ReadEnvelope([]byte(bad)); err == nil {
			t.Errorf("%s read as a report", bad)
		}
	}
}

func TestCanonical(t *testing.T) {
	cases := []struct{ in, want string }{
		{`{"b":1,"a":{"d":[3,2],"c":null}}`, `{"a":{"c":null,"d":[3,2]},"b":1}`},
		{"{ \"z\" : true ,\n\"y\":false}", `{"y":false,"z":true}`},
		// No HTML escaping; U+2028 is written as it is.
		{`{"t":"<script>& "}`, "{\"t\":\"<script>& \"}"},
		{`"<\/"`, `"</"`},
		// Only the required escapes: control characters, quote and backslash.
		{`"a\tb\nc\u0001\"\\"`, `"a\tb\nc\u0001\"\\"`},
		{`"\u007f é"`, "\"\u007f é\""},
		// Keys sort bytewise, so "B" comes before "a" and "é" last.
		{`{"é":1,"a":2,"B":3}`, `{"B":3,"a":2,"é":1}`},
		{`-0`, `0`},
		{`[]`, `[]`},
		{`{}`, `{}`},
	}
	for _, c := range cases {
		got, err := canonicalJSON([]byte(c.in))
		if err != nil || string(got) != c.want {
			t.Errorf("canonical(%s) = %s, %v; want %s", c.in, got, err, c.want)
		}
	}
	for _, bad := range []string{`1.5`, `{"a":1e3}`, `{"a":1,"a":2}`, `{"a":1} x`, `{`, ``, "\"\xff\"", `99999999999999999999`, strings.Repeat("[", 300) + strings.Repeat("]", 300)} {
		if got, err := canonicalJSON([]byte(bad)); err == nil {
			t.Errorf("canonical(%q) = %s, want an error", bad, got)
		}
	}
	// Canonical marshals Go values first.
	got, err := Canonical(map[string]any{"b": "<&>", "a": []int{1}})
	if err != nil || string(got) != `{"a":[1],"b":"<&>"}` {
		t.Errorf("Canonical = %s, %v", got, err)
	}
	if _, err := Canonical(map[string]float64{"x": 0.5}); err == nil {
		t.Error("a float was accepted")
	}
	// A whole float marshals as an integer; it is refused all the same,
	// in a struct, behind a pointer, in a list or in an interface.
	type withFloat struct{ N *float64 }
	three := 3.0
	for _, v := range []any{3.0, float32(2), withFloat{&three}, []any{1, 2.0}, map[string]any{"a": map[string]any{"b": 4.0}}} {
		if got, err := Canonical(v); err == nil {
			t.Errorf("Canonical(%#v) = %s, want an error", v, got)
		}
	}
	if _, err := Canonical(func() {}); err == nil {
		t.Error("a func was accepted")
	}
}

// Canonical JSON is a fixed point: canonicalising it again changes
// nothing; it reads as the same value as the input.
func FuzzCanonical(f *testing.F) {
	for _, s := range []string{`{"b":1,"a":[true,false,null,"x"]}`, `"< >"`, `{"a":{"b":{"c":-12}}}`, `[1,2,3]`, `{"é":"\t"}`, `{"a":1,"a":2}`} {
		f.Add([]byte(s))
	}
	r := copyPass()
	r.Verify.Coverage.Sample = 0
	e, _ := Envelope(r, "rhythm-roadie", "1.0.0")
	if b, err := json.Marshal(e); err == nil {
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		c1, err := canonicalJSON(in)
		if err != nil {
			return
		}
		if !json.Valid(c1) {
			t.Fatalf("canonical output isn't JSON: %q", c1)
		}
		c2, err := canonicalJSON(c1)
		if err != nil {
			t.Fatalf("canonical output doesn't parse again: %v", err)
		}
		if !bytes.Equal(c1, c2) {
			t.Fatalf("not a fixed point:\n%q\n%q", c1, c2)
		}
		a, _ := parseJSON(in)
		b, _ := parseJSON(c1)
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("canonical changed the value:\n%v\n%v", a, b)
		}
	})
}

// A saved report of a verify that couldn't read part of its input reads
// INCOMPLETE; one that stopped reads STOPPED.
func TestViewVerdictIncomplete(t *testing.T) {
	unread := copyPass()
	unread.Verify.Coverage.Skipped = []core.Skipped{{Table: core.SkippedInput + " after wp_users)", Reason: "dump-truncated"}}
	r, err := readReport(mustEnvelope(t, unread))
	if err != nil {
		t.Fatal(err)
	}
	if word, _ := r.verdict(); word != "INCOMPLETE" {
		t.Errorf("verdict %q", word)
	}
	r, err = readReport(mustEnvelope(t, copyError()))
	if err != nil {
		t.Fatal(err)
	}
	if word, _ := r.verdict(); word != "STOPPED" {
		t.Errorf("stopped run: verdict %q", word)
	}
}
