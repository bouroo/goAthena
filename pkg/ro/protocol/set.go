package protocol

import (
	"fmt"
	"slices"

	"github.com/bouroo/goAthena/pkg/ro/protocol/generated/thailand"
	"github.com/bouroo/goAthena/pkg/ro/protocol/generated/upstream"
)

// Source names the source pack the server speaks.
//
// A source is a rAthena fork: the Thai fork is the primary target and
// upstream is the compatibility set. The two forks' packet tables differ by
// one packet and their wire layouts are identical, but the difference is
// real, so the server resolves its constants from the selected source rather
// than from one hard-coded set.
type Source string

const (
	// SourceThailand is the Thai fork (rAthena-Thailand/rathenaThailand).
	//
	// The fork targets the main client family at PACKETVER 20250604.
	SourceThailand Source = "thailand"
	// SourceUpstream is upstream (rathena/rathena) master.
	//
	// Upstream targets the RE client family at PACKETVER 20211103.
	SourceUpstream Source = "upstream"
)

// String reports the source's name.
func (s Source) String() string { return string(s) }

// Sources holds every source pack the server can target, by name.
//
// The operator names the source pack in the config, so the name resolves
// to the pack the wire protocol comes from.
var Sources = map[string]Source{
	string(SourceThailand): SourceThailand,
	string(SourceUpstream): SourceUpstream,
}

// Checkout returns the path of the source pack's checkout.
//
// A source pack is a checkout of a rAthena fork, under third_party. The
// checkout holds the fork's scripts (npc) and its game data (db).
func (s Source) Checkout() string {
	switch s {
	case SourceUpstream:
		return "third_party/rathena"
	case SourceThailand:
		return "third_party/rathenaThailand"
	default:
		return ""
	}
}

// Set holds one source's resolved protocol constants.
//
// The set is the runtime form of the generated tables: the server resolves
// its opcodes, layouts and packet table from the selected source at boot, so
// a deployment speaks the protocol of the fork it was built for.
//
// The set is read-only after it is resolved.
type Set struct {
	// source is the source pack the set came from.
	source Source
	// variant is the client family the set resolved.
	variant Variant

	// packets is the resolved client-to-server packet table.
	packets Table
	// registrations is the packets in the source's own registration order.
	registrations []Packet
	// runtime is the opcode-to-handler map the source's own runtime builds.
	//
	// The source registers each packet with packetdb_addpacket, which
	// overwrites the opcode's entry (src/map/clif.cpp:25899-25906), and
	// the registration headers list the same opcodes under different
	// handlers across client versions. So the handler an opcode ends up
	// with is the one its LAST registration names — and that is the
	// handler the client reaches.
	runtime map[uint16]string
	// opcodes holds the resolved packet-header opcode ids.
	opcodes map[string]int
	// layouts holds the resolved on-wire size of every wire struct.
	layouts map[string]int
	// offsets holds the resolved field offsets of the structs the server
	// writes field by field.
	offsets map[string]map[string]int
}

// Resolve returns the protocol set for a source and a client family.
//
// The bool result reports whether the source is one the server knows.
func Resolve(source Source, variant Variant) (*Set, bool) {
	switch source {
	case SourceThailand:
		return thailandSet(variant), true
	case SourceUpstream:
		return upstreamSet(variant), true
	default:
		return nil, false
	}
}

// Source returns the set's source pack.
func (s *Set) Source() Source { return s.source }

// Variant returns the client family the set resolved.
func (s *Set) Variant() Variant { return s.variant }

// Table returns the resolved client-to-server packet table.
func (s *Set) Table() Table { return s.packets }

// Registrations returns the packets in the source's own registration order.
//
// The order matters: the source registers some handlers under several
// opcodes across client versions, so the last registration of a handler is
// the one the client uses at the resolved PACKETVER.
func (s *Set) Registrations() []Packet { return s.registrations }

// Opcode returns the resolved opcode id of a packet-header constant.
//
// The bool result reports whether the source declares the constant at all.
func (s *Set) Opcode(name string) (int, bool) {
	v, ok := s.opcodes[name]
	return v, ok
}

