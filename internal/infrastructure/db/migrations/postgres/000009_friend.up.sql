-- Wave 9: rAthena friend list (postgres variant). See the MariaDB file for the
-- shape rationale: bare (char_id, friend_id) pairs, one row per direction,
-- everything else resolved by joining `char`.
CREATE TABLE IF NOT EXISTS "friends" (
  "char_id" integer NOT NULL DEFAULT 0,
  "friend_id" integer NOT NULL DEFAULT 0,
  PRIMARY KEY ("char_id", "friend_id")
);
CREATE INDEX IF NOT EXISTS "friends_friend_id_idx" ON "friends" ("friend_id");
