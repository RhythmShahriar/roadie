// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package seed

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/internal/testutil"
	"github.com/RhythmShahriar/roadie/testbed/oracle"
)

const fixturesDir = "../fixtures"

func seedTo(t *testing.T, dir string, o Options) *Result {
	t.Helper()
	o.Fixtures, o.Out = fixturesDir, dir
	res, err := Run(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func readCanaries(t *testing.T, dir string) []Canary {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "canaries.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f canaryFile
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	if f.Version != oracle.CanaryFileVersion {
		t.Fatalf("canaries.json version %d, the oracle reads %d", f.Version, oracle.CanaryFileVersion)
	}
	return f.Canaries
}

// Every fixture says what it is, has an input and plants canaries exactly
// when it says it does; together they plant 2,000 or more.
func TestManifest(t *testing.T) {
	fixtures, err := Fixtures(fixturesDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(fixtures) == 0 {
		t.Fatal("no fixtures")
	}
	dir := t.TempDir()
	res := seedTo(t, dir, Options{Seed: 1})
	people := map[string]int{}
	for _, c := range res.Canaries {
		if c.Kind != "key" {
			people[c.Scenario]++
		}
	}
	total := 0
	for _, f := range fixtures {
		if f.Meta.Description == "" || f.Meta.Stack == "" {
			t.Errorf("%s: fixture.yaml needs a description and a stack", f.ID)
		}
		if f.Meta.Dialect != "mysql" && f.Meta.Dialect != "postgres" {
			t.Errorf("%s: dialect %q", f.ID, f.Meta.Dialect)
		}
		dumps, _ := filepath.Glob(filepath.Join(f.Dir, "*.sql.tmpl"))
		if _, err := os.Stat(filepath.Join(f.Dir, "files")); len(dumps) == 0 && err != nil {
			t.Errorf("%s: neither a *.sql.tmpl nor a files/ tree", f.ID)
		}
		if f.Meta.Canaries && people[f.ID] == 0 {
			t.Errorf("%s: says it plants canaries and plants none", f.ID)
		}
		if !f.Meta.Canaries && people[f.ID] > 0 {
			t.Errorf("%s: a clean fixture plants %d canaries", f.ID, people[f.ID])
		}
		total += people[f.ID]
	}
	if total < 2000 {
		t.Errorf("%d canaries across the fixtures, aim for 2,000 or more", total)
	}
	ids := map[string]bool{}
	for _, c := range res.Canaries {
		if ids[c.ID] {
			t.Errorf("canary ID %s twice", c.ID)
		}
		ids[c.ID] = true
		if c.Kind != "key" && (c.Table == "" || c.Column == "") {
			t.Errorf("%s: no table and column", c.ID)
		}
	}
}

// literalKey matches the live- and test-format keys push protection and
// scanners know. A template asks for keys with {{key "<name>"}} instead.
var literalKey = regexp.MustCompile(`\b(?:[spr]k_(?:live|test)_[0-9A-Za-z]{8,}|whsec_[0-9A-Za-z]{8,}|AKIA[0-9A-Z]{16}|SG\.[\w-]{16,}\.[\w-]{16,}|key-[0-9a-f]{32}|AC[0-9a-f]{32})\b`)

func TestNoLiteralKeysInTemplates(t *testing.T) {
	n := 0
	for _, root := range []string{fixturesDir, "."} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			n++
			if m := literalKey.Find(b); m != nil {
				t.Errorf("%s holds a literal key %s", path, core.MaskKey(string(m)))
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if n < 5 {
		t.Fatalf("checked only %d files", n)
	}
	// The pattern itself catches what it must.
	for _, k := range []string{testutil.J("sk_", "live_", "abcdefgh1234"), testutil.J("AK", "IA", "ABCDEFGHIJKLMNOP")} {
		if !literalKey.MatchString(k) {
			t.Errorf("pattern misses %s", core.MaskKey(k))
		}
	}
}

func dirBytes(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		b, err := os.ReadFile(path)
		out[rel] = b
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRunIsDeterministic(t *testing.T) {
	a, b, c := t.TempDir(), t.TempDir(), t.TempDir()
	seedTo(t, a, Options{Seed: 1})
	seedTo(t, b, Options{Seed: 1})
	seedTo(t, c, Options{Seed: 2})
	ab, bb, cb := dirBytes(t, a), dirBytes(t, b), dirBytes(t, c)
	if len(ab) == 0 || len(ab) != len(bb) {
		t.Fatalf("%d and %d files", len(ab), len(bb))
	}
	for name, data := range ab {
		if !bytes.Equal(data, bb[name]) {
			t.Errorf("%s differs between two runs with one seed", name)
		}
	}
	if bytes.Equal(ab["canaries.json"], cb["canaries.json"]) {
		t.Error("another seed gave the same canaries")
	}

	// Reseeding one fixture leaves the others' canaries and gives the
	// same values again.
	before := readCanaries(t, a)
	seedTo(t, a, Options{Seed: 1, Only: []string{"r04"}})
	if after := readCanaries(t, a); !slices.EqualFunc(before, after, func(x, y Canary) bool { return x.ID == y.ID && x.Value == y.Value }) {
		t.Error("reseeding r04 changed the canaries")
	}
	if _, err := Run(context.Background(), Options{Fixtures: fixturesDir, Out: a, Only: []string{"nope"}}); err == nil {
		t.Error("an unknown fixture ID is accepted")
	}
}

func TestRuntimeFilesArePrivate(t *testing.T) {
	dir := t.TempDir()
	seedTo(t, dir, Options{Seed: 1, Only: []string{"r04"}})
	for _, p := range []string{"canaries.json", "fixtures/r04/db.sql"} {
		st, err := os.Stat(filepath.Join(dir, p))
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s is %v, want 0600", p, st.Mode().Perm())
		}
	}
}

// Canaries look real in every way verify checks: a positive and a
// negative case per kind.
func TestCanaryValues(t *testing.T) {
	g := newGenerator(1, "t")
	for _, kind := range Kinds {
		for range 200 {
			v, err := g.value(kind)
			if err != nil {
				t.Fatal(err)
			}
			if len(v) < 8 {
				t.Fatalf("%s %q is shorter than the oracle's window", kind, v)
			}
			switch kind {
			case "email":
				_, domain, _ := strings.Cut(v, "@")
				if core.ReservedEmailDomain(domain) || v != strings.ToLower(v) {
					t.Fatalf("email %s", core.MaskEmail(v))
				}
			case "phone":
				if core.FictionalPhone(v) {
					t.Fatalf("phone in a fictional range: %s", core.MaskPhone(v))
				}
			case "ip":
				a := netip.MustParseAddr(v)
				if a.IsPrivate() || a.IsLoopback() || core.DocumentationIP(a) || !a.IsGlobalUnicast() {
					t.Fatalf("ip %s is not public", core.MaskIP(v))
				}
			case "card":
				if !luhnValid(v) || core.TestCard(v) || len(v) != 16 {
					t.Fatalf("card %s", core.MaskKey(v))
				}
			}
		}
	}
	// The checks above can fail.
	if !core.FictionalPhone("+44 7700 900123") || !core.TestCard(testutil.J("4242", "4242", "4242", "4242")) || luhnValid("4111111111111112") {
		t.Fatal("a negative case passed")
	}
	if _, err := g.value("shoe_size"); err == nil {
		t.Error("an unknown kind is accepted")
	}
}

func TestKeys(t *testing.T) {
	g := newGenerator(1, "t")
	prefixes := map[string]string{
		"stripe_live_secret": testutil.J("sk", "_live_"), "stripe_test_secret": testutil.J("sk", "_test_"),
		"stripe_live_publishable": testutil.J("pk", "_live_"), "aws_access_key": testutil.J("AK", "IA"),
	}
	for _, name := range KeyNames() {
		v, live, err := g.key(name)
		if err != nil || len(v) < 16 {
			t.Fatalf("%s: %v (%d bytes)", name, err, len(v))
		}
		if p := prefixes[name]; p != "" && !strings.HasPrefix(v, p) {
			t.Errorf("%s: %s lacks its prefix", name, core.MaskKey(v))
		}
		if strings.Contains(name, "test") || strings.Contains(name, "sandbox") {
			if live {
				t.Errorf("%s is marked live", name)
			}
		} else if !live {
			t.Errorf("%s is not marked live", name)
		}
	}
	if _, _, err := g.key("nope"); err == nil {
		t.Error("an unknown key name is accepted")
	}
}

func TestTemplateEncodings(t *testing.T) {
	dir := t.TempDir()
	fx := filepath.Join(dir, "fixtures", "t01")
	if err := os.MkdirAll(filepath.Join(fx, "files", "wp-content"), 0o750); err != nil {
		t.Fatal(err)
	}
	write := func(p, s string) {
		if err := os.WriteFile(filepath.Join(fx, p), []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("fixture.yaml", "description: test\nstack: wordpress\ncanaries: true\n")
	write("db.sql.tmpl", "{{$e := canary \"email\" \"e\" \"wp_users.user_email\"}}"+
		"{{b64 \"x\" $e}} {{b64url \"xy\" $e}} {{jsonslash $e}} {{url $e}} {{html $e}} {{htmlnum $e}} {{md5 $e}} {{sha256 $e}} {{upper $e}} {{ser $e}}\n"+
		"{{sql (sarr \"ip\" (ser (canary \"ip\" \"i\" \"wp_usermeta.meta_value\")))}}\n{{key \"stripe_test_secret\"}}\n")
	write("files/wp-content/debug.log.tmpl", "{{canary \"phone\" \"p\" \"files.debug_log\"}}\n")
	write("files/plain.txt", "{{not a template}}\n")
	res, err := Run(context.Background(), Options{Fixtures: filepath.Join(dir, "fixtures"), Out: filepath.Join(dir, "out"), Seed: 3})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, c := range res.Canaries {
		got[c.Kind] = c.Encodings
	}
	want := map[string][]string{
		"email": {"base64-1", "base64url-2", "html", "html-entity", "json-slash", "md5", "serialized", "sha256", "upper", "url"},
		"ip":    {"serialized"},
		"phone": {"raw"},
		"key":   {"raw"},
	}
	for k, w := range want {
		if !slices.Equal(got[k], w) {
			t.Errorf("%s encodings %v, want %v", k, got[k], w)
		}
	}
	plain, err := os.ReadFile(filepath.Join(dir, "out", "fixtures", "t01", "files", "plain.txt"))
	if err != nil || string(plain) != "{{not a template}}\n" {
		t.Errorf("a file without .tmpl is not copied as it is: %q %v", plain, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "out", "fixtures", "t01", "files", "wp-content", "debug.log")); err != nil {
		t.Errorf("rendered file: %v", err)
	}
	write("db.sql.tmpl", "{{canary \"shoe\" \"x\"}}")
	if _, err := Run(context.Background(), Options{Fixtures: filepath.Join(dir, "fixtures"), Out: filepath.Join(dir, "out")}); err == nil {
		t.Error("a template asking for an unknown kind seeds")
	}
}

// The oracle finds every canary the seeder planted, in each input as
// seeded, so a canary the oracle can't see could never pass for removed;
// the serialized list replays clean on the input itself.
func TestOracleFindsSeededCanaries(t *testing.T) {
	php := testutil.RequirePHP(t)
	dir := t.TempDir()
	res := seedTo(t, dir, Options{Seed: 1, PHP: oracle.PHP{Cmd: []string{php}}})
	fixtures, err := Fixtures(fixturesDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fixtures {
		var cs []oracle.Canary
		for _, c := range res.Canaries {
			if c.Scenario == f.ID {
				cs = append(cs, oracle.Canary{ID: c.ID, Kind: c.Kind, Scenario: c.Scenario, Value: c.Value, Live: c.Live})
			}
		}
		out := filepath.Join(dir, "fixtures", f.ID)
		found := map[string]map[string]bool{}
		dumps, _ := filepath.Glob(filepath.Join(out, "*.sql"))
		tree := filepath.Join(out, "files")
		if _, err := os.Stat(tree); err != nil {
			tree = ""
		}
		for _, d := range dumps {
			rep, err := oracle.Run(context.Background(), oracle.Options{Canaries: cs, Dump: d, Tree: tree})
			if err != nil {
				t.Fatal(err)
			}
			for _, s := range rep.Survivors {
				if found[s.ID] == nil {
					found[s.ID] = map[string]bool{}
				}
				found[s.ID][s.Encoding] = true
			}
		}
		for _, c := range res.Canaries {
			if c.Scenario != f.ID || (c.Kind == "key" && !c.Live) {
				continue
			}
			if len(found[c.ID]) == 0 {
				t.Errorf("%s: the oracle does not find %s in the seeded input", f.ID, c.ID)
			}
			for _, enc := range c.Encodings {
				if (enc == "md5" || enc == "sha256") && !found[c.ID][enc] {
					t.Errorf("%s: %s planted as %s, found as %v", f.ID, c.ID, enc, found[c.ID])
				}
			}
		}
		if _, err := os.Stat(filepath.Join(out, "db.sql")); err != nil || f.Meta.Dialect != "mysql" {
			continue
		}
		cells, err := oracle.LoadSerialized(filepath.Join(out, "serialized.json"))
		if err != nil {
			t.Fatal(err)
		}
		rep, err := oracle.Run(context.Background(), oracle.Options{Dump: filepath.Join(out, "db.sql"), Input: filepath.Join(out, "db.sql"), Serialized: cells, PHP: oracle.PHP{Cmd: []string{php}}})
		if err != nil {
			t.Fatal(err)
		}
		if rep.Integrity.Failures != 0 || rep.Integrity.Missing != 0 || rep.RowsDiffer {
			t.Errorf("%s: the input judged against itself: %+v", f.ID, rep.Integrity)
		}
		if f.ID == "r01" && rep.Integrity.Checked < 300 {
			t.Errorf("r01: only %d serialized cells", rep.Integrity.Checked)
		}
	}
}

// r04's edge rows sit just under Action Scheduler's 191-byte args column.
func TestActionSchedulerEdge(t *testing.T) {
	dir := t.TempDir()
	seedTo(t, dir, Options{Seed: 1, Only: []string{"r04"}})
	rc, err := oracle.OpenDump(filepath.Join(dir, "fixtures", "r04", "db.sql"))
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	edges, long := 0, 0
	_, err = oracle.ReadDump(rc, func(r oracle.Row) error {
		args, _ := r.Get("args")
		ext, _ := r.Get("extended_args")
		switch id, _ := r.Get("action_id"); {
		case r.Table != "wp_actionscheduler_actions":
		case len(args.S) > 191:
			t.Errorf("action %s: args of %d bytes", id.S, len(args.S))
		case id.S > "300" && len(id.S) == 3:
			edges++
			if len(args.S) < 181 || len(args.S) > 190 {
				t.Errorf("edge action %s: args of %d bytes", id.S, len(args.S))
			}
		case !ext.Null:
			long++
			if len(args.S) != 32 || len(ext.S) <= 191 {
				t.Errorf("long action %s: args %d bytes, extended %d", id.S, len(args.S), len(ext.S))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if edges != 10 || long != 30 {
		t.Errorf("%d edge and %d long actions", edges, long)
	}
}
