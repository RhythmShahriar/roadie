-- MailPoet 5 tables, from its Doctrine entities in
-- https://github.com/mailpoet/mailpoet/tree/trunk/mailpoet/lib/Entities
-- (only the tables the profile names, with their main columns).
-- capture.sh replaces this file with a dump of an install.
CREATE TABLE `wp_mailpoet_subscribers` (
  `id` int(11) unsigned NOT NULL AUTO_INCREMENT,
  `wp_user_id` bigint(20) NULL,
  `is_woocommerce_user` int(1) NOT NULL DEFAULT 0,
  `first_name` varchar(255) NOT NULL DEFAULT '',
  `last_name` varchar(255) NOT NULL DEFAULT '',
  `email` varchar(150) NOT NULL,
  `status` varchar(12) NOT NULL DEFAULT 'unconfirmed',
  `subscribed_ip` varchar(45) NULL,
  `confirmed_ip` varchar(45) NULL,
  `confirmed_at` timestamp NULL,
  `last_subscribed_at` timestamp NULL,
  `created_at` timestamp NULL,
  `updated_at` timestamp NULL,
  `deleted_at` timestamp NULL,
  `unconfirmed_data` longtext,
  `source` enum('form','imported','administrator','api','wordpress_user','woocommerce_user','woocommerce_checkout','unknown') DEFAULT 'unknown',
  `count_confirmations` int(11) unsigned NOT NULL DEFAULT 0,
  `unsubscribe_token` char(15) NULL,
  `link_token` char(32) NULL,
  `engagement_score` float unsigned NULL,
  `email_count` int(11) unsigned NOT NULL DEFAULT 0,
  PRIMARY KEY (`id`)
);
CREATE TABLE `wp_mailpoet_subscriber_custom_field` (
  `id` int(11) unsigned NOT NULL AUTO_INCREMENT,
  `subscriber_id` int(11) unsigned NOT NULL,
  `custom_field_id` int(11) unsigned NOT NULL,
  `value` text NOT NULL,
  `created_at` timestamp NULL,
  `updated_at` timestamp NULL,
  PRIMARY KEY (`id`)
);
CREATE TABLE `wp_mailpoet_subscriber_ips` (
  `ip` varchar(45) NOT NULL,
  `created_at` timestamp NOT NULL,
  PRIMARY KEY (`created_at`, `ip`)
);
CREATE TABLE `wp_mailpoet_sending_queues` (
  `id` int(11) unsigned NOT NULL AUTO_INCREMENT,
  `type` varchar(90) NULL,
  `newsletter_id` int(11) unsigned NULL,
  `task_id` int(11) unsigned NOT NULL,
  `newsletter_rendered_body` longtext,
  `newsletter_rendered_subject` varchar(250) NULL,
  `subscribers` longtext,
  `count_total` int(11) unsigned NOT NULL DEFAULT 0,
  `count_processed` int(11) unsigned NOT NULL DEFAULT 0,
  `count_to_process` int(11) unsigned NOT NULL DEFAULT 0,
  `meta` longtext,
  `created_at` timestamp NULL,
  `updated_at` timestamp NULL,
  `deleted_at` timestamp NULL,
  PRIMARY KEY (`id`)
);
CREATE TABLE `wp_mailpoet_log` (
  `id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  `name` varchar(255),
  `level` int(11),
  `message` longtext,
  `created_at` timestamp NULL,
  PRIMARY KEY (`id`)
);
CREATE TABLE `wp_mailpoet_statistics_opens` (
  `id` int(11) unsigned NOT NULL AUTO_INCREMENT,
  `newsletter_id` int(11) unsigned NOT NULL,
  `subscriber_id` int(11) unsigned NOT NULL,
  `queue_id` int(11) unsigned NOT NULL,
  `created_at` timestamp NULL,
  PRIMARY KEY (`id`)
);
CREATE TABLE `wp_mailpoet_statistics_clicks` (
  `id` int(11) unsigned NOT NULL AUTO_INCREMENT,
  `newsletter_id` int(11) unsigned NOT NULL,
  `subscriber_id` int(11) unsigned NOT NULL,
  `queue_id` int(11) unsigned NOT NULL,
  `link_id` int(11) unsigned NOT NULL,
  `count` int(11) unsigned NOT NULL,
  `created_at` timestamp NULL,
  `updated_at` timestamp NULL,
  PRIMARY KEY (`id`)
);
