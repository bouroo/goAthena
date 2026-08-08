// Package equip defines the rAthena equip-slot bitmask constants (EQP_*) and the
// translation between an item_db_equip.yml Locations map and the wire bitmask the
// inventory.equip column stores.
//
// Bit values are authoritative against rathenaThailand/src/common/mmo.hpp
// `enum equip_pos : uint32`. The Locations key→bit map mirrors the loader in
// rathenaThailand/src/map/itemdb.cpp:446, which resolves "EQP_"+<Locations key>
// (case-insensitively via script_get_constant) and ORs the result into the item's
// equip bitmask.
package equip

import "strings"

// Equipment-location bitmask constants. Values mirror
// rathenaThailand/src/common/mmo.hpp `enum equip_pos : uint32`.
const (
	HeadLow        uint32 = 0x000001
	HandRight      uint32 = 0x000002
	Garment        uint32 = 0x000004
	AccessoryRight uint32 = 0x000008
	Armor          uint32 = 0x000010
	HandLeft       uint32 = 0x000020
	Shoes          uint32 = 0x000040
	AccessoryLeft  uint32 = 0x000080
	HeadTop        uint32 = 0x000100
	HeadMid        uint32 = 0x000200

	CostumeHeadTop uint32 = 0x000400
	CostumeHeadMid uint32 = 0x000800
	CostumeHeadLow uint32 = 0x001000
	CostumeGarment uint32 = 0x002000
	Ammo           uint32 = 0x008000

	ShadowArmor    uint32 = 0x010000
	ShadowWeapon   uint32 = 0x020000
	ShadowShield   uint32 = 0x040000
	ShadowShoes    uint32 = 0x080000
	ShadowAccRight uint32 = 0x100000
	ShadowAccLeft  uint32 = 0x200000

	// Composites mirroring the EQP_* macros in rathenaThailand/src/map/pc.hpp.
	Arms        = HandRight | HandLeft           // EQP_ARMS (34): both hands (two-handed weapon)
	Helm        = HeadLow | HeadMid | HeadTop    // EQP_HELM (769)
	Accessories = AccessoryRight | AccessoryLeft // EQP_ACC (136)
)

// locationBits maps a lowercased item_db_equip.yml Locations key to its EQP_* bit.
// The keys are the names rathenaThailand's loader accepts (itemdb.cpp:446 resolves
// "EQP_"+key, including the Right_/Left_/Both_ aliases registered in
// script_constants.hpp:926-933).
var locationBits = map[string]uint32{
	"head_top":               HeadTop,
	"head_mid":               HeadMid,
	"head_low":               HeadLow,
	"armor":                  Armor,
	"right_hand":             HandRight,
	"left_hand":              HandLeft,
	"both_hand":              Arms,
	"garment":                Garment,
	"shoes":                  Shoes,
	"right_accessory":        AccessoryRight,
	"left_accessory":         AccessoryLeft,
	"both_accessory":         Accessories,
	"costume_head_top":       CostumeHeadTop,
	"costume_head_mid":       CostumeHeadMid,
	"costume_head_low":       CostumeHeadLow,
	"costume_garment":        CostumeGarment,
	"ammo":                   Ammo,
	"shadow_armor":           ShadowArmor,
	"shadow_weapon":          ShadowWeapon,
	"shadow_shield":          ShadowShield,
	"shadow_shoes":           ShadowShoes,
	"shadow_right_accessory": ShadowAccRight,
	"shadow_left_accessory":  ShadowAccLeft,
}

// LocationBits folds an item_db_equip.yml Locations map into the EQP_* bitmask the
// inventory.equip column stores, ORing the bit for every key set true. The lookup
// is case-insensitive to match script_get_constant. Unknown keys are dropped:
// rAthena's loader warns and demotes the item to IT_ETC, but this package only
// stores the bit, so dropping is the safe no-op (the item's Type is the caller's
// concern). A nil map yields 0.
func LocationBits(locations map[string]bool) uint32 {
	var bits uint32
	for name, active := range locations {
		if !active {
			continue
		}
		if bit, ok := locationBits[strings.ToLower(name)]; ok {
			bits |= bit
		}
	}
	return bits
}