// OpcodeByHandler returns the opcode the source ends up registering a parse
// handler for.
//
// The source registers each packet with packetdb_addpacket, which overwrites
// the opcode's whole entry (src/map/clif.cpp:25899-25906), and the
// registration headers list the same opcodes under different handlers across
// client versions. So an opcode ends up with the handler its LAST
// registration names, and one handler owns several opcodes — the
// client-version variants of one request.
//
// The lookup walks the registrations in the source's own order and returns
// the LAST registration of the wanted handler: the one for the resolved
// PACKETVER. When the variants differ in wire shape, the caller states the
// shape it parses with OpcodeForHandler, which is narrower.
//
// The bool result reports whether the source registers the handler at all.
func (s *Set) OpcodeByHandler(handler string) (uint16, bool) {
	var found uint16
	ok := false
	for _, p := range s.registrations {
		if p.Handler != handler || s.runtime[p.Opcode] != handler {
			continue
		}
		found = p.Opcode
		ok = true
	}
	return found, ok
}

// OpcodeForHandler returns the opcode the source's RUNTIME packet table
// dispatches a parse handler on for a request of the given wire shape.
//
// The source registers each packet with packetdb_addpacket, which overwrites
// the opcode's whole entry (src/map/clif.cpp:25899-25906). So the handler an
// opcode ends up with is the one its LAST registration names, and that is
// the handler the client reaches. One handler can own several opcodes — the
// client-version variants of one request — and they differ in wire shape,
// so the caller states the shape it parses (the frame length and the field
// offsets the source's struct has).
//
// The lookup keeps the registrations whose opcode's final handler is the
// wanted one AND whose length and field offsets match the wanted shape, and
// returns the LAST of them: the registration for the resolved PACKETVER.
//
// The bool result reports whether the source registers the handler with
// that shape at all.
func (s *Set) OpcodeForHandler(handler string, length int, offsets []int) (uint16, bool) {
	var found uint16
	ok := false
	for _, p := range s.registrations {
		if p.Handler != handler || s.runtime[p.Opcode] != handler {
			continue
		}
		if p.Length != length || !sameOffsets(p.Offsets, offsets) {
			continue
		}
		found = p.Opcode
		ok = true
	}
	return found, ok
}

// OpcodesForHandler returns every opcode the source ends up dispatching a
// parse handler on for a request of the given wire shape.
//
// One handler owns several opcodes: the source registers it under each
// client build's opcode, and the runtime keeps them all. The client that
// connects decides which one it sends, and the source's runtime accepts
// every one of them, so the server does too.
//
// The registrations are walked in the source's own order, and only those
// whose opcode's FINAL handler is the wanted one AND whose length and field
// offsets match the wanted shape are kept.
func (s *Set) OpcodesForHandler(handler string, length int, offsets []int) []uint16 {
	var out []uint16
	for _, p := range s.registrations {
		if p.Handler != handler || s.runtime[p.Opcode] != handler {
			continue
		}
		if p.Length != length || !sameOffsets(p.Offsets, offsets) {
			continue
		}
		out = append(out, p.Opcode)
	}
	return out
}

// VariantOpcodesFor returns every opcode the source ends up dispatching a
// parse handler on for a request of the given wire shape.
//
// It is the same set OpcodeForHandler resolves, so a caller that knows the
// handler and the shape can accept every client build's opcode.
func (s *Set) VariantOpcodesFor(handler string, length int, offsets []int) []uint16 {
	return s.OpcodesForHandler(handler, length, offsets)
}

// ClientVariantOpcodes returns the source's other opcodes for the opcode a
// client sent.
//
// The source registers one handler under each client build's opcode, so a
// client may send any of them for the same request. The server accepts the
// whole set, the way the source's runtime does.
//
// The bool result reports whether the source registers the opcode at all.
func (s *Set) ClientVariantOpcodes(opcode uint16) []uint16 {
	p, ok := s.packets.Lookup(opcode)
	if !ok || p.Handler == "" {
		return nil
	}
	all := s.OpcodesForHandler(p.Handler, p.Length, p.Offsets)
	out := make([]uint16, 0, len(all))
	for _, o := range all {
		if o == opcode {
			continue
		}
		if slices.Contains(out, o) {
			continue
		}
		out = append(out, o)
	}
	return out
}

