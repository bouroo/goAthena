package packet

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Skill-usage codec family for PACKETVER 20250604: CZ_USE_SKILL2 (parse)
// + ZC_NOTIFY_SKILL (encode) + ZC_ACK_TOUSESKILL (encode). The structs
// mirror the rAthena layouts pinned in the size/header const blocks in
// map.go and on the per-type godocs below.

// CZUseSkill is the decoded form of a client → map-server
// CZ_USE_SKILL2 frame (header 0x0438, 10 bytes on the wire). Source:
// rathena/src/map/clif_shuffle.hpp:4750 (the
// PACKETVER_RE_NUM >= 20190904 branch binds 0x0438 to
// clif_parse_UseSkillToId with field offsets 2,4,6) +
// rathena/src/map/clif.cpp:12987 (clif_parse_UseSkillToId).
//
// On-wire layout:
//
//	int16 packetType (0x0438)
//	int16 skillLv    — the skill level the client claims to be casting
//	uint16 skillID   — rAthena skill DB id (e.g. 5 = SM_BASH)
//	uint32 targetID  — victim entity GID; for self-targeted skills this
//	                   is the caster's own GID
//
// The 4-byte AID slot that older CZ_USE_SKILL variants carried has been
// folded into targetID for the UseSkillToId branch — rAthena reads the
// skillLV / skillID / targetID triple directly (clif.cpp:12987-12995)
// and derives the caster AID from the session.
type CZUseSkill struct {
	// SkillLv is the level at which the client is requesting the skill.
	// The dispatcher validates this against the skill DB's MaxLevel
	// before honouring the request.
	SkillLv int16
	// SkillID is the rAthena skill DB id (e.g. 5 = SM_BASH).
	SkillID uint16
	// TargetID is the entity GID the client wants to cast on.
	TargetID uint32
}

// ParseCZUseSkill decodes a CZ_USE_SKILL2 frame (including the 2-byte
// cmd header) into a CZUseSkill. The frame must carry cmd 0x0438 and
// contain at least 10 bytes; trailing bytes are ignored.
//
// Returns a wrapped error naming the off-by-one byte count if the frame
// is shorter than 10 bytes, or naming the unexpected cmd id if the
// header is not 0x0438.
func ParseCZUseSkill(frame []byte) (CZUseSkill, error) {
	if len(frame) < sizeCZUseSkill2 {
		return CZUseSkill{}, fmt.Errorf("packet: parse CZ_USE_SKILL2: want at least %d bytes, got %d", sizeCZUseSkill2, len(frame))
	}
	if cmd := binary.LittleEndian.Uint16(frame[0:2]); cmd != HeaderCZUSESKILL {
		return CZUseSkill{}, fmt.Errorf("packet: parse CZ_USE_SKILL2: unexpected cmd 0x%04x", cmd)
	}
	return CZUseSkill{
		SkillLv:  int16(binary.LittleEndian.Uint16(frame[2:4])), //nolint:gosec // wire slot is signed int16
		SkillID:  binary.LittleEndian.Uint16(frame[4:6]),
		TargetID: binary.LittleEndian.Uint32(frame[6:10]),
	}, nil
}

// Encode writes the CZ_USE_SKILL2 packet to w, mirroring the on-wire
// layout documented on CZUseSkill: [2:cmd=0x0438][2:skillLv]
// [2:skillID][4:targetID] = 10 bytes. Used by tests and by the e2e
// harness to drive the skill-usage handler.
func (r CZUseSkill) Encode(w io.Writer) error {
	buf := make([]byte, sizeCZUseSkill2)
	binary.LittleEndian.PutUint16(buf[0:], HeaderCZUSESKILL)
	binary.LittleEndian.PutUint16(buf[2:], uint16(r.SkillLv)) //nolint:gosec // wire slot is signed int16
	binary.LittleEndian.PutUint16(buf[4:], r.SkillID)
	binary.LittleEndian.PutUint32(buf[6:], r.TargetID)
	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write CZ_USE_SKILL2: %w", err)
	}
	return nil
}

// NotifySkillResponse encodes ZC_NOTIFY_SKILL (0x01de) — the per-hit
// skill notification the server broadcasts to all clients in view when
// a skill produces damage/heal/effect. Source:
// rathena/src/map/packets_struct.hpp:4658-4671 (PACKETVER >= 3
// branch, which uses int32 damage — 20250604 satisfies this).
//
// On-wire layout:
//
//	int16  packetType  (0x01de)
//	uint16 SKID        — skill DB id (e.g. 5 = SM_BASH)
//	uint32 AID         — caster entity GID
//	uint32 TargetID    — victim entity GID
//	uint32 StartTime   — server tick at which the hit was resolved
//	int32  AttackMT    — attack motion time (ms); 0 is acceptable for
//	                     instant-cast pre-Renewal skills like SM_BASH
//	int32  AttackedMT  — victim motion time (ms); 0 likewise
//	int32  Damage      — damage applied to the target (signed; negative
//	                     = heal on Renewal, but pre-Renewal sends ≥0)
//	int16  Level       — skill level used (echoes CZ_USE_SKILL2.SkillLv)
//	int16  Count       — number of hits in this notification (DIV); the
//	                     Bash slice sends 1
//	int8   Action      — rAthena damage_type selector (clif.cpp:691-707);
//	                     0 = DMG_NORMAL for a regular offensive hit
//
// Total: 2+2+4+4+4+4+4+4+2+2+1 = 33 bytes (sizeZCNotifySkill).
type NotifySkillResponse struct {
	SKID       uint16
	AID        uint32
	TargetID   uint32
	StartTime  uint32
	AttackMT   int32
	AttackedMT int32
	Damage     int32
	Level      int16
	Count      int16
	Action     int8
}

