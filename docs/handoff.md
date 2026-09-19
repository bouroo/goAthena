# goAthena — Handoff Ledger

> Living project ledger. Anchored to executable evidence (commit refs,
> file paths, gate exit codes). Updated as milestones land.
>
> Companion to `docs/roadmap.md`. Roadmap is the **plan of record**;
> this document is the **state of play** and the **work queue**.

---

## 0. Conventions

- **Status** uses three symbols:
  - ✅ **done** — verified end-to-end; L1 + L2 + (where applicable) L3 green; pushed.
  - 🟡 **partial** — first slice landed; remainder is open and listed.
  - 📋 **planned** — not started.
- **M-prefix** = milestone scope (from roadmap §5).
- **P-prefix** = phase (P0, P-scale, P-hard).
- **Push cadence** — each milestone ships on its own commit series on `develop`,
  rebased onto `origin/develop`, then `git push origin develop`.
- **Hard stop** — three failed verify cycles on the same item → stop and escalate.

---

## 1. Verifier gates (every phase)

| Gate | Tool | Pass criterion |
|---|---|---|
| L1 static | `task fmt` (gofumpt + goimports) + `task lint` (golangci-lint v2) | 0 issues |
| L1 vet | `task vet` (`go vet ./...`) | clean |
| L2 runtime | `task test-unit` (`go test -race -tags=unit ./internal/... ./pkg/...`) | all green; ≥60% coverage |
| L3 e2e | compose harness (MariaDB + Valkey + goathena + client) | login → char → map → at least one gameplay verb |
| L3 architecture | `arch_test.go` walks | intra-module direction enforced |

---

## 2. Milestone ledger

### M0 — Scaffold

| Item | Evidence | Status |
|---|---|---|
| Binary `cmd/goathena` boots | `go build ./...` green | ✅ |
| `/healthz` + `/readyz` | `internal/app/app.go` route handlers | ✅ |
| Migrations apply | `internal/infrastructure/db/migrations` | ✅ |
| Arch boundaries enforced | `internal/app/arch_test.go` + depguard | ✅ |
| `compose.yml` runs full stack | compose + Containerfile live | ✅ |

**Gaps to verify:** none observed. Audit walk on first pass.

---

### M1 — Account / Login

| Item | Evidence | Status |
|---|---|---|
| `:6900` login listener | `gateway/app/login.go` (221 LOC) | ✅ |
| Old-login handshake v55 | `gateway/app/login_integration_test.go` (132 LOC) | ✅ |
| Account auth + session keys | `modules/account/app/service.go` | ✅ |
| gnet TCP listener wired | `cmd/goathena/...` | ✅ |
| Panic-recovery hardened | `gateway/app/recover.go` + `shared/safe` (00af9aa) | ✅ |

**Gaps to verify:** `account/app/md5.go` only 10 LOC — confirm auth math matches rAthena.

---

### M2 — Character

| Item | Evidence | Status |
|---|---|---|
| Char CRUD | `character/app/service.go` (186 LOC) | ✅ |
| Char-select handshake | `gateway/app/char.go` (568 LOC) | ✅ |
| Session handoff via Valkey | `character/infra/valkey_session.go` | ✅ |
| Char TCP `:6121` | `gateway/app/char.go` | ✅ |
| Char deletion trio | commit `943373c` | ✅ |
| Reserve/accept/cancel e2e | `char_integration_test.go` | ✅ |
| GORM reserved-word handling | `gorm_integration_test.go` (commit `1e59f43`) | ✅ |

**Gaps to verify:** none observed.

---

### M3 — World core

| Item | Evidence | Status |
|---|---|---|
| Entity lifecycle | `world/app/world.go` (1097 LOC) | ✅ |
| AOI grid | `pkg/ro/aoi` | ✅ |
| 50 Hz tick loop | `world/app/world.go` + `app.go` tick | ✅ |
| Map-enter handshake | `gateway/app/map.go` + `world.go` EnterMap | ✅ |
| Map TCP `:5121` | `gateway/app/map.go` (964 LOC) | ✅ |
| Vital regen on tick | commit `ab2c0ae` | ✅ |
| Periodic checkpoint | commit `b2136a4` + `StartCheckpoint` | ✅ |
| Checkpoint panic-recovery | 00af9aa | ✅ |

**Gaps to verify:** none observed.

---

### M4 — Gateway ingress

