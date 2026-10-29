// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package killswitch

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// MuPluginPath is where the Roadie mu-plugin goes, relative to the site
// root.
const MuPluginPath = "wp-content/mu-plugins/roadie-staging.php"

// MuPluginMarker is in the header of every mu-plugin Roadie writes; a
// file at MuPluginPath without it is someone else's and is never replaced.
const MuPluginMarker = "Plugin Name: Roadie staging guard"

// muSumLabel starts the header line holding the mu-plugin's own sha256:
// the sum of the file with that hex written as 64 zeros. A hand edit
// (a hook commented out or put in dead code) no longer matches it.
const muSumLabel = " * Roadie-Sum: "

// muSumZero stands in for the sum while it is computed.
var muSumZero = strings.Repeat("0", sha256.Size*2)

// MuPluginIntact reports whether b is a Roadie mu-plugin exactly as
// Roadie wrote it: the marker is there and the file's bytes match the sum
// in its header. A hook name in the file says nothing on its own: a
// commented-out add_filter or one in dead code still holds the name.
func MuPluginIntact(b []byte) bool {
	if !bytes.Contains(b, []byte(MuPluginMarker)) {
		return false
	}
	i := bytes.Index(b, []byte(muSumLabel))
	if i < 0 || bytes.Contains(b[i+len(muSumLabel):], []byte(muSumLabel)) {
		return false
	}
	at := i + len(muSumLabel)
	if len(b) < at+len(muSumZero) {
		return false
	}
	want, err := hex.DecodeString(string(b[at : at+len(muSumZero)]))
	if err != nil {
		return false
	}
	zeroed := bytes.Clone(b)
	copy(zeroed[at:], muSumZero)
	got := sha256.Sum256(zeroed)
	return bytes.Equal(got[:], want)
}

// sealMuPlugin writes the sum of src into its Roadie-Sum line.
func sealMuPlugin(src string) []byte {
	b := []byte(src)
	sum := sha256.Sum256(b)
	at := bytes.Index(b, []byte(muSumLabel)) + len(muSumLabel)
	copy(b[at:], hex.EncodeToString(sum[:]))
	return b
}

// Crawlers are the bots robots.txt names on their own (PRD 13.2), so a
// bot that skips the "*" group is still told to stay out.
var Crawlers = []string{"GPTBot", "ClaudeBot", "CCBot", "Google-Extended", "Bytespider", "PerplexityBot", "Amazonbot", "Applebot-Extended"}

// RobotsTxt is the robots.txt a staging copy serves: everything
// disallowed for every agent, then the same for each named crawler.
func RobotsTxt() string {
	var b strings.Builder
	b.WriteString("User-agent: *\nDisallow: /\n")
	for _, c := range Crawlers {
		b.WriteString("\nUser-agent: " + c + "\nDisallow: /\n")
	}
	return b.String()
}

// The hooks the mu-plugin adds, which Check looks for. Their names are
// those of Action Scheduler 3.x, WooCommerce Subscriptions core, Jetpack
// and WordPress 5.7+.
const (
	hookAsyncRunner   = "action_scheduler_allow_async_request_runner"
	hookRunQueue      = "action_scheduler_run_queue"
	hookBatches       = "action_scheduler_queue_runner_concurrent_batches"
	hookDuplicateSite = "woocommerce_subscriptions_is_duplicate_site"
	hookJetpack       = "jetpack_offline_mode"
	hookPreMail       = "pre_wp_mail"
	hookRobots        = "wp_robots"
)

// phpSingle quotes s as a single-quoted PHP string.
func phpSingle(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}

// version is the rhythm-roadie version for the header.
func (o Options) version() string {
	if o.Version == "" {
		return "dev"
	}
	return o.Version
}

