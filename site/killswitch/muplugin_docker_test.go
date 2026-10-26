// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build docker

package killswitch

import (
	"crypto/rand"
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
	dockerWordPress     = "wordpress:6.8-php8.3-apache"
	dockerWPCLI         = "wordpress:cli-2.12.0-php8.3"
	dockerMySQL         = "mysql:8.4"
	pinWooCommerce      = "9.8.5"
	pinSubscriptionsZip = "https://github.com/Automattic/woocommerce-subscriptions-core/archive/refs/tags/8.2.0.zip"
	subscriptionsDir    = "woocommerce-subscriptions-core-8.2.0"
)

func docker(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("docker", args...).CombinedOutput() // #nosec G204 -- the test's own docker calls
	if err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestMuPluginInWordPress boots WordPress with WooCommerce (which bundles
// Action Scheduler), installs roadie-staging.php and checks that a due
// action stays pending through a page load and wp cron event run
// --due-now, that the duplicate-site filter answers true and that every
// hook name is in the pinned sources. Nightly only (build tag docker).
func TestMuPluginInWordPress(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not on PATH")
	}
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
	deadline := time.Now().Add(3 * time.Minute)
	for {
		out, err := exec.Command("docker", append(append([]string{"run", "--rm", "--network", net, "--volumes-from", wpName, "--user", "33:33"}, dbEnv...), dockerWPCLI, "wp", "db", "check")...).CombinedOutput() // #nosec G204 -- the test's own docker calls
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("database never came up: %s", out)
		}
		time.Sleep(3 * time.Second)
	}
	wp("core", "install", "--url=http://localhost", "--title=Roadie test", "--admin_user=admin",
		"--admin_password="+dbPass, "--admin_email=admin@example.invalid", "--skip-email")
	wp("plugin", "install", "woocommerce", "--version="+pinWooCommerce, "--activate")
	wp("plugin", "install", pinSubscriptionsZip)
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

	plugin := filepath.Join(t.TempDir(), "roadie-staging.php")
	if err := os.WriteFile(plugin, MuPlugin(Options{Noindex: true, Version: "test"}), 0o644); err != nil { // #nosec G306 -- the web server reads it
		t.Fatal(err)
	}
	docker(t, "exec", wpName, "mkdir", "-p", "/var/www/html/wp-content/mu-plugins")
	docker(t, "cp", plugin, wpName+":/var/www/html/wp-content/mu-plugins/roadie-staging.php")
	docker(t, "exec", wpName, "chown", "-R", "33:33", "/var/www/html/wp-content/mu-plugins")

	wp("eval", `as_schedule_single_action( time() - 60, 'roadie_test_hook' );`)
	docker(t, "exec", wpName, "curl", "-s", "-o", "/dev/null", "http://localhost/")
	time.Sleep(6 * time.Second) // longer than Action Scheduler's async sleep
	wp("cron", "event", "run", "--due-now")
	if got := wp("eval", `echo count( as_get_scheduled_actions( array( 'hook' => 'roadie_test_hook', 'status' => 'pending' ), 'ids' ) );`); got != "1" {
		t.Fatalf("pending actions after a page load and cron: %q, want 1", got)
	}
	if got := wp("eval", `echo apply_filters( 'woocommerce_subscriptions_is_duplicate_site', false ) ? 'yes' : 'no';`); got != "yes" {
		t.Fatalf("duplicate site: %q", got)
	}
	if got := wp("eval", `echo function_exists( 'wcs_is_duplicate_site' ) && ! wcs_is_duplicate_site() ? 'no' : 'yes';`); got != "yes" {
		t.Fatalf("wcs_is_duplicate_site: %q", got)
	}
	if got := wp("eval", `echo get_option( 'blog_public' );`); got != "0" {
		t.Fatalf("blog_public through the filter: %q", got)
	}
}
