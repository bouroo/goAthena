package packet

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// TestStorage_RequestOpcodes_AreRuntimeTable pins the three kafra request
// opcodes to the source's runtime table: the last registration of each
// handler, because packetdb_addpacket overwrites an opcode's entry
// (third_party/rathenaThailand/src/map/clif.cpp:25899-25906).
func TestStorage_RequestOpcodes_AreRuntimeTable(t *testing.T) {
	for _, tc := range []struct {
		name string
		got  uint16
		want uint16
	}{
		{"move body->store", HeaderCZMOVEITEMFROMBODYTOSTORE(), 0x0364},
		{"move store->body", HeaderCZMOVEITEMFROMSTORETOBODY(), 0x0365},
		{"close store", HeaderCZCLOSESTORE(), 0x0193},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("opcode = 0x%04x, want 0x%04x", tc.got, tc.want)
			}
		})
	}
}

// TestStorage_MoveFromBodyToStore_Wire proves the body→store request is the
// source's 8-byte frame: cmd 0x0364 at 0-1, the client's bag index at 2-3,
// the amount at 4-7.
//
// 0x0364 is the source's RUNTIME-table registration of clif_parse_MoveToKafra:
// the source re-registers the opcode in later PACKETVER blocks and
// packetdb_addpacket overwrites an opcode's entry
// (third_party/rathenaThailand/src/map/clif.cpp:25899-25906), so the last
// registration is the one the client speaks.
func TestStorage_MoveFromBodyToStore_Wire(t *testing.T) {
	frame := make([]byte, 8)
	binary.LittleEndian.PutUint16(frame[0:], 0x0364)
	binary.LittleEndian.PutUint16(frame[2:], 9)  // client's bag slot
	binary.LittleEndian.PutUint32(frame[4:], 12) // amount

	got, err := ParseCZMoveItemFromBodyToStore(frame)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// The source's handler subtracts 2 from the wire index
	// (clif.cpp:13648), so the parsed row is the server's 0-based bag row.
	if got.Index != 7 {
		t.Errorf("Index = %d, want 7", got.Index)
	}
	if got.Amount != 12 {
		t.Errorf("Amount = %d, want 12", got.Amount)
	}
	if got.Size() != 8 {
		t.Errorf("Size = %d, want 8", got.Size())
	}

	// The encoder writes the client's slot back: row + 2.
	var buf bytes.Buffer
	if err := (CZMoveItemFromBodyToStore{Index: 7, Amount: 12}).Encode(&buf); err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), frame) {
		t.Errorf("encode = %x, want %x", buf.Bytes(), frame)
	}
}

// TestStorage_MoveFromStoreToBody_Wire proves the store→body request is the
// source's 8-byte frame: cmd 0x0365 at 0-1, the client's warehouse slot at
// 2-3, the amount at 4-7.
//
// 0x0365 is the source's RUNTIME-table registration of
// clif_parse_MoveFromKafra: the source re-registers the opcode in later
// PACKETVER blocks and packetdb_addpacket overwrites an opcode's entry
// (third_party/rathenaThailand/src/map/clif.cpp:25899-25906), so the last
// registration is the one the client speaks.
func TestStorage_MoveFromStoreToBody_Wire(t *testing.T) {
	frame := make([]byte, 8)
	binary.LittleEndian.PutUint16(frame[0:], 0x0365)
	binary.LittleEndian.PutUint16(frame[2:], 4) // client's warehouse slot
	binary.LittleEndian.PutUint32(frame[4:], 3)

	got, err := ParseCZMoveItemFromStoreToBody(frame)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// The source's handler subtracts 1 from the wire index
	// (clif.cpp:13677), so the parsed row is the server's 0-based storage
	// row. The offset differs from the body→store move's 2 on purpose.
	if got.Index != 3 {
		t.Errorf("Index = %d, want 3", got.Index)
	}
	if got.Amount != 3 {
		t.Errorf("Amount = %d, want 3", got.Amount)
	}
	if got.Size() != 8 {
		t.Errorf("Size = %d, want 8", got.Size())
	}

	var buf bytes.Buffer
	if err := (CZMoveItemFromStoreToBody{Index: 3, Amount: 3}).Encode(&buf); err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), frame) {
		t.Errorf("encode = %x, want %x", buf.Bytes(), frame)
	}
}

