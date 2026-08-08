# goAthena — Project Plan & Roadmap

> Plan of record. Status reflects the verified state of the repository as of
> 2026-08-09. Every claim below is anchored to executable evidence (git refs,
> file paths, line counts, or test gates), not narrative.

---

## 1. Executive summary

**goAthena** is a from-scratch Go re-engineering of the Ragnarok Online server,
built as a **modular monolith** on domain-driven / clean-architecture principles.
It targets **wire + data compatibility** with the Thai Classic client
(**`PACKETVER 20250604`**) so that the existing client (`ClientROThailand`),
the rAthena SQL schema, and the game-data YAMLs work as-is. (roBrowser /
WebSocket support is on the roadmap, not yet wired.)

The reference implementation is **rAthena** (`third_party/rathenaThailand`,
Thai Classic fork). goAthena is **not** a line-for-line port: it keeps the
*protocol and data* identical while re-expressing the game loop, persistence,
and concurrency in idiomatic Go designed for horizontal scale-out.

### Current state (verified)

| Artifact | Status | Evidence |
|---|---|---|
| Repo `develop` tip | **Wiped greenfield** — `6a79d64 refactor: re-init project` removed 435 files / 78k lines, leaving only `README.md`, `LICENSE`, `docs/`, `third_party/` | `git show 6a79d64 --stat` |
| Proven prior build | **Fully recoverable**, one commit back at `cea42f8` (442 tracked files) | `git ls-tree cea42f8` |
| Upstream `main` | **M0–M7 merged** — `56d280b Greenfield modular-monolith rebuild + playable combat slice (M0–M7)` | `git log main` |
| Third-party refs | Present as **git submodules** (`rathena`, `rathenaThailand`, `ClientROThailand`, `ignore = all`) | `.gitmodules` at HEAD |

The re-init cleared the tree. The **verified `pkg/ro` kernel survives intact at
`cea42f8`** (the prior app-layer modules, build config, and migrations also
survive there as recoverable reference). **Phase 0 keeps the kernel verbatim and
builds the application layer ground-up** (see §6) — the rebuild does **not**
re-derive the already-proven protocol/crypto/stat math, and does **not**
wholesale-restore the prior app layer.

---

## 2. Architecture

### 2.1 Process model

A single deployable **modular monolith** — one `goathena` binary with three
subcommands (`serve`, `migrate`, `version`). `serve` runs supervised listeners:

- **Login** TCP `:6900` (the only static port the client knows — read from its `clientinfo.xml`)
- **Character** TCP `:6121` (advertised to the client during the login→char handoff)
- **Map** TCP `:5121` (a WebSocket / roBrowser listener is planned, not wired)
- **HTTP** `/healthz` + `/readyz`; **gRPC** (ops)

Internally the server mirrors rAthena's tick model but with Go-native
concurrency: a **fixed-timestep game loop at 50 Hz (20 ms)** drives AOI, timers,
and combat; a goroutine-per-connection reader feeds a serializing dispatcher per
entity so game-state mutation stays race-free without a global lock.

### 2.2 Bounded contexts

Each concern is a module under `internal/modules/` owning its data invariants
and exposing a narrow **domain port** (Go interface). Cross-module calls go
through interfaces, never concrete types. Layering inside a module is
clean-architecture: `domain` (pure) → `app` (use cases) → `infra` (adapters) →
`di` (wiring). Dependency direction is strictly inward.

| Module | Owns | Status @ `cf4d622` (HEAD) |
|---|---|---|
| `account` | Auth + login/char-select session | ✅ domain+app+infra |
| `character` | Character CRUD + progression | ✅ domain+app+infra |
| `world` | Entity, AOI, tick, spawn, combat authority (Agones adapter planned, not wired) | ✅ most built (domain 10 / app 41 files) |
| `gateway` | Ingress: codec + table-driven dispatch + broadcast render | ✅ app+domain+infra (TCP only; WS planned) |
| `inventory` | Item-container aggregate (char/cart/warehouse/storage) | ✅ domain+infra |
| `content` | Script engine (NPC dialog/quest/item script) | 🟡 partial — dialog bridge landed (602 LOC), full VM coverage open |
| `commerce/{shop,trade,vending,storage}` | Use-case services over economy+inventory ports | 🟡 shop slice landed (157 LOC); trade/vending/storage open |
| `economy` | Zeny-ledger aggregate | 🟡 first slice landed (216 LOC); full ledger open |
| `social` | Chat / friend / party / guild / mail | 🟡 scaffold (PlayerDirectory port, 87 LOC); chat lives in `world` for now |
| `transit` | Cross-map / cross-zone handshake | 🟡 cross-map warp landed (SetPosition + LeaveMap, 61 LOC); cross-zone handshake open |

