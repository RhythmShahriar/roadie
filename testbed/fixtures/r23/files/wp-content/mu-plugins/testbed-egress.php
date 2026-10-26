<?php
/**
 * Plugin Name: Roadie testbed egress drill
 * Description: Scenario r23. "wp testbed-egress" tries each way out and
 * prints one JSON object: for each path, whether PHP saw it succeed. Every
 * attempt carries the marker testbed-egress:<path> in its first bytes, so
 * the sinkhole log tells which path reached the "internet".
 *
 * @package RoadieTestbed
 */

// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

defined( 'ABSPATH' ) || exit;

if ( ! defined( 'WP_CLI' ) || ! WP_CLI ) {
	return;
}

/**
 * Runs every egress path and prints the results as JSON.
 */
function testbed_egress_drill() {
	$out = array();

	$out['wp_mail'] = (bool) wp_mail(
		'egress-drill@example.com',
		'testbed-egress:wp_mail',
		'testbed-egress:wp_mail'
	);

	$res                   = wp_remote_post(
		'https://egress-http.example.com/hook',
		array(
			'timeout' => 5,
			'headers' => array( 'X-Testbed-Egress' => 'testbed-egress:wp_remote_post' ),
			'body'    => 'testbed-egress:wp_remote_post',
		)
	);
	$out['wp_remote_post'] = ! is_wp_error( $res );

	$out['curl'] = false;
	if ( function_exists( 'curl_init' ) ) {
		// phpcs:disable WordPress.WP.AlternativeFunctions -- raw cURL is the path under test.
		$ch = curl_init( 'http://egress-curl.example.com/testbed-egress:curl' );
		curl_setopt( $ch, CURLOPT_RETURNTRANSFER, true );
		curl_setopt( $ch, CURLOPT_TIMEOUT, 5 );
		curl_setopt( $ch, CURLOPT_HTTPHEADER, array( 'X-Testbed-Egress: testbed-egress:curl' ) );
		$out['curl'] = false !== curl_exec( $ch ) && curl_errno( $ch ) === 0;
		curl_close( $ch );
		// phpcs:enable
	}

	// phpcs:ignore WordPress.PHP.NoSilencedErrors -- a failed mail() is a result here.
	$out['mail'] = (bool) @mail( 'egress-drill@example.com', 'testbed-egress:mail', 'testbed-egress:mail' );

	$out['smtp_socket'] = false;
	$errno              = 0;
	$errstr             = '';
	// phpcs:ignore WordPress.PHP.NoSilencedErrors
	$sock = @stream_socket_client( 'tcp://egress-smtp.example.com:587', $errno, $errstr, 5 );
	if ( $sock ) {
		fwrite( $sock, "EHLO testbed-egress:smtp_socket\r\n" );
		$out['smtp_socket'] = false !== fgets( $sock );
		fclose( $sock );
	}

	$out['fsockopen'] = false;
	// phpcs:ignore WordPress.PHP.NoSilencedErrors
	$fp = @fsockopen( 'egress-fsock.example.com', 443, $errno, $errstr, 5 );
	if ( $fp ) {
		fwrite( $fp, "testbed-egress:fsockopen\r\n" );
		$out['fsockopen'] = true;
		fclose( $fp );
	}

	WP_CLI::line( wp_json_encode( $out ) );
}

WP_CLI::add_command( 'testbed-egress', 'testbed_egress_drill' );
