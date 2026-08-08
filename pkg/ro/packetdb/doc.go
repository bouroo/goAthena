// Package packetdb parses rAthena's authoritative map-server packet
// definition source, third_party/rathena/src/map/clif_packetdb.hpp, and
// produces a typed Go registry that goAthena's gateway can use for
// PACKETVER-aware packet dispatch.
//
// # Scope (N1)
//
// The parser accepts the direct-numeric subset of clif_packetdb.hpp — packet
// entries whose command ID is a literal hex constant 0xNNNN. For a given
// PACKETVER, the registry flattens the version-gated entries (those inside
// #if / #elif / #else / #endif blocks) and returns a *packet.DB containing
// only the entries whose predicate evaluates true for that PACKETVER.
//
// # Deferred (documented)
//
// The following categories of entries are explicitly out of scope for N1
// and must not be silently lost: they are counted by ParseStats so a
// future parser version can verify they are eventually resolved:
//
//   - Symbolic HEADER_* / sizeof(struct PACKET_*) entries (~144 entries)
//     require C preprocessor emulation or a hand-maintained lookup table.
//     Deferred to N1.1.
//   - *Type alias entries (~8 entries) require resolving C++ typedefs from
//     clif.hpp / packets.hpp. Deferred to N1.1.
//   - clif_shuffle.hpp (4763 lines of version-keyed on-wire remapping) is
//     deferred to N1.2.
//   - Per-session PACKETVER selection (currently the caller passes a
//     single PACKETVER value) is deferred to N2.
//
// # PACKETVER assumption (single value)
//
// rAthena's predicates reference four variants of PACKETVER:
// PACKETVER, PACKETVER_MAIN_NUM, PACKETVER_RE_NUM, and PACKETVER_ZERO_NUM.
// They distinguish the main client, the renewal (re) client, and the zero
// (pre-re) client at compile time. For N1, ForPacketVer receives a single
// integer; all *_NUM predicates are evaluated against that single value.
// defined(PACKETVER_ZERO) is treated as true (the operator has chosen a
// PACKETVER, so the zero client path is potentially active). This is the
// conservative rule: it may include entries rAthena would exclude for a
// specific variant, but it will never exclude entries rAthena would
// include. The decision is recorded in D-012 in the project decision log.
//
// # Known rAthena source quirks
//
// clif_packetdb.hpp line 1303 contains a typo: "#if PACKETVER >= 2009122"
// (seven digits, missing one). The evaluator accepts any decimal integer
// after ">=", and the comparison is well-defined regardless of intent.
//
// # Relationship to pkg/ro/packet
//
// ForPacketVer produces a *packet.DB whose values are packet.Definition
// records (no version gate), so the gateway can continue to use db.Lookup
// and db.Length without change. PacketRegistry itself carries the
// version-gated Entry list that the flattener walks.
package packetdb
