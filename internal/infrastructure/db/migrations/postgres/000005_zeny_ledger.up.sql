-- Wave 5: zeny ledger. Append-only audit trail behind every balance change. The
-- ledger is the canonical record of "why the balance moved": every deduct or
-- credit writes one row carrying the signed delta, a reason, an optional peer
-- charID (trade/vending), and a map name (context). The sum of all rows for a
-- charID equals the current balance modulo the initial endowment.
--
-- Append-only: no UPDATE or DELETE statements in application code. Operators
-- who need a forensic timeline query this table; the economy service is the
-- only writer.
CREATE TABLE IF NOT EXISTS "zeny_ledger" (
  "id" bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  "account_id" integer NOT NULL DEFAULT 0,
  "char_id" integer NOT NULL,
  "amount" integer NOT NULL,
  "reason" varchar(32) NOT NULL DEFAULT 'unknown',
  "peer_char_id" integer NOT NULL DEFAULT 0,
  "map_name" varchar(16) NOT NULL DEFAULT '',
  "created_at" timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS "zeny_ledger_char_id_idx" ON "zeny_ledger" ("char_id");
CREATE INDEX IF NOT EXISTS "zeny_ledger_reason_idx" ON "zeny_ledger" ("reason");
CREATE INDEX IF NOT EXISTS "zeny_ledger_created_at_idx" ON "zeny_ledger" ("created_at");
