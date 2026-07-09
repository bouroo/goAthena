package packet

import (
	"encoding/binary"
	"fmt"
	"io"
)

// InventoryNormalItem is the per-entry shape rAthena writes into
// ZC_INVENTORY_ITEMLIST_NORMAL for PACKETVER 20250604. Source:
// rathena/src/map/packets_struct.hpp:418-448 (NORMALITEM_INFO).
//
// On-wire size: sizeNormalItem (26 bytes).
//
// The struct fields below are exactly the on-wire fields for this
// PACKETVER. Optional fields the gateway does not surface today
// (card slots, hire-expire, bind-on-equip, IsIdentified /
// PlaceETCTab flag) are kept as raw byte arrays at the trailing
// positions so the wire layout matches rAthena's emission exactly
// and the client's item UI parses every item correctly even when
// those fields are zero.
type InventoryNormalItem struct {
	// Index is the inventory grid index (rAthena's NORMALITEM_INFO.index).
	Index uint16
	// ITID is the item DB nameid (rAthena's ITID). For PACKETVER 20250604
	// this is a uint16 on the wire; the proto InventoryItem.nameid
	// is uint32 and gets truncated to uint16 at encode time.
	ITID uint16
	// Type is the rAthena IT_* type byte (0=healing, 2=etc, 3=weapon,
	// 4=armor, 5=card, 6=pet egg/ammunition).
	Type uint8
	// Count is the stack count.
	Count uint16
	// WearState is the bitfield rAthena uses to flag broken / over-encumbered
	// items. The gateway always writes 0 — break / repair is deferred.
	WearState uint32
	// Card is the 4-card slot. uint16[4] = 8 bytes; PACKETVER 20250604
	// uses uint16 cards (rathena/src/map/packets_struct.hpp:410-416).
	// The dispatcher writes them verbatim; cards are a future feature
	// and zero today.
	Card [4]uint16
	// HireExpireDate is the rental expiry timestamp. Always 0 today
	// — the rental system is a future feature.
	HireExpireDate uint32
	// BindOnEquipType is the bind flag rAthena's item_db bindOnEquipType
	// column writes. Always 0 today.
	BindOnEquipType uint16
	// Flag packs the IsIdentified / PlaceETCTab bit fields. The
	// dispatcher writes them verbatim. Bit layout per rathena/src/map/
	// packets_struct.hpp:441-447 (NORMALITEM_INFO post-20120925):
	// bit 0 = IsIdentified, bit 1 = PlaceETCTab, bits 2-7 = SpareBits.
	Flag uint8
}

// InventoryEquipItem is the per-entry shape rAthena writes into
// ZC_INVENTORY_ITEMLIST_EQUIP for PACKETVER 20250604. Source:
// rathena/src/map/packets_struct.hpp:457-507 (EQUIPITEM_INFO).
//
// On-wire size: sizeEquipItem (57 bytes).
type InventoryEquipItem struct {
	// Index is the inventory grid index.
	Index uint16
	// ITID is the item DB nameid. uint16 on the wire for PACKETVER 20250604.
	ITID uint16
	// Type is the rAthena IT_* type byte.
	Type uint8
	// Location is the EQP_* bitmask rAthena writes from inventory.equip.
	Location uint32
	// WearState is the same bitfield as InventoryNormalItem.WearState.
	WearState uint32
	// RefiningLevel is +0..+10 (rAthena's inventory.refine).
	RefiningLevel uint8
	// Card is the 4-card slot (uint16[4] for PACKETVER 20250604).
	Card [4]uint16
	// HireExpireDate is the rental expiry timestamp. Always 0 today.
	HireExpireDate uint32
	// BindOnEquipType is the bind flag.
	BindOnEquipType uint16
	// ItemSpriteNumber is the rAthena client view sprite (item_db.view).
	ItemSpriteNumber uint16
	// OptionCount is the number of valid OptionData entries (0..5).
	OptionCount uint8
	// OptionData is up to 5 ItemOptions slots (int16 index + int16 value
	// + uint8 param = 5 bytes each). All five slots are written on
	// the wire regardless of OptionCount; clients only read the
	// first OptionCount entries.
	OptionData [5]ItemOption
	// Flag packs the IsIdentified / IsDamaged / PlaceETCTab bit fields
	// (rathena/src/map/packets_struct.hpp:500-506, EQUIPITEM_INFO
	// post-20120925): bit 0 = IsIdentified, bit 1 = IsDamaged,
	// bit 2 = PlaceETCTab, bits 3-7 = SpareBits.
	Flag uint8
}

