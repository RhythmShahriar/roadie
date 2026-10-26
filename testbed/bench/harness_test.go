// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/internal/testutil"
	"github.com/RhythmShahriar/roadie/testbed/manifest"
	"github.com/RhythmShahriar/roadie/testbed/oracle"
)

func intp(n int) *int { return &n }

const envelopeJSON = `{"schema":"rhythm-roadie/v1","kind":"verify","tool":"rhythm-roadie","version":"1.0.0","complete":true,
"summary":{"critical":1,"high":1,"medium":0,"low":1,"info":1},"verdict":"findings","data":{"result":"fail","target_state":"replaced",
"findings":[{"id":"pii.email","severity":"high","title":"t","count":3,"locations":[{"table":"wp_users","column":"user_email","count":3}]},
{"id":"uploads.personal","severity":"critical","title":"t","count":1,"locations":[{"file":"wp-content/debug.log","count":1}]},
{"id":"pii.email-role","severity":"info","title":"t","count":1,"locations":[]},
{"id":"auth.none","severity":"low","title":"t","count":1,"locations":[]}],
"keys":[{"id":"key.stripe-live","env":"live","action":"kept"},{"id":"key.paypal-live","env":"live","action":"swapped"}],
"checks":[{"id":"search.indexable","status":"fail"},{"id":"side-effect.cron","status":"pass"}]}}`

func TestParseEnvelope(t *testing.T) {
	e, err := ParseEnvelope([]byte("note: a stray line\n" + envelopeJSON))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"auth.none", "key.stripe-live", "pii.email", "pii.email-role", "search.indexable", "uploads.personal"}
	if got := e.IDs(); !slices.Equal(got, want) {
		t.Errorf("IDs %q, want %q (a swapped key and a passing check don't count)", got, want)
	}
	if got, want := e.Locations(), []string{"file:wp-content/debug.log", "wp_users.user_email"}; !slices.Equal(got, want) {
		t.Errorf("locations %q, want %q", got, want)
	}
	if n := e.AtOrAbove(core.SeverityLow); n != 3 {
		t.Errorf("%d at or above low, want 3", n)
	}
	if got := e.FindingsAtOrAbove(core.SeverityMedium); !slices.Equal(got, []string{"pii.email", "uploads.personal"}) {
		t.Errorf("at or above medium %q", got)
	}
	if e.ErrorCode() != "" || e.NotBuilt() {
		t.Error("no error in this envelope")
	}
	for _, bad := range []string{"", "usage: rhythm-roadie copy", `{"schema":"rhythm-echo/v1"}`, `{"schema":`} {
		if _, err := ParseEnvelope([]byte(bad)); err == nil {
			t.Errorf("%q: want an error", bad)
		}
	}
	e, err = ParseEnvelope([]byte(`{"schema":"rhythm-roadie/v1","verdict":"error","data":{"error":{"code":"not-built","message":"m","exit":3}}}`))
	if err != nil || !e.NotBuilt() || !slices.Equal(e.IDs(), []string{"not-built"}) {
		t.Errorf("not-built envelope: %v %v", e, err)
	}
	var nilEnv *Envelope
	if nilEnv.ErrorCode() != "" || nilEnv.IDs() != nil || nilEnv.AtOrAbove(core.SeverityLow) != 0 {
		t.Error("a nil envelope has nothing")
	}
}

