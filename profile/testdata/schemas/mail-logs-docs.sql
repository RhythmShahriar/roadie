-- The mail log tables of WP Mail SMTP (debug events: Lite; email log: Pro,
-- as https://wpmailsmtp.com/docs/how-to-set-up-email-logging/ describes),
-- Post SMTP, FluentSMTP, WP Mail Logging and Email Log, from their sources:
-- https://plugins.svn.wordpress.org/post-smtp/trunk/Postman/Postman-Email-Log/
-- https://plugins.svn.wordpress.org/fluent-smtp/trunk/database/migrations/EmailLogs.php
-- https://plugins.svn.wordpress.org/wp-mail-logging/trunk/src/Model/WPML_Mail.php
-- https://plugins.svn.wordpress.org/email-log/trunk/include/Core/DB/TableManager.php
-- capture.sh replaces the free ones with a dump of an install.
CREATE TABLE `wp_wpmailsmtp_debug_events` (
  `id` int(10) unsigned NOT NULL AUTO_INCREMENT,
  `content` text,
  `initiator` text,
  `event_type` tinyint(3) unsigned NOT NULL DEFAULT 0,
  `created_at` timestamp NOT NULL,
  PRIMARY KEY (`id`)
);
CREATE TABLE `wp_wpmailsmtp_emails_log` (
  `id` int(10) unsigned NOT NULL AUTO_INCREMENT,
  `message_id` varchar(255),
  `subject` varchar(191) NOT NULL,
  `people` text NOT NULL,
  `headers` text NOT NULL,
  `content_plain` longtext,
  `content_html` longtext,
  `status` tinyint(3) unsigned NOT NULL DEFAULT 0,
  `date_sent` timestamp NOT NULL,
  `mailer` varchar(255) NOT NULL,
  `attachments` tinyint(3) unsigned NOT NULL DEFAULT 0,
  `initiator_name` varchar(255),
  `initiator_file` text,
  `parent_id` int(10) unsigned,
  `error_text` text,
  PRIMARY KEY (`id`)
);
CREATE TABLE `wp_post_smtp_logs` (
  `id` bigint(20) NOT NULL AUTO_INCREMENT,
  `solution` longtext,
  `success` varchar(255),
  `from_header` varchar(255),
  `to_header` longtext,
  `cc_header` longtext,
  `bcc_header` longtext,
  `reply_to_header` varchar(255),
  `transport_uri` varchar(255),
  `original_to` longtext,
  `original_subject` varchar(255),
  `original_message` longtext,
  `original_headers` longtext,
  `session_transcript` longtext,
  `time` bigint(20),
  PRIMARY KEY (`id`)
);
CREATE TABLE `wp_post_smtp_logmeta` (
  `id` bigint(20) NOT NULL AUTO_INCREMENT,
  `log_id` bigint(20) NOT NULL,
  `meta_key` varchar(255),
  `meta_value` longtext,
  PRIMARY KEY (`id`)
);
CREATE TABLE `wp_fsmpt_email_logs` (
  `id` int unsigned NOT NULL AUTO_INCREMENT,
  `site_id` int unsigned NULL,
  `to` text NULL,
  `from` varchar(255),
  `subject` varchar(255),
  `body` longtext NULL,
  `headers` longtext NULL,
  `attachments` longtext NULL,
  `status` varchar(20) DEFAULT 'pending',
  `response` text NULL,
  `extra` text NULL,
  `retries` int unsigned NULL DEFAULT 0,
  `resent_count` int unsigned NULL DEFAULT 0,
  `source` varchar(255) NULL,
  `created_at` timestamp NULL,
  `updated_at` timestamp NULL,
  PRIMARY KEY (`id`)
);
CREATE TABLE `wp_wpml_mails` (
  `mail_id` int(11) NOT NULL AUTO_INCREMENT,
  `timestamp` timestamp NOT NULL,
  `host` varchar(200) NOT NULL DEFAULT '0',
  `receiver` varchar(200) NOT NULL DEFAULT '0',
  `subject` varchar(200) NOT NULL DEFAULT '0',
  `message` text,
  `headers` text,
  `attachments` text,
  `error` varchar(400) DEFAULT '',
  `plugin_version` varchar(200) NOT NULL DEFAULT '0',
  PRIMARY KEY (`mail_id`)
);
CREATE TABLE `wp_email_log` (
  `id` mediumint(9) NOT NULL AUTO_INCREMENT,
  `to_email` varchar(500) NOT NULL,
  `subject` varchar(500) NOT NULL,
  `message` text NOT NULL,
  `headers` text NOT NULL,
  `attachments` text NOT NULL,
  `sent_date` timestamp NOT NULL,
  `attachment_name` varchar(1000),
  `ip_address` varchar(15),
  `result` tinyint(1),
  `error_message` varchar(1000),
  PRIMARY KEY (`id`)
);
