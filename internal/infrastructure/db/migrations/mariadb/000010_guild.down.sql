-- Reverse of 000010_guild.up.sql.
ALTER TABLE `char` DROP KEY `guild_id`;
ALTER TABLE `char` DROP COLUMN `guild_id`;
DROP TABLE IF EXISTS `guild`;