func TestCheckRun(t *testing.T) {
	env, err := ParseEnvelope([]byte(envelopeJSON))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		expect manifest.Expect
		forbid []string
		fail   string // the gate that must fail, "" for none
	}{
		{"all hold", manifest.Expect{ExitCode: intp(1), FindingIDs: []string{"pii.email", "key.stripe-live"}, TargetState: "replaced",
			Locations: []string{"file:wp-content/debug.log"}}, []string{"exact.survivors"}, ""},
		{"exit", manifest.Expect{ExitCode: intp(0)}, nil, "exit"},
		{"missing ID", manifest.Expect{FindingIDs: []string{"pii.phone"}}, nil, "finding-ids"},
		{"forbidden", manifest.Expect{}, []string{"pii.email"}, "forbid"},
		{"error code", manifest.Expect{ErrorCode: "dump-refused"}, nil, "error-code"},
		{"location", manifest.Expect{Locations: []string{"wp_usermeta.meta_value"}}, nil, "locations"},
		{"state", manifest.Expect{TargetState: "untouched"}, nil, "target-state"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cr := &caseRun{c: manifest.Case{Expect: c.expect, Forbid: c.forbid}}
			(&Harness{}).checkRun(cr, &Run{Exit: 1}, env, nil)
			for _, g := range cr.res.Gates {
				if g.Pass == (g.Name == c.fail) {
					t.Errorf("gate %s pass %v (%s)", g.Name, g.Pass, g.Detail)
				}
			}
			cr.res.score()
			if want := map[bool]string{true: Caught, false: Missed}[c.fail == ""]; cr.res.Outcome != want {
				t.Errorf("outcome %s, want %s", cr.res.Outcome, want)
			}
		})
	}
	// No envelope at all fails a case that expects IDs.
	cr := &caseRun{c: manifest.Case{Expect: manifest.Expect{ExitCode: intp(2), FindingIDs: []string{"pii.email"}}}}
	_, perr := ParseEnvelope([]byte("flag provided but not defined: -json"))
	(&Harness{}).checkRun(cr, &Run{Exit: 2}, nil, perr)
	if cr.res.Gates[0].Name != "envelope" || cr.res.Gates[0].Pass {
		t.Errorf("gates %+v", cr.res.Gates)
	}
}

func TestScore(t *testing.T) {
	pass, fail := Gate{Name: "a", Pass: true}, Gate{Name: "b"}
	for _, c := range []struct {
		cr   CaseResult
		want string
	}{
		{CaseResult{Gates: []Gate{pass}}, Caught},
		{CaseResult{Gates: []Gate{pass, fail}}, Missed},
		{CaseResult{Detail: "setup: docker"}, Errored},
		{CaseResult{}, Errored},
		{CaseResult{Pending: "later", Gates: []Gate{fail}}, Pending},
	} {
		c.cr.score()
		if c.cr.Outcome != c.want {
			t.Errorf("%+v: %s, want %s", c.cr, c.cr.Outcome, c.want)
		}
	}
	for _, c := range []struct {
		outs []string
		want string
	}{
		{[]string{Caught, Caught}, Caught},
		{[]string{Caught, Missed, Errored}, Missed},
		{[]string{Caught, Errored}, Errored},
		{[]string{Caught, Pending}, Caught},
		{[]string{Pending}, Pending},
	} {
		s := ScenarioResult{}
		for _, o := range c.outs {
			s.Cases = append(s.Cases, CaseResult{Outcome: o})
		}
		scoreScenario(&s)
		if s.Outcome != c.want {
			t.Errorf("%v: %s, want %s", c.outs, s.Outcome, c.want)
		}
	}
}

