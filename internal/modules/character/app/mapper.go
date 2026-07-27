// Package app implements the character bounded context's use cases: the
// CH_ENTER (char list) and CH_SELECT_CHAR (zone redirect) packet handlers and
// the domain-to-wire mapper they share. It depends on the character domain
// ports, the gateway domain Conn, and the packet kernel — nothing transport- or
// persistence-specific.
package app

import (
	"math"

	"github.com/bouroo/goAthena/internal/modules/character/domain"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// defaultWalkSpeed is rAthena's DEFAULT_WALK_SPEED (mmo.hpp:93), the speed the
// char-list builder hardcodes into every CHARACTER_INFO entry — the value the
// client renders before it learns the live speed from the map server.
const defaultWalkSpeed = 150

// ridingOptionMask is the bitmask of option bits rAthena treats as "mounted":
// when any is set the weapon is sent as 0, because the legacy client crashes on
// login if a riding option and a weapon are sent together (char.cpp:1800). The
// literals come straight from char.cpp's option&(0x20|0x80000|…) expression.
const ridingOptionMask = 0x20 |
	0x80000 | 0x100000 | 0x200000 | 0x400000 | 0x800000 |
	0x1000000 | 0x2000000 | 0x4000000 | 0x8000000

// MapCharacterInfo maps a domain Character to its on-wire CHARACTER_INFO form,
// field-for-field with rAthena's char.cpp mmo_char→char_info builder
// (char.cpp:1786-1862). The clamps mirror rAthena's own: str/agi/…/luk and the
// hair color are truncated to uint8, status/skill points to int16, and SP/MaxSP
// to int16 — the DB columns are wider than the wire slots, and the reference
// client expects the clamped values. Bodystate and healthstate are hardcoded 0
// (rAthena does the same; the option bitmask rides in effectstate).
func MapCharacterInfo(c domain.Character) packet.CharacterInfo {
	return packet.CharacterInfo{
		GID:      c.CharID,
		Exp:      int64(c.BaseExp), //nolint:gosec // G115: base_exp→int64, rAthena casts directly
		Money:    int32(c.Zeny),    //nolint:gosec // G115: zeny→int32, rAthena casts directly
		JobExp:   int64(c.JobExp),  //nolint:gosec // G115: job_exp→int64, rAthena casts directly
		JobLevel: int32(c.JobLevel),

		// rAthena hardcodes bodystate/healthstate to 0 in the char-info builder;
		// the option bitmask is carried in effectstate.
		BodyState:   0,
		HealthState: 0,
		EffectState: int32(c.Option), //nolint:gosec // G115: option→int32, rAthena casts directly

		Virtue: int32(c.Karma),
		Honor:  int32(c.Manner),

		JobPoint: clampI16(c.StatusPoint),

		HP:    int64(c.HP),
		MaxHP: int64(c.MaxHP),
		// rAthena clamps SP/MaxSP to INT16_MAX on the char-info path even though
		// the wire slot is int64; mirror it for byte parity with the client.
		SP:    clampSP(c.SP),
		MaxSP: clampSP(c.MaxSP),

		Speed: defaultWalkSpeed,
		Job:   int16(c.Class), //nolint:gosec // G115: class→int16, rAthena casts directly
		Head:  int16(c.Hair),
		Body:  int16(c.Body), //nolint:gosec // G115: body→int16, rAthena casts directly
		// Suppress the weapon when the option indicates a mount (client-crash
		// guard). M2a chars do not ride, so this is a no-op for seeded data.
		Weapon: weaponFor(c.Option, c.Weapon),
		Level:  int16(c.BaseLevel), //nolint:gosec // G115: base_level→int16, rAthena casts directly

		SPPoint: clampI16(c.SkillPoint),

		Accessory:   int16(c.HeadBottom),   //nolint:gosec // G115: head_bottom→int16, rAthena casts directly
		Shield:      int16(c.Shield),       //nolint:gosec // G115: shield→int16, rAthena casts directly
		Accessory2:  int16(c.HeadTop),      //nolint:gosec // G115: head_top→int16, rAthena casts directly
		Accessory3:  int16(c.HeadMid),      //nolint:gosec // G115: head_mid→int16, rAthena casts directly
		HeadPalette: int16(c.HairColor),    //nolint:gosec // G115: hair_color→int16, rAthena casts directly
		BodyPalette: int16(c.ClothesColor), //nolint:gosec // G115: clothes_color→int16, rAthena casts directly

		Name: c.Name,

		Str: clampU8(c.Str),
		Agi: clampU8(c.Agi),
		Vit: clampU8(c.Vit),
		Int: clampU8(c.Int),
		Dex: clampU8(c.Dex),
		Luk: clampU8(c.Luk),

		CharNum:   c.Slot,
		HairColor: clampU8(c.HairColor),

		// bIsChangedCharName: rename quota remaining ⇒ "name is changeable" (0)
		// vs exhausted (1). Inverted from ChrNameChangeCnt below.
		IsChangedCharName: renameToChanged(c.Rename),

		MapName: c.LastMap,

		// DelRevDate is seconds-until-deletion for a pending delete, 0 otherwise.
		// M2a seeds carry delete_date=0, so this is 0; the live time-delta is
		// computed when the delete flow lands (deferred).
		DelRevDate: int32(c.DeleteDate), //nolint:gosec // G115: delete_date→int32, rAthena casts directly

		RobePalette: int32(c.Robe),
		// goAthena has no char-move feature flag yet; rAthena reports 0 while it
		// is disabled, so the slot-change counter is 0 regardless of `moves`.
		ChrSlotChangeCnt: 0,
		ChrNameChangeCnt: renameToCounter(c.Rename),

		Sex: c.Sex,
	}
}

// weaponFor returns the weapon view ID unless the option bitmask indicates a
// mount, in which case it returns 0 (char.cpp:1800).
func weaponFor(option uint32, weapon uint16) int16 {
	if option&ridingOptionMask != 0 {
		return 0
	}
	return int16(weapon) //nolint:gosec // G115: weapon→int16, rAthena casts directly
}

// renameToChanged maps the rename quota to the bIsChangedCharName wire byte:
// quota remaining (>0) ⇒ 0 (changeable), exhausted (0) ⇒ 1.
func renameToChanged(rename uint32) int16 {
	if rename > 0 {
		return 0
	}
	return 1
}

// renameToCounter maps the rename quota to the ChrNameChangeCnt wire field:
// quota remaining (>0) ⇒ 1 (Add-Ons sidebar shown), exhausted (0) ⇒ 0.
func renameToCounter(rename uint32) int32 {
	if rename > 0 {
		return 1
	}
	return 0
}

// clampU8 truncates a wide DB stat to the uint8 wire slot (rAthena's u16min
// (val, UINT8_MAX)).
func clampU8(v uint16) uint8 {
	if v > math.MaxUint8 {
		return math.MaxUint8
	}
	return uint8(v)
}

// clampI16 truncates a wide DB point pool to the int16 wire slot (rAthena's
// umin(val, INT16_MAX)).
func clampI16(v uint32) int16 {
	if v > math.MaxInt16 {
		return math.MaxInt16
	}
	return int16(v)
}

// clampSP truncates SP/MaxSP to INT16_MAX on the char-info path (rAthena's
// min(val, INT16_MAX)), storing into the int64 wire slot.
func clampSP(v uint32) int64 {
	if v > math.MaxInt16 {
		return math.MaxInt16
	}
	return int64(v)
}
