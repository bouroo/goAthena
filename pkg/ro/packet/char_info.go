package packet

import (
	"errors"
	"fmt"
	"io"
)

// Sentinel errors returned by CharacterInfo.Encode / AcceptEnterResponse.Encode
// when a string field does not fit its fixed-width on-wire slot.
var (
	// ErrCharNameTooLong is returned when a character name exceeds 24 bytes
	// (the on-wire name[24] field of CHARACTER_INFO; rathena's NAME_LENGTH).
	ErrCharNameTooLong = errors.New("packet: character name exceeds 24 bytes")
)

// On-wire byte offsets and fixed slot widths for one CHARACTER_INFO entry.
// Layout source: rathena/src/common/packets.hpp:31-105 (packed struct, all
// `#if PACKETVER >= …` branches active for PACKETVER 20250604).
//
//	0  uint32 GID
//	4  int64  exp
//	12 int32  money
//	16 int64  jobexp
//	24 int32  joblevel
//	28 int32  bodystate
//	32 int32  healthstate
//	36 int32  effectstate
//	40 int32  virtue
//	44 int32  honor
//	48 int16  jobpoint
//	50 int64  hp
//	58 int64  maxhp
//	66 int64  sp
//	74 int64  maxsp
//	82 int16  speed
//	84 int16  job
//	86 int16  head
//	88 int16  body
//	90 int16  weapon
//	92 int16  level
//	94 int16  sppoint
//	96 int16  accessory
//	98 int16  shield
//	100 int16 accessory2
//	102 int16 accessory3
//	104 int16 headpalette
//	106 int16 bodypalette
//	108 char[24] name
//	132 uint8  Str
//	133 uint8  Agi
//	134 uint8  Vit
//	135 uint8  Int
//	136 uint8  Dex
//	137 uint8  Luk
//	138 uint8  CharNum
//	139 uint8  hairColor
//	140 int16  bIsChangedCharName
//	142 char[16] mapName
//	158 int32  DelRevDate
//	162 int32  robePalette
//	166 int32  chr_slot_changeCnt
//	170 int32  chr_name_changeCnt
//	174 uint8  sex
//
// Total = 175 bytes.
const (
	// CharacterInfoSize is the exact on-wire byte length of one CHARACTER_INFO
	// entry for PACKETVER 20250604. Asserted by tests.
	CharacterInfoSize = 175

	// acceptEnterHeaderSize is the fixed byte length of the HC_ACCEPT_ENTER
	// prefix preceding the trailing CHARACTER_INFO[] flexible array:
	// int16 packetType + int16 packetLength + uint8 total + uint8 premiumStart
	// + uint8 premiumEnd + char extension[20] = 2+2+1+1+1+20 = 27.
	acceptEnterHeaderSize = 27

	// nameSlot is the fixed byte width of the name[24] field (NAME_LENGTH).
	nameSlot = 24

	// charMapNameSlot is the fixed byte width of the mapName[16] field inside
	// CHARACTER_INFO (MAP_NAME_LENGTH_EXT).
	charMapNameSlot = 16

	// acceptEnterExtensionSlot is the fixed byte width of the extension[20]
	// field in the HC_ACCEPT_ENTER header.
	acceptEnterExtensionSlot = 20
)

// CharacterInfo is the packed per-character struct embedded in the trailing
// flexible array of HC_ACCEPT_ENTER. Field order, widths, and offsets mirror
// rathena/src/common/packets.hpp:31-105. Total wire length is 175 bytes.
//
// The Identity service populates only GID / Job / Level / JobLevel / Name /
// CharNum / Sex for the M2b minimal char-list path; the remaining fields
// stay at the zero value, which rAthena reads as "no premium state, no
// equipment, base stats unset".
type CharacterInfo struct {
	GID         uint32
	Exp         int64
	Money       int32
	JobExp      int64
	JobLevel    int32
	BodyState   int32
	HealthState int32
	EffectState int32
	Virtue      int32
	Honor       int32
	JobPoint    int16

	HP    int64
	MaxHP int64
	SP    int64
	MaxSP int64

	Speed   int16
	Job     int16
	Head    int16
	Body    int16
	Weapon  int16
	Level   int16
	SPPoint int16

	Accessory   int16
	Shield      int16
	Accessory2  int16
	Accessory3  int16
	HeadPalette int16
	BodyPalette int16

	// Name is zero-padded ASCII on the wire to fill a 24-byte slot.
	// Must fit in 24 bytes (NAME_LENGTH).
	Name string

	Str       uint8
	Agi       uint8
	Vit       uint8
	Int       uint8
	Dex       uint8
	Luk       uint8
	CharNum   uint8
	HairColor uint8

	IsChangedCharName int16

	// MapName is zero-padded ASCII on the wire to fill a 16-byte slot
	// (MAP_NAME_LENGTH_EXT).
	MapName string

	DelRevDate       int32
	RobePalette      int32
	ChrSlotChangeCnt int32
	ChrNameChangeCnt int32

	Sex uint8
}

// Size returns the on-wire byte length that Encode will write (always 175).
func (c CharacterInfo) Size() int {
	return CharacterInfoSize
}

// Encode writes one packed CHARACTER_INFO entry to w. Returns a wrapped
// error (sentinel + %w) if Name exceeds 24 bytes or MapName exceeds 16
// bytes; in that case no bytes are written to w.
//
// delegates the field-by-field serialization to encodeInto so the
// HC_ACCEPT_ENTER loop and the standalone Encode stay byte-for-byte
// identical.
func (c CharacterInfo) Encode(w io.Writer) error {
	if err := c.validate(); err != nil {
		return err
	}
	buf := make([]byte, CharacterInfoSize)
	if err := c.encodeInto(buf[:0]); err != nil {
		return err
	}
	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write CHARACTER_INFO: %w", err)
	}
	return nil
}

func (c CharacterInfo) validate() error {
	if len(c.Name) > nameSlot {
		return fmt.Errorf("packet: encode CHARACTER_INFO: %w", ErrCharNameTooLong)
	}
	if len(c.MapName) > charMapNameSlot {
		return fmt.Errorf("packet: encode CHARACTER_INFO: %w", ErrMapNameTooLong)
	}
	return nil
}
