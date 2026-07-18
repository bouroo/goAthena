-- goAthena migration 000004 — ipbanlist compatibility alignment (D5).
-- INTENT: This migration is the ONE-TIME exception to the additive-only
-- schema policy (see .agents/plans/go-athena-emulator/decision-log.md,
-- D-001 / D-002). The legacy goAthena schema in 000002_identity.up.sql
-- declared `list` as varchar(40) and omitted the PRIMARY KEY, both of
-- which diverge from rAthena canonical (third_party/rathena/sql-files/
-- main.sql:758-764). This file converges goAthena onto the rAthena
-- canonical. No future ALTER/MODIFY/CHANGE/DROP against any rAthena
-- column is permitted.

ALTER TABLE `ipbanlist` MODIFY `list` varchar(15) NOT NULL default '';
ALTER TABLE `ipbanlist` ADD PRIMARY KEY (`list`, `btime`);
