package packet

import (
	"encoding/binary"
	"fmt"
	"io"
)

// sizeSkillEntry is the per-entry size of SKILLDATA on the wire for
// PACKETVER < 20190807 (rathena/src/map/packets_struct.hpp:4250-4258):
//
//	off  bytes field
//	 0     2    id      uint16
//	 2     4    inf     int (lower bits are flag bits)
//	 6     2    level   uint16
//	 8     2    sp      uint16
//	10     2    range2  uint16
//	12    24    name    char[NAME_LENGTH]
//	36     1    upFlag  uint8
//	             ----
//	            37
//
// NAME_LENGTH = 23 + 1 = 24 (rathena/src/common/mmo.hpp:154). The
// 0x0b32 (PACKETVER >= 20190807) variant replaces the fixed-name slot
// with uint16 level2 and is not handled here — the gateway targets
// the pre-2019 client, and 0x010f is only ever paired with this
// SKILLDATA shape.
const sizeSkillEntry = 37

// SkillData is the per-entry shape rAthena writes into ZC_SKILLINFO_LIST
// for PACKETVER < 20190807. Source:
//
//	rathena/src/map/packets_struct.hpp:4250-4258 (SKILLDATA, #else branch)
//	ZC_SKILLINFO_LIST (0x010f) declared at rathena/src/map/
//	packets_struct.hpp:4271-4279.
//
// On-wire size: sizeSkillEntry (37 bytes).
//
// Inf carries the skill's learned-state bitmap (rAthena stores it as
// `int` but only the lower bits are meaningful — see clif.cpp:5678 for
// the INFINITE_* / KNOCK_* flag values). UpFlag is the "this skill is
// levelled up this session" hint the client uses to flash the row.
type SkillData struct {
	ID     uint16
	Inf    uint32
	Level  uint16
	SP     uint16
	Range2 uint16
	Name   string
	UpFlag uint8
}

// SkillInfoListResponse encodes a ZC_SKILLINFO_LIST packet (command
// 0x010f, variable length, PACKETVER < 20190807). The server sends
// this once on login and on CZ_NOTIFY_ACTORINIT so the client
// populates the skill pane with every skill the character has learned.
//
// Wire layout (rathena/src/map/packets_struct.hpp:4271-4275):
//
//	int16  packetType   (0x010f)
//	int16  packetLength (4 + 37 * len(Skills))
//	[per skill, 37 bytes:] SkillData
//
// An empty Skills slice produces the same 4-byte frame as
// EncodeEmptySkillList, so callers can transparently substitute one
// for the other.
type SkillInfoListResponse struct {
	Skills []SkillData
}

// Encode writes the ZC_SKILLINFO_LIST packet to w. The wire length is
// 4 + 37 * len(Skills); the encoder computes packetLength from the
// entry count so the caller cannot accidentally emit a frame whose
// length slot disagrees with the trailing bytes. Names longer than
// the 24-byte slot are truncated and the slot stays NUL-terminated.
func (r SkillInfoListResponse) Encode(w io.Writer) error {
	total := 4 + len(r.Skills)*sizeSkillEntry
	if total > 0xffff {
		return fmt.Errorf("packet: write ZC_SKILLINFO_LIST: too many skills (%d)", len(r.Skills))
	}
	buf := make([]byte, total)
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCSKILLINFOLIST)
	binary.LittleEndian.PutUint16(buf[2:], uint16(total))
	for i, s := range r.Skills {
		writeSkillEntry(buf, 4+i*sizeSkillEntry, s)
	}
	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write ZC_SKILLINFO_LIST: %w", err)
	}
	return nil
}

// writeSkillEntry serializes one SkillData into the 37-byte rAthena
// SKILLDATA layout for PACKETVER < 20190807. The caller is responsible
// for slicing the destination buffer so the offset has enough room
// (sizeSkillEntry bytes).
//
// Names are copied into a fixed 24-byte slot, NUL-terminated, and
// truncated when they would overflow the slot — the client's skill
// parser uses memcpy(name, 24) on the receiving end, so the trailing
// bytes after the first NUL must be zero.
func writeSkillEntry(buf []byte, off int, s SkillData) {
	binary.LittleEndian.PutUint16(buf[off:], s.ID)
	binary.LittleEndian.PutUint32(buf[off+2:], s.Inf)
	binary.LittleEndian.PutUint16(buf[off+6:], s.Level)
	binary.LittleEndian.PutUint16(buf[off+8:], s.SP)
	binary.LittleEndian.PutUint16(buf[off+10:], s.Range2)
	writeFixedName(buf[off+12:off+12+24], s.Name)
	buf[off+36] = s.UpFlag
}

// writeFixedName copies src into the 24-byte name slot, NUL-terminated.
// Names shorter than 24 bytes are padded with NULs; names longer than
// 23 usable bytes are truncated so the final byte is always 0.
func writeFixedName(dst []byte, src string) {
	n := copy(dst, src)
	if n >= len(dst) {
		dst[len(dst)-1] = 0
		return
	}
	for i := n; i < len(dst); i++ {
		dst[i] = 0
	}
}
