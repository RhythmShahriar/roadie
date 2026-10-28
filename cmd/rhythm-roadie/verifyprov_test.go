// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"regexp"
	"testing"

	"github.com/RhythmShahriar/roadie/internal/demo"
)

// A standalone verify's report says which profile pack, rules and config
// judged the copy, as a copy's report does: the signature covers them.
func TestVerifyReportProvenance(t *testing.T) {
	realShop(t)
	prod := demo.Production()[0]
	out, errOut, code := runCmd(t, "copy", "--from", "shop.sql", "--to", "out.sql", "--url", demoURL, "--production", prod, "--yes", "--offline", "--json")
	if code != 0 {
		t.Fatalf("copy: exit %d\n%s", code, errOut)
	}
	cp, _ := jsonData(t, out)
	out, errOut, code = runCmd(t, "verify", "out.sql", "--production", prod, "--offline", "--json")
	if code != 0 {
		t.Fatalf("verify: exit %d\n%s", code, errOut)
	}
	vd, _ := jsonData(t, out)
	hex := regexp.MustCompile(`^[0-9a-f]{64}$`)
	for _, k := range []string{"ruleset_sha256", "config_sha256"} {
		if s, _ := vd[k].(string); !hex.MatchString(s) {
			t.Errorf("verify data.%s = %q", k, s)
		}
	}
	if s, _ := vd["profile_pack"].(string); s == "" || s != cp["profile_pack"] {
		t.Errorf("verify data.profile_pack = %q, copy's %q", s, cp["profile_pack"])
	}
}
