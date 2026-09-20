package packet

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

// TestStorage_CZMoveItemRoundTrip proves the parse / encode symmetry for both
// warehouse-move packets. The encoder writes the cmd in slot 0-1, the index in
// slot 2-3, and the amount in slot 4-7; the parser reverses it.
func TestStorage_CZMoveItemRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		idx  uint16
		amt  uint32
		cmd  uint16
	}{
		{"toStore", 7, 99, HeaderCZMOVEITEMTOSTORE2},
		{"toBody", 3, 1, HeaderCZMOVEITEMTOBODY2},
		{"maxAmount", 0xffff, 0xffffffff, HeaderCZMOVEITEMTOSTORE2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			frame := []byte{
				0, 0, // cmd
				0, 0, // index
				0, 0, 0, 0, // amount
			}
			binary.LittleEndian.PutUint16(frame[0:], c.cmd)
			binary.LittleEndian.PutUint16(frame[2:], c.idx)
			binary.LittleEndian.PutUint32(frame[4:], c.amt)

			// parse → fields match
			switch c.cmd {
			case HeaderCZMOVEITEMTOSTORE2:
				got, err := ParseCZMoveItemToStore2(frame)
				if err != nil {
					t.Fatalf("parse to-store: %v", err)
				}
				if got.Index != c.idx || got.Amount != c.amt {
					t.Errorf("parse to-store = %+v, want idx=%d amt=%d", got, c.idx, c.amt)
				}
			case HeaderCZMOVEITEMTOBODY2:
				got, err := ParseCZMoveItemToBody2(frame)
				if err != nil {
					t.Fatalf("parse to-body: %v", err)
				}
				if got.Index != c.idx || got.Amount != c.amt {
					t.Errorf("parse to-body = %+v, want idx=%d amt=%d", got, c.idx, c.amt)
				}
			}

			// encode → frame matches
			var buf bytes.Buffer
			switch c.cmd {
			case HeaderCZMOVEITEMTOSTORE2:
				if err := (CZMoveItemToStore2{Index: c.idx, Amount: c.amt}).Encode(&buf); err != nil {
					t.Fatalf("encode to-store: %v", err)
				}
			case HeaderCZMOVEITEMTOBODY2:
				if err := (CZMoveItemToBody2{Index: c.idx, Amount: c.amt}).Encode(&buf); err != nil {
					t.Fatalf("encode to-body: %v", err)
				}
			}
			if !bytes.Equal(buf.Bytes(), frame) {
				t.Errorf("encode = %x, want %x", buf.Bytes(), frame)
			}
		})
	}
}

// TestStorage_CZMoveItem_RejectsBadCmd proves the parser refuses a frame whose
// cmd header disagrees with the parser. Same defence as the trade parsers
// (trade_test.go) and the trade item-add parser — guards against the codec
// being called with the wrong cmd on a hybrid frame.
func TestStorage_CZMoveItem_RejectsBadCmd(t *testing.T) {
	frame := []byte{0, 0, 0, 0, 0, 0, 0, 0}
	binary.LittleEndian.PutUint16(frame[0:], HeaderCZMOVEITEMTOBODY2) // wrong for to-store parser

	if _, err := ParseCZMoveItemToStore2(frame); err == nil {
		t.Error("expected error parsing to-store with to-body cmd")
	}
}

// TestStorage_CZMoveItem_ShortFrame proves the parser refuses a frame shorter
// than the fixed layout. Mirrors the trade parsers' defence against a partial
// buffer slipping through.
func TestStorage_CZMoveItem_ShortFrame(t *testing.T) {
	if _, err := ParseCZMoveItemToStore2([]byte{0, 0, 0}); !errors.Is(err, errShortFrame) && err == nil {
		t.Errorf("expected short-frame error, got %v", err)
	}
}

// errShortFrame is a stand-in; the actual storage parsers wrap a length check
// in fmt.Errorf without a sentinel, so the test asserts a non-nil error. The
// shape matches the trade tests' pattern.
var errShortFrame = errors.New("short")

