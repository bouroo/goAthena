package packet

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Sentinel errors returned by the char-server encoders when a string field
// does not fit its fixed-width on-wire slot. Use errors.Is to detect them.
var (
	// ErrMapNameTooLong is returned when a mapname exceeds 16 bytes
	// (MAP_NAME_LENGTH_EXT; mmo.hpp:164-165).
	ErrMapNameTooLong = errors.New("packet: mapname exceeds 16 bytes")
	// ErrDomainTooLong is returned when a domain exceeds 128 bytes
	// (HC_NOTIFY_ZONESVR domain[] slot).
	ErrDomainTooLong = errors.New("packet: domain exceeds 128 bytes")
	// ErrExtensionTooLong is returned when the HC_ACCEPT_ENTER extension
	// slot exceeds 20 bytes (packets.hpp:240-251).
	ErrExtensionTooLong = errors.New("packet: extension exceeds 20 bytes")
)

// RefuseEnterResponse encodes an HC_REFUSE_ENTER packet (command 0x006c).
// Layout source: rathena/src/common/packets.hpp:253-257.
//
// Fixed wire length: 3 bytes (int16 packetType + uint8 error).
type RefuseEnterResponse struct {
	// Error is the 8-bit error code (rAthena's REFUSE_ENTER_* enum).
	Error uint8
}

// Size returns the on-wire byte length that Encode will write (always 3).
func (r RefuseEnterResponse) Size() int {
	return sizeHCRefuseEnter
}

// Encode writes the HC_REFUSE_ENTER packet to w.
func (r RefuseEnterResponse) Encode(w io.Writer) error {
	buf := make([]byte, sizeHCRefuseEnter)
	binary.LittleEndian.PutUint16(buf[0:], HeaderHCREFUSEENTER)
	buf[2] = r.Error

	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write HC_REFUSE_ENTER: %w", err)
	}
	return nil
}

// NotifyZoneServerResponse encodes an HC_NOTIFY_ZONESVR packet (command
// 0x0ac5, used for PACKETVER >= 20170315). Layout source:
// rathena/src/common/packets.hpp:290-299.
//
// Fixed wire length: 156 bytes (2 + 4 + 16 + 4 + 2 + 128).
type NotifyZoneServerResponse struct {
	// CID is the character's account ID to which the zone will be told the
	// character belongs.
	CID uint32
	// MapName is the zone-side map name (zero-padded ASCII on the wire to
	// fill a 16-byte slot — MAP_NAME_LENGTH_EXT).
	MapName string
	// IP is the zone server's IPv4 address in network byte order, stored as
	// a uint32 (the most-significant byte of the original IPv4 octet set is
	// the highest byte of the uint32). The encoder writes these bytes to
	// the wire verbatim using binary.LittleEndian.PutUint32 — match the
	// convention used by encode.go LastIP/IP for AC_ACCEPT_LOGIN.
	IP uint32
	// Port is the zone server's TCP port.
	Port uint16
	// Domain is the zone-server DNS name or empty string (zero-padded ASCII
	// on the wire to fill a 128-byte slot).
	Domain string
}

// Size returns the on-wire byte length that Encode will write (always 156).
func (r NotifyZoneServerResponse) Size() int {
	return sizeHCNotifyZone
}

// Encode writes the HC_NOTIFY_ZONESVR packet to w. Returns a wrapped error
// (sentinel + %w) if MapName exceeds 16 bytes or Domain exceeds 128 bytes;
// in that case no bytes are written to w.
func (r NotifyZoneServerResponse) Encode(w io.Writer) error {
	if err := r.validate(); err != nil {
		return err
	}

	buf := make([]byte, sizeHCNotifyZone)
	// int16 packetType = 0x0ac5 (HeaderHCNOTIFYZONESVR).
	binary.LittleEndian.PutUint16(buf[0:], HeaderHCNOTIFYZONESVR)
	// uint32 CID at offset 2.
	binary.LittleEndian.PutUint32(buf[2:], r.CID)
	// char mapname[16] at offset 6 — zero-padded.
	writeFixedString(buf[6:6+mapNameExtSlot], r.MapName)
	// uint32 ip at offset 22 (2+4+16) — network-order bytes written
	// verbatim (LE PutUint32 preserves the byte order in the uint32
	// itself, matching the encode.go LastIP/IP convention).
	binary.LittleEndian.PutUint32(buf[22:], r.IP)
	// uint16 port at offset 26.
	binary.LittleEndian.PutUint16(buf[26:], r.Port)
	// char domain[128] at offset 28 — zero-padded.
	writeFixedString(buf[28:28+domainSlot], r.Domain)

	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write HC_NOTIFY_ZONESVR: %w", err)
	}
	return nil
}

func (r NotifyZoneServerResponse) validate() error {
	if len(r.MapName) > mapNameExtSlot {
		return fmt.Errorf("packet: encode HC_NOTIFY_ZONESVR: %w", ErrMapNameTooLong)
	}
	if len(r.Domain) > domainSlot {
		return fmt.Errorf("packet: encode HC_NOTIFY_ZONESVR: %w", ErrDomainTooLong)
	}
	return nil
}

