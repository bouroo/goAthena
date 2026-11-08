//go:build unit

package protocol_test

import (
	"testing"

	"github.com/bouroo/goAthena/pkg/ro/protocol"
)

// TestOpcodeForHandler pins the shape-aware runtime lookup.
//
// The source registers each packet with packetdb_addpacket, which overwrites
// the opcode's whole entry (src/map/clif.cpp:25899-25906), so the handler an
// opcode ends up with is the one its LAST registration names. One handler can
// own several opcodes — the client-version variants of one request — and they
// differ in wire shape, so the lookup narrows by the shape the caller parses
// and returns the last such registration.
//
// The three kafra requests are the load-bearing case: their handlers own
// several opcodes, and only the 8-byte cmd|index|amount shape is the one the
// server parses.
func TestOpcodeForHandler(t *testing.T) {
	t.Parallel()
	set, ok := protocol.Resolve(protocol.SourceThailand, protocol.VariantRE)
	if !ok {
		t.Fatal("the Thai fork must resolve")
	}

	for _, tc := range []struct {
		name    string
		handler string
		length  int
		offsets []int
		want    uint16
	}{
		{"move body to store", "clif_parse_MoveToKafra", 8, []int{2, 4}, 0x0364},
		{"move store to body", "clif_parse_MoveFromKafra", 8, []int{2, 4}, 0x0365},
		{"close store", "clif_parse_CloseKafra", 2, nil, 0x0193},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := set.OpcodeForHandler(tc.handler, tc.length, tc.offsets)
			if !ok {
				t.Fatalf("OpcodeForHandler(%q, %d, %v) found nothing", tc.handler, tc.length, tc.offsets)
			}
			if got != tc.want {
				t.Errorf("opcode = %#04x, want %#04x", got, tc.want)
			}
		})
	}
}

// TestOpcodeForHandler_RejectsWrongShape proves the shape narrowing is
// load-bearing: a handler that owns several opcodes must not resolve to one
// whose wire shape differs from the caller's.
//
// clif_parse_MoveToKafra owns 0x0094 (length 19, offsets 3/15) as well as
// 0x0364 (length 8, offsets 2/4), so the client-version variant must not
// answer a request the server parses as cmd|index|amount.
func TestOpcodeForHandler_RejectsWrongShape(t *testing.T) {
	t.Parallel()
	set, ok := protocol.Resolve(protocol.SourceThailand, protocol.VariantRE)
	if !ok {
		t.Fatal("the Thai fork must resolve")
	}

	if got, ok := set.OpcodeForHandler("clif_parse_MoveToKafra", 19, []int{3, 15}); !ok || got != 0x0094 {
		t.Errorf("client-version variant = %#04x (ok=%v), want 0x0094", got, ok)
	}
	if got, ok := set.OpcodeForHandler("clif_parse_MoveToKafra", 12, []int{2, 4}); ok {
		t.Errorf("a shape no registration carries resolved to %#04x", got)
	}
}
