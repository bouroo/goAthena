package service

import (
	identityv1 "github.com/bouroo/goAthena/api/pb/identity/v1"
	"github.com/bouroo/goAthena/internal/features/gateway/domain"
)

// viewDataFromCharacter maps the identityv1.CharacterDetail returned
// by identity.GetCharacter into a domain.ViewData snapshot for the
// session registry.
//
// The mapping mirrors the self-spawn field sources used by
// buildSelfSpawn (handleCZEnter) so a future fan-out that constructs
// a SpawnUnitResponse from the ViewData will produce the same wire
// shape as the per-connection self-spawn. Fields on SpawnUnitResponse
// with no source in the identity proto (BodyState, HealthState,
// EffectState, HeadDir, GUID, GEmblemVer, Honor, Virtue, IsPKModeON,
// Font, IsBoss, Body) are left at their zero value — the fan-out
// encoder will write those slots as zero, matching the self-spawn's
// output for the same fields today.
//
// ObjectType is always 0 (PC) — the gateway only ever registers PC
// sessions today; NPC and monster entities are not gateway-side
// clients and never enter the registry.
//
// Speed defaults to 150 (rAthena's pc_setnewpc amotion) to match
// buildSelfSpawn's hardcoded self-spawn speed.
func viewDataFromCharacter(aid uint32, c *identityv1.CharacterDetail) domain.ViewData {
	v := domain.ViewData{
		ObjectType: 0, // PC — the only value the gateway registers.
		AID:        aid,
		GID:        c.GetCharId(),
		Speed:      150,
		Job:        int16(clampUint16(c.GetClassId())), //nolint:gosec // wire slot is 16-bit
		Head:       clampUint16(c.GetHair()),
		Weapon:     c.GetWeapon(),
		Shield:     c.GetShield(),
		// Accessory / Accessory2 / Accessory3 follow the same
		// head_bottom / head_top / head_mid wire order as the
		// self-spawn.
		Accessory:   clampUint16(c.GetHeadBottom()),
		Accessory2:  clampUint16(c.GetHeadTop()),
		Accessory3:  clampUint16(c.GetHeadMid()),
		HeadPalette: int16(clampUint16(c.GetHairColor())),    //nolint:gosec // wire slot is 16-bit
		BodyPalette: int16(clampUint16(c.GetClothesColor())), //nolint:gosec // ditto
		Robe:        clampUint16(c.GetRobe()),
		Sex:         uint8(clampUint16(c.GetSex())),       //nolint:gosec // wire slot is 8-bit
		CLevel:      int16(clampUint16(c.GetBaseLevel())), //nolint:gosec // wire slot is 16-bit
		MaxHP:       int32(c.GetMaxHp()),                  //nolint:gosec // max_hp is int32 on the wire; clamped upstream by rAthena
		HP:          int32(c.GetHp()),                     //nolint:gosec // ditto
		Name:        c.GetName(),
	}
	return v
}
