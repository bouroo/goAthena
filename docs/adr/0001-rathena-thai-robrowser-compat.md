# ADR 0001: rathenaThailand / ClientROThailand config audit → goAthena disposition

- **Status:** Accepted
- **Date:** 2026-08-01
- **Context:** `third_party/rathenaThailand` (server, submodule, upstream
  `github.com/rAthena-Thailand/rathenaThailand`, `ignore = all`) and
  `third_party/ClientROThailand` (client, submodule, `.git` disabled) are
  vendored **reference** trees, not deployment targets. goAthena is the
  Go reimplementation that must stay behaviorally compatible with them for
  both the native client and roBrowser. An audit of their login/char/map
  server `.conf` files and the client's `clientinfo.xml` / Warp2025 patch
  metadata found 10 issues that would block or degrade a real rAthena
  deployment. This ADR records how each finding maps onto goAthena's own
  config surface: already solved architecturally, fixed here, or explicitly
  out of scope with a reason.

Neither submodule is edited by this change — see the decision log in
`.agents/plans/rathena-robrowser-fix/state.json` for why (foreign upstream
push authority, disabled git checkout). All decisions below land in
goAthena's own tracked config (`config.yaml`, `internal/config/config.go`).

## Findings and disposition

| # | rAthena/client finding | goAthena disposition | Evidence |
|---|---|---|---|
| 1 | Single hardcoded `PACKETVER` compiled into the binary (`src/config/packets.hpp:16` = `20250604`); roBrowser targets an older packet layout, so packet field order/length mismatches would desync the map phase. | **Already solved, architecturally superior.** `gateway.packetver_min`/`packetver_max` (`internal/config/config.go:132-142`) allow per-session PACKETVER selection instead of one compile-time constant; a `CA_LOGIN` outside the range falls back to `gateway.packetver`. | `internal/config/config.go:126-142`, `config.yaml` `gateway.packetver*` |
| 2 | `char_ip`/`map_ip` commented out in `char_athena.conf`/`map_athena.conf`; rAthena silently auto-detects the first network interface, which is wrong on multi-interface hosts and unreachable for any client not on that host. | **Fixed here (U2).** `gateway.map_addr`/`map_ws_addr` already require an explicit value (no auto-detect fallback exists), but had no comment warning operators that the `localhost` default only works for a same-host client. Added deployment-warning comments. | `config.yaml` `gateway.map_addr`/`map_ws_addr` comments; `internal/config/config.go` `MapAddr`/`MapWSAddr` doc comments |
| 3 | `subnet_athena.conf` only covers `127.0.0.0/8`; LAN/WAN players fall outside the rule and get the raw (wrong) advertised map IP. | **N/A - no equivalent mechanism.** goAthena has no subnet-rewrite layer; `MapAddr` is a single explicit value advertised to every client (see #2). No fix needed beyond #2's operator guidance. | — |
| 4 | `clientinfo.xml` declares `langtype=0` (Korean) against a `tis620` (Thai) server codepage; wrong `langtype` changes the client's login-packet password-encryption path and text handling. | **Already solved, architecturally superior.** `gateway.text_codepage` is a per-**connection** choice, not a single global one: native TCP sessions use `TextCodepage` (default `utf-8`, can be `windows-874`/`tis-620` for Thai Classic), WebSocket/roBrowser sessions always use UTF-8 regardless. One deployment can correctly serve both transports at once. | `internal/modules/gateway/infra/ws.go:69`; `internal/config/config.go` `TextCodepage` doc |
| 5 | `new_account: no` blocks `_M`/`_F` client-side auto-registration; every account needs manual SQL insertion. | **Out of scope - not a config knob.** Account creation policy is identity-service application logic (`internal/modules/account/app/service.go`), not a value in `config.yaml`. Auditing/changing that policy is a feature decision, not a config port; left for a separate task if desired. | `internal/modules/account/app/service.go` |
| 6 | `use_web_auth_token: yes` + a separate web-server binary (port 8888) required once `PACKETVER > 20200300`. | **N/A for roBrowser.** roBrowser does not use the web-auth-token flow; it is a native-client-only 2020+ feature. No goAthena config change needed for roBrowser compatibility specifically. | `third_party/rathenaThailand/src/config/packets.hpp:92` (`WEB_SERVER_ENABLE`) |
| 7 | `pincode_enabled: yes`; roBrowser's char-select often lacks the pincode packet handshake, and 3 failed attempts lock the account. | **Out of scope - not implemented as enforcement logic yet.** The `pincode`/`pincode_change` columns exist in the identity schema (`internal/infrastructure/db/migrations/000002_identity.up.sql:20-21`) but no service-layer enforcement was found in `internal/modules/account`. There is nothing to disable; flagging here so pincode enforcement, if/when built, defaults to off or roBrowser-aware rather than inheriting rAthena's default-on behavior blindly. | `internal/infrastructure/db/migrations/000002_identity.up.sql:20-21` |
| 8 | Client `DATA.ini` ships with an empty `[Data]` section (no GRF load order), so the native client has no packed asset source. | **N/A - client-tree only, and goAthena serves assets differently.** `assets.grf_dir`/`assets.enabled` (`config.yaml`) already model a GRF-backed HTTP asset server for roBrowser; the vendored client's own `DATA.ini` is outside goAthena's tracked trees per this ADR's scope decision. | `config.yaml` `assets:` |
| 9 | Inter-server credentials default to publicly-known `sv1`/`pv1`. | **N/A - different mechanism, already dev-scoped.** goAthena's inter-service auth is gRPC-based (`gateway.identity_addr`, `gateway.zone_addr`), not a shared conf-file password; `db.user`/`db.password` default to `goathena`/`goathena` and are already documented as local-dev defaults, not touched by this audit. | `config.yaml` `db:` |
| 10 | `inter_athena.conf` DB codepage (`tis620`) vs `clientinfo.xml` (`euc-kr`) mismatch corrupts non-ASCII names/chat. | **Already solved** - see #4; `TextCodepage` is validated and parsed via `textenc.ParseCodepage` per connection, decoupling the wire encoding from the DB storage encoding. | `internal/config/config.go` `TextCodepage` doc |

## Fixed in this change

- **`zone.renewal` default contradiction.** `config.yaml` already stated
  "rathenaThailand ships RENEWAL ON" but set `renewal: false`, so a default
  deployment loaded `db/pre-re/` against data generated for a server
  compiled with `RENEWAL` on (`third_party/rathenaThailand/src/config/core.hpp`
  does not define `PRERE`). Both `db/re/` and `db/pre-re/` exist in the
  vendored submodule, so flipping the default to `true` is safe and was
  applied in `config.yaml`, `internal/config/config.go` (`setDefaults` and
  the `Renewal` field doc), and `internal/config/config_test.go`.
- **Deployment-warning comments** for `gateway.map_addr` / `gateway.map_ws_addr`
  (finding #2 above).

## Known gap, not fixed here (documentation debt, flagged not fabricated)

`internal/config/config.go`'s `Packetver` field doc points to
`.agents/plans/rathena-compat-roadmap/subplans/n2-per-session-packetver.md`,
which does not exist on disk in this checkout. The per-session negotiation
mechanism it documents (`packetver_min`/`packetver_max`) is implemented and
tested regardless (see #1); only the referenced planning doc is missing.
Recreating that plan document is a separate, larger task and is intentionally
not fabricated here to avoid speculative-content scope creep - noting the gap
is the honest deliverable.

## Consequences

- Operators relying on the previous `renewal: false` default must now set
  `ZONE_RENEWAL=false` explicitly if they intentionally pair goAthena with a
  Pre-Renewal client/data tree.
- No runtime behavior changes for `map_addr`/`map_ws_addr` (comment-only);
  operators deploying off the gateway's own host still need to override
  them, exactly as before, but the requirement is now discoverable in-file.

## Addendum (follow-up audit, 2026-08-01): MapAddr bind/advertise conflation

A second-pass audit of the same rathenaThailand/ClientROThailand config
surface found a gap in the original finding #2 fix: `gateway.map_addr` /
`gateway.map_ws_addr` were documented as "advertised to the client, not
listened on here" (config.yaml), but `internal/app/composition.go` actually
used them as **both** the bind address for the map TCP/WS listeners
(`gwinfra.NewMapTCPHandler(...).Run("tcp://" + cfg.Gateway.MapAddr)`) and the
advertised zone address in `HC_NOTIFY_ZONESVR`
(`character/di.go: app.ParseZoneAddr(cfg.Gateway.MapAddr)`). This is a
narrower version of rAthena's `char_ip`/`map_ip` problem than finding #2
described: unlike stock rAthena, which splits `bind_ip` (what the process
binds to) from `char_ip`/`map_ip` (what's advertised), goAthena had no split
at all. The deployment advice added by the original fix — "override
`GATEWAY_MAP_ADDR` to a reachable LAN/WAN hostname or IP" — is unsafe advice
on its own: a public DNS name behind a router/NAT, or a Docker host port
that differs from the container's internal port, is reachable but not
bindable, so following that advice makes the map listener fail to start
(`net.Listen` on an address the host doesn't own) and every client hangs
after `CH_SELECT_CHAR`.

**Fixed:** Added `gateway.map_bind_addr` / `gateway.map_ws_bind_addr`
(env `GATEWAY_MAP_BIND_ADDR` / `GATEWAY_MAP_WS_BIND_ADDR`, both optional,
default `""`). `GatewayConfig.MapListenAddr()` / `MapWSListenAddr()` resolve
to the bind override when set, else fall back to the advertised address —
preserving today's default (same-host, bind-equals-advertise) behavior
exactly. `composition.go`'s map listeners and the gateway readiness checker
(`internal/modules/gateway/di.go`) now bind/dial through these resolvers
instead of the advertised address directly. This restores the rAthena
`bind_ip` vs `char_ip`/`map_ip` split for NAT, port-forwarded, and
Docker-published-port deployments — the common case for a real roBrowser
deployment reachable over the internet, not just the same-host dev default.

Evidence: `internal/config/config.go` (`MapBindAddr`, `MapWSBindAddr`,
`MapListenAddr`, `MapWSListenAddr`), `internal/app/composition.go`,
`internal/modules/gateway/di.go`, `internal/config/config_test.go`
(`TestGatewayConfig_MapListenAddr`, `TestLoad_Defaults` additions).
