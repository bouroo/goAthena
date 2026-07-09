# AGENTS.md

Repo-specific guidance for AI coding agents working on **goAthena** — a distributed, cloud-native Ragnarok Online emulator in Go.
Verify anything here against `Taskfile.yml`, `.golangci.yml`, `.github/workflows/ci.yml`, and `.agents/plans/go-athena-emulator/` — those are the executable source of truth.

## Reference: rAthena source (legacy reference implementation)

The canonical rAthena C/C++ source is checked out **outside this repo** at:

```
third_party/rathena
```

This is the system of record for legacy RO behavior — packet formats, the script dialect, map file formats, the DB schema, and game-data YAMLs. When implementing a Go equivalent, read the corresponding rAthena file first and treat its semantics as correct unless an explicit design decision (recorded in `.agents/plans/go-athena-emulator/decision-log.md`) says otherwise. Do **not** copy or vendor rAthena source into this repo — read it for reference only.

Quick map (legacy → concern → where to look):

| Concern | rAthena path |
|---|---|
| Packet parse / stream crypto | `src/map/clif.cpp`, `src/common/des.cpp`, `src/common/socket.cpp` |
| Login / accounts | `src/login/` (`login.cpp`, `loginclif.cpp`, `account.cpp`) |
| Character server / inter-server comms | `src/char/` (`char.cpp`, `inter.cpp`, `int_*.cpp`) |
| Map server / pathfinding / script VM | `src/map/` (`map.cpp`, `path.cpp`, `script.cpp`, `pc.cpp`, `mob.cpp`, `skill.cpp`) |
| Shared utilities (timer, sql, grf, md5) | `src/common/` |
| Game DBs (skill/item/mob/quest) | `db/` (`re/`, `pre-re/`, `import-tmpl/`) |
| Script corpus (compatibility fixtures) | `npc/` |
| SQL schema | `sql-files/main.sql` |

The full legacy→Go service mapping, deliverable IDs (DEL-01..06), workstreams (WS-A..H), phased exit gates, and risk matrix live in `.agents/plans/go-athena-emulator/project-plan.md`.

## Toolchain

