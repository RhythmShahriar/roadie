-- Ninja Forms 3 tables from its migrations, e.g.
-- https://plugins.svn.wordpress.org/ninja-forms/trunk/includes/Database/Migrations/Fields.php
-- Submissions are nf_sub posts in wp_posts and wp_postmeta.
-- capture.sh replaces this file with a dump of an install.
CREATE TABLE `wp_nf3_forms` (
  `id` int NOT NULL AUTO_INCREMENT,
  `title` longtext,
  `key` longtext,
  `created_at` timestamp,
  `updated_at` datetime,
  `views` int(11),
  `subs` int(11),
  `form_title` longtext,
  `default_label_pos` varchar(15),
  `show_title` bit,
  `clear_complete` bit,
  `hide_complete` bit,
  `logged_in` bit,
  `seq_num` int,
  UNIQUE KEY (`id`)
);
CREATE TABLE `wp_nf3_fields` (
  `id` int NOT NULL AUTO_INCREMENT,
  `label` longtext,
  `key` longtext,
  `type` longtext,
  `parent_id` int NOT NULL,
  `created_at` timestamp,
  `updated_at` datetime,
  `field_label` longtext,
  `field_key` longtext,
  `order` int(11),
  `required` bit,
  `default_value` longtext,
  `label_pos` varchar(15),
  `personally_identifiable` bit,
  UNIQUE KEY (`id`)
);
CREATE TABLE `wp_nf3_objects` (
  `id` int NOT NULL AUTO_INCREMENT,
  `type` longtext,
  `title` longtext,
  `created_at` timestamp,
  `updated_at` datetime,
  `object_title` longtext,
  UNIQUE KEY (`id`)
);
CREATE TABLE `wp_nf3_object_meta` (
  `id` int NOT NULL AUTO_INCREMENT,
  `parent_id` int NOT NULL,
  `key` longtext NOT NULL,
  `value` longtext,
  `meta_key` longtext,
  `meta_value` longtext,
  UNIQUE KEY (`id`)
);
CREATE TABLE `wp_nf3_chunks` (
  `id` int NOT NULL AUTO_INCREMENT,
  `name` varchar(200),
  `value` longtext,
  UNIQUE KEY (`id`)
);
