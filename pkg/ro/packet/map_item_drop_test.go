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
	if got, want := r.Size(), 24; got != want {
		t.Errorf("Size() = %d, want %d", got, want)
	}
}

// TestItemFallEntryResponse_EncodeFieldLayout verifies the on-wire byte
// layout of ZC_ITEM_FALL_ENTRY (0x0ADD) at PACKETVER 20250604. The legacy
// clif.cpp:864 comment "<name id>.W" is stale — nameID is uint32 at
// >=20181121, so the frame is 24 bytes (not 22) and every field after
// nameID sits 2 bytes further than the old v4 layout.
func TestItemFallEntryResponse_EncodeFieldLayout(t *testing.T) {
	t.Parallel()

	r := &ItemFallEntryResponse{
		ID:             0x11223344,
		NameID:         0x12345678, // exercises all 4 nameID bytes
		Type:           3,          // IT_ETC
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
	if len(out) != 24 {
		t.Fatalf("encoded length = %d, want 24; bytes=% x", len(out), out)
	}

	// [0:2] packetType
	if got := binary.LittleEndian.Uint16(out[0:2]); got != HeaderZCItemFallEntry {
		t.Errorf("packetType = 0x%04x, want 0x%04x", got, HeaderZCItemFallEntry)
	}
	// [2:6] ID
	if got := binary.LittleEndian.Uint32(out[2:6]); got != 0x11223344 {
		t.Errorf("ID = 0x%x, want 0x11223344", got)
	}
	// [6:10] NameID (uint32)
	if got := binary.LittleEndian.Uint32(out[6:10]); got != 0x12345678 {
		t.Errorf("NameID = 0x%x, want 0x12345678", got)
	}
	// [10:12] Type
	if got := binary.LittleEndian.Uint16(out[10:12]); got != 3 {
		t.Errorf("Type = %d, want 3", got)
	}
	// [12] Identified
	if out[12] != 1 {
		t.Errorf("Identified = %d, want 1", out[12])
	}
	// [13:15] X
	if got := binary.LittleEndian.Uint16(out[13:15]); got != 155 {
		t.Errorf("X = %d, want 155", got)
	}
	// [15:17] Y
	if got := binary.LittleEndian.Uint16(out[15:17]); got != 165 {
		t.Errorf("Y = %d, want 165", got)
	}
	// [17] SubX
	if out[17] != 4 {
		t.Errorf("SubX = %d, want 4", out[17])
	}
	// [18] SubY
	if out[18] != 7 {
		t.Errorf("SubY = %d, want 7", out[18])
	}
	// [19:21] Amount
	if got := binary.LittleEndian.Uint16(out[19:21]); got != 9 {
		t.Errorf("Amount = %d, want 9", got)
	}
	// [21] ShowDropEffect
	if out[21] != 1 {
		t.Errorf("ShowDropEffect = %d, want 1", out[21])
	}
	// [22:24] DropEffectMode
	if got := binary.LittleEndian.Uint16(out[22:24]); got != 0 {
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

func TestItemEntryResponse_EncodeFieldLayout(t *testing.T) {
	t.Parallel()

	r := &ItemEntryResponse{
		AID:        0xAAAABBBB,
		NameID:     0x12345678,
		Identified: 1,
		X:          155,
		Y:          165,
		Amount:     9,
		SubX:       4,
		SubY:       7,
	}
	var buf bytes.Buffer
	if err := r.Encode(&buf); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	out := buf.Bytes()
	if len(out) != 19 {
		t.Fatalf("encoded length = %d, want 19; bytes=% x", len(out), out)
	}
	if got := binary.LittleEndian.Uint16(out[0:2]); got != HeaderZCItemEntry {
		t.Errorf("packetType = 0x%04x, want 0x%04x", got, HeaderZCItemEntry)
	}
	if got := binary.LittleEndian.Uint32(out[2:6]); got != 0xAAAABBBB {
		t.Errorf("AID = 0x%x, want 0xAAAABBBB", got)
	}
	if got := binary.LittleEndian.Uint32(out[6:10]); got != 0x12345678 {
		t.Errorf("NameID = 0x%x, want 0x12345678", got)
	}
	if out[10] != 1 {
		t.Errorf("Identified = %d, want 1", out[10])
	}
	if got := binary.LittleEndian.Uint16(out[11:13]); got != 155 {
		t.Errorf("X = %d, want 155", got)
	}
	if got := binary.LittleEndian.Uint16(out[13:15]); got != 165 {
		t.Errorf("Y = %d, want 165", got)
	}
	if got := binary.LittleEndian.Uint16(out[15:17]); got != 9 {
		t.Errorf("Amount = %d, want 9", got)
	}
	if out[17] != 4 {
		t.Errorf("SubX = %d, want 4", out[17])
	}
	if out[18] != 7 {
		t.Errorf("SubY = %d, want 7", out[18])
	}
}

func TestItemDisappearResponse_EncodeFieldLayout(t *testing.T) {
	t.Parallel()

	r := &ItemDisappearResponse{AID: 0xCAFEBABE}
	var buf bytes.Buffer
	if err := r.Encode(&buf); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	out := buf.Bytes()
	if len(out) != 6 {
		t.Fatalf("encoded length = %d, want 6; bytes=% x", len(out), out)
	}
	if got := binary.LittleEndian.Uint16(out[0:2]); got != HeaderZCItemDisappear {
		t.Errorf("packetType = 0x%04x, want 0x%04x", got, HeaderZCItemDisappear)
	}
	if got := binary.LittleEndian.Uint32(out[2:6]); got != 0xCAFEBABE {
		t.Errorf("AID = 0x%x, want 0xCAFEBABE", got)
	}
}

func TestItemThrowAckResponse_EncodeFieldLayout(t *testing.T) {
	t.Parallel()

	r := &ItemThrowAckResponse{Index: 7, Count: 3}
	var buf bytes.Buffer
	if err := r.Encode(&buf); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	out := buf.Bytes()
	if len(out) != 6 {
		t.Fatalf("encoded length = %d, want 6; bytes=% x", len(out), out)
	}
	if got := binary.LittleEndian.Uint16(out[0:2]); got != HeaderZCItemThrowAck {
		t.Errorf("packetType = 0x%04x, want 0x%04x", got, HeaderZCItemThrowAck)
	}
	if got := binary.LittleEndian.Uint16(out[2:4]); got != 7 {
		t.Errorf("Index = %d, want 7", got)
	}
	if got := binary.LittleEndian.Uint16(out[4:6]); got != 3 {
		t.Errorf("Count = %d, want 3", got)
	}
}

// TestItemPickupAckResponse_EncodeFieldLayout pins the 70-byte ZC_ITEM_PICKUP_ACK
// (0x0b41) layout at PACKETVER 20250604 — the >=20200916 band. This is the
// only A4 packet whose size was not previously shipped, so the full offset
// map is asserted byte-exact.
func TestItemPickupAckResponse_EncodeFieldLayout(t *testing.T) {
	t.Parallel()

	r := &ItemPickupAckResponse{
		Index:           11,
		Count:           5,
		NameID:          0x12345678,
		IsIdentified:    1,
		IsDamaged:       0,
		Slot:            [4]uint32{1, 2, 3, 4},
		Location:        0x10,
		Type:            3,
		Result:          1,
		HireExpireDate:  -1,
		BindOnEquipType: 0,
		Option:          [5]ItemOption{{Index: 10, Value: 5, Param: 1}},
		Favorite:        0,
		Look:            0,
		RefiningLevel:   0,
		Grade:           0,
	}
	var buf bytes.Buffer
	if err := r.Encode(&buf); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	out := buf.Bytes()
	if len(out) != 70 {
		t.Fatalf("encoded length = %d, want 70; bytes=% x", len(out), out)
	}
	if got := binary.LittleEndian.Uint16(out[0:2]); got != HeaderZCItemPickupAck {
		t.Errorf("packetType = 0x%04x, want 0x%04x", got, HeaderZCItemPickupAck)
	}
	if got := binary.LittleEndian.Uint16(out[2:4]); got != 11 {
		t.Errorf("Index = %d, want 11", got)
	}
	if got := binary.LittleEndian.Uint16(out[4:6]); got != 5 {
		t.Errorf("Count = %d, want 5", got)
	}
	if got := binary.LittleEndian.Uint32(out[6:10]); got != 0x12345678 {
		t.Errorf("NameID = 0x%x, want 0x12345678", got)
	}
	if out[10] != 1 {
		t.Errorf("IsIdentified = %d, want 1", out[10])
	}
	if out[11] != 0 {
		t.Errorf("IsDamaged = %d, want 0", out[11])
	}
	for i, want := range [4]uint32{1, 2, 3, 4} {
		if got := binary.LittleEndian.Uint32(out[12+i*4:]); got != want {
			t.Errorf("Slot[%d] = %d, want %d", i, got, want)
		}
	}
	if got := binary.LittleEndian.Uint32(out[28:32]); got != 0x10 {
		t.Errorf("Location = 0x%x, want 0x10", got)
	}
	if out[32] != 3 {
		t.Errorf("Type = %d, want 3", out[32])
	}
	if out[33] != 1 {
		t.Errorf("Result = %d, want 1", out[33])
	}
	if got := int32(binary.LittleEndian.Uint32(out[34:38])); got != -1 {
		t.Errorf("HireExpireDate = %d, want -1", got)
	}
	// Option[0] at offset 40: int16 Index=10, int16 Value=5, uint8 Param=1.
	if got := binary.LittleEndian.Uint16(out[40:42]); got != 10 {
		t.Errorf("Option[0].Index = %d, want 10", got)
	}
	if got := binary.LittleEndian.Uint16(out[42:44]); got != 5 {
		t.Errorf("Option[0].Value = %d, want 5", got)
	}
	if out[44] != 1 {
		t.Errorf("Option[0].Param = %d, want 1", out[44])
	}
	// Options[1..4] must be zeroed (unused).
	for i := 45; i < 65; i++ {
		if out[i] != 0 {
			t.Errorf("unused option byte [%d] = %d, want 0", i, out[i])
		}
	}
	// Favorite[65], Look[66:68], RefiningLevel[68], Grade[69] all zero here.
	if out[65] != 0 || binary.LittleEndian.Uint16(out[66:68]) != 0 || out[68] != 0 || out[69] != 0 {
		t.Errorf("trailing fields non-zero: fav=%d look=%d refine=%d grade=%d", out[65], binary.LittleEndian.Uint16(out[66:68]), out[68], out[69])
	}
}
