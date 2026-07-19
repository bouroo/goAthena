-- goAthena migration 000008 (down) — remove Wave 4 quest / mail / mail_attachments / auction tables.
-- Reverse creation order to mirror the up migration's last-to-first drop semantics.

DROP TABLE IF EXISTS `quest`;
DROP TABLE IF EXISTS `mail_attachments`;
DROP TABLE IF EXISTS `mail`;
DROP TABLE IF EXISTS `auction`;
