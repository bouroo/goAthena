-- goAthena migration 000004 (down) — inverse of ipbanlist compatibility.
-- Provided for completeness; DOWN is destructive (it widens `list` back
-- to varchar(40), removes the composite PK, and recreates the original
-- single-column `KEY list (list)` index so the schema is restored to
-- its exact pre-migration state). DOWN is not used in production.

ALTER TABLE `ipbanlist` DROP PRIMARY KEY;
ALTER TABLE `ipbanlist` MODIFY `list` varchar(40) NOT NULL default '';
ALTER TABLE `ipbanlist` ADD KEY `list` (`list`);
