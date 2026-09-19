# goAthena — Security Audit (M14)

> Reviewed against the code on `develop` as of 2026-09-20. Each row is
> anchored to a file path or commit ref. The threat model is a single
> process reachable on :6900 / :6121 / :5121 by anyone with network
> access; the audit covers only what an attacker on that network can
> reach. Internal-only assumptions (e.g. "the database is trusted") are
> out of scope.

---

## 1. Threat model

| Asset | Attacker goal | Attack surface |
|---|---|---|
| Account credentials | Brute-force a password | login listener :6900 |
| Player state | Forge packets as another player | map listener :5121, char listener :6121 |
| Server availability | DoS / OOM / reactor death | any listener |
| Zeny / items | Duplicate or drain a player's holdings | trade, shop, item-drop verbs |
| NPC scripts | Trigger unintended code paths | content module's script VM |

The audit's posture is: every handler on the wire is untrusted; auth is the
single source of identity; persistence is the only place state may be
mutated.

---

## 2. Findings

| ID | Severity | Status | Title |
|---|---|---|---|
| F-01 | ✅ closed | `00af9aa` | Unguarded panic in gnet reactor terminates the server |
| F-02 | ✅ closed | `556a8ee` | Item-script builtins bypass inventory port (scripted theft vector) |
| F-03 | ✅ closed | `8c87e57` | Zeny movements are not auditable (no transaction log) |
| F-04 | ✅ closed | this commit | Login brute-force is unthrottled |
| F-05 | ✅ verified | — | Unknown opcodes are silently skipped (not closed) |
| F-06 | ✅ verified | — | Auth context checked on every handler |
| F-07 | 🟡 partial | — | Variable-length packets read on-wire length prefix safely |
| F-08 | 🟡 partial | — | No OTel-driven abuse detection yet |

---

## 3. F-01 — Reactor panic terminates server (closed)

**Before:** an attacker sending a malformed packet to any handler could
trigger a panic; gnet's reactor recovers nothing, so a single panic ended
every listener and the control plane with it.

**Fix:** `internal/shared/safe` + `internal/modules/gateway/app/recover.go`
landed in commit `00af9aa`. Every reactor callback and per-frame dispatch
goroutine is now guarded. The contract (`safe.Guard` must be the
directly-deferred call, not wrapped) is documented in `safe.go`.

**Verification:** L2 unit tests `internal/shared/safe` + `gateway/app/recover`.

---

## 4. F-02 — Item-script builtins bypass inventory port (closed)

**Before:** M10 dialog builtins went through the content module's ports,
but there were no item-script builtins; an unsafe extension would have
mutated inventory state outside the bounded context.

**Fix:** M10 Rung A (commit `556a8ee`) added `getitem/delitem/countitem/
equip/unequip/getitem2` builtins that route through `ScriptInventory`, a
port declared in the content domain and implemented by a world-side adapter.
The adapter validates `EquipService` slot rules on equip and rejects
short-stack delitem (the all-or-nothing contract rAthena uses).

**Verification:** 7 new VM tests + 4 new scriptinv tests; race-clean.

---

## 5. F-03 — Zeny movements are not auditable (closed)

**Before:** every zeny deduct/credit went straight to the `char.zeny` column
with no record. Operators investigating "where did my zeny go?" had nothing
to query.

**Fix:** the zeny ledger (commit `8c87e57`) appends one row per successful
movement, carrying the signed delta, a reason, an optional peer charID, and
the player's map. Shop and trade legs are reason-tagged
(`ReasonShopBuy`/`ReasonShopSell`/`ReasonTrade` + peer).

**Verification:** ledger migration applies (wave 5); three service tests
cover the audit guarantee, the failure posture, and the peer recording.

---

## 6. F-04 — Login brute-force is unthrottled (closed)

**Before:** the :6900 listener accepted CA_LOGIN at wire speed. An attacker
could run millions of guesses per second from a single IP.