Combat is a `world` **app service** (no independent data) to avoid a
`world → combat → world` cycle. Reference data (mob/item/skill tables) lives in
the shared `pkg/ro` kernel; the `content` engine calls inventory/economy ports by
injection, never the reverse.

### 2.3 Boundary enforcement (merge-blocking, not advisory)

Three mechanisms, enforced from the first commit:

1. **depguard** — a module may import a peer's `domain` only; importing a peer's
   `app`/`infra`/`di` is denied. The composition root (`internal/app`) is
   forbidden inside modules (would form a cycle).
2. **`internal/app/arch_test.go`** — a stdlib source-walk assertion that catches
   intra-module drift the linter cannot (e.g. a `domain` entity importing a
   persistence driver). Runs in the unit gate.
3. **shared value types** (`internal/shared/`) — `EntityID`, `Money`, `Position`
   … type-system-enforced cross-module agreement on primitives.

### 2.4 The kernel — `pkg/ro` (carried verbatim, proven)

Everything else builds on this; it is the most-verified code in the repo
(57 test files). It is **not** re-derived.

| Package | Role |
|---|---|
| `packet` | Typed request parsers (`Parse*`), `(*Response).Encode`, streaming `Decoder.Feed/Next`, opcode tables |
| `packetdb` | Version-gated opcode compiler (`ForPacketVer`) — the per-`PACKETVER` dispatcher seam |
| `crypto` / `textenc` | Stream crypto + multi-byte text encoding (Korean/Thai codepages) |
| `aoi` / `pathfinding` / `romap` | Area-of-interest grid, A*, map/tile models |
| `script` | Script types/opcode/parse (the engine that runs them lands in `content`) |
| `itemdb`, `mobdb`, `skilldb`, `skilltree`, `jobdb`, `jobbasepoints`, `statpoint`, `statcalc`, `constdb`, `rathenadb`, `athenaconf`, `mapindex` | Game-data registries loaded from rAthena YAMLs |

**Key compatibility facts** (verified against source/memory):
- `PACKETVER 20250604` → **no stream obfuscation** (`KeysForVersion` returns `0,0,0`; codec is identity).
- Thai Classic is **non-renewal** → `db/pre-re` tree is authoritative; pre-renewal HIT/FLEE/DEF2/CRIT/base-ATK/MATK/amotion formulas apply.
- EXP rides `ZC_LONGLONGPAR_CHANGE` (int64, `0x0acb`) at this packetver, never the 32-bit `0x00b1`.
- Client declares `servicetype=korea`, `langtype=0`, `version=55`, `euc-kr` codepage — despite "Thai Classic" branding.

---

## 3. Technology stack

| Layer | Choice | Rationale |
|---|---|---|
| Language | **Go 1.26+** | Concurrency, single-static-binary deploy, GC fit for a game loop |
| Primary DB | **PostgreSQL** (MariaDB as compatibility fallback) | PG for production durability; MariaDB keeps rAthena schema read/write compat |
| Cache / sessions | **Valkey** (Redis-fork) | Session keys, hot state, rate-limit counters |
| Inter-service eventing | **NATS** *(planned)* | Configured (`config.yaml`, compose sidecar); no `nats.Connect` in the binary yet. Scale-out bus for when modules extract to separate binaries |
| Game-server orchestration | **Agones** *(planned)* | Per-shard/per-map `GameServer` allocation on K8s; design seam only, no adapter wired |
| Network | **gnet v2** (TCP) | Zero-copy event loop for the map protocol. A `coder/websocket` listener for roBrowser is planned, not wired |
| Migrations | **golang-migrate** (embedded `go:embed`) | Self-contained, idempotent, 11-wave schema |
| DI | **samber/do v2** | Auditable, line-by-line wiring; infra singletons, plain-ctor use cases |
| Observability | Prometheus `/metrics` | Live today. OpenTelemetry tracing + Grafana dashboards are planned |
| Build/lint | **Task** runner, **golangci-lint v2**, **gofumpt/goimports** | Merge-blocking CI |

---

## 4. Deployment strategy — hobbyist → enterprise

A single binary and one image serve both ends; only the **surrounding topology**
differs.

### Tier 1 — Hobbyist (single node)
`podman compose up` — one `goathena` container with MariaDB + Valkey + NATS as
sidecars. One process, one map zone, hundreds of concurrent players. No K8s.
Lowest operational footprint.

