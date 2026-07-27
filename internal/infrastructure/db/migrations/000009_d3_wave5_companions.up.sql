-- goAthena Phase R0 D3 Wave 5: rAthena companion table parity.
-- Column definitions and indexes match sql-files/main.sql; InnoDB and utf8mb4
-- are goAthena storage policy adaptations (D-006).
-- Tables: elemental, homunculus, mercenary, mercenary_owner, pet.

CREATE TABLE IF NOT EXISTS `elemental` (
  `ele_id` int(11) unsigned NOT NULL auto_increment,
  `char_id` int(11) unsigned NOT NULL,
  `class` mediumint(9) unsigned NOT NULL default '0',
  `mode` int(11) unsigned NOT NULL default '1',
  `hp` int(11) unsigned NOT NULL default '0',
  `sp` int(11) unsigned NOT NULL default '0',
  `max_hp` int(11) unsigned NOT NULL default '0',
  `max_sp` int(11) unsigned NOT NULL default '0',
  `atk1` MEDIUMINT(6) unsigned NOT NULL default '0',
  `atk2` MEDIUMINT(6) unsigned NOT NULL default '0',
  `matk` MEDIUMINT(6) unsigned NOT NULL default '0',
  `aspd` smallint(4) unsigned NOT NULL default '0',
  `def` smallint(4) unsigned NOT NULL default '0',
  `mdef` smallint(4) unsigned NOT NULL default '0',
  `flee` smallint(4) unsigned NOT NULL default '0',
  `hit` smallint(4) unsigned NOT NULL default '0',
  `life_time` bigint(20) NOT NULL default '0',
  PRIMARY KEY  (`ele_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `homunculus` (
  `homun_id` int(11) NOT NULL auto_increment,
  `char_id` int(11) unsigned NOT NULL,
  `class` mediumint(9) unsigned NOT NULL default '0',
  `prev_class` mediumint(9) NOT NULL default '0',
  `name` varchar(24) NOT NULL default '',
  `level` smallint(4) NOT NULL default '0',
  `exp` bigint(20) unsigned NOT NULL default '0',
  `intimacy` int(12) NOT NULL default '0',
  `hunger` smallint(4) NOT NULL default '0',
  `str` smallint(4) unsigned NOT NULL default '0',
  `agi` smallint(4) unsigned NOT NULL default '0',
  `vit` smallint(4) unsigned NOT NULL default '0',
  `int` smallint(4) unsigned NOT NULL default '0',
  `dex` smallint(4) unsigned NOT NULL default '0',
  `luk` smallint(4) unsigned NOT NULL default '0',
  `hp` int(11) unsigned NOT NULL default '0',
  `max_hp` int(11) unsigned NOT NULL default '0',
  `sp` int(11) unsigned NOT NULL default '0',
  `max_sp` int(11) unsigned NOT NULL default '0',
  `skill_point` smallint(4) unsigned NOT NULL default '0',
  `alive` tinyint(2) NOT NULL default '1',
  `rename_flag` tinyint(2) NOT NULL default '0',
  `vaporize` tinyint(2) NOT NULL default '0',
  `autofeed` tinyint(2) NOT NULL default '0',
  PRIMARY KEY  (`homun_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `mercenary` (
  `mer_id` int(11) unsigned NOT NULL auto_increment,
  `char_id` int(11) unsigned NOT NULL,
  `class` mediumint(9) unsigned NOT NULL default '0',
  `hp` int(11) unsigned NOT NULL default '0',
  `sp` int(11) unsigned NOT NULL default '0',
  `kill_counter` int(11) NOT NULL,
  `life_time` bigint(20) NOT NULL default '0',
  PRIMARY KEY  (`mer_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `mercenary_owner` (
  `char_id` int(11) unsigned NOT NULL,
  `merc_id` int(11) unsigned NOT NULL default '0',
  `arch_calls` int(11) NOT NULL default '0',
  `arch_faith` int(11) NOT NULL default '0',
  `spear_calls` int(11) NOT NULL default '0',
  `spear_faith` int(11) NOT NULL default '0',
  `sword_calls` int(11) NOT NULL default '0',
  `sword_faith` int(11) NOT NULL default '0',
  PRIMARY KEY  (`char_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `pet` (
  `pet_id` int(11) unsigned NOT NULL auto_increment,
  `class` mediumint(9) unsigned NOT NULL default '0',
  `name` varchar(24) NOT NULL default '',
  `account_id` int(11) unsigned NOT NULL default '0',
  `char_id` int(11) unsigned NOT NULL default '0',
  `level` smallint(4) unsigned NOT NULL default '0',
  `egg_id` int(10) unsigned NOT NULL default '0',
  `equip` int(10) unsigned NOT NULL default '0',
  `intimate` smallint(9) unsigned NOT NULL default '0',
  `hungry` smallint(9) unsigned NOT NULL default '0',
  `rename_flag` tinyint(4) unsigned NOT NULL default '0',
  `incubate` int(11) unsigned NOT NULL default '0',
  `autofeed` tinyint(2) NOT NULL default '0',
  PRIMARY KEY  (`pet_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