- Go 1.26+. Module path: `github.com/bouroo/goAthena`.
- [Task](https://taskfile.dev) is the command runner. Prefer `task <name>` over raw `go` commands — they encode build flags, ldflags, and coverage flags you will get wrong by hand.
- Local services (MariaDB 11.4 LTS, Valkey 9, NATS 2.x) run via containers: `docker compose up -d mariadb valkey nats`. PostgreSQL is also supported as an alternative DB driver (see Migrations section).

## Services (multi-service monorepo)

goAthena is a **multi-service monorepo**. Each service is a separate binary with its own `cmd/` entry point and DI composition root.

| Service | Binary | DEL | Statefulness | Transport | Role |
|---|---|---|---|---|---|
| **gateway** | `cmd/gateway` | DEL-01 | Stateless | TCP (gnet) + WebSocket | kRO packet parse/decrypt, WSS for roBrowser, gRPC routing |
| **identity** | `cmd/identity` | DEL-02 | Stateless | HTTP (echo) + gRPC | Login, character CRUD, warehouse locking |
| **zone** | `cmd/zone` | DEL-03 | Stateful (Agones) | gRPC + Agones SDK | Map instances, AOI tower-grid, pathfinding, tick loop |
| **migrate** | `cmd/migrate` | — | CLI | — | DB migration runner |

The **script engine** (DEL-04) is a library embedded in zone-service — not a separate binary. Hot-reload via in-process atomic pointer swap. See `decision-log.md` D-003.

## Commands

| Task | What it does |
|---|---|
| `task build` | Build all service binaries into `bin/`. |
| `task build-gateway` / `build-identity` / `build-zone` | Build a single service. |
| `task run-gateway` / `run-identity` / `run-zone` | Build + run a single service. |
| `task test` / `task test-unit` | Unit tests only. **This is the default test command.** |
| `task test-integration` | Requires live mariadb + valkey + nats. |
| `task test-e2e` | Boots the full server cluster. |
| `task lint` | `golangci-lint run --timeout=5m ./...` |
| `task fmt` | `gofumpt -w . && goimports -w .` |
| `task tidy` + `task verify` | Tidy, then fail if `go.mod`/`go.sum` have diff. |
| `task generate` | `go generate ./...` (mockgen) **then** `task proto` (protoc). |
| `task proto` | Regenerate `api/pb/` from `api/proto/`. |
| `task migrate-up` / `migrate-down` / `migrate-create NAME=...` | golang-migrate CLI against `$DB_*` env (MariaDB). |

Single-package / single-test (raw go, must pass the tag yourself):

```bash
go test -race -tags=unit -run TestName ./internal/features/gateway/service/...
go test -race -tags=integration ./internal/features/identity/repository/...
```

## Test build tags are mandatory

Every test file carries `//go:build unit | integration | e2e`. **Plain `go test ./...` runs zero tests.** Always pass `-tags=unit` (or the appropriate tag). `task test` defaults to `unit`.

- `unit` — hermetic, mocked (sqlmock + `go.uber.org/mock` mocks).
- `integration` — needs mariadb + valkey + nats; migrations are run first.
- `e2e` — under `test/e2e/`, boots the whole cluster.

## Generated code — regenerate, never hand-edit

- Mocks: `go:generate` directives in feature `domain/{service,repository}.go` files produce `*/mock/*_mock.go` via `mockgen`. Run `go generate ./...` after touching a port interface, or tests won't compile.
- Protobuf: `api/pb/**` is generated from `api/proto/**`. Run `task proto` after editing `.proto` files.
- `api/pb/` and `*/mock/` are excluded from lint and formatting — do not "fix" them.

CI runs `go generate ./...` before tests; if you skip it locally your tree will diverge.

## CI gates (`.github/workflows/ci.yml`)

Order: **lint → unit → integration → build**. Each stage depends on the previous.

- Lint stage also runs `go mod tidy` and fails on `go.mod`/`go.sum` diff, plus `gofmt -s -l .` must be empty.
- Unit stage enforces a **60% coverage gate** on `./internal/... ./pkg/...`. Don't drop coverage.
- Integration stage runs `go run ./cmd/migrate up` (the self-contained migrator binary, not the CLI) before tests.
- Branches: `main`, `develop`.

## Lint is strict (`.golangci.yml`, golangci-lint v2)

- `wrapcheck` — errors from outside the package must be wrapped (`fmt.Errorf("...: %w", err)`). Exceptions: echo `Context.JSON`, `errors.GRPCErr`, `status.Error`.
- `errcheck` with `check-type-assertions: true`.
- `exhaustive` — switch over enums must cover all cases.
- `gocyclo` ≤ 15, `funlen` ≤ 120 lines, `nestif`, `gocritic`, `gosec`, `revive`, `testifylint`.
- Run `task fmt` (gofumpt + goimports) before committing — CI checks `gofmt -s`.

## Architecture

### Multi-service clean architecture

Each service follows **clean architecture** inside each feature package under `internal/features/<name>/`:

```
domain/      entities + ports (interfaces) — no external deps
repository/  GORM implementation of the outbound port (MariaDB driver)
service/     use-case implementation of the inbound port
handler/     transport (gnet TCP / WebSocket / echo HTTP / gRPC)
di/          Register(injector) wires the feature into the container
dto/         request/response shapes (where applicable)
```

Composition uses **samber/do/v2**: every layer exposes `Register(c *do.Injector) error`. Each service has its own composition root in `internal/app/<svc>/app.go` that wires the DI container; `cmd/<svc>/main.go` is a thin entry point that loads config, sets build-time vars (Version/CommitSHA/BuildTime), and calls the service's `Run`. Bootstrap order:

```
config → telemetry → infrastructure (db/nats/valkey as needed) → shared servers → features
```

`internal/app/common/` provides shared bootstrap (signal handling, config loading, telemetry init, version metadata).

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

These are importable by external tooling (load testers, packet analyzers). Never add `internal/` imports to `pkg/ro/`.

### Infrastructure providers (`internal/infrastructure/`)

| Package | Adapter for |
|---|---|
| `db/` | MariaDB via GORM (mysql driver) + golang-migrate; PostgreSQL optional via `db.driver` config |
| `messaging/nats/` | NATS pub/sub client (inter-service comms) |
| `messaging/valkey/` | Valkey client (sessions, registries, distributed locks) |
| `net/` | kRO packet codec, stream crypto (shared gateway/zone) |
| `assets/` | GRF decoder, LRU cache, EUC-KR→UTF-8 |
| `agones/` | Agones Go SDK lifecycle (`Ready`/`Allocate`/`Shutdown`) |

Configuration: `config.yaml` + environment variables (no prefix) → typed struct via viper + go-playground/validator. Each service reads only the config blocks it needs. See `.env.example` for the full key list.

## Migrations (MariaDB)

Two equivalent paths — keep them in sync:

- `task migrate-up` — uses the `migrate` CLI pointed at `internal/infrastructure/db/migrations`.
- `go run ./cmd/migrate up` — self-contained binary that `go:embed`s the same SQL files (used by CI and the container image `Containerfile.migrate`).

Create new ones with `task migrate-create NAME=add_users` (writes to `internal/infrastructure/db/migrations`).

**Multi-DB support:** MariaDB is the primary driver (`db.driver: mariadb`, uses `gorm.io/driver/mysql`). PostgreSQL is supported as an alternative (`db.driver: postgres`, uses `gorm.io/driver/postgres`). The DB init layer selects the GORM driver based on config; repository code is dialect-agnostic. Migrations are MariaDB-first (MySQL dialect); PostgreSQL migrations live in `internal/infrastructure/db/migrations/postgres/` when needed.

The Identity Service must be read-compatible with the legacy rAthena schema at `rathena/sql-files/main.sql`. When creating migrations that touch login/char tables, cross-reference the legacy schema first.

## Project structure

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

## Adding a new feature

1. Create `internal/features/<name>/` with `domain/`, `service/`, `handler/`, `di/` subpackages.
2. Define ports (interfaces) in `domain/`, implementations in `service/`/`handler/`.
3. Add `di.Register(injector) error` that wires the feature.
4. Call `di.Register(injector)` in the appropriate service's composition root (`internal/app/<svc>/app.go`).
5. Add a config block to `config.yaml` / `.env.example` if the feature needs configuration.
6. Run `go generate ./...` if you added mock-generating interfaces.
