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

// A live key in a file wp-config.php includes (or one that file
// includes) runs as wp-config.php's own does: the key step finds it,
// plans it, swaps or blanks it there and its check passes after.
// Negative: included files without a live key are left as they are and
// an include of a missing file is no finding.
func TestKeyStepIncludedFiles(t *testing.T) {
	key := testutil.J("sk_", "live_", "51Hq", "Zx0aTq2wN8eLm3VbYpQr")
	cfg := "<?php\nrequire_once __DIR__ . '/wp-config-local.php';\ninclude __DIR__ . '/missing.php';\n" +
		"define( 'DB_NAME', 'shop' );\nrequire_once ABSPATH . 'wp-settings.php';\n"
	local := "<?php\ndefine( 'WP_DEBUG', false );\nrequire __DIR__ . '/conf/keys.php';\n"
	keysFile := "<?php\n\ndefine( 'STRIPE_SECRET', '" + key + "' );\n"
	s := testutil.FakeSite(map[string][]byte{"wp-config.php": []byte(cfg), "wp-config-local.php": []byte(local), "conf/keys.php": []byte(keysFile)})
	st := keyStep(Options{})
	ctx := context.Background()
	se, fs, err := st.Check(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if se.Status != "on" || len(fs) != 1 || fs[0].ID != core.KeyLiveID("stripe") || fs[0].Locations[0].File != "conf/keys.php" || fs[0].Locations[0].Line != 3 {
		t.Fatalf("check before: %+v %+v", se, fs)
	}
	plan, err := st.Plan(ctx, s)
	if err != nil || len(plan) != 1 || plan[0].File != "conf/keys.php" {
		t.Fatalf("plan %+v %v", plan, err)
	}
	if err := st.Apply(ctx, s); err != nil {
		t.Fatal(err)
	}
	files := s.Files()
	if out := string(files["conf/keys.php"]); strings.Contains(out, key) || !strings.Contains(out, "define( 'STRIPE_SECRET',") {
		t.Errorf("conf/keys.php after apply:\n%s", out)
	}
	if string(files["wp-config.php"]) != cfg || string(files["wp-config-local.php"]) != local {
		t.Error("files without a live key changed")
	}
	if got := st.(*keyFileStep).AppliedKeys(); len(got) != 1 || got[0].At.File != "conf/keys.php" {
		t.Errorf("applied %+v", got)
	}
	if se, fs, _ := st.Check(ctx, s); se.Status != "off" || len(fs) != 0 {
		t.Errorf("after apply: %+v %+v", se, fs)
	}
}

// An include Roadie can't follow leaves the key check unknown, not off.
func TestKeyStepUnfollowedInclude(t *testing.T) {
	cfg := "<?php\nrequire getenv( 'KEYS_FILE' );\nrequire_once ABSPATH . 'wp-settings.php';\n"
	s := testutil.FakeSite(map[string][]byte{"wp-config.php": []byte(cfg)})
	se, fs, err := keyStep(Options{}).Check(context.Background(), s)
	if err != nil || len(fs) != 0 || se.Status != "unknown" || !strings.Contains(se.How, "wp-config.php line 2") {
		t.Errorf("check: %+v %+v %v", se, fs, err)
	}
}
