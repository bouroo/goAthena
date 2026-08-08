package itemdb

import "testing"

// TestWeaponClass pins the item_db SubType → rAthena weapon_type (W_*) mapping
// used for the LOOK_WEAPON view sprite. Values are the enum in pc.hpp:966; the
// strings are the camelCase W_ suffixes item_db_equip.yml carries.
func TestWeaponClass(t *testing.T) {
	t.Parallel()
	cases := []struct {
		subtype string
		want    uint16
	}{
		{"Dagger", 1},
		{"1hSword", 2},
		{"2hSword", 3},
		{"1hSpear", 4},
		{"2hSpear", 5},
		{"1hAxe", 6},
		{"2hAxe", 7},
		{"Mace", 8},
		{"Staff", 10},
		{"Bow", 11},
		{"Knuckle", 12},
		{"Musical", 13},
		{"Whip", 14},
		{"Book", 15},
		{"Katar", 16},
		{"Huuma", 22},
		{"2hStaff", 23},
		// case-insensitive (data may carry mixed casing).
		{"dagger", 1},
		{"2HSWORD", 3},
		{"1hsword", 2},
		{"2hstaff", 23},
	}
	for _, c := range cases {
		got, ok := WeaponClass(c.subtype)
		if !ok {
			t.Errorf("WeaponClass(%q) = (_, false), want (%d, true)", c.subtype, c.want)
			continue
		}
		if got != c.want {
			t.Errorf("WeaponClass(%q) = %d, want %d", c.subtype, got, c.want)
		}
	}
	// Non-weapon / blank / unrecognized subtypes return (0, false) so callers
	// leave the look field unchanged rather than rendering bare hands (W_FIST).
	for _, s := range []string{"", "Arrow", "Bullet", "Unknown"} {
		if _, ok := WeaponClass(s); ok {
			t.Errorf("WeaponClass(%q) = (_, true), want (_, false)", s)
		}
	}
}
