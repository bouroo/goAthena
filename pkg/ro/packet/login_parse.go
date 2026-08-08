package packet

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// caLoginUsernameSlot is the fixed on-wire byte width of the username[24]
// field in CA_LOGIN (rathena NAME_LENGTH = 24, mmo.hpp:154).
const caLoginUsernameSlot = 24

// caLoginPasswordSlot is the fixed on-wire byte width of the password[24]
// field in CA_LOGIN.
const caLoginPasswordSlot = 24

// Sentinel errors returned by CALoginRequest.Encode when a string field does
// not fit its fixed-width on-wire slot. Use errors.Is to detect them.
var (
	// ErrCALoginUsernameTooLong is returned when CALoginRequest.Username
	// exceeds 24 bytes (NAME_LENGTH).
	ErrCALoginUsernameTooLong = errors.New("packet: CA_LOGIN username exceeds 24 bytes")
	// ErrCALoginPasswordTooLong is returned when CALoginRequest.Password
	// exceeds 24 bytes.
	ErrCALoginPasswordTooLong = errors.New("packet: CA_LOGIN password exceeds 24 bytes")
)

// CALoginRequest is the decoded form of a client→login-server CA_LOGIN
// packet (header 0x0064, 55 bytes on the wire). Source: rathena/src/common/
// packets.hpp PACKET_CA_LOGIN and rathena/src/login/loginclif.cpp.
type CALoginRequest struct {
	// Version is the client's PACKETVER-encoded version (little-endian uint32).
	Version uint32
	// Username is the account name, NUL-trimmed from a 24-byte slot.
	Username string
	// Password is the account password, NUL-trimmed from a 24-byte slot.
	Password string
	// ClientType is the client type byte (0x0 kRO, 0x1 kRO, etc., per rAthena's
	// clienttype registry).
	ClientType uint8
}

// ParseCALogin parses a full 55-byte CA_LOGIN frame (including the 2-byte cmd
// header) into a CALoginRequest. Returns a wrapped error if the frame is not
// exactly 55 bytes or its cmd header is not HeaderCALOGIN (0x0064).
func ParseCALogin(frame []byte) (CALoginRequest, error) {
	if len(frame) != sizeCALogin {
		return CALoginRequest{}, fmt.Errorf("packet: parse CA_LOGIN: want %d bytes, got %d", sizeCALogin, len(frame))
	}
	if cmd := binary.LittleEndian.Uint16(frame[0:2]); cmd != HeaderCALOGIN {
		return CALoginRequest{}, fmt.Errorf("packet: parse CA_LOGIN: unexpected cmd 0x%04x", cmd)
	}

	return CALoginRequest{
		Version:    binary.LittleEndian.Uint32(frame[2:6]),
		Username:   cstr(frame[6:30]),
		Password:   cstr(frame[30:54]),
		ClientType: frame[54],
	}, nil
}

// cstr returns the NUL-terminated prefix of b as a string. If b contains no
// NUL byte, the entire slice is returned (matching rAthena's behavior of
// reading the full fixed-width slot when the field is not NUL-terminated).
func cstr(b []byte) string {
	prefix, _, ok := bytes.Cut(b, []byte{0})
	if ok {
		return string(prefix)
	}
	return string(b)
}

// Encode writes the CA_LOGIN packet to w, mirroring the on-wire layout
// documented on CALoginRequest: [2:cmd=0x0064][4:version][24:username]
// [24:password][1:clientType] = 55 bytes. Source: rathena/src/common/
// packets.hpp PACKET_CA_LOGIN and rathena/src/login/loginclif.cpp.
//
// Returns a wrapped error (sentinel + %w) if Username exceeds 24 bytes or
// Password exceeds 24 bytes; in that case no bytes are written to w.
func (r CALoginRequest) Encode(w io.Writer) error {
	if err := r.validate(); err != nil {
		return err
	}

	buf := make([]byte, sizeCALogin)
	// int16 packetType = 0x0064 (HeaderCALOGIN).
	binary.LittleEndian.PutUint16(buf[0:], HeaderCALOGIN)
	// uint32 version at offset 2.
	binary.LittleEndian.PutUint32(buf[2:], r.Version)
	// char username[24] at offset 6 — zero-padded.
	writeFixedString(buf[6:6+caLoginUsernameSlot], r.Username)
	// char password[24] at offset 30 — zero-padded.
	writeFixedString(buf[30:30+caLoginPasswordSlot], r.Password)
	// uint8 client_type at offset 54.
	buf[54] = r.ClientType

	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write CA_LOGIN: %w", err)
	}
	return nil
}

func (r CALoginRequest) validate() error {
	if len(r.Username) > caLoginUsernameSlot {
		return fmt.Errorf("packet: encode CA_LOGIN: %w", ErrCALoginUsernameTooLong)
	}
	if len(r.Password) > caLoginPasswordSlot {
		return fmt.Errorf("packet: encode CA_LOGIN: %w", ErrCALoginPasswordTooLong)
	}
	return nil
}