| Item | Evidence | Status |
|---|---|---|
| Table-driven dispatch | `gateway/app/dispatch.go` (1812 LOC) | ✅ |
| CZ_ENTER / LoadEndAck | dispatch + map.go | ✅ |
| Movement | `gateway/app/dispatch.go` + `world.go` Move | ✅ |
| Variable-length dispatch | commit `d257fd5` | ✅ |
| Reactor panic-recovery | 00af9aa | ✅ |

**Gaps to verify:** walk `dispatch.go` for verbs not yet implemented.

---

### M5 — Inventory

| Item | Evidence | Status |
|---|---|---|
| Item-container aggregate | `inventory/domain/item.go` + `app/service.go` (55 LOC) | ✅ |
| LoadEndAck init burst | commit `c03a781` (init burst populated) | ✅ |
| Inventory GORM persistence | `inventory/infra/gorm.go` | ✅ |
| Inventory GORM e2e | `gorm_integration_test.go` | ✅ |
| Bag-grid re-sync after shop | commit `f14f435` | ✅ |
| Equipment system | commit `ea8c61f` + `world/app/equip.go` (157 LOC) | ✅ |

**Gaps to verify:** storage/warehouse **not yet wired** — listed under M9.

---

### M6 — Spawn / drops

| Item | Evidence | Status |
|---|---|---|
| Mob spawn | `world/app/spawn.go` (278 LOC) | ✅ |
| Floor items | `world/domain/flooritem.go` | ✅ |
| Drops on death | `world/app/combat.go` | ✅ |
| CZ_ITEM_PICKUP | `gateway/app/dispatch.go` | ✅ |
| Pickup vanish + loot sweep + respawn appear | commit `6969495` | ✅ |
| Mob respawn timer | `spawn.go` scheduleRespawn | ✅ |
| Mob respawn panic-recovery | 00af9aa | ✅ |

**Gaps to verify:** none observed.

---

### M7 — Combat

| Item | Evidence | Status |
|---|---|---|
| CombatService | `world/app/combat.go` (295 LOC) | ✅ |
| Melee NormalMelee pre-re | `pkg/ro/combat` | ✅ |
| CZ_ACTION_REQUEST | dispatch + combat.go | ✅ |
| HP reduction | combat service | ✅ |
| Element/size/crit/miss modifiers | commit `881f61e` + `pkg/ro/combat` | ✅ |
| Combat depth GORM isolation | commit `881f61e` | ✅ |
| Mob AI / mob→player combat | commit `cd8d09d` + `mobai.go` (356 LOC) | ✅ |
| Player death + respawn at save point | commit `26d82b8` | ✅ |
| Vital-state persistence | commit `33cf878` | ✅ |
| CZ_RESTART respawn button | commit `e0d326f` | ✅ |
| EXP-on-kill reward | commit `75d0971` | ✅ |
| Leveling | commit `f465d49` + `leveling.go` (156 LOC) | ✅ |
| Stat-point allocation | commit `5d09740` | ✅ |
| Skill-point spend | commit `eb3217b` | ✅ |

**Gaps to verify:** range/magic skills (M10 future).

---

### M8 — Economy

| Item | Evidence | Status |
|---|---|---|
| Zeny value object | `economy/domain/zeny.go` (51 LOC) | ✅ |
| EconomyService | `economy/app/service.go` (101 LOC) | ✅ |
| DeductZeny / CreditZeny | service.go | ✅ |
| GetZeny read-only path | service.go | ✅ |
| L1+L2 unit tests | service_test.go (115 LOC) + zeny_test.go (57 LOC) | ✅ |

**Remaining:** **transaction log + audit ledger** — the slice only balances an
in-memory Zeny and persists it; no record of why a movement happened. A
real ledger needs:

- `economy_domain.ZenyTransaction` (account, amount, reason, peer_char, ts).
- Append on every deduct/credit, immutable.
- A `LedgerRepository` port + GORM-backed impl with a `zeny_transaction` table
  (composable with the migration suite).
- A query port for ops (last N for char, totals by reason).

**Plan:** design + port + repo + tests + migration + push as M8-final.

---

### M9 — Commerce

| Item | Evidence | Status |
|---|---|---|
| Shop service (Buy/Sell) | `commerce/shop/app/service.go` (95 LOC) | ✅ |
| Shop catalog registry | `commerce/shop/domain/shop.go` | ✅ |
| Dev seed catalog | `commerce/shop/seed.go` | ✅ |
| Catalog populates from NPC shop scripts | commit `1403e6a` | ✅ |
| Shop + zeny + inventory integration | L1+L2 green | ✅ |
| Trade (P2P) state machine | commit `0bca8d3` + `world/app/trade.go` (495 LOC) | ✅ |
| Trade opcodes wired | `gateway/app/dispatch.go` | ✅ |
| Trade e2e | `world/app/trade_test.go` (565 LOC) | ✅ |
| Bag-grid re-sync after shop | commit `f14f435` | ✅ |

