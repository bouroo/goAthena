//go:build unit

package packet

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestItemFallEntryResponse_Size(t *testing.T) {
	t.Parallel()

	r := &ItemFallEntryResponse{}
	if got, want := r.Size(), 22; got != want {
		t.Errorf("Size() = %d, want %d", got, want)
	}
}

// TestItemFallEntryResponse_EncodeFieldLayout verifies the on-wire byte
// layout matches rathena/src/map/clif.cpp:864 exactly:
//
//	0ADD <id>.L <name id>.W <type>.W <identified>.B
//	     <x>.W <y>.W <subX>.B <subY>.B <amount>.W
//	     <show drop effect>.B <drop effect mode>.W
func TestItemFallEntryResponse_EncodeFieldLayout(t *testing.T) {
	t.Parallel()

	r := &ItemFallEntryResponse{
		ID:             0x11223344,
		NameID:         0x5566,
		Type:           3, // IT_ETC
		Identified:     1,
		X:              155,
		Y:              165,
		SubX:           4,
		SubY:           7,
		Amount:         9,
		ShowDropEffect: 1, // MVP
		DropEffectMode: 0,
	}

	var buf bytes.Buffer
	if err := r.Encode(&buf); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	out := buf.Bytes()
	if len(out) != 22 {
		t.Fatalf("encoded length = %d, want 22; bytes=% x", len(out), out)
	}

	// [0:2] packetType
	if got := binary.LittleEndian.Uint16(out[0:2]); got != HeaderZCItemFallEntry {
		t.Errorf("packetType = 0x%04x, want 0x%04x", got, HeaderZCItemFallEntry)
	}
	// [2:6] ID
	if got := binary.LittleEndian.Uint32(out[2:6]); got != 0x11223344 {
		t.Errorf("ID = 0x%x, want 0x11223344", got)
	}
	// [6:8] NameID
	if got := binary.LittleEndian.Uint16(out[6:8]); got != 0x5566 {
		t.Errorf("NameID = 0x%x, want 0x5566", got)
	}
	// [8:10] Type
	if got := binary.LittleEndian.Uint16(out[8:10]); got != 3 {
		t.Errorf("Type = %d, want 3", got)
	}
	// [10] Identified
	if out[10] != 1 {
		t.Errorf("Identified = %d, want 1", out[10])
	}
	// [11:13] X
	if got := binary.LittleEndian.Uint16(out[11:13]); got != 155 {
		t.Errorf("X = %d, want 155", got)
	}
	// [13:15] Y
	if got := binary.LittleEndian.Uint16(out[13:15]); got != 165 {
		t.Errorf("Y = %d, want 165", got)
	}
	// [15] SubX
	if out[15] != 4 {
		t.Errorf("SubX = %d, want 4", out[15])
	}
	// [16] SubY
	if out[16] != 7 {
		t.Errorf("SubY = %d, want 7", out[16])
	}
	// [17:19] Amount
	if got := binary.LittleEndian.Uint16(out[17:19]); got != 9 {
		t.Errorf("Amount = %d, want 9", got)
	}
	// [19] ShowDropEffect
	if out[19] != 1 {
		t.Errorf("ShowDropEffect = %d, want 1", out[19])
	}
	// [20:22] DropEffectMode
	if got := binary.LittleEndian.Uint16(out[20:22]); got != 0 {
		t.Errorf("DropEffectMode = %d, want 0", got)
	}
}

// TestItemFallEntryResponse_OpcodeMatchesWire verifies the on-wire
// opcode 0x0ADD matches what the packet header constant resolves to
// when written little-endian (low byte 0xDD, high byte 0x0A).
func TestItemFallEntryResponse_OpcodeMatchesWire(t *testing.T) {
	t.Parallel()

	r := &ItemFallEntryResponse{}
	var buf bytes.Buffer
	if err := r.Encode(&buf); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if buf.Len() < 2 {
		t.Fatalf("encoded buffer too short: %d", buf.Len())
	}
	if buf.Bytes()[0] != 0xDD || buf.Bytes()[1] != 0x0A {
		t.Errorf("opcode bytes = % x, want DD 0A (LE uint16 0x0ADD)", buf.Bytes()[0:2])
	}
}

// TestNewMapServerDB_HasZCItemFallEntry checks the new packet is
// registered with the correct name, length, and direction.
func TestNewMapServerDB_HasZCItemFallEntry(t *testing.T) {
	t.Parallel()

	db := NewMapServerDB()
	def, ok := db.Lookup(HeaderZCItemFallEntry)
	if !ok {
		t.Fatalf("Lookup(0x%04x) = missing, want registered", HeaderZCItemFallEntry)
	}
	if def.Name != "ZC_ITEM_FALL_ENTRY" {
		t.Errorf("Name = %q, want ZC_ITEM_FALL_ENTRY", def.Name)
	}
	if def.Length != 22 {
		t.Errorf("Length = %d, want 22", def.Length)
	}
	if def.Direction != DirectionServerToClient {
		t.Errorf("Direction = %d, want DirectionServerToClient (%d)", def.Direction, DirectionServerToClient)
	}
}