// canned is a run's scored part: two copies (one leaky), two bad verify
// scenarios (one caught) and a control pass with one false alarm.
func canned() (Scored, map[string]manifest.Scenario) {
	sc := Scored{
		Scenarios: []ScenarioResult{
			{ID: "v02", Outcome: Missed, Cases: []CaseResult{{Outcome: Missed}}},
			{ID: "r01", Outcome: Caught, Cases: []CaseResult{{Outcome: Caught, Canaries: 1500, LiveKeys: 2,
				Oracle:     &oracle.Report{Survivors: []oracle.Survivor{}, ByEncoding: map[string]int{}, Integrity: &oracle.Integrity{Checked: 300}},
				Gate3:      &Gate3{DNS: 4},
				OutputHash: "aa"}}},
			{ID: "r04", Outcome: Missed, Cases: []CaseResult{{Outcome: Missed, Canaries: 200, LiveKeys: 1,
				Oracle: &oracle.Report{Survivors: []oracle.Survivor{{ID: "r04-email-0001", Kind: "email", Encoding: "json", Count: 2}, {ID: "r04-email-0001", Kind: "email", Encoding: "raw", Count: 1}, {ID: "r04-email-0002", Kind: "email", Encoding: "raw", Count: 1}, {ID: "r04-key-stripe_live_secret", Kind: "key", Encoding: "raw", Count: 1}},
					ByEncoding: map[string]int{"json": 2, "raw": 3}, LiveKeys: 1, TestKeysMissing: []string{"r04-key-stripe_test_secret"},
					Integrity: &oracle.Integrity{Checked: 10, Failures: 1}},
				Gates: []Gate{{Name: "rows", Pass: false}, {Name: "boot", Pass: false}},
				Gate3: &Gate3{Sinkhole: 2, StubLive: 1, EchoRealRecips: 3}}}},
			{ID: "v01", Outcome: Caught, Cases: []CaseResult{{Outcome: Caught}}},
		},
		Control: Control{Rounds: 5, Checks: 20, FalseAlarms: 1, ByInput: map[string]int{"g01": 1}, IDs: []string{"pii.phone"}},
	}
	kinds := map[string]manifest.Scenario{
		"r01": {ID: "r01", Kind: manifest.KindCopy}, "r04": {ID: "r04", Kind: manifest.KindCopy},
		"v01": {ID: "v01", Kind: manifest.KindVerify}, "v02": {ID: "v02", Kind: manifest.KindVerify},
	}
	return sc, kinds
}

func TestSummarise(t *testing.T) {
	sc, kinds := canned()
	summarize(&sc, kinds)
	s := sc.Summary
	want := Summary{
		Scenarios: 4, Outcomes: map[string]int{Caught: 2, Missed: 2},
		Canaries: 1700, CanarySurvivors: 2, ByEncoding: map[string]int{"json": 2, "raw": 3},
		LiveKeys: 3, LiveKeysSurvived: 1, TestKeysChanged: 1, IntegrityChecked: 310, IntegrityFailures: 1,
		RowsDiffer: 1, Sinkhole: 2, StubLive: 1, EchoReal: 3, BootFailures: 1,
		VerifyRecall: 1, VerifyBad: 2, FalseAlarms: 1, ControlChecks: 20,
	}
	got, _ := jsonOf(s)
	exp, _ := jsonOf(want)
	if got != exp {
		t.Errorf("summary\n got %s\nwant %s", got, exp)
	}
}