// sameOffsets reports whether a registration's field offsets are the wanted
// ones.
func sameOffsets(got, want []int) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// PacketLengthByHandler returns the on-wire length the source registers for
// the opcode a parse handler is bound to, or the bool result false when the
// source registers no handler at all.
//
// The length is the one the opcode's final registration carries, because
// packetdb_addpacket overwrites the opcode's whole entry.
func (s *Set) PacketLengthByHandler(handler string) (int, bool) {
	opcode, ok := s.OpcodeByHandler(handler)
	if !ok {
		return 0, false
	}
	return s.PacketLength(opcode)
}

// Layouts returns every resolved on-wire size of a wire struct.
//
// The map is the set's own; a caller that reads it must not write it.
func (s *Set) Layouts() map[string]int { return s.layouts }

// Layout returns the resolved on-wire size of a wire struct.
//
// The bool result reports whether the source declares the struct at all.
func (s *Set) Layout(name string) (int, bool) {
	v, ok := s.layouts[name]
	return v, ok
}

// Offsets returns the resolved byte offset of every field of a wire struct.
//
// The offsets come from the source's own compiler, so they account for the
// packed attribute and for the preprocessor-selected field widths.
//
// The bool result reports whether the struct is one the generator resolved.
func (s *Set) Offsets(structName string) (map[string]int, bool) {
	v, ok := s.offsets[structName]
	return v, ok
}

// PacketOffsets returns the field offsets the source registers for an
// opcode.
//
// The bool result reports whether the opcode is registered.
func (s *Set) PacketOffsets(opcode uint16) ([]int, bool) {
	p, ok := s.packets.Lookup(opcode)
	if !ok {
		return nil, false
	}
	return p.Offsets, true
}

// PacketLength returns the wire length the source registers for an opcode.
//
// The bool result reports whether the opcode is registered.
func (s *Set) PacketLength(opcode uint16) (int, bool) {
	p, ok := s.packets.Lookup(opcode)
	if !ok {
		return 0, false
	}
	return p.Length, true
}

// MustOpcode returns the resolved opcode id of a packet-header constant.
//
// The panic on a missing constant is deliberate: the source declares these
// unconditionally, so a missing one means the generated tables are stale.
//
// The caller names itself, so the panic says which feature's table is
// stale.
func (s *Set) MustOpcode(feature, name string) uint16 {
	v, ok := s.opcodes[name]
	if !ok {
		panic(feature + ": upstream opcode " + name + " is missing from the generated tables")
	}
	// The opcode is a two-byte field, and the generated table holds the
	// value the source declares, so the conversion cannot overflow.
	return uint16(v) //nolint:gosec // the source's opcode is a two-byte field.
}

// MustOpcodeByHandler returns the opcode the source registers for a parse
// handler.
//
// Some upstream packets have no header constant and are registered
// numerically, so the parse handler is the only name they carry.
func (s *Set) MustOpcodeByHandler(feature, handler string) uint16 {
	opcode, ok := s.OpcodeByHandler(handler)
	if !ok {
		panic(feature + ": no upstream packet registers the handler " + handler)
	}
	return opcode
}

// MustLayout returns the resolved on-wire size of a wire struct.
func (s *Set) MustLayout(feature, name string) int {
	v, ok := s.layouts[name]
	if !ok {
		panic(feature + ": upstream layout " + name + " is missing from the generated tables")
	}
	return v
}

// MustPacketLength returns the wire length the source registers for an
// opcode.
func (s *Set) MustPacketLength(feature string, opcode uint16) int {
	length, ok := s.PacketLength(opcode)
	if !ok {
		panic(fmt.Sprintf("%s: the upstream opcode %#04x is not registered", feature, opcode))
	}
	return length
}