**Remaining:**

- **Vending** — player-owned vending machines: open shop, list items, browse,
  buy. rAthena opcodes: `CZ_OPEN_VENDING`, `CZ_VENDOR_*`, `ZC_PC_PURCHASE_ITEMLIST*`.
  Needs a `vending` bounded sub-context with a `VendingService`, a
  `VendingRepository` (ephemeral listings), and stock/inventory binding. Substantial.
- **Storage (warehouse)** — `CZ_REQ_OPENSTORE`, deposit/withdraw over
  inventory + zeny ports. Needs a `storage` bounded sub-context with a
  `StorageService` and a separate `storage` table per char.

**Plan:** design vending port + vending service + vending gateway wiring;
then storage service + storage table + migration. Both have L1+L2 tests.

---

### M10 — Content (script VM)

| Item | Evidence | Status |
|---|---|---|
| Script kernel (lexer/parser/compiler/VM) | `pkg/ro/script` (6,075 LOC across 19 files) | ✅ |
| Dialog builtins (mes/next/close/menu/input) | `pkg/ro/script/builtins.go` (232 LOC) | ✅ |
| Dialog bridge to gateway | `content/app/engine.go` (285 LOC) | ✅ |
| NPC click → ZC_SAY_DIALOG2 etc. | `gateway/app/map.go` + content engine | ✅ |
| L1+L2 + L3 dialog e2e | engine_test.go (251 LOC) + integration | ✅ |
| Item-use script verbs | commit `de38b74` (usable-item verb) | ✅ partial — flat heal + consume, no item-script exec |
| NPC script corpus seeding | commit `117a191` + `world/app/seed.go` (186 LOC) | ✅ |
| Shops from NPC scripts | commit `1403e6a` | ✅ |
| Warp portals from corpus | commit `8923241` | ✅ |
| Script-VM panic-recovery | 00af9aa | ✅ |

**Remaining (the bulk):** full rAthena `script.cpp` coverage. The 29k LOC C++
script VM carries roughly 600 builtins in the production engine. Currently
implemented (12): `mes`, `next`, `close`, `close2`, `end`, `set`, `warp`,
`percentheal`, `select`, `prompt`, `menu`, `input`.

**Scope ladder** (each rung is a commit; we ship as far as time + L2 permits):

1. **Rung A — Item scripts** (`getitem`, `delitem`, `countitem`, `equip`,
   `unequip`, `getitem2`): bridge inventory port to script via new builtins.
   Small, ships a usable script corpus subset (healer NPC sells potion, player
   buys, item grants heal over time).
2. **Rung B — Quest engine** (`set`, `if`, `getvariableofnpc`, quest-state
   storage): persistent NPC variables per player, kills counter, reward
   scripts. Larger.
3. **Rung C — Misc verbs** (`announce`, `mapannounce`, `getiteminfo`, `bonus`,
   `sc_start`, `sc_end`, `heal`): more content authors expect.
4. **Rung D — Monster/Account scripts**: NPC event blocks (`OnMyMobDead`,
   `OnInit`, `OnTouch`, `OnWhisperGlobal`); structural.
5. **Rung E — Operator primitives** (`getgmlevel`, `warp` variants,
   `pvpon`/`pvpoff`, `setmapflag`, `removemapflag`).

**Plan in this session:** ship Rung A (item-script builtins + a working
quest-trigger NPC); record remaining rungs as explicit follow-up tickets.

---

### M11 — Social

| Item | Evidence | Status |
|---|---|---|
| ChatService (whisper + public) | `social/app/chat.go` + `gateway/app/chat.go` (323 LOC) | ✅ |
| PlayerDirectory port | `social/domain/chat.go` | ✅ |
| CZ_GLOBAL_MESSAGE / ZC_NOTIFY_CHAT | commit `e3e46fc` | ✅ |
| CZ_WHISPER routing | commit `159e483` | ✅ |
| CZ_GETCHARNAMEREQUEST | commit `70f336b` | ✅ |
| Whisper ignore list | commit `1337f89` | ✅ |

**Remaining:**

- **Friend list** — `CZ_ADD_FRIENDS`, `CZ_DELETE_FRIENDS`, accept/reject, online
  notify. Persistent table.
- **Party** — create/leave/exp-share, `CZ_MAKE_GROUP`, `CZ_REQ_JOIN_GROUP`,
  `CZ_REQ_LEAVE_GROUP`, `ZC_PARTY_*`, `ZC_EXP_GROUPINFO_SHARING`, etc.
