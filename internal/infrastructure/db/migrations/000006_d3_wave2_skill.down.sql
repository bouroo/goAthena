-- goAthena migration 000006 (down) — remove Wave 2 skill / sc_data / hotkey tables.
-- Reverse creation order to mirror the up migration's last-to-first drop semantics.

DROP TABLE IF EXISTS `skillcooldown_mercenary`;
DROP TABLE IF EXISTS `skillcooldown_homunculus`;
DROP TABLE IF EXISTS `skill_homunculus`;
DROP TABLE IF EXISTS `skill`;
DROP TABLE IF EXISTS `skillcooldown`;
DROP TABLE IF EXISTS `sc_data`;
DROP TABLE IF EXISTS `hotkey`;
