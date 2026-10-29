// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package verify

import (
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/internal/testutil"
)

// includeSite is a local WordPress site whose wp-config.php requires
// wp-config-local.php, which requires conf/keys.php; files adds to it.
func includeSite(files map[string][]byte) *testutil.Site {
	all := map[string][]byte{
		"wp-config.php": []byte("<?php\nrequire_once __DIR__ . '/wp-config-local.php';\ndefine( 'DB_NAME', 'shop' );\n" +
			"require_once ABSPATH . 'wp-settings.php';\n"),
		"wp-config-local.php": []byte("<?php\ndefine( 'WP_DEBUG', false );\nrequire __DIR__ . '/conf/keys.php';\n"),
	}
	for k, v := range files {
		all[k] = v
	}
	s := testutil.FakeSite(all)
	s.EP.Kind = core.LocalSite
	return s
}

// TestRunIncludedConfigKeys: a live key defined in a file wp-config.php
// includes, or in a file that one includes, runs as wp-config.php's
// own does and fails verify where it is.
func TestRunIncludedConfigKeys(t *testing.T) {
	for _, c := range []struct{ file, src string }{
		{"wp-config-local.php", "<?php\ndefine( 'WP_DEBUG', false );\ndefine( 'STRIPE_SECRET', '" + liveStripe() + "' );\n"},
		{"conf/keys.php", "<?php\n\ndefine( 'STRIPE_SECRET', '" + liveStripe() + "' );\n"},
	} {
		st := includeSite(map[string][]byte{c.file: []byte(c.src)})
		res := runJob(t, Job{Site: st, Source: source(options([]string{"1", "blogname", "Shop"}))})
		f := find(res, "key.stripe-live")
		if f == nil || f.Locations[0].File != c.file || f.Locations[0].Line != 3 {
			t.Errorf("%s: %+v", c.file, f)
		}
	}
}

// TestRunMuPluginKey: a live key in an mu-plugin is read like one in a
// plugin.
func TestRunMuPluginKey(t *testing.T) {
	st := includeSite(map[string][]byte{
		"wp-content/mu-plugins/keys.php": []byte("<?php\n$stripe = '" + liveStripe() + "';\n"),
	})
	res := runJob(t, Job{Site: st, Source: source(options([]string{"1", "blogname", "Shop"}))})
	f := find(res, "key.stripe-live")
	if f == nil || f.Locations[0].File != "wp-content/mu-plugins/keys.php" || f.Locations[0].Line != 2 {
		t.Fatalf("mu-plugin key: %+v", f)
	}
}

// TestRunIncludesClean: included files and mu-plugins without a live key
// pass; an include Roadie can't follow is listed as skipped.
func TestRunIncludesClean(t *testing.T) {
	st := includeSite(map[string][]byte{
		"conf/keys.php":                  []byte("<?php\ndefine( 'STRIPE_SECRET', '' );\n"),
		"wp-content/mu-plugins/keys.php": []byte("<?php\n$stripe = getenv( 'STRIPE_SECRET' );\n"),
	})
	res := runJob(t, Job{Site: st, Source: source(options([]string{"1", "blogname", "Shop"}))})
	for _, f := range res.Findings {
		if strings.HasPrefix(f.ID, "key.") {
			t.Errorf("no key: %+v", f)
		}
	}
	for _, s := range res.Coverage.Skipped {
		if s.Table == "wp-config.php include" {
			t.Errorf("every include is followed: %+v", s)
		}
	}
	st = includeSite(map[string][]byte{
		"wp-config-local.php": []byte("<?php\nrequire getenv( 'KEYS_FILE' );\n"),
	})
	res = runJob(t, Job{Site: st, Source: source(options([]string{"1", "blogname", "Shop"}))})
	found := false
	for _, s := range res.Coverage.Skipped {
		found = found || s.Table == "wp-config.php include" && strings.Contains(s.Reason, "wp-config-local.php line 2")
	}
	if !found {
		t.Errorf("an include of an expression is listed as skipped: %+v", res.Coverage.Skipped)
	}
}
