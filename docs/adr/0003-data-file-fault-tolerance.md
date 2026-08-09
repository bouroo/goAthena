# ADR 0003: Tolerate (don't abort on) rAthena data-file quirks at load time

- **Status:** Accepted
- **Date:** 2026-08-09
- **Commit:** `d257fd5`

## Context

goAthena loads rAthena's authoritative game-data YAMLs (item_db, mob_db,
script corpora) directly from a `db_path` pointing at a real rAthena checkout.
Two classes of defect in those upstream files would, if treated strictly,
abort the whole server boot:

1. **Duplicate YAML mapping keys.** `yaml.v3` rejects duplicate keys when
   decoding into a struct, which would abort the **entire** item_db load and
   break drops, equip, and use-item resolution. rAthena's own loader is
   last-wins tolerant (`pkg/ro/itemdb/itemdb.go:146-158`, `:346-374`).
2. **Optional/missing data files.** The operator may not have a rAthena
   checkout pointed at `ZONE_DB_PATH`; mobs/items should still resolve (to
   0 DEF / no drops) so the server boots.

goAthena is pre-renewal, so it reads the pre-re DBs at
`<db_path>/pre-re/*.yml` where `db_path` is the operator's `ZONE_DB_PATH`
pointing at a rAthena checkout (`internal/modules/world/di.go:55-81`,
`internal/config/config.go:153`).

## Decision

**Load defensively, degrade gracefully.** The item_db loader decodes into a
raw `yaml.Node` tree (which preserves duplicates without error), then
collapses duplicate keys last-wins via `dedupeMappingKeys` before decoding
entries — matching rAthena's semantics
(`pkg/ro/itemdb/itemdb.go:146-158`, `:352-374`). A load failure in
`loadMobDB`/`loadItemDB` logs a WARN and returns an empty registry instead
of erroring boot (`internal/modules/world/di.go:55-81`).

## Consequences

- **Positive:** The server boots against a stock rAthena checkout without
  hand-editing upstream YAMLs; a bad or missing file degrades one subsystem
  rather than the whole process.
- **Negative:** Last-wins means a genuinely duplicated item entry silently
  shadows an earlier one; defects in upstream data are masked, not surfaced.
  The operator must read WARN logs to know a file failed to load.
- **Trade-off:** This is a correctness-vs-bootability trade that favors
  "server stays up" — appropriate for a game server but would be wrong for a
  financial ledger.

## References

- `pkg/ro/itemdb/itemdb.go:146-158`, `:346-374`
- `internal/modules/world/di.go:21-81`
- `internal/config/config.go:153` (`db_path` / `ZONE_DB_PATH`)
