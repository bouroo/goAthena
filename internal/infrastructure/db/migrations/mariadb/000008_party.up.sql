-- Wave 8: rAthena party tables (sql-files/main.sql:971-982, Thai Classic).
--
-- Shape note — there is NO `party_member` table in rAthena. Membership is a
-- single column on `char` (`char.party_id`), and the leader is recorded on the
-- party row as an (account_id, char_id) pair (`leader_id`/`leader_char`). The
-- char-server reconstructs the roster with
--   SELECT ... FROM `char` WHERE `party_id` = ?
-- and derives `member.leader` by comparing each row against leader_id/leader_char
-- (src/char/int_party.cpp:237-250). We mirror that exactly so an rAthena dump
-- loads with no transform — the alternative (a join table) would not.
--
-- ENGINE=InnoDB (not rAthena's MyISAM): engine is invisible to reads/writes, and
-- InnoDB gives us the transaction that create-party + set-char.party_id needs to
-- be atomic.
CREATE TABLE IF NOT EXISTS `party` (
  `party_id` int unsigned NOT NULL AUTO_INCREMENT,
  `name` varchar(24) NOT NULL DEFAULT '',
  `exp` tinyint unsigned NOT NULL DEFAULT 0,
  `item` tinyint unsigned NOT NULL DEFAULT 0,
  `leader_id` int unsigned NOT NULL DEFAULT 0,
  `leader_char` int unsigned NOT NULL DEFAULT 0,
  PRIMARY KEY (`party_id`)
) ENGINE=InnoDB;

-- Wave 8: party membership. rAthena keeps this on `char`, so the column and its
-- lookup key are added here rather than in 000002_char — the party wave owns
-- them. `party_id` = 0 means "not in a party" (rAthena's sentinel).
ALTER TABLE `char`
  ADD COLUMN `party_id` int unsigned NOT NULL DEFAULT 0,
  ADD KEY `party_id` (`party_id`);