- **Guild** — create/join/leave/alliances/chat (`CZ_REQ_GUILD_MENU`,
  `CZ_GUILD_*`).
- **Mail** — send/receive/attachment (`CZ_MAIL_*`, `ZC_MAIL_*`).

**Plan in this session:** ship **party** end-to-end (the smallest grouping with
real gameplay consequence — exp share); record friend/guild/mail as
follow-up tickets.

---

### M12 — Transit

| Item | Evidence | Status |
|---|---|---|
| Warp in-zone (SetPosition + LeaveMap) | `transit/app/service.go` (42 LOC) | ✅ |
| Warp portals from corpus | commit `8923241` | ✅ |
| ZC_NPCACK_MAPMOVE wire | gateway dispatch | ✅ |

**Remaining:** **cross-zone handshake** — the multi-process case where the
target map runs on a different node (Agones fleet). Without the fleet the
monolith can also run multiple map shards, so a cross-zone handoff needs:

1. A `MapDirectory` port (name → instance addr).
2. A `Handoff` port (sign a session token the target zone can validate).
3. Wire the CZ_ENTER path to first resolve the map name → (zone, addr) and
   either (a) re-enter locally if the zone is this process, or (b) emit a
   `ZC_NOTIFY_TRANSFER`/`ZC_TRANSFER` redirect the client uses to reconnect.
4. `Agones` adapter is M13.

**Plan in this session:** ship the local cross-zone handoff (a) and the
transfer-redirect packet framing, with the directory port stub. M13 wires
the remote (b) path.

---

### M13 — Scale-out (NATS + Agones)

| Item | Evidence | Status |
|---|---|---|
| NATS in compose | `compose.yml` sidecar | ✅ partial — sidecar present, no `nats.Connect` in binary |
| Subject naming convention | not yet | 📋 |
| Versioned schema for events | not yet | 📋 |
| One module extracted as NATS service | not yet | 📋 |
| Agones Fleet | not yet | 📋 |

**Plan in this session:** wire `nats.Connect` in `composition.go`; define
subject taxonomy (`economy.zeny.*`, `social.party.*`, `transit.handoff.*`,
`world.entity.*`); extract **economy** as the first NATS-backed remote (small
port surface, hot path already validates the architecture); add an env-driven
local-vs-remote switch so CI stays green. Agones adapter is a follow-up.

---

### M14 — Hardening

| Item | Evidence | Status |
|---|---|---|
| Prometheus `/metrics` | commit `7d64573` | ✅ |
| Docker compose e2e | compose + Containerfile | ✅ |
| 36MB distroless image | Containerfile | ✅ |
| OTel tracing SDK | commit `4fcb6c4` (OTel SDK landed) | ✅ partial — SDK is in; full coverage audit needed |
| OTel span coverage across modules | partial | 📋 |
| Security review | not yet | 📋 |
| Load test harness | not yet | 📋 |

**Plan in this session:**

1. OTel span audit + per-module coverage matrix; add spans where missing.
2. Threat-model + security review pass (auth on every write, packet DB
   exhaustive coverage, header skip on unknown opcodes, replay protection).
3. Load harness: a `cmd/loadgen` binary that opens N TCP conns and runs a
   script (login → char → enter → sit → stand loop). Compose sidecar.

---

## 3. Commit / push cadence

| Stage | Action |
|---|---|
| Per milestone rung | `git add -p` → `git commit -m "feat(<module>): <rung>"` |
| After L1+L2+L3 | `git push origin develop` |
| Handoff update | update §2 of this file in the same commit as the work |

---

## 4. Open follow-up tickets (deferred beyond this session)

| Ticket | Priority | Notes |
|---|---|---|
| M8: full transaction log + audit | M | Small. Ship next session. |
| M9: vending | L | Substantial. |
| M9: storage/warehouse | M | Small once schema is in. |
| M10: Rung B–E | L | Multi-session. |
| M11: friend list | M | Persistent table. |
| M11: guild | L | Large. |
| M11: mail | L | Large. |
| M12: Agones fleet adapter | L | M13 prereq. |
| M13: Agones SDK | L | Architecture-defining. |
| M14: threat model | M | One-shot. |
| M14: load test harness | M | One-shot. |

---

## 5. Session log

| Date | Author | Commit | Note |
|---|---|---|---|
| 2026-09-20 | goAthena agent | `00af9aa` | WIP: panic-recovery hardening (safe pkg + gateway reactor guards) — committed + L1+L2 green |
