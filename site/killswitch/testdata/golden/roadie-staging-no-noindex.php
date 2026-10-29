<?php
/*
 * Plugin Name: Roadie staging guard
 * Description: Keeps this pseudonymised staging copy from running production's queues, renewals, mail and search indexing.
 * Roadie (rhythm-roadie 1.0.0) wrote this file. Delete it to turn the guard off.
 * Roadie-Sum: 12d2c034449a10ca886798b5927143813ca3195b24f848642a5e7962fbc0d769
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

// A notice in the admin bar, so nobody mistakes this copy for the shop.
add_action( 'admin_bar_menu', function ( $bar ) {
	$bar->add_node(
		array(
			'id'    => 'roadie-staging',
			'title' => 'Pseudonymised staging copy (Roadie)',
		)
	);
}, 100 );
