# goAthena

[![CI](https://github.com/bouroo/goAthena/actions/workflows/ci.yml/badge.svg)](https://github.com/bouroo/goAthena/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/bouroo/goAthena)](https://goreportcard.com/report/github.com/bouroo/goAthena)
[![Go Version](https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go&logoColor=white)](https://go.dev/doc/go1.26)
[![License](https://img.shields.io/badge/license-GPLv3-blue.svg)](https://www.gnu.org/licenses/gpl-3.0.html)

A Go re-implementation of the **Ragnarok Online** server, built as a modular
monolith on domain-driven / clean-architecture principles.

## What is this?

**Ragnarok Online (RO)** is a long-running Korean MMORPG whose private-server
scene has been kept alive for two decades by community emulators. **rAthena** is
the most widely used one — a mature, single-process C/C++ project that handles
login, characters, and the game world in tightly coupled daemons.

**goAthena** is a from-scratch Go re-engineering of that game logic (not a line-
for-line port) that preserves **wire and data compatibility** with rAthena, so
existing clients (the **ClientROThailand** TCP client and the **roBrowser**
WebSocket client) and the rAthena database schema and game-data YAMLs work as-is.
It targets `PACKETVER 20250604` (Thai Classic). The process model is a single
deployable **modular monolith** — one binary, supervised listeners — whose
bounded-context modules are split-ready (every seam is a Go interface; the first
real scale-out extracts a module into its own binary over the NATS bus).

> **Project status — greenfield rebuild in progress.** The application layer was
> rebuilt from an empty tree into a modular monolith, carrying the verified,
> tested **kernel** verbatim (`pkg/ro` protocol/data libraries, the
> infrastructure adapters, and the 11-wave SQL schema). **Milestone M0 (scaffold)
> is complete:** the single binary boots, serves `/healthz` + `/readyz`, and
> applies migrations. The gameplay milestones (login → character → world →
> combat → commerce → script → transit) land in M1–M12. goAthena is **not yet a
> drop-in replacement**; it is a usable, tested foundation under active rebuild.

## Who is this for?

- **Curious developers** — anyone interested in MMORPG server architecture,
  real-time networking, or re-implementing legacy C++ in Go.
- **Contributors** — people who want to run goAthena locally and start hacking
  on it.
- **RO community** — server operators and scripters evaluating a modern
  alternative to rAthena.

## Prerequisites

- **Go 1.26+**
- **Docker** (or Podman)
- **[Task](https://taskfile.dev/installation/)** — the build runner used throughout
- A running **MariaDB 11.4+**, **Valkey 9+**, and **NATS 2.x** — easiest via `docker compose`
- **PostgreSQL** is also supported as an alternative database driver

## Quick start

```bash
cp .env.example .env                       # local config defaults
docker compose up -d mariadb valkey nats   # start the backing services
task migrate-up                            # goathena migrate up — apply schema
task serve                                 # build + run `goathena serve`
```

`goathena` is the one binary. It speaks three subcommands:

| Command | What it does |
|---|---|
| `serve` | Run the modular-monolith server: HTTP health/gRPC now; login/char/map TCP + WebSocket game listeners arrive in M1+. Blocks until SIGINT/SIGTERM. |
| `migrate up \| down [N] \| force VERSION \| version` | Apply/roll back the embedded SQL schema. Self-contained (`go:embed`), idempotent. |
| `version` | Print build metadata (`main.Version`/`CommitSHA`/`BuildTime`, injected at release time). |

In Docker the compose `goathena` service runs `serve` and the one-shot `migrate`
service runs `migrate up` first; the app waits on `migrate` completing
successfully.

## Architecture

A single process, structured as independent **bounded contexts** under
`internal/modules/`. Each module owns its data and invariants and exposes a
narrow domain port to the others; cross-module calls go through interfaces, not
concrete types. Layering inside a module is clean-architecture: `domain`
(ports + value objects, pure) → `app` (use cases) → `infra` (adapters) → `di`
(wiring). Dependency direction is strictly inward.

Three boundary mechanisms are enforced from the first commit (merge-blocking in
CI, not advisory):

1. **depguard** — a module may import another module's `domain` package only;
   importing a peer's `app`/`infra`/`di` is denied. The composition root
   (`internal/app`) is forbidden inside modules (it would form a cycle).
2. **`internal/app/arch_test.go`** — a stdlib source-walk assertion that catches
   intra-module drift the linter can't (e.g. a `domain` entity importing a
   persistence driver). Runs in the unit gate with no build or exec.
3. **shared value types** in `internal/shared/` — the type system enforces
   cross-module agreement on primitives (`EntityID`, `Money`, `Position`, …).

The composition root (`internal/app/composition.go`) wires the process with
explicit, ordered factory calls: config → logger → telemetry → HTTP/gRPC
servers, then (M1+) persistence and the gateway ingress + feature modules.
Infra singletons (DB/Valkey/NATS/logger) live in a `samber/do/v2` injector;
use-case services are plain constructors so wiring is auditable line-by-line.

### Bounded contexts

| Module | Owns |
|---|---|
| `account` | Authentication + the login/char-select session |
| `character` | Character CRUD + progression (stats, skill points) |
| `world` | Entity, AOI, tick, spawn, combat authority, Agones adapter |
| `inventory` | Item-container aggregate per char/warehouse/storage |
| `economy` | Zeny-ledger aggregate |
| `commerce/{shop,trade,vending,storage}` | Use-case services over the economy + inventory ports |
| `social` | Chat / friend / party / guild / mail (sub-packages) |
| `transit` | Cross-map handshake |
| `content` | The script engine (NPC dialog/quest/item script) |
| `gateway` | Ingress: codec + table-driven dispatch + broadcast render |

Combat is a `world` app service, not its own context (no independent data),
which avoids a `world → combat → world` cycle. Reference data (mob/item/skill
tables) lives in `pkg/ro` as shared kernel; the `content` engine calls
inventory/economy ports via injection, never the reverse.

## RO protocol & data libraries (`pkg/ro`)

Carried verbatim and unit-tested — the spec everything else builds on:

| Package | Role |
|---|---|
| `packet` | Typed request parsers (`Parse*`), `(*Response).Encode`, streaming `Decoder.Feed/Next`, opcode tables |
| `packetdb` | Version-gated opcode compiler (`ForPacketVer`) — the dispatcher's per-`PACKETVER` seam |
| `crypto` / `textenc` | Stream crypto and the multi-byte text encoding (Thai codepage) |
| `aoi` / `pathfinding` / `romap` | Area-of-interest grid, A*, and map/tile models |
| `script` | Script types/opcode/parse (the engine that runs them lands in `content`, M9) |
| `itemdb`, `mobdb`, `skilldb`, `skilltree`, `jobdb`, `jobbasepoints`, `statpoint`, `constdb`, `rathenadb`, `athenaconf`, `mapindex` | Game-data registries loaded from rAthena YAMLs |

## Project layout

```
cmd/
  goathena/          # single binary: serve + migrate + version subcommands
  genpacket/         # dev tool: regenerate packet tables from clif_packetdb
  import-conf/       # dev tool: import rAthena conf into data/
  healthcheck/       # minimal HTTP probe for distroless container healthchecks
internal/
  app/               # composition root (composition.go) + boundary arch_test.go
  config/            # config.yaml + env loader, validation
  modules/           # bounded contexts (see table above)
  shared/            # cross-module value types, errors, middleware, server, telemetry
  infrastructure/    # db (+ embedded migrations), messaging/{nats,valkey}, agones, assets, net
  testutil/          # shared test helpers
pkg/ro/              # RO protocol & data libraries (the kernel)
api/                 # per-BC NATS event contracts — rebuilt alongside the modules
deployments/         # agones, docker, kustomize, observability manifests
data/                # game data: mob_db, mob_spawns, npc
config.yaml          # default configuration (overridable per-env)
compose.yml          # mariadb + valkey + nats + goathena (+ observability profile)
```

## Reference: testing, schema, codegen, lint

### Tests (build tags)

```bash
task test-unit        # go test -race -tags=unit ./internal/... ./pkg/...   (60% coverage gate)
task test-integration # go test -race -tags=integration ./...               (needs mariadb+valkey+nats)
```

Tests carry a `//go:build unit | integration | e2e` tag. Unit tests are
hermetic; integration tests spin the real MariaDB/Valkey/NATS from compose.

### Migrations

`goathena migrate up` applies the embedded SQL in
`internal/infrastructure/db/migrations` (11 waves, MariaDB-first). The DSN scheme
selects the engine: `mysql://` for MariaDB, `postgres://` for PostgreSQL.
Create new ones with `task migrate-create NAME=add_users`. Migrations that touch
login/char tables must stay read-compatible with the legacy
`rathena/sql-files/main.sql` schema.

### Lint & format

`task lint` runs `golangci-lint run --timeout=5m ./...` (v2): `wrapcheck`,
`errcheck` (with `check-type-assertions: true`), `exhaustive`, `gocyclo` ≤ 15,
`funlen` ≤ 120, `nestif`, `gocritic`, `gosec`, `revive`, `depguard`,
`testifylint`, and more. Errors from outside the package must be wrapped with
`fmt.Errorf("...: %w", err)`. `task fmt` runs `gofumpt -w . && goimports -w .`;
CI also checks `gofmt -s`. `task tidy && task verify` fails if `go.mod`/`go.sum`
have diff.

## Deployment

- `deployments/docker/` — the `Containerfile` builds the single `goathena`
  image (`serve` by default; override `command: ["migrate", "up"]` for the init
  container).
- `deployments/kustomize/` — Kubernetes manifests (base + overlays).
- `deployments/agones/` — Agones `Fleet` / `GameServer` CRDs for the game world.
- `deployments/observability/` — OpenTelemetry Collector + Prometheus configs.

## Reference: rAthena

goAthena's source of truth for legacy RO behavior — packet formats, the script
dialect, map file formats, the DB schema, and game-data YAMLs — is the upstream
[rAthena](https://github.com/rathena/rathena) C/C++ codebase. It's checked out
locally as `third_party/rathena` and is read for reference only; nothing from it
is vendored into goAthena.

Quick map (where to look in rAthena for a given concern):

| Concern | rAthena path |
|---|---|
| Packet parse / stream crypto | `src/map/clif.cpp`, `src/common/des.cpp`, `src/common/socket.cpp` |
| Login / accounts | `src/login/` |
| Character server / inter-server comms | `src/char/` |
| Map server / pathfinding / script VM | `src/map/` |
| Shared utilities (timer, sql, grf, md5) | `src/common/` |
| Game DBs | `db/` |
| Script corpus | `npc/` |
| SQL schema | `sql-files/main.sql` |

## License

[GPLv3](https://www.gnu.org/licenses/gpl-3.0.html).
