// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/klauspost/compress/gzip"
	"gopkg.in/yaml.v3"

	"github.com/RhythmShahriar/roadie/testbed/manifest"
	"github.com/RhythmShahriar/roadie/testbed/oracle"
)

// portFetch is this file's test listener (the lane's ports are
// 127.0.0.1:33120-33139; drill_test.go has 33120 and 33121).
const portFetch = "127.0.0.1:33123"

func TestCountHost(t *testing.T) {
	prod := []string{
		"https://shop.example.com/", "http://shop.example.com", "//shop.example.com/x.css", `https:\/\/shop.example.com\/p`,
		`https:\\/\\/shop.example.com`, "https%3A%2F%2Fshop.example.com%2Fcart", "https%3a%2f%2fSHOP.example.com",
		"https://www.shop.example.com/about/", "'shop.example.com'", "shop.example.com.",
	}
	other := []string{
		"https://shop.example.com.au/", "https://myshop.example.com", "info@shop.example.com", "info%40shop.example.com",
		"mg.shop.example.com", "shop.example.community", "my-shop.example.com", "awww.shop.example.com",
	}
	for _, s := range prod {
		if p, o := CountHost([]byte(s), "shop.example.com"); p != 1 || o != 0 {
			t.Errorf("%q: prod %d other %d, want 1 and 0", s, p, o)
		}
	}
	for _, s := range other {
		if p, o := CountHost([]byte(s), "shop.example.com"); p != 0 || o != 1 {
			t.Errorf("%q: prod %d other %d, want 0 and 1", s, p, o)
		}
	}
	if p, o := CountHost([]byte("nothing here"), "shop.example.com"); p != 0 || o != 0 {
		t.Errorf("no host: %d %d", p, o)
	}
	if productionHost != "shop.example.com" {
		t.Errorf("productionHost %q", productionHost)
	}
}

func TestReadSearchReplace(t *testing.T) {
	pairs, err := ReadSearchReplace("../tools/wp-cli/search-replace.txt")
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) < 3 || pairs[0] != (SearchReplace{"https://shop.example.com", "http://staging-wp"}) {
		t.Errorf("pairs %q", pairs)
	}
	for _, p := range pairs {
		if !strings.Contains(strings.ToLower(p.From), productionHost) || !strings.Contains(p.To, stagingHost) {
			t.Errorf("pair %q doesn't move production to staging", p)
		}
	}
	dir := t.TempDir()
	for name, body := range map[string]string{"space": "https://a b\n", "empty": "# only a comment\n\n", "three": "a\tb\tc\n", "blank-to": "a\t\n"} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadSearchReplace(p); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if _, err := ReadSearchReplace(filepath.Join(dir, "missing")); err == nil {
		t.Error("a missing file must fail")
	}
}

// replibyteTransformers are the transformer names Replibyte 0.10.0
// documents (replibyte.com/docs/transformers).
var replibyteTransformers = []string{"email", "first-name", "phone-number", "random", "keep-first-char", "credit-card", "redacted", "transient", "custom-wasm"}

