-- Easy Digital Downloads 3.3 tables, from the schema classes in
-- https://github.com/awesomemotive/easy-digital-downloads/tree/main/includes/database/schemas
-- (only the tables the profile names). capture.sh replaces this file with a
-- dump of an install.
CREATE TABLE `wp_edd_customers` (
  `id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  `user_id` bigint(20) unsigned NOT NULL DEFAULT 0,
  `email` varchar(100) NOT NULL DEFAULT '',
  `name` varchar(255) NOT NULL DEFAULT '',
  `status` varchar(20) NOT NULL DEFAULT '',
  `purchase_value` decimal(18,9) NOT NULL DEFAULT 0,
  `purchase_count` bigint(20) unsigned NOT NULL DEFAULT 0,
  `date_created` datetime NOT NULL,
  `date_modified` datetime NOT NULL,
  `uuid` varchar(100) NOT NULL DEFAULT '',
  PRIMARY KEY (`id`)
);
CREATE TABLE `wp_edd_customermeta` (
  `meta_id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  `edd_customer_id` bigint(20) unsigned NOT NULL DEFAULT 0,
  `meta_key` varchar(255) DEFAULT NULL,
  `meta_value` longtext,
  PRIMARY KEY (`meta_id`)
);
CREATE TABLE `wp_edd_customer_addresses` (
  `id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  `customer_id` bigint(20) unsigned NOT NULL DEFAULT 0,
  `is_primary` tinyint(1) NOT NULL DEFAULT 0,
  `type` varchar(20) NOT NULL DEFAULT 'billing',
  `status` varchar(20) NOT NULL DEFAULT 'active',
  `name` mediumtext NOT NULL,
  `address` mediumtext NOT NULL,
  `address2` mediumtext NOT NULL,
  `city` mediumtext NOT NULL,
  `region` mediumtext NOT NULL,
  `postal_code` varchar(32) NOT NULL DEFAULT '',
  `country` mediumtext NOT NULL,
  `date_created` datetime NOT NULL,
  `date_modified` datetime NOT NULL,
  `uuid` varchar(100) NOT NULL DEFAULT '',
  PRIMARY KEY (`id`)
);
CREATE TABLE `wp_edd_customer_email_addresses` (
  `id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  `customer_id` bigint(20) unsigned NOT NULL DEFAULT 0,
  `type` varchar(20) NOT NULL DEFAULT 'secondary',
  `status` varchar(20) NOT NULL DEFAULT 'active',
  `email` varchar(100) NOT NULL DEFAULT '',
  `date_created` datetime NOT NULL,
  `date_modified` datetime NOT NULL,
  `uuid` varchar(100) NOT NULL DEFAULT '',
  PRIMARY KEY (`id`)
);
CREATE TABLE `wp_edd_orders` (
  `id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  `parent` bigint(20) unsigned NOT NULL DEFAULT 0,
  `order_number` varchar(255) NOT NULL DEFAULT '',
  `status` varchar(20) NOT NULL DEFAULT 'pending',
  `type` varchar(20) NOT NULL DEFAULT 'sale',
  `user_id` bigint(20) unsigned NOT NULL DEFAULT 0,
  `customer_id` bigint(20) unsigned NOT NULL DEFAULT 0,
  `email` varchar(100) NOT NULL DEFAULT '',
  `ip` varchar(60) NOT NULL DEFAULT '',
  `gateway` varchar(100) NOT NULL DEFAULT 'manual',
  `mode` varchar(20) NOT NULL DEFAULT '',
  `currency` varchar(20) NOT NULL DEFAULT '',
  `payment_key` varchar(64) NOT NULL DEFAULT '',
  `tax_rate_id` bigint(20) DEFAULT NULL,
  `subtotal` decimal(18,9) NOT NULL DEFAULT 0,
  `discount` decimal(18,9) NOT NULL DEFAULT 0,
  `tax` decimal(18,9) NOT NULL DEFAULT 0,
  `total` decimal(18,9) NOT NULL DEFAULT 0,
  `rate` decimal(10,5) NOT NULL DEFAULT 1,
  `date_created` datetime NOT NULL,
  `date_modified` datetime NOT NULL,
  `date_completed` datetime DEFAULT NULL,
  `date_refundable` datetime DEFAULT NULL,
  `date_actions_run` datetime DEFAULT NULL,
  `uuid` varchar(100) NOT NULL DEFAULT '',
  PRIMARY KEY (`id`)
);
CREATE TABLE `wp_edd_order_addresses` (
  `id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  `order_id` bigint(20) unsigned NOT NULL DEFAULT 0,
  `type` varchar(20) NOT NULL DEFAULT 'billing',
  `name` mediumtext NOT NULL,
  `address` mediumtext NOT NULL,
  `address2` mediumtext NOT NULL,
  `city` mediumtext NOT NULL,
  `region` mediumtext NOT NULL,
  `postal_code` varchar(32) NOT NULL DEFAULT '',
  `country` mediumtext NOT NULL,
  `date_created` datetime NOT NULL,
  `date_modified` datetime NOT NULL,
  `uuid` varchar(100) NOT NULL DEFAULT '',
  PRIMARY KEY (`id`)
);
CREATE TABLE `wp_edd_order_transactions` (
  `id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  `object_id` bigint(20) unsigned NOT NULL DEFAULT 0,
  `object_type` varchar(20) NOT NULL DEFAULT '',
  `transaction_id` varchar(256) NOT NULL DEFAULT '',
  `gateway` varchar(20) NOT NULL DEFAULT '',
  `status` varchar(20) NOT NULL DEFAULT '',
  `total` decimal(18,9) NOT NULL DEFAULT 0,
  `rate` decimal(10,5) NOT NULL DEFAULT 1,
  `date_created` datetime NOT NULL,
  `date_modified` datetime NOT NULL,
  `uuid` varchar(100) NOT NULL DEFAULT '',
  PRIMARY KEY (`id`)
);
CREATE TABLE `wp_edd_logs` (
  `id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  `object_id` bigint(20) unsigned NOT NULL DEFAULT 0,
  `object_type` varchar(20) DEFAULT NULL,
  `user_id` bigint(20) unsigned NOT NULL DEFAULT 0,
  `type` varchar(20) DEFAULT NULL,
  `title` varchar(200) DEFAULT NULL,
  `content` longtext DEFAULT NULL,
  `date_created` datetime NOT NULL,
  `date_modified` datetime NOT NULL,
  `uuid` varchar(100) NOT NULL DEFAULT '',
  PRIMARY KEY (`id`)
);
CREATE TABLE `wp_edd_logs_file_downloads` (
  `id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  `product_id` bigint(20) unsigned NOT NULL DEFAULT 0,
  `file_id` bigint(20) unsigned NOT NULL DEFAULT 0,
  `order_id` bigint(20) unsigned NOT NULL DEFAULT 0,
  `price_id` bigint(20) unsigned NOT NULL DEFAULT 0,
  `customer_id` bigint(20) unsigned NOT NULL DEFAULT 0,
  `ip` varchar(60) NOT NULL DEFAULT '',
  `user_agent` varchar(200) NOT NULL DEFAULT '',
  `date_created` datetime NOT NULL,
  `date_modified` datetime NOT NULL,
  `uuid` varchar(100) NOT NULL DEFAULT '',
  PRIMARY KEY (`id`)
);
CREATE TABLE `wp_edd_logs_api_requests` (
  `id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  `user_id` bigint(20) unsigned NOT NULL DEFAULT 0,
  `api_key` varchar(32) NOT NULL DEFAULT 'public',
  `token` varchar(32) NOT NULL DEFAULT '',
  `version` varchar(32) NOT NULL DEFAULT '',
  `request` longtext NOT NULL,
  `error` longtext NOT NULL,
  `ip` varchar(60) NOT NULL DEFAULT '',
  `time` varchar(60) NOT NULL DEFAULT '',
  `date_created` datetime NOT NULL,
  `date_modified` datetime NOT NULL,
  `uuid` varchar(100) NOT NULL DEFAULT '',
  PRIMARY KEY (`id`)
);
CREATE TABLE `wp_edd_notes` (
  `id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  `object_id` bigint(20) unsigned NOT NULL DEFAULT 0,
  `object_type` varchar(20) NOT NULL DEFAULT '',
  `user_id` bigint(20) unsigned NOT NULL DEFAULT 0,
  `content` longtext NOT NULL,
  `date_created` datetime NOT NULL,
  `date_modified` datetime NOT NULL,
  `uuid` varchar(100) NOT NULL DEFAULT '',
  PRIMARY KEY (`id`)
);