// ItemOption is a single random / crafted option entry attached to
// an equipped item. Source: rathena/src/map/packets_struct.hpp:450-454.
type ItemOption struct {
	// Index is the option ID (e.g. rAthena's optionIndex enum).
	Index uint16
	// Value is the option's magnitude (e.g. +5 STR → Value=5).
	Value uint16
	// Param is the option's level (e.g. +5 STR level 1 → Param=1).
	Param uint8
}

// writeNormalItem serializes one InventoryNormalItem into the 26-byte
// rAthena NORMALITEM_INFO layout for PACKETVER 20250604. The caller
// is responsible for slicing the destination buffer so the offset
// has enough room (sizeNormalItem bytes).
func writeNormalItem(buf []byte, off int, it InventoryNormalItem) {
	binary.LittleEndian.PutUint16(buf[off:], it.Index)
	binary.LittleEndian.PutUint16(buf[off+2:], it.ITID)
	buf[off+4] = it.Type
	binary.LittleEndian.PutUint16(buf[off+5:], it.Count)
	binary.LittleEndian.PutUint32(buf[off+7:], it.WearState)
	for i, c := range it.Card {
		binary.LittleEndian.PutUint16(buf[off+11+i*2:], c)
	}
	binary.LittleEndian.PutUint32(buf[off+19:], it.HireExpireDate)
	binary.LittleEndian.PutUint16(buf[off+23:], it.BindOnEquipType)
	buf[off+25] = it.Flag
}

// writeEquipItem serializes one InventoryEquipItem into the 57-byte
// rAthena EQUIPITEM_INFO layout for PACKETVER 20250604. The caller
// is responsible for slicing the destination buffer so the offset
// has enough room (sizeEquipItem bytes).
func writeEquipItem(buf []byte, off int, it InventoryEquipItem) {
	binary.LittleEndian.PutUint16(buf[off:], it.Index)
	binary.LittleEndian.PutUint16(buf[off+2:], it.ITID)
	buf[off+4] = it.Type
	binary.LittleEndian.PutUint32(buf[off+5:], it.Location)
	binary.LittleEndian.PutUint32(buf[off+9:], it.WearState)
	buf[off+13] = it.RefiningLevel
	for i, c := range it.Card {
		binary.LittleEndian.PutUint16(buf[off+14+i*2:], c)
	}
	binary.LittleEndian.PutUint32(buf[off+22:], it.HireExpireDate)
	binary.LittleEndian.PutUint16(buf[off+26:], it.BindOnEquipType)
	binary.LittleEndian.PutUint16(buf[off+28:], it.ItemSpriteNumber)
	buf[off+30] = it.OptionCount
	for i, opt := range it.OptionData {
		binary.LittleEndian.PutUint16(buf[off+31+i*5:], opt.Index)
		binary.LittleEndian.PutUint16(buf[off+33+i*5:], opt.Value)
		buf[off+35+i*5] = opt.Param
	}
	buf[off+56] = it.Flag
}

// InventoryListNormalResponse encodes a ZC_INVENTORY_ITEMLIST_NORMAL
// packet (command 0x00a3, variable length, PACKETVER 20250604). The
// server sends this on CZ_NOTIFY_ACTORINIT (LoadEndAck) so the
// client initialises the inventory grid.
//
// Wire layout (rathena/src/map/packets_struct.hpp:1187-1194 +
// NORMALITEM_INFO:418-448):
//
//	int16  packetType   (0x00a3)
//	int16  packetLength (4 + 26 * len(Items))
//	[per item, 26 bytes:] InventoryNormalItem
type InventoryListNormalResponse struct {
	Items []InventoryNormalItem
}