// TestReplibyteConfig checks the committed configuration against the
// schema the fixtures use: every table and column it names exists;
// every transformer is one Replibyte has.
func TestReplibyteConfig(t *testing.T) {
	b, err := os.ReadFile("../tools/replibyte/conf.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var conf struct {
		Source struct {
			Transformers []struct {
				Database string `yaml:"database"`
				Table    string `yaml:"table"`
				Columns  []struct {
					Name        string `yaml:"name"`
					Transformer string `yaml:"transformer_name"`
				} `yaml:"columns"`
			} `yaml:"transformers"`
		} `yaml:"source"`
		Datastore struct {
			LocalDisk struct {
				Dir string `yaml:"dir"`
			} `yaml:"local_disk"`
		} `yaml:"datastore"`
	}
	if err := yaml.Unmarshal(b, &conf); err != nil {
		t.Fatal(err)
	}
	if conf.Datastore.LocalDisk.Dir != "@@DATASTORE@@" {
		t.Errorf("datastore %q: the harness replaces @@DATASTORE@@", conf.Datastore.LocalDisk.Dir)
	}
	schema, err := os.ReadFile("../fixtures/_shared/mysql.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	tables := map[string]string{}
	for _, m := range regexp.MustCompile("(?s)CREATE TABLE `\\{\\{\\.\\}\\}([a-z_]+)` \\((.*?)\\n\\)").FindAllSubmatch(schema, -1) {
		tables["wp_"+string(m[1])] = string(m[2])
	}
	if len(conf.Source.Transformers) == 0 {
		t.Fatal("no transformers")
	}
	for _, tr := range conf.Source.Transformers {
		cols, ok := tables[tr.Table]
		if !ok {
			t.Errorf("table %s isn't in the fixtures' schema", tr.Table)
			continue
		}
		for _, c := range tr.Columns {
			if !strings.Contains(cols, "`"+c.Name+"`") {
				t.Errorf("%s.%s isn't in the fixtures' schema", tr.Table, c.Name)
			}
			if !slices.Contains(replibyteTransformers, c.Transformer) {
				t.Errorf("%s.%s: Replibyte has no transformer %q", tr.Table, c.Name, c.Transformer)
			}
		}
	}
}

func TestParseTools(t *testing.T) {
	for in, want := range map[string][]string{
		"all": ToolNames(), "none": nil, "": nil, "wp-cli": {"wp-cli"}, "replibyte, wp-cli,replibyte": {"replibyte", "wp-cli"},
	} {
		got, err := ParseTools(in)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("ParseTools(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"mailpit", "wp-cli,", "ALL"} {
		if _, err := ParseTools(bad); err == nil {
			t.Errorf("ParseTools(%q): want an error", bad)
		}
	}
	if !toolsNeedDocker([]string{ToolWPCLI}, []string{"r09"}) || toolsNeedDocker([]string{ToolWPCLI}, []string{"g01"}) || toolsNeedDocker(nil, nil) {
		t.Error("toolsNeedDocker")
	}
}

func TestToolInputScore(t *testing.T) {
	cases := []struct {
		in   ToolInput
		want string
	}{
		{ToolInput{URLs: &Count{0, 10}, Collateral: &Count{0, 3}}, Caught},
		{ToolInput{URLs: &Count{1, 10}}, Missed},
		{ToolInput{URLs: &Count{0, 10}, Collateral: &Count{2, 3}}, Missed},
		{ToolInput{Canaries: &Count{0, 5}, LiveKeys: &Count{0, 2}, Integrity: &Count{1, 9}}, Missed},
		{ToolInput{SideEffects: &Count{}}, Caught},
		{ToolInput{Outcome: Errored, Detail: "x", URLs: &Count{}}, Errored},
	}
	for i, c := range cases {
		c.in.score()
		if c.in.Outcome != c.want {
			t.Errorf("case %d: %s, want %s", i, c.in.Outcome, c.want)
		}
	}
}

// toolHarness is a harness with the manifest loaded and no Docker.
func toolHarness(t *testing.T) *Harness {
	t.Helper()
	f, err := manifest.Load("../scenarios.yaml")
	if err != nil {
		t.Fatal(err)
	}
	h := &Harness{Runtime: t.TempDir(), Work: t.TempDir(), ToolsDir: "../tools", Tools: ToolNames(), GreenmaskVersion: "0.2.26", Ex: &fakeExec{}, Log: io.Discard}
	h.manifest = map[string]manifest.Scenario{}
	for _, s := range f.Scenarios {
		h.manifest[s.ID] = s
	}
	return h
}

// A tool that can't run is "not run" with its reason, never an error.
func TestToolsNotRun(t *testing.T) {
	h := toolHarness(t)
	res := h.RunTools(context.Background(), nil)
	if len(res) != len(ToolSpecs) {
		t.Fatalf("%d tools, want %d", len(res), len(ToolSpecs))
	}
	for _, r := range res {
		if r.NotRun == "" || len(r.Inputs) != 0 {
			t.Errorf("%s: want not run without the testbed, got %+v", r.Name, r)
		}
	}
	if !strings.Contains(res[2].NotRun, "PostgreSQL only") || res[2].Version != "0.2.26" {
		t.Errorf("greenmask: %+v", res[2])
	}
	if !strings.Contains(res[0].NotRun, "make -C testbed up") {
		t.Errorf("wp-cli: %q", res[0].NotRun)
	}

	// With the testbed up: what make tools left decides.
	h.dockerUp = true
	h.Ex = &fakeExec{fail: func(args []string) bool { return slices.Contains(args, "cli") }}
	rb := filepath.Join(h.Runtime, "tools", ToolReplibyte)
	if err := os.MkdirAll(rb, 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(rb, "version"), []byte("0.10.0\n"), 0o600)
	_ = os.WriteFile(filepath.Join(rb, "not-run"), []byte("no sha256 pinned for replibyte 0.10.0 in testbed/Makefile yet\n"), 0o600)
	res = h.RunTools(context.Background(), []string{"r01"})
	got := map[string]ToolResult{}
	for _, r := range res {
		got[r.Name] = r
	}
	if r := got[ToolReplibyte]; r.Version != "0.10.0" || !strings.HasPrefix(r.NotRun, "no sha256 pinned") {
		t.Errorf("replibyte: %+v", r)
	}
	if r := got[ToolStagingShield]; !strings.Contains(r.NotRun, "make -C testbed tools") {
		t.Errorf("staging-shield: %+v", r)
	}
	if r := got[ToolWPCLI]; !strings.HasPrefix(r.NotRun, "WP-CLI in the sshd container") {
		t.Errorf("wp-cli: %+v", r)
	}
	// -only without any tool input: no tool at all.
	if res := h.RunTools(context.Background(), []string{"g01"}); len(res) != 0 {
		t.Errorf("-only g01: %+v", res)
	}

	// results.json records them under tools.
	path := filepath.Join(h.Work, "results.json")
	if err := WriteResults(path, Results{Version: ResultsVersion, Tools: res}); err != nil {
		t.Fatal(err)
	}
	if err := WriteResults(path, Results{Version: ResultsVersion, Tools: h.RunTools(context.Background(), nil)}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !bytes.Contains(b, []byte(`"tools": [`)) || !bytes.Contains(b, []byte(`"not_run": "no sha256 pinned`)) {
		t.Errorf("results.json:\n%s", b)
	}
}

// fakeDump is a mysqldump-shaped output with the URLs given.
func fakeDump(rows ...string) []byte {
	var b strings.Builder
	b.WriteString("CREATE TABLE `wp_options` (\n  `option_value` longtext\n);\n")
	for _, r := range rows {
		b.WriteString("INSERT INTO `wp_options` VALUES ('" + r + "');\n")
	}
	return []byte(b.String())
}

func TestRunWPCLI(t *testing.T) {
	h := toolHarness(t)
	in := t.TempDir()
	input := fakeDump("https://shop.example.com/a", `https:\\/\\/shop.example.com\\/b`, "https://shop.example.com.au/", "info@shop.example.com")
	if err := os.WriteFile(filepath.Join(in, "db.sql"), input, 0o600); err != nil {
		t.Fatal(err)
	}
	// search-replace "rewrote" the first URL and broke the lookalike; the
	// escaped one is left.
	output := fakeDump("http://staging-wp/a", `https:\\/\\/shop.example.com\\/b`, "http://staging-wp.au/", "info@shop.example.com")
	ex := &fakeExec{out: func(args []string) []byte {
		switch {
		case slices.Contains(args, "search-replace"):
			return []byte("Success: made 2 replacements.\n2\n")
		case len(args) == 3 && strings.Contains(args[2], "mysqldump"):
			return output
		}
		return nil
	}}
	h.Ex = ex
	st := &scenarioState{in: in, work: t.TempDir()}
	ti := h.runWPCLI(context.Background(), st)
	ti.score()
	if ti.Outcome != Missed || *ti.URLs != (Count{1, 2}) || *ti.Collateral != (Count{1, 2}) {
		t.Fatalf("got %+v urls %+v collateral %+v", ti, ti.URLs, ti.Collateral)
	}
	if !strings.Contains(ti.Detail, "14 replacements in 7 passes") {
		t.Errorf("detail %q", ti.Detail)
	}
	var sr int
	for _, c := range ex.calls {
		if slices.Contains(c, "search-replace") {
			sr++
			if c[0] != ctrSSHD || c[1] != "roadie" || !slices.Contains(c, "--all-tables") || !slices.Contains(c, "--precise") {
				t.Errorf("search-replace call %q", c)
			}
		}
	}
	if sr != 7 {
		t.Errorf("%d search-replace calls, want 7 (one per pair)", sr)
	}
	if last := ex.calls[len(ex.calls)-1]; last[0] != ctrProdDB || !strings.Contains(strings.Join(last, " "), "mysql -uroot") {
		t.Errorf("the scratch database must be dropped at the end, last call %q", last)
	}
	if b, _ := os.ReadFile(filepath.Join(st.work, "out.sql")); !bytes.Equal(b, output) {
		t.Error("out.sql is not the dump")
	}

	// A failing WP-CLI is an error with the reason, not a miss.
	h.Ex = &fakeExec{fail: func(args []string) bool { return slices.Contains(args, "search-replace") }}
	ti = h.runWPCLI(context.Background(), st)
	ti.score()
	if ti.Outcome != Errored || !strings.HasPrefix(ti.Detail, "wp search-replace") {
		t.Errorf("failing wp: %+v", ti)
	}
}

func TestRunReplibyte(t *testing.T) {
	h := toolHarness(t)
	in, work := t.TempDir(), t.TempDir()
	// Values made at run time: plain test data, never real people or keys.
	email := "tb.canary." + "r01x01" + "@example.org"
	phone := "+44 7700 " + "900123"
	key := "sk_" + "live_" + strings.Repeat("Tb0", 8)
	cs := []oracle.Canary{
		{ID: "r01-email-0001", Kind: "email", Scenario: "r01", Value: email},
		{ID: "r01-phone-0001", Kind: "phone", Scenario: "r01", Value: phone},
		{ID: "r01-key-stripe_live_secret", Kind: "key", Scenario: "r01", Value: key, Live: true},
	}
	_ = os.WriteFile(filepath.Join(in, "db.sql"), fakeDump(email, "call "+phone+" now", key), 0o600)
	var argv []string
	defer func(f func(context.Context, ...string) ([]byte, error)) { dockerRun = f }(dockerRun)
	dockerRun = func(_ context.Context, args ...string) ([]byte, error) {
		argv = args
		// Replibyte "transformed" the email column only.
		return nil, os.WriteFile(filepath.Join(work, "out.sql"), fakeDump("someone@example.net", "call "+phone+" now", key), 0o600)
	}
	st := &scenarioState{in: in, work: work, canaries: cs}
	ti := h.runReplibyte(context.Background(), st, "/opt/replibyte")
	ti.score()
	if ti.Outcome != Missed || *ti.Canaries != (Count{1, 2}) || *ti.LiveKeys != (Count{1, 1}) || ti.Integrity != nil {
		t.Fatalf("got %+v canaries %+v keys %+v", ti, ti.Canaries, ti.LiveKeys)
	}
	if strings.Contains(ti.Detail, phone) || strings.Contains(ti.Detail, key) {
		t.Errorf("detail must not hold a value: %q", ti.Detail)
	}
	joined := strings.Join(argv, " ")
	for _, want := range []string{"--network none", "/opt/replibyte:/usr/local/bin/replibyte:ro", in + ":/in:ro", runnerImage, "dump create -s mysql -i < /in/db.sql", "dump restore local -v latest -o"} {
		if !strings.Contains(joined, want) {
			t.Errorf("docker run %q lacks %q", joined, want)
		}
	}
	conf, _ := os.ReadFile(filepath.Join(work, "conf.yaml"))
	if !bytes.Contains(conf, []byte(`dir: "/work/datastore"`)) {
		t.Error("conf.yaml must name the work folder's datastore")
	}

	// A clean output is caught; a failing run is an error.
	dockerRun = func(context.Context, ...string) ([]byte, error) {
		return nil, os.WriteFile(filepath.Join(work, "out.sql"), fakeDump("someone@example.net", "call +1 555 0100 now", "sk_"+"test_x"), 0o600)
	}
	ti = h.runReplibyte(context.Background(), st, "/opt/replibyte")
	if ti.score(); ti.Outcome != Caught {
		t.Errorf("clean output: %+v", ti)
	}
	dockerRun = func(context.Context, ...string) ([]byte, error) {
		return nil, errors.New("docker run: exit status 1: unknown flag -i")
	}
	ti = h.runReplibyte(context.Background(), st, "/opt/replibyte")
	if ti.score(); ti.Outcome != Errored || !strings.Contains(ti.Detail, "unknown flag") {
		t.Errorf("failing run: %+v", ti)
	}
}

func TestUnpackRelease(t *testing.T) {
	dir := t.TempDir()
	var tb bytes.Buffer
	zw := gzip.NewWriter(&tb)
	tw := tar.NewWriter(zw)
	for name, body := range map[string]string{"replibyte_v0/replibyte": "#!/bin/sh\n", "replibyte_v0/README.md": "x", "../escape": "no"} {
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg})
		_, _ = tw.Write([]byte(body))
	}
	_ = tw.Close()
	_ = zw.Close()
	rel := filepath.Join(dir, "release")
	_ = os.WriteFile(rel, tb.Bytes(), 0o600)
	bin, err := unpackBinary(rel, filepath.Join(dir, "bin"), "replibyte")
	if err != nil || filepath.Base(bin) != "replibyte" {
		t.Fatalf("%q %v", bin, err)
	}
	if fi, _ := os.Stat(bin); fi.Mode()&0o100 == 0 {
		t.Error("the binary must be executable")
	}
	if _, err := os.Stat(filepath.Join(dir, "escape")); err == nil {
		t.Error("an entry outside the folder was written")
	}
	if _, err := unpackBinary(rel, filepath.Join(dir, "bin"), "greenmask"); err == nil {
		t.Error("a missing binary must fail")
	}
	_ = os.WriteFile(rel, []byte("not gzip"), 0o600)
	if _, err := unpackBinary(rel, filepath.Join(dir, "bin"), "replibyte"); err == nil {
		t.Error("a release that isn't .tar.gz must fail")
	}

	plugin := func(names ...string) string {
		var zb bytes.Buffer
		w := zip.NewWriter(&zb)
		for _, n := range names {
			f, _ := w.Create(n)
			_, _ = f.Write([]byte("<?php\n"))
		}
		_ = w.Close()
		p := filepath.Join(t.TempDir(), "plugin.zip")
		_ = os.WriteFile(p, zb.Bytes(), 0o600)
		return p
	}
	slug, err := unzipPlugin(plugin("staging-shield/staging-shield.php", "staging-shield/inc/a.php"), filepath.Join(dir, "plugin"))
	if err != nil || slug != "staging-shield" {
		t.Fatalf("%q %v", slug, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "plugin", "staging-shield", "inc", "a.php")); err != nil {
		t.Error(err)
	}
	for _, bad := range [][]string{{"../evil.php"}, {"a/x.php", "b/y.php"}, {}} {
		if _, err := unzipPlugin(plugin(bad...), filepath.Join(dir, "bad")); err == nil {
			t.Errorf("%q: want an error", bad)
		}
	}
}

// TestFetchTools runs tools/fetch.sh against a local server: a pinned
// release is kept; an unverified pin, a wrong sha256 and a failed
// download each leave "not-run" with the reason instead of failing.
func TestFetchTools(t *testing.T) {
	for _, tool := range []string{"sh", "curl", "sha256sum"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("needs %s", tool)
		}
	}
	body := []byte("release bytes")
	sum := sha256.Sum256(body)
	ln, err := net.Listen("tcp", portFetch)
	if err != nil {
		t.Skipf("port %s: %v", portFetch, err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ok.tar.gz" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	})}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()
	base := "http://" + portFetch
	dir := t.TempDir()
	cmd := exec.Command("sh", "../tools/fetch.sh", dir)
	cmd.Env = append(os.Environ(), "TESTBED_TOOLS="+strings.Join([]string{
		"good 1.0 " + base + "/ok.tar.gz " + hex.EncodeToString(sum[:]),
		"unpinned 2.0 " + base + "/ok.tar.gz unverified",
		"wrongsum 3.0 " + base + "/ok.tar.gz " + strings.Repeat("0", 64),
		"missing 4.0 " + base + "/gone.zip " + hex.EncodeToString(sum[:]),
	}, "\n"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fetch.sh: %v\n%s", err, out)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "good", "release")); !bytes.Equal(b, body) {
		t.Error("good: the release wasn't kept")
	}
	for name, want := range map[string]string{"unpinned": "no sha256 pinned", "wrongsum": "not the pinned", "missing": "download of"} {
		v, nr := fetched(filepath.Join(dir, name))
		if !strings.Contains(nr, want) || v == "" {
			t.Errorf("%s: version %q not-run %q, want %q", name, v, nr, want)
		}
		if _, err := os.Stat(filepath.Join(dir, name, "release")); err == nil {
			t.Errorf("%s: a release that wasn't verified must not be kept", name)
		}
	}
	if v, nr := fetched(filepath.Join(dir, "good")); v != "1.0" || nr != "" {
		t.Errorf("good: %q %q", v, nr)
	}
}

func TestDiffComparesTools(t *testing.T) {
	dir := t.TempDir()
	base := Results{Version: ResultsVersion, Scored: Scored{Scenarios: []ScenarioResult{{ID: "r01", Outcome: Caught}}}}
	a, b := base, base
	a.Tools = []ToolResult{{Name: ToolWPCLI, Inputs: []ToolInput{{Input: "r09", Outcome: Missed, URLs: &Count{1, 274}, SideEffects: &Count{Left: 3}}}}}
	b.Tools = []ToolResult{{Name: ToolWPCLI, Inputs: []ToolInput{{Input: "r09", Outcome: Missed, URLs: &Count{1, 274}, SideEffects: &Count{Left: 9}}}}}
	pa, pb := filepath.Join(dir, "a.json"), filepath.Join(dir, "b.json")
	_ = WriteResults(pa, a)
	_ = WriteResults(pb, b)
	var out bytes.Buffer
	if same, err := DiffResults(pa, pb, &out); err != nil || !same {
		t.Errorf("side-effect counts vary between runs and aren't compared: %v %s", err, out.String())
	}
	b.Tools[0].Inputs[0].URLs = &Count{0, 274}
	_ = WriteResults(pb, b)
	out.Reset()
	if same, _ := DiffResults(pa, pb, &out); same || !strings.Contains(out.String(), "tool wp-cli/r09: missed urls 1/274 | missed urls 0/274") {
		t.Errorf("a changed count must show: %s", out.String())
	}
}
