-- WPForms. The Lite tables (payments, payment meta, tasks meta, logs) from
-- the plugin source, e.g.
-- https://plugins.svn.wordpress.org/wpforms-lite/trunk/src/Db/Payments/Payment.php
-- and the Pro entry tables (paid) as WPForms documents them at
-- https://wpforms.com/docs/complete-guide-to-form-entries/
-- capture.sh replaces the Lite part with a dump of an install.
CREATE TABLE `wp_wpforms_payments` (
  `id` bigint(20) NOT NULL AUTO_INCREMENT,
  `form_id` bigint(20) NOT NULL,
  `status` varchar(10) NOT NULL DEFAULT '',
  `subtotal_amount` decimal(26,8) NOT NULL DEFAULT 0,
  `discount_amount` decimal(26,8) NOT NULL DEFAULT 0,
  `total_amount` decimal(26,8) NOT NULL DEFAULT 0,
  `currency` varchar(3) NOT NULL DEFAULT '',
  `entry_id` bigint(20) NOT NULL DEFAULT 0,
  `gateway` varchar(20) NOT NULL DEFAULT '',
  `type` varchar(12) NOT NULL DEFAULT '',
  `mode` varchar(4) NOT NULL DEFAULT '',
  `transaction_id` varchar(40) NOT NULL DEFAULT '',
  `customer_id` varchar(40) NOT NULL DEFAULT '',
  `subscription_id` varchar(40) NOT NULL DEFAULT '',
  `subscription_status` varchar(10) NOT NULL DEFAULT '',
  `title` varchar(255) NOT NULL DEFAULT '',
  `date_created_gmt` datetime NOT NULL,
  `date_updated_gmt` datetime NOT NULL,
  `is_published` tinyint(1) NOT NULL DEFAULT 1,
  PRIMARY KEY (`id`)
);
CREATE TABLE `wp_wpforms_payment_meta` (
  `id` bigint(20) NOT NULL AUTO_INCREMENT,
  `payment_id` bigint(20) NOT NULL,
  `meta_key` varchar(255),
  `meta_value` longtext,
  PRIMARY KEY (`id`)
);
CREATE TABLE `wp_wpforms_tasks_meta` (
  `id` bigint(20) NOT NULL AUTO_INCREMENT,
  `action` varchar(255) NOT NULL,
  `data` longtext NOT NULL,
  `date` datetime NOT NULL,
  PRIMARY KEY (`id`)
);
CREATE TABLE `wp_wpforms_logs` (
  `id` bigint(20) NOT NULL AUTO_INCREMENT,
  `title` varchar(255) NOT NULL,
  `message` longtext NOT NULL,
  `types` varchar(255) NOT NULL,
  `create_at` datetime NOT NULL,
  `form_id` bigint(20),
  `entry_id` bigint(20),
  `user_id` bigint(20),
  PRIMARY KEY (`id`)
);
CREATE TABLE `wp_wpforms_entries` (
  `entry_id` bigint(20) NOT NULL AUTO_INCREMENT,
  `form_id` bigint(20) NOT NULL,
  `post_id` bigint(20) NOT NULL,
  `user_id` bigint(20) NOT NULL,
  `status` varchar(30) NOT NULL,
  `type` varchar(30) NOT NULL,
  `viewed` tinyint(1) DEFAULT 0,
  `starred` tinyint(1) DEFAULT 0,
  `fields` longtext NOT NULL,
  `meta` longtext NOT NULL,
  `date` datetime NOT NULL,
  `date_modified` datetime NOT NULL,
  `ip_address` varchar(128) NOT NULL,
  `user_agent` varchar(256) NOT NULL,
  `user_uuid` varchar(36) NOT NULL,
  `notes_count` bigint(20) NOT NULL,
  PRIMARY KEY (`entry_id`)
);
CREATE TABLE `wp_wpforms_entry_fields` (
  `id` bigint(20) NOT NULL AUTO_INCREMENT,
  `entry_id` bigint(20) NOT NULL,
  `form_id` bigint(20) NOT NULL,
  `field_id` int(11) NOT NULL,
  `value` longtext NOT NULL,
  `date` datetime NOT NULL,
  PRIMARY KEY (`id`)
);
CREATE TABLE `wp_wpforms_entry_meta` (
  `id` bigint(20) NOT NULL AUTO_INCREMENT,
  `entry_id` bigint(20) NOT NULL,
  `form_id` bigint(20) NOT NULL,
  `user_id` bigint(20) NOT NULL,
  `status` varchar(30) NOT NULL,
  `type` varchar(255) NOT NULL,
  `data` longtext NOT NULL,
  `date` datetime NOT NULL,
  PRIMARY KEY (`id`)
);
