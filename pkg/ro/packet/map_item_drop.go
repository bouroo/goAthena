package packet

import (
	"encoding/binary"
	"fmt"
	"io"
)

// HeaderZCItemFallEntry is the map-server opcode for ZC_ITEM_FALL_ENTRY
// in the v5 layout that adds <showDropEffect> and <dropEffectMode>.
//
// rAthena binds 0x0ADD (size 22) for PACKETVER >= 20180418:
// rathena/src/map/clif_packetdb.hpp:1921 — packet(0x0ADD, 22).
// The v5 on-wire shape is documented at rathena/src/map/clif.cpp:864:
//
//	0ADD <id>.L <name id>.W <type>.W <identified>.B <x>.W <y>.W
//	     <subX>.B <subY>.B <amount>.W <show drop effect>.B
//	     <drop effect mode>.W (ZC_ITEM_FALL_ENTRY5)
//
// Earlier PACKETVERs use the v3 shape (0x009E, no <type>) or the v4
// shape (0x084B, no <showDropEffect>/<dropEffectMode>). PACKETVER
// 20250604 falls into the v5 window.
const HeaderZCItemFallEntry = 0x0ADD

// sizeZCItemFallEntry is the fixed on-wire byte length of ZC_ITEM_FALL_ENTRY
// for PACKETVER >= 20180418:
//
//	int16  packetType        (2)
//	uint32 ID                (4)
//	uint16 nameID            (2)
//	uint16 type              (2)
//	uint8  identified        (1)
//	uint16 x                 (2)
//	uint16 y                 (2)
//	uint8  subX              (1)
//	uint8  subY              (1)
//	uint16 amount            (2)
//	uint8  showDropEffect    (1)
//	uint16 dropEffectMode    (2)
//	                        ----
//	                        22
const sizeZCItemFallEntry = 22

// ItemFallEntryResponse encodes ZC_ITEM_FALL_ENTRY (0x0ADD, v5) — the
// server-to-client notification that an item has appeared on the ground
// at (X, Y). Sent after a monster kill drops an item (rathena clif.cpp
// clif_set_unit_idle → clif_additem, and the ground-spawn path in
// mob.cpp / battle.cpp).
//
// ShowDropEffect controls the visual cue the client plays:
//
//	0 — normal drop
//	1 — MVP prize (player shouts "MVP!")
//	2 — rare / quest special
//
// DropEffectMode is rAthena's "drop effect mode" hint (0 normally); its
// wire layout has been stable across the v5 era and is reserved for
// future client-driven effects.
type ItemFallEntryResponse struct {
	ID             uint32 // ground item object ID
	NameID         uint16 // item sprite / item ID
	Type           uint16 // IT_* enum (0=healing, 2=usable, 3=etc, 4=armor, 5=weapon, 6=card, …)
	Identified     uint8  // 1 if identified
	X              uint16
	Y              uint16
	SubX           uint8 // sub-cell x (0-11)
	SubY           uint8 // sub-cell y (0-11)
	Amount         uint16
	ShowDropEffect uint8  // 0=normal, 1=MVP, 2=special
	DropEffectMode uint16 // 0 normally
}

// Size returns the fixed on-wire byte length of this packet.
func (r *ItemFallEntryResponse) Size() int { return sizeZCItemFallEntry }

// Encode writes the ZC_ITEM_FALL_ENTRY packet to w. The wire layout
// follows rathena/src/map/clif.cpp:864 exactly; the two trailing bytes
// of the 22-byte frame are explicitly zeroed so callers cannot leak
// stack data into the buffer if the struct is reused.
func (r *ItemFallEntryResponse) Encode(w io.Writer) error {
	var buf [sizeZCItemFallEntry]byte
	binary.LittleEndian.PutUint16(buf[0:2], HeaderZCItemFallEntry)
	binary.LittleEndian.PutUint32(buf[2:6], r.ID)
	binary.LittleEndian.PutUint16(buf[6:8], r.NameID)
	binary.LittleEndian.PutUint16(buf[8:10], r.Type)
	buf[10] = r.Identified
	binary.LittleEndian.PutUint16(buf[11:13], r.X)
	binary.LittleEndian.PutUint16(buf[13:15], r.Y)
	buf[15] = r.SubX
	buf[16] = r.SubY
	binary.LittleEndian.PutUint16(buf[17:19], r.Amount)
	buf[19] = r.ShowDropEffect
	binary.LittleEndian.PutUint16(buf[20:22], r.DropEffectMode)
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_ITEM_FALL_ENTRY: %w", err)
	}
	return nil
}