### Tier 2 — Small fleet (self-hosted, a few nodes)
Modular monolith behind a load balancer; Valkey for shared sessions so any node
can resume a login; NATS for cross-node broadcasts. Read-replicas for PG.

### Tier 3 — Enterprise (Kubernetes + Agones)
- **Agones `Fleet`** allocates **per-zone (per-map-shard) `GameServer` processes**
  — the natural unit of horizontal scale for a zone-based world. Each allocation
  is an isolated game loop.
- The modular monolith's bounded contexts (each already behind an interface)
  extract into **separate binaries over NATS** as load demands: e.g. the
  `economy` zeny-ledger becomes a dedicated, sharded service; `social` chat fans
  out via NATS subjects.
- **PostgreSQL** primary+replicas for durable state; **Valkey** cluster for hot
  cache; **OTel Collector** → Prometheus/Grafana for telemetry.
- Kustomize base + dev/prod overlays, HPA, PDB, Traefik ingress + rate-limit.

The architectural seam is real today: `composition.go` wires the process via
explicit ordered factories and every module is injectable behind an interface.
The scale-out *plumbing* is not — there is no `nats.Connect` and no Agones
adapter in the binary; versioned NATS subjects and an Agones lifecycle client
are future work, not present in the tree.

---

## 5. Milestone roadmap

The re-init wiped the tree; the rebuild re-lands each milestone fresh (keep the
`pkg/ro` kernel, rewrite the app layer on the framework stack). **M0–M7 are
re-landed and proven** in the new tree; **first slices of M8, M9, M10, and M12
have landed** and M11 is a scaffold. M13 scale-out and M14 hardening remain
planned; **dual-client (roBrowser/WebSocket) and PACKETVER decoupling are not
started.**

| Milestone | Scope | Status |
|---|---|---|
| **M0** Scaffold | Binary boots; `/healthz`+`/readyz`; migrations apply; arch boundaries enforced | ✅ re-landed |
| **M1** Account / Login | `:6900` login handshake (v55 old-login), account auth, session keys, gnet listener | ✅ re-landed |
| **M2** Character | Char CRUD, char-select, session handoff, char TCP :6121 | ✅ re-landed |
| **M3** World core | Entity lifecycle, AOI grid, 50 Hz tick, map-enter, map :5121 | ✅ re-landed |
| **M4** Gateway ingress | Table-driven opcode dispatch, LoadEndAck, movement | ✅ re-landed |
| **M5** Inventory | Item-container aggregate + LoadEndAck init burst | ✅ re-landed |
| **M6** Spawn / drops | Mob spawn, floor items, drops, pickup | ✅ re-landed |
| **M7** Combat | Melee damage, attack action, HP reduction | ✅ re-landed |
| **M8** Economy | Zeny value object + EconomyService (DeductZeny/CreditZeny) | 🟡 partial — first slice (216 LOC); full zeny-ledger open |
| **M9** Commerce | Shop buy/sell (economy+inventory ports) | 🟡 partial — shop slice (157 LOC); trade/vending/storage open |
| **M10** Content | script VM ↔ dialog bridge (mes/next/select/input/close) | 🟡 partial — dialog bridge landed (602 LOC); full script-VM coverage open |
| **M11** Social | Chat/whisper routing scaffold (PlayerDirectory port) | 🟡 scaffold (87 LOC) |
| **M12** Transit | cross-map warp (SetPosition + LeaveMap) | 🟡 partial — in-zone warp landed (61 LOC); cross-zone handshake + Agones allocation open |
| **M13** Scale-out prep | Module extraction over NATS; Agones fleet wiring; sharding keys | 📋 planned (no `nats.Connect`, no Agones adapter yet) |
| **M14** Hardening | Prometheus /metrics + Docker compose verified (36MB distroless, e2e login) | 🟡 partial — /metrics + compose e2e login live; OTel tracing, security review, load test open |

**Effort weighting** (from `rathena-subsystem-size-risk-profile`): the protocol/
crypto/path work is a few hundred lines and **done**; the real effort is the
**script VM (`script.cpp` 29k LOC), skills (16k), and combat (9k)**. Milestones
M9–M10 carry the dominant risk.

---

## 6. Implementation methodology — loop engineering

Each phase follows **THINK → ACT → PROVE → GROW** and ends with a local commit.
A phase is "done" only when executable evidence (command + exit code) confirms
it — never when the code merely looks right.

### Verify gates (every phase)

- **L1 static** — `task fmt` (gofumpt + goimports), `task lint` (golangci-lint v2).
- **L2 runtime** — `task test-unit` (`go test -race -tags=unit ./internal/... ./pkg/...`, 60% coverage gate).
- **L3 end-to-end** — at least one path crosses a real boundary (login→char→map over TCP, or a real DB). Integration tests spin real MariaDB/Valkey/NATS via compose.
- **Hard stop** — 3 failed verify cycles on one issue → stop, hand back, run GROW retro.