// TestStorage_MoveIndexOffsetsDiffer proves the two moves do not share one
// index convention: the same wire index means a different server row in each
// direction, because the source subtracts 2 for the bag and 1 for the
// warehouse (clif.cpp:13648 and clif.cpp:13677).
func TestStorage_MoveIndexOffsetsDiffer(t *testing.T) {
	frame := make([]byte, 8)
	binary.LittleEndian.PutUint16(frame[2:], 5)

	binary.LittleEndian.PutUint16(frame[0:], 0x0364)
	bag, err := ParseCZMoveItemFromBodyToStore(frame)
	if err != nil {
		t.Fatalf("parse body→store: %v", err)
	}
	binary.LittleEndian.PutUint16(frame[0:], 0x0365)
	store, err := ParseCZMoveItemFromStoreToBody(frame)
	if err != nil {
		t.Fatalf("parse store→body: %v", err)
	}
	if bag.Index != 3 {
		t.Errorf("bag Index = %d, want 3", bag.Index)
	}
	if store.Index != 4 {
		t.Errorf("storage Index = %d, want 4", store.Index)
	}
	if bag.Index == store.Index {
		t.Error("body→store and store→body must not share one index offset")
	}
}

// TestStorage_Move_RejectsBadCmd proves each parser refuses a frame whose cmd
// header disagrees with it (defence against hybrid frames).
func TestStorage_Move_RejectsBadCmd(t *testing.T) {
	frame := make([]byte, 8)
	binary.LittleEndian.PutUint16(frame[0:], 0x0365)
	if _, err := ParseCZMoveItemFromBodyToStore(frame); err == nil {
		t.Error("expected error parsing body→store with store→body cmd")
	}
	binary.LittleEndian.PutUint16(frame[0:], 0x0364)
	if _, err := ParseCZMoveItemFromStoreToBody(frame); err == nil {
		t.Error("expected error parsing store→body with body→store cmd")
	}
}

// TestStorage_Move_ShortFrame proves the parsers refuse a frame shorter than
// the fixed 8-byte layout.
func TestStorage_Move_ShortFrame(t *testing.T) {
	short := make([]byte, 7)
	binary.LittleEndian.PutUint16(short[0:], 0x0364)
	if _, err := ParseCZMoveItemFromBodyToStore(short); err == nil {
		t.Error("expected error for short body→store frame")
	}
	binary.LittleEndian.PutUint16(short[0:], 0x0365)
	if _, err := ParseCZMoveItemFromStoreToBody(short); err == nil {
		t.Error("expected error for short store→body frame")
	}
}

// TestStorage_Move_RejectsIndexBelowOffset proves a wire index below the
// source's offset is refused: the source's own range check drops such a
// request (item_index < 0 at clif.cpp:13648).
func TestStorage_Move_RejectsIndexBelowOffset(t *testing.T) {
	frame := make([]byte, 8)
	binary.LittleEndian.PutUint16(frame[0:], 0x0364)
	binary.LittleEndian.PutUint16(frame[2:], 1)
	if _, err := ParseCZMoveItemFromBodyToStore(frame); err == nil {
		t.Error("expected error for bag index below the offset")
	}
	binary.LittleEndian.PutUint16(frame[0:], 0x0365)
	binary.LittleEndian.PutUint16(frame[2:], 0)
	if _, err := ParseCZMoveItemFromStoreToBody(frame); err == nil {
		t.Error("expected error for storage index below the offset")
	}
}

