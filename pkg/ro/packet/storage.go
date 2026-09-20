package packet

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Storage packet family for PACKETVER 20250604. The wire layout mirrors
// rAthena's inventory-item structure (NORMALITEM_INFO / EQUIPITEM_INFO at
// packets_struct.hpp:418-507) because rAthena stores storage rows in the
// same shapes as inventory rows. We reuse writeNormalItem / writeEquipItem.
//
// Source layout references:
//
//   - third_party/rathenaThailand/src/map/clif.cpp:7801-7850 (clif_storageList)
//     — the per-list encoders for ZC_STORE_NORMALITEMLIST (0x07e9) +
//     ZC_STORE_EQUIPMENTITEMLIST (0x07ea).
//   - third_party/rathenaThailand/src/map/clif.cpp:8020-8035 (clif_storageItemListResult)
//     — ZC_STOREITEMLISTRESULT (0x07eb) on deposit / withdraw.
//   - third_party/rathenaThailand/src/map/clif.cpp:8018-8019 (clif_storageOpen)
//     — ZC_ACCEPT_ENTER2 (0x07e3) on warehouse open.

// CZ_REQ_OPENSTORE2 is the parsed form of CZ_REQ_OPENSTORE2 (header 0x07e4).
// Layout for PACKETVER 20250604 (clif_packetdb.hpp binds 0x07e4,26):
//
//	int16 packetType (0x07e4)
//	char  accountName[24]
//
// rAthena opens the warehouse on any post-NPC-trade storage access. We only
// need the accountName to validate the warehouse belongs to the requesting
// connection; the gateway already binds accountID through CZ_ENTER, so this
// field is informational and not used by the handler.
type CZReqOpenStore2 struct {
	AccountName string
}

// sizeCZReqOpenStore2 is the fixed on-wire byte count for the post-20170412
// PACKETVER_MAIN_NUM CZ_REQ_OPENSTORE2 form.
const sizeCZReqOpenStore2 = 26

// SizeCZReqOpenStore2 is the exported alias used by the gateway dispatch
// table to register the fixed-size handler entry.
const SizeCZReqOpenStore2 = sizeCZReqOpenStore2

// ParseCZReqOpenStore2 decodes a CZ_REQ_OPENSTORE2 frame.
func ParseCZReqOpenStore2(frame []byte) (CZReqOpenStore2, error) {
	if len(frame) < sizeCZReqOpenStore2 {
		return CZReqOpenStore2{}, fmt.Errorf("packet: parse CZ_REQ_OPENSTORE2: want at least %d bytes, got %d", sizeCZReqOpenStore2, len(frame))
	}
	if cmd := binary.LittleEndian.Uint16(frame[0:2]); cmd != HeaderCZREQOPENSTORE2 {
		return CZReqOpenStore2{}, fmt.Errorf("packet: parse CZ_REQ_OPENSTORE2: unexpected cmd 0x%04x", cmd)
	}
	return CZReqOpenStore2{AccountName: readNameField(frame[2:], 0, 24)}, nil
}

// Encode writes the CZ_REQ_OPENSTORE2 packet to w (used by the e2e harness).
func (r CZReqOpenStore2) Encode(w io.Writer) error {
	var buf [sizeCZReqOpenStore2]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderCZREQOPENSTORE2)
	writeNameField(buf[:], 2, r.AccountName)
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write CZ_REQ_OPENSTORE2: %w", err)
	}
	return nil
}

// CZMoveItemToStore2 is the parsed form of CZ_MOVE_ITEM_TO_STORE2 (header
// 0x07e6, 8 bytes). Index is the bag grid slot (server row + 2 per rAthena's
// client_index convention); Amount is the stack count to deposit.
type CZMoveItemToStore2 struct {
	Index  uint16
	Amount uint32
}

const sizeCZMoveItemToStore2 = 8

// SizeCZMoveItemToStore2 is the exported alias used by the gateway dispatch
// table to register the fixed-size handler entry.
const SizeCZMoveItemToStore2 = sizeCZMoveItemToStore2

