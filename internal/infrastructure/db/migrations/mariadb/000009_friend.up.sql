-- Wave 9: rAthena friend list (sql-files/main.sql:424-428).
--
-- Shape note — rAthena stores the friend list as bare (char_id, friend_id) ID
-- pairs, one row per direction, keyed by the composite PK. Names, account ids
-- and online flags are NOT stored here: the char-server resolves them by
-- joining `char` when it loads the list (mmo.hpp s_friend is runtime-only).
-- We mirror that exactly so an rAthena dump loads with no transform.
--
-- ENGINE=InnoDB (not rAthena's MyISAM): engine is invisible to reads/writes,
-- and InnoDB gives us the transaction that accept-friend (two-row insert) and
-- remove-friend (two-row delete) need to be atomic.
CREATE TABLE IF NOT EXISTS `friends` (
  `char_id` int unsigned NOT NULL DEFAULT 0,
  `friend_id` int unsigned NOT NULL DEFAULT 0,
  PRIMARY KEY (`char_id`, `friend_id`),
  KEY `friend_id` (`friend_id`)
) ENGINE=InnoDB;
