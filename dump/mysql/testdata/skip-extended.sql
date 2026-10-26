-- MySQL dump 10.13  Distrib 8.0.36, for Linux (x86_64)
-- Server version	8.0.36
/*!40101 SET NAMES utf8mb4 */;
DROP TABLE IF EXISTS `wp_terms`;
CREATE TABLE `wp_terms` (
  `term_id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `name` varchar(200) NOT NULL DEFAULT '',
  `slug` varchar(200) NOT NULL DEFAULT '',
  PRIMARY KEY (`term_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
LOCK TABLES `wp_terms` WRITE;
INSERT INTO `wp_terms` VALUES (1,'Uncategorized','uncategorized');
INSERT INTO `wp_terms` VALUES (2,'News; and more','news');
INSERT INTO `wp_terms` VALUES (3,'-- not a comment','dashes');
INSERT INTO `wp_terms` VALUES (4,'# nor this','hash');
UNLOCK TABLES;
-- Dump completed on 2026-10-26 10:00:00
