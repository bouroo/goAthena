package packet

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Floor-item packet opcodes (server→client). Verified against
// third_party/rathena @ 0c3ca757 — packets.hpp DEFINE_PACKET_HEADER lines
// cited per const. PACKETVER 20250604 (MAIN) opens every gate below.
const (
	HeaderZCItemFallEntry uint16 = 0x0ADD // packets.hpp ZC_ITEM_FALL_ENTRY (clif_packetdb.hpp:1921)
	HeaderZCItemEntry     uint16 = 0x009d // packets.hpp:2211 (clif_addflooritem — pickable spawn)
	HeaderZCItemDisappear uint16 = 0x00a1 // packets.hpp:615  (clif_clearflooritem — vanish)
	HeaderZCItemThrowAck  uint16 = 0x00af // packets.hpp:823  (drop ack, SELF)
	HeaderZCItemPickupAck uint16 = 0x0b41 // >=20200916 band wins at 20250604 (packets_struct.hpp:582)
)

// On-wire byte sizes (all gates open at PACKETVER 20250604).
const (
	sizeZCItemFallEntry = 24 // see ItemFallEntryResponse doc
	sizeZCItemEntry     = 19 // 2+4+4+1+2+2+2+1+1
	sizeZCItemDisappear = 6  // 2+4
	sizeZCItemThrowAck  = 6  // 2+2+2
	sizeZCItemPickupAck = 70 // see ItemPickupAckResponse doc
)

// ItemFallEntryResponse encodes ZC_ITEM_FALL_ENTRY (0x0ADD, v5) — the only packet
// the rathenaThailand fork sends when a new item lands on the ground. The drop
// path is map_addflooritem (map.cpp) → clif_dropflooritem (clif.cpp:865), which
// emits 0x0ADD alone. ZC_ITEM_ENTRY (0x009d) is NOT on the drop path: it is sent
// by clif_getareachar_item only when a player's AOI reveal sweeps over an
// already-present floor item (map-enter / walk-in). Sending 0x009d on a fresh
// drop would be a vanilla-rAthena behavior the fork dropped.
//
// Wire layout at PACKETVER 20250604 (struct packet_dropflooritem, all
// gates open). NOTE: the legacy clif.cpp:864 comment "<name id>.W" is
// stale — ITID is uint32 at >=20181121, so nameid is 4 bytes:
//
//	int16  packetType     (2)   offset 0
//	uint32 ID             (4)   offset 2
//	uint32 nameID         (4)   offset 6   [>=20181121]
//	uint16 type           (2)   offset 10  [>=20130000]
//	uint8  identified     (1)   offset 12
//	uint16 x              (2)   offset 13
//	uint16 y              (2)   offset 15
//	uint8  subX           (1)   offset 17
//	uint8  subY           (1)   offset 18
//	uint16 amount         (2)   offset 19
//	uint8  showDropEffect (1)   offset 21  [>=20180418]
//	uint16 dropEffectMode (2)   offset 22  [>=20180418]
//	                           ----
//	                           24
type ItemFallEntryResponse struct {
	ID             uint32 // ground item object ID
	NameID         uint32 // item DB id (sprite) — 4 bytes at >=20181121
	Type           uint16 // IT_* enum
	Identified     uint8
	X              uint16
	Y              uint16
	SubX           uint8
	SubY           uint8
	Amount         uint16
	ShowDropEffect uint8
	DropEffectMode uint16
}

// Size returns the fixed on-wire byte length of this packet.
func (r *ItemFallEntryResponse) Size() int { return sizeZCItemFallEntry }

// Encode writes ZC_ITEM_FALL_ENTRY to w. The trailing bytes of the 24-byte
// frame are explicitly zeroed via the zero-value array so reused structs
// cannot leak prior stack contents.
func (r *ItemFallEntryResponse) Encode(w io.Writer) error {
	var buf [sizeZCItemFallEntry]byte
	binary.LittleEndian.PutUint16(buf[0:2], HeaderZCItemFallEntry)
	binary.LittleEndian.PutUint32(buf[2:6], r.ID)
	binary.LittleEndian.PutUint32(buf[6:10], r.NameID)
	binary.LittleEndian.PutUint16(buf[10:12], r.Type)
	buf[12] = r.Identified
	binary.LittleEndian.PutUint16(buf[13:15], r.X)
	binary.LittleEndian.PutUint16(buf[15:17], r.Y)
	buf[17] = r.SubX
	buf[18] = r.SubY
	binary.LittleEndian.PutUint16(buf[19:21], r.Amount)
	buf[21] = r.ShowDropEffect
	binary.LittleEndian.PutUint16(buf[22:24], r.DropEffectMode)
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_ITEM_FALL_ENTRY: %w", err)
	}
	return nil
}

