# Login-load baseline (M14)

Recorded baseline for the login path (`:6900` CA_LOGIN → authenticate →
session store → AC_ACCEPT/REFUSE). Re-record per release machine; numbers are
workstation-local, the pass criterion is the SHAPE (zero errors, limiter
shaped as configured), not the absolute rate.

## How to reproduce

```sh
# profiles A–C: limiter disabled for a capacity signal (the target sets
# GATEWAY_LOGIN_RATE_BURST=0 on the container)
LOADGEN_CONNS=100 LOADGEN_RATE=10 LOADGEN_DURATION=30s task loadtest

# profile D: production-shaped limiter (compose defaults, 5 burst / 1 per s)
docker compose up -d goathena   # wait for /readyz :8080, seed per Taskfile
go run ./cmd/loadgen -conns 2 -rate 2 -duration 15s
```

## Environment

- goAthena @ the commit stamped in handoff §5 (this baseline's code commit)
- compose local tier: `mariadb:11.8` + `valkey:9` + `nats:2-alpine` +
  `goathena` (distroless, 36 MB) on Docker Desktop, Apple Silicon (Darwin 25.6)
- each login exercises: packet parse → MD5 auth (MariaDB read) → Valkey
  `PutSession` → AC_ACCEPT_LOGIN encode/write

## Results (2026-09-21)

| Profile | Conns × rate | Duration | Limiter | Success | Refused | Throttle | Errors |
|---|---|---|---|---|---|---|---|
| A | 10 × 2/s | 30s | off | 590 | 0 | 0 | 0 |
| B | 50 × 5/s | 30s | off | 7450 | 0 | 0 | 0 |
| C | 100 × 10/s | 30s | off | 29900 | 0 | 0 | 0 |
| D | 2 × 2/s | 15s | **5 burst / 1 per s** | 18 | 0 | 10 | 0 |

- C sustains **≈1000 logins/s** with zero errors on this workstation — the
  login path is not the bottleneck at hobbyist scale.
- D shows the brute-force limiter shaped exactly as configured: the 5-token
  burst plus ~1/s refill passes, everything above it is silently dropped and
  reported as throttle (a denial is silence by design — no reply, so the
  attack cannot probe the limiter).

## What the first baseline run caught (fixed in the same commit)

`cmd/loadgen` had never actually round-tripped against the modern server —
every earlier run misclassified:

1. **Double cmd header** — the encoder prepended a manual `0x0064` on top of
   `CALoginRequest.Encode`, which already writes the full 55-byte frame. The
   wire got 57 bytes; the server's fixed-size framing read an empty username
   and then `cmd 0x0000` garbage frames.
2. **Stale refuse opcode** — classified `0x006a` (pre-2017); the server
   answers `0x083e` at PACKETVER 20250604, so every refused login counted as
   "throttle".
3. **Partial-frame reads** — only the 2-byte header was consumed per reply,
   leaving each reply's tail to misalign the next read on the same
   connection. The accept carries its total wire length at offset 2; the
   classifier now drains the full frame.
4. **Timeout classification** — a limiter drop (no reply) surfaced as a read
   timeout; it is now `throttle`, not `error`, and detected via
   `errors.As` (the ctx-reader wrapper hides the concrete `net.Error`).
