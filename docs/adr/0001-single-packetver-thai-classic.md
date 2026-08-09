# ADR 0001: Target a single PACKETVER (Thai Classic 20250604), not multi-client

- **Status:** Accepted
- **Date:** 2026-08-09
- **Commit:** `d257fd5`

## Context

rAthena gates packet formats, opcodes, and obfuscation on a compile-time
`PACKETVER` macro, with four variants (`PACKETVER`, `PACKETVER_MAIN_NUM`,
`PACKETVER_RE_NUM`, `PACKETVER_ZERO_NUM`) distinguishing the main, renewal, and
zero (pre-re) clients. goAthena's first compatibility target is the Thai
Classic client (`ClientROThailand`) at `PACKETVER 20250604` — a single,
fixed, post-cutoff version on the MAIN path with no RE range set
(`pkg/ro/packet/char.go:17-26`).

Choosing one PACKETVER has a large simplifying payoff: `20250604` is strictly
greater than `20180307`, so `KeysForVersion` returns `(0,0,0)`
(`pkg/ro/crypto/obfuscation.go:111-121`, `pkg/ro/crypto/obfuscation_keys.go:220`).
The packet-id stream codec becomes an identity transform — no per-session
key scheduling, no encode/decode state machine on the hot path. This is the
core compatibility thesis: keep the *protocol and data* identical while
re-expressing the server in idiomatic Go.

## Decision

Build for **one** PACKETVER (`20250604`) only. Packet databases are
hand-curated for the Thai Classic slice (`NewCharServerDB` et al. in
`pkg/ro/packet/`) rather than generated for arbitrary versions, and the
crypto package's identity path is the only one exercised in production. The
`packetdb` parser's single-value `ForPacketVer` evaluation
(`pkg/ro/packetdb/doc.go:30-41`) treats all `*_NUM` predicates against that
one integer.

## Consequences

- **Positive:** No stream-crypto state on the gnet eventloop; simpler, faster
  codec path. A smaller, auditable packet surface. EXP rides the 64-bit
  `ZC_LONGLONGPAR_CHANGE` (`0x0acb`) rather than the legacy 32-bit form.
- **Negative:** roBrowser / WebSocket and any non-Thai-Classic client are
  **not yet supported**. Multi-client support is deferred to the M8 milestone
  and requires lifting the single-PACKETVER assumption and adding per-session
  version selection (the `packetdb` doc marks this "deferred to N2",
  `pkg/ro/packetdb/doc.go:27-28`).
- **Note:** `pkg/ro/packetdb/doc.go:41` references a "D-012 in the project
  decision log" that does not exist in the repo; this ADR supersedes that
  dangling reference.

## References

- `docs/roadmap.md` §1 (Executive summary)
- `pkg/ro/packet/char.go:17-26`, `pkg/ro/packet/char.go:75-87`
- `pkg/ro/crypto/obfuscation.go:111-121`, `pkg/ro/crypto/obfuscation_keys.go:214-220`
- `pkg/ro/packetdb/doc.go:30-41`
