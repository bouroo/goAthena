-- goAthena migration 000007 (down) — remove Wave 3 guild / party / friends / memo tables.
-- Reverse creation order to mirror the up migration's last-to-first drop semantics.

DROP TABLE IF EXISTS `party_bookings`;
DROP TABLE IF EXISTS `party`;
DROP TABLE IF EXISTS `memo`;
DROP TABLE IF EXISTS `guild_storage_log`;
DROP TABLE IF EXISTS `guild_storage`;
DROP TABLE IF EXISTS `guild_skill`;
DROP TABLE IF EXISTS `guild_position`;
DROP TABLE IF EXISTS `guild_member`;
DROP TABLE IF EXISTS `guild_expulsion`;
DROP TABLE IF EXISTS `guild_castle`;
DROP TABLE IF EXISTS `guild_alliance`;
DROP TABLE IF EXISTS `guild`;
DROP TABLE IF EXISTS `friends`;
