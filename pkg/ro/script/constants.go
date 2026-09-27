package script

import "strings"

// constants is the compile-time constant table the compiler resolves
// identifiers against, mirroring rAthena's script_set_constant /
// script_get_constant pair (script.cpp:2315): a name in this table compiles to
// an integer literal, not a variable read. rAthena seeds the same table from
// script_constants.hpp (export_constant) plus db/const.yml.
//
// Scope: the constants the implemented builtins take as arguments. It is not
// the full rAthena set (thousands of entries) — a script using an unlisted
// constant still compiles, it just reads an unset variable as 0 exactly as
// before. Extend the table when a builtin starts consuming a new constant; the
// upgrade path when breadth starts to hurt is generating it from those two
// upstream sources.
//
// Keys are lowercase: rAthena's string table compares with strcasecmp
// (script.cpp:700 add_str), so `bc_map` and `BC_MAP` are the same constant
// upstream.
var constants = map[string]int64{
	// Broadcast flags — clif.hpp:245 enum broadcast_flags. announce/mapannounce.
	"bc_all":         0,
	"bc_map":         1,
	"bc_area":        2,
	"bc_self":        3,
	"bc_pc":          0x00,
	"bc_npc":         0x08,
	"bc_yellow":      0x00,
	"bc_blue":        0x10,
	"bc_woe":         0x20,
	"bc_default":     0, // BC_ALL|BC_PC|BC_YELLOW
	"bc_target_mask": 0x07,
	"bc_source_mask": 0x08,
	"bc_color_mask":  0x30,

	// ITEMINFO_* — script.hpp:2240 enum iteminfo. getiteminfo.
	"iteminfo_buy":           0,
	"iteminfo_sell":          1,
	"iteminfo_type":          2,
	"iteminfo_maxchance":     3,
	"iteminfo_gender":        4,
	"iteminfo_locations":     5,
	"iteminfo_weight":        6,
	"iteminfo_attack":        7,
	"iteminfo_defense":       8,
	"iteminfo_range":         9,
	"iteminfo_slot":          10,
	"iteminfo_view":          11,
	"iteminfo_equiplevelmin": 12,
	"iteminfo_weaponlevel":   13,
	"iteminfo_aliasname":     14,
	"iteminfo_equiplevelmax": 15,
	"iteminfo_magicattack":   16,
	"iteminfo_id":            17,
	"iteminfo_aegisname":     18,
	"iteminfo_armorlevel":    19,
	"iteminfo_subtype":       20,

	// IT_* item types — common/mmo.hpp:223 enum item_types. Compared against
	// getiteminfo(<id>, ITEMINFO_TYPE).
	"it_healing":      0,
	"it_unknown":      1,
	"it_usable":       2,
	"it_etc":          3,
	"it_armor":        4,
	"it_weapon":       5,
	"it_card":         6,
	"it_petegg":       7,
	"it_petarmor":     8,
	"it_unknown2":     9,
	"it_ammo":         10,
	"it_delayconsume": 11,
	"it_shadowgear":   12,
	"it_charm":        13,
	"it_cash":         18,

	// Font weights — script.hpp:436 FW_*. announce's optional fontType.
	"fw_dontcare": 0,
	"fw_normal":   400,
}

// lookupConstant resolves an identifier against the constant table. Names match
// case-insensitively, matching rAthena's string table.
func lookupConstant(name string) (int64, bool) {
	v, ok := constants[strings.ToLower(name)]
	return v, ok
}

// isConstantName reports whether ident may name a constant. Scoped script
// variables (`.@x`, `@x`, `#x`, `$x`, `'x`) and string variables (`x$`) are
// never constants, so the check keeps a variable that happens to share a
// constant's spelling from being folded to a literal.
func isConstantName(ident string) bool {
	if ident == "" {
		return false
	}
	switch ident[0] {
	case '.', '@', '#', '$', '\'':
		return false
	}
	return true
}