// Encode writes the ZC_INVENTORY_ITEMLIST_NORMAL packet to w. The
// wire length is 4 + 26 * len(Items); the encoder computes
// packetLength from the entry count so the caller cannot
// accidentally emit a frame whose length slot disagrees with the
// trailing bytes.
func (r InventoryListNormalResponse) Encode(w io.Writer) error {
	total := 4 + len(r.Items)*sizeNormalItem
	if total > 0xffff {
		return fmt.Errorf("packet: write ZC_INVENTORY_ITEMLIST_NORMAL: too many items (%d)", len(r.Items))
	}
	buf := make([]byte, total)
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCINVENTORYITEMLISTNORMAL)
	binary.LittleEndian.PutUint16(buf[2:], uint16(total))
	for i, it := range r.Items {
		writeNormalItem(buf, 4+i*sizeNormalItem, it)
	}
	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write ZC_INVENTORY_ITEMLIST_NORMAL: %w", err)
	}
	return nil
}

// InventoryListEquipResponse encodes a ZC_INVENTORY_ITEMLIST_EQUIP
// packet (command 0x00a4, variable length, PACKETVER 20250604). The
// server sends this on CZ_NOTIFY_ACTORINIT (LoadEndAck) so the
// client initialises the equip view.
//
// Wire layout (rathena/src/map/packets_struct.hpp:1196-1203 +
// EQUIPITEM_INFO:457-507):
//
//	int16  packetType   (0x00a4)
//	int16  packetLength (4 + 57 * len(Items))
//	[per item, 57 bytes:] InventoryEquipItem
type InventoryListEquipResponse struct {
	Items []InventoryEquipItem
}

// Encode writes the ZC_INVENTORY_ITEMLIST_EQUIP packet to w. The
// wire length is 4 + 57 * len(Items); the encoder computes
// packetLength from the entry count.
func (r InventoryListEquipResponse) Encode(w io.Writer) error {
	total := 4 + len(r.Items)*sizeEquipItem
	if total > 0xffff {
		return fmt.Errorf("packet: write ZC_INVENTORY_ITEMLIST_EQUIP: too many items (%d)", len(r.Items))
	}
	buf := make([]byte, total)
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCINVENTORYITEMLISTEQUIP)
	binary.LittleEndian.PutUint16(buf[2:], uint16(total))
	for i, it := range r.Items {
		writeEquipItem(buf, 4+i*sizeEquipItem, it)
	}
	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write ZC_INVENTORY_ITEMLIST_EQUIP: %w", err)
	}
	return nil
}

// ReqWearEquipAckResponse encodes a ZC_REQ_WEAR_EQUIP_ACK_V5 packet
// (command 0x0999, fixed 11 bytes, PACKETVER 20250604). The server
// sends this in response to CZ_REQ_WEAR_EQUIP_V5 to tell the client
// the equip succeeded, failed, or failed for a low-level reason.
//
// Wire layout (rathena/src/map/packets_struct.hpp:1269-1276 +
// clif.cpp:4301-4325):
//
//	int16  PacketType       (0x0999)
//	uint16 index            (inventory index)
//	uint32 wearLocation     (EQP_* bitmask)
//	uint16 wItemSpriteNumber (rAthena emits the view sprite when
//	                          equip succeeded and the item is
//	                          visible; 0 otherwise. clif.cpp:4316-4322)
//	uint8  result           (0=fail, 1=ok, 2=low-level fail)
type ReqWearEquipAckResponse struct {
	// Index is the inventory grid index the client tried to equip.
	Index uint16
	// WearLocation is the EQP_* bitmask the slot ended up in.
	WearLocation uint32
	// ItemSpriteNumber is the rAthena client view sprite (item_db.view)
	// rAthena writes when the equip succeeded and the item is
	// visible; 0 for invisible slots or failed equips.
	ItemSpriteNumber uint16
	// Result is the equip outcome byte. rAthena values:
	// 0 = failure, 1 = success, 2 = failure due to low level
	// (clif.cpp:4306-4309).
	Result uint8
}

// Size returns the on-wire byte length that Encode will write
// (always 11).
func (r ReqWearEquipAckResponse) Size() int { return sizeZCReqWearEquipAckV5 }

// Encode writes the ZC_REQ_WEAR_EQUIP_ACK_V5 packet to w.
func (r ReqWearEquipAckResponse) Encode(w io.Writer) error {
	var buf [sizeZCReqWearEquipAckV5]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCREQWEAREQUIPACKV5)
	binary.LittleEndian.PutUint16(buf[2:], r.Index)
	binary.LittleEndian.PutUint32(buf[4:], r.WearLocation)
	binary.LittleEndian.PutUint16(buf[8:], r.ItemSpriteNumber)
	buf[10] = r.Result
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_REQ_WEAR_EQUIP_ACK_V5: %w", err)
	}
	return nil
}