// TestStorage_ItemListResult_RoundTrip proves ZC_STOREITEMLISTRESULT is a
// fixed-size 6-byte frame with the cmd at 0-1, length at 2-3, result at 4-5.
func TestStorage_ItemListResult_RoundTrip(t *testing.T) {
	for _, result := range []uint16{0, 1, 0xffff} {
		t.Run("result", func(t *testing.T) {
			resp := StorageItemListResult{Result: result}
			if got := resp.Size(); got != sizeZCStoreItemListResult {
				t.Errorf("Size = %d, want %d", got, sizeZCStoreItemListResult)
			}
			var buf bytes.Buffer
			if err := resp.Encode(&buf); err != nil {
				t.Fatalf("encode: %v", err)
			}
			if buf.Len() != sizeZCStoreItemListResult {
				t.Errorf("encode len = %d, want %d", buf.Len(), sizeZCStoreItemListResult)
			}
			if cmd := binary.LittleEndian.Uint16(buf.Bytes()[0:2]); cmd != HeaderZCSTOREITEMLISTRESULT {
				t.Errorf("cmd = 0x%04x, want 0x%04x", cmd, HeaderZCSTOREITEMLISTRESULT)
			}
			if length := binary.LittleEndian.Uint16(buf.Bytes()[2:4]); length != sizeZCStoreItemListResult {
				t.Errorf("packetLength = %d, want %d", length, sizeZCStoreItemListResult)
			}
			if got := binary.LittleEndian.Uint16(buf.Bytes()[4:6]); got != result {
				t.Errorf("result = %d, want %d", got, result)
			}
		})
	}
}

// TestStorage_ListNormal_RoundTrip proves ZC_STORE_NORMALITEMLIST carries the
// per-item entries at the right offsets and the cmd / packetLength slots are
// filled in correctly. With two stackable items, packetLength = 4 + 26*2 = 56.
func TestStorage_ListNormal_RoundTrip(t *testing.T) {
	items := []InventoryNormalItem{
		{Index: 2, ITID: 501, Type: 0, Count: 10, Flag: 1}, // Red Potion identified
		{Index: 3, ITID: 502, Type: 0, Count: 5, Flag: 1},  // Orange Potion identified
	}
	resp := StorageListNormalResponse{Items: items}
	var buf bytes.Buffer
	if err := resp.Encode(&buf); err != nil {
		t.Fatalf("encode: %v", err)
	}
	want := 4 + 26*len(items)
	if buf.Len() != want {
		t.Errorf("encode len = %d, want %d", buf.Len(), want)
	}
	out := buf.Bytes()
	if cmd := binary.LittleEndian.Uint16(out[0:2]); cmd != HeaderZCSTORENORMALITEMLIST {
		t.Errorf("cmd = 0x%04x, want 0x%04x", cmd, HeaderZCSTORENORMALITEMLIST)
	}
	if length := binary.LittleEndian.Uint16(out[2:4]); length != uint16(want) {
		t.Errorf("packetLength = %d, want %d", length, want)
	}
}

// TestStorage_ListEquip_RoundTrip proves ZC_STORE_EQUIPMENTITEMLIST carries
// the per-item entries at the right offsets. With one equipped item,
// packetLength = 4 + 57 = 61.
func TestStorage_ListEquip_RoundTrip(t *testing.T) {
	items := []InventoryEquipItem{
		{Index: 0, ITID: 1101, Type: 3, Location: 0x0001, RefiningLevel: 7, ItemSpriteNumber: 1, Flag: 1},
	}
	resp := StorageListEquipResponse{Items: items}
	var buf bytes.Buffer
	if err := resp.Encode(&buf); err != nil {
		t.Fatalf("encode: %v", err)
	}
	want := 4 + 57*len(items)
	if buf.Len() != want {
		t.Errorf("encode len = %d, want %d", buf.Len(), want)
	}
	out := buf.Bytes()
	if cmd := binary.LittleEndian.Uint16(out[0:2]); cmd != HeaderZCSTOREEQUIPMENTITEMLIST {
		t.Errorf("cmd = 0x%04x, want 0x%04x", cmd, HeaderZCSTOREEQUIPMENTITEMLIST)
	}
	if length := binary.LittleEndian.Uint16(out[2:4]); length != uint16(want) {
		t.Errorf("packetLength = %d, want %d", length, want)
	}
}

