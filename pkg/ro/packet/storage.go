package packet

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Storage packet family for the Thai fork (rAthena-Thailand/rathenaThailand)
// at PACKETVER 20250604.
//
// The wire shapes come from the source pack, not from a hand-derived table:
//
//   - Request opcodes and lengths: the source's RUNTIME packet table, which
//     is what the client speaks. The source registers the same opcode in
//     several PACKETVER blocks and packetdb_addpacket overwrites an
//     opcode's entry
//     (third_party/rathenaThailand/src/map/clif.cpp:25899-25906), so the
//     effective registration for each handler is its LAST one. At
//     PACKETVER 20250604 the runtime table holds:
//
//	clif_parse_MoveToKafra:   0x0364 (len 8, index at 2, amount at 4)
//	                          plus 0x07ec, 0x086c, 0x0893, 0x08a0,
//	                          0x08ac, 0x093b
//	clif_parse_MoveFromKafra: 0x0365 (len 8, index at 2, amount at 4)
//	                          plus 0x0861, 0x0874, 0x08a6, 0x0963
//	clif_parse_CloseKafra:    0x0193 (len 2)
//
//     The handlers are registered at
//     third_party/rathenaThailand/src/map/clif_packetdb.hpp:95-97 (the first
//     block) and re-registered in the later PACKETVER blocks of the same
//     file; the lookup by handler resolves the last registration, which is
//     the one above. The registered field offsets (2 and 4) are where the
//     source's handlers read the index and the amount, so the wire layout
//     is cmd uint16 | index uint16 | amount uint32.
//
//   - Response structs: third_party/rathenaThailand/src/map/packets.hpp:1032-1048
//
//	PACKET_ZC_CLOSE_STORE            { int16 packetType; }                    (0xf8)
//	PACKET_ZC_DELETE_ITEM_FROM_STORE { int16 packetType; uint16 index;
//	                                    uint32 amount; }                       (0xf6)
//	PACKET_ZC_NOTIFY_STOREITEM_COUNTINFO { int16 packetType; uint16 amount;
//	                                    uint16 max_amount; }                  (0xf2)
//
//   - Storage item lists: third_party/rathenaThailand/src/map/packets_struct.hpp:1205-1260
//     (ZC_STORE_ITEMLIST_NORMAL / ZC_STORE_ITEMLIST_EQUIP). Both carry an
//     invType byte at PACKETVER_RE_NUM >= 20180912, and both compute their
//     length as (sizeof(struct) - sizeof(list)) + sizeof(entry) * count
//     (third_party/rathenaThailand/src/map/clif.cpp:3182-3235).
//
// The item entries are the same NORMALITEM_INFO / EQUIPITEM_INFO shapes the
// inventory burst writes, so writeNormalItem / writeEquipItem are reused
// verbatim.

// StorageInvType is the invType byte the source stamps on the warehouse item
// lists: INVTYPE_STORAGE. Source: third_party/rathenaThailand/src/map/clif.cpp:99-104
// (enum e_inventory_type) and :3161 (clif_storagelist sets
// `e_inventory_type type = INVTYPE_STORAGE`).
const StorageInvType uint8 = 2

// bagIndexOffset is the gap between a bag server row and the index the
// client sends for a body→store move: the source computes
// `item_index = RFIFOW(fd,info->pos[0])-2` in clif_parse_MoveToKafra
// (third_party/rathenaThailand/src/map/clif.cpp:13648).
const bagIndexOffset uint16 = 2

// storageIndexOffset is the gap between a warehouse server row and the index
// the client sends for a store→body move: the source computes
// `item_index = RFIFOW(fd,info->pos[0])-1` in clif_parse_MoveFromKafra
// (third_party/rathenaThailand/src/map/clif.cpp:13677). It differs from the
// bag offset on purpose — the warehouse grid has no reserved first slots.
const storageIndexOffset uint16 = 1

// The on-wire lengths of the source's kafra storage requests. The source's
// runtime packet table registers the two move requests with length 8
// (field offsets 2 and 4) and the close request with length 2.
const (
	sizeCZMoveItemFromBodyToStore = 8
	sizeCZMoveItemFromStoreToBody = 8
	sizeCZCloseStore              = 2
)

