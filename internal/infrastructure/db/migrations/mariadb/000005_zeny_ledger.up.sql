-- Wave 5: zeny ledger. Append-only audit trail behind every balance change.
-- MariaDB version. Same shape as the postgres migration; engine-specific
-- differences: AUTO_INCREMENT instead of GENERATED IDENTITY, BACKQUOTED names,
-- unsigned ints, ENGINE=InnoDB.
CREATE TABLE IF NOT EXISTS `zeny_ledger` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `account_id` int unsigned NOT NULL DEFAULT 0,
  `char_id` int unsigned NOT NULL,
  `amount` int NOT NULL,
  `reason` varchar(32) NOT NULL DEFAULT 'unknown',
  `peer_char_id` int unsigned NOT NULL DEFAULT 0,
  `map_name` varchar(16) NOT NULL DEFAULT '',
  `created_at` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `char_id` (`char_id`),
  KEY `reason` (`reason`),
  KEY `created_at` (`created_at`)
) ENGINE=InnoDB;
