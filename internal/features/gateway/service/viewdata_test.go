//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/assert"

	identityv1 "github.com/bouroo/goAthena/api/pb/identity/v1"
	"github.com/bouroo/goAthena/internal/features/gateway/domain"
)

// TestViewDataFromCharacter_MapsAllFields asserts that the helper
// copies every character-derived appearance field into a ViewData
// snapshot the same way the per-connection self-spawn does. The
// fields with no character source (BodyState, HealthState,
// EffectState, HeadDir, GUID, GEmblemVer, Honor, Virtue, IsPKModeON,
// Font, IsBoss, Body) must remain at their zero value — the
// fan-out encoder will write those slots as zero, matching the
// self-spawn.
func TestViewDataFromCharacter_MapsAllFields(t *testing.T) {
	t.Parallel()

	c := &identityv1.CharacterDetail{
		CharId:       9001,
		Name:         "alpha",
		ClassId:      7, // swordsman
		BaseLevel:    50,
		Hp:           1234,
		MaxHp:        2000,
		Hair:         5,
		HairColor:    3,
		ClothesColor: 1,
		Weapon:       1101,
		Shield:       2202,
		HeadTop:      3303,
		HeadMid:      4404,
		HeadBottom:   5505,
		Robe:         6606,
		Sex:          1, // male
	}

	got := viewDataFromCharacter(4242, c)

	assert.Equal(t, uint8(0), got.ObjectType, "ObjectType must be 0 (PC)")
	assert.Equal(t, uint32(4242), got.AID, "AID must be the connection AccountID")
	assert.Equal(t, uint32(9001), got.GID, "GID must come from CharacterDetail.CharId")
	assert.Equal(t, int16(150), got.Speed, "Speed must default to 150 (rAthena's pc_setnewpc)")
	assert.Equal(t, int16(7), got.Job, "Job must come from CharacterDetail.ClassId")
	assert.Equal(t, uint16(5), got.Head, "Head must come from CharacterDetail.Hair")
	assert.Equal(t, uint32(1101), got.Weapon)
	assert.Equal(t, uint32(2202), got.Shield)
	assert.Equal(t, uint16(5505), got.Accessory, "Accessory (head_bottom) must come from CharacterDetail.HeadBottom")
	assert.Equal(t, uint16(3303), got.Accessory2, "Accessory2 (head_top) must come from CharacterDetail.HeadTop")
	assert.Equal(t, uint16(4404), got.Accessory3, "Accessory3 (head_mid) must come from CharacterDetail.HeadMid")
	assert.Equal(t, int16(3), got.HeadPalette, "HeadPalette must come from CharacterDetail.HairColor")
	assert.Equal(t, int16(1), got.BodyPalette, "BodyPalette must come from CharacterDetail.ClothesColor")
	assert.Equal(t, uint16(6606), got.Robe)
	assert.Equal(t, uint8(1), got.Sex)
	assert.Equal(t, int16(50), got.CLevel)
	assert.Equal(t, int32(2000), got.MaxHP)
	assert.Equal(t, int32(1234), got.HP)
	assert.Equal(t, "alpha", got.Name)
}

// TestViewDataFromCharacter_ClampsOverwideFields covers the clamp
// helpers: ClassId/Hair/HairColor/ClothesColor/BaseLevel are uint32
// on the wire but the destination fields are 16-bit. Values above
// 0xffff must saturate to 0 (sentinel) so a misconfigured row visibly
// fails rather than silently wraps. Head* / Robe / Sex are also
// clamped via clampUint16; Sex is the only 8-bit destination so it
// is further narrowed to the low byte of the clamp result.
func TestViewDataFromCharacter_ClampsOverwideFields(t *testing.T) {
	t.Parallel()

	c := &identityv1.CharacterDetail{
		CharId:       1,
		Name:         "wide",
		ClassId:      0x10001, // 17-bit — clampUint16 saturates to 0.
		BaseLevel:    0x10050, // ditto
		Hair:         0x10005, // ditto
		HairColor:    0x10003, // ditto
		ClothesColor: 0x10001, // ditto
		Weapon:       0xFFFFFFFF,
		Shield:       0xFFFFFFFF,
		HeadTop:      0x10000, // exactly 16-bit boundary, clamp returns 0
		HeadMid:      0x10000,
		HeadBottom:   0x10000,
		Robe:         0x10000,
		Sex:          0x101,
		MaxHp:        0xFFFFFFFF,
	}

	got := viewDataFromCharacter(1, c)

	// clampUint16 saturates to 0 for v > 0xffff.
	assert.Equal(t, int16(0), got.Job, "ClassId > 0xffff must saturate Job to 0")
	assert.Equal(t, int16(0), got.CLevel, "BaseLevel > 0xffff must saturate CLevel to 0")
	assert.Equal(t, uint16(0), got.Head, "Hair > 0xffff must saturate Head to 0")
	assert.Equal(t, int16(0), got.HeadPalette, "HairColor > 0xffff must saturate HeadPalette to 0")
	assert.Equal(t, int16(0), got.BodyPalette, "ClothesColor > 0xffff must saturate BodyPalette to 0")
	assert.Equal(t, uint16(0), got.Accessory, "HeadBottom > 0xffff must saturate Accessory to 0")
	assert.Equal(t, uint16(0), got.Accessory2, "HeadTop > 0xffff must saturate Accessory2 to 0")
	assert.Equal(t, uint16(0), got.Accessory3, "HeadMid > 0xffff must saturate Accessory3 to 0")
	assert.Equal(t, uint16(0), got.Robe, "Robe > 0xffff must saturate Robe to 0")
	// Weapon/Shield are 32-bit on the wire so they pass through
	// unchanged.
	assert.Equal(t, uint32(0xFFFFFFFF), got.Weapon)
	assert.Equal(t, uint32(0xFFFFFFFF), got.Shield)
	// MaxHP is int32 on the wire; the helper performs an unchecked
	// cast from uint32. The test documents the actual behavior.
	assert.Equal(t, int32(-1), got.MaxHP)
	// Sex is uint8: clampUint16(0x101) = 0x0101, narrowed to byte
	// = 0x01.
	assert.Equal(t, uint8(0x01), got.Sex)
}

// TestViewDataFromCharacter_NilCharacterReturnsEmpty is a defensive
// guard: a nil character is impossible in production (the dispatch
// handler only calls SetView when char != nil) but the helper must
// not panic on nil so future callers can use it without a guard.
func TestViewDataFromCharacter_NilCharacterReturnsEmpty(t *testing.T) {
	t.Parallel()

	got := viewDataFromCharacter(4242, nil)
	assert.Equal(t, domain.ViewData{
		ObjectType: 0, // PC — the helper always sets this.
		AID:        4242,
		Speed:      150,
	}, got, "nil character must yield a zero-valued View except ObjectType/AID/Speed")
}
