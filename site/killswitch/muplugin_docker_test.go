// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build docker

package killswitch

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Pinned versions for the nightly mu-plugin test. The hook names the
// mu-plugin uses are checked against these sources, not assumed.
const (
	dockerWordPress  = "wordpress:6.8.3-php8.3-apache"
	dockerWPCLI      = "wordpress:cli-2.12.0-php8.3"
	dockerMySQL      = "mysql:8.4"
	subscriptionsDir = "woocommerce-subscriptions-core-8.2.0"
)

// pinnedZip is a plugin zip the test installs, checked against its
// SHA-256 before it reaches the container.
type pinnedZip struct {
	slug, version, url, sha256 string
}

var (
	zipWooCommerce = pinnedZip{"woocommerce", "9.8.5",
		"https://downloads.wordpress.org/plugin/woocommerce.9.8.5.zip",
		"9008e72eacda8f3bda01d0b704502eaddd8bc147651d61bd9aa7a015bf583964"}
	// subscriptions-core has no wordpress.org release: this is the
	// source archive of its 8.2.0 tag.
	zipSubscriptionsCore = pinnedZip{"woocommerce-subscriptions-core", "8.2.0",
		"https://github.com/Automattic/woocommerce-subscriptions-core/archive/refs/tags/8.2.0.zip",
		"1d2a898b65eb76473558d797407b3e41a0a3cbf898c4a2e5ae24e1df4ccf893e"}
)

// subscriptionsLoader loads subscriptions-core the way WooCommerce
// Subscriptions does: its own main file is only a header, so activating
// it alone would load nothing. The file name sorts after woocommerce/,
// so WooCommerce is loaded first.
const subscriptionsLoader = `<?php
/**
 * Plugin Name: Roadie test loader for WooCommerce Subscriptions Core
 * Description: Loads subscriptions-core for Roadie's nightly mu-plugin test.
 */
if ( class_exists( 'WooCommerce' ) && ! class_exists( 'WC_Subscriptions_Core_Plugin' ) ) {
	require_once WP_PLUGIN_DIR . '/` + subscriptionsDir + `/includes/class-wc-subscriptions-core-plugin.php';
	new WC_Subscriptions_Core_Plugin();
}
`