// CZMoveItemFromBodyToStore is the parsed form of the source's
// CZ_MOVE_ITEM_FROM_BODY_TO_STORE request (opcode 0x0364, 8 bytes).
//
// The opcode is the source's runtime-table registration of
// clif_parse_MoveToKafra: the source re-registers the opcode in later
// PACKETVER blocks and packetdb_addpacket overwrites an opcode's entry
// (third_party/rathenaThailand/src/map/clif.cpp:25899-25906), so the last
// registration is the one the client speaks.
//
// Wire layout (the runtime registration carries the field offsets 2 and 4):
//
//	uint16 cmd   (0x0364)
//	uint16 index (the client's bag slot)
//	uint32 amount
//
// Index is the server's 0-based bag row, the value the source's handler
// works with: clif_parse_MoveToKafra reads the wire index and subtracts 2
// (third_party/rathenaThailand/src/map/clif.cpp:13648), because the client's
// first two grid slots are reserved. Encode adds the same 2 back, so the
// round trip is exact.
type CZMoveItemFromBodyToStore struct {
	// Index is the 0-based bag row the item moves from.
	Index uint16
	// Amount is the stack count to deposit.
	Amount uint32
}

// Size returns the on-wire byte length of the request (always 8).
func (CZMoveItemFromBodyToStore) Size() int { return sizeCZMoveItemFromBodyToStore }

// ParseCZMoveItemFromBodyToStore decodes a CZ_MOVE_ITEM_FROM_BODY_TO_STORE
// frame.
//
// The index is returned as the server's bag row: the source's handler
// subtracts 2 from the wire index
// (third_party/rathenaThailand/src/map/clif.cpp:13648), and a wire index below
// that offset is rejected here because the source's own range check
// (`item_index < 0`) drops the request.
func ParseCZMoveItemFromBodyToStore(frame []byte) (CZMoveItemFromBodyToStore, error) {
	if len(frame) < sizeCZMoveItemFromBodyToStore {
		return CZMoveItemFromBodyToStore{}, fmt.Errorf("packet: parse CZ_MOVE_ITEM_FROM_BODY_TO_STORE: want at least %d bytes, got %d", sizeCZMoveItemFromBodyToStore, len(frame))
	}
	if cmd := binary.LittleEndian.Uint16(frame[0:2]); cmd != HeaderCZMOVEITEMFROMBODYTOSTORE() {
		return CZMoveItemFromBodyToStore{}, fmt.Errorf("packet: parse CZ_MOVE_ITEM_FROM_BODY_TO_STORE: unexpected cmd 0x%04x", cmd)
	}
	wire := binary.LittleEndian.Uint16(frame[2:4])
	if wire < bagIndexOffset {
		return CZMoveItemFromBodyToStore{}, fmt.Errorf("packet: parse CZ_MOVE_ITEM_FROM_BODY_TO_STORE: index %d is below the bag offset %d", wire, bagIndexOffset)
	}
	return CZMoveItemFromBodyToStore{
		Index:  wire - bagIndexOffset,
		Amount: binary.LittleEndian.Uint32(frame[4:8]),
	}, nil
}

// Encode writes the CZ_MOVE_ITEM_FROM_BODY_TO_STORE packet to w.
func (r CZMoveItemFromBodyToStore) Encode(w io.Writer) error {
	buf := make([]byte, sizeCZMoveItemFromBodyToStore)
	binary.LittleEndian.PutUint16(buf[0:], HeaderCZMOVEITEMFROMBODYTOSTORE())
	binary.LittleEndian.PutUint16(buf[2:], r.Index+bagIndexOffset)
	binary.LittleEndian.PutUint32(buf[4:], r.Amount)
	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write CZ_MOVE_ITEM_FROM_BODY_TO_STORE: %w", err)
	}
	return nil
}

// CZMoveItemFromStoreToBody is the parsed form of the source's
// CZ_MOVE_ITEM_FROM_STORE_TO_BODY request (opcode 0x0365, 8 bytes).
//
// The opcode is the source's runtime-table registration of
// clif_parse_MoveFromKafra: the source re-registers the opcode in later
// PACKETVER blocks and packetdb_addpacket overwrites an opcode's entry
// (third_party/rathenaThailand/src/map/clif.cpp:25899-25906), so the last
// registration is the one the client speaks.
//
// Wire layout (the runtime registration carries the field offsets 2 and 4):
//
//	uint16 cmd   (0x0365)
//	uint16 index (the client's warehouse slot)
//	uint32 amount
//
// Index is the server's 0-based warehouse row, the value the source's handler
// works with: clif_parse_MoveFromKafra reads the wire index and subtracts 1
// (third_party/rathenaThailand/src/map/clif.cpp:13677). The offset differs
// from the body→store move's offset (2) on purpose. Encode adds the same 1
// back, so the round trip is exact.
type CZMoveItemFromStoreToBody struct {
	// Index is the 0-based warehouse row the item moves from.
	Index uint16
	// Amount is the stack count to withdraw.
	Amount uint32
}

