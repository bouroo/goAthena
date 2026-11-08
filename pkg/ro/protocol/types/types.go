// Package types holds the wire types the generated source packs use.
//
// The generated packages hold the resolved tables and import these types;
// the package that selects a source pack imports both. The types live here
// so a generated package does not have to import the package that selects
// it, which would be an import cycle.
package types

// Packet is one resolved client-to-server packet: the opcode the client sends,
// the wire length the source registers for it, the source's parse handler,
// and the byte offsets of the fields that handler reads.
//
// The values are resolved from the source's own headers (see cmd/protocolgen),
// so they hold for exactly the client variant the table was generated for.
type Packet struct {
	// Opcode is the client-to-server opcode.
	Opcode uint16
	// Length is the wire length the source registers. A negative length means
	// the source registered a variable-length packet: the frame carries its
	// own length field.
	Length int
	// Handler is the source's parse function name, e.g. "clif_parse_WalkToXY".
	// It is empty for packets the source registers without a parser.
	Handler string
	// Name is the source's own name for the packet, e.g. "CZ_CONTACTNPC".
	// It is how a caller asks for the packet by name rather than by the
	// number it happens to have in this client family.
	Name string
	// Offsets are the byte positions of the fields the handler reads, in the
	// order the source lists them.
	Offsets []int
}

// Variable reports whether the packet is variable-length.
func (p Packet) Variable() bool { return p.Length < 0 }