// thailandSet returns the Thai fork's set for a client family.
func thailandSet(variant Variant) *Set {
	switch variant {
	case VariantMain:
		return &Set{
			source:        SourceThailand,
			variant:       VariantMain,
			packets:       buildTable(VariantMain, thailand.MainPackets),
			runtime:       buildRuntime(thailand.MainPackets),
			registrations: thailand.MainRegistrations,
			opcodes:       thailand.MainOpcodes,
			layouts:       thailand.MainLayouts,
			offsets: map[string]map[string]int{
				"CHARACTER_INFO":            thailand.MainCHARACTER_INFOOffsets,
				"NORMALITEM_INFO":           thailand.MainNORMALITEM_INFOOffsets,
				"EQUIPITEM_INFO":            thailand.MainEQUIPITEM_INFOOffsets,
				"EQUIPSLOTINFO":             thailand.MainEQUIPSLOTINFOOffsets,
				"PACKET_ZC_ITEM_PICKUP_ACK": thailand.MainPACKET_ZC_ITEM_PICKUP_ACKOffsets,
			},
		}
	case VariantRE:
		return &Set{
			source:        SourceThailand,
			variant:       VariantRE,
			packets:       buildTable(VariantRE, thailand.REPackets),
			runtime:       buildRuntime(thailand.REPackets),
			registrations: thailand.RERegistrations,
			opcodes:       thailand.REOpcodes,
			layouts:       thailand.RELayouts,
			offsets: map[string]map[string]int{
				"CHARACTER_INFO":            thailand.RECHARACTER_INFOOffsets,
				"NORMALITEM_INFO":           thailand.RENORMALITEM_INFOOffsets,
				"EQUIPITEM_INFO":            thailand.REEQUIPITEM_INFOOffsets,
				"EQUIPSLOTINFO":             thailand.REEQUIPSLOTINFOOffsets,
				"PACKET_ZC_ITEM_PICKUP_ACK": thailand.REPACKET_ZC_ITEM_PICKUP_ACKOffsets,
			},
		}
	default:
		return nil
	}
}

// upstreamSet returns upstream master's set for a client family.
func upstreamSet(variant Variant) *Set {
	switch variant {
	case VariantMain:
		return &Set{
			source:        SourceUpstream,
			variant:       VariantMain,
			packets:       buildTable(VariantMain, upstream.MainPackets),
			runtime:       buildRuntime(upstream.MainPackets),
			registrations: upstream.MainRegistrations,
			opcodes:       upstream.MainOpcodes,
			layouts:       upstream.MainLayouts,
			offsets: map[string]map[string]int{
				"CHARACTER_INFO":            upstream.MainCHARACTER_INFOOffsets,
				"NORMALITEM_INFO":           upstream.MainNORMALITEM_INFOOffsets,
				"EQUIPITEM_INFO":            upstream.MainEQUIPITEM_INFOOffsets,
				"EQUIPSLOTINFO":             upstream.MainEQUIPSLOTINFOOffsets,
				"PACKET_ZC_ITEM_PICKUP_ACK": upstream.MainPACKET_ZC_ITEM_PICKUP_ACKOffsets,
			},
		}
	case VariantRE:
		return &Set{
			source:        SourceUpstream,
			variant:       VariantRE,
			packets:       buildTable(VariantRE, upstream.REPackets),
			runtime:       buildRuntime(upstream.REPackets),
			registrations: upstream.RERegistrations,
			opcodes:       upstream.REOpcodes,
			layouts:       upstream.RELayouts,
			offsets: map[string]map[string]int{
				"CHARACTER_INFO":            upstream.RECHARACTER_INFOOffsets,
				"NORMALITEM_INFO":           upstream.RENORMALITEM_INFOOffsets,
				"EQUIPITEM_INFO":            upstream.REEQUIPITEM_INFOOffsets,
				"EQUIPSLOTINFO":             upstream.REEQUIPSLOTINFOOffsets,
				"PACKET_ZC_ITEM_PICKUP_ACK": upstream.REPACKET_ZC_ITEM_PICKUP_ACKOffsets,
			},
		}
	default:
		return nil
	}
}