// Size returns the on-wire byte length of the request (always 8).
func (CZMoveItemFromStoreToBody) Size() int { return sizeCZMoveItemFromStoreToBody }

// ParseCZMoveItemFromStoreToBody decodes a CZ_MOVE_ITEM_FROM_STORE_TO_BODY
// frame.
//
// The index is returned as the server's warehouse row: the source's handler
// subtracts 1 from the wire index
// (third_party/rathenaThailand/src/map/clif.cpp:13677), and a wire index below
// that offset is rejected here because the source's own range check drops the
// request.
func ParseCZMoveItemFromStoreToBody(frame []byte) (CZMoveItemFromStoreToBody, error) {
	if len(frame) < sizeCZMoveItemFromStoreToBody {
		return CZMoveItemFromStoreToBody{}, fmt.Errorf("packet: parse CZ_MOVE_ITEM_FROM_STORE_TO_BODY: want at least %d bytes, got %d", sizeCZMoveItemFromStoreToBody, len(frame))
	}
	if cmd := binary.LittleEndian.Uint16(frame[0:2]); cmd != HeaderCZMOVEITEMFROMSTORETOBODY() {
		return CZMoveItemFromStoreToBody{}, fmt.Errorf("packet: parse CZ_MOVE_ITEM_FROM_STORE_TO_BODY: unexpected cmd 0x%04x", cmd)
	}
	wire := binary.LittleEndian.Uint16(frame[2:4])
	if wire < storageIndexOffset {
		return CZMoveItemFromStoreToBody{}, fmt.Errorf("packet: parse CZ_MOVE_ITEM_FROM_STORE_TO_BODY: index %d is below the storage offset %d", wire, storageIndexOffset)
	}
	return CZMoveItemFromStoreToBody{
		Index:  wire - storageIndexOffset,
		Amount: binary.LittleEndian.Uint32(frame[4:8]),
	}, nil
}

// Encode writes the CZ_MOVE_ITEM_FROM_STORE_TO_BODY packet to w.
func (r CZMoveItemFromStoreToBody) Encode(w io.Writer) error {
	buf := make([]byte, sizeCZMoveItemFromStoreToBody)
	binary.LittleEndian.PutUint16(buf[0:], HeaderCZMOVEITEMFROMSTORETOBODY())
	binary.LittleEndian.PutUint16(buf[2:], r.Index+storageIndexOffset)
	binary.LittleEndian.PutUint32(buf[4:], r.Amount)
	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write CZ_MOVE_ITEM_FROM_STORE_TO_BODY: %w", err)
	}
	return nil
}

// CZCloseStore is the parsed form of the source's CZ_CLOSE_STORE request
// (opcode 0x0193, 2 bytes).
//
// The opcode is the source's runtime-table registration of
// clif_parse_CloseKafra: the source re-registers the opcode in later
// PACKETVER blocks and packetdb_addpacket overwrites an opcode's entry
// (third_party/rathenaThailand/src/map/clif.cpp:25899-25906), so the last
// registration is the one the client speaks.
//
// Wire layout (the runtime registration carries no field offsets):
//
//	uint16 cmd (0x0193)
//
// The request carries no body: the source's handler
// (third_party/rathenaThailand/src/map/clif.cpp:13747) only clears the
// session's storage flag.
type CZCloseStore struct{}

// Size returns the on-wire byte length of the request (always 2).
func (CZCloseStore) Size() int { return sizeCZCloseStore }

// ParseCZCloseStore decodes a CZ_CLOSE_STORE frame.
func ParseCZCloseStore(frame []byte) (CZCloseStore, error) {
	if len(frame) < sizeCZCloseStore {
		return CZCloseStore{}, fmt.Errorf("packet: parse CZ_CLOSE_STORE: want at least %d bytes, got %d", sizeCZCloseStore, len(frame))
	}
	if cmd := binary.LittleEndian.Uint16(frame[0:2]); cmd != HeaderCZCLOSESTORE() {
		return CZCloseStore{}, fmt.Errorf("packet: parse CZ_CLOSE_STORE: unexpected cmd 0x%04x", cmd)
	}
	return CZCloseStore{}, nil
}

// Encode writes the CZ_CLOSE_STORE packet to w.
func (CZCloseStore) Encode(w io.Writer) error {
	buf := make([]byte, sizeCZCloseStore)
	binary.LittleEndian.PutUint16(buf[0:], HeaderCZCLOSESTORE())
	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write CZ_CLOSE_STORE: %w", err)
	}
	return nil
}

