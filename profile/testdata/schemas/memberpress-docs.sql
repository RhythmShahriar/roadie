-- MemberPress (paid, no install to capture). Table names from
-- https://memberpress.com/docs/where-is-data-stored/ ; the columns are the
-- ones the profile's rules need plus the usual ID and date columns, as
-- MeprDb creates them. Confirm them against a licensed install
-- (capture.sh with MEMBERPRESS_ZIP set) before relying on the others.
-- Member addresses are user meta (mepr-address-*) in wp_usermeta.
CREATE TABLE `wp_mepr_transactions` (
  `id` bigint(20) NOT NULL AUTO_INCREMENT,
  `amount` decimal(16,2) NOT NULL,
  `total` decimal(16,2) DEFAULT 0,
  `user_id` bigint(20) NOT NULL,
  `product_id` bigint(20) NOT NULL,
  `coupon_id` bigint(20) DEFAULT NULL,
  `trans_num` varchar(255) DEFAULT NULL,
  `status` varchar(255) DEFAULT 'pending',
  `txn_type` varchar(255) DEFAULT 'payment',
  `response` text DEFAULT NULL,
  `gateway` varchar(255) DEFAULT 'MeprPayPalGateway',
  `subscription_id` bigint(20) DEFAULT NULL,
  `ip_addr` varchar(255) DEFAULT NULL,
  `prorated` tinyint(1) DEFAULT 0,
  `created_at` datetime NOT NULL,
  `expires_at` datetime DEFAULT '0000-00-00 00:00:00',
  `corporate_account_id` bigint(20) DEFAULT 0,
  `parent_transaction_id` bigint(20) DEFAULT 0,
  PRIMARY KEY (`id`)
);
CREATE TABLE `wp_mepr_subscriptions` (
  `id` bigint(20) NOT NULL AUTO_INCREMENT,
  `user_id` bigint(20) NOT NULL,
  `product_id` bigint(20) NOT NULL,
  `coupon_id` bigint(20) DEFAULT NULL,
  `subscr_id` varchar(255) DEFAULT NULL,
  `price` decimal(16,2) NOT NULL,
  `total` decimal(16,2) DEFAULT 0,
  `period` int(11) DEFAULT 1,
  `period_type` varchar(20) DEFAULT 'months',
  `limit_cycles` tinyint(1) DEFAULT 0,
  `limit_cycles_num` int(11) DEFAULT 1,
  `trial` tinyint(1) DEFAULT 0,
  `status` varchar(20) DEFAULT 'pending',
  `gateway` varchar(255) DEFAULT 'MeprPayPalGateway',
  `response` longtext DEFAULT NULL,
  `ip_addr` varchar(255) DEFAULT NULL,
  `cc_last4` varchar(10) DEFAULT '4242',
  `cc_exp_month` varchar(10) DEFAULT '01',
  `cc_exp_year` varchar(10) DEFAULT '1999',
  `token` varchar(255) DEFAULT NULL,
  `created_at` datetime NOT NULL,
  PRIMARY KEY (`id`)
);
CREATE TABLE `wp_mepr_members` (
  `id` bigint(20) NOT NULL AUTO_INCREMENT,
  `user_id` bigint(20) NOT NULL,
  `first_txn_id` bigint(20) DEFAULT NULL,
  `latest_txn_id` bigint(20) DEFAULT NULL,
  `txn_count` bigint(20) DEFAULT NULL,
  `expired_txn_count` bigint(20) DEFAULT NULL,
  `active_txn_count` bigint(20) DEFAULT NULL,
  `sub_count` bigint(20) DEFAULT NULL,
  `memberships` longtext DEFAULT NULL,
  `total_spent` decimal(16,2) DEFAULT 0,
  `created_at` datetime NOT NULL,
  `updated_at` datetime DEFAULT NULL,
  PRIMARY KEY (`id`)
);
