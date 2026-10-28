// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package killswitch

import (
	"context"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/internal/testutil"
)

// AWS keys in wp-config.php are found and blanked whether they are plain
// defines or strings inside a define built from an expression (WP
// Offload Media's AS3CF_SETTINGS, as in testbed r12), with the real key
// rules. Positive: both forms. Negative: an empty AS3CF_SETTINGS key and
// a provider name are left alone.
func TestKeyStepAWSInWPConfig(t *testing.T) {
	access := testutil.J("AKIA", "7R7IGKPH6BEN25T7")
	secret := testutil.J("qUTGo7rxIOrE9XTot5wA", "0Qr6lGXH0x3xzr144JZa")
	access2 := testutil.J("AKIA", "QWERTYUIOPASDFGH")
	secret2 := testutil.J("wJalrXUtnFEMI/K7MDENG/", "bPxRfiCY0123456789")
	cfg := "<?php\n" +
		"define( 'DB_NAME', 'shop' );\n" +
		"define( 'AS3CF_SETTINGS', serialize( array(\n" +
		"\t'provider'          => 'aws',\n" +
		"\t'access-key-id'     => '" + access + "',\n" +
		"\t'secret-access-key' => \"" + secret + "\",\n" +
		"\t'bucket'            => '',\n" +
		") ) );\n" +
		"define( 'AWS_ACCESS_KEY_ID', '" + access2 + "' );\n" +
		"define( 'AWS_SECRET_ACCESS_KEY', '" + secret2 + "' );\n" +
		"$table_prefix = 'wp_';\n" +
		"require_once ABSPATH . 'wp-settings.php';\n"
	s := testutil.FakeSite(map[string][]byte{"wp-config.php": []byte(cfg)})
	st := keyStep(Options{})
	ctx := context.Background()
	_, fs, err := st.Check(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	lines := map[int]bool{}
	for _, f := range fs {
		if f.ID != core.KeyLiveID("aws") {
			t.Errorf("finding %s", f.ID)
		}
		lines[f.Locations[0].Line] = true
	}
	for _, l := range []int{5, 6, 9, 10} {
		if !lines[l] {
			t.Errorf("no AWS key found at line %d (found %v)", l, lines)
		}
	}
	plan, err := st.Plan(ctx, s)
	if err != nil || len(plan) != 4 {
		t.Fatalf("plan %v %v", plan, err)
	}
	if err := st.Apply(ctx, s); err != nil {
		t.Fatal(err)
	}
	out := string(s.Files()["wp-config.php"])
	for _, k := range []string{access, secret, access2, secret2} {
		if strings.Contains(out, k) {
			t.Errorf("%s still in wp-config.php:\n%s", core.MaskKey(k), out)
		}
	}
	for _, keep := range []string{"'provider'          => 'aws',", "'access-key-id'     => '',", "'secret-access-key' => \"\",", "define( 'AWS_SECRET_ACCESS_KEY', '' );"} {
		if !strings.Contains(out, keep) {
			t.Errorf("want %q in\n%s", keep, out)
		}
	}
	if _, fs, _ := st.Check(ctx, s); len(fs) != 0 {
		t.Errorf("after apply: %v", fs)
	}
	if got := len(st.(*keyFileStep).AppliedKeys()); got != 4 {
		t.Errorf("applied %d keys", got)
	}
}
