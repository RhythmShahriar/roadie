// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The command prints counts, never a canary or key value.
func TestRunPrintsNoValues(t *testing.T) {
	out := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-fixtures", "../../../fixtures", "-out", out, "-php", "", "-only", "r04"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "r04") || !strings.Contains(stdout.String(), "canaries.json") {
		t.Errorf("output %q", stdout.String())
	}
	b, err := os.ReadFile(filepath.Join(out, "canaries.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f struct{ Canaries []struct{ Value string } }
	if err := json.Unmarshal(b, &f); err != nil || len(f.Canaries) == 0 {
		t.Fatalf("canaries.json: %v", err)
	}
	for _, c := range f.Canaries {
		if strings.Contains(stdout.String()+stderr.String(), c.Value) {
			t.Fatal("the command printed a canary value")
		}
	}
	if code := run([]string{"-fixtures", filepath.Join(out, "none")}, &stdout, &stderr); code != 1 {
		t.Errorf("missing fixtures: exit %d", code)
	}
	if code := run([]string{"-bogus"}, &stdout, &stderr); code != 2 {
		t.Errorf("bad flag: exit %d", code)
	}
}
