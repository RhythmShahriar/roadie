-- Fluent Forms tables from its migrations in
-- https://plugins.svn.wordpress.org/fluentform/trunk/database/Migrations/
-- capture.sh replaces this file with a dump of an install.
CREATE TABLE `wp_fluentform_forms` (
  `id` int unsigned NOT NULL AUTO_INCREMENT,
  `title` varchar(255) NOT NULL,
  `status` varchar(45) DEFAULT 'Draft',
  `appearance_settings` text,
  `form_fields` longtext,
  `has_payment` tinyint(1) NOT NULL DEFAULT 0,
  `type` varchar(45),
  `conditions` text,
  `created_by` int,
  `created_at` timestamp NULL,
  `updated_at` timestamp NULL,
  PRIMARY KEY (`id`)
);
CREATE TABLE `wp_fluentform_submissions` (
  `id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  `form_id` int unsigned NULL,
  `serial_number` int unsigned NULL,
  `response` longtext NULL,
  `source_url` varchar(255) NULL,
  `user_id` int unsigned NULL,
  `status` varchar(45) NULL DEFAULT 'unread',
  `is_favourite` tinyint(1) NOT NULL DEFAULT 0,
  `browser` varchar(45) NULL,
  `device` varchar(45) NULL,
  `ip` varchar(45) NULL,
  `city` varchar(45) NULL,
  `country` varchar(45) NULL,
  `payment_status` varchar(45) NULL,
  `payment_method` varchar(45) NULL,
  `payment_type` varchar(45) NULL,
  `currency` varchar(45) NULL,
  `payment_total` float NULL,
  `total_paid` float NULL,
  `created_at` timestamp NULL,
  `updated_at` timestamp NULL,
  PRIMARY KEY (`id`)
);
CREATE TABLE `wp_fluentform_submission_meta` (
  `id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  `response_id` bigint(20) unsigned,
  `form_id` int unsigned,
  `meta_key` varchar(45),
  `value` longtext,
  `status` varchar(45),
  `user_id` int unsigned,
  `name` varchar(45),
  `created_at` timestamp NULL,
  `updated_at` timestamp NULL,
  PRIMARY KEY (`id`)
);
CREATE TABLE `wp_fluentform_entry_details` (
  `id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  `form_id` bigint(20) unsigned,
  `submission_id` bigint(20) unsigned,
  `field_name` varchar(255),
  `sub_field_name` varchar(255),
  `field_value` longtext,
  PRIMARY KEY (`id`)
);
CREATE TABLE `wp_fluentform_logs` (
  `id` int unsigned NOT NULL AUTO_INCREMENT,
  `parent_source_id` int unsigned NULL,
  `source_type` varchar(255) NULL,
  `source_id` int unsigned NULL,
  `component` varchar(255) NULL,
  `status` char(30) NULL,
  `title` varchar(255) NOT NULL,
  `description` longtext NULL,
  `created_at` timestamp NULL,
  PRIMARY KEY (`id`)
);
CREATE TABLE `wp_fluentform_form_analytics` (
  `id` int unsigned NOT NULL AUTO_INCREMENT,
  `form_id` int unsigned NULL,
  `user_id` int unsigned NULL,
  `source_url` varchar(255) NOT NULL,
  `platform` char(30) NULL,
  `browser` char(30) NULL,
  `city` varchar(100) NULL,
  `country` varchar(100) NULL,
  `ip` char(15) NULL,
  `count` int DEFAULT 1,
  `created_at` timestamp NULL,
  PRIMARY KEY (`id`)
);
CREATE TABLE `wp_fluentform_draft_submissions` (
  `id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  `form_id` int unsigned NULL,
  `hash` varchar(255) NOT NULL,
  `type` varchar(255) DEFAULT 'step_data',
  `step_completed` int unsigned NOT NULL,
  `user_id` int unsigned NOT NULL,
  `response` longtext NULL,
  `source_url` varchar(255) NULL,
  `browser` varchar(45) NULL,
  `device` varchar(45) NULL,
  `ip` varchar(45) NULL,
  `created_at` timestamp NULL,
  `updated_at` timestamp NULL,
  PRIMARY KEY (`id`)
);