// ItemEntryResponse encodes ZC_ITEM_ENTRY (0x009d) — the packet that makes
// a floor item *pickable* (rAthena clif_addflooritem). Without it the
// client renders the drop animation but a CZ_ACTION_REQUEST pickup against
// the tile does nothing. The struct has no type field (PACKET_ZC_ITEM_ENTRY).
//
//	int16 packetType (2) offset 0
//	uint32 AID        (4) offset 2   floor-item object ID
//	uint32 nameID     (4) offset 6   [>=20181121]
//	uint8  identified (1) offset 10
//	uint16 x          (2) offset 11
//	uint16 y          (2) offset 13
//	uint16 amount     (2) offset 15
//	uint8  subX       (1) offset 17
//	uint8  subY       (1) offset 18
//	                    ----
//	                    19
type ItemEntryResponse struct {
	AID        uint32 // floor-item object ID
	NameID     uint32 // item DB id
	Identified uint8
	X          uint16
	Y          uint16
	Amount     uint16
	SubX       uint8
	SubY       uint8
}

// Size returns the fixed on-wire byte length of this packet.
func (r *ItemEntryResponse) Size() int { return sizeZCItemEntry }

// Encode writes ZC_ITEM_ENTRY to w.
func (r *ItemEntryResponse) Encode(w io.Writer) error {
	var buf [sizeZCItemEntry]byte
	binary.LittleEndian.PutUint16(buf[0:2], HeaderZCItemEntry)
	binary.LittleEndian.PutUint32(buf[2:6], r.AID)
	binary.LittleEndian.PutUint32(buf[6:10], r.NameID)
	buf[10] = r.Identified
	binary.LittleEndian.PutUint16(buf[11:13], r.X)
	binary.LittleEndian.PutUint16(buf[13:15], r.Y)
	binary.LittleEndian.PutUint16(buf[15:17], r.Amount)
	buf[17] = r.SubX
	buf[18] = r.SubY
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_ITEM_ENTRY: %w", err)
	}
	return nil
}

// ItemDisappearResponse encodes ZC_ITEM_DISAPPEAR (0x00a1) — broadcast to
// AREA viewers when a floor item is removed (picked up or expired). The
// picker receives ZC_ITEM_PICKUP_ACK separately. Layout: int16 + uint32.
type ItemDisappearResponse struct {
	AID uint32 // floor-item object ID
}

// Size returns the fixed on-wire byte length of this packet.
func (r *ItemDisappearResponse) Size() int { return sizeZCItemDisappear }

// Encode writes ZC_ITEM_DISAPPEAR to w.
func (r *ItemDisappearResponse) Encode(w io.Writer) error {
	var buf [sizeZCItemDisappear]byte
	binary.LittleEndian.PutUint16(buf[0:2], HeaderZCItemDisappear)
	binary.LittleEndian.PutUint32(buf[2:6], r.AID)
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_ITEM_DISAPPEAR: %w", err)
	}
	return nil
}

// ItemThrowAckResponse encodes ZC_ITEM_THROW_ACK (0x00af) — the SELF ack
// of a successful client-initiated drop. Index = source inventory
// position, Count = dropped amount. Layout: int16 + uint16 + uint16.
type ItemThrowAckResponse struct {
	Index uint16
	Count uint16
}

// Size returns the fixed on-wire byte length of this packet.
func (r *ItemThrowAckResponse) Size() int { return sizeZCItemThrowAck }

// Encode writes ZC_ITEM_THROW_ACK to w.
func (r *ItemThrowAckResponse) Encode(w io.Writer) error {
	var buf [sizeZCItemThrowAck]byte
	binary.LittleEndian.PutUint16(buf[0:2], HeaderZCItemThrowAck)
	binary.LittleEndian.PutUint16(buf[2:4], r.Index)
	binary.LittleEndian.PutUint16(buf[4:6], r.Count)
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_ITEM_THROW_ACK: %w", err)
	}
	return nil
}

