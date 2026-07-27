-- goAthena migration 000009 (down) — remove Wave 5 companion tables.
-- Reverse creation order to mirror the up migration's last-to-first drop semantics.

DROP TABLE IF EXISTS `pet`;
DROP TABLE IF EXISTS `mercenary_owner`;
DROP TABLE IF EXISTS `mercenary`;
DROP TABLE IF EXISTS `homunculus`;
DROP TABLE IF EXISTS `elemental`;