// StorageDeleteItemResponse encodes the source's ZC_DELETE_ITEM_FROM_STORE
// (0xf6, 8 bytes) — the server's notice that a warehouse row was removed.
//
// Wire layout (third_party/rathenaThailand/src/map/packets.hpp:1037-1042):
//
//	uint16 cmd   (0xf6)
//	uint16 index (the client's warehouse slot)
//	uint32 amount
//
// Index is the client-facing warehouse slot, the value the source stamps on
// the packet (third_party/rathenaThailand/src/map/clif.cpp:4943 uses
// client_storage_index, i.e. server row + 1).
type StorageDeleteItemResponse struct {
	// Index is the client-facing warehouse slot of the removed row.
	Index uint16
	// Amount is the removed stack count.
	Amount uint32
}

// Size returns the on-wire byte length Encode will write (always 8).
func (StorageDeleteItemResponse) Size() int {
	return layout("PACKET_ZC_DELETE_ITEM_FROM_STORE")
}

// Encode writes the ZC_DELETE_ITEM_FROM_STORE packet to w.
func (r StorageDeleteItemResponse) Encode(w io.Writer) error {
	buf := make([]byte, r.Size())
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCDELETEITEMFROMSTORE())
	binary.LittleEndian.PutUint16(buf[2:], r.Index)
	binary.LittleEndian.PutUint32(buf[4:], r.Amount)
	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write ZC_DELETE_ITEM_FROM_STORE: %w", err)
	}
	return nil
}

// StorageItemCountInfoResponse encodes the source's
// ZC_NOTIFY_STOREITEM_COUNTINFO (0xf2, 6 bytes) — the warehouse's current
// and maximum item counts.
//
// Wire layout (third_party/rathenaThailand/src/map/packets.hpp:1044-1048):
//
//	uint16 cmd   (0xf2)
//	uint16 amount     (current item count)
//	uint16 maxAmount  (maximum item count)
//
// The source emits it right after the item lists when the warehouse opens
// (third_party/rathenaThailand/src/map/storage.cpp:147-148 calls
// clif_storagelist then clif_updatestorageamount).
type StorageItemCountInfoResponse struct {
	// Amount is the warehouse's current item count.
	Amount uint16
	// MaxAmount is the warehouse's maximum item count.
	MaxAmount uint16
}

// Size returns the on-wire byte length Encode will write (always 6).
func (StorageItemCountInfoResponse) Size() int {
	return layout("PACKET_ZC_NOTIFY_STOREITEM_COUNTINFO")
}

// Encode writes the ZC_NOTIFY_STOREITEM_COUNTINFO packet to w.
func (r StorageItemCountInfoResponse) Encode(w io.Writer) error {
	buf := make([]byte, r.Size())
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCNOTIFYSTOREITEMCOUNTINFO())
	binary.LittleEndian.PutUint16(buf[2:], r.Amount)
	binary.LittleEndian.PutUint16(buf[4:], r.MaxAmount)
	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write ZC_NOTIFY_STOREITEM_COUNTINFO: %w", err)
	}
	return nil
}

// StorageCloseResponse encodes the source's ZC_CLOSE_STORE (0xf8, 2 bytes) —
// the server's notice that the warehouse is closed.
//
// Wire layout (third_party/rathenaThailand/src/map/packets.hpp:1032-1035):
//
//	uint16 cmd (0xf8)
type StorageCloseResponse struct{}

// Size returns the on-wire byte length Encode will write (always 2).
func (StorageCloseResponse) Size() int { return layout("PACKET_ZC_CLOSE_STORE") }

// Encode writes the ZC_CLOSE_STORE packet to w.
func (r StorageCloseResponse) Encode(w io.Writer) error {
	buf := make([]byte, r.Size())
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCSTORECLOSE())
	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write ZC_CLOSE_STORE: %w", err)
	}
	return nil
}

// StorageListNormalResponse encodes the source's ZC_STORE_ITEMLIST_NORMAL
// (opcode storageListNormalType, 0xb09 at PACKETVER 20250604) — the
// warehouse's stackable rows.
//
// Wire layout (third_party/rathenaThailand/src/map/packets_struct.hpp:1205-1214
// + NORMALITEM_INFO:418-448):
//
//	uint16  packetType   (0xb09)
//	uint16  packetLength (5 + 26 * len(Items))
//	uint8   invType      (INVTYPE_STORAGE = 2)
//	[per item, 26 bytes:] InventoryNormalItem
//
// The length is the source's own formula
// (third_party/rathenaThailand/src/map/clif.cpp:3216): the struct minus its
// list array, plus the entries actually written.
type StorageListNormalResponse struct {
	// InvType is the inventory type byte; the source stamps
	// INVTYPE_STORAGE on the warehouse lists.
	InvType uint8
	// Items holds the stackable warehouse rows.
	Items []InventoryNormalItem
}

