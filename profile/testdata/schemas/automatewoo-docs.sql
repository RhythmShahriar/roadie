-- AutomateWoo (paid, no install to capture). Table names from
-- https://woocommerce.com/document/automatewoo/regenerate-automatewoo-database-tables/
-- which lists the nine tables. The columns are the ones the profile's rules
-- need plus the usual id, meta and date columns; confirm them against a
-- licensed install (capture.sh with AUTOMATEWOO_ZIP set) before relying on
-- the others.
CREATE TABLE `wp_automatewoo_guests` (
  `id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  `email` varchar(255) NOT NULL DEFAULT '',
  `tracking_key` varchar(32) NOT NULL DEFAULT '',
  `created` datetime NULL,
  `last_active` datetime NULL,
  `language` varchar(10) NOT NULL DEFAULT '',
  `most_recent_order` bigint(20) unsigned NOT NULL DEFAULT 0,
  `version` varchar(32) NOT NULL DEFAULT '',
  PRIMARY KEY (`id`)
);
CREATE TABLE `wp_automatewoo_guest_meta` (
  `meta_id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  `guest_id` bigint(20) unsigned NOT NULL,
  `meta_key` varchar(255) NULL,
  `meta_value` longtext NULL,
  PRIMARY KEY (`meta_id`)
);
CREATE TABLE `wp_automatewoo_customers` (
  `id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  `user_id` bigint(20) unsigned NOT NULL DEFAULT 0,
  `guest_id` bigint(20) unsigned NOT NULL DEFAULT 0,
  `id_key` varchar(32) NOT NULL DEFAULT '',
  `state` varchar(100) NOT NULL DEFAULT '',
  `last_purchased` datetime NULL,
  `created` datetime NULL,
  `unsubscribed` int(1) NOT NULL DEFAULT 0,
  `unsubscribed_date` datetime NULL,
  PRIMARY KEY (`id`)
);
CREATE TABLE `wp_automatewoo_customer_meta` (
  `meta_id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  `customer_id` bigint(20) unsigned NOT NULL,
  `meta_key` varchar(255) NULL,
  `meta_value` longtext NULL,
  PRIMARY KEY (`meta_id`)
);
CREATE TABLE `wp_automatewoo_queue` (
  `id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  `workflow_id` bigint(20) unsigned NOT NULL,
  `date` datetime NULL,
  `created` datetime NULL,
  `failed` int(1) NOT NULL DEFAULT 0,
  `failure_code` int(3) NOT NULL DEFAULT 0,
  PRIMARY KEY (`id`)
);
CREATE TABLE `wp_automatewoo_queue_meta` (
  `meta_id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  `event_id` bigint(20) unsigned NOT NULL,
  `meta_key` varchar(255) NULL,
  `meta_value` longtext NULL,
  PRIMARY KEY (`meta_id`)
);
CREATE TABLE `wp_automatewoo_abandoned_carts` (
  `id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  `status` varchar(100) NOT NULL DEFAULT '',
  `user_id` bigint(20) unsigned NOT NULL DEFAULT 0,
  `guest_id` bigint(20) unsigned NOT NULL DEFAULT 0,
  `last_modified` datetime NULL,
  `created` datetime NULL,
  `items` longtext NULL,
  `coupons` longtext NULL,
  `fees` longtext NULL,
  `shipping_tax_total` double DEFAULT 0 NOT NULL,
  `shipping_total` double DEFAULT 0 NOT NULL,
  `total` double DEFAULT 0 NOT NULL,
  `token` varchar(32) NOT NULL DEFAULT '',
  `currency` varchar(8) NOT NULL DEFAULT '',
  PRIMARY KEY (`id`)
);
CREATE TABLE `wp_automatewoo_logs` (
  `id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  `workflow_id` bigint(20) unsigned NOT NULL,
  `date` datetime NULL,
  `has_errors` int(1) NOT NULL DEFAULT 0,
  `has_blocked_emails` int(1) NOT NULL DEFAULT 0,
  `tracking_enabled` int(1) NOT NULL DEFAULT 0,
  `conversion_tracking_enabled` int(1) NOT NULL DEFAULT 0,
  PRIMARY KEY (`id`)
);
CREATE TABLE `wp_automatewoo_log_meta` (
  `meta_id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  `log_id` bigint(20) unsigned NOT NULL,
  `meta_key` varchar(255) NULL,
  `meta_value` longtext NULL,
  PRIMARY KEY (`meta_id`)
);
