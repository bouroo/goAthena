-- Learned skills: char_id → (skill_id, level). Multiple skills per char; primary
-- key prevents duplicate (char_id, skill_id) pairs. rAthena has no separate skill
-- table (skills are encoded in a blob column), but goAthena uses a normalised
-- layout so skill lookups are cheap and the table is portable.
CREATE TABLE IF NOT EXISTS "skill" (
  "char_id" integer NOT NULL,
  "skill_id" integer NOT NULL,
  "level" smallint NOT NULL DEFAULT 1,
  PRIMARY KEY ("char_id", "skill_id")
);
CREATE INDEX IF NOT EXISTS "skill_char_id_idx" ON "skill" ("char_id");
