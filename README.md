# goAthena

[![CI](https://github.com/bouroo/goAthena/actions/workflows/ci.yml/badge.svg)](https://github.com/bouroo/goAthena/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/bouroo/goAthena)](https://goreportcard.com/report/github.com/bouroo/goAthena)
[![Go Version](https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go&logoColor=white)](https://go.dev/doc/go1.26)
[![License](https://img.shields.io/badge/license-GPLv3-blue.svg)](https://www.gnu.org/licenses/gpl-3.0.html)

A distributed, cloud-native emulator for **Ragnarok Online**, written in Go.

## What is this?

**Ragnarok Online (RO)** is a long-running Korean MMORPG whose private-server scene has been kept alive for two decades by community emulators. **rAthena** is the most widely used one — a mature, single-process C/C++ project that handles login, characters, and the game world in tightly coupled daemons.

**goAthena** is a from-scratch Go re-implementation of the same game logic, but redesigned for the cloud: instead of three monolithic daemons on one box, it's three independently deployable services connected over a message bus, scaled on Kubernetes, and capable of running thousands of concurrent players per game world. It preserves wire and data compatibility with rAthena so existing clients (the Korean RO client, *kRO*) and browser clients (*roBrowser*) just work, and existing NPC scripts and database schemas can be reused.

## Who is this for?

- **Curious developers** — anyone interested in MMORPG server architecture, distributed systems, or re-implementing legacy C++ in Go.
- **Contributors** — people who want to run goAthena locally and start hacking on it.
- **RO community** — server operators and scripters evaluating a modern alternative to rAthena.

## Prerequisites

- **Go 1.26+**
- **Docker** (or Podman)
- **[Task](https://taskfile.dev/installation/)** — the build runner used throughout
- A running **MariaDB 11.4+**, **Valkey 9+**, and **NATS 2.x** — easiest via `docker compose`
- **PostgreSQL** is also supported as an alternative database driver

## Quick start

```bash
cp .env.example .env                          # create your local config
docker compose up -d mariadb valkey nats      # start the three backing services
task migrate-up                               # apply the database schema

task run-identity                             # accounts & characters — HTTP 8080, gRPC 50051
task run-gateway                              # front door — TCP 6900 (kRO clients), WS 6901 (roBrowser), HTTP 8081, gRPC 50052
task run-zone                                 # the live game world — HTTP 8082, gRPC 7121 (Agones-managed)
```

Each `task run-<service>` command builds and runs one binary. Run the ones you need; they're independent processes that talk to each other over gRPC and NATS.

## The services at a glance

goAthena is a multi-service monorepo. Every service has its own `cmd/<svc>/main.go` entry point and its own dependency-injection composition root.

| Service | What it does, in plain terms | Binary | Transport | State |
|---|---|---|---|---|
| **gateway** | The front door. Decrypts the kRO client's TCP traffic, translates roBrowser's WebSocket traffic, and routes sessions to the right backend service. | `cmd/gateway` | TCP + WebSocket + HTTP + gRPC | Stateless |
| **identity** | Accounts, logins, characters, and warehouses. The "who are you and what characters do you own" tier. | `cmd/identity` | HTTP + gRPC | Stateless |
| **zone** | The live game world. Runs map instances, movement, pathfinding, combat, NPC scripts, and the per-tick simulation loop. | `cmd/zone` | HTTP + gRPC | Stateful (Agones) |
| **migrate** | A one-shot CLI that applies or rolls back database migrations. | `cmd/migrate` | — | CLI |

The **script engine** — the parser and virtual machine that runs rAthena's NPC scripts — is a library embedded inside the zone service, not a separate binary. It hot-reloads in place via an atomic pointer swap, so you can edit a script and see the change without restarting the server.

### Port map

| Service | HTTP | gRPC | TCP / WS |
|---|---|---|---|
| gateway | `8081` | `50052` | TCP `6900` (kRO), WS `6901` (roBrowser) |
| identity | `8080` | `50051` | — |
| zone | `8082` | `7121` | — |
| MariaDB | — | — | `3306` |
| Valkey | — | — | `6379` |
| NATS | — | — | `4222` |

## Project status

goAthena has shipped all five planned phases. The core platform is in place: ingress, identity, script engine, physics/AOI, cluster scale, and QA. A few highlights:

- **20,000+ legacy NPC scripts** parse, compile, and hot-reload cleanly
- **2,000 concurrent players per zone** sustained at 50 ms ticks with substantial headroom
- **Multi-zone transit** — players can walk between maps running on different zone pods
- **Cloud-native** — designed for Kubernetes, orchestrated by [Agones](https://agones.dev/), autoscaled on player density

For the full phase-by-phase breakdown (deliverables, exit gates, and metrics), see [`.agents/plans/go-athena-emulator/project-plan.md`](.agents/plans/go-athena-emulator/project-plan.md).

## Architecture

### Multi-service clean architecture

Each service follows clean architecture inside every feature package under `internal/features/<name>/`:

```
domain/      entities + ports (interfaces) — no external deps
repository/  GORM implementation of the outbound port (MariaDB or PostgreSQL driver)
service/     use-case implementation of the inbound port
handler/     transport (gnet TCP / WebSocket / echo HTTP / gRPC)
di/          Register(injector) wires the feature into the container
dto/         request/response shapes (where applicable)
```

Composition uses [`samber/do/v2`](https://github.com/samber/do): every layer exposes `Register(c *do.Injector) error`. Each service has its own composition root in `internal/app/<svc>/app.go` that wires the dependency-injection (DI) container; `cmd/<svc>/main.go` is a thin entry point that loads config, sets build-time vars (`Version`, `CommitSHA`, `BuildTime`), and calls the service's `Run`.

Bootstrap order:

```
config → telemetry → infrastructure (db/nats/valkey as needed) → shared servers → features
```

`internal/app/common/` provides shared bootstrap: signal handling, config loading, telemetry init, and version metadata. Configuration is loaded from `config.yaml` and the environment (no prefix) into a typed, validated struct via `spf13/viper` + `go-playground/validator`. Each service reads only the config blocks it needs — see [`.env.example`](.env.example) for the full key list.

### RO protocol libraries (`pkg/ro/`)

Reusable, publicly importable Ragnarok-Online-domain packages with **zero `internal/` dependencies** — meaning external tools (load testers, packet analyzers, replay tools) can use them without pulling in the rest of the codebase.

| Package | What it does |
|---|---|
| `pkg/ro/packet` | Packet structures, `packet_db` parser, `PACKETVER` schema merge |
| `pkg/ro/crypto` | Stream decryption (rolling pseudo-RNG — a deterministic number generator seeded per session) |
| `pkg/ro/script` | Script types, opcodes, scope definitions |
| `pkg/ro/romap` | `.gat`/`.rsw`/`.gnd` map-file loaders → walkability and height grids |
| `pkg/ro/aoi` | **AOI** (Area of Interest) tower-grid engine — 18×18 cells, adaptive squeezing |
| `pkg/ro/pathfinding` | **A\*** (a best-first pathfinding algorithm) on the walkability grid |

### Project layout

```
goAthena/
├── cmd/
│   ├── gateway/main.go               # gateway (TCP/WS ingress)
│   ├── identity/main.go              # identity service
│   ├── zone/main.go                  # zone service (Agones)
│   └── migrate/main.go               # database migration runner
├── internal/
│   ├── app/                          # per-service composition roots
│   │   ├── common/                   # shared bootstrap (signal, config, telemetry)
│   │   ├── gateway/                  # gateway DI wiring
│   │   ├── identity/                 # identity DI wiring
│   │   └── zone/                     # zone DI wiring
│   ├── config/                       # typed multi-service config
│   ├── features/
│   │   ├── gateway/                  # packet codec, TCP/WS ingress
│   │   ├── identity/                 # login, char, warehouse
│   │   ├── zone/                     # map instances, AOI, tick loop
│   │   └── script/                   # parser + VM (embedded in zone)
│   ├── infrastructure/
│   │   ├── db/                       # MariaDB (GORM) + migrations
│   │   ├── messaging/{nats,valkey}/  # NATS pub/sub + Valkey sessions/locks
│   │   ├── net/                      # kRO packet codec, stream crypto
│   │   ├── assets/                   # GRF decoder, asset cache
│   │   └── agones/                   # Agones SDK wrapper
│   ├── shared/{errors,middleware,server,telemetry}/
│   └── testutil/
├── pkg/ro/                           # public RO protocol libraries (see above)
├── api/{proto,pb}/                   # protobuf source + generated code
├── deployments/{agones,kustomize,observability,docker}/
├── test/e2e/
├── compose.yml                       # MariaDB, NATS, Valkey, services
├── config.yaml
├── Taskfile.yml
└── go.mod                            # github.com/bouroo/goAthena
```

## Reference

### Testing

Every test file carries a build tag — `//go:build unit | integration | e2e` — and **`go test ./...` with no tag runs zero tests.** Always pass `-tags=unit` (or the appropriate tag); `task test` defaults to `unit`.

| Task | What it does |
|---|---|
| `task test` / `task test-unit` | Unit tests only (default). Hermetic — mocked with sqlmock + `go.uber.org/mock`. |
| `task test-integration` | Requires live mariadb + valkey + nats. Migrations run first. |
| `task test-e2e` | Boots the full server cluster (`test/e2e/`). |

Single-package / single-test (raw `go`, you supply the tag):

```bash
go test -race -tags=unit -run TestName ./internal/features/gateway/service/...
go test -race -tags=integration ./internal/features/identity/repository/...
```

CI enforces a **60% coverage gate** on `./internal/... ./pkg/...` — don't drop coverage.

### Migrations

Two equivalent paths, kept in sync:

- `task migrate-up` / `task migrate-down` — uses the `migrate` CLI pointed at `internal/infrastructure/db/migrations`.
- `go run ./cmd/migrate up` — a self-contained binary that `go:embed`s the same SQL files (used by CI and the `Containerfile.migrate` image).

Create new ones with `task migrate-create NAME=add_users` (writes to `internal/infrastructure/db/migrations`).

The Identity Service must be read-compatible with the legacy rAthena schema at `rathena/sql-files/main.sql`. When creating migrations that touch login/char tables, cross-reference the legacy schema first.

**Multi-DB support.** MariaDB is the primary driver (`db.driver: mariadb`, using `gorm.io/driver/mysql`). PostgreSQL is supported as an alternative (`db.driver: postgres`, using `gorm.io/driver/postgres`). The DB init layer selects the GORM (Go ORM) driver based on config; repository code is dialect-agnostic. Migrations are MariaDB-first; PostgreSQL migrations live in `internal/infrastructure/db/migrations/postgres/` when needed.

### Code generation

- **Mocks** — `go:generate` directives in feature `domain/{service,repository}.go` files produce `*/mock/*_mock.go` via `mockgen`. Run `go generate ./...` after touching a port interface, or tests won't compile.
- **Protobuf** — `api/pb/**` is generated from `api/proto/**`. Run `task proto` after editing `.proto` files.
- `api/pb/` and `*/mock/` are excluded from lint and formatting — do not hand-edit.

`task generate` runs `go generate ./...` and then `task proto` in one shot. CI runs it before tests; if you skip it locally your tree will diverge.

### Lint & format

`task lint` runs `golangci-lint run --timeout=5m ./...` (v2). It enforces `wrapcheck`, `errcheck` (with `check-type-assertions: true`), `exhaustive`, `gocyclo` ≤ 15, `funlen` ≤ 120, `nestif`, `gocritic`, `gosec`, `revive`, and `testifylint`. Errors from outside the package must be wrapped with `fmt.Errorf("...: %w", err)`.

`task fmt` runs `gofumpt -w . && goimports -w .` — run it before committing; CI checks `gofmt -s`.

`task tidy` then `task verify` tidies modules and fails if `go.mod`/`go.sum` have diff.

### Deployment

- `Containerfile` — multi-stage distroless/non-root server image.
- `Containerfile.migrate` — self-contained migration binary that `go:embed`s SQL files.
- `compose.yml` — local stack (mariadb, nats, valkey, and the services).
- `deployments/kustomize/` — Kubernetes manifests (base + overlays).
- `deployments/agones/` — Agones `Fleet` / `GameServer` Custom Resource Definitions (CRDs) for the zone service.
- `deployments/observability/` — OpenTelemetry Collector + Prometheus configs.

## Reference: rAthena

goAthena's source of truth for legacy RO behavior — packet formats, the script dialect, map file formats, the DB schema, and game-data YAMLs — is the upstream [rAthena](https://github.com/rathena/rathena/tree/7f080871c8b3bbe7a79027194633201c63422ee1) C/C++ codebase. It's checked out locally as `third_party/rathena` (outside this repo) and is read for reference only; nothing from it is vendored into goAthena.

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