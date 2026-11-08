package packet

import (
	"github.com/bouroo/goAthena/pkg/ro/protocol"
)

// The protocol constants are resolved from the selected source pack at use,
// not baked in at compile time.
//
// The reason is that a source pack's packet headers select their opcode and
// their struct layout per client family (src/common/packets.hpp:
// DEFINE_PACKET_HEADER, and the PACKETVER_*_NUM guards around the struct
// definitions). The Thai fork alone declares 77 packet headers with more
// than one value, so a constant that is right for one family is wrong for
// the other.
//
// So each constant below is a function that asks the installed set. The set
// is installed at boot from the selected source and family
// (protocol.Install), and the generated tables under
// pkg/ro/protocol/generated hold that source's own resolved values.
//
// The name in each call is the upstream packet's own name. Where upstream
// registers a client-to-server packet without a header constant, the name is
// the parse handler the source binds it to, which is what identifies the
// packet upstream.

// opcode returns the opcode of an upstream packet, or panics.
//
// The panic is the contract: a missing packet is a build-time mistake in the
// generated tables, not a runtime condition a caller can recover from.
func opcode(name string) uint16 {
	v, ok := protocol.Active().Opcode(name)
	if !ok {
		panic("packet: the source pack has no packet named " + name)
	}
	return uint16(v) //nolint:gosec // an opcode is a two-byte field.
}

// opcodeForHandler returns the opcode the source's RUNTIME packet table
// dispatches a parse handler on for a request of the given wire shape, or
// panics.
//
// The length and the field offsets are the ones the source's own struct
// carries — the shape the packet layer parses. They narrow the handler's
// client-version variants to the one the resolved PACKETVER speaks.
func opcodeForHandler(handler string, length int, offsets []int) uint16 {
	v, ok := protocol.Active().OpcodeForHandler(handler, length, offsets)
	if !ok {
		panic("packet: the source pack registers no packet for the handler " + handler + " with the requested shape")
	}
	return v
}

// ClientVariantOpcodes returns the source's other opcodes for the opcode a
// client sent.
//
// The source registers one handler under each client build's opcode and its
// runtime keeps them all, so a client may send any of them for the same
// request. The server accepts the whole set, the way the source's runtime
// does.
func ClientVariantOpcodes(opcode uint16) []uint16 {
	return protocol.Active().ClientVariantOpcodes(opcode)
}

// layout returns the on-wire size of an upstream struct, or panics.
//
// A size is the compiled size of the packed struct, so it is the source
// pack's own answer and not a hand-counted one.
func layout(name string) int {
	v, ok := protocol.Active().Layout(name)
	if !ok {
		panic("packet: the source pack has no struct named " + name)
	}
	return v
}

// packetLengthOf returns the on-wire length the source registers for a
// client-to-server opcode, or panics.
//
// The length is -1 for the variable-length packets, whose real size is in
// the frame's length prefix.
func packetLengthOf(opcode uint16) int {
	v, ok := protocol.Active().PacketLength(opcode)
	if !ok {
		panic("packet: the source pack registers no length for the opcode")
	}
	return v
}

// literalOpcode returns an opcode the source writes as a literal.
//
// A few packets upstream sends have no header constant: it writes the
// opcode straight into the frame. The literal is the opcode, and it is the
// same in every client family.
func literalOpcode(opcode uint16) uint16 { return opcode }

// LiteralOpcode returns an opcode the source writes as a literal.
//
// A few packets upstream sends have no header constant: it writes the
// opcode straight into the frame. The literal is the opcode, and it is the
// same in every client family.
func LiteralOpcode(opcode uint16) uint16 { return literalOpcode(opcode) }
