// Package protocol resolves the Ragnarok Online wire protocol for a specific
// client variant.
//
// rAthena's protocol is not a flat table of opcodes: every opcode, every
// struct layout, and every field width is guarded by preprocessor conditionals
// over PACKETVER_MAIN_NUM, PACKETVER_RE_NUM, PACKETVER_ZERO_NUM, and the
// RENEWAL balance axis. Upstream ships three client families — the "main"
// (Ragexe) client, the "re" (RagexeRE) client, and the "zero" (RagexeZero)
// client — and a single server build selects one of them at compile time.
//
// This package resolves that selection at runtime instead: a Variant names the
// client family, and the tables below carry the resolved opcode ids, wire
// lengths, and struct layouts for that family. Nothing here guesses a value;
// every number is derived from upstream's own headers (see the codegen in
// pkg/ro/protocol/codegen).
//
// # Why a table and not a struct-per-packet
//
// Upstream defines ~2000 wire structs whose layouts vary per client family.
// Hand-porting them is how protocol drift enters a server: a field silently
// keeps the wrong width and the client desynchronises. Resolving the tables
// from upstream's headers keeps the C++ source the single source of truth.
package protocol

// Variant identifies a rAthena client family.
//
// The numeric values match the upstream PACKETVER_*_NUM selection: exactly one
// of the three is defined for any given build, and upstream's conditionals test
// them against date thresholds. A Variant therefore selects a distinct set of
// opcodes and layouts.
type Variant int8

const (
	// VariantMain is the "main" (Ragexe) client family. Upstream defines
	// PACKETVER_MAIN_NUM for it.
	VariantMain Variant = iota
	// VariantRE is the "re" (RagexeRE) client family. Upstream defines
	// PACKETVER_RE_NUM for it.
	VariantRE
	// VariantZero is the "zero" (RagexeZero) client family. Upstream selects
	// it with defined(PACKETVER_ZERO) rather than a numeric threshold.
	VariantZero
)

// String reports the variant name, matching upstream's spelling so log lines
// and error messages line up with upstream's own terminology.
func (v Variant) String() string {
	switch v {
	case VariantMain:
		return "main"
	case VariantRE:
		return "re"
	case VariantZero:
		return "zero"
	default:
		return "unknown"
	}
}

// Variants holds every client family the server can target, by name.
//
// The operator names the client family in the config, so the name
// resolves to the family the wire protocol comes from.
var Variants = map[string]Variant{
	VariantMain.String(): VariantMain,
	VariantRE.String():   VariantRE,
}

// PacketVer is a client protocol date (YYYYMMDD), the same form upstream uses
// for PACKETVER.
type PacketVer int

// Upstream master's shipped default. Upstream selects the RE client family at
// this date (see upstream src/config/packets.hpp: the RE range is
// 20200902 <= PACKETVER <= 20211118).
const PacketVerRE20211103 PacketVer = 20211103

// RE selects the RE client family for v.
//
// Upstream defines PACKETVER_RE when the date falls in one of its two RE
// ranges: (20151104, 20180704) exclusive, or [20200902, 20211118] inclusive.
// The second range is what upstream master's default (20211103) lands in.
//
// The bool result reports whether v selects the RE family at all; when false
// the caller should use VariantMain.
func RE(v PacketVer) (Variant, bool) {
	switch {
	case v > 20151104 && v < 20180704:
		return VariantRE, true
	case v >= 20200902 && v <= 20211118:
		return VariantRE, true
	default:
		return VariantMain, false
	}
}
