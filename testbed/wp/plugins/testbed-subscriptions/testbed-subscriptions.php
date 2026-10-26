<?php
// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

/**
 * Plugin Name: Testbed Subscriptions core loader
 * Description: Runs the open-source WooCommerce Subscriptions core library (woocommerce-subscriptions-core) as a plugin, as the paid Subscriptions plugin does, so the testbed has real subscription tables, renewals and staging mode.
 * Version: 1.0.0
 * Requires Plugins: woocommerce
 */

defined('ABSPATH') || exit;

add_action('plugins_loaded', static function () {
    $core = WP_PLUGIN_DIR . '/woocommerce-subscriptions-core/includes/class-wc-subscriptions-core-plugin.php';
    if (!class_exists('WooCommerce') || !is_readable($core) || class_exists('WC_Subscriptions_Core_Plugin')) {
        return;
    }
    require_once $core;
    new WC_Subscriptions_Core_Plugin();
}, 5);
