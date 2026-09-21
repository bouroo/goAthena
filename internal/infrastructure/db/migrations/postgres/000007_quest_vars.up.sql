-- Wave 7: rAthena-style NPC quest variables (postgres variant).
-- Same shape as the MariaDB migration; engine-specific differences:
-- double-quoted names, no unsigned types, bigint for the timestamp column
-- (postgres has no auto-update timestamp; the engine sets it).
CREATE TABLE IF NOT EXISTS "quest" (
  "char_id" integer NOT NULL DEFAULT 0,
  "npc_name" varchar(24) NOT NULL DEFAULT '',
  "var_name" varchar(32) NOT NULL DEFAULT '',
  "value" varchar(255) NOT NULL DEFAULT '0',
  "updated_at" bigint NOT NULL DEFAULT 0,
  PRIMARY KEY ("char_id", "npc_name", "var_name")
);
CREATE INDEX IF NOT EXISTS "quest_char_id_idx" ON "quest" ("char_id");
CREATE INDEX IF NOT EXISTS "quest_npc_name_idx" ON "quest" ("npc_name");