// TestStorage_CloseStore_Request proves CZ_CLOSE_STORE is the source's bare
// 2-byte request.
//
// 0x0193 is the source's RUNTIME-table registration of clif_parse_CloseKafra:
// the source re-registers the opcode in later PACKETVER blocks and
// packetdb_addpacket overwrites an opcode's entry
// (third_party/rathenaThailand/src/map/clif.cpp:25899-25906), so the last
// registration is the one the client speaks.
func TestStorage_CloseStore_Request(t *testing.T) {
	frame := []byte{0x93, 0x01}
	if _, err := ParseCZCloseStore(frame); err != nil {
		t.Fatalf("parse: %v", err)
	}
	var buf bytes.Buffer
	if err := (CZCloseStore{}).Encode(&buf); err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), frame) {
		t.Errorf("encode = %x, want %x", buf.Bytes(), frame)
	}
	// A wrong cmd is refused.
	if _, err := ParseCZCloseStore([]byte{0x65, 0x03}); err == nil {
		t.Error("expected error for wrong cmd")
	}
	// A short frame is refused.
	if _, err := ParseCZCloseStore([]byte{0x93}); err == nil {
		t.Error("expected error for short frame")
	}
}

// TestStorage_DeleteItemFromStore_Wire proves ZC_DELETE_ITEM_FROM_STORE is the
// source's 8-byte frame: cmd 0xf6 at 0-1, the client's warehouse slot at 2-3,
// the amount at 4-7 (third_party/rathenaThailand/src/map/packets.hpp:1037-1042).
func TestStorage_DeleteItemFromStore_Wire(t *testing.T) {
	resp := StorageDeleteItemResponse{Index: 6, Amount: 25}
	if resp.Size() != 8 {
		t.Errorf("Size = %d, want 8", resp.Size())
	}
	var buf bytes.Buffer
	if err := resp.Encode(&buf); err != nil {
		t.Fatalf("encode: %v", err)
	}
	want := []byte{0xf6, 0x00, 0x06, 0x00, 0x19, 0x00, 0x00, 0x00}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("encode = %x, want %x", buf.Bytes(), want)
	}
}

// TestStorage_ItemCountInfo_Wire proves ZC_NOTIFY_STOREITEM_COUNTINFO is the
// source's 6-byte frame: cmd 0xf2 at 0-1, the current count at 2-3, the max
// count at 4-5 (third_party/rathenaThailand/src/map/packets.hpp:1044-1048).
func TestStorage_ItemCountInfo_Wire(t *testing.T) {
	resp := StorageItemCountInfoResponse{Amount: 30, MaxAmount: 600}
	if resp.Size() != 6 {
		t.Errorf("Size = %d, want 6", resp.Size())
	}
	var buf bytes.Buffer
	if err := resp.Encode(&buf); err != nil {
		t.Fatalf("encode: %v", err)
	}
	want := []byte{0xf2, 0x00, 0x1e, 0x00, 0x58, 0x02}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("encode = %x, want %x", buf.Bytes(), want)
	}
}

// TestStorage_CloseStore_Response proves ZC_CLOSE_STORE is the source's bare
// 2-byte notice (third_party/rathenaThailand/src/map/packets.hpp:1032-1035).
func TestStorage_CloseStore_Response(t *testing.T) {
	resp := StorageCloseResponse{}
	if resp.Size() != 2 {
		t.Errorf("Size = %d, want 2", resp.Size())
	}
	var buf bytes.Buffer
	if err := resp.Encode(&buf); err != nil {
		t.Fatalf("encode: %v", err)
	}
	want := []byte{0xf8, 0x00}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("encode = %x, want %x", buf.Bytes(), want)
	}
}

