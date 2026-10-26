-- MySQL dump 10.13  Distrib 8.0.36, for Linux (x86_64)
-- Server version	8.0.36
/*!40101 SET @OLD_SQL_MODE=@@SQL_MODE, SQL_MODE='NO_BACKSLASH_ESCAPES,NO_AUTO_VALUE_ON_ZERO' */;
CREATE TABLE `paths` (
  `id` int NOT NULL,
  `path` varchar(255) NOT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
INSERT INTO `paths` VALUES (1,'C:\Users\'),(2,'it''s'),(3,'a\nb');
/*!40101 SET SQL_MODE=@OLD_SQL_MODE */;
INSERT INTO `paths` VALUES (4,'back\\slash');
-- Dump completed on 2026-10-26 10:00:00