// fetchZip returns a local copy of z from the ROADIE_PLUGIN_ZIPS folder
// of <slug>.<version>.zip files, verified against its pinned SHA-256.
// The test never downloads: the nightly workflow fetches the zips in a
// step of its own (site/testdata/fetch-plugin-zips.sh).
func fetchZip(t *testing.T, dir string, z pinnedZip) string {
	t.Helper()
	name := z.slug + "." + z.version + ".zip"
	dst := filepath.Join(dir, name)
	b, err := os.ReadFile(filepath.Join(pluginZips(t), name)) // #nosec G304 -- the tester's own folder
	if err != nil {
		t.Fatalf("ROADIE_PLUGIN_ZIPS: %v (the pinned zip is %s)", err, z.url)
	}
	sum := sha256.Sum256(b)
	if got := hex.EncodeToString(sum[:]); got != z.sha256 {
		t.Fatalf("%s: sha256 %s, want %s", name, got, z.sha256)
	}
	if err := os.WriteFile(dst, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return dst
}

// pluginZips is the ROADIE_PLUGIN_ZIPS folder; without it the test
// skips, since tests never reach the internet.
func pluginZips(t *testing.T) string {
	t.Helper()
	zips := os.Getenv("ROADIE_PLUGIN_ZIPS")
	if zips == "" {
		t.Skip("set ROADIE_PLUGIN_ZIPS to a folder of the pinned plugin zips (site/testdata/fetch-plugin-zips.sh)")
	}
	return zips
}

func docker(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("docker", args...).CombinedOutput() // #nosec G204 -- the test's own docker calls
	if err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestMuPluginInWordPress boots WordPress with WooCommerce (which bundles
// Action Scheduler) and subscriptions-core, all from pinned zips. Before
// the mu-plugin it proves the controls: Subscriptions sees the live site
// and a due action runs. With roadie-staging.php it checks that a due
// action and a due subscription renewal stay pending through a page load
// and wp cron event run --due-now, that Subscriptions calls the site a
// duplicate and that every hook name is in the pinned sources. Nightly
// only (build tag docker).
func TestMuPluginInWordPress(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not on PATH")
	}
	pluginZips(t)
	zipDir := t.TempDir()
	zips := []string{fetchZip(t, zipDir, zipWooCommerce), fetchZip(t, zipDir, zipSubscriptionsCore)}

	var rnd [4]byte
	_, _ = rand.Read(rnd[:])
	id := "roadie-mu-" + hex.EncodeToString(rnd[:])
	dbPass := "roadie" + "-test-" + hex.EncodeToString(rnd[:])
	net, dbName, wpName, vol := id, id+"-db", id+"-wp", id+"-html"
	docker(t, "network", "create", net)
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", wpName, dbName).Run()
		_ = exec.Command("docker", "network", "rm", net).Run()
		_ = exec.Command("docker", "volume", "rm", vol).Run()
	})
	docker(t, "run", "-d", "--name", dbName, "--network", net, "-e", "MYSQL_DATABASE=wp", "-e", "MYSQL_USER=wp",
		"-e", "MYSQL_PASSWORD="+dbPass, "-e", "MYSQL_RANDOM_ROOT_PASSWORD=1", dockerMySQL)
	dbEnv := []string{"-e", "WORDPRESS_DB_HOST=" + dbName, "-e", "WORDPRESS_DB_USER=wp", "-e", "WORDPRESS_DB_PASSWORD=" + dbPass, "-e", "WORDPRESS_DB_NAME=wp"}
	docker(t, append(append([]string{"run", "-d", "--name", wpName, "--network", net, "-v", vol + ":/var/www/html"}, dbEnv...), dockerWordPress)...)
	wp := func(args ...string) string {
		t.Helper()
		base := append(append([]string{"run", "--rm", "--network", net, "--volumes-from", wpName, "--user", "33:33"}, dbEnv...), dockerWPCLI, "wp")
		return docker(t, append(base, args...)...)
	}
	// The mysql client in the WP-CLI image refuses MySQL 8.4's self-signed
	// TLS, so readiness is asked inside the database container.
	deadline := time.Now().Add(3 * time.Minute)
	for {
		out, err := exec.Command("docker", "exec", dbName, "mysql", "-uwp", "-p"+dbPass, "-h127.0.0.1", "-e", "SELECT 1", "wp").CombinedOutput() // #nosec G204 -- the test's own docker calls
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("database never came up: %s", out)
		}
		time.Sleep(3 * time.Second)
	}
	// The WordPress image copies its files in on first start.
	for docker(t, "exec", wpName, "sh", "-c", "test -f /var/www/html/wp-config.php && echo ok || echo wait") != "ok" {
		if time.Now().After(deadline) {
			t.Fatal("WordPress files never came up")
		}
		time.Sleep(2 * time.Second)
	}
	wp("core", "install", "--url=http://localhost", "--title=Roadie test", "--admin_user=admin",
		"--admin_password="+dbPass, "--admin_email=admin@example.invalid", "--skip-email")

	// The zips go into the shared volume, where the WP-CLI container sees
	// them. They are removed once installed.
	docker(t, "exec", wpName, "mkdir", "-p", "/var/www/html/roadie-zips")
	for _, z := range zips {
		docker(t, "cp", z, wpName+":/var/www/html/roadie-zips/"+filepath.Base(z))
	}
	docker(t, "exec", wpName, "chown", "-R", "33:33", "/var/www/html/roadie-zips")
	wp("plugin", "install", "/var/www/html/roadie-zips/"+filepath.Base(zips[0]), "--activate")
	wp("plugin", "install", "/var/www/html/roadie-zips/"+filepath.Base(zips[1]))
	docker(t, "exec", wpName, "rm", "-rf", "/var/www/html/roadie-zips")
	loader := filepath.Join(t.TempDir(), "zz-roadie-subscriptions-loader.php")
	if err := os.WriteFile(loader, []byte(subscriptionsLoader), 0o644); err != nil { // #nosec G306 -- the web server reads it
		t.Fatal(err)
	}
	docker(t, "cp", loader, wpName+":/var/www/html/wp-content/plugins/zz-roadie-subscriptions-loader.php")
	docker(t, "exec", wpName, "chown", "33:33", "/var/www/html/wp-content/plugins/zz-roadie-subscriptions-loader.php")
	wp("plugin", "activate", "zz-roadie-subscriptions-loader")
	wp("config", "set", "WP_ENVIRONMENT_TYPE", "staging")

	// The hook names, in the pinned sources.
	for hook, dir := range map[string]string{
		hookAsyncRunner:   "wp-content/plugins/woocommerce/packages/action-scheduler",
		hookBatches:       "wp-content/plugins/woocommerce/packages/action-scheduler",
		hookRunQueue:      "wp-content/plugins/woocommerce/packages/action-scheduler",
		hookDuplicateSite: "wp-content/plugins/" + subscriptionsDir,
	} {
		docker(t, "exec", wpName, "grep", "-rqF", "'"+hook+"'", "/var/www/html/"+dir)
	}

	// Controls without the mu-plugin. Subscriptions is loaded and, with
	// its URL lock set to this site, calls it the live site.
	if got := wp("eval", `echo class_exists( 'WCS_Staging' ) ? 'loaded' : 'missing';`); got != "loaded" {
		t.Fatalf("subscriptions-core: %q, want loaded", got)
	}
	wp("eval", `WCS_Staging::set_duplicate_site_url_lock();`)
	if got := wp("eval", `echo WCS_Staging::is_duplicate_site() ? 'duplicate' : 'live';`); got != "live" {
		t.Fatalf("control: Subscriptions calls the site %q, want live", got)
	}
	// A due action runs through a page load and cron without the
	// mu-plugin, so the pending check below can fail.
	wp("eval", `as_schedule_single_action( time() - 60, 'roadie_control_hook' );`)
	docker(t, "exec", wpName, "curl", "-s", "-o", "/dev/null", "http://localhost/")
	time.Sleep(6 * time.Second) // longer than Action Scheduler's async sleep
	wp("cron", "event", "run", "--due-now")
	if got := wp("eval", `echo count( as_get_scheduled_actions( array( 'hook' => 'roadie_control_hook', 'status' => 'pending' ), 'ids' ) );`); got != "0" {
		t.Fatalf("control: pending actions %q, want 0 (the due action should have run)", got)
	}

	plugin := filepath.Join(t.TempDir(), "roadie-staging.php")
	if err := os.WriteFile(plugin, MuPlugin(Options{Noindex: true, Version: "test"}), 0o644); err != nil { // #nosec G306 -- the web server reads it
		t.Fatal(err)
	}
	docker(t, "exec", wpName, "mkdir", "-p", "/var/www/html/wp-content/mu-plugins")
	docker(t, "cp", plugin, wpName+":/var/www/html/wp-content/mu-plugins/roadie-staging.php")
	docker(t, "exec", wpName, "chown", "-R", "33:33", "/var/www/html/wp-content/mu-plugins")

	wp("eval", `as_schedule_single_action( time() - 60, 'roadie_test_hook' );`)
	wp("eval", `as_schedule_single_action( time() - 60, 'woocommerce_scheduled_subscription_payment', array( 'subscription_id' => 4242 ), 'woocommerce-subscriptions' );`)
	docker(t, "exec", wpName, "curl", "-s", "-o", "/dev/null", "http://localhost/")
	time.Sleep(6 * time.Second) // longer than Action Scheduler's async sleep
	wp("cron", "event", "run", "--due-now")
	for _, hook := range []string{"roadie_test_hook", "woocommerce_scheduled_subscription_payment"} {
		if got := wp("eval", `echo count( as_get_scheduled_actions( array( 'hook' => '`+hook+`', 'status' => 'pending' ), 'ids' ) );`); got != "1" {
			t.Fatalf("pending %s after a page load and cron: %q, want 1", hook, got)
		}
	}
	if got := wp("eval", `echo WCS_Staging::is_duplicate_site() ? 'duplicate' : 'live';`); got != "duplicate" {
		t.Fatalf("Subscriptions calls the site %q with the mu-plugin, want duplicate", got)
	}
	if got := wp("eval", `echo get_option( 'blog_public' );`); got != "0" {
		t.Fatalf("blog_public through the filter: %q", got)
	}
}
