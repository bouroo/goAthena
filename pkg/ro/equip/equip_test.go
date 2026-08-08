//go:build unit

package equip_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/bouroo/goAthena/pkg/ro/equip"
)

// TestLocationBits_Knife is the L3 anchor: a Knife (item_db_equip.yml 1201) lists
// only Right_Hand, so its equip bitmask is EQP_HAND_R (2). The equip service and
// the inventory.equip column both key off this value.
func TestLocationBits_Knife(t *testing.T) {
	t.Parallel()
	assert.Equal(t, equip.HandRight, equip.LocationBits(map[string]bool{"Right_Hand": true}))
}

// TestLocationBits_FullHelm asserts the three head slots OR into EQP_HELM, the
// composite a full-helm item (Head_Top+Head_Mid+Head_Low) carries.
func TestLocationBits_FullHelm(t *testing.T) {
	t.Parallel()
	got := equip.LocationBits(map[string]bool{
		"Head_Top": true, "Head_Mid": true, "Head_Low": true,
	})
	assert.Equal(t, equip.Helm, got, "Helm = Head_Top|Head_Mid|Head_Low = 769")
}

// TestLocationBits_TwoHanded asserts the Both_Hand alias resolves to EQP_ARMS
// (Hand_R|Hand_L = 34) — two-handed weapons list a single Both_Hand key.
func TestLocationBits_TwoHanded(t *testing.T) {
	t.Parallel()
	assert.Equal(t, equip.Arms, equip.LocationBits(map[string]bool{"Both_Hand": true}))
}

// TestLocationBits_BothAccessories asserts the Right_/Left_ accessory keys map to
// the right slots: ACC_R (8) and ACC_L (128), not the swapped values the plan's
// table carried. Together they equal EQP_ACC (136).
func TestLocationBits_BothAccessories(t *testing.T) {
	t.Parallel()
	got := equip.LocationBits(map[string]bool{
		"Right_Accessory": true, "Left_Accessory": true,
	})
	assert.Equal(t, equip.Accessories, got, "ACC_R|ACC_L = 136")
	assert.Equal(t, equip.AccessoryRight, equip.LocationBits(map[string]bool{"Right_Accessory": true}))
	assert.Equal(t, equip.AccessoryLeft, equip.LocationBits(map[string]bool{"Left_Accessory": true}))
}

// TestLocationBits_CaseInsensitive matches script_get_constant's case-insensitive
// lookup so a YAML written lower- or upper-case still resolves.
func TestLocationBits_CaseInsensitive(t *testing.T) {
	t.Parallel()
	got := equip.LocationBits(map[string]bool{"right_hand": true, "HEAD_TOP": true})
	assert.Equal(t, equip.HandRight|equip.HeadTop, got, "2|256 = 258")
}

// TestLocationBits_InactiveIgnored asserts a key explicitly false contributes no
// bit (it would clear in rAthena, but the load path starts from a zero bitmask,
// so an inactive key is simply absent).
func TestLocationBits_InactiveIgnored(t *testing.T) {
	t.Parallel()
	got := equip.LocationBits(map[string]bool{"Right_Hand": false, "Head_Low": true})
	assert.Equal(t, equip.HeadLow, got)
}

// TestLocationBits_UnknownIgnored asserts an unrecognized key is dropped rather
// than faulting — the safe no-op matching the loader's warn-and-demote path.
func TestLocationBits_UnknownIgnored(t *testing.T) {
	t.Parallel()
	got := equip.LocationBits(map[string]bool{"Nonsense": true, "Garment": true})
	assert.Equal(t, equip.Garment, got)
}

// TestLocationBits_Nil asserts a non-equip item (no Locations block) yields a zero
// bitmask — the value UseEquipment reads to reject a non-equipment item.
func TestLocationBits_Nil(t *testing.T) {
	t.Parallel()
	assert.Equal(t, uint32(0), equip.LocationBits(nil))
	assert.Equal(t, uint32(0), equip.LocationBits(map[string]bool{}))
}
