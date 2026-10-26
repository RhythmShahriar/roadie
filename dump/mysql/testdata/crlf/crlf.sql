-- MySQL dump 10.13  Distrib 8.0.36, for Win64 (x86_64)
-- Server version	8.0.36
/*!40101 SET NAMES utf8mb4 */;
CREATE TABLE `t` (
  `id` int NOT NULL,
  `note` text,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
INSERT INTO `t` VALUES (1,'two\r\nlines'),
(2,'raw
crlf');
-- Dump completed on 2026-10-26 10:00:00
