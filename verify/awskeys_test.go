// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package verify

import (
	"slices"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/internal/testutil"
)

// AWS keys in wp-config.php are findings: in a plain define and inside
// AS3CF_SETTINGS' serialize( array( ... ) ) as testbed r12 has them. An
// AS3CF_SETTINGS whose keys are blank is not.
func TestRunWPConfigAWSKeys(t *testing.T) {
	cfg := func(access, secret, plain string) []byte {
		return []byte("<?php\n" +
			"define( 'WP_ENVIRONMENT_TYPE', 'staging' );\n" +
			"define( 'AS3CF_SETTINGS', serialize( array(\n" +
			"\t'provider'          => 'aws',\n" +
			"\t'access-key-id'     => '" + access + "',\n" +
			"\t'secret-access-key' => '" + secret + "',\n" +
			") ) );\n" +
			"define( 'AWS_SECRET_ACCESS_KEY', '" + plain + "' );\n")
	}
	access := testutil.J("AKIA", "7R7IGKPH6BEN25T7")
	secret := testutil.J("qUTGo7rxIOrE9XTot5wA", "0Qr6lGXH0x3xzr144JZa")
	plain := testutil.J("wJalrXUtnFEMI/K7MDENG/", "bPxRfiCY0123456789")
	st := testutil.FakeSite(map[string][]byte{"wp-config.php": cfg(access, secret, plain)})
	res := runJob(t, Job{Site: st, Source: source(options([]string{"1", "blogname", "Shop"}))})
	var lines []int
	for _, f := range res.Findings {
		if f.ID != core.KeyLiveID("aws") {
			continue
		}
		for _, l := range f.Locations {
			if l.File == "wp-config.php" {
				lines = append(lines, l.Line)
			}
		}
	}
	slices.Sort(lines)
	if !slices.Equal(lines, []int{5, 6, 8}) {
		t.Errorf("AWS keys at lines %v, want 5, 6 and 8: %v", lines, ids(res))
	}
	st = testutil.FakeSite(map[string][]byte{"wp-config.php": cfg("", "", "")})
	res = runJob(t, Job{Site: st, Source: source(options([]string{"1", "blogname", "Shop"}))})
	if f := find(res, core.KeyLiveID("aws")); f != nil {
		t.Errorf("blank keys reported: %+v", f)
	}
}