// ReqTakeoffEquipAckResponse encodes a ZC_REQ_TAKEOFF_EQUIP_ACK
// packet (command 0x99a, fixed 9 bytes, PACKETVER 20250604). The
// server sends this in response to CZ_REQ_TAKEOFF_EQUIP.
//
// Wire layout (rathena/src/map/packets.hpp:1007-1013 +
// clif.cpp:4329-4349):
//
//	int16  PacketType   (0x99a)
//	uint16 index        (inventory index)
//	uint32 wearLocation (EQP_* bitmask the slot was in before takeoff)
//	uint8  flag         (inverted for PACKETVER >= 20110824 —
//	                     clif.cpp:4338 — so 0 = success, 1 = failure
//	                     on the wire)
type ReqTakeoffEquipAckResponse struct {
	// Index is the inventory grid index the client tried to unequip.
	Index uint16
	// WearLocation is the EQP_* bitmask the slot was in.
	WearLocation uint32
	// Flag is the unequip outcome byte, already wire-inverted
	// (0 = success, 1 = failure for PACKETVER >= 20110824). The
	// dispatcher is responsible for applying the inversion when
	// mapping from the identity service's success bool — the
	// encoder does not invert again.
	Flag uint8
}

// Size returns the on-wire byte length that Encode will write
// (always 9).
func (r ReqTakeoffEquipAckResponse) Size() int { return sizeZCReqTakeoffEquipAck }

// Encode writes the ZC_REQ_TAKEOFF_EQUIP_ACK packet to w.
func (r ReqTakeoffEquipAckResponse) Encode(w io.Writer) error {
	var buf [sizeZCReqTakeoffEquipAck]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCREQTAKEOFFEQUIPACK)
	binary.LittleEndian.PutUint16(buf[2:], r.Index)
	binary.LittleEndian.PutUint32(buf[4:], r.WearLocation)
	buf[8] = r.Flag
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_REQ_TAKEOFF_EQUIP_ACK: %w", err)
	}
	return nil
}

// UseItemAck2Response encodes a ZC_USE_ITEM_ACK2 packet (command
// 0x01c8, fixed 13 bytes, PACKETVER 20250604). The server sends
// this in response to CZ_USE_ITEM2 to tell the client the use
// succeeded (and the new stack count) or failed.
//
// Wire layout (rathena/src/map/packets_struct.hpp:2577-2589 +
// clif.cpp:4478-4494):
//
//	int16 PacketType (0x01c8)
//	int16 index      (client-side index, +2 from the server row
//	                  per clif.cpp:4482)
//	uint16 itemId    (PACKETVER 20250604 branch; uint32 branch is
//	                  reserved for PACKETVER >= 20181121)
//	uint32 AID       (the player's AID)
//	int16 amount     (remaining stack count on success; ignored on
//	                  failure — clif_send is conditional)
//	uint8  result    (0=failure, 1=success)
type UseItemAck2Response struct {
	// Index is the inventory index the client sees (server index + 2
	// for PACKETVER 20250604, per clif.cpp:4482). The dispatcher
	// does the +2 translation; the encoder writes the value verbatim.
	Index uint16
	// ItemID is the item DB nameid of the item that was used.
	ItemID uint16
	// AID is the player's account id (rAthena writes sd->id).
	AID uint32
	// Amount is the remaining stack count after the use; 0 if the
	// stack was deleted.
	Amount uint16
	// Result is the use-item outcome byte (0=failure, 1=success).
	Result uint8
}

// Size returns the on-wire byte length that Encode will write
// (always 13).
func (r UseItemAck2Response) Size() int { return sizeZCUseItemAck2 }

// Encode writes the ZC_USE_ITEM_ACK2 packet to w.
func (r UseItemAck2Response) Encode(w io.Writer) error {
	var buf [sizeZCUseItemAck2]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCUSEITEMACK2)
	binary.LittleEndian.PutUint16(buf[2:], r.Index)
	binary.LittleEndian.PutUint16(buf[4:], r.ItemID)
	binary.LittleEndian.PutUint32(buf[6:], r.AID)
	binary.LittleEndian.PutUint16(buf[10:], r.Amount)
	buf[12] = r.Result
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_USE_ITEM_ACK2: %w", err)
	}
	return nil
}