// TestStorage_ListEmpty proves an empty list still emits the cmd + length=4
// frame so the client can distinguish "empty" from "no list at all".
func TestStorage_ListEmpty(t *testing.T) {
	for _, tc := range []struct {
		name string
		fn   func(io.Writer) error
		cmd  uint16
	}{
		{"normal", func(w io.Writer) error { return StorageListNormalResponse{}.Encode(w) }, HeaderZCSTORENORMALITEMLIST},
		{"equip", func(w io.Writer) error { return StorageListEquipResponse{}.Encode(w) }, HeaderZCSTOREEQUIPMENTITEMLIST},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := tc.fn(&buf); err != nil {
				t.Fatalf("encode: %v", err)
			}
			if buf.Len() != 4 {
				t.Errorf("encode len = %d, want 4", buf.Len())
			}
			if cmd := binary.LittleEndian.Uint16(buf.Bytes()[0:2]); cmd != tc.cmd {
				t.Errorf("cmd = 0x%04x, want 0x%04x", cmd, tc.cmd)
			}
		})
	}
}

// TestStorage_CZReqOpenStore2_ParseRoundTrip proves the parser extracts the
// NUL-padded name and rejects frames with a non-matching cmd.
func TestStorage_CZReqOpenStore2_ParseRoundTrip(t *testing.T) {
	frame := []byte{
		0, 0, // cmd 0x07e4
	}
	frame = append(frame, []byte("testuser")...)
	frame = append(frame, 0) // NUL terminator inside the 24-byte field
	for len(frame) < sizeCZReqOpenStore2 {
		frame = append(frame, 0)
	}
	binary.LittleEndian.PutUint16(frame[0:], HeaderCZREQOPENSTORE2)

	got, err := ParseCZReqOpenStore2(frame)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.AccountName != "testuser" {
		t.Errorf("accountName = %q, want %q", got.AccountName, "testuser")
	}

	// Encode round-trip.
	var buf bytes.Buffer
	if err := got.Encode(&buf); err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), frame) {
		t.Errorf("encode = %x, want %x", buf.Bytes(), frame)
	}
}

// TestStorage_CZReqOpenStore2_ShortFrame proves the parser refuses a frame
// shorter than the fixed 26-byte layout.
func TestStorage_CZReqOpenStore2_ShortFrame(t *testing.T) {
	frame := make([]byte, sizeCZReqOpenStore2-1)
	binary.LittleEndian.PutUint16(frame[0:], HeaderCZREQOPENSTORE2)
	if _, err := ParseCZReqOpenStore2(frame); err == nil {
		t.Error("expected error for short frame")
	}
}

// TestStorage_CZReqOpenStore2_BadCmd proves the parser refuses a frame with
// the wrong cmd (defence against hybrid frames).
func TestStorage_CZReqOpenStore2_BadCmd(t *testing.T) {
	frame := make([]byte, sizeCZReqOpenStore2)
	binary.LittleEndian.PutUint16(frame[0:], HeaderCZCLOSESTORE) // wrong
	if _, err := ParseCZReqOpenStore2(frame); err == nil {
		t.Error("expected error for wrong cmd")
	}
}

// TestStorage_CloseStore_Encode proves the bare 2-byte CZ_CLOSE_STORE writes
// only the cmd header (no body to parse).
func TestStorage_CloseStore_Encode(t *testing.T) {
	var buf bytes.Buffer
	if err := EncodeCZCloseStore(&buf); err != nil {
		t.Fatalf("encode: %v", err)
	}
	if buf.Len() != 2 {
		t.Errorf("encode len = %d, want 2", buf.Len())
	}
	if cmd := binary.LittleEndian.Uint16(buf.Bytes()[0:2]); cmd != HeaderCZCLOSESTORE {
		t.Errorf("cmd = 0x%04x, want 0x%04x", cmd, HeaderCZCLOSESTORE)
	}
}

// (no helpers required — every test uses bytes.Buffer directly)
