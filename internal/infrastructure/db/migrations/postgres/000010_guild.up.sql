-- Wave 10: rAthena guild tables (postgres variant). See the MariaDB file for
-- shape rationale: first-slice columns only; membership on `char.guild_id`;
-- `guild_member` join table deferred to the guild-exp wave.
CREATE TABLE IF NOT EXISTS "guild" (
  "guild_id" INT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  "name" varchar(24) NOT NULL DEFAULT '',
  "char_id" INT NOT NULL DEFAULT 0,
  "master" varchar(24) NOT NULL DEFAULT '',
  "guild_lv" SMALLINT NOT NULL DEFAULT 1,
  "max_member" SMALLINT NOT NULL DEFAULT 16,
  "average_lv" SMALLINT NOT NULL DEFAULT 1
);
CREATE INDEX IF NOT EXISTS "guild_char_id_idx" ON "guild" ("char_id");

ALTER TABLE "char" ADD COLUMN IF NOT EXISTS "guild_id" INT NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS "char_guild_id_idx" ON "char" ("guild_id");
