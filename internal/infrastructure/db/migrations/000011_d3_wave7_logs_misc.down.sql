-- goAthena migration 000011 (down) — remove Wave 7 logs + misc tables.
-- Reverse creation order to mirror the up migration's last-to-first drop semantics.

DROP TABLE IF EXISTS `sales`;
DROP TABLE IF EXISTS `market`;
DROP TABLE IF EXISTS `db_roulette`;
DROP TABLE IF EXISTS `clan_alliance`;
DROP TABLE IF EXISTS `clan`;
DROP TABLE IF EXISTS `bonus_script`;
DROP TABLE IF EXISTS `barter`;
DROP TABLE IF EXISTS `achievement`;
DROP TABLE IF EXISTS `interlog`;
DROP TABLE IF EXISTS `charlog`;
