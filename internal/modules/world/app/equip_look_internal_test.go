//go:build unit

package app

import (
	"strings"
	"testing"

	invdomain "github.com/bouroo/goAthena/internal/modules/inventory/domain"
	"github.com/bouroo/goAthena/pkg/ro/equip"
	"github.com/bouroo/goAthena/pkg/ro/itemdb"
)

// lookItemDB builds a minimal item_db registry carrying a right-hand dagger
// (weapon class 1) and a left-hand shield (view sprite 7) — the two entries
// computeLook resolves. A two-handed sword entry is included to exercise the
// both-hands case (weapon=class, shield=0).
func lookItemDB(t *testing.T) *itemdb.Registry {
	t.Helper()
	const yaml = `Header:
  Type: ITEM_DB
  Version: 3
Body:
  - {Id: 1201, AegisName: Knife,   Name: Knife,   Type: Weapon, SubType: Dagger,  Locations: {Right_Hand: true}}
  - {Id: 2101, AegisName: Buckler, Name: Buckler, Type: Armor,  View: 7,          Locations: {Left_Hand: true}}
  - {Id: 1118, AegisName: Katana,  Name: Katana,  Type: Weapon, SubType: 2hSword, Locations: {Both_Hand: true}}
`
	reg, err := itemdb.Load(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("itemdb.Load: %v", err)
	}
	return reg
}

// TestComputeLook_WeaponAndShield resolves the right-hand dagger class (1) and
// the left-hand shield view (7) from a worn bag.
func TestComputeLook_WeaponAndShield(t *testing.T) {
	rows := []invdomain.InventoryItem{
		{NameID: 1201, Equip: equip.HandRight},
		{NameID: 2101, Equip: equip.HandLeft},
	}
	weapon, shield := computeLook(rows, lookItemDB(t))
	if weapon != 1 {
		t.Errorf("weapon = %d, want 1 (W_DAGGER)", weapon)
	}
	if shield != 7 {
		t.Errorf("shield = %d, want 7 (armor View)", shield)
	}
}

// TestComputeLook_TwoHanded verifies a both-hands weapon yields its weapon class
// and a zero shield (it occupies both hands; rAthena status.shield stays W_FIST).
func TestComputeLook_TwoHanded(t *testing.T) {
	rows := []invdomain.InventoryItem{
		{NameID: 1118, Equip: equip.Arms}, // Both_Hand → HandRight|HandLeft
	}
	weapon, shield := computeLook(rows, lookItemDB(t))
	if weapon != 3 {
		t.Errorf("weapon = %d, want 3 (W_2HSWORD)", weapon)
	}
	if shield != 0 {
		t.Errorf("shield = %d, want 0 (two-handed occupies left hand)", shield)
	}
}

// TestComputeLook_Empty confirms no/unequipped rows yield bare-hands (0,0).
func TestComputeLook_Empty(t *testing.T) {
	if weapon, shield := computeLook(nil, lookItemDB(t)); weapon != 0 || shield != 0 {
		t.Errorf("(nil rows) weapon=%d shield=%d, want 0,0", weapon, shield)
	}
	rows := []invdomain.InventoryItem{{NameID: 1201, Equip: 0}}
	if weapon, shield := computeLook(rows, lookItemDB(t)); weapon != 0 || shield != 0 {
		t.Errorf("(unequipped) weapon=%d shield=%d, want 0,0", weapon, shield)
	}
}

// TestComputeLook_NilItemDB confirms a missing item_db disables the computation.
func TestComputeLook_NilItemDB(t *testing.T) {
	rows := []invdomain.InventoryItem{{NameID: 1201, Equip: equip.HandRight}}
	if weapon, shield := computeLook(rows, nil); weapon != 0 || shield != 0 {
		t.Errorf("nil itemDB: weapon=%d shield=%d, want 0,0", weapon, shield)
	}
}