func TestResultsAreReproducible(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, date string) string {
		sc, kinds := canned()
		summarize(&sc, kinds)
		r := Results{Version: ResultsVersion, Scored: sc, Run: RunInfo{Meta: Meta{Date: date, WallMS: int64(len(date))},
			Scenarios: map[string]ScenarioRun{"r01": {WallMS: 12345, Cases: map[string]CaseRun{"-": {Gate3: &Gate3{DNS: len(date)}}}}}}}
		p := filepath.Join(dir, name)
		if err := WriteResults(p, r); err != nil {
			t.Fatal(err)
		}
		return p
	}
	a := write("a.json", "2026-10-26T10:00:00Z")
	b := write("b.json", "2026-10-27T11:30:00Z")
	var out bytes.Buffer
	same, err := DiffResults(a, b, &out)
	if err != nil || !same {
		t.Fatalf("two runs with the same score differ: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "4 scenarios, the same score") {
		t.Errorf("output %q", out.String())
	}
	r, err := ReadResults(a)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, s := range r.Scored.Scenarios {
		ids = append(ids, s.ID)
	}
	if want := []string{"r01", "r04", "v01", "v02"}; !slices.Equal(ids, want) {
		t.Errorf("order %q, want %q (PRD 22.2)", ids, want)
	}
	scored, _ := jsonOf(r.Scored)
	if regexp.MustCompile(`20\d\d-\d\d-\d\dT`).MatchString(scored) {
		t.Error("the scored part holds a timestamp")
	}
	if strings.Contains(scored, `"dns"`) {
		t.Error("gate 3's run-to-run counts belong under run, not the score")
	}
	fi, err := os.Stat(a)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("results.json mode %v %v", fi.Mode(), err)
	}

	// A changed gate shows up by case and gate.
	r.Scored.Scenarios[0].Cases[0].Gates = append(r.Scored.Scenarios[0].Cases[0].Gates, Gate{Name: "canaries", Pass: false, Detail: "1 of 1500 survived"})
	r.Scored.Scenarios[0].Cases[0].OutputHash = "bb"
	c := filepath.Join(dir, "c.json")
	if err := WriteResults(c, r); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	same, err = DiffResults(a, c, &out)
	if err != nil || same {
		t.Fatalf("a different score must differ: %v", err)
	}
	for _, want := range []string{"r01 gate canaries: - | false 1 of 1500 survived", "r01 output: aa | bb"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("diff lacks %q:\n%s", want, out.String())
		}
	}
	empty := filepath.Join(dir, "e.json")
	if err := WriteResults(empty, Results{Version: ResultsVersion}); err != nil {
		t.Fatal(err)
	}
	if _, err := DiffResults(a, empty, &out); err == nil {
		t.Error("an empty run proves nothing: want an error")
	}
	if err := os.WriteFile(empty, []byte(`{"version":9}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadResults(empty); err == nil {
		t.Error("an unknown version must be refused")
	}
}

func TestOracleGates(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.sql")
	dump := "CREATE TABLE `contacts` (\n  `id` int NOT NULL,\n  `email` varchar(99),\n  PRIMARY KEY (`id`)\n);\n" +
		"INSERT INTO `contacts` VALUES (1,'a@x.test'),(2,'b@x.test');\n" +
		"CREATE TABLE `deals` (\n  `id` int NOT NULL,\n  `contact_email` varchar(99),\n  PRIMARY KEY (`id`)\n);\n" +
		"INSERT INTO `deals` VALUES (1,'a@x.test'),(2,'b@x.test'),(3,'c@x.test');\n"
	if err := os.WriteFile(in, []byte(dump), 0o600); err != nil {
		t.Fatal(err)
	}
	st := &scenarioState{canaries: []oracle.Canary{{ID: "x-email-0001", Kind: "email"}, {ID: "x-key-a", Kind: "key", Live: true}, {ID: "x-key-b", Kind: "key"}}}
	cr := &caseRun{st: st, c: manifest.Case{Expect: manifest.Expect{Canaries: intp(0), Integrity: true}}}
	rep := oracle.Report{
		Survivors: []oracle.Survivor{{ID: "x-email-0001", Kind: "email", Encoding: "base64-1", Count: 1}},
		Integrity: &oracle.Integrity{Checked: 4},
		// wp_options is the target's own install table: not the input's.
		Rows: []oracle.TableRows{{Table: "contacts", Input: 2, Output: 2}, {Table: "deals", Input: 3, Output: 2}, {Table: "wp_options", Output: 120}},
	}
	(&Harness{}).oracleGates(cr, rep, in)
	got := map[string]Gate{}
	for _, g := range cr.res.Gates {
		got[g.Name] = g
	}
	if g := got["canaries"]; g.Pass || !strings.Contains(g.Detail, "x-email-0001 (base64-1)") {
		t.Errorf("canaries %+v", g)
	}
	if !got["live-keys"].Pass || !got["test-keys"].Pass || !got["integrity"].Pass {
		t.Errorf("gates %+v", cr.res.Gates)
	}
	if g := got["rows"]; g.Pass || g.Detail != "deals 3 to 2" {
		t.Errorf("rows %+v: only the input's tables count", g)
	}
	if cr.res.Canaries != 1 || cr.res.LiveKeys != 1 {
		t.Errorf("counted %d canaries and %d live keys", cr.res.Canaries, cr.res.LiveKeys)
	}
	if _, ok := got["links"]; ok {
		t.Errorf("a links gate with no link: %+v", got["links"])
	}

	// A link in the output tree fails the case: what it points to went
	// unread.
	cr = &caseRun{st: st, c: manifest.Case{Expect: manifest.Expect{Canaries: intp(0)}}}
	(&Harness{}).oracleGates(cr, oracle.Report{Links: []string{"wp-content/uploads/backup.sql"}}, in)
	linked := false
	for _, g := range cr.res.Gates {
		if g.Name == "links" {
			linked = !g.Pass && strings.Contains(g.Detail, "wp-content/uploads/backup.sql")
		}
	}
	if !linked {
		t.Errorf("no failing links gate: %+v", cr.res.Gates)
	}
	for _, g := range cr.res.Gates {
		if strings.Contains(g.Detail, "@") {
			t.Errorf("gate %s carries a value: %s", g.Name, g.Detail)
		}
	}

	n, err := joinCount(in, manifest.Join{Left: "contacts.email", Right: "deals.contact_email"})
	if err != nil || n != 2 {
		t.Errorf("join count %d %v, want 2", n, err)
	}
}

func TestInputDump(t *testing.T) {
	st := &scenarioState{in: "/rt/fixtures/r18"}
	cr := &caseRun{st: st, c: manifest.Case{Command: []string{"copy", "--from", "${in}/latin1.sql", "--to", "x"}}}
	if got := inputDump(cr); got != filepath.Join("/rt/fixtures/r18", "latin1.sql") {
		t.Errorf("got %s", got)
	}
	cr.c.Command = []string{"copy", "--from", "${ssh_source}"}
	if got := inputDump(cr); got != filepath.Join("/rt/fixtures/r18", "db.sql.gz") {
		t.Errorf("got %s (no db.sql there, so the generated one)", got)
	}
}

func TestFinishedDumpAndLeftovers(t *testing.T) {
	work := t.TempDir()
	out := filepath.Join(work, "out.sql")
	st := &scenarioState{in: work}
	cr := &caseRun{st: st, work: work, c: manifest.Case{Command: []string{"copy", "--from", "${in}/db.sql", "--to", "${work}/out.sql"},
		Expect: manifest.Expect{NoFinishedOutput: true}}}
	cr.local = localVars(st, work)
	if err := os.MkdirAll(filepath.Join(work, "tmp"), 0o700); err != nil {
		t.Fatal(err)
	}
	before := targetFiles(cr, nil)
	(&Harness{}).checkLeftovers(cr, nil, before)
	if g := cr.res.Gates[0]; !g.Pass {
		t.Errorf("nothing left: %+v", g)
	}
	_ = os.WriteFile(out, []byte("INSERT INTO t VALUES (1);\n"), 0o600)
	_ = os.WriteFile(filepath.Join(work, "tmp", "roadie-123.spool"), []byte("x"), 0o600)
	cr.res = CaseResult{}
	(&Harness{}).checkLeftovers(cr, nil, before)
	if g := cr.res.Gates[0]; g.Pass || g.Detail != "tmp/roadie-123.spool left behind" {
		t.Errorf("a spool left behind must fail: %+v", g)
	}
	_ = os.WriteFile(out, []byte("INSERT INTO t VALUES (1);\n-- Dump completed on 2026-10-26 10:00:00\n"), 0o600)
	if !finishedDump(out) {
		t.Error("the trailer marks a finished dump")
	}
	cr.res = CaseResult{}
	(&Harness{}).checkLeftovers(cr, nil, before)
	if g := cr.res.Gates[0]; g.Pass || !strings.Contains(g.Detail, "out.sql looks finished") {
		t.Errorf("a finished-looking output must fail: %+v", g)
	}
}

func TestStagingConfig(t *testing.T) {
	h := &Harness{db: map[string]string{"MYSQL_PASSWORD": "pw" + "123"}}
	cfg := stagingConfig(h.dbValues(true, false), "subdomain")
	for _, want := range []string{"define( 'DB_HOST', 'staging-db' );", "define( 'DB_PASSWORD', 'pw123' );", "define( 'WP_ENVIRONMENT_TYPE', 'staging' );",
		"define( 'MULTISITE', true );", "define( 'SUBDOMAIN_INSTALL', true );", "require_once ABSPATH . 'wp-settings.php';"} {
		if !strings.Contains(cfg, want) {
			t.Errorf("config lacks %q", want)
		}
	}
	if strings.Contains(stagingConfig(h.dbValues(false, false), ""), "MULTISITE") {
		t.Error("a single site has no multisite defines")
	}
	if v := h.dbValues(false, true); v["@@DB_HOST@@"] != "127.0.0.1" || v["@@DB_PORT@@"] != "18928" || v["@@DB_NAME@@"] != "laravel" {
		t.Errorf("Laravel through the gate: %v", v)
	}
	got := fill([]byte("DB_HOST=@@DB_HOST@@:@@DB_PORT@@"), h.dbValues(false, false))
	if string(got) != "DB_HOST=127.0.0.1:18927:18927" {
		t.Errorf("fill: %s", got)
	}
}

func TestSiteDir(t *testing.T) {
	in, work := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(in, "files", "wp-content"), 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(in, "files", "wp-content", "debug.log"), []byte("x"), 0o600)
	h := &Harness{db: map[string]string{"MYSQL_PASSWORD": "p"}}
	if err := h.siteDir(&scenarioState{in: in, s: manifest.Scenario{Stack: "woocommerce"}}, work); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(work, "site", "wp-config.php"))
	if err != nil || !bytes.Contains(b, []byte("'127.0.0.1:18927'")) {
		t.Errorf("a WordPress site without its own wp-config.php gets the harness's: %v", err)
	}
	if _, err := os.Stat(filepath.Join(work, "site", "wp-content", "debug.log")); err != nil {
		t.Error(err)
	}
}

func TestMakeFiles(t *testing.T) {
	in := t.TempDir()
	if err := makeFile(in, manifest.MakeWpress); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(in, "archive.sql"))
	if len(b) != 4377*2+len("-- not really a dump\n") || !bytes.HasPrefix(b, []byte("database.sql\x00")) {
		t.Errorf("wpress archive of %d bytes", len(b))
	}
	if testing.Short() {
		t.Skip("the gzip bomb takes a few seconds")
	}
	if err := makeFile(in, manifest.MakeGzipBomb); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(in, "bomb.sql.gz"))
	if err != nil {
		t.Fatal(err)
	}
	if ratio := int64(bombLines<<10) / fi.Size(); ratio < 100 {
		t.Errorf("the bomb inflates only %dx", ratio)
	}
	if err := makeFile(in, "nope"); err == nil {
		t.Error("an unknown make must fail")
	}
}

func TestUntarStaysInside(t *testing.T) {
	var buf bytes.Buffer
	newTar(&buf, map[string]string{"ok/a.txt": "a", "../escape.txt": "x", "/abs.txt": "y"})
	dir := t.TempDir()
	links, err := untar(&buf, filepath.Join(dir, "x"))
	if err != nil || len(links) != 0 {
		t.Fatal(links, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "x", "ok", "a.txt")); err != nil {
		t.Error(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "escape.txt")); err == nil {
		t.Error("a name climbing out was extracted")
	}
}

// Links in a collected tree are skipped and named, never written.
func TestUntarNamesLinks(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, h := range []*tar.Header{
		{Name: "wp-content/uploads/backup.sql", Typeflag: tar.TypeSymlink, Linkname: "/var/backups/prod.sql"},
		{Name: "wp-content/hard", Typeflag: tar.TypeLink, Linkname: "wp-content/a.txt"},
		{Name: "wp-content/a.txt", Typeflag: tar.TypeReg, Size: 1, Mode: 0o600},
	} {
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Size > 0 {
			_, _ = tw.Write([]byte("a"))
		}
	}
	_ = tw.Close()
	dir := t.TempDir()
	links, err := untar(&buf, dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"wp-content/uploads/backup.sql", "wp-content/hard"}; !slices.Equal(links, want) {
		t.Errorf("links %q, want %q", links, want)
	}
	if _, err := os.Lstat(filepath.Join(dir, "wp-content", "uploads", "backup.sql")); err == nil {
		t.Error("a link was written")
	}
	if _, err := os.Stat(filepath.Join(dir, "wp-content", "a.txt")); err != nil {
		t.Error(err)
	}
}

func TestBlockDevice(t *testing.T) {
	for in, want := range map[string]string{"nvme0n1p2": "nvme0n1", "nvme0n1": "nvme0n1", "sda1": "sda", "sdb": "sdb", "mmcblk0p1": "mmcblk0", "dm-0": "dm-"} {
		if got := blockDevice(in); got != want {
			t.Errorf("%s: %s, want %s", in, got, want)
		}
	}
}

func TestStableKey(t *testing.T) {
	h := &Harness{keys: [2]string{"k1", "k2"}, signKey: "s", db: map[string]string{"MYSQL_PASSWORD": "p"}}
	if got := h.withStableKey([]string{"copy", "--from", "a"}); !slices.Contains(got, "--keep-key") {
		t.Errorf("copy gets --keep-key: %q", got)
	}
	if got := h.withStableKey([]string{"verify", "a"}); slices.Contains(got, "--keep-key") {
		t.Errorf("verify takes no key: %q", got)
	}
	env := h.roadieEnv("k1")
	for _, want := range []string{"ROADIE_SIGNING_KEY=s", "ROADIE_KEY=k1", "ROADIE_TO_PASSWORD=p"} {
		if !slices.Contains(env, want) {
			t.Errorf("env lacks %s", want)
		}
	}
	h.PerRun = true
	if got := h.withStableKey([]string{"copy"}); slices.Contains(got, "--keep-key") || slices.Contains(h.roadieEnv("k1"), "ROADIE_KEY=k1") {
		t.Error("-per-run leaves the key per run")
	}
}

func TestSecretFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "rt", "k")
	a, err := secretFile(p)
	if err != nil || len(a) != 44 {
		t.Fatalf("%q %v", a, err)
	}
	fi, _ := os.Stat(p)
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode())
	}
	if b, _ := secretFile(p); b != a {
		t.Error("a second read must give the same key")
	}
}

func TestIsolatedEnv(t *testing.T) {
	env := isolatedEnv("/w/home", "/bin", []string{"X=1"})
	for _, want := range []string{"HOME=/w/home", "XDG_CONFIG_HOME=/w/home/.config", "XDG_CACHE_HOME=/w/home/.cache",
		"RHYTHM_CONFIG_DIR=/w/home/.config/rhythm", "RHYTHM_NO_UPDATE_CHECK=1", "RHYTHM_OFFLINE=1", "PATH=/bin", "X=1"} {
		if !slices.Contains(env, want) {
			t.Errorf("env lacks %s", want)
		}
	}
}

func TestNeedsDocker(t *testing.T) {
	f, err := manifest.Load("../scenarios.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if needsDocker(f, []string{"g01", "g02", "v01"}) {
		t.Error("verify of seeded dumps needs no Docker")
	}
	for _, id := range []string{"r01", "g04", "r18", "g05"} {
		if !needsDocker(f, []string{id}) {
			t.Errorf("%s needs the testbed", id)
		}
	}
}

// fakeRoadie writes a stand-in rhythm-roadie that prints envelope and
// exits with code, for runs of the harness without the real binary.
func fakeRoadie(t *testing.T, envelope string, code int) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "rhythm-roadie")
	script := "#!/bin/sh\ncat <<'EOF'\n" + envelope + "\nEOF\nexit " + string(rune('0'+code)) + "\n"
	if err := os.WriteFile(p, []byte(script), 0o700); err != nil { // #nosec G306 -- a test script
		t.Fatal(err)
	}
	return p
}

// The whole harness on the control inputs, with a stand-in binary: a
// clean verify is caught five times over; one finding at low is a false
// alarm in every round.
func TestRunAllControl(t *testing.T) {
	if testing.Short() {
		t.Skip("seeds fixtures")
	}
	f, err := manifest.Load("../scenarios.yaml")
	if err != nil {
		t.Fatal(err)
	}
	clean := `{"schema":"rhythm-roadie/v1","kind":"verify","verdict":"ok","data":{"result":"pass","findings":[]}}`
	noisy := `{"schema":"rhythm-roadie/v1","kind":"verify","verdict":"findings","data":{"result":"fail","findings":[{"id":"pii.phone","severity":"low","title":"t","count":1,"locations":[]}]}}`
	for _, c := range []struct {
		name, envelope string
		code           int
		outcome        string
		alarms         int
	}{
		{"clean", clean, 0, Caught, 0},
		{"noisy", noisy, 0, Missed, 10},
	} {
		t.Run(c.name, func(t *testing.T) {
			rt := t.TempDir()
			h := &Harness{Bin: fakeRoadie(t, c.envelope, c.code), Runtime: rt, Fixtures: "../fixtures", Work: filepath.Join(t.TempDir(), "work"), Seed: 1, Rounds: 5}
			if err := h.Init(context.Background()); err != nil {
				t.Fatal(err)
			}
			res := h.RunAll(context.Background(), f, []string{"g01", "g02"})
			if len(res.Scored.Scenarios) != 2 {
				t.Fatalf("%d scenarios", len(res.Scored.Scenarios))
			}
			for _, s := range res.Scored.Scenarios {
				if s.Outcome != c.outcome {
					t.Errorf("%s: %s, want %s (%+v)", s.ID, s.Outcome, c.outcome, s.Cases)
				}
			}
			ctl := res.Scored.Control
			if ctl.Checks != 10 || ctl.FalseAlarms != c.alarms || ctl.Rounds != 5 {
				t.Errorf("control %+v", ctl)
			}
			if _, err := os.Stat(filepath.Join(rt, "fixtures", "g01", "db.sql")); err != nil {
				t.Error("the input wasn't reseeded:", err)
			}
			for _, name := range []string{"signing.key", "roadie.key", "roadie-other.key"} {
				if fi, err := os.Stat(filepath.Join(rt, name)); err != nil || fi.Mode().Perm() != 0o600 {
					t.Errorf("%s: %v", name, err)
				}
			}
		})
	}
}

// No fixture template or harness file may hold a literal live key: the
// harness's own fakes come from testutil.J.
func TestNoLiteralKeys(t *testing.T) {
	re := regexp.MustCompile(`\b[spr]k_(live|test)_[0-9A-Za-z]{8,}`)
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		b, _ := os.ReadFile(f)
		if m := re.Find(b); m != nil {
			t.Errorf("%s holds a literal key %s", f, core.MaskKey(string(m)))
		}
	}
	if !re.MatchString(testutil.J("sk_", "live_", "abcdefgh123")) {
		t.Error("the pattern misses a key")
	}
}

func jsonOf(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

// newTar writes a tar of name to content into w.
func newTar(w io.Writer, files map[string]string) *tar.Writer {
	tw := tar.NewWriter(w)
	for name, body := range files {
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(body)), Typeflag: tar.TypeReg})
		_, _ = tw.Write([]byte(body))
	}
	_ = tw.Close()
	return tw
}