// Size returns the on-wire byte length Encode will write.
func (r StorageListNormalResponse) Size() int {
	return sizeStorageListHeader + len(r.Items)*sizeNormalItem
}

// Encode writes the ZC_STORE_ITEMLIST_NORMAL packet to w.
func (r StorageListNormalResponse) Encode(w io.Writer) error {
	total := r.Size()
	if total > 0xffff {
		return fmt.Errorf("packet: write ZC_STORE_ITEMLIST_NORMAL: too many items (%d)", len(r.Items))
	}
	buf := make([]byte, total)
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCSTOREITEMLISTNORMAL())
	binary.LittleEndian.PutUint16(buf[2:], uint16(total)) //nolint:gosec // G115: the length is bounded by the check above.
	buf[4] = r.InvType
	for i, it := range r.Items {
		writeNormalItem(buf, sizeStorageListHeader+i*sizeNormalItem, it)
	}
	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write ZC_STORE_ITEMLIST_NORMAL: %w", err)
	}
	return nil
}

// StorageListEquipResponse encodes the source's ZC_STORE_ITEMLIST_EQUIP
// (opcode storageListEquipType, 0xb39 at PACKETVER 20250604) — the
// warehouse's equipment rows.
//
// Wire layout (third_party/rathenaThailand/src/map/packets_struct.hpp:1250-1259
// + EQUIPITEM_INFO:457-507):
//
//	uint16  packetType   (0xb39)
//	uint16  packetLength (5 + 57 * len(Items))
//	uint8   invType      (INVTYPE_STORAGE = 2)
//	[per item, 57 bytes:] InventoryEquipItem
//
// The length is the source's own formula
// (third_party/rathenaThailand/src/map/clif.cpp:3182): the struct minus its
// list array, plus the entries actually written.
type StorageListEquipResponse struct {
	// InvType is the inventory type byte; the source stamps
	// INVTYPE_STORAGE on the warehouse lists.
	InvType uint8
	// Items holds the equipment warehouse rows.
	Items []InventoryEquipItem
}

// Size returns the on-wire byte length Encode will write.
func (r StorageListEquipResponse) Size() int {
	return sizeStorageListHeader + len(r.Items)*sizeEquipItem
}

// Encode writes the ZC_STORE_ITEMLIST_EQUIP packet to w.
func (r StorageListEquipResponse) Encode(w io.Writer) error {
	total := r.Size()
	if total > 0xffff {
		return fmt.Errorf("packet: write ZC_STORE_ITEMLIST_EQUIP: too many items (%d)", len(r.Items))
	}
	buf := make([]byte, total)
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCSTOREITEMLISTEQUIP())
	binary.LittleEndian.PutUint16(buf[2:], uint16(total)) //nolint:gosec // G115: the length is bounded by the check above.
	buf[4] = r.InvType
	for i, it := range r.Items {
		writeEquipItem(buf, sizeStorageListHeader+i*sizeEquipItem, it)
	}
	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write ZC_STORE_ITEMLIST_EQUIP: %w", err)
	}
	return nil
}

// EncodeStorageOpen writes the source's warehouse-open burst: the two item
// lists followed by the item count.
//
// The source opens the warehouse from the NPC script
// (third_party/rathenaThailand/src/map/script.cpp:10966 calls
// storage_storageopen), and storage_storageopen sends the lists and then the
// count (third_party/rathenaThailand/src/map/storage.cpp:147-148):
//
//	clif_storagelist(sd, sd->storage.u.items_storage, ...);
//	clif_updatestorageamount(*sd, sd->storage.amount, sd->storage.max_amount);
//
// There is no client request that opens the warehouse, so the burst is a
// server-side entry point: the gateway calls it when the player talks to the
// kafra NPC.
func EncodeStorageOpen(w io.Writer, normal []InventoryNormalItem, equip []InventoryEquipItem, amount, maxAmount uint16) error {
	if err := (StorageListNormalResponse{InvType: StorageInvType, Items: normal}).Encode(w); err != nil {
		return fmt.Errorf("packet: write warehouse open: %w", err)
	}
	if err := (StorageListEquipResponse{InvType: StorageInvType, Items: equip}).Encode(w); err != nil {
		return fmt.Errorf("packet: write warehouse open: %w", err)
	}
	if err := (StorageItemCountInfoResponse{Amount: amount, MaxAmount: maxAmount}).Encode(w); err != nil {
		return fmt.Errorf("packet: write warehouse open: %w", err)
	}
	return nil
}
