// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/testbed/oracle"
)

func TestRun(t *testing.T) {
	rt := t.TempDir()
	out := filepath.Join(rt, "fixtures", "r17")
	// Another scenario's canaries in canaries.json stay.
	other := `{"version":1,"seed":1,"canaries":[{"id":"r01-email-0001","kind":"email","scenario":"r01","value":"x"}]}`
	if err := os.WriteFile(filepath.Join(rt, "canaries.json"), []byte(other), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-size", "3MB", "-small", "2MB", "-out", out, "-runtime", rt}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	for _, f := range []string{"db.sql.gz", "db-small.sql.gz", "serialized.json", "serialized-small.json", "gen.json"} {
		fi, err := os.Stat(filepath.Join(out, f))
		// Windows files have no Unix mode: only the file counts there.
		if err != nil || (runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600) {
			t.Errorf("%s: %v", f, err)
		}
	}
	cs, err := oracle.LoadCanaries(filepath.Join(rt, "canaries.json"), "")
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	n := 0
	for _, c := range cs {
		if ids[c.ID] {
			t.Errorf("canary %s twice", c.ID)
		}
		ids[c.ID] = true
		if c.Scenario == "r17" {
			n++
			if strings.Contains(stdout.String(), c.Value) {
				t.Errorf("the output prints the value of %s", c.ID)
			}
		}
	}
	if !ids["r01-email-0001"] || n != 8 {
		t.Errorf("canaries: r01 kept %v, %d of r17 (want 8: first and last row of two files)", ids["r01-email-0001"], n)
	}
	var m Manifest
	b, _ := os.ReadFile(filepath.Join(out, "gen.json"))
	if err := json.Unmarshal(b, &m); err != nil || m.Files["db.sql.gz"] == nil || m.Files["db.sql.gz"].Bytes < 2_970_000 {
		t.Errorf("gen.json: %v %+v", err, m)
	}
	// The gzip reads back as the dump.
	rc, err := oracle.OpenDump(filepath.Join(out, "db.sql.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	st, err := oracle.ReadDump(rc, nil)
	if err != nil || st.Rows["wp_usermeta"] == 0 {
		t.Errorf("%v %v", st.Rows, err)
	}
	if code := run([]string{"-size", "lots"}, &stdout, &stderr); code != 2 {
		t.Errorf("a bad size: exit %d, want 2", code)
	}
}