// AcceptEnterResponse encodes an HC_ACCEPT_ENTER packet (command 0x006b,
// used for PACKETVER >= 20100413). Layout source:
//
//	rathena/src/common/packets.hpp:240-251.
//
// Wire layout: a 27-byte fixed prefix (int16 packetType + int16 packetLength
// + uint8 total + uint8 premiumStart + uint8 premiumEnd + char extension[20])
// followed by N × 175-byte CHARACTER_INFO entries. Total wire length =
// 27 + 175*len(Characters) bytes.
type AcceptEnterResponse struct {
	// Total is the maximum number of character slots available to the
	// account (mirrors rAthena's `total` slot field; the client displays
	// this as "Slots: N" in the character select screen).
	Total uint8
	// PremiumStart is the start-slot index for premium-only slots.
	PremiumStart uint8
	// PremiumEnd is the end-slot index for premium-only slots.
	PremiumEnd uint8
	// Extension is a 20-byte zero-padded slot for forward-compatibility
	// fields (rathena's `extension[20]`). Empty string fills with zeros.
	Extension string
	// Characters is the trailing flexible array of CHARACTER_INFO entries.
	Characters []CharacterInfo
}

// Size returns the total on-wire byte length that Encode will write.
func (r AcceptEnterResponse) Size() int {
	return acceptEnterHeaderSize + CharacterInfoSize*len(r.Characters)
}

// Encode writes the HC_ACCEPT_ENTER packet to w. Returns a wrapped error
// (sentinel + %w) if Extension exceeds 20 bytes or any CharacterInfo's
// Name/MapName overflows their slot; in that case no bytes are written to w.
func (r AcceptEnterResponse) Encode(w io.Writer) error {
	if err := r.validate(); err != nil {
		return err
	}

	buf := make([]byte, r.Size())
	pos := 0

	// int16 packetType = 0x006b (HeaderHCACCEPTENTER).
	binary.LittleEndian.PutUint16(buf[pos:], HeaderHCACCEPTENTER)
	pos += 2
	// int16 packetLength = total wire length (validated by validate).
	binary.LittleEndian.PutUint16(buf[pos:], uint16(r.Size())) //nolint:gosec // validated by r.validate()
	pos += 2
	// uint8 total.
	buf[pos] = r.Total
	pos++
	// uint8 premiumStart.
	buf[pos] = r.PremiumStart
	pos++
	// uint8 premiumEnd.
	buf[pos] = r.PremiumEnd
	pos++
	// char extension[20] — zero-padded.
	writeFixedString(buf[pos:pos+acceptEnterExtensionSlot], r.Extension)
	pos += acceptEnterExtensionSlot

	// Trailing CHARACTER_INFO[] flexible array.
	for i, ch := range r.Characters {
		var charBuf [CharacterInfoSize]byte
		if err := ch.encodeInto(charBuf[:0]); err != nil {
			return fmt.Errorf("packet: encode HC_ACCEPT_ENTER: characters[%d]: %w", i, err)
		}
		copy(buf[pos:], charBuf[:])
		pos += CharacterInfoSize
	}

	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write HC_ACCEPT_ENTER: %w", err)
	}
	return nil
}

