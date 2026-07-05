# goAthena

[![CI](https://github.com/bouroo/goAthena/actions/workflows/ci.yml/badge.svg)](https://github.com/bouroo/goAthena/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/bouroo/goAthena)](https://goreportcard.com/report/github.com/bouroo/goAthena)
[![Go Version](https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go&logoColor=white)](https://go.dev/doc/go1.26)
[![License](https://img.shields.io/badge/license-GPLv3-blue.svg)](https://www.gnu.org/licenses/gpl-3.0.html)

A distributed, cloud-native Ragnarok Online emulator in Go.

goAthena re-engineers the legacy rAthena C/C++ emulator (login/char/map daemons) into a distributed platform built around three independently deployable services — **gateway**, **identity**, and **zone** — connected via NATS pub/sub, fronted by a MariaDB-backed identity tier and a Valkey-backed session/lock tier, and orchestrated on Kubernetes through Agones for stateful game-server lifecycles.

The canonical rAthena C/C++ source lives at [rathena](https://github.com/rathena/rathena) (outside this repo) and is the system of record for legacy behavior. No rAthena source is vendored here.

## Prerequisites

- Go 1.26+
- Docker or Podman
- [Task](https://taskfile.dev/installation/)
- MariaDB 11.4+ (run via container). PostgreSQL is also supported as an alternative DB driver.
- Valkey 9+ (run via container)
- NATS 2.x+ (run via container)

## Quick start

```bash
cp .env.example .env
docker compose up -d mariadb valkey nats
task migrate-up

task run-gateway   # TCP 6900 (kRO clients) + WSS 443 (roBrowser)
task run-identity  # HTTP 8080 + gRPC 50051
task run-zone      # gRPC 50052 (Agones-managed)
```

Each service is a separate binary; run the ones you need. `task run-<service>` builds and runs the binary in one step.

## Services

goAthena is a multi-service monorepo. Each service has its own `cmd/<svc>/main.go` entry point and DI composition root.

| Service | Binary | DEL | Statefulness | Transport | Role |
|---|---|---|---|---|---|
| **gateway** | `cmd/gateway` | DEL-01 | Stateless | TCP (gnet) + WebSocket | kRO packet parse/decrypt, WSS for roBrowser, gRPC routing |
| **identity** | `cmd/identity` | DEL-02 | Stateless | HTTP (echo) + gRPC | Login, character CRUD, warehouse locking |
| **zone** | `cmd/zone` | DEL-03 | Stateful (Agones) | gRPC + Agones SDK | Map instances, AOI tower-grid, pathfinding, tick loop |
| **migrate** | `cmd/migrate` | — | CLI | — | DB migration runner |

The **script engine** (DEL-04) is a library embedded in the zone service — not a separate binary. Hot-reload is done via in-process atomic pointer swap.

## Progress

### Phase 1 — Ingress & Protocol (WS-A / DEL-01)

- [ ] D1. `gnet` TCP socket pool + connection lifecycle
- [ ] D2. kRO packet parser + `PACKETVER` schema merge
- [ ] D3. Stream decryption (rolling pseudo-RNG)
- [ ] D4. WebSocket ingress (`/ws/`) for roBrowser
- [ ] D5. Gateway→service gRPC contract

**Exit gate:** 5,000 req/s decryption verified; packet DB indexed; gRPC contract frozen.

### Phase 2 — Auth & Identity (WS-B / DEL-02)

- [ ] D6. Auth DB connector (legacy schema read-compat)
- [ ] D7. gRPC APIs (authenticate, char CRUD, slot management)
- [ ] D8. Session issuance + Valkey session store
- [ ] D9. Warehouse distributed lock (`SET NX PX`, anti-dupe)

**Exit gate:** Concurrent warehouse writes produce zero races / zero dupes across mock nodes.

### Phase 3 — Script Engine & VM (WS-D / DEL-04)

- [ ] D16. Lexical scanner (tab-delimited header + body modes)
- [ ] D17. `goyacc` grammar → AST (`script`/`warp`/`monster`/`mapflag`)
- [ ] D18. Stack-based bytecode compiler (`OpPush`/`OpLoad`/`OpStore`/`OpJump`/`OpCallBuiltin`)
- [ ] D19. 5 scope namespaces (`.@var`, `var`, `#var`, `$var`, `$@var`)
- [ ] D20. Async yielding (`mes`/`menu`/`next`/`input`) + zero-downtime hot-reload

**Exit gate:** ≥500 legacy scripts run + hot-reloaded with zero dropped invocations.

### Phase 4 — Physics & AOI (WS-C / DEL-03)

- [ ] D10. Terrain loaders (`.gat`/`.rsw`/`.gnd` → walkability/height grids)
- [ ] D11. A* pathfinder + movement model (`CellWalkTime`/`NextMoveTick`)
- [ ] D12. Physics tick loop (movement, collision, combat hooks, AI)
- [ ] D13. AOI tower-grid engine (18×18 cells, "Update Many, Fetch One")
- [ ] D14. Adaptive AOI squeezing (15→8→5 cells at density >100) + network LOD + write coalescing
- [ ] D15. Agones Go SDK lifecycle (`Ready`/`Allocate`/`Shutdown`)

**Exit gate:** Simulation loop latency < 5ms per tick on benchmark maps.

### Phase 5 — Cluster Scale & QA (WS-E/F/G/H / DEL-05/06)

- [ ] D21. NATS subject contracts + pub/sub (transit, social, broadcast)
- [ ] D22. Valkey registry schemas (account/char hash-maps, single-writer-by-Zone locking)
- [ ] D23. Cross-zone player transit handshake
- [ ] D24. GRF decoder (`0x200`/`0x300`) + LRU asset cache + EUC-KR→UTF-8
- [ ] D25. Docker Compose local stack (gateway, identity, zone, NATS, Valkey, MariaDB)
- [ ] D26. Agones `Fleet`/`GameServer` manifests + autoscaler policy
- [ ] D27. CI/CD pipeline (build, test, image publish, deploy)
- [ ] D28. Structured logging + distributed tracing + metrics
- [ ] D29. Load-test harness (WOE-density: 2,000 entities/zone)
- [ ] D30. E2E suite (auth → char select → map enter → transit → warehouse) + compatibility vectors

**Exit gate:** 50ms ticks sustained with 2,000 players in one zone; autoscaler reclaims idle pods.

## Directory tree

```
goAthena/
├── cmd/
│   ├── gateway/main.go               # DEL-01: Ingress Gateway (TCP/WSS)
│   ├── identity/main.go              # DEL-02: Identity Service
│   ├── zone/main.go                  # DEL-03: Zone Service (Agones)
│   └── migrate/main.go               # DB migration runner
├── internal/
│   ├── app/                          # per-service composition roots
│   │   ├── common/                   # shared bootstrap (signal, config, telemetry)
│   │   ├── gateway/                  # gateway DI wiring
│   │   ├── identity/                 # identity DI wiring
│   │   └── zone/                     # zone DI wiring
│   ├── config/                       # typed multi-service config
│   ├── features/
│   │   ├── gateway/                  # WS-A: packet codec, TCP/WS ingress
│   │   ├── identity/                 # WS-B: login, char, warehouse
│   │   ├── zone/                     # WS-C: map instances, AOI, tick loop
│   │   └── script/                   # WS-D: parser + VM (embedded in zone)
│   ├── infrastructure/
│   │   ├── db/                       # MariaDB (GORM) + migrations
│   │   ├── messaging/{nats,valkey}/  # pub/sub + sessions/locks
│   │   ├── net/                      # kRO packet codec, stream crypto
│   │   ├── assets/                   # GRF decoder, asset cache
│   │   └── agones/                   # Agones SDK wrapper
│   ├── shared/{errors,middleware,server,telemetry}/
│   └── testutil/
├── pkg/ro/                           # public RO protocol libraries
│   ├── packet/                       # packet structs, packet_db, PACKETVER
│   ├── crypto/                       # stream decryption
│   ├── script/                       # types, opcodes, scopes
│   ├── romap/                        # .gat/.rsw/.gnd loaders
│   ├── aoi/                          # tower-grid AOI engine
│   └── pathfinding/                  # A*
├── api/{proto,pb}/                   # protobuf source + generated code
├── deployments/{agones,kustomize,observability}/
├── test/e2e/
├── compose.yml                       # MariaDB, NATS, Valkey, services
├── config.yaml
├── Taskfile.yml
└── go.mod                            # github.com/bouroo/goAthena
```

## Architecture

### Multi-service clean architecture

Each service follows clean architecture inside every feature package under `internal/features/<name>/`:

```
domain/      entities + ports (interfaces) — no external deps
repository/  GORM implementation of the outbound port (MariaDB driver (mysql wire protocol))
service/     use-case implementation of the inbound port
handler/     transport (gnet TCP / WebSocket / echo HTTP / gRPC)
di/          Register(injector) wires the feature into the container
dto/         request/response shapes (where applicable)
```

Composition uses **samber/do/v2**: every layer exposes `Register(c *do.Injector) error`. Each service has its own composition root in `internal/app/<svc>/app.go` that wires the DI container. `cmd/<svc>/main.go` is a thin entry point that loads config, sets build-time vars (`Version`, `CommitSHA`, `BuildTime`), and calls the service's `Run`.

Bootstrap order:

```
config → telemetry → infrastructure (db/nats/valkey as needed) → shared servers → features
```

`internal/app/common/` provides shared bootstrap: signal handling, config loading, telemetry init, version metadata.

Configuration is loaded from `config.yaml` and the environment (no prefix) into a typed, validated struct via `spf13/viper` + `go-playground/validator`. Each service reads only the config blocks it needs — see `.env.example` for the full key list.

### RO protocol libraries (`pkg/ro/`)

Reusable, publicly importable RO-domain packages with **zero `internal/` dependencies**:

| Package | Responsibility |
|---|---|
| `pkg/ro/packet` | Packet structures, `packet_db` parser, `PACKETVER` schema merge |
| `pkg/ro/crypto` | Stream decryption (rolling pseudo-RNG) |
| `pkg/ro/script` | Script types, opcodes, scope definitions |
| `pkg/ro/romap` | `.gat`/`.rsw`/`.gnd` file loaders → walkability/height grids |
| `pkg/ro/aoi` | Tower-grid AOI engine (18×18 cells, adaptive squeezing) |
| `pkg/ro/pathfinding` | A* on walkability grid |

These are importable by external tooling (load testers, packet analyzers, replay tools). Never add `internal/` imports to `pkg/ro/`.

## Reference: rAthena

The canonical rAthena C/C++ source is located at [rathena](https://github.com/rathena/rathena). It is the system of record for legacy RO behavior — packet formats, the script dialect, map file formats, the DB schema, and game-data YAMLs. Read it for reference; do not copy or vendor it.

Quick map:

| Concern | rAthena path |
|---|---|
| Packet parse / stream crypto | `src/map/clif.cpp`, `src/common/des.cpp`, `src/common/socket.cpp` |
| Login / accounts | `src/login/` |
| Character server / inter-server comms | `src/char/` |
| Map server / pathfinding / script VM | `src/map/` |
| Shared utilities | `src/common/` |
| Game DBs | `db/` |
| Script corpus | `npc/` |
| SQL schema | `sql-files/main.sql` |

The full legacy→Go service mapping, deliverables, workstreams, and phased exit gates live in `.agents/plans/go-athena-emulator/project-plan.md`.

## Testing

Test build tags are mandatory. Every test file carries `//go:build unit | integration | e2e`. Plain `go test ./...` runs zero tests — always pass `-tags=unit` (or the appropriate tag).

| Task | What it does |
|---|---|
| `task test` / `task test-unit` | Unit tests only (default). Hermetic, mocked (sqlmock + `go.uber.org/mock`). |
| `task test-integration` | Requires live mariadb + valkey + nats. Migrations run first. |
| `task test-e2e` | Boots the full server cluster (`test/e2e/`). |

Single-package / single-test:

```bash
go test -race -tags=unit -run TestName ./internal/features/gateway/service/...
go test -race -tags=integration ./internal/features/identity/repository/...
```

CI enforces a **60% coverage gate** on `./internal/... ./pkg/...` — don't drop coverage.

## Deployment

- `Containerfile` — multi-stage distroless/non-root server image.
- `Containerfile.migrate` — self-contained migration binary that `go:embed`s SQL files.
- `compose.yml` — local stack (mariadb, nats, valkey, and the services).
- `deployments/kustomize/` — Kubernetes manifests (base + overlays).
- `deployments/agones/` — Agones Fleet / GameServer CRDs for the zone service.
- `deployments/observability/` — OpenTelemetry Collector + Prometheus configs.

## Migrations

Two equivalent paths, kept in sync:

- `task migrate-up` / `task migrate-down` — uses the `migrate` CLI pointed at `internal/infrastructure/db/migrations`.
- `go run ./cmd/migrate up` — self-contained binary that `go:embed`s the same SQL files (used by CI and the `Containerfile.migrate` image).

Create new ones with `task migrate-create NAME=add_users` (writes to `internal/infrastructure/db/migrations`).

The Identity Service must be read-compatible with the legacy rAthena schema at `rathena/sql-files/main.sql`. When creating migrations that touch login/char tables, cross-reference the legacy schema first.

**Multi-DB support:** MariaDB is the primary driver (`db.driver: mariadb`, uses `gorm.io/driver/mysql`). PostgreSQL is supported as an alternative (`db.driver: postgres`, uses `gorm.io/driver/postgres`). The DB init layer selects the GORM driver based on config; repository code is dialect-agnostic. Migrations are MariaDB-first; PostgreSQL migrations live in `internal/infrastructure/db/migrations/postgres/` when needed.

## Code generation

- Mocks: `go:generate` directives in feature `domain/{service,repository}.go` files produce `*/mock/*_mock.go` via `mockgen`. Run `go generate ./...` after touching a port interface, or tests won't compile.
- Protobuf: `api/pb/**` is generated from `api/proto/**`. Run `task proto` after editing `.proto` files.
- `api/pb/` and `*/mock/` are excluded from lint and formatting — do not hand-edit.

`task generate` runs `go generate ./...` and then `task proto` in one shot.

## Lint

`task lint` runs `golangci-lint run --timeout=5m ./...` (v2). It enforces `wrapcheck`, `errcheck` (with `check-type-assertions: true`), `exhaustive`, `gocyclo` ≤ 15, `funlen` ≤ 120, `nestif`, `gocritic`, `gosec`, `revive`, and `testifylint`. Errors from outside the package must be wrapped with `fmt.Errorf("...: %w", err)`.

`task fmt` runs `gofumpt -w . && goimports -w .` — run it before committing; CI checks `gofmt -s`.

`task tidy` then `task verify` tidy modules and fail if `go.mod`/`go.sum` have diff.