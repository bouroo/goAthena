-- Wave 6: rAthena `storage` table (sql-files/main.sql, Thai Classic).
-- The player warehouse bounded context. One row per stored item; mirrors
-- the rAthena storage schema verbatim (column names + types + size rules).
-- Storage rows are addressable by the rAthena client via the warehouse
-- index (0..MAX_STORAGE) on ZC_STORE_NORMALITEMLIST / ZC_STORE_EQUIPMENTITEMLIST.
CREATE TABLE IF NOT EXISTS `storage` (
  `id` int unsigned NOT NULL AUTO_INCREMENT,
  `account_id` int unsigned NOT NULL DEFAULT 0,
  `nameid` int unsigned NOT NULL DEFAULT 0,
  `amount` int unsigned NOT NULL DEFAULT 0,
  `equip` int unsigned NOT NULL DEFAULT 0,
  `identify` smallint NOT NULL DEFAULT 0,
  `refine` tinyint unsigned NOT NULL DEFAULT 0,
  `attribute` tinyint unsigned NOT NULL DEFAULT 0,
  `card0` int unsigned NOT NULL DEFAULT 0,
  `card1` int unsigned NOT NULL DEFAULT 0,
  `card2` int unsigned NOT NULL DEFAULT 0,
  `card3` int unsigned NOT NULL DEFAULT 0,
  `option_id0` smallint NOT NULL DEFAULT 0,
  `option_val0` smallint NOT NULL DEFAULT 0,
  `option_parm0` tinyint NOT NULL DEFAULT 0,
  `option_id1` smallint NOT NULL DEFAULT 0,
  `option_val1` smallint NOT NULL DEFAULT 0,
  `option_parm1` tinyint NOT NULL DEFAULT 0,
  `option_id2` smallint NOT NULL DEFAULT 0,
  `option_val2` smallint NOT NULL DEFAULT 0,
  `option_parm2` tinyint NOT NULL DEFAULT 0,
  `option_id3` smallint NOT NULL DEFAULT 0,
  `option_val3` smallint NOT NULL DEFAULT 0,
  `option_parm3` tinyint NOT NULL DEFAULT 0,
  `option_id4` smallint NOT NULL DEFAULT 0,
  `option_val4` smallint NOT NULL DEFAULT 0,
  `option_parm4` tinyint NOT NULL DEFAULT 0,
  `expire_time` int unsigned NOT NULL DEFAULT 0,
  `favorite` tinyint unsigned NOT NULL DEFAULT 0,
  `bound` tinyint unsigned NOT NULL DEFAULT 0,
  `unique_id` bigint unsigned NOT NULL DEFAULT 0,
  `equip_switch` int unsigned NOT NULL DEFAULT 0,
  `enchantgrade` tinyint unsigned NOT NULL DEFAULT 0,
  KEY `account_id` (`account_id`),
  PRIMARY KEY (`id`)
) ENGINE=InnoDB;