// ParseCZMoveItemToStore2 decodes a CZ_MOVE_ITEM_TO_STORE2 frame.
func ParseCZMoveItemToStore2(frame []byte) (CZMoveItemToStore2, error) {
	if len(frame) < sizeCZMoveItemToStore2 {
		return CZMoveItemToStore2{}, fmt.Errorf("packet: parse CZ_MOVE_ITEM_TO_STORE2: want at least %d bytes, got %d", sizeCZMoveItemToStore2, len(frame))
	}
	if cmd := binary.LittleEndian.Uint16(frame[0:2]); cmd != HeaderCZMOVEITEMTOSTORE2 {
		return CZMoveItemToStore2{}, fmt.Errorf("packet: parse CZ_MOVE_ITEM_TO_STORE2: unexpected cmd 0x%04x", cmd)
	}
	return CZMoveItemToStore2{
		Index:  binary.LittleEndian.Uint16(frame[2:4]),
		Amount: binary.LittleEndian.Uint32(frame[4:8]),
	}, nil
}

// Encode writes the CZ_MOVE_ITEM_TO_STORE2 packet to w (used by the e2e harness).
func (r CZMoveItemToStore2) Encode(w io.Writer) error {
	var buf [sizeCZMoveItemToStore2]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderCZMOVEITEMTOSTORE2)
	binary.LittleEndian.PutUint16(buf[2:], r.Index)
	binary.LittleEndian.PutUint32(buf[4:], r.Amount)
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write CZ_MOVE_ITEM_TO_STORE2: %w", err)
	}
	return nil
}

// CZMoveItemToBody2 is the parsed form of CZ_MOVE_ITEM_TO_BODY2 (header
// 0x07e7, 8 bytes). Index is the warehouse slot (server row + 2); Amount is
// the stack count to withdraw.
type CZMoveItemToBody2 struct {
	Index  uint16
	Amount uint32
}

const sizeCZMoveItemToBody2 = 8

// SizeCZMoveItemToBody2 is the exported alias used by the gateway dispatch
// table to register the fixed-size handler entry.
const SizeCZMoveItemToBody2 = sizeCZMoveItemToBody2

// ParseCZMoveItemToBody2 decodes a CZ_MOVE_ITEM_TO_BODY2 frame.
func ParseCZMoveItemToBody2(frame []byte) (CZMoveItemToBody2, error) {
	if len(frame) < sizeCZMoveItemToBody2 {
		return CZMoveItemToBody2{}, fmt.Errorf("packet: parse CZ_MOVE_ITEM_TO_BODY2: want at least %d bytes, got %d", sizeCZMoveItemToBody2, len(frame))
	}
	if cmd := binary.LittleEndian.Uint16(frame[0:2]); cmd != HeaderCZMOVEITEMTOBODY2 {
		return CZMoveItemToBody2{}, fmt.Errorf("packet: parse CZ_MOVE_ITEM_TO_BODY2: unexpected cmd 0x%04x", cmd)
	}
	return CZMoveItemToBody2{
		Index:  binary.LittleEndian.Uint16(frame[2:4]),
		Amount: binary.LittleEndian.Uint32(frame[4:8]),
	}, nil
}

// Encode writes the CZ_MOVE_ITEM_TO_BODY2 packet to w (used by the e2e harness).
func (r CZMoveItemToBody2) Encode(w io.Writer) error {
	var buf [sizeCZMoveItemToBody2]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderCZMOVEITEMTOBODY2)
	binary.LittleEndian.PutUint16(buf[2:], r.Index)
	binary.LittleEndian.PutUint32(buf[4:], r.Amount)
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write CZ_MOVE_ITEM_TO_BODY2: %w", err)
	}
	return nil
}

// EncodeCZCloseStore writes the CZ_CLOSE_STORE packet to w (a bare 2-byte cmd;
// used by the e2e harness). There is no body to parse — the handler validates
// the cmd only.
func EncodeCZCloseStore(w io.Writer) error {
	var buf [2]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderCZCLOSESTORE)
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write CZ_CLOSE_STORE: %w", err)
	}
	return nil
}

// StorageItemListResult encodes ZC_STOREITEMLISTRESULT (0x07eb, 6 bytes) —
// the server→client ack for a deposit / withdraw. result is a rAthena
// storage_result enum (clif.cpp:8021-8032): 0 = success, 1 = failure.
//
// Wire layout (rathenaThailand/src/map/packets_struct.hpp packets.hpp storage
// ack shape):
//
//	int16 packetType   (0x07eb)
//	int16 packetLength (always 6)
//	int16 result       (0 = success, 1 = failure)
type StorageItemListResult struct {
	Result uint16
}

