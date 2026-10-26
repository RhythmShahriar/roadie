-- MySQL dump 10.13  Distrib 8.0.36, for Linux (x86_64)
-- Server version	8.0.36
/*!40101 SET NAMES utf8mb4 */;
DROP TABLE IF EXISTS `files`;
CREATE TABLE `files` (
  `id` int NOT NULL,
  `name` varchar(64) CHARACTER SET latin1 NOT NULL,
  `data` longblob,
  `hash` binary(16) DEFAULT NULL,
  `flags` bit(8) DEFAULT NULL,
  `body` varchar(20) DEFAULT NULL,
  `total` decimal(10,2) DEFAULT NULL,
  `created` timestamp NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `hash` (`hash`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
LOCK TABLES `files` WRITE;
INSERT INTO `files` VALUES (1,'a.bin',0x00FF10,0x0123456789ABCDEF0123456789ABCDEF,0x05,_binary 'raw\0bytes',-12.50,'2026-01-01 00:00:00'),(2,'b.bin',NULL,NULL,b'101',_utf8mb4'intro',1e3,NULL),(3,'c.bin',X'CAFE',NULL,0b11,N'national',.5,'2026-01-02 00:00:00');
UNLOCK TABLES;
-- Dump completed on 2026-10-26 10:00:00
