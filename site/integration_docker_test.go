// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build docker

package site_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/keys"
	"github.com/RhythmShahriar/roadie/site"
	"github.com/RhythmShahriar/roadie/site/exposure"
	"github.com/RhythmShahriar/roadie/site/killswitch"
)

// Pinned versions for the nightly kill-switch test.
const (
	ksWordPress   = "wordpress:6.8.3-php8.3-apache"
	ksWPCLI       = "wordpress:cli-2.12.0-php8.3"
	ksMySQL       = "mysql:8.4"
	ksWooCommerce = "9.8.5"
	ksWPMailSMTP  = "4.10.3"
)

// ksZipSHA256 pins the plugin zips from ROADIE_PLUGIN_ZIPS, as
// site/testdata/fetch-plugin-zips.sh does.
var ksZipSHA256 = map[string]string{
	"woocommerce." + ksWooCommerce + ".zip": "9008e72eacda8f3bda01d0b704502eaddd8bc147651d61bd9aa7a015bf583964",
	"wp-mail-smtp." + ksWPMailSMTP + ".zip": "39fdb36add56c42ae803684cf94053320e4ba1262d24933d2eb16c215266c6a4",
}

// sinkPHP listens on 2525 (SMTP) and 8080 (HTTP) and prints one line per
// connection: the sink the kill switch must keep every message from.
const sinkPHP = `<?php
$servers = array();
foreach ( array( 2525, 8080 ) as $port ) {
	$s = stream_socket_server( "tcp://0.0.0.0:$port", $errno, $errstr );
	if ( ! $s ) {
		fwrite( STDERR, "$errstr\n" );
		exit( 1 );
	}
	$servers[ $port ] = $s;
}
echo "ready\n";
while ( true ) {
	$read   = array_values( $servers );
	$write  = null;
	$except = null;
	if ( stream_select( $read, $write, $except, null ) < 1 ) {
		continue;
	}
	foreach ( $read as $s ) {
		$c = @stream_socket_accept( $s );
		if ( ! $c ) {
			continue;
		}
		$port = array_search( $s, $servers, true );
		echo "connection $port\n";
		fwrite( $c, 2525 === $port ? "421 sink closes\r\n" : "HTTP/1.1 200 OK\r\nContent-Length: 0\r\nConnection: close\r\n\r\n" );
		fclose( $c );
	}
}
`

// fixturePlugin mails on every init and from a cron hook. It lets
// WordPress reach the sink's private address, as a real relay or ERP
// would be public.
const fixturePlugin = `<?php
/*
 * Plugin Name: Roadie fixture mailer
 * Description: Test fixture: sends a mail on every request.
 */
add_filter( 'http_request_host_is_external', '__return_true' );
add_action( 'init', function () {
	$ok = wp_mail( 'customer@example.invalid', 'Roadie fixture ' . ( defined( 'WP_CLI' ) ? 'cli' : 'web' ), 'Hello from the fixture plugin.' );
	update_option( 'roadie_fixture_last', $ok ? 'sent' : 'failed', false );
}, 20 );
add_action( 'roadie_fixture_cron', function () {
	wp_mail( 'customer@example.invalid', 'Roadie fixture cron', 'Hello from cron.' );
} );
`