// sizeZCStoreItemListResult is the fixed on-wire byte count for the
// PACKETVER_MAIN_NUM>=20100427 form (clif.cpp:8021 emits sizeof(...)=6).
const sizeZCStoreItemListResult = 6

// Size returns the on-wire byte length Encode will write (always 6).
func (StorageItemListResult) Size() int { return sizeZCStoreItemListResult }

// Encode writes the ZC_STOREITEMLISTRESULT packet to w.
func (r StorageItemListResult) Encode(w io.Writer) error {
	var buf [sizeZCStoreItemListResult]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCSTOREITEMLISTRESULT)
	binary.LittleEndian.PutUint16(buf[2:], sizeZCStoreItemListResult)
	binary.LittleEndian.PutUint16(buf[4:], r.Result)
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_STOREITEMLISTRESULT: %w", err)
	}
	return nil
}

// StorageListNormalResponse encodes a ZC_STORE_NORMALITEMLIST packet (command
// 0x07e9, variable length, PACKETVER 20250604). The server sends this on
// CZ_REQ_OPENSTORE2 to populate the warehouse grid with stackable items.
//
// Wire layout (rathenaThailand/src/map/clif.cpp:7801-7820 clif_storageList +
// packets_struct.hpp:418-448 NORMALITEM_INFO):
//
//	int16  packetType   (0x07e9)
//	int16  packetLength (4 + 26 * len(Items))
//	[per item, 26 bytes:] InventoryNormalItem
//
// The header is 4 bytes (no invType byte — the warehouse uses the bare
// NORMALITEM_INFO list, unlike the inventory init burst which carries a
// trailing invType byte at PACKETVER >= 20181002).
type StorageListNormalResponse struct {
	Items []InventoryNormalItem
}

// Encode writes the ZC_STORE_NORMALITEMLIST packet to w.
func (r StorageListNormalResponse) Encode(w io.Writer) error {
	total := sizeStorageListHeader + len(r.Items)*sizeNormalItem
	if total > 0xffff {
		return fmt.Errorf("packet: write ZC_STORE_NORMALITEMLIST: too many items (%d)", len(r.Items))
	}
	buf := make([]byte, total)
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCSTORENORMALITEMLIST)
	binary.LittleEndian.PutUint16(buf[2:], uint16(total))
	for i, it := range r.Items {
		writeNormalItem(buf, sizeStorageListHeader+i*sizeNormalItem, it)
	}
	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write ZC_STORE_NORMALITEMLIST: %w", err)
	}
	return nil
}

// StorageListEquipResponse encodes a ZC_STORE_EQUIPMENTITEMLIST packet
// (command 0x07ea, variable length, PACKETVER 20250604). The server sends
// this on CZ_REQ_OPENSTORE2 to populate the warehouse grid with equipped
// items.
//
// Wire layout (rathenaThailand/src/map/clif.cpp:7822-7848 clif_storageList
// + packets_struct.hpp:457-507 EQUIPITEM_INFO):
//
//	int16  packetType   (0x07ea)
//	int16  packetLength (4 + 57 * len(Items))
//	[per item, 57 bytes:] InventoryEquipItem
type StorageListEquipResponse struct {
	Items []InventoryEquipItem
}

// Encode writes the ZC_STORE_EQUIPMENTITEMLIST packet to w.
func (r StorageListEquipResponse) Encode(w io.Writer) error {
	total := sizeStorageListHeader + len(r.Items)*sizeEquipItem
	if total > 0xffff {
		return fmt.Errorf("packet: write ZC_STORE_EQUIPMENTITEMLIST: too many items (%d)", len(r.Items))
	}
	buf := make([]byte, total)
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCSTOREEQUIPMENTITEMLIST)
	binary.LittleEndian.PutUint16(buf[2:], uint16(total))
	for i, it := range r.Items {
		writeEquipItem(buf, sizeStorageListHeader+i*sizeEquipItem, it)
	}
	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write ZC_STORE_EQUIPMENTITEMLIST: %w", err)
	}
	return nil
}