// muParts names what the mu-plugin does for o, for the plan. A side
// effect the config keeps gets no hook.
func (o Options) muParts() []string {
	var out []string
	if !o.kept("action_scheduler") {
		out = append(out, "Action Scheduler paused")
	}
	if !o.kept("subscriptions") {
		out = append(out, "Subscriptions staging mode")
	}
	out = append(out, "Jetpack offline")
	if !o.kept("mail") {
		out = append(out, "wp_mail blocked without Echo")
	}
	if o.Noindex {
		out = append(out, "noindex")
	}
	return out
}

func muPlugin(o Options) []byte {
	var b strings.Builder
	b.WriteString(`<?php
/*
 * ` + MuPluginMarker + `
 * Description: Keeps this pseudonymised staging copy from running production's queues, renewals, mail and search indexing.
 * Roadie (rhythm-roadie ` + strings.NewReplacer("*/", "", "\n", " ").Replace(o.version()) + `) wrote this file. Delete it to turn the guard off.
` + muSumLabel + muSumZero + `
 * rhythm-roadie verify reports this file as changed when its bytes no longer match the sum above.
 */

if ( ! defined( 'ABSPATH' ) ) {
	exit;
}

// A copy that calls itself production is left alone: the guard is for
// staging only; WP_ENVIRONMENT_TYPE says which one this is.
if ( function_exists( 'wp_get_environment_type' ) && 'production' === wp_get_environment_type() ) {
	return;
}
`)
	if !o.kept("action_scheduler") {
		b.WriteString(`
// Action Scheduler: no async runner, no queue runner on WP-Cron, no
// batches. Pending actions stay pending; nothing from production runs.
add_filter( '` + hookAsyncRunner + `', '__return_false', 999 );
add_filter( '` + hookBatches + `', function () {
	return 0;
}, 999 );
add_action( 'init', function () {
	if ( class_exists( 'ActionScheduler_QueueRunner' ) ) {
		remove_action( '` + hookRunQueue + `', array( ActionScheduler_QueueRunner::instance(), 'run' ) );
	}
}, 20 );
`)
	}
	if !o.kept("subscriptions") {
		b.WriteString(`
// WooCommerce Subscriptions: a duplicate site never charges renewals.
add_filter( '` + hookDuplicateSite + `', '__return_true', 999 );
`)
	}
	b.WriteString(`
// Jetpack: offline mode, so this copy never talks to WordPress.com as
// production.
add_filter( '` + hookJetpack + `', '__return_true', 999 );
`)
	if !o.kept("mail") {
		b.WriteString(`
// Mail: without the Echo connector, wp_mail() sends nothing at all.
if ( ! file_exists( __DIR__ . '/rhythm-echo.php' ) ) {
	add_filter( '` + hookPreMail + `', function () {
		return false;
	}, 999 );
}
`)
	}
	if o.Noindex {
		b.WriteString(`
// Search engines: noindex in every page and header, no sitemaps and a
// robots.txt that disallows everything. blog_public in the database is
// left as it is, so a push of this database to production can't carry
// noindex there.
add_filter( '` + hookRobots + `', function ( $robots ) {
	$robots['noindex']  = true;
	$robots['nofollow'] = true;
	return $robots;
}, 999 );
add_action( 'send_headers', function () {
	header( 'X-Robots-Tag: noindex, nofollow' );
} );
add_filter( 'pre_option_blog_public', function () {
	return '0';
}, 999 );
add_filter( 'wp_sitemaps_enabled', '__return_false', 999 );
add_filter( 'robots_txt', function () {
	return ` + phpSingle(RobotsTxt()) + `;
}, 999 );
`)
	}
	b.WriteString(`
// A notice in the admin bar, so nobody mistakes this copy for the shop.
add_action( 'admin_bar_menu', function ( $bar ) {
	$bar->add_node(
		array(
			'id'    => 'roadie-staging',
			'title' => 'Pseudonymised staging copy (Roadie)',
		)
	);
}, 100 );
`)
	return sealMuPlugin(b.String())
}
