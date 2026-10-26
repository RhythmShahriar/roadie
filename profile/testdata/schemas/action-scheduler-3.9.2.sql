-- Action Scheduler 3.9.2 (schema version 7), prefix wp_. Read from
-- https://raw.githubusercontent.com/woocommerce/action-scheduler/3.9.2/classes/schema/ActionScheduler_StoreSchema.php
-- and ActionScheduler_LoggerSchema.php in the same folder. capture.sh replaces
-- this file with a dump of the copy WooCommerce 9.8.0 bundles.
CREATE TABLE `wp_actionscheduler_actions` (
  `action_id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  `hook` varchar(191) NOT NULL,
  `status` varchar(20) NOT NULL,
  `scheduled_date_gmt` datetime NULL DEFAULT '0000-00-00 00:00:00',
  `scheduled_date_local` datetime NULL DEFAULT '0000-00-00 00:00:00',
  `priority` tinyint unsigned NOT NULL DEFAULT '10',
  `args` varchar(191),
  `schedule` longtext,
  `group_id` bigint(20) unsigned NOT NULL DEFAULT '0',
  `attempts` int(11) NOT NULL DEFAULT '0',
  `last_attempt_gmt` datetime NULL DEFAULT '0000-00-00 00:00:00',
  `last_attempt_local` datetime NULL DEFAULT '0000-00-00 00:00:00',
  `claim_id` bigint(20) unsigned NOT NULL DEFAULT '0',
  `extended_args` varchar(8000) DEFAULT NULL,
  PRIMARY KEY (`action_id`)
);
CREATE TABLE `wp_actionscheduler_claims` (
  `claim_id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  `date_created_gmt` datetime NULL DEFAULT '0000-00-00 00:00:00',
  PRIMARY KEY (`claim_id`)
);
CREATE TABLE `wp_actionscheduler_groups` (
  `group_id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  `slug` varchar(255) NOT NULL,
  PRIMARY KEY (`group_id`)
);
CREATE TABLE `wp_actionscheduler_logs` (
  `log_id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  `action_id` bigint(20) unsigned NOT NULL,
  `message` text NOT NULL,
  `log_date_gmt` datetime NULL DEFAULT '0000-00-00 00:00:00',
  `log_date_local` datetime NULL DEFAULT '0000-00-00 00:00:00',
  PRIMARY KEY (`log_id`)
);
