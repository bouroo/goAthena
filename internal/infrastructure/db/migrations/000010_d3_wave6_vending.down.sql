-- goAthena migration 000010 (down) — remove Wave 6 vending/buyingstore tables.
-- Reverse creation order to mirror the up migration's last-to-first drop semantics.

DROP TABLE IF EXISTS `vendings`;
DROP TABLE IF EXISTS `vending_items`;
DROP TABLE IF EXISTS `buyingstores`;
DROP TABLE IF EXISTS `buyingstore_items`;
