package packet

import (
	"bytes"
	"encoding/binary"
	"fmt"
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
// exactly 55 bytes.
func ParseCALogin(frame []byte) (CALoginRequest, error) {
	if len(frame) != sizeCALogin {
		return CALoginRequest{}, fmt.Errorf("packet: parse CA_LOGIN: want %d bytes, got %d", sizeCALogin, len(frame))
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
