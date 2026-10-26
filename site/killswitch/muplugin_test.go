// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package killswitch

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/internal/testutil"
)

func TestMuPluginGolden(t *testing.T) {
	testutil.Golden(t, "roadie-staging.php", MuPlugin(Options{Noindex: true, Version: "1.0.0"}))
	testutil.Golden(t, "roadie-staging-no-noindex.php", MuPlugin(Options{Version: "1.0.0"}))
}

func TestMuPluginContents(t *testing.T) {
	b := string(MuPlugin(Options{Noindex: true, Version: "1.0.0"}))
	for _, want := range []string{
		MuPluginMarker,
		"Roadie (rhythm-roadie 1.0.0) wrote this file. Delete it to turn the guard off.",
		"'production' === wp_get_environment_type()",
		"add_filter( 'action_scheduler_allow_async_request_runner', '__return_false'",
		"remove_action( 'action_scheduler_run_queue', array( ActionScheduler_QueueRunner::instance(), 'run' ) );",
		"}, 20 );",
		"'action_scheduler_queue_runner_concurrent_batches'",
		"add_filter( 'woocommerce_subscriptions_is_duplicate_site', '__return_true'",
		"add_filter( 'jetpack_offline_mode', '__return_true'",
		"file_exists( __DIR__ . '/rhythm-echo.php' )",
		"'pre_wp_mail'",
		"header( 'X-Robots-Tag: noindex, nofollow' );",
		"'pre_option_blog_public'",
		"add_filter( 'wp_sitemaps_enabled', '__return_false'",
		"User-agent: GPTBot\nDisallow: /",
		"User-agent: Applebot-Extended",
		"'Pseudonymised staging copy (Roadie)'",
	} {
		if !strings.Contains(b, want) {
			t.Errorf("mu-plugin lacks %q", want)
		}
	}
	off := string(MuPlugin(Options{Version: "1.0.0"}))
	for _, gone := range []string{"wp_robots", "X-Robots-Tag", "robots_txt", "blog_public", "wp_sitemaps_enabled"} {
		if strings.Contains(off, gone) {
			t.Errorf("noindex off still has %s", gone)
		}
	}
	if !strings.Contains(string(MuPlugin(Options{})), "rhythm-roadie dev") {
		t.Error("no version fallback")
	}
	if strings.Contains(string(MuPlugin(Options{Version: "1*/ evil\n"})), "1*/") {
		t.Error("a version can close the header comment")
	}
}

// writeTemp writes b to a new file and returns its path.
func writeTemp(t *testing.T, name string, b []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestMuPluginPHPLint(t *testing.T) {
	php := testutil.RequirePHP(t)
	for _, o := range []Options{{Noindex: true}, {}} {
		p := writeTemp(t, "roadie-staging.php", MuPlugin(o))
		if out, err := exec.Command(php, "-l", p).CombinedOutput(); err != nil { // #nosec G204 -- the test's own php
			t.Fatalf("php -l: %v\n%s", err, out)
		}
	}
}

// TestMuPluginHooks loads the plugin in PHP with stand-ins for WordPress
// and checks what it registers: nothing on production, the mail block
// only without the Echo connector.
func TestMuPluginHooks(t *testing.T) {
	php := testutil.RequirePHP(t)
	plugin := writeTemp(t, "plugin.php", MuPlugin(Options{Noindex: true}))
	run := func(env, echo string) map[string]bool {
		out, err := exec.Command(php, filepath.Join("testdata", "harness.php"), plugin, env, echo).CombinedOutput() // #nosec G204 -- the test's own php
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		var hooks [][]any
		if err := json.Unmarshal(out, &hooks); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		got := map[string]bool{}
		for _, h := range hooks {
			got[h[0].(string)+" "+h[1].(string)] = true
		}
		return got
	}
	if got := run("production", "0"); len(got) != 0 {
		t.Fatalf("production: %v", got)
	}
	got := run("staging", "0")
	for _, want := range []string{"add " + hookAsyncRunner, "add " + hookBatches, "remove " + hookRunQueue, "add " + hookDuplicateSite,
		"add " + hookJetpack, "add " + hookPreMail, "add " + hookRobots, "add send_headers", "add robots_txt", "add admin_bar_menu"} {
		if !got[want] {
			t.Errorf("staging: no %s in %v", want, got)
		}
	}
	if got := run("staging", "1"); got["add "+hookPreMail] {
		t.Error("pre_wp_mail blocks mail although the Echo connector is there")
	}
}

func TestRobotsTxt(t *testing.T) {
	r := RobotsTxt()
	if !strings.HasPrefix(r, "User-agent: *\nDisallow: /\n") || strings.Count(r, "Disallow: /\n") != len(Crawlers)+1 {
		t.Fatalf("%q", r)
	}
}
