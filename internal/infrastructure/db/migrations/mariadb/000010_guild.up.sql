-- Wave 10: rAthena guild tables (sql-files/main.sql:460-481), first slice.
--
-- Shape note — this slice stores only the columns the guild core reads and
-- writes (name, master, level, caps). The rAthena dump's remaining columns
-- (mes1/mes2, exp/next_exp/skill_point, emblem_*, last_master_change) land
-- with their features (guild notice, exp donation, emblems). Membership rides
-- `char.guild_id` (added below); rAthena's `guild_member` join table (per-
-- member exp/position) lands with guild-exp donation, as its own wave.
--
-- ENGINE=InnoDB (not rAthena's MyISAM): the engine is invisible to
-- reads/writes, and InnoDB makes the transactional create-guild +
-- set-char.guild_id atomic.
CREATE TABLE IF NOT EXISTS `guild` (
  `guild_id` int unsigned NOT NULL AUTO_INCREMENT,
  `name` varchar(24) NOT NULL DEFAULT '',
  `char_id` int unsigned NOT NULL DEFAULT 0,
  `master` varchar(24) NOT NULL DEFAULT '',
  `guild_lv` tinyint unsigned NOT NULL DEFAULT 1,
  `max_member` tinyint unsigned NOT NULL DEFAULT 16,
  `average_lv` smallint unsigned NOT NULL DEFAULT 1,
  PRIMARY KEY (`guild_id`),
  KEY `char_id` (`char_id`)
) ENGINE=InnoDB;

-- Guild membership: same pattern as char.party_id (wave 8). 0 = "not in
-- guild" (rAthena's sentinel).
ALTER TABLE `char` ADD COLUMN `guild_id` int unsigned NOT NULL DEFAULT 0, ADD KEY `guild_id` (`guild_id`);
