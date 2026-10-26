-- MySQL dump 10.13  Distrib 8.0.36, for Linux (x86_64)
-- Server version	8.0.36
/*!40101 SET NAMES utf8mb4 */;
DROP TABLE IF EXISTS `wp_links`;
CREATE TABLE `wp_links` (
  `link_id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `link_url` varchar(255) NOT NULL DEFAULT '',
  `link_name` varchar(255) NOT NULL DEFAULT '',
  `link_owner` bigint unsigned NOT NULL DEFAULT '1',
  PRIMARY KEY (`link_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
LOCK TABLES `wp_links` WRITE;
INSERT INTO `wp_links` (`link_id`, `link_url`, `link_name`, `link_owner`) VALUES (1,'https://example.test/a','A',1),(2,'https://example.test/b','B (b)',1);
INSERT IGNORE INTO `wp_links` (`link_name`, `link_id`) VALUES ('Reordered',3);
REPLACE INTO `wp_links` VALUES (4,'https://example.test/d','D',2);
INSERT INTO `wp_links` VALUES (5,'https://example.test/e','E',1) ON DUPLICATE KEY UPDATE `link_name`=VALUES(`link_name`), `link_owner`=1;
UNLOCK TABLES;
-- Dump completed on 2026-10-26 10:00:00
