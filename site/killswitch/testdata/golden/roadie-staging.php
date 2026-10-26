<?php
/*
 * Plugin Name: Roadie staging guard
 * Description: Keeps this pseudonymised staging copy from running production's queues, renewals, mail and search indexing.
 * Roadie (rhythm-roadie 1.0.0) wrote this file. Delete it to turn the guard off.
 */

if ( ! defined( 'ABSPATH' ) ) {
	exit;
}

// A copy that calls itself production is left alone: the guard is for
// staging only; WP_ENVIRONMENT_TYPE says which one this is.
if ( function_exists( 'wp_get_environment_type' ) && 'production' === wp_get_environment_type() ) {
	return;
}

// Action Scheduler: no async runner, no queue runner on WP-Cron, no
// batches. Pending actions stay pending; nothing from production runs.
add_filter( 'action_scheduler_allow_async_request_runner', '__return_false', 999 );
add_filter( 'action_scheduler_queue_runner_concurrent_batches', function () {
	return 0;
}, 999 );
add_action( 'init', function () {
	if ( class_exists( 'ActionScheduler_QueueRunner' ) ) {
		remove_action( 'action_scheduler_run_queue', array( ActionScheduler_QueueRunner::instance(), 'run' ) );
	}
}, 20 );

// WooCommerce Subscriptions: a duplicate site never charges renewals.
add_filter( 'woocommerce_subscriptions_is_duplicate_site', '__return_true', 999 );

// Jetpack: offline mode, so this copy never talks to WordPress.com as
// production.
add_filter( 'jetpack_offline_mode', '__return_true', 999 );

// Mail: without the Echo connector, wp_mail() sends nothing at all.
if ( ! file_exists( __DIR__ . '/rhythm-echo.php' ) ) {
	add_filter( 'pre_wp_mail', function () {
		return false;
	}, 999 );
}

// Search engines: noindex in every page and header, no sitemaps and a
// robots.txt that disallows everything. blog_public in the database is
// left as it is, so a push of this database to production can't carry
// noindex there.
add_filter( 'wp_robots', function ( $robots ) {
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
	return 'User-agent: *
Disallow: /

User-agent: GPTBot
Disallow: /

User-agent: ClaudeBot
Disallow: /

User-agent: CCBot
Disallow: /

User-agent: Google-Extended
Disallow: /

User-agent: Bytespider
Disallow: /

User-agent: PerplexityBot
Disallow: /

User-agent: Amazonbot
Disallow: /

User-agent: Applebot-Extended
Disallow: /
';
}, 999 );

// A notice in the admin bar, so nobody mistakes this copy for the shop.
add_action( 'admin_bar_menu', function ( $bar ) {
	$bar->add_node(
		array(
			'id'    => 'roadie-staging',
			'title' => 'Pseudonymised staging copy (Roadie)',
		)
	);
}, 100 );
