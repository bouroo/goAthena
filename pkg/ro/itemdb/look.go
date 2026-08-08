package itemdb

import "strings"

// weaponClassByName maps a lowercased weapon item_db SubType to the rAthena
// weapon_type enum value (W_* in rathenaThailand/src/map/pc.hpp:966). item_db.yml
// stores the W_ suffix in camelCase ("Dagger", "1hSword", "2hStaff", ...) — the
// same names rAthena registers as script constants (script_constants.hpp
// export_constant(W_*)); clif resolves them in itemdb.cpp:165. W_FIST(0)=bare
// hands has no SubType. W_2HMACE(9) is enum-defined but rAthena marks it unused,
// so no item carries it; it is included for enum fidelity.
var weaponClassByName = map[string]uint16{
	"dagger":   1,
	"1hsword":  2,
	"2hsword":  3,
	"1hspear":  4,
	"2hspear":  5,
	"1haxe":    6,
	"2haxe":    7,
	"mace":     8,
	"2hmace":   9,
	"staff":    10,
	"bow":      11,
	"knuckle":  12,
	"musical":  13,
	"whip":     14,
	"book":     15,
	"katar":    16,
	"revolver": 17,
	"rifle":    18,
	"gatling":  19,
	"shotgun":  20,
	"grenade":  21,
	"huuma":    22,
	"2hstaff":  23,
}

// WeaponClass maps a weapon item_db SubType string to the rAthena weapon_type
// value the LOOK_WEAPON view-sprite field carries in ZC_SPRITE_CHANGE. It returns
// (0, false) for a non-weapon or unrecognized subtype so the caller leaves the
// look field unchanged rather than rendering bare hands (W_FIST) for a real
// weapon. The lookup is case-insensitive.
func WeaponClass(subtype string) (uint16, bool) {
	c, ok := weaponClassByName[strings.ToLower(subtype)]
	return c, ok
}
