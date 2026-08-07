package packet

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Ground-target skill codec family for PACKETVER 20250604: CZ_USE_SKILL_TOPOS
// (parse) + ZC_NOTIFY_GROUNDSKILL (encode). The structs mirror the rAthena
// layouts pinned in the size/header const blocks in map.go and the per-type
// godocs below.

// CZUseSkillToPos is the decoded form of a client → map-server CZ_USE_SKILL_TOPOS
// frame (header 0x0AF4, 11 bytes on the wire). Source:
// rathena/src/map/clif_packetdb.hpp:1905 (the PACKETVER >= 20180207 branch
// binds 0x0AF4 to clif_parse_UseSkillToPos with field offsets 2,4,6,8,10) +
// rathena/src/map/clif.cpp:13131 (clif_parse_UseSkillToPos).
//
// On-wire layout:
//
//	int16  packetType (0x0AF4)
//	int16  skillLv    — the skill level the client claims to be casting
//	uint16 skillID    — rAthena skill DB id (e.g. 89 = MG_STORMGUST)
//	uint16 xPos       — target tile X
//	uint16 yPos       — target tile Y
//	uint8  moreinfo   — wire-present but server-ignored (rAthena clif.cpp:13137
//	                    RFIFOB is commented out and passes -1); consumed then
//	                    discarded so the frame length stays 11
type CZUseSkillToPos struct {
	// SkillLv is the level at which the client is requesting the skill.
	SkillLv int16
	// SkillID is the rAthena skill DB id (e.g. 89 = MG_STORMGUST).
	SkillID uint16
	// X is the target tile X coordinate.
	X uint16
	// Y is the target tile Y coordinate.
	Y uint16
}

// ParseCZUseSkillToPos decodes a CZ_USE_SKILL_TOPOS frame. The frame must carry
// cmd 0x0AF4 and contain at least 11 bytes; the trailing moreinfo byte at [10]
// is consumed but discarded (rAthena ignores it). Returns a wrapped error naming
// the byte count if the frame is short, or naming the unexpected cmd id otherwise.
func ParseCZUseSkillToPos(frame []byte) (CZUseSkillToPos, error) {
	if len(frame) < sizeCZUseSkillToPos {
		return CZUseSkillToPos{}, fmt.Errorf("packet: parse CZ_USE_SKILL_TOPOS: want at least %d bytes, got %d", sizeCZUseSkillToPos, len(frame))
	}
	if cmd := binary.LittleEndian.Uint16(frame[0:2]); cmd != HeaderCZUSESKILLTOPOS {
		return CZUseSkillToPos{}, fmt.Errorf("packet: parse CZ_USE_SKILL_TOPOS: unexpected cmd 0x%04x", cmd)
	}
	return CZUseSkillToPos{
		SkillLv: int16(binary.LittleEndian.Uint16(frame[2:4])), //nolint:gosec // wire slot is signed int16
		SkillID: binary.LittleEndian.Uint16(frame[4:6]),
		X:       binary.LittleEndian.Uint16(frame[6:8]),
		Y:       binary.LittleEndian.Uint16(frame[8:10]),
	}, nil
}

// Encode writes the CZ_USE_SKILL_TOPOS packet to w, mirroring the on-wire layout
// documented on CZUseSkillToPos: [2:cmd=0x0AF4][2:skillLv][2:skillID][2:xPos]
// [2:yPos][1:moreinfo=0] = 11 bytes. Used by tests and the e2e harness to drive
// the ground-skill handler.
func (r CZUseSkillToPos) Encode(w io.Writer) error {
	var buf [sizeCZUseSkillToPos]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderCZUSESKILLTOPOS)
	binary.LittleEndian.PutUint16(buf[2:], uint16(r.SkillLv)) //nolint:gosec // wire slot is signed int16
	binary.LittleEndian.PutUint16(buf[4:], r.SkillID)
	binary.LittleEndian.PutUint16(buf[6:], r.X)
	binary.LittleEndian.PutUint16(buf[8:], r.Y)
	// buf[10] moreinfo — rAthena ignores it; write 0 so the frame is exactly 11B.
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write CZ_USE_SKILL_TOPOS: %w", err)
	}
	return nil
}

// GroundSkillPoseEffect encodes ZC_NOTIFY_GROUNDSKILL (0x0117) — the
// non-damaging ground-skill animation broadcast the server sends to the AREA
// around the cast tile when a ground-target skill resolves (rAthena's
// clif_skill_poseffect, clif.cpp:6197). One frame per cast (NOT per victim);
// per-victim damage rides the existing ZC_NOTIFY_SKILL (NotifySkillResponse).
// Source: rathena/src/map/packets_struct.hpp:4696-4705
// PACKET_ZC_NOTIFY_GROUNDSKILL.
//
// On-wire layout:
//
//	int16  packetType (0x0117)
//	uint16 SKID       — skill DB id (e.g. 89 = MG_STORMGUST)
//	uint32 AID        — CASTER entity GID (not a target; the target is x/y)
//	int16  level      — skill level used (echoes CZ_USE_SKILL_TOPOS.SkillLv)
//	int16  xPos       — cast tile X
//	int16  yPos       — cast tile Y
//	uint32 startTime  — server tick at which the cast resolved
//
// Total: 2+2+4+2+2+2+4 = 18 bytes (sizeZCNotifyGroundSkill).
type GroundSkillPoseEffect struct {
	SKID      uint16
	AID       uint32
	Level     int16
	XPos      int16
	YPos      int16
	StartTime uint32
}

// Size returns the on-wire byte length that Encode will write (always 18).
func (r GroundSkillPoseEffect) Size() int { return sizeZCNotifyGroundSkill }

// Encode writes the ZC_NOTIFY_GROUNDSKILL packet to w.
func (r GroundSkillPoseEffect) Encode(w io.Writer) error {
	var buf [sizeZCNotifyGroundSkill]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCNOTIFYGROUNDSKILL)
	binary.LittleEndian.PutUint16(buf[2:], r.SKID)
	binary.LittleEndian.PutUint32(buf[4:], r.AID)
	binary.LittleEndian.PutUint16(buf[8:], uint16(r.Level)) //nolint:gosec // wire slot is signed int16
	binary.LittleEndian.PutUint16(buf[10:], uint16(r.XPos)) //nolint:gosec // wire slot is signed int16
	binary.LittleEndian.PutUint16(buf[12:], uint16(r.YPos)) //nolint:gosec // wire slot is signed int16
	binary.LittleEndian.PutUint32(buf[14:], r.StartTime)
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_NOTIFY_GROUNDSKILL: %w", err)
	}
	return nil
}
