# ADR 0002: Cache and resolve per-connection auth on the gnet eventloop

- **Status:** Accepted
- **Date:** 2026-08-09
- **Commit:** `d257fd5` (authored in `9f72b68`, "fix conn-context data race")

## Context

gnet v2 gives each connection a `Context()` slot, and the goAthena gateway
uses it to cache the authed identity after a verified enter handshake:
`charAuth` on the char server (`internal/modules/gateway/app/char.go:200`)
and `mapAuth` on the map server (`internal/modules/gateway/app/map.go:185`).
Later frames read that cache to know which account/character owns the
connection without re-verifying the handshake.

The gateway dispatches each fully-arrived frame to a handler on a **separate
goroutine** (`go h.fn(s, c, auth, cp)` at `internal/modules/gateway/app/map.go:94`)
so the reactor never blocks on DB or world work. But `gnet.Conn`'s context
slot is mutated by the eventloop (e.g. `conn.release()` on close), and reading
it off-loop races that mutation. A data race was found and fixed in `9f72b68`.

## Decision

**Resolve the auth cache exactly once, on the eventloop, and pass it by value
into the off-loop handler.** `OnTraffic` calls `authFromConn(c)`
(`internal/modules/gateway/app/map.go:93`, `dispatch.go:421-427`) before
spawning the handler goroutine; the handler signature receives `*mapAuth`
(`dispatch.go:33`) and must **not** read `c.Context()` itself. The comment at
`internal/modules/gateway/app/map.go:90-92` records this invariant.

## Consequences

- **Positive:** The gnet context slot is only touched from the single goroutine
  that owns it, eliminating the close-race. Handlers are concurrency-safe
  without locks on the auth cache. The pattern is uniform across char and map
  servers.
- **Positive:** The frame buffer is detached (`cp := append([]byte(nil), frame...)`)
  at `map.go:89` for the same reason — the handler owns its copy.
- **Negative:** Any future handler that reaches for `c.Context()` directly
  reintroduces the race; the invariant is documented but not enforced by the
  type system. Adding a lint rule or wrapper type is a future hardening.
- **Negative:** `mapAuth`/`charAuth` are value types cached on the conn, so
  fields added there must stay safe to copy.

## References

- `internal/modules/gateway/app/map.go:89-94`, `:181-192`
- `internal/modules/gateway/app/dispatch.go:30-33`, `:419-428`
- `internal/modules/gateway/app/char.go:200`, `:230-231`
- Commit `9f72b68` (conn-context data race fix)
