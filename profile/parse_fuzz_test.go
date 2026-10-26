// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package profile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// FuzzParse feeds random profile files to Parse: it never panics; a
// file it accepts has an ID, an explain line on every rule and rule IDs
// under the profile's ID, because the report and explain lean on both.
func FuzzParse(f *testing.F) {
	names, err := filepath.Glob(filepath.Join("builtin", "*.yaml"))
	if err != nil {
		f.Fatal(err)
	}
	for _, n := range names {
		b, err := os.ReadFile(n)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	f.Add([]byte("id: x\nname: X\nstack: sql\nchecked_against: \"docs: x\"\n"))
	f.Add([]byte("id: x\nname: X\nstack: wordpress\nchecked_against: \"docs: x\"\nrules: [{table: \"{prefix}{site}t\", column: c, rule: \"json:a.b=email\", explain: e}]\n"))
	f.Add([]byte("a: &x [1]\nb: *x\n"))
	f.Fuzz(func(t *testing.T, b []byte) {
		d, err := Parse("fuzz.yaml", b)
		if err != nil {
			return
		}
		if d.ID == "" || strings.ContainsAny(d.ID, " ./") {
			t.Fatalf("accepted id %q", d.ID)
		}
		for _, r := range d.Rules {
			if r.Explain == "" || !strings.HasPrefix(r.ID, d.ID+".") {
				t.Fatalf("accepted rule %+v", r)
			}
		}
	})
}