// applyRulesPHP does what the engine does with the kill switch's
// DataRules, for the rules this test meets: fixed or blank leaves of an
// option, fixed or blank columns and truncated tables.
const applyRulesPHP = `<?php
global $wpdb;
$ops = json_decode( base64_decode( 'RULES_B64' ), true );
foreach ( $ops as $op ) {
	$table = str_replace( '{prefix}', $wpdb->prefix, $op['table'] );
	if ( $wpdb->get_var( $wpdb->prepare( 'SHOW TABLES LIKE %s', $table ) ) !== $table ) {
		continue;
	}
	if ( 'truncate' === $op['kind'] ) {
		$wpdb->query( "TRUNCATE TABLE ` + "`$table`" + `" );
		continue;
	}
	if ( 'option' === $op['kind'] ) {
		$v = get_option( $op['option'] );
		if ( ! is_array( $v ) ) {
			continue;
		}
		$node =& $v;
		$keys = explode( '.', $op['path'] );
		$last = array_pop( $keys );
		foreach ( $keys as $k ) {
			if ( ! isset( $node[ $k ] ) || ! is_array( $node[ $k ] ) ) {
				continue 2;
			}
			$node =& $node[ $k ];
		}
		if ( array_key_exists( $last, $node ) ) {
			$node[ $last ] = $op['value'];
			unset( $node );
			update_option( $op['option'], $v );
		}
		continue;
	}
	if ( $op['where'] ) {
		$wpdb->update( $table, array( $op['column'] => $op['value'] ), $op['where'] );
		continue;
	}
	$wpdb->query( $wpdb->prepare( "UPDATE ` + "`$table` SET `{$op['column']}`" + ` = %s", $op['value'] ) );
}
echo "applied\n";
`

// ruleOp is one DataRule in the shape applyRulesPHP reads.
type ruleOp struct {
	Kind   string            `json:"kind"`
	Table  string            `json:"table"`
	Option string            `json:"option,omitempty"`
	Path   string            `json:"path,omitempty"`
	Column string            `json:"column,omitempty"`
	Where  map[string]string `json:"where"`
	Value  string            `json:"value"`
}

func ruleOps(t *testing.T, rules []core.Rule) []ruleOp {
	t.Helper()
	var out []ruleOp
	for _, r := range rules {
		op := ruleOp{Table: r.Table, Value: r.Value, Where: map[string]string{}}
		switch r.Action {
		case core.Truncate:
			op.Kind = "truncate"
			out = append(out, op)
			continue
		case core.Blank, core.SwapKey:
			op.Value = ""
		case core.Fixed:
		default:
			t.Fatalf("rule %s: action %s", r.ID, r.Action)
		}
		for k, v := range r.Where {
			if strings.ContainsAny(v, "*?[") {
				t.Fatalf("rule %s: glob %q", r.ID, v)
			}
			op.Where[k] = v
		}
		if r.Path != "" {
			op.Kind, op.Option, op.Path = "option", r.Where["option_name"], r.Path
		} else {
			op.Kind, op.Column = "column", r.Column
		}
		out = append(out, op)
	}
	return out
}

func run(t *testing.T, stdin string, name string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(name, args...) // #nosec G204 -- the test's own docker and go calls
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func must(t *testing.T, stdin string, name string, args ...string) string {
	t.Helper()
	out, err := run(t, stdin, name, args...)
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return out
}

// buildEcho builds rhythm-echo for Linux from the Echo module Roadie
// already depends on, so the test runs the same Echo without a registry.
func buildEcho(t *testing.T) string {
	t.Helper()
	dir := must(t, "", "go", "list", "-m", "-f", "{{.Dir}}", "github.com/RhythmShahriar/echo")
	bin := filepath.Join(t.TempDir(), "rhythm-echo")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/rhythm-echo") // #nosec G204 -- the test's own build
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build rhythm-echo: %v\n%s", err, out)
	}
	return filepath.Dir(bin)
}

