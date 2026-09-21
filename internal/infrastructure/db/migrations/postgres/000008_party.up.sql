-- Wave 8: rAthena party tables (postgres variant). See the MariaDB file for the
-- shape rationale: there is no `party_member` table — membership is
-- `char.party_id` and the leader is the `party.leader_id`/`leader_char` pair.
CREATE TABLE IF NOT EXISTS "party" (
  "party_id" integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  "name" varchar(24) NOT NULL DEFAULT '',
  "exp" smallint NOT NULL DEFAULT 0,
  "item" smallint NOT NULL DEFAULT 0,
  "leader_id" integer NOT NULL DEFAULT 0,
  "leader_char" integer NOT NULL DEFAULT 0
);

ALTER TABLE "char" ADD COLUMN "party_id" integer NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS "char_party_id_idx" ON "char" ("party_id");