// Size returns the on-wire byte length that Encode will write (always 33).
func (r NotifySkillResponse) Size() int {
	return sizeZCNotifySkill
}

// Encode writes the ZC_NOTIFY_SKILL packet to w. The buffer is fixed at
// sizeZCNotifySkill (33) bytes — the client parses a fixed-layout
// block, so a wrong-length frame would corrupt the next packet on the
// TCP stream.
func (r NotifySkillResponse) Encode(w io.Writer) error {
	var buf [sizeZCNotifySkill]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCNOTIFYSKILL)
	binary.LittleEndian.PutUint16(buf[2:], r.SKID)
	binary.LittleEndian.PutUint32(buf[4:], r.AID)
	binary.LittleEndian.PutUint32(buf[8:], r.TargetID)
	binary.LittleEndian.PutUint32(buf[12:], r.StartTime)
	binary.LittleEndian.PutUint32(buf[16:], uint32(r.AttackMT))   //nolint:gosec // wire slot is unsigned
	binary.LittleEndian.PutUint32(buf[20:], uint32(r.AttackedMT)) //nolint:gosec // wire slot is unsigned
	binary.LittleEndian.PutUint32(buf[24:], uint32(r.Damage))     //nolint:gosec // wire slot is unsigned
	binary.LittleEndian.PutUint16(buf[28:], uint16(r.Level))      //nolint:gosec // wire slot is signed int16
	binary.LittleEndian.PutUint16(buf[30:], uint16(r.Count))      //nolint:gosec // wire slot is signed int16
	buf[32] = byte(r.Action)                                      //nolint:gosec // int8 fits in a byte
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_NOTIFY_SKILL: %w", err)
	}
	return nil
}

// AckUseSkillResponse encodes ZC_ACK_TOUSESKILL (0x0110) — the server's
// "skill failed" reply sent when CZ_USE_SKILL2 is rejected (insufficient
// SP, wrong level, on cooldown, no skill learned, etc.). Source:
// rathena/src/map/packets_struct.hpp:2448-2460 (the
// PACKETVER_MAIN_NUM >= 20181121 / PACKETVER_RE_NUM >= 20180704
// branch — 20250604 satisfies it — which uses int32 btype and
// uint32 itemId).
//
// On-wire layout:
//
//	int16  packetType (0x0110)
//	uint16 skillId   — the skill that was rejected
//	int32  btype     — "before-type" / category selector rAthena uses to
//	                   branch the failure handling on the client
//	                   (BF_WEAPON / BF_MAGIC / BF_MISC); pre-Renewal the
//	                   emulator passes BF_SHORT (0) for melee skills
//	uint32 itemId    — rAthena item id when btype implies an item-driven
//	                   skill; 0 when the skill isn't item-driven
//	uint8  flag      — rAthena success/partial-success flag; 0 = hard
//	                   fail
//	uint8  cause     — USESKILL_FAIL_* reason code (see the const block
//	                   below). 12 = UseSkillFailSPInsufficient.
//
// Total: 2+2+4+4+1+1 = 14 bytes (sizeZCAckToUseSkill).
type AckUseSkillResponse struct {
	SkillID uint16
	BType   int32
	ItemID  uint32
	Flag    uint8
	Cause   uint8
}

// Size returns the on-wire byte length that Encode will write (always 14).
func (r AckUseSkillResponse) Size() int {
	return sizeZCAckToUseSkill
}

// Encode writes the ZC_ACK_TOUSESKILL packet to w. Used to acknowledge
// (positively or negatively) a CZ_USE_SKILL2 request. The pre-Renewal
// Bash slice only emits the negative path (SP insufficient →
// Cause = UseSkillFailSPInsufficient).
func (r AckUseSkillResponse) Encode(w io.Writer) error {
	var buf [sizeZCAckToUseSkill]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCACKTOUSESKILL)
	binary.LittleEndian.PutUint16(buf[2:], r.SkillID)
	binary.LittleEndian.PutUint32(buf[4:], uint32(r.BType)) //nolint:gosec // wire slot is unsigned
	binary.LittleEndian.PutUint32(buf[8:], r.ItemID)
	buf[12] = r.Flag
	buf[13] = r.Cause
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_ACK_TOUSESKILL: %w", err)
	}
	return nil
}

// USESKILL_FAIL_* cause codes for the AckUseSkillResponse.Cause byte.
// Sourced from rathena/src/map/skill.hpp:e_fail_skill_reason (verified
// against packets_struct.hpp:2448-2460 + clif.cpp:29812-29833 where
// the server picks the cause). Only the SP-insufficient code is
// emitted by the current slice; the rest are exported so handlers can
// extend the negative-path surface without redefining constants.
const (
	// UseSkillFailSPInsufficient (rAthena: USESKILL_FAIL_SP_INSUFFICIENT=12).
	// Emitted when the caster's current SP < skill.SpAt(skillLv). This is
	// the only negative-cause the P3b-2 skill-usage slice needs.
	UseSkillFailSPInsufficient uint8 = 12
)
