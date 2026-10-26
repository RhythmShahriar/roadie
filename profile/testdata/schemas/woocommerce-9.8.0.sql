-- WooCommerce 9.8.0 tables, prefix wp_ (the lookup tables Roadie has no rule
-- for are left out). Read from
-- https://raw.githubusercontent.com/woocommerce/woocommerce/9.8.0/plugins/woocommerce/includes/class-wc-install.php
-- (get_schema) and
-- https://raw.githubusercontent.com/woocommerce/woocommerce/9.8.0/plugins/woocommerce/src/Internal/DataStores/Orders/OrdersTableDataStore.php
-- (get_database_schema). profile/testdata/capture/capture.sh replaces this file
-- with a mysqldump --no-data of a real install when it runs.
CREATE TABLE `wp_woocommerce_sessions` (
  `session_id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  `session_key` char(32) NOT NULL,
  `session_value` longtext NOT NULL,
  `session_expiry` bigint(20) unsigned NOT NULL,
  PRIMARY KEY (`session_id`)
);
CREATE TABLE `wp_woocommerce_api_keys` (
  `key_id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  `user_id` bigint(20) unsigned NOT NULL,
  `description` varchar(200) NULL,
  `permissions` varchar(10) NOT NULL,
  `consumer_key` char(64) NOT NULL,
  `consumer_secret` char(43) NOT NULL,
  `nonces` longtext NULL,
  `truncated_key` char(7) NOT NULL,
  `last_access` datetime NULL DEFAULT NULL,
  PRIMARY KEY (`key_id`)
);
CREATE TABLE `wp_woocommerce_attribute_taxonomies` (
  `attribute_id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  `attribute_name` varchar(200) NOT NULL,
  `attribute_label` varchar(200) NULL,
  `attribute_type` varchar(20) NOT NULL,
  `attribute_orderby` varchar(20) NOT NULL,
  `attribute_public` int(1) NOT NULL DEFAULT 1,
  PRIMARY KEY (`attribute_id`)
);
CREATE TABLE `wp_woocommerce_downloadable_product_permissions` (
  `permission_id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  `download_id` varchar(36) NOT NULL,
  `product_id` bigint(20) unsigned NOT NULL,
  `order_id` bigint(20) unsigned NOT NULL DEFAULT 0,
  `order_key` varchar(200) NOT NULL,
  `user_email` varchar(200) NOT NULL,
  `user_id` bigint(20) unsigned NULL,
  `downloads_remaining` varchar(9) NULL,
  `access_granted` datetime NOT NULL DEFAULT '0000-00-00 00:00:00',
  `access_expires` datetime NULL DEFAULT NULL,
  `download_count` bigint(20) unsigned NOT NULL DEFAULT 0,
  PRIMARY KEY (`permission_id`)
);
CREATE TABLE `wp_woocommerce_order_items` (
  `order_item_id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  `order_item_name` text NOT NULL,
  `order_item_type` varchar(200) NOT NULL DEFAULT '',
  `order_id` bigint(20) unsigned NOT NULL,
  PRIMARY KEY (`order_item_id`)
);
CREATE TABLE `wp_woocommerce_order_itemmeta` (
  `meta_id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  `order_item_id` bigint(20) unsigned NOT NULL,
  `meta_key` varchar(255) DEFAULT NULL,
  `meta_value` longtext NULL,
  PRIMARY KEY (`meta_id`)
);
CREATE TABLE `wp_woocommerce_payment_tokens` (
  `token_id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  `gateway_id` varchar(200) NOT NULL,
  `token` text NOT NULL,
  `user_id` bigint(20) unsigned NOT NULL DEFAULT '0',
  `type` varchar(200) NOT NULL,
  `is_default` tinyint(1) NOT NULL DEFAULT '0',
  PRIMARY KEY (`token_id`)
);
CREATE TABLE `wp_woocommerce_payment_tokenmeta` (
  `meta_id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  `payment_token_id` bigint(20) unsigned NOT NULL,
  `meta_key` varchar(255) NULL,
  `meta_value` longtext NULL,
  PRIMARY KEY (`meta_id`)
);
CREATE TABLE `wp_woocommerce_log` (
  `log_id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  `timestamp` datetime NOT NULL,
  `level` smallint(4) NOT NULL,
  `source` varchar(200) NOT NULL,
  `message` longtext NOT NULL,
  `context` longtext NULL,
  PRIMARY KEY (`log_id`)
);
CREATE TABLE `wp_wc_webhooks` (
  `webhook_id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  `status` varchar(200) NOT NULL,
  `name` text NOT NULL,
  `user_id` bigint(20) unsigned NOT NULL,
  `delivery_url` text NOT NULL,
  `secret` text NOT NULL,
  `topic` varchar(200) NOT NULL,
  `date_created` datetime NOT NULL DEFAULT '0000-00-00 00:00:00',
  `date_created_gmt` datetime NOT NULL DEFAULT '0000-00-00 00:00:00',
  `date_modified` datetime NOT NULL DEFAULT '0000-00-00 00:00:00',
  `date_modified_gmt` datetime NOT NULL DEFAULT '0000-00-00 00:00:00',
  `api_version` smallint(4) NOT NULL,
  `failure_count` smallint(10) NOT NULL DEFAULT '0',
  `pending_delivery` tinyint(1) NOT NULL DEFAULT '0',
  PRIMARY KEY (`webhook_id`)
);
CREATE TABLE `wp_wc_download_log` (
  `download_log_id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  `timestamp` datetime NOT NULL,
  `permission_id` bigint(20) unsigned NOT NULL,
  `user_id` bigint(20) unsigned NULL,
  `user_ip_address` varchar(100) NULL DEFAULT '',
  PRIMARY KEY (`download_log_id`)
);
CREATE TABLE `wp_wc_customer_lookup` (
  `customer_id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  `user_id` bigint(20) unsigned DEFAULT NULL,
  `username` varchar(60) DEFAULT '' NOT NULL,
  `first_name` varchar(255) NOT NULL,
  `last_name` varchar(255) NOT NULL,
  `email` varchar(100) NULL DEFAULT NULL,
  `date_last_active` timestamp NULL DEFAULT NULL,
  `date_registered` timestamp NULL DEFAULT NULL,
  `country` char(2) DEFAULT '' NOT NULL,
  `postcode` varchar(20) DEFAULT '' NOT NULL,
  `city` varchar(100) DEFAULT '' NOT NULL,
  `state` varchar(100) DEFAULT '' NOT NULL,
  PRIMARY KEY (`customer_id`)
);
CREATE TABLE `wp_wc_admin_notes` (
  `note_id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  `name` varchar(255) NOT NULL,
  `type` varchar(20) NOT NULL,
  `locale` varchar(20) NOT NULL,
  `title` longtext NOT NULL,
  `content` longtext NOT NULL,
  `content_data` longtext NULL,
  `status` varchar(200) NOT NULL,
  `source` varchar(200) NOT NULL,
  `date_created` datetime NOT NULL DEFAULT '0000-00-00 00:00:00',
  `date_reminder` datetime NULL DEFAULT NULL,
  `is_snoozable` tinyint(1) DEFAULT 0 NOT NULL,
  `layout` varchar(20) DEFAULT '' NOT NULL,
  `image` varchar(200) NULL DEFAULT NULL,
  `is_deleted` tinyint(1) DEFAULT 0 NOT NULL,
  `is_read` tinyint(1) DEFAULT 0 NOT NULL,
  `icon` varchar(200) NOT NULL DEFAULT 'info',
  PRIMARY KEY (`note_id`)
);
CREATE TABLE `wp_wc_admin_note_actions` (
  `action_id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  `note_id` bigint(20) unsigned NOT NULL,
  `name` varchar(255) NOT NULL,
  `label` varchar(255) NOT NULL,
  `query` longtext NOT NULL,
  `status` varchar(255) NOT NULL,
  `actioned_text` varchar(255) NOT NULL,
  `nonce_action` varchar(255) NULL DEFAULT NULL,
  `nonce_name` varchar(255) NULL DEFAULT NULL,
  PRIMARY KEY (`action_id`)
);
CREATE TABLE `wp_wc_order_stats` (
  `order_id` bigint(20) unsigned NOT NULL,
  `parent_id` bigint(20) unsigned NOT NULL DEFAULT 0,
  `date_created` datetime DEFAULT '0000-00-00 00:00:00' NOT NULL,
  `date_created_gmt` datetime DEFAULT '0000-00-00 00:00:00' NOT NULL,
  `date_paid` datetime DEFAULT '0000-00-00 00:00:00',
  `date_completed` datetime DEFAULT '0000-00-00 00:00:00',
  `num_items_sold` int(11) DEFAULT 0 NOT NULL,
  `total_sales` double DEFAULT 0 NOT NULL,
  `tax_total` double DEFAULT 0 NOT NULL,
  `shipping_total` double DEFAULT 0 NOT NULL,
  `net_total` double DEFAULT 0 NOT NULL,
  `returning_customer` tinyint(1) DEFAULT NULL,
  `status` varchar(200) NOT NULL,
  `customer_id` bigint(20) unsigned NOT NULL,
  PRIMARY KEY (`order_id`)
);
CREATE TABLE `wp_wc_orders` (
  `id` bigint(20) unsigned NOT NULL,
  `status` varchar(20) NULL,
  `currency` varchar(10) NULL,
  `type` varchar(20) NULL,
  `tax_amount` decimal(26,8) NULL,
  `total_amount` decimal(26,8) NULL,
  `customer_id` bigint(20) unsigned NULL,
  `billing_email` varchar(320) NULL,
  `date_created_gmt` datetime NULL,
  `date_updated_gmt` datetime NULL,
  `parent_order_id` bigint(20) unsigned NULL,
  `payment_method` varchar(100) NULL,
  `payment_method_title` text NULL,
  `transaction_id` varchar(100) NULL,
  `ip_address` varchar(100) NULL,
  `user_agent` text NULL,
  `customer_note` text NULL,
  PRIMARY KEY (`id`)
);
CREATE TABLE `wp_wc_order_addresses` (
  `id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  `order_id` bigint(20) unsigned NOT NULL,
  `address_type` varchar(20) NULL,
  `first_name` text NULL,
  `last_name` text NULL,
  `company` text NULL,
  `address_1` text NULL,
  `address_2` text NULL,
  `city` text NULL,
  `state` text NULL,
  `postcode` text NULL,
  `country` text NULL,
  `email` varchar(320) NULL,
  `phone` varchar(100) NULL,
  PRIMARY KEY (`id`)
);
CREATE TABLE `wp_wc_order_operational_data` (
  `id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  `order_id` bigint(20) unsigned NULL,
  `created_via` varchar(100) NULL,
  `woocommerce_version` varchar(20) NULL,
  `prices_include_tax` tinyint(1) NULL,
  `coupon_usages_are_counted` tinyint(1) NULL,
  `download_permission_granted` tinyint(1) NULL,
  `cart_hash` varchar(100) NULL,
  `new_order_email_sent` tinyint(1) NULL,
  `order_key` varchar(100) NULL,
  `order_stock_reduced` tinyint(1) NULL,
  `date_paid_gmt` datetime NULL,
  `date_completed_gmt` datetime NULL,
  `shipping_tax_amount` decimal(26,8) NULL,
  `shipping_total_amount` decimal(26,8) NULL,
  `discount_tax_amount` decimal(26,8) NULL,
  `discount_total_amount` decimal(26,8) NULL,
  `recorded_sales` tinyint(1) NULL,
  PRIMARY KEY (`id`)
);
CREATE TABLE `wp_wc_orders_meta` (
  `id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  `order_id` bigint(20) unsigned NULL,
  `meta_key` varchar(255),
  `meta_value` text NULL,
  PRIMARY KEY (`id`)
);