// ItemPickupAckResponse encodes ZC_ITEM_PICKUP_ACK (0x0b41) — the SELF ack
// of a successful pickup. The >=20200916 band wins at PACKETVER 20250604;
// every conditional gate below is open. Result: 0=fail, 1=success.
//
//	int16  PacketType      (2) offset 0
//	uint16 Index           (2) offset 2   destination inventory slot
//	uint16 count           (2) offset 4   amount added
//	uint32 nameid          (4) offset 6   [>=20181121]
//	uint8  IsIdentified    (1) offset 10
//	uint8  IsDamaged       (1) offset 11
//	EQUIPSLOTINFO slot     (16) offset 12 uint32 card[4] [>=20181121]
//	uint32 location        (4) offset 28  [>=20120925]
//	uint8  type            (1) offset 32
//	uint8  result          (1) offset 33
//	int32  HireExpireDate  (4) offset 34  [>=20061218]
//	uint16 bindOnEquipType (2) offset 38  [>=20071002]
//	ItemOptions option[5]  (25) offset 40 [>=20150226] 5×5
//	uint8  favorite        (1) offset 65  [>=20160921]
//	uint16 look            (2) offset 66  [>=20160921]
//	uint8  refiningLevel   (1) offset 68  [>=20200916, moved to tail]
//	uint8  grade           (1) offset 69  [>=20200916]
//	                           ----
//	                           70
type ItemPickupAckResponse struct {
	Index           uint16
	Count           uint16
	NameID          uint32
	IsIdentified    uint8
	IsDamaged       uint8
	Slot            [4]uint32
	Location        uint32
	Type            uint8
	Result          uint8
	HireExpireDate  int32
	BindOnEquipType uint16
	Option          [5]ItemOption
	Favorite        uint8
	Look            uint16
	RefiningLevel   uint8
	Grade           uint8
}

// Size returns the fixed on-wire byte length of this packet.
func (r *ItemPickupAckResponse) Size() int { return sizeZCItemPickupAck }

// Encode writes ZC_ITEM_PICKUP_ACK to w. The option/slot/look/etc. fields
// are written verbatim; A4 leaves cards/options zero, so only NameID/Type/
// Count/Index/Result carry real data.
func (r *ItemPickupAckResponse) Encode(w io.Writer) error {
	var buf [sizeZCItemPickupAck]byte
	binary.LittleEndian.PutUint16(buf[0:2], HeaderZCItemPickupAck)
	binary.LittleEndian.PutUint16(buf[2:4], r.Index)
	binary.LittleEndian.PutUint16(buf[4:6], r.Count)
	binary.LittleEndian.PutUint32(buf[6:10], r.NameID)
	buf[10] = r.IsIdentified
	buf[11] = r.IsDamaged
	binary.LittleEndian.PutUint32(buf[12:16], r.Slot[0])
	binary.LittleEndian.PutUint32(buf[16:20], r.Slot[1])
	binary.LittleEndian.PutUint32(buf[20:24], r.Slot[2])
	binary.LittleEndian.PutUint32(buf[24:28], r.Slot[3])
	binary.LittleEndian.PutUint32(buf[28:32], r.Location)
	buf[32] = r.Type
	buf[33] = r.Result
	// HireExpireDate is a signed epoch (negative = no expiry); written
	// verbatim as 4 wire bytes, so the int32→uint32 cast is a bit-cast.
	binary.LittleEndian.PutUint32(buf[34:38], uint32(r.HireExpireDate)) //nolint:gosec // bit-cast of a raw int32 epoch
	binary.LittleEndian.PutUint16(buf[38:40], r.BindOnEquipType)
	for i, opt := range r.Option {
		base := 40 + i*5
		binary.LittleEndian.PutUint16(buf[base:base+2], opt.Index)
		binary.LittleEndian.PutUint16(buf[base+2:base+4], opt.Value)
		buf[base+4] = opt.Param
	}
	buf[65] = r.Favorite
	binary.LittleEndian.PutUint16(buf[66:68], r.Look)
	buf[68] = r.RefiningLevel
	buf[69] = r.Grade
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_ITEM_PICKUP_ACK: %w", err)
	}
	return nil
}
