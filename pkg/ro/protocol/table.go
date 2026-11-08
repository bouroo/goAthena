package protocol

import (
	"github.com/bouroo/goAthena/pkg/ro/protocol/types"
)

// Packet is one resolved client-to-server packet.
//
// It is an alias of the generated packages' type, so a caller that holds a
// packet from one source pack can hold a packet from any.
type Packet = types.Packet

// Table is the resolved packet table for one client variant: the opcodes the
// server dispatches on, keyed by opcode.
type Table struct {
	// Variant is the client family this table was resolved for.
	Variant Variant
	// Packets holds the resolved client-to-server packets.
	Packets map[uint16]Packet
}

// Lookup returns the packet registered for the opcode.
//
// The second result reports whether the opcode is known for this variant.
func (t Table) Lookup(opcode uint16) (Packet, bool) {
	p, ok := t.Packets[opcode]
	return p, ok
}

// Len reports the number of registered opcodes.
func (t Table) Len() int { return len(t.Packets) }

// buildTable indexes a resolved packet list by opcode.
func buildTable(v Variant, list []Packet) Table {
	t := Table{Variant: v, Packets: make(map[uint16]Packet, len(list))}
	for _, p := range list {
		t.Packets[p.Opcode] = p
	}
	return t
}

// buildRuntime maps each opcode to the handler it ends up carrying.
//
// The source registers each packet with packetdb_addpacket, which
// overwrites the opcode's entry (src/map/clif.cpp:25899-25906), so the
// handler an opcode carries is the one its last registration names. The
// generated list is already resolved that way, so the map is a read of it.
func buildRuntime(list []Packet) map[uint16]string {
	runtime := make(map[uint16]string, len(list))
	for _, p := range list {
		if p.Handler == "" {
			continue
		}
		runtime[p.Opcode] = p.Handler
	}
	return runtime
}
