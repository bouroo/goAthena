-- goAthena Phase R0 D3 Wave 1: rAthena registry-table parity.
-- Column definitions and indexes match sql-files/main.sql; InnoDB and utf8mb4
-- are goAthena storage policy adaptations (D-006).
-- Tables: acc_reg_num, acc_reg_str, char_reg_num, char_reg_str,
-- global_acc_reg_num, global_acc_reg_str, mapreg.

CREATE TABLE IF NOT EXISTS `acc_reg_num` (
  `account_id` int(11) unsigned NOT NULL default '0',
  `key` varchar(32) binary NOT NULL default '',
  `index` int(11) unsigned NOT NULL default '0',
  `value` bigint(11) NOT NULL default '0',
  PRIMARY KEY (`account_id`,`key`,`index`),
  KEY `account_id` (`account_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `acc_reg_str` (
  `account_id` int(11) unsigned NOT NULL default '0',
  `key` varchar(32) binary NOT NULL default '',
  `index` int(11) unsigned NOT NULL default '0',
  `value` varchar(254) NOT NULL default '0',
  PRIMARY KEY (`account_id`,`key`,`index`),
  KEY `account_id` (`account_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `char_reg_num` (
  `char_id` int(11) unsigned NOT NULL default '0',
  `key` varchar(32) binary NOT NULL default '',
  `index` int(11) unsigned NOT NULL default '0',
  `value` bigint(11) NOT NULL default '0',
  PRIMARY KEY (`char_id`,`key`,`index`),
  KEY `char_id` (`char_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `char_reg_str` (
  `char_id` int(11) unsigned NOT NULL default '0',
  `key` varchar(32) binary NOT NULL default '',
  `index` int(11) unsigned NOT NULL default '0',
  `value` varchar(254) NOT NULL default '0',
  PRIMARY KEY (`char_id`,`key`,`index`),
  KEY `char_id` (`char_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `global_acc_reg_num` (
  `account_id` int(11) unsigned NOT NULL default '0',
  `key` varchar(32) binary NOT NULL default '',
  `index` int(11) unsigned NOT NULL default '0',
  `value` bigint(11) NOT NULL default '0',
  PRIMARY KEY (`account_id`,`key`,`index`),
  KEY `account_id` (`account_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `global_acc_reg_str` (
  `account_id` int(11) unsigned NOT NULL default '0',
  `key` varchar(32) binary NOT NULL default '',
  `index` int(11) unsigned NOT NULL default '0',
  `value` varchar(254) NOT NULL default '0',
  PRIMARY KEY (`account_id`,`key`,`index`),
  KEY `account_id` (`account_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `mapreg` (
  `varname` varchar(32) binary NOT NULL,
  `index` int(11) unsigned NOT NULL default '0',
  `value` varchar(255) NOT NULL,
  PRIMARY KEY (`varname`,`index`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
