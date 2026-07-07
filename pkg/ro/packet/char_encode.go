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