// TestStorage_ListNormal_Wire proves ZC_STORE_ITEMLIST_NORMAL carries the
// source's header and entries: cmd storageListNormalType (0xb09) at 0-1, the
// length at 2-3, the invType byte at 4, and 26-byte NORMALITEM_INFO entries
// from offset 5.
//
// The length is the source's own formula (clif.cpp:3216): the struct minus
// its list array, plus the entries written — 5 + 26*count here.
func TestStorage_ListNormal_Wire(t *testing.T) {
	items := []InventoryNormalItem{
		{Index: 1, ITID: 501, Type: 0, Count: 10, Flag: 1},
		{Index: 2, ITID: 502, Type: 0, Count: 5, Flag: 1},
	}
	resp := StorageListNormalResponse{InvType: StorageInvType, Items: items}
	want := 5 + 26*len(items)
	if resp.Size() != want {
		t.Errorf("Size = %d, want %d", resp.Size(), want)
	}
	var buf bytes.Buffer
	if err := resp.Encode(&buf); err != nil {
		t.Fatalf("encode: %v", err)
	}
	if buf.Len() != want {
		t.Errorf("encode len = %d, want %d", buf.Len(), want)
	}
	out := buf.Bytes()
	if cmd := binary.LittleEndian.Uint16(out[0:2]); cmd != opcode("storageListNormalType") {
		t.Errorf("cmd = 0x%04x, want 0x%04x", cmd, opcode("storageListNormalType"))
	}
	if length := binary.LittleEndian.Uint16(out[2:4]); length != uint16(want) {
		t.Errorf("packetLength = %d, want %d", length, want)
	}
	if out[4] != StorageInvType {
		t.Errorf("invType = %d, want %d", out[4], StorageInvType)
	}
	// The first entry starts right after the 5-byte header, and the second
	// entry starts one entry later — a wrong entry offset or a wrong entry
	// size would move it.
	first := out[5 : 5+sizeNormalItem]
	if idx := binary.LittleEndian.Uint16(first[0:2]); idx != 1 {
		t.Errorf("first entry index = %d, want 1", idx)
	}
	if itid := binary.LittleEndian.Uint16(first[2:4]); itid != 501 {
		t.Errorf("first entry ITID = %d, want 501", itid)
	}
	if count := binary.LittleEndian.Uint16(first[5:7]); count != 10 {
		t.Errorf("first entry count = %d, want 10", count)
	}
	second := out[5+sizeNormalItem : 5+2*sizeNormalItem]
	if idx := binary.LittleEndian.Uint16(second[0:2]); idx != 2 {
		t.Errorf("second entry index = %d, want 2", idx)
	}
	if itid := binary.LittleEndian.Uint16(second[2:4]); itid != 502 {
		t.Errorf("second entry ITID = %d, want 502", itid)
	}
}

// TestStorage_ListEquip_Wire proves ZC_STORE_ITEMLIST_EQUIP carries the
// source's header and entries: cmd storageListEquipType (0xb39) at 0-1, the
// length at 2-3, the invType byte at 4, and 57-byte EQUIPITEM_INFO entries
// from offset 5.
//
// The length is the source's own formula (clif.cpp:3182) — 5 + 57*count here.
func TestStorage_ListEquip_Wire(t *testing.T) {
	items := []InventoryEquipItem{
		{Index: 1, ITID: 1101, Type: 3, Location: 0x0001, RefiningLevel: 7, ItemSpriteNumber: 1, Flag: 1},
	}
	resp := StorageListEquipResponse{InvType: StorageInvType, Items: items}
	want := 5 + 57*len(items)
	if resp.Size() != want {
		t.Errorf("Size = %d, want %d", resp.Size(), want)
	}
	var buf bytes.Buffer
	if err := resp.Encode(&buf); err != nil {
		t.Fatalf("encode: %v", err)
	}
	if buf.Len() != want {
		t.Errorf("encode len = %d, want %d", buf.Len(), want)
	}
	out := buf.Bytes()
	if cmd := binary.LittleEndian.Uint16(out[0:2]); cmd != opcode("storageListEquipType") {
		t.Errorf("cmd = 0x%04x, want 0x%04x", cmd, opcode("storageListEquipType"))
	}
	if length := binary.LittleEndian.Uint16(out[2:4]); length != uint16(want) {
		t.Errorf("packetLength = %d, want %d", length, want)
	}
	if out[4] != StorageInvType {
		t.Errorf("invType = %d, want %d", out[4], StorageInvType)
	}
	entry := out[5 : 5+sizeEquipItem]
	if idx := binary.LittleEndian.Uint16(entry[0:2]); idx != 1 {
		t.Errorf("entry index = %d, want 1", idx)
	}
	if itid := binary.LittleEndian.Uint16(entry[2:4]); itid != 1101 {
		t.Errorf("entry ITID = %d, want 1101", itid)
	}
	if refine := entry[13]; refine != 7 {
		t.Errorf("entry refine = %d, want 7", refine)
	}
}

