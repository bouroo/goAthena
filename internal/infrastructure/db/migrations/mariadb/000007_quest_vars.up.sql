-- Wave 7: rAthena-style NPC quest variables (sql-files/main.sql quest table,
-- Thai Classic). The persistent storage behind NPC scripts that read or write
-- named variables per character: kill counters, reward flags, dialogue
-- branches. Keyed by (char_id, npc_name, var_name) with the value as a string
-- (numeric quest state is stored as decimal text, matching rAthena's quest
-- table; the engine parses it back to int64 on read).
--
-- The table is deliberately separate from `inventory` and `zeny_ledger`:
-- quest state is NPC-script-managed and not directly balance-bearing, so the
-- ledger/atomicity contract doesn't apply. Updates are last-writer-wins
-- (rAthena's quest table has no version column).
CREATE TABLE IF NOT EXISTS `quest` (
  `char_id` int unsigned NOT NULL DEFAULT 0,
  `npc_name` varchar(24) NOT NULL DEFAULT '',
  `var_name` varchar(32) NOT NULL DEFAULT '',
  `value` varchar(255) NOT NULL DEFAULT '0',
  `updated_at` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  KEY `char_id` (`char_id`),
  KEY `npc_name` (`npc_name`),
  PRIMARY KEY (`char_id`, `npc_name`, `var_name`)
) ENGINE=InnoDB;
