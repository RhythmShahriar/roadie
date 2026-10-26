-- MySQL dump 10.13  Distrib 8.0.36, for Linux (x86_64)
-- Server version	8.0.36
/*!40101 SET NAMES utf8mb4 */;
CREATE TABLE `wp_users` (
  `ID` bigint unsigned NOT NULL AUTO_INCREMENT,
  `user_email` varchar(100) NOT NULL DEFAULT '',
  PRIMARY KEY (`ID`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
/*!50003 SET @saved_cs_client      = @@character_set_client */ ;
/*!50003 SET sql_mode              = 'ONLY_FULL_GROUP_BY,STRICT_TRANS_TABLES' */ ;
DELIMITER ;;
/*!50003 CREATE*/ /*!50017 DEFINER=`root`@`localhost`*/ /*!50003 TRIGGER `keep_email` AFTER UPDATE ON `wp_users` FOR EACH ROW BEGIN
  IF NEW.user_email <> OLD.user_email THEN
    UPDATE wp_users SET user_email = 'old;value' WHERE ID = NEW.ID;
  END IF;
END */;;
DELIMITER ;
/*!50003 SET sql_mode              = @saved_sql_mode */ ;
DELIMITER $$
CREATE DEFINER=`root`@`%` PROCEDURE `p`()
BEGIN
  SELECT 1; SELECT ';'; -- a comment; with a semicolon
END$$
DELIMITER ;
-- Dump completed on 2026-10-26 10:00:00
