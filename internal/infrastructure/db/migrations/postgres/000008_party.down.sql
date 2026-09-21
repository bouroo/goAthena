-- Reverse of 000008_party.up.sql (postgres).
DROP INDEX IF EXISTS "char_party_id_idx";
ALTER TABLE "char" DROP COLUMN "party_id";
DROP TABLE IF EXISTS "party";
