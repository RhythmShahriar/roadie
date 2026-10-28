// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/engine"
	"github.com/RhythmShahriar/roadie/internal/demo"
	"github.com/RhythmShahriar/roadie/internal/testutil"
	"github.com/RhythmShahriar/roadie/verify"
)

// realShop writes the demo shop's dump in a temp project, with the real
// deps; it returns its folder.
func realShop(t *testing.T) string {
	t.Helper()
	if err := demo.Shop(io.Discard, demo.Options{Seed: demoSeed, Customers: 1, Leaky: true}); errors.Is(err, core.ErrNotBuilt) {
		t.Skip("internal/demo.Shop isn't built")
	}
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("RHYTHM_CACHE_DIR", t.TempDir())
	old := dep
	t.Cleanup(func() { dep = old })
	dep = realDeps()
	testutil.NoNetwork(t)
	if err := writeDemoShop(filepath.Join(dir, "shop.sql")); err != nil {
		t.Fatal(err)
	}
	return dir
}

// jsonData decodes the data and summary of a --json envelope.
func jsonData(t *testing.T, out string) (data, summary map[string]any) {
	t.Helper()
	var env struct {
		Data    map[string]any `json:"data"`
		Summary map[string]any `json:"summary"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	return env.Data, env.Summary
}

func findingIDs(data map[string]any) map[string]bool {
	out := map[string]bool{}
	fs, _ := data["findings"].([]any)
	for _, f := range fs {
		if m, ok := f.(map[string]any); ok {
			out[m["id"].(string)] = true
		}
	}
	return out
}

// A standalone verify of a dump runs the side-effect checks that read only
// rows: Roadie's copy passes; the same copy with its webhook switched
// back to active fails on side-effect.webhooks.
func TestVerifyDumpSideEffects(t *testing.T) {
	realShop(t)
	prod := demo.Production()[0]
	out, errOut, code := runCmd(t, "copy", "--from", "shop.sql", "--to", "out.sql", "--url", demoURL, "--production", prod, "--yes", "--offline", "--plain")
	if code != 0 {
		t.Fatalf("copy: exit %d\n%s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "2 side effects") || !strings.Contains(out, "(data rules only: a dump has no files)") {
		t.Errorf("copy to a dump counts no side effects:\n%s", out)
	}
	out, errOut, code = runCmd(t, "copy", "--from", "shop.sql", "--to", "out2.sql", "--url", demoURL, "--production", prod, "--yes", "--offline", "--json")
	if _, sum := jsonData(t, out); code != 0 || sum["side_effects_off"] != float64(2) {
		t.Errorf("copy --json: exit %d summary %v\n%s", code, sum, errOut)
	}
	out, errOut, code = runCmd(t, "verify", "out.sql", "--production", prod, "--offline", "--json")
	if code != 0 {
		t.Fatalf("verify of the copy: exit %d\n%s\n%s", code, out, errOut)
	}
	b, err := os.ReadFile("out.sql")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte("VALUES (1,'paused',")) {
		t.Fatal("the copy's webhook isn't paused")
	}
	if err := os.WriteFile("hook.sql", bytes.Replace(b, []byte("VALUES (1,'paused',"), []byte("VALUES (1,'active',"), 1), 0o600); err != nil {
		t.Fatal(err)
	}
	out, errOut, code = runCmd(t, "verify", "hook.sql", "--production", prod, "--offline", "--json")
	data, sum := jsonData(t, out)
	if code != 1 || !findingIDs(data)[core.FindingSideEffectWebhooks] {
		t.Fatalf("active webhook: exit %d %v\n%s", code, findingIDs(data), errOut)
	}
	if n, _ := sum["side_effects_total"].(float64); n < 1 {
		t.Errorf("side_effects_total %v", sum["side_effects_total"])
	}
	out, _, _ = runCmd(t, "verify", "hook.sql", "--production", prod, "--offline", "--plain")
	for _, want := range []string{"[FAIL] WooCommerce webhooks", "Off: WP-Cron: a database or dump target has no files to read"} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Errorf("checks lack %q\n%s", want, out)
		}
	}
}

// verify takes --max-output-ratio and --max-value as copy does; its
// stop doesn't speak of a target it never writes.
func TestVerifyDumpLimits(t *testing.T) {
	dir := realShop(t)
	shop, err := os.ReadFile(filepath.Join(dir, "shop.sql"))
	if err != nil {
		t.Fatal(err)
	}
	// Padding past the 64 MiB grace that compresses far better than 20
	// times.
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write(shop)
	line := []byte("-- " + strings.Repeat("a", 1<<20) + "\n")
	for range 70 {
		_, _ = zw.Write(line)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("bomb.sql.gz", gz.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	out, errOut, code := runCmd(t, "verify", "bomb.sql.gz", "--offline", "--plain")
	if code != 3 || !strings.Contains(out+errOut, "dump-bomb") || !strings.Contains(out+errOut, "--max-output-ratio") || strings.Contains(out+errOut, "untouched") {
		t.Errorf("bomb: exit %d\n%s\n%s", code, out, errOut)
	}
	out, errOut, code = runCmd(t, "verify", "bomb.sql.gz", "--max-output-ratio", "100000", "--offline", "--plain")
	if code == 2 || code == 3 {
		t.Errorf("bomb with --max-output-ratio: exit %d\n%s\n%s", code, out, errOut)
	}

	out, errOut, code = runCmd(t, "verify", "shop.sql", "--max-value", "1KiB", "--offline", "--plain")
	if code != 3 || !strings.Contains(out+errOut, "value-too-large") || !strings.Contains(out+errOut, "--max-value") || strings.Contains(out+errOut, "untouched") {
		t.Errorf("max-value 1KiB: exit %d\n%s\n%s", code, out, errOut)
	}
	if _, errOut, code = runCmd(t, "verify", "shop.sql", "--max-value", "1GiB", "--offline", "--plain"); code == 2 || code == 3 {
		t.Errorf("max-value 1GiB: exit %d\n%s", code, errOut)
	}
	if _, errOut, code = runCmd(t, "verify", "shop.sql", "--max-value", "lots", "--offline", "--plain"); code != 2 || !strings.Contains(errOut, "--max-value") {
		t.Errorf("bad --max-value: exit %d\n%s", code, errOut)
	}
}

func TestVerifyWording(t *testing.T) {
	for in, want := range map[string]string{
		"dump-bomb: x grew. If it's real, pass --max-output-ratio 20; the target is untouched.": "dump-bomb: x grew. If it's real, pass --max-output-ratio 20.",
		"dump-truncated: x is cut. Export it again; the target is untouched.":                   "dump-truncated: x is cut. Export it again.",
		"stopped. The target is untouched.":                                                     "stopped.",
		"nothing to change":                                                                     "nothing to change",
	} {
		if got := verifyWording(&core.Error{Msg: in}).Msg; got != want {
			t.Errorf("%q: got %q", in, got)
		}
	}
	if verifyWording(nil) != nil {
		t.Error("nil")
	}
}

// A standalone verify of a site names it by the host it calls itself, as
// copy and prepare do; a dump keeps its file name.
func TestVerifyBoxNamesSiteByHost(t *testing.T) {
	site := filepath.Join(t.TempDir(), "site")
	if err := os.MkdirAll(site, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ target, url, want string }{
		{site, "https://staging.shop.test", "PASS   staging.shop.test is a pseudonymised copy"},
		{site, "", "PASS   site is a pseudonymised copy"},
		{"staging.sql", "", "PASS   staging.sql is a pseudonymised copy"},
	} {
		fx := &verifyFixture{results: []*core.VerifyResult{heuristicPass()}}
		useVerify(t, fx, func(d *deps) {
			d.verifyJob = func(_ context.Context, _ engine.Job, target core.Endpoint, _ *core.Endpoint) (verify.Job, error) {
				return verify.Job{Target: target, URL: c.url}, nil
			}
		})
		out, errOut, code := runCmd(t, "verify", c.target, "--no-config", "--plain")
		if code != 0 || !strings.Contains(out, c.want) {
			t.Errorf("%s %q: exit %d\n%s\n%s", c.target, c.url, code, out, errOut)
		}
	}
}

// A live key URL-encoded into a link ("%3Dsk_live_...") is a key: copy
// blanks it and verify, standalone and exact, fails a dump that keeps it.
func TestCopyBlanksURLEncodedKey(t *testing.T) {
	dir := realShop(t)
	shop, err := os.ReadFile(filepath.Join(dir, "shop.sql"))
	if err != nil {
		t.Fatal(err)
	}
	key := testutil.J("sk_", "live_", "Zx9Wq8Er7Ty6Ui5Op4As3Df2")
	row := "INSERT INTO `wp_options` VALUES (9001,'x_url','https://hooks.example.net/cb?redirect=https%3A%2F%2Fpay.example.net%2Fauth%3Fkey%3D" + key + "','off');\n"
	i := bytes.Index(shop, []byte("-- Dump completed"))
	if i < 0 {
		t.Fatal("the demo dump has no end line")
	}
	prodDump := append(append(append([]byte{}, shop[:i]...), row...), shop[i:]...)
	if err := os.WriteFile("prod_url.sql", prodDump, 0o600); err != nil {
		t.Fatal(err)
	}
	prod := demo.Production()[0]
	out, errOut, code := runCmd(t, "copy", "--from", "prod_url.sql", "--to", "out_url.sql", "--url", demoURL, "--production", prod, "--yes", "--offline", "--plain")
	if code != 0 {
		t.Fatalf("copy: exit %d\n%s\n%s", code, out, errOut)
	}
	b, err := os.ReadFile("out_url.sql")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte(key)) {
		t.Fatal("the copy keeps the URL-encoded live key")
	}
	if !bytes.Contains(b, []byte("auth%3Fkey%3D")) {
		t.Error("the copy lost the link around the key")
	}
	if j := bytes.Index(b, []byte("-- Dump completed")); j < 0 {
		t.Fatal("the copy has no end line")
	} else if err := os.WriteFile("leak_url.sql", append(append(append([]byte{}, b[:j]...), row...), b[j:]...), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"prod_url.sql", "--production", prod, "--offline", "--json"},
		{"leak_url.sql", "--production", prod, "--offline", "--json"},
		{"leak_url.sql", "--from", "prod_url.sql", "--production", prod, "--offline", "--json"},
	} {
		out, errOut, code := runCmd(t, "verify", args...)
		if data, _ := jsonData(t, out); code != 1 || !findingIDs(data)["key.stripe-live"] {
			t.Errorf("%v: exit %d %v\n%s", args, code, findingIDs(data), errOut)
		}
	}
}

// A verify that --skip leaves data checks out never claims a whole PASS:
// the box says "PASS (partial: ...)" and complete is false, as a sample's.
func TestVerifySkipIsPartial(t *testing.T) {
	realShop(t)
	prod := demo.Production()[0]
	if out, errOut, code := runCmd(t, "copy", "--from", "shop.sql", "--to", "out.sql", "--url", demoURL, "--production", prod, "--yes", "--offline", "--plain"); code != 0 {
		t.Fatalf("copy: exit %d\n%s\n%s", code, out, errOut)
	}
	const all = "emails,keys,names,phones,addresses,ips,cards,urls"
	out, errOut, code := runCmd(t, "verify", "out.sql", "--production", prod, "--offline", "--plain", "--skip", all)
	if code != 0 || !strings.Contains(out, "PASS (partial: emails, phones, names, addresses, ips, cards, keys, urls)") ||
		!strings.Contains(out, "checks left out by --skip: emails, phones") {
		t.Errorf("--skip: exit %d\n%s\n%s", code, out, errOut)
	}
	var env struct {
		Complete bool `json:"complete"`
	}
	out, _, _ = runCmd(t, "verify", "out.sql", "--production", prod, "--offline", "--json", "--skip", "emails")
	if err := json.Unmarshal([]byte(out), &env); err != nil || env.Complete {
		t.Errorf("--skip emails --json: complete %v (%v)\n%s", env.Complete, err, out)
	}
	if data, _ := jsonData(t, out); data["coverage"].(map[string]any)["skipped_checks"] == nil {
		t.Errorf("no coverage.skipped_checks:\n%s", out)
	}
	out, _, _ = runCmd(t, "verify", "out.sql", "--production", prod, "--offline", "--json", "--skip", "side-effects")
	if err := json.Unmarshal([]byte(out), &env); err != nil || !env.Complete {
		t.Errorf("--skip side-effects leaves the data checks whole: complete %v (%v)", env.Complete, err)
	}
	out, _, _ = runCmd(t, "verify", "out.sql", "--production", prod, "--offline", "--plain")
	if !strings.Contains(out, "PASS ") || strings.Contains(out, "partial") {
		t.Errorf("no --skip:\n%s", out)
	}
}
