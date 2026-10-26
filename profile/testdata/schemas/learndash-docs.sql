-- LearnDash (paid, no install to capture). The quiz tables are WP Pro
-- Quiz's under a learndash_pro_quiz_ name, read from
-- https://plugins.svn.wordpress.org/wp-pro-quiz/trunk/lib/helper/WpProQuiz_Helper_DbUpgrade.php
-- and the activity table as LearnDash documents it at
-- https://developers.learndash.com/ (learndash_user_activity).
CREATE TABLE `wp_learndash_user_activity` (
  `activity_id` bigint(20) unsigned NOT NULL AUTO_INCREMENT,
  `user_id` bigint(20) unsigned NOT NULL DEFAULT 0,
  `post_id` bigint(20) unsigned NOT NULL DEFAULT 0,
  `course_id` bigint(20) unsigned NOT NULL DEFAULT 0,
  `activity_type` varchar(50) DEFAULT NULL,
  `activity_status` tinyint(1) unsigned DEFAULT NULL,
  `activity_started` int(11) unsigned DEFAULT NULL,
  `activity_completed` int(11) unsigned DEFAULT NULL,
  `activity_updated` int(11) unsigned DEFAULT NULL,
  PRIMARY KEY (`activity_id`)
);
CREATE TABLE `wp_learndash_pro_quiz_toplist` (
  `toplist_id` int(11) NOT NULL AUTO_INCREMENT,
  `quiz_id` int(11) NOT NULL,
  `date` int(10) unsigned NOT NULL,
  `user_id` bigint(20) unsigned NOT NULL,
  `name` varchar(30) NOT NULL,
  `email` varchar(200) NOT NULL,
  `points` int(10) unsigned NOT NULL,
  `result` float unsigned NOT NULL,
  `ip` varchar(100) NOT NULL,
  PRIMARY KEY (`toplist_id`, `quiz_id`)
);
CREATE TABLE `wp_learndash_pro_quiz_statistic_ref` (
  `statistic_ref_id` int(10) unsigned NOT NULL AUTO_INCREMENT,
  `quiz_id` int(11) NOT NULL,
  `user_id` bigint(20) unsigned NOT NULL,
  `create_time` int(11) NOT NULL,
  `is_old` tinyint(1) unsigned NOT NULL,
  `form_data` text,
  PRIMARY KEY (`statistic_ref_id`)
);
