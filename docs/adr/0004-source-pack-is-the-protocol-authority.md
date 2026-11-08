# 0004 — The source pack is the authority: resolve opcodes from the source's runtime table

Status: Accepted

## Context

goAthena speaks the RO client protocol. The protocol is defined by the
rAthena source tree the server is built against, and two source trees exist:

- `third_party/rathenaThailand` — rAthena-Thailand/rathenaThailand, PACKETVER
  20250604. The primary target: the Thai Classic client.
- `third_party/rathena` — rAthena/rathena master. The compatibility set.

The two forks are close but not identical, and their differences are easy to
get wrong. The port of the Thai fork onto this branch found roughly fifteen
places where the branch's hand-derived wire values disagreed with the
source's own:

| Packet | Branch's value | Source's value |
|---|---|---|
| `CZ_REQ_LEAVE_GUILD` | 52 | 54 |
| `ZC_FRIENDS_LIST_sub` | 8 | 32 |
| `ZC_ITEM_FALL_ENTRY` | 24 | 22 |
| `ZC_SKILLINFO_LIST` entry | 37 | 15 |
| `ZC_USE_SKILL_TOPOS` | 11 | 10 |
| `ZC_GUILD_INFO` name offset | 17 | 46 |
| `CZ_ACTION_REQUEST` | 0x0089 | 0x0437 |
| `CZ_MOVE_ITEM_FROM_BODY_TO_STORE` | 0x00f3 | 0x0364 |
| `CZ_CLOSE_STORE` | 0x07e5 | 0x0193 |

Two of those were not merely off: `0x00f3` is `CZ_GLOBAL_MESSAGE` in the
source's runtime table, and `0x008b` — which the branch registered as
`ZC_ACTION_RESPONSE` — the source registers as a two-byte packet with no
handler at all. A client built for the source cannot read either frame.

The deeper problem is *how* an opcode is resolved. The source's
`clif_packetdb.hpp` lists the same opcode under different handlers across
client versions, and the source's runtime resolves this with
`packetdb_addpacket`, which overwrites an opcode's whole entry
(`third_party/rathenaThailand/src/map/clif.cpp:25899-25906`). So:

- the source's runtime table is not the first block of `clif_packetdb.hpp`,
  and
- one handler owns several opcodes, so "the opcode for this handler" is not
  a single fact.

Reading the top block of the registration header — what a human does when
skimming the file — gives the wrong answer for most of the modern client's
opcodes.

## Decision

**The source pack is the authority, and the source's own runtime table is
the authority for opcodes.**

Three consequences:

1. **Every opcode and length resolves from the installed source pack at
   boot.** `pkg/ro/protocol` holds one `Set` per (source pack, client
   family); the packet layer resolves every opcode and wire length through
   it. Nothing is a compile-time constant that depends on the fork.

2. **The opcode for a parse handler is the source's *last* registration of
   that handler**, narrowed by the request's wire shape. The source's
   registration order is the file order, and `packetdb_addpacket` overwrites
   an opcode's entry, so the last registration is the one the resolved
   PACKETVER speaks. Where the client-version variants differ in shape, the
   caller states the shape it parses — the frame length and the field
   offsets the source's struct carries — and the lookup keeps only the
   registrations that match.

3. **The generated tables come from the source's own compiler.**
   `cmd/protocolgen` compiles the source's registration headers and emits
   the resolved table and the registration order. The generator is
   source-aware: it takes `-source thailand|upstream` and writes into a
   per-source package.

The rule is not inferred from opcode numbering. It is derived from the
source's own registration order and the source's own struct shapes.

### Verification

The rule was checked against the source's runtime table, reproduced by
compiling the source's registration headers into a probe that mirrors
`clif_parse_ActionRequest` and `packetdb_addpacket` exactly. The probe and
the generated tables agree on every opcode.

The generator's own output is now pinned: `scripts/verify-protocolgen.sh`
regenerates each source pack's tables from its checkout and compares the
resolved values to the committed ones. All ten files match, so the
committed tables and the generator cannot drift apart unnoticed.

## Consequences

- A packet the branch has no constant for still resolves: the source's
  packet table names it, and the port reads it from there.
- Adding a fork is a submodule plus a generator run; no per-fork code.
- The packet layer's headers are functions, not constants. A package-level
  var would resolve at `init()`, before the boot installs the set, so the
  resolution must happen after the install.
- A wire-shape mismatch is a build-time panic, not a silent misparse.

## References

- `pkg/ro/protocol/set.go` — `OpcodeByHandler`, `OpcodeForHandler`.
- `pkg/ro/packet/resolve.go` — the packet layer's resolution helpers.
- `cmd/protocolgen/` — the source-aware generator.
- `third_party/rathenaThailand/src/map/clif.cpp:25899-25906` —
  `packetdb_addpacket`, the overwrite that makes the last registration win.
- `third_party/rathenaThailand/src/map/clif_packetdb.hpp` — the source's
  registration headers.
- `third_party/rathenaThailand/src/map/clif.cpp:11396-11397` — the doc
  comment above `clif_parse_WalkToXY` that names both client variants.
