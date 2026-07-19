-- goAthena Phase R0 D3 Wave 7: rAthena logs + misc table parity (D3 closure).
-- Column definitions, PRIMARY KEY/KEY/UNIQUE clauses, column casing, and
-- seed INSERTs match sql-files/main.sql verbatim (D-001). InnoDB and
-- utf8mb4 are goAthena storage policy adaptations (D-006); bonus_script
-- is already InnoDB in rAthena — preserved as-is. clan AUTO_INCREMENT=5
-- preserved.
-- Tables: charlog, interlog, achievement, barter, bonus_script, clan,
-- clan_alliance, db_roulette, market, sales.

CREATE TABLE IF NOT EXISTS `charlog` (
  `id` bigint(20) unsigned NOT NULL auto_increment,
  `time` datetime NOT NULL,
  `char_msg` varchar(255) NOT NULL default 'char select',
  `account_id` int(11) unsigned NOT NULL default '0',
  `char_num` tinyint(4) NOT NULL default '0',
  `name` varchar(23) NOT NULL default '',
  `str` int(11) unsigned NOT NULL default '0',
  `agi` int(11) unsigned NOT NULL default '0',
  `vit` int(11) unsigned NOT NULL default '0',
  `int` int(11) unsigned NOT NULL default '0',
  `dex` int(11) unsigned NOT NULL default '0',
  `luk` int(11) unsigned NOT NULL default '0',
  `hair` tinyint(4) NOT NULL default '0',
  `hair_color` int(11) NOT NULL default '0',
  PRIMARY KEY (`id`),
  KEY `account_id` (`account_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `interlog` (
  `id` INT NOT NULL AUTO_INCREMENT,
  `time` datetime NOT NULL,
  `log` varchar(255) NOT NULL default '',
  PRIMARY KEY (`id`),
  INDEX `time` (`time`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `achievement` (
  `char_id` int(11) unsigned NOT NULL default '0',
  `id` bigint(11) unsigned NOT NULL,
  `count1` int unsigned NOT NULL default '0',
  `count2` int unsigned NOT NULL default '0',
  `count3` int unsigned NOT NULL default '0',
  `count4` int unsigned NOT NULL default '0',
  `count5` int unsigned NOT NULL default '0',
  `count6` int unsigned NOT NULL default '0',
  `count7` int unsigned NOT NULL default '0',
  `count8` int unsigned NOT NULL default '0',
  `count9` int unsigned NOT NULL default '0',
  `count10` int unsigned NOT NULL default '0',
  `completed` datetime,
  `rewarded` datetime,
  PRIMARY KEY (`char_id`,`id`),
  KEY `char_id` (`char_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `barter` (
  `name` varchar(50) NOT NULL DEFAULT '',
  `index` SMALLINT(5) UNSIGNED NOT NULL,
  `amount` SMALLINT(5) UNSIGNED NOT NULL,
  PRIMARY KEY  (`name`,`index`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `bonus_script` (
  `char_id` INT(11) UNSIGNED NOT NULL,
  `script` TEXT NOT NULL,
  `tick` BIGINT(20) NOT NULL DEFAULT '0',
  `flag` SMALLINT(5) UNSIGNED NOT NULL DEFAULT '0',
  `type` TINYINT(1) UNSIGNED NOT NULL DEFAULT '0',
  `icon` SMALLINT(3) NOT NULL DEFAULT '-1',
  KEY `char_id` (`char_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `clan` (
  `clan_id` int(11) unsigned NOT NULL AUTO_INCREMENT,
  `name` varchar(24) NOT NULL DEFAULT '',
  `master` varchar(24) NOT NULL DEFAULT '',
  `mapname` varchar(24) NOT NULL DEFAULT '',
  `max_member` smallint(6) unsigned NOT NULL DEFAULT '0',
  PRIMARY KEY (`clan_id`)
) ENGINE=InnoDB AUTO_INCREMENT=5 DEFAULT CHARSET=utf8mb4;

INSERT INTO `clan` VALUES ('1', 'Swordman Clan', 'Raffam Oranpere', 'prontera', '500');
INSERT INTO `clan` VALUES ('2', 'Arcwand Clan', 'Devon Aire', 'geffen', '500');
INSERT INTO `clan` VALUES ('3', 'Golden Mace Clan', 'Berman Aire', 'prontera', '500');
INSERT INTO `clan` VALUES ('4', 'Crossbow Clan', 'Shaam Rumi', 'payon', '500');

CREATE TABLE IF NOT EXISTS `clan_alliance` (
  `clan_id` int(11) unsigned NOT NULL DEFAULT '0',
  `opposition` int(11) unsigned NOT NULL DEFAULT '0',
  `alliance_id` int(11) unsigned NOT NULL DEFAULT '0',
  `name` varchar(24) NOT NULL DEFAULT '',
  PRIMARY KEY (`clan_id`,`alliance_id`),
  KEY `alliance_id` (`alliance_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

INSERT INTO `clan_alliance` VALUES ('1', '0', '3', 'Golden Mace Clan');
INSERT INTO `clan_alliance` VALUES ('2', '0', '3', 'Golden Mace Clan');
INSERT INTO `clan_alliance` VALUES ('2', '1', '4', 'Crossbow Clan');
INSERT INTO `clan_alliance` VALUES ('3', '0', '1', 'Swordman Clan');
INSERT INTO `clan_alliance` VALUES ('3', '0', '2', 'Arcwand Clan');
INSERT INTO `clan_alliance` VALUES ('3', '0', '4', 'Crossbow Clan');
INSERT INTO `clan_alliance` VALUES ('4', '0', '3', 'Golden Mace Clan');
INSERT INTO `clan_alliance` VALUES ('4', '1', '2', 'Arcwand Clan');

CREATE TABLE IF NOT EXISTS `db_roulette` (
  `index` int(11) NOT NULL default '0',
  `level` smallint(5) unsigned NOT NULL,
  `item_id` int(10) unsigned NOT NULL,
  `amount` smallint(5) unsigned NOT NULL DEFAULT '1',
  `flag` smallint(5) unsigned NOT NULL DEFAULT '1',
  PRIMARY KEY (`index`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `market` (
  `name` varchar(50) NOT NULL DEFAULT '',
  `nameid` int(10) UNSIGNED NOT NULL,
  `price` INT(11) UNSIGNED NOT NULL,
  `amount` INT(11) NOT NULL,
  `flag` TINYINT(2) UNSIGNED NOT NULL DEFAULT '0',
  PRIMARY KEY  (`name`,`nameid`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `sales` (
  `nameid` int(10) unsigned NOT NULL,
  `start` datetime NOT NULL,
  `end` datetime NOT NULL,
  `amount` int(11) NOT NULL,
  PRIMARY KEY (`nameid`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
