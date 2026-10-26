/*!40101 SET NAMES utf8mb4 */;
/*!50503 SET character_set_client = utf8mb4 */;
/*!40101 SET collation_connection = utf8mb4_unicode_520_ci */;
SET NAMES utf8mb4 COLLATE utf8mb4_unicode_520_ci;
CREATE TABLE `t` (
  `a` varchar(10) COLLATE utf8mb4_unicode_520_ci NOT NULL DEFAULT 'utf8mb4_0900_ai_ci',
  `b` text COLLATE utf8mb4_unicode_520_ci
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_520_ci COMMENT='keeps utf8mb4_0900_ai_ci in strings';
INSERT INTO `t` VALUES ('utf8mb4_0900_ai_ci','data stays');
/*!50003 SET sql_mode = 'x' */;
