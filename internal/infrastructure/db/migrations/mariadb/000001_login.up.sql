-- Wave 1: rAthena `login` account table (sql-files/main.sql, Thai Classic).
-- goAthena owns the schema via migrations; GORM never AutoMigrates. InnoDB
-- replaces MyISAM (same column layout, transactional); auto-increment starts at
-- 2000000 to match rAthena's account-id namespace.
CREATE TABLE IF NOT EXISTS `login` (
  `account_id` int unsigned NOT NULL AUTO_INCREMENT,
  `userid` varchar(23) NOT NULL DEFAULT '',
  `user_pass` varchar(32) NOT NULL DEFAULT '',
  `sex` enum('M','F','S') NOT NULL DEFAULT 'M',
  `email` varchar(39) NOT NULL DEFAULT '',
  `group_id` tinyint NOT NULL DEFAULT 0,
  `state` int unsigned NOT NULL DEFAULT 0,
  `unban_time` int unsigned NOT NULL DEFAULT 0,
  `expiration_time` int unsigned NOT NULL DEFAULT 0,
  `logincount` mediumint unsigned NOT NULL DEFAULT 0,
  `lastlogin` datetime DEFAULT NULL,
  `last_ip` varchar(100) NOT NULL DEFAULT '',
  `birthdate` date DEFAULT NULL,
  `character_slots` tinyint unsigned NOT NULL DEFAULT 0,
  `pincode` varchar(4) NOT NULL DEFAULT '',
  `pincode_change` int unsigned NOT NULL DEFAULT 0,
  `vip_time` int unsigned NOT NULL DEFAULT 0,
  `old_group` tinyint NOT NULL DEFAULT 0,
  `web_auth_token` varchar(17) DEFAULT NULL,
  `web_auth_token_enabled` tinyint NOT NULL DEFAULT 0,
  PRIMARY KEY (`account_id`),
  KEY `name` (`userid`),
  UNIQUE KEY `web_auth_token_key` (`web_auth_token`)
) ENGINE=InnoDB AUTO_INCREMENT=2000000;
