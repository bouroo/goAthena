package packet

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// On-wire slot widths for the fixed-width char fields of the modern
// (PACKETVER >= 20170315) login-server response packets. Source:
// rathena/src/common/packets.hpp:175-238 and rathena/src/common/mmo.hpp:121
// (WEB_AUTH_TOKEN_LENGTH = 17).
const (
	// acceptHeaderSize is the byte length of the fixed prefix of
	// PACKET_AC_ACCEPT_LOGIN preceding the char_servers[] flexible array.
	// int16 packetType + int16 packetLength + uint32*4 + char[26] + uint8 +
	// char[17] = 2+2+4+4+4+4+26+1+17 = 64.
	acceptHeaderSize = 64
	// acceptCharServerSize is the on-wire byte length of a single
	// PACKET_AC_ACCEPT_LOGIN_sub element (ip:4 + port:2 + name:20 +
	// users:2 + type:2 + new_:2 + unknown:128 = 160).
	acceptCharServerSize = 160
	// refuseLoginSize is the on-wire byte length of PACKET_AC_REFUSE_LOGIN
	// (PACKETVER >= 20120000): int16 packetType + uint32 error + char[20]
	// unblock_time = 2+4+20 = 26.
	refuseLoginSize = 26

	// lastLoginSlot is the fixed byte width of the last_login[26] field.
	lastLoginSlot = 26
	// tokenSlot is the fixed byte width of the token[17] field
	// (WEB_AUTH_TOKEN_LENGTH = 17).
	tokenSlot = 17
	// charServerNameSlot is the fixed byte width of the char server name[20]
	// field in PACKET_AC_ACCEPT_LOGIN_sub.
	charServerNameSlot = 20
	// unblockTimeSlot is the fixed byte width of the unblock_time[20] field
	// in PACKET_AC_REFUSE_LOGIN.
	unblockTimeSlot = 20
)

// maxCharServers caps the trailing flexible array so the uint16 packetLength
// field stays representable: (65535 - acceptHeaderSize) / acceptCharServerSize.
const maxCharServers = (1<<16 - acceptHeaderSize) / acceptCharServerSize

// Sentinel errors returned by the encoder when a string field does not fit
// its fixed-width on-wire slot. Use errors.Is to detect them.
var (
	// ErrLastLoginTooLong is returned when last_login exceeds 26 bytes
	// (UTF-8 length, not rune count — the wire slot is a raw byte array).
	ErrLastLoginTooLong = errors.New("packet: last_login exceeds 26 bytes")
	// ErrTokenTooLong is returned when token exceeds 17 bytes
	// (WEB_AUTH_TOKEN_LENGTH).
	ErrTokenTooLong = errors.New("packet: token exceeds 17 bytes")
	// ErrCharServerNameTooLong is returned when a char server name exceeds
	// 20 bytes.
	ErrCharServerNameTooLong = errors.New("packet: char server name exceeds 20 bytes")
	// ErrUnblockTimeTooLong is returned when unblock_time exceeds 20 bytes.
	ErrUnblockTimeTooLong = errors.New("packet: unblock_time exceeds 20 bytes")
)

// CharServer describes a single char-server entry in PACKET_AC_ACCEPT_LOGIN_sub.
//
// Field layout (rathena/src/common/packets.hpp:176-184, packed struct, 160
// bytes on the wire): ip(4) + port(2) + name[20](20) + users(2) + type(2) +
// new_(2) + unknown[128](128).
type CharServer struct {
	// IP is the char server's IPv4 address in network byte order (already
	// in big-endian uint32 form, as rAthena stores it — rathena/src/login/
	// loginclif.cpp builds this from inet_addr / htonl).
	IP uint32
	// Port is the char server's TCP port.
	Port uint16
	// Name is a short display name. Must fit in 20 bytes on the wire.
	Name string
	// Users is the current player count.
	Users uint16
	// Type is the char server type flag (e.g. 0x0 normal, others per
	// rathena's server type registry).
	Type uint16
	// New is the "new" flag (rathena struct field is `new_`; Go forbids the
	// underscore suffix per revive's var-naming rule, and the exported
	// identifier `New` does not collide with the builtin `new` function
	// because Go identifiers are case-sensitive).
	New uint16
}

// AcceptLoginResponse encodes a modern (PACKETVER >= 20170315) AC_ACCEPT_LOGIN
// packet (command 0x0ac4). Layout source:
//
//	rathena/src/common/packets.hpp:186-198 (PACKET_AC_ACCEPT_LOGIN).
//
// Total wire length = 64 + 160*len(CharServers) bytes.
type AcceptLoginResponse struct {
	// LoginID1 is the upper 32 bits of the session token.
	LoginID1 uint32
	// AID is the account ID.
	AID uint32
	// LoginID2 is the lower 32 bits of the session token.
	LoginID2 uint32
	// LastIP is the last login IPv4 address in network byte order.
	LastIP uint32
	// LastLogin is the last login name (zero-padded ASCII on the wire;
	// truncated to 26 bytes — call sites MUST validate via Encode errors).
	LastLogin string
	// Sex is the account sex byte (0x0 female, 0x1 male in kRO).
	Sex uint8
	// Token is the WEB_AUTH_TOKEN (zero-padded on the wire; empty string
	// fills the slot with zero bytes).
	Token string
	// CharServers is the trailing flexible-array of char-server entries.
	CharServers []CharServer
}

// Size returns the total on-wire byte length that Encode will write.
func (r AcceptLoginResponse) Size() int {
	return acceptHeaderSize + acceptCharServerSize*len(r.CharServers)
}

