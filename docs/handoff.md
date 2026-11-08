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
- **Release cadence** — publish (CD: image build + trivy gate) fires only for
  `v*` tags whose commit is reachable from `origin/main`; the cd.yml guard job
  refuses anything else. To release: merge `develop` → `main`, then tag there.
- **Hard stop** — three failed verify cycles on the same item → stop and escalate.

---

## 1. Verifier gates (every phase)

| Gate | Tool | Pass criterion |
|---|---|---|
| L1 static | `task fmt-check` (gofumpt + goimports) + `task lint` (golangci-lint v2, pinned to the Go toolchain — see ci.yml) | 0 issues |
| L1 vet | `task vet` (`go vet ./...`) across tagless + `-tags=unit` + `-tags=integration` | clean |
| L1 scan-vuln | `task scan-vuln` (govulncheck — reachable call paths only) | 0 reachable |
| L1 scan-sec | `task scan-sec` (gosec minus the golangci-adjudicated rule classes — see the Taskfile target) | 0 findings |
| L2 runtime | `task test-unit` (`go test -race -tags=unit ./internal/... ./pkg/...`) | all green; ≥60% coverage |
| L3 integration | `task test-integration` (`go test -race -tags=integration ./...`) — testcontainers MariaDB/Postgres + live gnet gateway TCP suites | all green |
| L3 architecture | `arch_test.go` walks | intra-module direction enforced |

All of L1 + L2 + L3 run in the pre-push hook (`.githook`, `GATE_SKIP=<names>` to
override) and in CI; the same `task` targets back both so they cannot drift.

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

**Remaining:** ~~**transaction log + audit ledger** — the slice only balances an
in-memory Zeny and persists it; no record of why a movement happened. A
real ledger needs:~~ **done** in commit `8c87e57`:

- `economy_domain.ZenyTransaction` (account, amount, reason, peer_char, map, ts).
- Append on every deduct/credit (one row per successful movement).
- `LedgerRepository` port + GORM-backed impl with a `zeny_ledger` table
  (migration `000005_zeny_ledger`).
- Query ports for ops (`ListByChar`, `SumByChar`).
- Shop and trade wired with reason-tagged variants (`ReasonShopBuy`,
  `ReasonShopSell`, `ReasonTrade` + peer char).

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

**Storage first slice — done** in commit `1a2b848`:

- `commerce/storage/domain.StorageItem` + `StorageRepository` port
  (account-keyed, `MAX_STORAGE` ceiling at rAthena floor of 600).
- `commerce/storage/infra/{memory,gorm}.go` — stackable items merge into
  existing same-NameID row; equipment always inserts; merge-or-insert
  wrapped in a transaction.
- Migration `000006_storage.{up,down}.sql` for both MariaDB and Postgres
  (rAthena schema verbatim).
- `app.StorageService` + memory-repo unit tests (Add/Remove/Stacking/
  Equipment/StorageFull/Insufficient/InvalidAmount).
- GORM integration test (Add/Stack/Load/Remove/Equipment-separate-row/over-remove/
  not-found) using the shared testdb harness.
- composition.go registers the module; the storage service is resolvable
  from the DI injector at boot.

**Storage second slice (gateway wiring)** — done in commit `c33c242`:

- `pkg/ro/packet/storage.go` — packet codecs for `CZ_REQ_OPENSTORE2`
  (0x07e4), `CZ_CLOSE_STORE` (0x07e5), `CZ_MOVE_ITEM_TO_STORE2`
  (0x07e6), `CZ_MOVE_ITEM_TO_BODY2` (0x07e7), `ZC_STORE_NORMALITEMLIST`
  (0x07e9), `ZC_STORE_EQUIPMENTITEMLIST` (0x07ea),
  `ZC_STOREITEMLISTRESULT` (0x07eb). `ZC_ACCEPT_ENTER2` (0x07e3) header
  defined but not yet emitted (the init lists carry everything the
  client needs; the ack is informational).
- `world/app/storage.go` — the cross-context orchestrator. Bag↔warehouse
  moves with distinct sentinels (ErrStorageIndexOutOfRange /
  ErrStorageEquipped / ErrStorageInsufficient / ErrStorageRowMissing /
  ErrStorageFull) so the gateway maps each to a ZC_STOREITEMLISTRESULT
  byte without string parsing.
- `world/app/storage_test.go` — 9 orchestrator unit tests (stackable
  move, warehouse-side merge, withdraw, index-out-of-range, equipped
  rejection, insufficient amount, warehouse-full, load warehouse).
- `gateway/app/dispatch.go` — 4 new handlers
  (handleReqOpenStore2, handleCloseStore, handleMoveItemToStore2,
  handleMoveItemToBody2) + writeStorageLists (init-burst encoder) +
  writeStorageItemListResult (ack). The dispatch table registers the
  four CZ opcodes with their fixed sizes.
- `gateway/app/map.go` — SetStorage setter for DI-root injection;
  nil-tolerant so harnesses without a storage service still build.
- `gateway/di.go` — SetStorage wired via the optional-resolve helper.
- `world/di.go` — StorageService provider registered alongside
  TradeService (same Register, same injector).
- L1+L2 green (fmt + lint + vet + race tests).

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
| Rung B quest engine | commit `b7f8ab7` (persistent NPC vars) | ✅ |
| Rung C misc verbs | `heal`/`announce`/`mapannounce`/`getiteminfo` + `script/constants.go` (this commit) | ✅ |

**Remaining (the bulk):** full rAthena `script.cpp` coverage. The 29k LOC C++
script VM carries roughly 600 builtins in the production engine. Currently
implemented (21): `mes`, `next`, `close`, `close2`, `end`, `set`, `warp`,
`percentheal`, `select`, `prompt`, `menu`, `input`, `getitem`, `getitem2`,
`delitem`, `countitem`, `equip`, `unequip`, `getvariableofnpc`, `setquestvar`,
plus Rung C's `heal`, `announce`, `mapannounce`, `getiteminfo`. Item-script
Rung A landed in commit `556a8ee`.

Rung D (monster/event scripts) and Rung E (operator primitives) are deferred —
each is its own commit-sized effort. `bonus`/`sc_start`/`sc_end` were dropped
from Rung C's scope: both need subsystems that do not exist yet (a persistent
stat-bonus aggregate over equipped items, and a status-effect registry with
tick/expiry), so they are their own rungs rather than one builtin each.

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

**Rung B — Quest engine** — done in commit `b7f8ab7`:

- `internal/modules/content/quest/{domain,app,infra}` new bounded context.
  `QuestVar` mirrors the rAthena `quest` table (sql-files/main.sql
  quest.sql) keyed `(char_id, npc_name, var_name)`. `QuestService`
  exposes `GetVar / SetVar / GetVarOfNPC / ListByChar` with name-length
  validation against the migration VARCHAR bounds (24 / 32 bytes) and
  decimal↔int64 coercion at the boundary.
- Migration `000007_quest_vars.{up,down}.sql` for both MariaDB and
  Postgres. GORM upsert via `clause.OnConflict` (postgres
  ON CONFLICT DO UPDATE; mariadb emits the matching ON DUPLICATE KEY
  UPDATE). Last-writer-wins, matching rAthena's quest table.
- `pkg/ro/script/builtins.go` — two new builtins:
  - `getvariableofnpc(npcName, varName)`: reads another NPC's
    persistent variable for the dialog's player. rAthena
    script.cpp `buildin_getvariableofnpc`.
  - `setquestvar(npcName, varName, value)`: persists a value with
    explicit NPC-scope. rAthena's `set` has implicit script-variable
    mirrors; we expose persistence explicitly so the script author is
    never surprised by an implicit DB write.
- `script.Host` interface gains `GetQuestVar / SetQuestVar` methods.
  `ScriptHost` implements them via the content-domain `ScriptQuest` port
  (DI-resolved; nil-tolerant).
- `pkg/ro/script/vm_test.go` FakeHost extended with `questVars` map +
  `questSetErr`; 3 new VM tests cover `getvariableofnpc` / `setquestvar`
  / short-arg safety.
- L1+L2 green (fmt + lint + vet + race tests).