// TestKillSwitchInWordPress (security X6, K1, K2; qa r22) boots WordPress
// with WP Mail SMTP sending to a sink, a plugin that mails on init, an
// active WooCommerce webhook pointing at the sink and Echo in seal mode.
// First it proves the fixture reaches the sink. Then it applies the
// WordPress Steps and DataRules to the container's files and database: a
// page load and wp cron event run --due-now send nothing to the sink,
// Echo captures the mail and the webhook is paused. With Echo stopped,
// mail fails closed and the sink still sees nothing. Nightly only (build
// tag docker).
func TestKillSwitchInWordPress(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not on PATH")
	}
	if _, err := run(t, "", "docker", "info"); err != nil {
		t.Skip("docker isn't running")
	}
	zips := os.Getenv("ROADIE_PLUGIN_ZIPS")
	if zips == "" {
		t.Skip("set ROADIE_PLUGIN_ZIPS to a folder of the pinned plugin zips (site/testdata/fetch-plugin-zips.sh)")
	}
	ctx := context.Background()
	echoBin := buildEcho(t)

	var rnd [4]byte
	_, _ = rand.Read(rnd[:])
	id := "roadie-ks-" + hex.EncodeToString(rnd[:])
	dbPass := "roadie" + "-test-" + hex.EncodeToString(rnd[:])
	netName, dbName, wpName, echoName, sinkName := id, id+"-db", id+"-wp", id+"-echo", id+"-sink"

	html := t.TempDir()
	if err := os.Chmod(html, 0o777); err != nil { // #nosec G302 -- the containers' www-data writes here
		t.Fatal(err)
	}
	scripts := t.TempDir()
	if err := os.WriteFile(filepath.Join(scripts, "sink.php"), []byte(sinkPHP), 0o644); err != nil { // #nosec G306 -- read by the container
		t.Fatal(err)
	}

	must(t, "", "docker", "network", "create", netName)
	t.Cleanup(func() {
		_, _ = run(t, "", "docker", "rm", "-f", wpName, dbName, echoName, sinkName)
		_, _ = run(t, "", "docker", "network", "rm", netName)
	})
	// Runs before the removal above: files the containers own become
	// removable by t.TempDir's cleanup.
	t.Cleanup(func() { _, _ = run(t, "", "docker", "exec", wpName, "chmod", "-R", "a+rwX", "/var/www/html") })

	must(t, "", "docker", "run", "-d", "--name", dbName, "--network", netName, "-e", "MYSQL_DATABASE=wp", "-e", "MYSQL_USER=wp",
		"-e", "MYSQL_PASSWORD="+dbPass, "-e", "MYSQL_RANDOM_ROOT_PASSWORD=1", ksMySQL)
	must(t, "", "docker", "run", "-d", "--name", sinkName, "--network", netName, "-v", scripts+":/fixture:ro",
		"--entrypoint", "php", ksWordPress, "/fixture/sink.php")
	must(t, "", "docker", "run", "-d", "--name", echoName, "--network", netName, "--user", "33:33", "-e", "HOME=/tmp",
		"-v", echoBin+":/echo:ro", "--entrypoint", "/echo/rhythm-echo", ksWordPress,
		"up", "--seal", "--listen=0.0.0.0:8025", "--smtp=0.0.0.0:1025", "--private-network")
	// No WORDPRESS_* variables: the image copies WordPress and leaves
	// wp-config.php to wp config create, so it holds literal defines.
	must(t, "", "docker", "run", "-d", "--name", wpName, "--network", netName, "-v", html+":/var/www/html", ksWordPress)

	// ROADIE_PLUGIN_ZIPS names the folder of <slug>.<version>.zip files
	// the plugins install from; the test itself never downloads.
	wpIn := func(stdin string, args ...string) (string, error) {
		base := []string{"run", "--rm", "--network", netName, "-v", html + ":/var/www/html", "--user", "33:33"}
		base = append(base, "-v", zips+":/zips:ro")
		if stdin != "" {
			base = append(base, "-i")
		}
		return run(t, stdin, "docker", append(append(base, ksWPCLI, "wp"), args...)...)
	}
	wp := func(args ...string) string {
		t.Helper()
		out, err := wpIn("", args...)
		if err != nil {
			t.Fatalf("wp %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return out
	}
	page := func() {
		t.Helper()
		must(t, "", "docker", "exec", wpName, "curl", "-s", "-o", "/dev/null", "http://localhost/")
	}
	sinkHits := func() string {
		t.Helper()
		var hits []string
		for _, l := range strings.Split(must(t, "", "docker", "logs", sinkName), "\n") {
			if strings.HasPrefix(l, "connection ") {
				hits = append(hits, l)
			}
		}
		return strings.Join(hits, ", ")
	}
	waitFor := func(what string, ok func() bool) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Minute)
		for !ok() {
			if time.Now().After(deadline) {
				t.Fatalf("%s never came up", what)
			}
			time.Sleep(2 * time.Second)
		}
	}
	// dueCron schedules the fixture's cron hook a second in the past, with
	// a fresh argument: WordPress drops a single event that repeats one
	// from the last ten minutes.
	dueCron := func() {
		t.Helper()
		wp("eval", `wp_schedule_single_event( time() - 1, 'roadie_fixture_cron', array( uniqid() ) );`)
	}
	waitFor("WordPress files", func() bool {
		_, err := os.Stat(filepath.Join(html, "wp-includes", "version.php"))
		return err == nil
	})
	must(t, "", "docker", "exec", wpName, "chmod", "-R", "a+rwX", "/var/www/html")
	wp("config", "create", "--dbname=wp", "--dbuser=wp", "--dbpass="+dbPass, "--dbhost="+dbName, "--skip-check")
	// The mysql client in the WP-CLI image refuses MySQL 8.4's self-signed
	// TLS, so readiness is asked inside the database container and
	// queries go through $wpdb. Its TCP port opens only once the
	// entrypoint's temporary server is gone.
	waitFor("the database", func() bool {
		_, err := run(t, "", "docker", "exec", dbName, "mysql", "-uwp", "-p"+dbPass, "-h127.0.0.1", "-e", "SELECT 1", "wp")
		return err == nil
	})
	waitFor("the sink", func() bool { return strings.Contains(must(t, "", "docker", "logs", sinkName), "ready") })

	wp("core", "install", "--url=http://localhost", "--title=Roadie test", "--admin_user=admin",
		"--admin_password="+dbPass, "--admin_email=admin@example.invalid", "--skip-email")
	install := func(slug, version string) {
		t.Helper()
		zip := slug + "." + version + ".zip"
		b, err := os.ReadFile(filepath.Join(zips, zip)) // #nosec G304 -- the tester's own folder
		if err != nil {
			t.Fatalf("ROADIE_PLUGIN_ZIPS: %v", err)
		}
		if sum := sha256.Sum256(b); hex.EncodeToString(sum[:]) != ksZipSHA256[zip] {
			t.Fatalf("%s: sha256 %x, want %s", zip, sum, ksZipSHA256[zip])
		}
		wp("plugin", "install", "/zips/"+zip, "--activate")
	}
	install("woocommerce", ksWooCommerce)
	install("wp-mail-smtp", ksWPMailSMTP)
	if err := os.WriteFile(filepath.Join(html, "wp-content", "plugins", "roadie-fixture.php"), []byte(fixturePlugin), 0o644); err != nil { // #nosec G306 -- read by the web server
		t.Fatal(err)
	}
	wp("plugin", "activate", "roadie-fixture")
	smtp, _ := json.Marshal(map[string]any{
		"mail": map[string]any{"mailer": "smtp", "from_email": "shop@example.invalid", "from_name": "Shop", "return_path": false},
		"smtp": map[string]any{"host": sinkName, "port": 2525, "encryption": "none", "autotls": false, "auth": false, "user": "", "pass": ""},
	})
	wp("option", "update", "wp_mail_smtp", string(smtp), "--format=json")
	wp("eval", `$w = new WC_Webhook(); $w->set_name( 'ERP' ); $w->set_topic( 'order.created' ); $w->set_delivery_url( 'http://`+sinkName+`:8080/hook' ); $w->set_secret( 'fixture-secret' ); $w->set_user_id( 1 ); $w->set_status( 'active' ); $w->save(); echo $w->get_id();`)

	// Control: without the kill switch the fixture reaches the sink, by
	// SMTP from a page load and by HTTP from the webhook.
	page()
	wp("eval", `wc_create_order( array( 'status' => 'processing' ) );`)
	wp("action-scheduler", "run")
	if hits := sinkHits(); !strings.Contains(hits, "connection 2525") || !strings.Contains(hits, "connection 8080") {
		t.Fatalf("the fixture never reached the sink (%q), so the test would prove nothing", hits)
	}
	before := sinkHits()

	// The kill switch, as prepare applies it.
	must(t, "", "docker", "exec", wpName, "chmod", "-R", "a+rwX", "/var/www/html")
	ep, err := site.ParseEndpoint(html)
	if err != nil {
		t.Fatal(err)
	}
	s, err := site.Open(ctx, ep, site.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	o := killswitch.Options{
		EchoURL: "http://" + echoName + ":8025", Noindex: true, Version: "test",
		Gateways: []keys.Gateway{},
		Mailers:  []keys.Mailer{{ID: "wp-mail-smtp", Option: "wp_mail_smtp", MailerPath: "mail.mailer", DefaultMailer: "mail", SecretPaths: []string{"smtp.pass", "smtp.user"}}},
		Keys:     func([]core.Pair) []core.KeyHit { return nil },
		Salt: func() string {
			var b [48]byte
			_, _ = rand.Read(b[:])
			return base64.RawStdEncoding.EncodeToString(b[:])[:64]
		},
	}
	for _, st := range append(killswitch.Steps(core.StackWordPress, o), exposure.Steps(o)...) {
		if err := st.Apply(ctx, s); err != nil {
			t.Fatalf("%s: %v", st.ID(), err)
		}
	}
	ops, _ := json.Marshal(ruleOps(t, killswitch.DataRules(core.StackWordPress, o)))
	script := strings.Replace(applyRulesPHP, "RULES_B64", base64.StdEncoding.EncodeToString(ops), 1)
	if out, err := wpIn(script, "eval-file", "-"); err != nil || !strings.Contains(out, "applied") {
		t.Fatalf("data rules: %v\n%s", err, out)
	}
	waitFor("Echo", func() bool {
		out, err := run(t, "", "docker", "exec", wpName, "curl", "-s", "http://"+echoName+":8025/v1/health")
		return err == nil && strings.Contains(out, "seal")
	})

	// Sealed: nothing reaches the sink, Echo has the mail, the webhook is
	// paused. WooCommerce 9.8.5's wc_admin_daily fatals on the answer
	// Echo fakes for woocommerce.com (an Echo follow-up), so it is taken
	// out of the cron run.
	_, _ = wpIn("", "cron", "event", "delete", "wc_admin_daily")
	dueCron()
	page()
	wp("eval", `wc_create_order( array( 'status' => 'processing' ) );`)
	wp("cron", "event", "run", "--due-now")
	if got := sinkHits(); got != before {
		t.Fatalf("the sink saw %q after the kill switch (before: %q)", got, before)
	}
	if got := wp("eval", `global $wpdb; echo implode( ',', $wpdb->get_col( "SELECT status FROM {$wpdb->prefix}wc_webhooks" ) );`); got != "paused" {
		t.Fatalf("webhook status %q, want paused", got)
	}
	for _, subject := range []string{"Roadie fixture web", "Roadie fixture cron"} {
		var msgs string
		for try := 0; try < 10 && !strings.Contains(msgs, subject); try++ {
			if try > 0 {
				time.Sleep(time.Second)
			}
			msgs = must(t, "", "docker", "exec", wpName, "curl", "-s", "http://"+echoName+":8025/v1/messages")
		}
		if !strings.Contains(msgs, subject) {
			t.Errorf("Echo has no %q message: %s", subject, msgs)
		}
	}

	// Echo stopped: mail fails closed and the sink still sees nothing.
	must(t, "", "docker", "stop", echoName)
	dueCron()
	page()
	// wp-cli's own request mails too and logs Echo's refusal before the
	// value, so only the last line counts.
	out := strings.Split(wp("option", "get", "roadie_fixture_last"), "\n")
	if got := out[len(out)-1]; got != "failed" {
		t.Errorf("wp_mail with Echo stopped: %q, want failed", got)
	}
	wp("cron", "event", "run", "--due-now")
	if got := sinkHits(); got != before {
		t.Fatalf("the sink saw %q with Echo stopped (before: %q)", got, before)
	}
}