// TestStorage_ListEmpty proves an empty list still emits the header frame
// (cmd + length 5 + invType) so the client can distinguish "empty" from "no
// list at all".
func TestStorage_ListEmpty(t *testing.T) {
	for _, tc := range []struct {
		name string
		fn   func(*bytes.Buffer) error
		cmd  uint16
	}{
		{"normal", func(b *bytes.Buffer) error { return StorageListNormalResponse{InvType: StorageInvType}.Encode(b) }, opcode("storageListNormalType")},
		{"equip", func(b *bytes.Buffer) error { return StorageListEquipResponse{InvType: StorageInvType}.Encode(b) }, opcode("storageListEquipType")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := tc.fn(&buf); err != nil {
				t.Fatalf("encode: %v", err)
			}
			if buf.Len() != 5 {
				t.Errorf("encode len = %d, want 5", buf.Len())
			}
			out := buf.Bytes()
			if cmd := binary.LittleEndian.Uint16(out[0:2]); cmd != tc.cmd {
				t.Errorf("cmd = 0x%04x, want 0x%04x", cmd, tc.cmd)
			}
			if length := binary.LittleEndian.Uint16(out[2:4]); length != 5 {
				t.Errorf("packetLength = %d, want 5", length)
			}
			if out[4] != StorageInvType {
				t.Errorf("invType = %d, want %d", out[4], StorageInvType)
			}
		})
	}
}

// TestStorage_EncodeStorageOpen proves the server-side warehouse-open burst
// is the two item lists followed by the item count, in the source's order
// (storage.cpp:147-148).
func TestStorage_EncodeStorageOpen(t *testing.T) {
	normal := []InventoryNormalItem{{Index: 1, ITID: 501, Count: 10}}
	equip := []InventoryEquipItem{{Index: 2, ITID: 1101, RefiningLevel: 7}}
	var buf bytes.Buffer
	if err := EncodeStorageOpen(&buf, normal, equip, 12, 600); err != nil {
		t.Fatalf("encode: %v", err)
	}
	out := buf.Bytes()
	normalLen := 5 + 26*len(normal)
	equipLen := 5 + 57*len(equip)
	want := normalLen + equipLen + 6
	if len(out) != want {
		t.Fatalf("burst len = %d, want %d", len(out), want)
	}
	if cmd := binary.LittleEndian.Uint16(out[0:2]); cmd != opcode("storageListNormalType") {
		t.Errorf("first cmd = 0x%04x, want 0x%04x", cmd, opcode("storageListNormalType"))
	}
	if cmd := binary.LittleEndian.Uint16(out[normalLen : normalLen+2]); cmd != opcode("storageListEquipType") {
		t.Errorf("second cmd = 0x%04x, want 0x%04x", cmd, opcode("storageListEquipType"))
	}
	countAt := normalLen + equipLen
	if cmd := binary.LittleEndian.Uint16(out[countAt : countAt+2]); cmd != opcode("ZC_NOTIFY_STOREITEM_COUNTINFO") {
		t.Errorf("third cmd = 0x%04x, want 0x%04x", cmd, opcode("ZC_NOTIFY_STOREITEM_COUNTINFO"))
	}
	if amount := binary.LittleEndian.Uint16(out[countAt+2 : countAt+4]); amount != 12 {
		t.Errorf("amount = %d, want 12", amount)
	}
	if maxAmount := binary.LittleEndian.Uint16(out[countAt+4 : countAt+6]); maxAmount != 600 {
		t.Errorf("maxAmount = %d, want 600", maxAmount)
	}
}
