-- goAthena Phase R0 D3 Wave 2: rAthena skill / sc_data / hotkey table parity.
-- Column definitions and indexes match sql-files/main.sql; InnoDB and utf8mb4
-- are goAthena storage policy adaptations (D-006).
-- Tables: hotkey, sc_data, skillcooldown, skill, skill_homunculus,
-- skillcooldown_homunculus, skillcooldown_mercenary.

CREATE TABLE IF NOT EXISTS `hotkey` (
  `char_id` INT(11) unsigned NOT NULL,
  `hotkey` TINYINT(2) unsigned NOT NULL,
  `type` TINYINT(1) unsigned NOT NULL default '0',
  `itemskill_id` INT(11) unsigned NOT NULL default '0',
  `skill_lvl` TINYINT(4) unsigned NOT NULL default '0',
  PRIMARY KEY (`char_id`,`hotkey`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `sc_data` (
  `account_id` int(11) unsigned NOT NULL,
  `char_id` int(11) unsigned NOT NULL,
  `type` smallint(11) unsigned NOT NULL,
  `tick` bigint(20) NOT NULL,
  `val1` int(11) NOT NULL default '0',
  `val2` int(11) NOT NULL default '0',
  `val3` int(11) NOT NULL default '0',
  `val4` int(11) NOT NULL default '0',
  PRIMARY KEY (`char_id`, `type`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `skillcooldown` (
  `account_id` int(11) unsigned NOT NULL,
  `char_id` int(11) unsigned NOT NULL,
  `skill` smallint(11) unsigned NOT NULL DEFAULT '0',
  `tick` bigint(20) NOT NULL,
  PRIMARY KEY (`char_id`,`skill`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `skill` (
  `char_id` int(11) unsigned NOT NULL default '0',
  `id` smallint(11) unsigned NOT NULL default '0',
  `lv` tinyint(4) unsigned NOT NULL default '0',
  `flag` TINYINT(1) UNSIGNED NOT NULL default 0,
  PRIMARY KEY  (`char_id`,`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `skill_homunculus` (
  `homun_id` int(11) NOT NULL,
  `id` int(11) NOT NULL,
  `lv` smallint(6) NOT NULL,
  PRIMARY KEY  (`homun_id`,`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `skillcooldown_homunculus` (
  `homun_id` int(11) NOT NULL,
  `skill` smallint(11) unsigned NOT NULL DEFAULT '0',
  `tick` bigint(20) NOT NULL,
  PRIMARY KEY (`homun_id`,`skill`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `skillcooldown_mercenary` (
  `mer_id` int(11) unsigned NOT NULL,
  `skill` smallint(11) unsigned NOT NULL DEFAULT '0',
  `tick` bigint(20) NOT NULL,
  PRIMARY KEY (`mer_id`,`skill`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
