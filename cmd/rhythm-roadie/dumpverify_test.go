// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/internal/demo"
	"github.com/RhythmShahriar/roadie/internal/testutil"
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