**Rung C (this commit)** — the verbs content authors use outside dialogs:
`heal`, `announce`, `mapannounce`, `getiteminfo`, plus the script-constant table
those and every future builtin need.

- `pkg/ro/script/constants.go` — a compile-time constant table resolved by the
  compiler (rAthena resolves constants while parsing, script.cpp:2315
  `script_get_constant`; names match case-insensitively like rAthena's
  `add_str`/`strcasecmp` string table). Without it `bc_map`/`ITEMINFO_TYPE`
  compiled to a variable read and silently answered 0. The table is scoped to
  the constants implemented builtins take (`BC_*`, `ITEMINFO_*`, `IT_*`,
  `FW_*`) and is generated-from-upstream-shaped when breadth matters.
- `heal(hp, sp)`: absolute restore via `world.AddVitals`, clamped to [0, max]
  (rAthena `status_heal`, script.cpp:6007). The script-side int64 saturates to
  int32 (rAthena `cap_value(hhp, INT_MIN, INT_MAX)`), so an overflowing heal
  cannot wrap into damage.
- `announce(text, flag)` / `mapannounce(map, text, flag)`: the audience is
  resolved in `world` (it owns the entity registry and AOI grids) over the
  `BC_*` target bits — `BC_SELF`/`BC_MAP`/`BC_AREA`/`BC_ALL`, with `BC_NPC`
  choosing the NPC as the source instead of the dialog's player (rAthena
  script.cpp:11957 / :12028). `world.OnAnnounce` hands the recipient char ids to
  the gateway, which encodes one `ZC_BROADCAST` and writes it to each live
  connection — the content module never touches connections.
- `ZC_BROADCAST` (0x009a) codec: `[2:cmd][2:packetLength][prefix+text+NUL]`.
  The colour has no wire field — `clif_broadcast` (clif.cpp:6725) prefixes the
  text with `blue` (BC_BLUE) or `ssss` (BC_WOE), which the encoder reproduces;
  an empty message writes no frame (clif.cpp:6728). Packet DB 149 → **150**.
- `getiteminfo(item, code)`: `item` is a numeric name id or an AegisName
  (rAthena dispatches on the argument type, script.cpp:14766). The columns come
  from new `itemdb.ItemEntry.Info` (+ `Info*` codes), which answers the YAML
  scalars and the load-time defaults rAthena fills in — Sell = Buy/2
  (itemdb.cpp:1188), EquipLevelMax = MAX_LEVEL, Gender = SEX_BOTH, and the
  W_*/AMMO_*/CARD_* subtype resolution (itemdb.cpp:165-190). An unknown item or
  column answers -1; `ITEMINFO_AEGISNAME` is the one string column.
- Evidence: kernel builtin tests in `pkg/ro/script/vm_test.go` (flag constants
  resolve, sign survives, both item argument shapes), `itemdb` column tests,
  content-adapter tests, world audience tests (`announce_test.go`: self/map/
  area/all + non-PC anchor + unknown map + flat heal clamp), and L3 over real
  TCP (`TestMap_AnnounceReachesClient`, `..._BluePrefixReachesClient`,
  `..._AnnounceMapReachesMapOnly`). L1 (fmt/lint/vet/scans) + L2 (race) green.

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
| Friend list (add/reply/remove + online toggles) | `social/friend/*` + `gateway/app/friend.go` + migration `000009_friend` | ✅ |
| Party | `social/party/*` + `gateway/app/party.go` + migration `000008_party` | ✅ |
| Guild (create/invite/reply/leave/ban/break/chat + menuinterface, LoadEndAck burst) | `pkg/ro/packet/guild.go` + `social/guild/*` + `gateway/app/guild.go` + migration `000010_guild` | ✅ |
| Mail (RODEX: send with zeny/item attachments, inbox, read, delete, collect, recipient check) | `pkg/ro/packet/mail.go` + `social/mail/*` + `gateway/app/mail.go` + migration `000011_mail` | ✅ |

**Remaining:**

- *(none for the M11 core)*

**Plan in this session:** ✅ shipped — party end-to-end (`51e26f8`), friend
list end-to-end (`06fb692`), guild first slice end-to-end (`767c283`), mail
end-to-end (`375aa87`). Guild deferred: alliances, positions/skills,
exp donation, emblem, storage (needs M9 guild storage). Mail deferred:
account/returned inbox tabs + expiry cron, random options/enchantgrade on
attachment rows (upgrades with the inventory row-preserving insert).
---

### M12 — Transit

| Item | Evidence | Status |
|---|---|---|
| Warp in-zone (SetPosition + LeaveMap) | `transit/app/service.go` (42 LOC) | ✅ |
| Warp portals from corpus | commit `8923241` | ✅ |
| ZC_NPCACK_MAPMOVE wire | gateway dispatch | ✅ |

