// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
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
