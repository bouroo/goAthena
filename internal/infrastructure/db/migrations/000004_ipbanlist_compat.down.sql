-- goAthena migration 000004 (down) — inverse of ipbanlist compatibility.
-- Provided for completeness; DOWN is destructive (it widens `list` back
-- to varchar(40) and removes the composite PK, reverting to the legacy
-- goAthena defect that pre-dated the additive schema policy). DOWN is
-- not used in production.

ALTER TABLE `ipbanlist` DROP PRIMARY KEY;
ALTER TABLE `ipbanlist` MODIFY `list` varchar(40) NOT NULL default '';