**Remaining:** **remote cross-zone handshake** — the target map runs on a
different node (Agones fleet). The v0 slice landed (`67b4979`): a
`transit/domain.MapDirectory` port (`Resolve` map → `Zone{IPv4, Port}`) with
the single-zone `LocalDirectory` in production wiring (`transit.
RegisterMapDirectory`), a `ZC_NPCACK_SERVERMOVE` (0x0ac7, 156B,
`PACKET_ZC_NPCACK_SERVERMOVE` at PACKETVER ≥ 20170315 — the ledger's earlier
"ZC_NOTIFY_TRANSFER" guess has no rAthena counterpart) codec + packet-DB
entry, and the gateway/content warp paths branch local-vs-remote on the
resolved zone: remote persists the destination, `WorldService.
LeaveRemoteZone` tears the player out of this zone (offline row stamped at
the destination cell so the remote zone's `EnterMap` loads it), and the
client is redirected to the zone ip:port. World/cross-process validation of
the reconnecting client already rides the shared Valkey session (CZ_ENTER's
`GetSession`+`LoginID1` check), so a separate Handoff token port adds no
security the shared session does not already provide — M13 drops in a fleet
directory (remote = real address) and keeps the client-side seam unchanged.

1. ~~A `MapDirectory` port (name → instance addr).~~ ✅ v0 this commit
2. ~~A `Handoff` port~~ — skipped: the shared Valkey session is the
   cross-zone client credential (same check as same-zone CZ_ENTER).
3. ~~Wire the CZ_ENTER/warp path to resolve map → (zone, addr).~~ ✅
   gateway `relocateThroughPortal` + content `ScriptHost.Warp`.
4. `Agones` fleet directory providing remote addresses — M13.

---

### M13 — Scale-out (NATS + Agones)

| Item | Evidence | Status |
|---|---|---|
| NATS in compose | `compose.yml` sidecar | ✅ partial — sidecar present; binary dials on demand (`natsinfra.New`) |
| Subject naming convention | `economy/remote` (`goathena.<module>.v<wire>.<verb>`) | ✅ v0 taxonomy |
| Versioned schema for events | subject `v0` suffix = wire-version contract (add field = compatible; remove/retype = bump verb) | ✅ |
| One module extracted as NATS service | economy: `economy.Service` interface + `remote.Proxy`/`remote.Server` + `goathena serve-economy` host | ✅ first extraction |
| Agones Fleet manifests | `deploy/k3s/base/zone-fleet.yaml` + `deploy/k3s/base/single/gameserver.yaml` + `transit/agones/fleet_manifest_test.go` | ✅ — two-Fleet manifest (prontera/geffen map sets) + a single-shard GameServer; `Dynamic` port policy (Agones picks an open host port and the shard advertises the one it was allocated — a `Static` policy pinned the host port and allowed one shard per node); `self_name` read from the pod label `agones.dev/gameserver` via the downward API; probes on `/healthz`+`/readyz`; the test decodes each document with the Agones API's own YAML decoder and runs `Validate` + `ApplyDefaults`, asserting the `game` port name, the port policy and the `goathena.dev/maps` annotation the directory reads (it caught the missing `spec.strategy.type`, and the policy regression when it was flipped back to `Static`). The manifests live under `deploy/k3s/` — the tree that is actually applied; the older `deploy/agones/` tree was removed rather than left to drift. |
| Fleet `MapDirectory` | `transit/agones` + `transit/static` | ✅ — `zone.directory.mode` `local\|agones\|static`; agones resolves Ready GameServers (maps CSV annotation `goathena.dev/maps` or `goathena.dev/map-<name>` labels, `game` port) via in-cluster creds or KUBECONFIG (Swarm path); static parses `routes` map→host:port; fatal boot error on non-local dial failure |
| Fleet cross-node admission | ✅ done | The shard advertises the address Agones assigned it. `internal/infrastructure/agones/allocation.go` reads the allocation from the sidecar (`GetGameServer`) at boot — the node address and the assigned host port — and `gateway.MapEndpointResolver` carries it into the char server's handshake. The port policy is **Dynamic**, so Agones picks an open host port and a node can carry several shards; a Static policy pinned the host port to the container port and allowed one shard per node. The advertised address is runtime state: the pod's IP is assigned after the manifest is written, so no env var can express it. |
| Tier 2 — small self-hosted fleet | `deploy/tier2/compose.yml` + `scripts/verify-tier2.sh` + `cmd/charselect` | ✅ — the middle rung of the ladder: two goathena nodes over one shared Postgres/Valkey/NATS, each serving a disjoint set of maps, finding each other through a static route table (`zone.directory.mode=static`: each node names the maps served by *another* node, and that node's address; a map with no route is served by this process, so the table is the fleet minus one and no node redirects a client to itself). The route table reaches the client through the per-map address handoff — the char server resolves the endpoint for the character's *own* map. `task verify-tier2` proves both nodes carry this tree's commit stamp, both are healthy, a login is accepted through the shared session store, and **a character on a remote map is handed the other node's address** (the real handshake, driven by `cmd/charselect`). |
| Tier 3 K8s deployment | `deploy/k3s/` + `scripts/verify-k3s.sh` + `scripts/crdschema/` | ✅ — the whole tier: namespace, Secret, ConfigMap, the client-facing and control-plane Services, Postgres (with its volume), Valkey, NATS, the OTel collector, the per-map-set Fleets, the `FleetAutoscaler`s, and a TCP ingress; dev, prod and single overlays. Every resource is validated against the real API schema (`kubeconform -strict`), with the Agones CRD schemas extracted from the Agones release manifest because the built-in catalog does not carry Agones. The overlays: dev 21, prod 24, single 17 — all valid, in Linux and macOS alike. |
| Tier 1 compose verification | `task verify-tier1` + `task stack-up` | ✅ — proves the running container carries *this tree's* commit stamp, that `/healthz` `/readyz` `/version` `/metrics` all answer 200, and that a login is accepted. The check exists because `docker compose up` reuses the last image: a stale image served an older commit whose `/metrics` 404'd and refused every login while `/healthz` stayed green. The stack looked up and the app was not the app. `task stack-up` rebuilds with `--no-cache`. |
| The autoscaler scales on the players | `internal/infrastructure/agones/counter.go` + `internal/app/counter.go` + `WorldService.OnlinePlayerCount` | ✅ — the shard publishes its connected-player count to the sidecar every 10s (the beta SDK's `UpdateCounter`); Agones aggregates it across the fleet; the `FleetAutoscaler`'s **Counter** policy sizes the fleet by it (aggregate capacity covers the aggregate players, 10% spare). The count is the world's live player registry, and it counts player characters only — a mob is not a player. The `Buffer` and `Schedule` policies stay alongside it: they cover the load the counter cannot see, since a shard that is up but not yet loaded still has to exist before its first player arrives. The counter's name is a contract between three places (the Fleet's `spec.template.spec.counters.players`, the name the shard publishes, and the policy's `key`), and the manifest test asserts all three agree — a typo in any one leaves the autoscaler scaling on a counter that does not exist. |

**Plan in this session:** wire `nats.Connect` in `composition.go`; define
subject taxonomy (`economy.zeny.*`, `social.party.*`, `transit.handoff.*`,
`world.entity.*`); extract **economy** as the first NATS-backed remote (small
port surface, hot path already validates the architecture); add an env-driven
local-vs-remote switch so CI stays green. Agones adapter is a follow-up.

**Open (the advertised address landed; these remain):**

1. ~~**Advertised map address from the Agones allocation.**~~ ✅ closed — the
   shard reads the allocation at boot (`GetGameServer`) and advertises the
   node address and the assigned port; the port policy is `Dynamic`, so a node
   can carry several shards.
2. **Sharding keys** — the NATS subjects are not partitioned by zone yet.
3. **Further module extractions** over NATS (social/mail are the next-largest
   port surfaces after economy).
4. ~~**A player-count signal for the autoscaler.**~~ ✅ closed — the shard
   publishes its connected-player count to the sidecar every 10s
   (`internal/infrastructure/agones/counter.go`, the beta SDK's
   `UpdateCounter`), Agones aggregates it across the fleet, and the
   `FleetAutoscaler`'s **Counter** policy sizes the fleet by it: the
   aggregate capacity covers the aggregate players, with 10% spare. The
   count comes from the world's live player registry
   (`WorldService.OnlinePlayerCount`), which counts player characters
   only — a mob is not a player. The Buffer and Schedule policies stay
   alongside it, because they cover what the counter cannot see: a shard
   that is up but not yet loaded still has to exist before its first
   player arrives, and a cold start takes longer than a report interval.
5. **The advertised address for a public deployment.** The shard advertises
   the address Agones assigned it, which is the node's — reachable from
   inside the cluster, and from the internet only if the node's address is
   publicly routable. The prod overlay still sets
   `GATEWAY_CHAR_HOST`/`GATEWAY_MAP_HOST` to a placeholder, and the operator
   must replace it with the ingress's external address for a client outside
   the cluster. This is item 1 seen from the deployment side: the handshake
   carries an IPv4, so a cluster-internal address cannot be what a public
   client is handed.

6. ~~**The released image carries no build stamp.**~~ ✅ closed in
   `v0.1.0-beta.20` — `cd.yml`'s build step passes `build-args`
   (`VERSION`/`COMMIT`/`BUILD_TIME`), so a published image reports the
   commit it was built from at `/version` instead of the Containerfile's
   `ARG` defaults. `VERSION` is the tag without its `v` (`0.1.0-beta.20`),
   which is what the metadata-action's semver `{{version}}` yields — the
   same form as the image's own tag.

7. ~~**The published image is amd64 only.**~~ ✅ closed in
   `v0.1.0-beta.20` — the build step gained `platforms:
   linux/amd64,linux/arm64` and the runner gained
   `docker/setup-qemu-action`, so the arm64 half is built under
   emulation. An arm64 node pulls the same tag. The cost is the
   emulated build's time, which is why the publish job's timeout went
   from 10 to 25 minutes.

---

### M14 — Hardening

| Item | Evidence | Status |
|---|---|---|
| Prometheus `/metrics` | commit `7d64573` | ✅ |
| Docker compose e2e | compose + Containerfile | ✅ |
| 36MB distroless image | Containerfile | ✅ |
| OTel tracing SDK | commit `4fcb6c4` | ✅ — SDK + OTLP exporter wired; the global W3C propagator is now set too (without it the injected traceparent is silently dropped) |
| OTel span coverage | `internal/shared/traces`, gateway dispatch, `economy/remote` | ✅ — instrumented at process boundaries only: one span per decoded client frame (map + char, labelled with the rAthena packet name from the packet DB), one per login attempt, and a producer/consumer span pair around each NATS request in `economy/remote` (traceparent rides the message headers, so a zone's frame trace continues into the economy host's DB work). The 50 Hz world tick and the AOI broadcast fan-out are deliberately not instrumented — a span per loop step would cost more than it measures. |
| OTel abuse signal (F-08 item 3) | `internal/shared/metrics` | ✅ — `goathena_gateway_unknown_opcodes_total{listener,known}`; `known="false"` is the probe/fuzz signal (a playing client never sends an opcode the DB does not define), `known="true"` is the unwired-verb coverage signal (drop/trade/skill today). |
| Login-load baseline | `cmd/loadgen` + `task loadtest`, `docs/loadtest-baseline.md` (`28af5c3`) | ✅ — ≈1000 logins/s zero-error; limiter shape verified |
| Security review | F-01..F-07 closed | ✅ — see `docs/security-audit.md` |

**Landed this session:** span instrumentation (`internal/shared/traces`), the
W3C propagator registration, and the unknown-opcode counter. Remaining M14
items are all closed or tracked elsewhere; the module coverage matrix is the
table above.

**Deliberately out of scope:** tracing the 50 Hz world tick, the AOI broadcast
fan-out, or individual entity mutations. Per-frame spans on those would swamp
the exporter and cost more than the loop they measure; the frame span already
brackets them, so a slow tick shows up as a slow frame.

### M15 — The Thai fork as the primary target

| Item | Evidence | Status |
|---|---|---|
| Source pack as the protocol authority | `pkg/ro/protocol`, `pkg/ro/packet/resolve.go`, [ADR 0004](adr/0004-source-pack-is-the-protocol-authority.md) | ✅ — every opcode and wire length resolves from the installed source pack at boot; nothing is a fork-dependent constant. The port of the Thai fork onto `develop` found ≈15 wire values the branch had hand-derived wrongly (frame lengths, field offsets, and opcodes), all corrected against the source's own tables. |
| Source-aware generator | `cmd/protocolgen` (`-source thailand|upstream`), `pkg/ro/protocol/generated/{thailand,upstream}/` | ✅ — the generator compiles the source's registration headers and emits the resolved table and the registration order, per source pack. |
| Operator-selected source pack | `client_source` / `client_variant` in `config.yaml` + `ZONE_CLIENT_SOURCE` / `ZONE_CLIENT_VARIANT` | ✅ — the boot installs the set the configuration names (`cmd/goathena`'s `installProtocol`). |
| Reference data follows the source pack | `internal/config`'s `resolveSourcePackPaths` | ✅ — an unset `db_path` / `script_path` points at the selected source pack's `db/` and `npc/`; an explicit path wins. |
| Thai-only schema | `internal/infrastructure/db/migrations/*/000012_vip_storage.*.sql` | ✅ — the fork's `vip_storage` table, for both drivers. |
| Both script corpora compile | `internal/modules/content/corpus_integration_test.go` | ✅ — the fork's 1209 scripts and upstream's 1138 both compile (77/72 dialog scripts, 9308/9304 spawns). |
| The warehouse speaks the source's protocol | `pkg/ro/packet/storage.go`, `internal/modules/gateway/app/dispatch.go` | ✅ — the source's kafra moves (0x0364 / 0x0365) and close (0x0193), the source's item-list packets (0xb09 / 0xb39 with the invType byte), and the source's delete/count notices. The branch had invented a 0x07e3-0x07eb range the source does not have. |

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
| M9: storage/warehouse | ✅ done | Service+schema `1a2b848` + gateway wiring `c33c242`. Guild storage deferred to M11. |
| M10: Rung D–E | L | Rung A `556a8ee`; Rung B `b7f8ab7`; Rung C (this commit: `heal`/`announce`/`mapannounce`/`getiteminfo` + constant table). Rungs D–E queued. |
| M10: `bonus`/`sc_start`/`sc_end` | L | Not a builtin-sized task: needs a persistent stat-bonus aggregate over equips and a status-effect registry (tick/expiry). Own rungs. |
| M11: friend list | ✅ done | Wire codecs + dispatch + `friends` table + online toggles (`gateway/app/friend.go`, migration `000009_friend`). |
| M11: guild | ✅ done | First slice end-to-end: codecs + service + gateway + `000010_guild`; alliances/positions/skills/exp/emblem deferred. |
| M11: mail | ✅ done | RODEX end-to-end: codecs + service + gateway + `000011_mail` (`375aa87`); account/returned tabs + expiry cron deferred. |
| M12: Agones fleet directory | ✅ done | `transit/agones` + `transit/static` + `zone.directory.mode` switch (this session); fleet YAML manifests remain M13. |
| M12: cross-zone redirect v0 | ✅ done | Directory port + SERVERMOVE framing + warp-path branch (`67b4979`); Agones fleet directory = M13 prereq. |
| M13: Agones SDK | ✅ done | Sidecar lifecycle (`internal/infrastructure/agones`) + fleet directory (`transit/agones`) + fleet/GameServer manifests (`deploy/k3s/`) + the advertised allocation + the player-count counter. The `deploy/agones/` tree was deleted; `deploy/k3s/` is the one tree. |
| M13: release image carries no build stamp | ✅ done | `cd.yml` passes `build-args`; a published image reports its commit at `/version` (from `v0.1.0-beta.20`). |
| M13: release image is amd64 only | ✅ done | The build gained `platforms: linux/amd64,linux/arm64` + QEMU (from `v0.1.0-beta.20`). |
| M14: threat model | M | One-shot. |
| M14: load test harness | ✅ done | `cmd/loadgen` (wire-correct at 20250604) + `task loadtest` + recorded baseline (`28af5c3`). |
| M14 F-07 frame cap | ✅ done | Packet DB `MaxLength` + gateway close-on-oversize (this commit). |
| M13 Agones adapter | ✅ done | Sidecar lifecycle + fleet directory + manifests + the advertised map address from the allocation (SDK `GetGameServer`) + the player-count counter + the per-map address handoff. The sharding keys (which NATS subject a zone's traffic belongs on) remain — see § M13 Open. |
| M13 economy over NATS | ✅ done | `economy.Service` seam + `remote.Proxy`/`Server` + `serve-economy` host; sharding keys remain. |
| Release pipeline hardening | ✅ done | Deps cleared the trivy HIGH gate (`v0.1.0-beta.6`+); gosec/govulncheck in hook+CI (`5503b26`); tags gated main (`6a93c9c`); auto GitHub Release (`8af706e`); all live-verified through `v0.1.0-beta.8`. **The gate also fired for real**: `v0.1.0-beta.9`'s scan failed on 5 HIGH advisories the local govulncheck gate does not see (govulncheck reports only reachable vulnerabilities; the release scan reads the whole shipped binary). Fixed in `500fcf6` (golang:1.27.3, x/net v0.62.0, module Go directive 1.27.3) and released as `v0.1.0-beta.10`. **`latest` was dead**: cd.yml gates the `latest` tag on `is_default_branch`, but cd.yml fires on tag pushes and a tag push is never on the default branch — so `latest` was never published and stayed pinned to a stale image carrying 16 HIGH advisories. Fixed in `f8cfe80` (the rule now tracks the newest release unconditionally) plus a manual prune workflow (`.github/workflows/prune-image.yml`), because deleting a package version needs `packages: delete`, which only a workflow's own `GITHUB_TOKEN` has. | The registry was then pruned: every tag from beta.1 to beta.9 and every SHA-tagged build carried 8-20 HIGH and 1 CRITICAL advisory (the publish job only ever adds tags, so they accumulated), and only `latest`, `0.1.0-beta.10` and `0.1.0-beta.11` remain, all three scanning clean. **Released**: `v0.1.0-beta.11`, guard/publish/scan/release all green, prerelease live at https://github.com/bouroo/goAthena/releases/tag/v0.1.0-beta.11. **The Go bump broke CI's lint job** and the comment above the linter pin says exactly how: golangci-lint embeds its own type-checker, so a linter built against an older Go cannot read the newer stdlib's export data — v2.13.2 (built with go1.27.0) fails on Go 1.27.3 with `export data version 5 is greater than maximum supported version 4`, naming no project file. Fixed in `f1a4b18` (pin to v2.15.0; verified v2.14.0 and v2.15.0 read it, v2.13.2 does not). CI is green again on every job.

---

## 5. Session log

| Date | Author | Commit | Note |
|---|---|---|---|
| 2026-09-20 | goAthena agent | `00af9aa` | WIP: panic-recovery hardening (safe pkg + gateway reactor guards) — committed + L1+L2 green |
| 2026-09-20 | goAthena agent | `d25d14c` | docs: add handoff ledger |
| 2026-09-20 | goAthena agent | `8c87e57` | M8: zeny ledger (transaction log + audit + reason-tagged movement) |
| 2026-09-20 | goAthena agent | `556a8ee` | M10 Rung A: item-script builtins (getitem/delitem/countitem/equip/unequip) + world-side adapter |
| 2026-09-20 | goAthena agent | `0e8a104` | M14: security audit pass — login rate limiter + cmd/loadgen + audit doc |
| 2026-09-20 | goAthena agent | `1a2b848` | M9 storage first slice — warehouse aggregate + rAthena `storage` schema + GORM repo + service tests (gateway wiring next) |
| 2026-09-20 | goAthena agent | `c33c242` | M9 storage second slice — gateway wiring (packet codecs + dispatch handlers + world orchestrator) — storage end-to-end |
| 2026-09-20 | goAthena agent | `b7f8ab7` | M10 Rung B — quest engine (persistent NPC vars + `getvariableofnpc` / `setquestvar` builtins) |
| 2026-09-20 | goAthena agent | `f1d96a8` | test: LoadEndAck burst drain by frame not byte count — fixes CI TestMap_SeededShopClickOpensDealType (0x02c9 leftover) |
| 2026-09-20 | goAthena agent | `cfd8cb1` | build: CI job ordering (integration after lint+unit, build last) + pre-push gate gains L3 test-integration (runtime-gated, GATE_SKIP override) + fix `.` sentinel silently skipping new-branch pushes + fix set -e exempting gate_run in && list (L3 red exited 0) |
| 2026-09-20 | goAthena agent | `06fb692` | M11 friend list end-to-end — `CZ_ADD_FRIENDS`/`CZ_DELETE_FRIENDS`/`CZ_ACK_REQ_ADD_FRIENDS` wire codecs + dispatch, `friends` table (rAthena shape, one row per direction) + GORM repo, bidirectional accept/remove in one tx, online/offline `ZC_FRIENDS_STATE` toggles both directions, `ZC_FRIENDS_LIST` in LoadEndAck burst; unit+GORM integration+gateway e2e (two conns, add→accept→remove, restore) |
| 2026-09-20 | goAthena agent | `767c283` | M11 guild first slice end-to-end — CZ create/invite/reply/leave/ban/break/chat/menuinterface codecs + 9 dispatch entries, `guild` table + `char.guild_id` (`000010_guild`), memory+GORM repos (guild dies with last member out), invite acks 0/1/2/3 to inviter, LoadEndAck belong/info/roster tail, master-only invite/expel, break=master+key+empty; wire-verified: ZC_UPDATE_GDID 0x02f7/47B (>=20220216, masterGID), ZC_GUILD_INFO 0x0b7b/118B |
| 2026-09-21 | goAthena agent | `375aa87` | M11 mail (RODEX) end-to-end — 18 C→S + 9 S→C codecs (DB 121→148), `social/mail` module (domain/app/infra/di), gateway `mail.go` + 19 dispatch entries + per-char staging & op mutex, `mail`+`mail_attachments` tables (`000011_mail`), fee math (2% + 2500/item) with ledger reasons, saga compensation on failed send, claim-first collect; rAthena-anchored wire fixes: attachment sub 60B / add-item ack 64B (uint32 cards + 25B options), MAIL_TYPE bits 0x2/0x4/0x8, newest-first inbox; L3 caught GORM `mails` pluralization + reserved-word `Order("index")`; gateway e2e ×5 + GORM round-trip/cap on MariaDB+postgres |
| 2026-09-21 | goAthena agent | `300fe5e` | deps refresh (parallel session): otel 1.46, valkey-go 1.0.78, gorm postgres 1.6.3, x/crypto 0.57.0, grpc 1.84.0 + stdlib-modernized call sites (maps/slices) — supersedes the two open dependabot PRs |
| 2026-09-21 | goAthena agent | `37ccb62` `75648e7` | release-scan fixes — v0.1.0-beta.4's trivy gate correctly failed on 3 HIGH CVEs; x/crypto/grpc bumps cleared two, grpc needed the CVE-2026-84445 fix pseudo-version (v1.85.0-dev.0.20260825072537 — trivy's DB tracks branch lineage: 1.84.0 predates the backport); testdb literal restored to the nested ContainerRequest form (flat form is a Go 1.27 promoted-field literal that older typecheckers reject) |
| 2026-09-21 | goAthena agent | `5503b26` | gosec + govulncheck gates: `task scan-vuln`/`scan-sec` run in the pre-push hook and a parallel CI security job (pinned gosec v2.29.0 / govulncheck v1.7.0, shared Taskfile targets); gosec excludes only golangci-adjudicated classes, no severity floor (probe-verified a floor drops real LOW classes; G402/G306 probe fails the gate); timeout-minutes on every CI/CD job |
| 2026-09-21 | goAthena agent | `6a93c9c` `ff21f34` | CD hardening — publish only for tags reachable from main (ancestry guard job; live-verified negative: a develop tag fails in 8s with publish+scan skipped); release cadence documented in §0 |
| 2026-09-21 | goAthena agent | `8af706e` | CD release job — a green scan now cuts the GitHub Release from the annotated tag's message (prerelease for beta/rc/alpha/dev, full for stable, re-run safe); Releases page retro-filled beta.4–7; live-verified end-to-end with v0.1.0-beta.8 (guard ✓ publish ✓ scan ✓ release ✓ prerelease flag ✓) after merging develop→main (`16791bd`, `577c968`) |
| 2026-09-21 | goAthena agent | this commit | M14 F-07 closed + M13 first slice — variable-length frames capped per the packet DB (`MaxLength`/`InboundCap()`, default 8KB): a declared length above the cap closes the connection instead of reserving buffer (`TestMap_OversizeVariableFrameCloses`, live gnet: oversize chat header closes, in-cap whisper answers); Agones SDK sidecar lifecycle wired (`internal/infrastructure/agones`: Ready/health-stream/Shutdown on `AGONES_SDK_GRPC_PORT` auto-detect, Noop otherwise) and driven from `App.Run` (ready after listeners, shutdown before drain); agones unit tests (env detect, ping loop cadence+teardown, Noop) — M13 🟡 partial, M14 F-07 ✅ |
| 2026-09-21 | goAthena agent | `0155ada` | M13 economy extraction over NATS — `economy.Service` interface seam (7 zeny verbs; shop/mail/trade resolve the interface), `nats.economy: local\|remote` switch + `nats://\|tls://` scheme validation + optional NATS_USER/PASSWORD, request/reply Proxy+Server (`goathena.economy.v0.zeny.{get,credit,deduct}`, error codes↔sentinels incl. zeny overflow, sanitized replies, queue group, flush-before-return, bounded-drain `natsinfra.Close`), `goathena serve-economy` headless DB+NATS host; adversarial review 11 findings → 6 fixed (overflow sentinel, doubled error text, async-drain shutdown, malformed-URL fatal config, driver-error leak, bus creds) + security-audit F-09 partial; unit round-trips over in-process nats-server + L3 MariaDB/postgres (real char/zeny_ledger rows move through proxy→broker→host→DB, overdraw refuses without moving) — M13 🟡 (sharding keys open) |
| 2026-09-21 | goAthena agent | `28af5c3` | M14 login-load baseline — `cmd/loadgen` was never wire-correct at 20250604 and the first live run caught it: double cmd header (57B on a 55B frame → empty username + 0x0000 garbage), stale refuse opcode (0x006a vs the server's 0x083e — refused logins counted as throttle), partial-frame reads misaligning reply N+1, read-timeout (silent limiter drop) now classified throttle via errors.As; `task loadtest` target (compose up limiter-off → readyz wait → idempotent DELETE+INSERT seed → loadgen → down); compose goathena service repaired (DB_NAME/DB_USER/DB_PASSWORD unset → config fatal at boot); `.gitignore` loadgen pattern root-anchored (shadowed cmd/loadgen); baseline: ≈1000 logins/s zero-error (100 conns × 10/s × 30s → 29900/29900), limiter 5-burst/1s shape verified (18 accept / 10 throttle / 0 error) — docs/loadtest-baseline.md |
| 2026-09-27 | goAthena agent | `67b4979` | M12 cross-zone redirect v0 — `transit/domain.MapDirectory` port (`Resolve` map → `Zone{IPv4, Port}`, wire-order address) + single-zone `LocalDirectory` provider in composition; `ZC_NPCACK_SERVERMOVE` codec (0x0ac7, 156B: mapName[24] BE-ip swapped-port empty domain[128], `PACKET_ZC_NPCACK_SERVERMOVE` ≥20170315 — clif_changemapserver) + packet-DB entry (count 148→149); gateway `relocateThroughPortal` + content `ScriptHost.Warp` branch on the resolved zone: remote persists destination, `WorldService.LeaveRemoteZone` (LeaveMap refactor over shared `leaveMap`, offline row stamped at the destination cell so the remote zone's `EnterMap` loads it), client redirected to zone ip:port; Handoff token port skipped — the shared Valkey session check in CZ_ENTER is the cross-zone client credential; L3 `TestMap_CrossZonePortalRedirects` (live gnet 156B frame) + local-warp regression green; Agones fleet directory = M13 || 2026-09-27 | goAthena agent | this commit | M13 fleet `MapDirectory` — `zone.directory.mode` `local\|agones\|static` (ZoneDirectoryConfig + fatal `Validate` on static-without-routes + fatal boot error in `app.New` when a non-local directory fails to build): `transit/agones` resolves Ready Agones GameServers via the K8s API (in-cluster creds, kubeconfig fallback = Docker Swarm path; per-List resolve — cold path), map declaration by CSV annotation `goathena.dev/maps` or `goathena.dev/map-<name>` labels, `game` status port → `Zone` (BE IPv4, plain port — Encode does the BE write; the "ntows(htons) swap" cancels on the value, pinned by e2e bytes), `self_name` marks this pod's own GameServer local, selector narrows candidates; `transit/static` parses the `routes` map→host:port table (IPv4 literal only, malformed = config error); provider switch in `transit.RegisterMapDirectory`; L2 race units green (agones/static/config fake-clientset tests), L1 + scans green, crosszone e2e regression green; fleet YAML manifests = remaining M13 |
| 2026-09-28 | goAthena agent | this commit | M10 Rung C — misc script verbs. `pkg/ro/script/constants.go` compile-time constant table (`BC_*`/`ITEMINFO_*`/`IT_*`/`FW_*`, case-insensitive like rAthena's `strcasecmp` string table) folded in `compileExpr`, without which `bc_map`/`ITEMINFO_TYPE` compiled to a variable read answering 0; `heal(hp,sp)` (absolute, int64→int32 saturating like `cap_value`, over `world.AddVitals`), `announce(text,flag)`/`mapannounce(map,text,flag)` (audience resolved in `world` over the `BC_*` target bits with `BC_NPC` source selection → `world.OnAnnounce` → gateway encodes one `ZC_BROADCAST` per recipient connection; content never touches connections), `getiteminfo(item,code)` (numeric id or AegisName; new `itemdb.ItemEntry.Info` + `Info*` codes answer the YAML scalars and rAthena's load-time defaults — Sell=Buy/2, EquipLevelMax=MAX_LEVEL, Gender=SEX_BOTH, W_/AMMO_/CARD_ subtype resolution; -1 for unknown item/column, AegisName the one string column); `ZC_BROADCAST` (0x009a) codec reproduces clif_broadcast's prefix-marker colour (`blue`/`ssss`) and empty-message drop, packet DB 149→150; `ScriptWorld` port gains `Announce`/`AnnounceMap`/`HealAbs`; evidence — kernel builtin tests, itemdb column tests, content adapter tests, world audience tests (`announce_test.go`), L3 over real TCP (`TestMap_AnnounceReachesClient`/`_BluePrefixReachesClient`/`_AnnounceMapReachesMapOnly`); `bonus`/`sc_start` deferred (need stat-bonus aggregate + status-effect registry) |
| 2026-09-28 | goAthena agent | this commit | M13 Agones fleet manifests — `deploy/agones/fleet.yaml` (two Fleets: prontera + geffen map sets) and `deploy/agones/gameserver.yaml` (single-shard case), plus `config.yaml` directory-mode notes. `Static` port policy is deliberate: the process binds `GATEWAY_MAP_PORT` at boot and nothing tells it a dynamic host port, so `Dynamic` would redirect clients to a port nobody listens on; the cost (one shard per node) and the fix (SDK `GetGameServer` at boot → advertise `Status.Address`) are recorded as M13 Open. `self_name` is read from the pod label `agones.dev/gameserver` via the downward API. `transit/agones/fleet_manifest_test.go` decodes each document with the Agones API's own YAML decoder and runs `ApplyDefaults`+`Validate` against a local no-op `APIHooks` (avoids pulling `agones/pkg/testing`'s `apiextensions-apiserver` dep), asserting the `game` port name and `goathena.dev/maps` annotation the directory reads — it caught both fleets missing `spec.strategy.type`, which the apiserver requires. `k8s.io/api` promoted indirect→direct (the test uses `corev1.Pod`). L1 (fmt/lint/vet) + L2 green. |
| 2026-09-28 | goAthena agent | this commit | M14 OTel span audit — the SDK was wired but emitted nothing (zero `StartSpan` calls repo-wide). Added `internal/shared/traces`: `Frame` (per decoded client frame), `InjectPublish`/`ExtractSubscribe` (producer/consumer around a NATS request, traceparent in message headers), a `Span` type whose `Set`/`End` tolerate nil, and a case-insensitive `carrier`. Instrumented: one span per decoded frame on both the map and char listeners (named from the packet DB: `frame CZ_ENTER`, attrs `packet.opcode`/`packet.name`), one per login attempt, and the `economy/remote` request pair — **the handler signature gained a leading `fctx context.Context` and the 69 in-handler `context.Background()` calls now use it**, so frame → service → bus is one trace (dispatch-table `size`/`fn` shape unchanged otherwise; `revive` unused-parameter forced 25 handlers to `_`). Registered `propagation.TraceContext`+`Baggage` in `initOTel` — without it injection is a silent no-op. Added `internal/shared/metrics.UnknownOpcodes{listener,known}` at both skip sites (F-08 item 3). **The propagation test caught a real bug**: NATS lowercases header keys on the wire while OTel's shipped `propagation.HeaderCarrier` is `http.Header`-backed and canonicalizes on Get, so every cross-process trace silently split into two unrelated roots — the case-insensitive carrier fixes it, and reverting to the stdlib carrier reproduces the failure. Deliberately NOT instrumented: the 50 Hz tick, the AOI broadcast fan-out, entity mutations (the frame span already brackets them). Tests: `internal/shared/traces` (5, incl. a lowercased-header round-trip), `economy/remote` `TestProxyCallEmitsLinkedTrace` (asserts producer/consumer share a trace ID and the consumer's parent is the producer). Evidence: L1 green (0 lint issues), L2 `-race` green, coverage 87.7%, L3 green (gateway 64.7s + economy 6.2s integration over real TCP/NATS). |
| 2026-11-08 | goAthena agent | `6e7a981` | M15 the source pack is the protocol authority — the Thai fork becomes the primary protocol target and upstream the compatibility set, and the wire protocol stops being hand-copied constants: every opcode and length resolves from the installed source pack at boot, out of the source's own runtime packet table. The source registers a packet with `packetdb_addpacket`, which OVERWRITES the opcode's whole entry (clif.cpp:25899-25906), and the registration headers list the same opcodes under different handlers across client versions — so the runtime table is not the first block of `clif_packetdb.hpp`, one handler owns several opcodes, and "the opcode for this handler" is not a single fact. The resolution walks the source's own registration order, keeps the last registration whose shape the server parses, and the server then accepts every opcode the source binds to that handler. Verified against a C++ probe that reproduces the source's runtime table exactly (it includes the registration headers the way clif.cpp:25929-25935 does): every opcode agrees. The port found ≈15 wire values the branch had hand-derived wrongly — CZ_REQ_LEAVE_GUILD 52→54, ZC_FRIENDS_LIST_sub 8→32, ZC_ITEM_FALL_ENTRY 24→22, ZC_SKILLINFO_LIST 37→15, ZC_USE_SKILL_TOPOS 11→10, ZC_GUILD_INFO name 17→46, CZ_ACTION_REQUEST 0x0089→0x0437, the kafra moves 0x00f3→0x0364 and 0x00f5→0x0365, CZ_CLOSE_STORE 0x07e5→0x0193 — and two of those were not merely off: 0x00f3 is CZ_GLOBAL_MESSAGE in the source's runtime table, and 0x008b (which the branch registered as ZC_ACTION_RESPONSE) the source registers as a two-byte packet with no handler, so a client built for the source could read neither frame. The warehouse now speaks the source's kafra protocol instead of the invented 0x07e3-0x07eb range, and the open is the source's own `openstorage` script command (there is no client request that opens a warehouse) wired through a content-module port. Source pack and client family are operator configuration (`client_source`/`client_variant`); the reference data and NPC corpus follow the selected pack; the fork's `vip_storage` table is a migration for both drivers. Deps bumped for advisories published since the last release: echo v5.4.4, compress v1.20.2, x/net v0.60.0. Gates: fmt-check, lint 0 issues, vet, scan-sec, scan-vuln 0 reachable, unit + integration suites green, kernel coverage 78.2%; live: login succeeds with zero refusals and zero errors on all four (pack × family) combinations. ADR 0004 records why the source's runtime table is the authority |
| 2026-11-08 | goAthena agent | `6e7a981` `f1ffde1` | M15 released — the port landed on `feat/thai-fork-on-develop` (122 files, +26280/-3982) off `origin/develop`, merged `develop` → `main`, and shipped as `v0.1.0-beta.11` (prerelease, guard/publish/scan/release green). The release pipeline demanded three fixes of itself along the way, each caught by its own gate rather than by inspection: (1) beta.9's scan failed on 5 HIGH advisories the local govulncheck gate cannot see — govulncheck reports only REACHABLE vulnerabilities, the release scan reads the whole shipped binary — so the image's own toolchain was the finding (golang:1.27.1 → 1.27.3, x/net v0.60.0 → v0.62.0, module Go directive → 1.27.3); (2) `latest` was never published at all — cd.yml gates it on `is_default_branch`, but cd.yml fires on tag pushes and a tag push is never on the default branch, so it stayed pinned to a stale image with 16 HIGH advisories; (3) the registry had no way to delete one, since ghcr.io answers the registry's own manifest DELETE with 405 UNSUPPORTED and a local `gh` token lacks `packages: delete` — the prune workflow goes through the GitHub Packages API with the workflow's own token. The registry is now down to `latest`, `0.1.0-beta.10` and `0.1.0-beta.11`, all three scanning clean. Gates: fmt-check, lint 0 issues, vet, scan-sec, scan-vuln 0 reachable, unit + integration green, kernel coverage 78.2%; live: login succeeds with zero refusals and zero errors on all four (pack × family) combinations, thailand/re 14, thailand/main 12, upstream/re 12 |
| 2026-11-08 | goAthena agent | `f1a4b18` `4697748` | modernize to the Go 1.27 baseline — `go fix` and the standalone modernize analyzer, run across all three build tags, found every site still on the pre-1.22 idiom: ~30 counted loops → `for i := range n`, 5 map-copy loops → `maps.Copy`, 8 string-split loops → `strings.SplitSeq`, 2 hand-rolled membership loops → `slices.Contains`, a `sort.Slice` → `slices.Sort`, an `interface{}` → `any`, an `atomic.AddInt64` counter → `atomic.Int64`, and testcontainers' embedded `ContainerRequest` → promoted fields. Two of the rewrites reversed earlier decisions, both now stale: the `tc := tc` copies in the AOI tests (Go 1.22's per-iteration range variable made them redundant, and they are parallel subtests — the case the copy existed for) and the nested `ContainerRequest` from `37ccb62` (nested because a flat literal needs Go 1.27's promoted-field literals and the typechecker of the day, gopls on a 1.26 tree, rejected them; gopls v0.24.0 accepts it now). `reflect.TypeAssert` panics on a zero Value where the old assertion returned `(zero, false)`, so the `min_duration` rule gained a boundary test — exactly 1s passes, below fails, and the test fails if the floor moves to `>`. The Go 1.27.3 bump had broken CI's lint job on every branch since (golangci-lint v2.13.2 cannot decode the newer export data); the pin moves to v2.15.0 and CI is green on all six jobs. Gates: fmt-check, lint 0 issues, vet, scan-sec, scan-vuln 0 reachable, unit + integration green, kernel coverage 78.2% |
| 2026-11-08 | goAthena agent | `d2992cd` `0335e82` `5f8448f` `13a3a9d` `899f53b` | refactor pass on `cmd/protocolgen` — the generator asked the source's own C++ compiler what the source resolves to, and that invocation was restated in four places: the include-path list (a fact about the source's tree layout) appeared three times, and two of the copies were byte-identical. The cost was not the line count but the drift: a change to the include paths fixes one copy and leaves the others pointing at the wrong place, and the generator then silently resolves the wrong thing. `cmd/protocolgen/compiler.go` now owns the compiler invocation, the probe workspace and the header include shape; `resolveRegistrations` owns the three-input preamble that `resolveTable` and `registrationOrderFor` both ran; the dead `srcDir` parameter (threaded through 20 sites, read in 2, because `sourceDir` is always `filepath.Join(checkout, "src")`) is gone. Duplication counts: include-path list 3→1, PACKETVER defines 3→1, temp-dir creation 5→1, the `packets_struct.hpp` rule 2→1, `srcDir` 27→0. **The generator's output was never pinned**, so the pass began by pinning it: `scripts/verify-protocolgen.sh` regenerates each source pack's tables from its checkout and compares the resolved values to the committed ones — all 10 files OK, before and after every step. That gate earned its keep immediately: it caught an argument-ordering bug (the compiler's mode flags must precede the input) that the build alone did not. Gates: fmt-check, lint 0 issues, vet, scan-sec, scan-vuln 0 reachable, unit + integration green, kernel coverage 78.2%, CI green on all six jobs |
| 2026-11-08 | goAthena agent | `777f333` `a7624ed` | release fixes the release itself demanded — (1) the NATS trace-propagation test raced: the host ends its consumer span AFTER it publishes the reply, so the reply can arrive and the test's assertion can run before the consumer span is exported; under CI's parallel package load it sometimes was not there yet. The test now waits for both spans with a deadline. Pre-existing, not a regression from the protocolgen refactor — the test file is unchanged since M14 and the failure reproduces identically on v0.1.0-beta.12; widening the window (delaying the host's span.End by 50ms) makes it fail 3/3 on both trees and pass 5/5 against the fixed test. The wait did not weaken the check: breaking the traceparent propagation still fails it on the trace-ID and parent assertions. (2) `v0.1.0-beta.13`'s release scan failed twice with NO findings in it — the trivy-action installs its scanner by downloading a release tarball from `github.com/.../releases/download/`, and that route answers 504, so the install step died and the job reported what looked like a scan failure. The scanner now runs from the `aquasec/trivy` container, which comes from a registry and is unaffected; the gate is unchanged (HIGH+CRITICAL fail, unfixed ignored). Verified locally with the same command and flags: the published image scans clean (exit 0), and `nginx:1.14` reports 27 CRITICAL + 50 HIGH with exit 1, so the gate still fails on a vulnerable image |
| 2026-11-08 | goAthena agent | `d073c90` | Tier 3 (k3s) and Tier 1 (compose) deployment, both verified — the k3s base carries the whole tier (namespace, Secret, ConfigMap, Postgres with its volume, Valkey, NATS, the OTel collector, the client-facing and control-plane Services, the per-map-set Fleets, the FleetAutoscalers, a TCP ingress), and the prod overlay pins the image, the resources, the budgets and the address the client is handed. Every resource is validated against the real API schema by `scripts/verify-k3s.sh` (kubeconform -strict), with the Agones CRD schemas extracted from the Agones release manifest by `scripts/crdschema` — the built-in catalog carries 616 groups and none for agones.dev, so without that the Fleets, which carry the shard's contract with the transit MapDirectory, would be skipped silently. Both overlays: dev 21 and prod 24 resources, all valid; the check was confirmed to fail on a Fleet whose `replicas` is a string and to pass again once restored. Three bugs surfaced by reading the rendered manifests rather than the sources: a strategic merge on a Fleet REPLACES the whole `containers` list (kustomize cannot infer a merge key for a CRD), so every shard would have started with no arguments, no environment and no config mount — the Fleet patches are JSON patches now; a patch file with several documents applies all of them to every target listed for it; and a PDB over a single-replica service blocks node drains permanently, so the budgets live in the prod overlay where the replica counts make them meaningful. Tier 1's `task verify-tier1` proves the running container is this tree: the commit stamp in /version matches HEAD, the four endpoints answer 200, and a real login is accepted (0 refused, 0 errors). It exists because `docker compose up` reuses the last image, and the cached one served an older commit whose /metrics 404'd and refused every login while /healthz stayed green — the stack looked up and the app was not the app. |
| 2026-11-08 | goAthena agent | `4619cda` | the deployment tiers shipped — v0.1.0-beta.15 (`v0.1.0-beta.16` carries the image-tag fix) — the k3s base carries the whole Tier 3 (namespace, Secret, ConfigMap, Postgres with its volume, Valkey, NATS, the OTel collector, the client-facing and control-plane Services, the per-map-set Fleets, the FleetAutoscalers, a TCP ingress) with dev and prod overlays, and every resource is validated against the real API schema by `scripts/verify-k3s.sh` (kubeconform -strict), with the Agones CRD schemas extracted from the Agones release manifest by `scripts/crdschema` — the built-in catalog carries 616 groups and none for agones.dev, so without that the Fleets, which carry the shard's contract with the transit MapDirectory, would be skipped silently. Tier 1's `task verify-tier1` proves the running container is this tree: the commit stamp in /version matches HEAD, the four endpoints answer 200, and a real login is accepted (0 refused, 0 errors). It exists because `docker compose up` reuses the last image, and the cached one served an older commit whose /metrics 404'd and refused every login while /healthz stayed green — the stack looked up and the app was not the app. Three bugs surfaced by reading the rendered manifests rather than the sources: a strategic merge on a Fleet REPLACES the whole `containers` list (kustomize cannot infer a merge key for a CRD), so every shard would have started with no arguments, no environment and no config mount — the Fleet patches are JSON patches now; a patch file with several documents applies all of them to every target listed for it; and a PDB over a single-replica service blocks node drains permanently, so the budgets live in the prod overlay where the replica counts make them meaningful. Two more the CI caught that a local run could not, both in the verification tooling and both meaning the check had never actually run against the Agones schema on Linux: kubeconform expands `{{.ResourceKind}}` in LOWERCASE, so the schema file has to be `fleet_v1.json` — macOS's case-insensitive filesystem found `Fleet_v1.json` anyway and the check passed, Linux did not; and the local schema location must come BEFORE the remote catalog, because a failed fetch from the catalog aborts a resource instead of falling through to the next location. And one found by pulling the published image rather than by reading the workflow: the prod overlay pinned `ghcr.io/bouroo/goathena:v0.1.0-beta.15`, but the publishing workflow sets the image's tags with `type=semver`, so the image is `0.1.0-beta.15` — the `v` belongs to the git tag, not the image. The pinned tag has never existed for ANY release, so every shard on a real cluster would have failed to pull. A schema check cannot see that an image tag resolves. |
| 2026-11-08 | goAthena agent | this commit | M13 closes the advertised-address ticket — the shard now advertises the address Agones assigned it. `internal/infrastructure/agones/allocation.go` adds the `Alloctor` surface: it reads the allocation from the sidecar (`GetGameServer`) at boot and returns the node address and the assigned host port. `gateway.MapEndpointResolver` is the port that carries it into the char server's handshake (`NotifyZoneServerResponse`), and the composition root provides it — the allocation inside a fleet, the configured `gateway.map_host` otherwise. The address is runtime state: the pod's IP is assigned after the manifest is written, so no env var can express it, and a shard that advertised the configured host would send every client to an address that is not its own. With the shard able to advertise what it was allocated, the Fleets' port policy moves from `Static` to `Dynamic`: Agones picks an open host port, so a node can carry several shards instead of one. The Agones manifests move under `deploy/k3s/` — the tree the overlays actually apply — and the older `deploy/agones/` tree is removed rather than left to drift; the manifest test now reads the deployed files and asserts the port policy (it catches a flip back to `Static`). A third overlay, `deploy/k3s/overlays/single/`, deploys one GameServer instead of the Fleets: a hand-allocated boss zone, a staging shard, a smoke test. It is an alternative, not an addition — the overlay removes the Fleets and their autoscalers, since an autoscaler scales Fleets and a Fleet that is not deployed has nothing to scale. |
| 2026-11-08 | goAthena agent | this commit | M13 closes the player-count ticket — the fleet now sizes on the players. The shard publishes its connected-player count to the sidecar every 10s (`internal/infrastructure/agones/counter.go`, the beta SDK's `UpdateCounter`); Agones aggregates it across the fleet; the `FleetAutoscaler`'s **Counter** policy sizes the fleet by it — the aggregate capacity covers the aggregate players, with 10% spare. The count comes from the world's live player registry (`WorldService.OnlinePlayerCount`), and it counts player characters only: a mob is not a player, because a count that included them would inflate the load the autoscaler thinks it is serving and scale the fleet up for players who are not there. The Buffer and Schedule policies stay alongside the Counter: they cover the load the counter cannot see, since a shard that is up but not yet loaded still has to exist before its first player arrives, and a cold start takes longer than a report interval. The counter's name is a contract between three places — the Fleet's `spec.template.spec.counters.players`, the name the shard publishes, and the policy's `key` — and a typo in any one of them leaves the autoscaler scaling on a counter that does not exist, so the manifest test asserts all three agree (it catches a flipped key). Two real defects Agones' own validator surfaced, both in the committed manifests: the buffer policies' `minReplicas` (1) was below their `bufferSize` (2, and 5), which Agones rejects — the floor is now the buffer size; and the counter needs the `CountsAndLists` feature gate, which the chart enables by default but which the test process has to apply itself (the gates default to empty until the controller parses them at startup). |
| 2026-11-09 | goAthena agent | this commit | **A P0 protocol defect, found by reading the source against the encoders, and the Tier 2 tier.** The first: every packet carrying a server address wrote the IPv4 and the port in the wrong byte order. The packets parsed, the client connected, and it connected to the **wrong host** — 127.0.0.1 went on the wire as 7f 00 00 01, which the client reads as 1.0.0.127. rAthena writes those fields as `p.ip = htonl(ip); p.port = ntows(htons(port));` (char_clif.cpp:909-910, loginclif.cpp:137-138, clif.cpp:2178-2179), the structs are packed, and socket_send memcpy's them — so the wire bytes are the host memory image of those field values. Compiled from the source's own structs: HC_NOTIFY_ZONESVR ip@22 = 01 00 00 7f, port@26 = 01 14; AC_ACCEPT_LOGIN_sub ip@0 = 01 00 00 7f, port@4 = e9 17; ZC_NPCACK_SERVERMOVE ip@30 = 01 00 00 7f, port@34 = 01 14. One convention now: the Go field holds the source's field value, the encoder writes the little-endian memory image. Why it survived: the loadgen only exercises the login port and no test followed the address the handshake carried — the login was accepted, the health check was green, and the character would have been handed to a host that is not this one. A second, quieter bug: the Agones map endpoint and the zone directory were registered **after** compose built the listeners, so the char server never saw them — the Agones allocation was dead code and every fleet shard advertised its configured host instead of the address Agones assigned it. Both registrations moved before the listeners are built, and the directory's failure stays fatal. Two data races fixed on the way: SetMapEndpoints and SetZoneDirectory write fields the gnet event loop reads on every char-select and portal crossing. The second: **Tier 2** — the middle rung of the ladder, which was prose only. `deploy/tier2/compose.yml` runs two goathena nodes over one shared Postgres/Valkey/NATS, each serving a disjoint set of maps, finding each other through a static route table. `ZONE_DIRECTORY_ROUTES` could not be set from the environment at all: the env overlay had no map case, so the variable failed the boot with 'unsupported kind map' — which made the static directory unreachable from a container deployment. `task verify-tier2` proves both nodes carry this tree's commit stamp, both are healthy, a login is accepted through the shared session store, and **a character on a remote map is handed the other node's address** — the real handshake, driven by `cmd/charselect` (login → char-enter → char-select → the map address out of HC_NOTIFY_ZONESVR). Gates: fmt-check, lint 0 issues, vet, scan-sec, scan-vuln exit 0; unit + integration green under -race; coverage 78.2%; verify-tier1 exit 0; verify-tier2 exit 0; verify-k3s dev 23 / prod 26 / single 17 all valid |