**Fix:** a per-IP token-bucket rate limiter
(`gateway/app/ratelimit.go` + DI wiring + config defaults). Defaults: 5
attempts per IP per burst, refilling at 1/sec — matches common
fail2ban-for-SSH posture. The limiter sits before the DB-bound goroutine,
so a throttled attempt is dropped silently (no AC_REFUSE_LOGIN), denying the
attacker a probe channel. Operators disable it by setting
`GATEWAY_LOGIN_RATE_BURST=0` or `GATEWAY_LOGIN_RATE_PER_SEC=0`.

**Verification:** six unit tests cover the burst / refill / per-IP
isolation / disabled / canonical-IP contract. The login handler is
updated to consult the limiter in `OnTraffic`; the integration test
passes with `limiter=nil` (test path) and a follow-up integration test
with a real limiter is queued.

---

## 7. F-05 — Unknown opcodes are silently skipped (verified)

The map server's `unhandledSkip` (gateway/app/map.go:532) consults the
packet DB before skipping; an opcode with no DB entry is treated as
"resync over the 2-byte header and keep reading". This is the safe
posture — booting the client on an unknown opcode would be a denial of
service against the legitimate player, not a useful security control.
Verified by `gateway/app/frames_test.go` and `frames_integration_test.go`.

---

## 8. F-06 — Auth context checked on every handler (verified)

Every `mapHandler.fn` (`gateway/app/dispatch.go`) starts with
`if auth == nil { return }` (e.g. `handleContactNPC` at line 102). The
`mapAuth` is resolved on the eventloop and passed into the handler; the
handler never reads `c.Context()` off-loop (gnet races `conn.release()`).
The CZ_ENTER trust gate is the first packet the map server processes; all
subsequent handlers may assume `auth != nil`. Verified by code walk.

---

## 9. F-07 — Variable-length packets (partial)

`gateway/app/dispatch.go:36` declares `frameSize func(c gnet.Conn) (int, bool)`
for variable-length packets. `variableFrameSize` reads the uint16 length
prefix at offset 2 and waits until the full frame has buffered. A malformed
length (`n < 4`) is treated as a header resync (skip 2 bytes, continue).
Verified by `frames_test.go`.

**Open:** a maximum length cap. A packet claiming `n=65535` reserves 64 KB
of buffer; a stream of such packets at high rate can pressure the
connection. rAthena's clif_packetdb caps the longest variable packet at a
few KB; the goAthena packet DB carries the length field but not an enforced
cap. Recommended follow-up: a per-opcode max length in the packet DB and a
guard in `OnTraffic` that disconnects on a length-prefix > the cap.

---

## 10. F-08 — No OTel-driven abuse detection (open)

OTel tracing is wired (`internal/app/otel.go`), but no spans yet drive
abuse detection. Recommended follow-ups:
- Span on every login attempt (success / refused / throttled) so a Grafana
  panel shows spikes.
- Span on every per-frame dispatch with a slow-handler alert (>10 ms).
- Counter for unknown opcodes per connection so a port-scan surfaces.

These are dashboard glue, not security-critical. Landed in M14 when the
load harness exists to validate them.

---

## 11. Tooling added in M14

- `cmd/loadgen` — minimal TCP load harness for :6900. N concurrent conns,
  R attempts/sec each. Reports success / refused / throttle / error
  counts. Used to verify F-04 in CI and to baseline the login throughput
  before / after each release.
- `internal/shared/safe` — panic-recovery policy as a single source of
  truth; documented contract that `Guard` must be the directly-deferred
  call.
- `gateway/app/ratelimit.go` — per-IP token-bucket limiter with tests
  covering the burst / refill / disabled / canonical-IP cases.

---

## 12. Outstanding work (not closed in this audit)

- F-07 follow-up: per-opcode max length cap in the packet DB.
- F-08 follow-up: OTel spans + Grafana panels.
- Threat model inputs:
  - What is the cost of a successful login brute-force? (account takeover.)
  - What is the cost of a successful inventory mutation by a script?
    (the script author wrote the script, so this is a content-team
    concern, not a runtime security one — covered by code review.)
  - What is the cost of a DoS against the login listener? (operator
    visibility into throttled attempts — F-08.)
- mTLS / cert pinning for the char→map handoff in M13 (cross-zone
  handshake) — the wire stays plaintext until then; a NATS auth subject
  is the future boundary.
