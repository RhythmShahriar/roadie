<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

/**
 * Plugin Name: Roadie testbed armed side effects
 * Description: Scenario r13. The hooks a real shop runs from cron and
 * Action Scheduler, each one sending: a newsletter mail per customer, a
 * CRM post and a Stripe call. On a staging Roadie prepared they must reach
 * Echo or fail at the blanked credentials, never the sinkhole.
 *
 * @package RoadieTestbed
 */

defined( 'ABSPATH' ) || exit;

add_action(
	'testbed_send_newsletter',
	static function ( $email = '' ) {
		if ( is_array( $email ) ) {
			$email = isset( $email['email'] ) ? $email['email'] : '';
		}
		if ( '' !== $email ) {
			wp_mail( $email, 'Bean & Leaf: the October roast', 'New beans are in.' );
		}
	}
);

add_action(
	'testbed_ping_crm',
	static function () {
		wp_remote_post(
			'https://hooks.zapier.com/hooks/catch/123456/crm/',
			array(
				'timeout' => 5,
				'body'    => array( 'event' => 'cron' ),
			)
		);
	}
);

add_action(
	'testbed_charge_card',
	static function () {
		$settings = get_option( 'woocommerce_stripe_settings', array() );
		$secret   = is_array( $settings ) && isset( $settings['secret_key'] ) ? $settings['secret_key'] : '';
		wp_remote_post(
			'https://api.stripe.com/v1/charges',
			array(
				'timeout' => 5,
				'headers' => array( 'Authorization' => 'Bearer ' . $secret ),
				'body'    => array(
					'amount'   => 1200,
					'currency' => 'gbp',
				),
			)
		);
	}
);