// Encode writes the AC_ACCEPT_LOGIN packet to w. Returns a wrapped error
// (sentinel + %w) if any string field overflows its fixed-width slot; in
// that case no bytes are written to w.
func (r AcceptLoginResponse) Encode(w io.Writer) error {
	if err := r.validate(); err != nil {
		return err
	}

	buf := make([]byte, r.Size())
	pos := 0

	// int16 packetType = 0x0ac4 (HeaderACACCEPTLOGIN).
	binary.LittleEndian.PutUint16(buf[pos:], HeaderACACCEPTLOGIN)
	pos += 2
	// int16 packetLength = total wire length (guaranteed in-range by validate).
	binary.LittleEndian.PutUint16(buf[pos:], uint16(r.Size())) //nolint:gosec // validated by r.validate()
	pos += 2
	// uint32 login_id1.
	binary.LittleEndian.PutUint32(buf[pos:], r.LoginID1)
	pos += 4
	// uint32 AID.
	binary.LittleEndian.PutUint32(buf[pos:], r.AID)
	pos += 4
	// uint32 login_id2.
	binary.LittleEndian.PutUint32(buf[pos:], r.LoginID2)
	pos += 4
	// uint32 last_ip.
	binary.LittleEndian.PutUint32(buf[pos:], r.LastIP)
	pos += 4
	// char last_login[26] — zero-padded.
	writeFixedString(buf[pos:pos+lastLoginSlot], r.LastLogin)
	pos += lastLoginSlot
	// uint8 sex.
	buf[pos] = r.Sex
	pos++
	// char token[17] — zero-padded.
	writeFixedString(buf[pos:pos+tokenSlot], r.Token)
	pos += tokenSlot

	// PACKET_AC_ACCEPT_LOGIN_sub char_servers[] — already zero-filled by
	// make([]byte, ...); write the live fields into each 160-byte slot.
	for _, cs := range r.CharServers {
		binary.LittleEndian.PutUint32(buf[pos:], cs.IP)
		pos += 4
		binary.LittleEndian.PutUint16(buf[pos:], cs.Port)
		pos += 2
		writeFixedString(buf[pos:pos+charServerNameSlot], cs.Name)
		pos += charServerNameSlot
		binary.LittleEndian.PutUint16(buf[pos:], cs.Users)
		pos += 2
		binary.LittleEndian.PutUint16(buf[pos:], cs.Type)
		pos += 2
		binary.LittleEndian.PutUint16(buf[pos:], cs.New)
		pos += 2
		pos += 128 // unknown[128] — already zero.
	}

	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write AC_ACCEPT_LOGIN: %w", err)
	}
	return nil
}

func (r AcceptLoginResponse) validate() error {
	if len(r.CharServers) > maxCharServers {
		return fmt.Errorf("packet: encode AC_ACCEPT_LOGIN: %d char servers exceeds wire-length limit (%d)", len(r.CharServers), maxCharServers)
	}
	if len(r.LastLogin) > lastLoginSlot {
		return fmt.Errorf("packet: encode AC_ACCEPT_LOGIN: %w", ErrLastLoginTooLong)
	}
	if len(r.Token) > tokenSlot {
		return fmt.Errorf("packet: encode AC_ACCEPT_LOGIN: %w", ErrTokenTooLong)
	}
	for i, cs := range r.CharServers {
		if len(cs.Name) > charServerNameSlot {
			return fmt.Errorf("packet: encode AC_ACCEPT_LOGIN: char_servers[%d]: %w", i, ErrCharServerNameTooLong)
		}
	}
	return nil
}

// RefuseLoginResponse encodes a modern (PACKETVER >= 20120000) AC_REFUSE_LOGIN
// packet (command 0x083e). Layout source:
//
//	rathena/src/common/packets.hpp:225-230 (PACKET_AC_REFUSE_LOGIN).
//
// Fixed wire length: 26 bytes.
type RefuseLoginResponse struct {
	// Error is the 32-bit error code (packets.hpp:225; rAthena's
	// REFUSE_* enum values are 32-bit, unlike the legacy 8-bit form).
	Error uint32
	// UnblockTime is the unblock-time string (typically ASCII digits or
	// empty). Zero-padded on the wire to fill the 20-byte slot.
	UnblockTime string
}

// Size returns the on-wire byte length that Encode will write (always 26).
func (r RefuseLoginResponse) Size() int {
	return refuseLoginSize
}

// Encode writes the AC_REFUSE_LOGIN packet to w. Returns a wrapped error if
// UnblockTime exceeds 20 bytes; in that case no bytes are written to w.
func (r RefuseLoginResponse) Encode(w io.Writer) error {
	if err := r.validate(); err != nil {
		return err
	}

	buf := make([]byte, refuseLoginSize)
	// int16 packetType = 0x083e (HeaderACREFUSELOGIN).
	binary.LittleEndian.PutUint16(buf[0:], HeaderACREFUSELOGIN)
	// uint32 error.
	binary.LittleEndian.PutUint32(buf[2:], r.Error)
	// char unblock_time[20] — zero-padded.
	writeFixedString(buf[6:6+unblockTimeSlot], r.UnblockTime)

	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write AC_REFUSE_LOGIN: %w", err)
	}
	return nil
}

func (r RefuseLoginResponse) validate() error {
	if len(r.UnblockTime) > unblockTimeSlot {
		return fmt.Errorf("packet: encode AC_REFUSE_LOGIN: %w", ErrUnblockTimeTooLong)
	}
	return nil
}

// writeFixedString copies src into dst and zero-pads the tail. It assumes
// len(src) <= len(dst) — callers must validate lengths first. This matches
// rAthena's memcpy-with-trailing-zero-fill behavior for packed char[N] arrays.
func writeFixedString(dst []byte, src string) {
	copy(dst, src)
	clear(dst[len(src):])
}
