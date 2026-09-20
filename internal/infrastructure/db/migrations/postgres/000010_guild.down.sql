-- Reverse of 000010_guild.up.sql (postgres).
DROP INDEX IF EXISTS "char_guild_id_idx";
ALTER TABLE "char" DROP COLUMN "guild_id";
DROP INDEX IF EXISTS "guild_char_id_idx";
DROP TABLE IF EXISTS "guild";