### Phase plan (committed, one per phase)

| Phase | Deliverable | Commit gate | Status |
|---|---|---|---|
| **P0** Kernel re-establishment | Keep proven `pkg/ro` kernel verbatim; decouple it from the app layer; rewrite README | `go build ./...` + kernel unit tests green | ✅ done |
| **M0** Scaffold | config (validator) + composition (echo/do) + db (GORM) + valkey + `goathena` binary + build config | `task verify` green; L3 live health/readyz | ✅ done |
| **M1** Login | account module (domain/app/infra) + migration system + gnet login listener :6900 | L3 real-TCP handshake e2e | ✅ done |
| **M2** Character | char CRUD, char-select, session handoff (Valkey), char TCP :6121 | L3 char-select over TCP | ✅ done |
| **M3** World | entity registry + AOI grid + 50Hz tick + map-enter (CZ_ENTER→ZC_ACCEPT_ENTER), map :5121 | L3 map-enter over TCP | ✅ done |
| **M4** Gateway ingress | table-driven opcode dispatch (CZ_ENTER/LoadEndAck/move), LoadEndAck inventory burst | L3 dispatch e2e | ✅ done |
| **M5** Inventory | item-container aggregate (domain/infra/app) + LoadEndAck init burst + wave3 | L1+L2+wave3 migrated | ✅ done |
| **M6** Spawn / drops | mob spawn + floor items + drops + CZ_ITEM_PICKUP | L1+L2 | ✅ done |
| **M7** Combat | CombatService + melee NormalMelee (pre-re) + CZ_ACTION_REQUEST + HP reduction | L1+L2 | ✅ done |
| **M8** Economy | `economy` zeny-ledger aggregate + ports | L1+L2 | 🟡 partial (first slice) |
| **M9** Commerce | `shop`/`trade`/`vending`/`storage` over economy+inventory | L3 (trade e2e) | 🟡 partial (shop slice) |
| **M10** Content | `content` script VM — dialog/quest/item-script execution | L3 (NPC dialog e2e) | 🟡 partial (dialog bridge) |
| **M11** Social | friend/party/guild/mail | L3 | 🟡 scaffold |
| **M12** Transit | cross-map handshake + Agones allocation path | L3 (map-change e2e) | 🟡 partial (in-zone warp; cross-zone+Agones open) |
| **P-scale** Scale-out | extract one module to a NATS binary; Agones fleet | L3 (multi-process) | 📋 |
| **P-hard** Harden | OTel coverage, security review, perf profiling, load test | perf budget met | 📋 |

### Phase 0 — recovery decision (recorded)

The re-init wiped the tree but the proven kernel survives at `cea42f8`. The
chosen path: **keep the verified `pkg/ro` kernel verbatim** — do **not**
re-derive protocol/crypto/stat math (months of correctness risk, zero benefit) —
and **build the application layer (modules, infrastructure, `cmd`, composition
root) ground-up with efficiency as a first-class design goal.** No wholesale
restore; the prior app layer is treated as recoverable reference, not reused.

One kernel defect was found and fixed in this phase: `pkg/ro/athenaconf`
imported the app-layer `internal/config` (an inward dependency that would make
the kernel unbuildable without the app layer). Decoupled by introducing a
kernel-local `athenaconf.Config`/`athenaconf.Identity` apply target; the app
layer will map or embed these fields. The `third_party/` submodules are
untouched.

---

## 7. Reference

| Concern | rAthena path (`third_party/rathenaThailand/`) |
|---|---|
| Packet parse / stream crypto | `src/map/clif.cpp`, `src/common/des.cpp`, `src/common/socket.cpp` |
| Login / accounts | `src/login/` |
| Character / inter-server | `src/char/` |
| Map server / pathfinding / script VM | `src/map/` |
| Shared utilities (timer, sql, grf, md5) | `src/common/` |
| Game DBs (pre-re authoritative) | `db/`, `db/pre-re/` |
| Script corpus + spawns | `npc/`, `npcTH/` (~1.2k spawn files each) |
| SQL schema | `sql-files/main.sql` (reserved-word cols `int`/`rename`/`key` need back-quoting) |

goAthena's source truth for legacy behavior, packet formats, script dialect, map
file formats, schema, and game-data YAMLs is the upstream rAthena C/C++ codebase,
checked out locally for reference only; nothing is vendored into goAthena.
