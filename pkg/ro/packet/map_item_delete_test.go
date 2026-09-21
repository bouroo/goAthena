//go:build unit

package packet

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// TestDeleteItemFromBodyResponse_EncodeFieldLayout pins the 8-byte
// ZC_DELETE_ITEM_FROM_BODY (0x07fa) layout at the offsets the fork's struct
// declares (packets.hpp:825-831): int16 packetType, int16 deleteType,
// uint16 index, int16 count. Every field is asserted byte-exact, so a reorder
// or a dropped count field fails here rather than silently on the wire — and
// the length assert is the one the size-derivation mutation probe inverts.
func TestDeleteItemFromBodyResponse_EncodeFieldLayout(t *testing.T) {
	t.Parallel()

	// The value is not the point of the layout test, but pinning it here keeps
	// the enum honest: clif.cpp:2905-2914 documents 6 = "Item sold".
	if DeleteTypeItemSold != 6 {
		t.Fatalf("DeleteTypeItemSold = %d, want 6 (clif.cpp:2911)", DeleteTypeItemSold)
	}

	r := &DeleteItemFromBodyResponse{DeleteType: DeleteTypeItemSold, Index: 7, Count: 3}
	var buf bytes.Buffer
	if err := r.Encode(&buf); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	out := buf.Bytes()
	if len(out) != 8 {
		t.Fatalf("encoded length = %d, want 8; bytes=% x", len(out), out)
	}
	if got := binary.LittleEndian.Uint16(out[0:2]); got != HeaderZCDeleteItemFromBody {
		t.Errorf("packetType = 0x%04x, want 0x%04x", got, HeaderZCDeleteItemFromBody)
	}
	if got := int16(binary.LittleEndian.Uint16(out[2:4])); got != DeleteTypeItemSold { //nolint:gosec // G115: bit-cast back to the signed wire field
		t.Errorf("deleteType = %d, want %d", got, DeleteTypeItemSold)
	}
	if got := binary.LittleEndian.Uint16(out[4:6]); got != 7 {
		t.Errorf("index = %d, want 7", got)
	}
	if got := int16(binary.LittleEndian.Uint16(out[6:8])); got != 3 { //nolint:gosec // G115: bit-cast back to the signed wire field
		t.Errorf("count = %d, want 3", got)
	}
}

// TestDeleteItemFromBodyResponse_Size pins the declared size to the byte count
// the encoder actually writes, so Size() cannot drift from the frame.
func TestDeleteItemFromBodyResponse_Size(t *testing.T) {
	t.Parallel()

	r := &DeleteItemFromBodyResponse{}
	if got := r.Size(); got != 8 {
		t.Errorf("Size() = %d, want 8", got)
	}
	var buf bytes.Buffer
	if err := r.Encode(&buf); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if got := buf.Len(); got != r.Size() {
		t.Errorf("encoded %d bytes, Size() says %d", got, r.Size())
	}
}