// encodeInto writes the CHARACTER_INFO into dst, returning the sentinel error
// from validate without writing anything if a string field overflows. Used by
// AcceptEnterResponse.Encode so a partial header is never written when an
// entry is malformed.
func (c CharacterInfo) encodeInto(dst []byte) error {
	if err := c.validate(); err != nil {
		return err
	}

	buf := dst[:CharacterInfoSize]
	pos := 0

	binary.LittleEndian.PutUint32(buf[pos:], c.GID)
	pos += 4
	binary.LittleEndian.PutUint64(buf[pos:], uint64(c.Exp)) //nolint:gosec // wire is 64-bit, sign-preserving via two's complement reinterpret
	pos += 8
	binary.LittleEndian.PutUint32(buf[pos:], uint32(c.Money)) //nolint:gosec // wire is 32-bit, sign-preserving via two's complement reinterpret
	pos += 4
	binary.LittleEndian.PutUint64(buf[pos:], uint64(c.JobExp)) //nolint:gosec // wire is 64-bit
	pos += 8
	binary.LittleEndian.PutUint32(buf[pos:], uint32(c.JobLevel)) //nolint:gosec // wire is 32-bit
	pos += 4
	binary.LittleEndian.PutUint32(buf[pos:], uint32(c.BodyState)) //nolint:gosec // wire is 32-bit
	pos += 4
	binary.LittleEndian.PutUint32(buf[pos:], uint32(c.HealthState)) //nolint:gosec // wire is 32-bit
	pos += 4
	binary.LittleEndian.PutUint32(buf[pos:], uint32(c.EffectState)) //nolint:gosec // wire is 32-bit
	pos += 4
	binary.LittleEndian.PutUint32(buf[pos:], uint32(c.Virtue)) //nolint:gosec // wire is 32-bit
	pos += 4
	binary.LittleEndian.PutUint32(buf[pos:], uint32(c.Honor)) //nolint:gosec // wire is 32-bit
	pos += 4
	binary.LittleEndian.PutUint16(buf[pos:], uint16(c.JobPoint)) //nolint:gosec // wire is 16-bit
	pos += 2
	binary.LittleEndian.PutUint64(buf[pos:], uint64(c.HP)) //nolint:gosec // wire is 64-bit
	pos += 8
	binary.LittleEndian.PutUint64(buf[pos:], uint64(c.MaxHP)) //nolint:gosec // wire is 64-bit
	pos += 8
	binary.LittleEndian.PutUint64(buf[pos:], uint64(c.SP)) //nolint:gosec // wire is 64-bit
	pos += 8
	binary.LittleEndian.PutUint64(buf[pos:], uint64(c.MaxSP)) //nolint:gosec // wire is 64-bit
	pos += 8
	binary.LittleEndian.PutUint16(buf[pos:], uint16(c.Speed)) //nolint:gosec // wire is 16-bit
	pos += 2
	binary.LittleEndian.PutUint16(buf[pos:], uint16(c.Job)) //nolint:gosec // wire is 16-bit
	pos += 2
	binary.LittleEndian.PutUint16(buf[pos:], uint16(c.Head)) //nolint:gosec // wire is 16-bit
	pos += 2
	binary.LittleEndian.PutUint16(buf[pos:], uint16(c.Body)) //nolint:gosec // wire is 16-bit
	pos += 2
	binary.LittleEndian.PutUint16(buf[pos:], uint16(c.Weapon)) //nolint:gosec // wire is 16-bit
	pos += 2
	binary.LittleEndian.PutUint16(buf[pos:], uint16(c.Level)) //nolint:gosec // wire is 16-bit
	pos += 2
	binary.LittleEndian.PutUint16(buf[pos:], uint16(c.SPPoint)) //nolint:gosec // wire is 16-bit
	pos += 2
	binary.LittleEndian.PutUint16(buf[pos:], uint16(c.Accessory)) //nolint:gosec // wire is 16-bit
	pos += 2
	binary.LittleEndian.PutUint16(buf[pos:], uint16(c.Shield)) //nolint:gosec // wire is 16-bit
	pos += 2
	binary.LittleEndian.PutUint16(buf[pos:], uint16(c.Accessory2)) //nolint:gosec // wire is 16-bit
	pos += 2
	binary.LittleEndian.PutUint16(buf[pos:], uint16(c.Accessory3)) //nolint:gosec // wire is 16-bit
	pos += 2
	binary.LittleEndian.PutUint16(buf[pos:], uint16(c.HeadPalette)) //nolint:gosec // wire is 16-bit
	pos += 2
	binary.LittleEndian.PutUint16(buf[pos:], uint16(c.BodyPalette)) //nolint:gosec // wire is 16-bit
	pos += 2
	writeFixedString(buf[pos:pos+nameSlot], c.Name)
	pos += nameSlot
	buf[pos] = c.Str
	pos++
	buf[pos] = c.Agi
	pos++
	buf[pos] = c.Vit
	pos++
	buf[pos] = c.Int
	pos++
	buf[pos] = c.Dex
	pos++
	buf[pos] = c.Luk
	pos++
	buf[pos] = c.CharNum
	pos++
	buf[pos] = c.HairColor
	pos++
	binary.LittleEndian.PutUint16(buf[pos:], uint16(c.IsChangedCharName)) //nolint:gosec // wire is 16-bit
	pos += 2
	writeFixedString(buf[pos:pos+charMapNameSlot], c.MapName)
	pos += charMapNameSlot
	binary.LittleEndian.PutUint32(buf[pos:], uint32(c.DelRevDate)) //nolint:gosec // wire is 32-bit
	pos += 4
	binary.LittleEndian.PutUint32(buf[pos:], uint32(c.RobePalette)) //nolint:gosec // wire is 32-bit
	pos += 4
	binary.LittleEndian.PutUint32(buf[pos:], uint32(c.ChrSlotChangeCnt)) //nolint:gosec // wire is 32-bit
	pos += 4
	binary.LittleEndian.PutUint32(buf[pos:], uint32(c.ChrNameChangeCnt)) //nolint:gosec // wire is 32-bit
	pos += 4
	buf[pos] = c.Sex

	return nil
}

func (r AcceptEnterResponse) validate() error {
	if len(r.Extension) > acceptEnterExtensionSlot {
		return fmt.Errorf("packet: encode HC_ACCEPT_ENTER: %w", ErrExtensionTooLong)
	}
	for i, ch := range r.Characters {
		if err := ch.validate(); err != nil {
			return fmt.Errorf("packet: encode HC_ACCEPT_ENTER: characters[%d]: %w", i, err)
		}
	}
	return nil
}
