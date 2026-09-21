-- Reverse of 000008_party.up.sql.
ALTER TABLE `char`
  DROP KEY `party_id`,
  DROP COLUMN `party_id`;
DROP TABLE IF EXISTS `party`;
