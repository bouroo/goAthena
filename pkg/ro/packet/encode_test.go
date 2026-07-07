//go:build unit

package packet

import (
	"bytes"
	"errors"
	"testing"
)

// acceptHeaderBytes constructs the expected 64-byte AC_ACCEPT_LOGIN prefix
// for tests. lastLogin and token are written verbatim and zero-padded on the
// right; pass shorter strings to assert zero-fill behavior, or a 26/17-byte
// string to assert exact-fit behavior.
func acceptHeaderBytes(loginID1, aid, loginID2, lastIP uint32, lastLogin string, sex uint8, token string) []byte {
	out := make([]byte, acceptHeaderSize)
	pos := 0
	writeLE16(out[pos:], HeaderACACCEPTLOGIN)
	pos += 2
	// packetLength is filled in by the caller (depends on char-server count).
	pos += 2
	writeLE32(out[pos:], loginID1)
	pos += 4
	writeLE32(out[pos:], aid)
	pos += 4
	writeLE32(out[pos:], loginID2)
	pos += 4
	writeLE32(out[pos:], lastIP)
	pos += 4
	writeFixedString(out[pos:pos+lastLoginSlot], lastLogin)
	pos += lastLoginSlot
	out[pos] = sex
	pos++
	writeFixedString(out[pos:pos+tokenSlot], token)
	pos += tokenSlot
	_ = pos
	return out
}

// charServerBytes constructs one 160-byte PACKET_AC_ACCEPT_LOGIN_sub entry.
func charServerBytes(ip uint32, port uint16, name string, users, typ, newf uint16) []byte {
	out := make([]byte, acceptCharServerSize)
	pos := 0
	writeLE32(out[pos:], ip)
	pos += 4
	writeLE16(out[pos:], port)
	pos += 2
	writeFixedString(out[pos:pos+charServerNameSlot], name)
	pos += charServerNameSlot
	writeLE16(out[pos:], users)
	pos += 2
	writeLE16(out[pos:], typ)
	pos += 2
	writeLE16(out[pos:], newf)
	pos += 2
	// unknown[128] is already zero.
	return out
}

func writeLE16(b []byte, v uint16) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
}

func writeLE32(b []byte, v uint32) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
}

func TestAcceptLoginResponse_Encode_ZeroCharServers(t *testing.T) {
	t.Parallel()

	resp := AcceptLoginResponse{
		LoginID1:  0x11223344,
		AID:       0xaabbccdd,
		LoginID2:  0x55667788,
		LastIP:    0xc0a80101, // 192.168.1.1
		LastLogin: "alice",
		Sex:       1,
		Token:     "tok",
	}

	var buf bytes.Buffer
	if err := resp.Encode(&buf); err != nil {
		t.Fatalf("Encode err = %v, want nil", err)
	}
	got := buf.Bytes()

	wantLen := acceptHeaderSize // 64
	if len(got) != wantLen {
		t.Fatalf("len = %d, want %d", len(got), wantLen)
	}

	want := acceptHeaderBytes(resp.LoginID1, resp.AID, resp.LoginID2, resp.LastIP, resp.LastLogin, resp.Sex, resp.Token)
	// patch in the packetLength (= total length for 0 char servers = 64).
	writeLE16(want[2:], uint16(wantLen))

	if !bytes.Equal(got, want) {
		t.Errorf("bytes mismatch:\n got=% x\nwant=% x", got, want)
	}
}

func TestAcceptLoginResponse_Encode_OneCharServer(t *testing.T) {
	t.Parallel()

	cs := CharServer{
		IP:    0x0a0b0c0d,
		Port:  6121,
		Name:  "char-1",
		Users: 42,
		Type:  0x1,
		New:   0x0,
	}
	resp := AcceptLoginResponse{
		LoginID1:    0xdeadbeef,
		AID:         0x00000007,
		LoginID2:    0xfeedface,
		LastIP:      0x7f000001, // 127.0.0.1
		LastLogin:   "bob",
		Sex:         0,
		Token:       "abcdefghijklmnopq", // exactly 17 bytes (slot-fills)
		CharServers: []CharServer{cs},
	}

	var buf bytes.Buffer
	if err := resp.Encode(&buf); err != nil {
		t.Fatalf("Encode err = %v, want nil", err)
	}
	got := buf.Bytes()

	wantLen := acceptHeaderSize + acceptCharServerSize // 64 + 160 = 224
	if len(got) != wantLen {
		t.Fatalf("len = %d, want %d", len(got), wantLen)
	}

	want := acceptHeaderBytes(resp.LoginID1, resp.AID, resp.LoginID2, resp.LastIP, resp.LastLogin, resp.Sex, resp.Token)
	writeLE16(want[2:], uint16(wantLen))
	want = append(want, charServerBytes(cs.IP, cs.Port, cs.Name, cs.Users, cs.Type, cs.New)...)

	if !bytes.Equal(got, want) {
		t.Errorf("bytes mismatch:\n got=% x\nwant=% x", got, want)
	}

	// Spot-check the spec invariants at exact offsets.
	if got[0] != 0xc4 || got[1] != 0x0a {
		t.Errorf("header bytes = %02x %02x, want c4 0a", got[0], got[1])
	}
	if got[2] != 0xe0 || got[3] != 0x00 { // 224 = 0x00e0 little-endian
		t.Errorf("packetLength bytes = %02x %02x, want e0 00", got[2], got[3])
	}
}

func TestAcceptLoginResponse_Encode_TwoCharServers(t *testing.T) {
	t.Parallel()

	resp := AcceptLoginResponse{
		LoginID1:  1,
		AID:       2,
		LoginID2:  3,
		LastIP:    4,
		LastLogin: "",
		Sex:       1,
		Token:     "", // exercises the zero-fill path for the 17-byte token slot.
		CharServers: []CharServer{
			{IP: 0x01020304, Port: 7000, Name: "alpha", Users: 10, Type: 0, New: 1},
			{IP: 0x05060708, Port: 7001, Name: "beta", Users: 20, Type: 1, New: 0},
		},
	}

	var buf bytes.Buffer
	if err := resp.Encode(&buf); err != nil {
		t.Fatalf("Encode err = %v, want nil", err)
	}
	got := buf.Bytes()

	wantLen := acceptHeaderSize + 2*acceptCharServerSize // 64 + 320 = 384
	if len(got) != wantLen {
		t.Fatalf("len = %d, want %d", len(got), wantLen)
	}

	want := acceptHeaderBytes(resp.LoginID1, resp.AID, resp.LoginID2, resp.LastIP, resp.LastLogin, resp.Sex, resp.Token)
	writeLE16(want[2:], uint16(wantLen))
	for _, cs := range resp.CharServers {
		want = append(want, charServerBytes(cs.IP, cs.Port, cs.Name, cs.Users, cs.Type, cs.New)...)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("bytes mismatch:\n got=% x\nwant=% x", got, want)
	}

	// Assert the 17-byte token slot is present and zero-filled when empty.
	tokenSlotStart := acceptHeaderSize - tokenSlot
	for i := 0; i < tokenSlot; i++ {
		if got[tokenSlotStart+i] != 0 {
			t.Errorf("token slot byte %d = 0x%02x, want 0x00 (zero-fill when empty)", i, got[tokenSlotStart+i])
		}
	}
	// And the last_login[26] slot is zero-filled too.
	lastLoginSlotStart := acceptHeaderSize - tokenSlot - 1 - lastLoginSlot
	for i := 0; i < lastLoginSlot; i++ {
		if got[lastLoginSlotStart+i] != 0 {
			t.Errorf("last_login slot byte %d = 0x%02x, want 0x00", i, got[lastLoginSlotStart+i])
		}
	}
}

func TestAcceptLoginResponse_Encode_HeaderBytesExact(t *testing.T) {
	t.Parallel()

	// Pin the absolute first-two-bytes invariant: little-endian 0x0ac4.
	resp := AcceptLoginResponse{}
	var buf bytes.Buffer
	if err := resp.Encode(&buf); err != nil {
		t.Fatalf("Encode err = %v, want nil", err)
	}
	got := buf.Bytes()
	if got[0] != 0xc4 || got[1] != 0x0a {
		t.Fatalf("first two bytes = %02x %02x, want c4 0a", got[0], got[1])
	}
	// packetLength for 0 char servers = 64 → LE 0x40 0x00.
	if got[2] != 0x40 || got[3] != 0x00 {
		t.Fatalf("packetLength = %02x %02x, want 40 00", got[2], got[3])
	}
}

func TestAcceptLoginResponse_Encode_OverflowErrors(t *testing.T) {
	t.Parallel()

	tooLong26 := make([]byte, lastLoginSlot+1)
	for i := range tooLong26 {
		tooLong26[i] = 'x'
	}
	tooLong17 := make([]byte, tokenSlot+1)
	for i := range tooLong17 {
		tooLong17[i] = 't'
	}
	tooLong20 := make([]byte, charServerNameSlot+1)
	for i := range tooLong20 {
		tooLong20[i] = 'n'
	}

	cases := []struct {
		name    string
		resp    AcceptLoginResponse
		wantErr error
	}{
		{
			name: "last_login too long",
			resp: AcceptLoginResponse{
				LastLogin:   string(tooLong26),
				CharServers: nil,
			},
			wantErr: ErrLastLoginTooLong,
		},
		{
			name: "token too long",
			resp: AcceptLoginResponse{
				Token:       string(tooLong17),
				CharServers: nil,
			},
			wantErr: ErrTokenTooLong,
		},
		{
			name: "char server name too long",
			resp: AcceptLoginResponse{
				CharServers: []CharServer{{Name: string(tooLong20)}},
			},
			wantErr: ErrCharServerNameTooLong,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			err := tc.resp.Encode(&buf)
			if err == nil {
				t.Fatalf("Encode err = nil, want %v", tc.wantErr)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("Encode err = %v, want errors.Is(.., %v)", err, tc.wantErr)
			}
			if buf.Len() != 0 {
				t.Errorf("partial output written: %d bytes (want 0 on error)", buf.Len())
			}
		})
	}
}

func TestAcceptLoginResponse_Size(t *testing.T) {
	t.Parallel()

	for _, n := range []int{0, 1, 2, 5} {
		resp := AcceptLoginResponse{CharServers: make([]CharServer, n)}
		want := acceptHeaderSize + acceptCharServerSize*n
		if got := resp.Size(); got != want {
			t.Errorf("Size() with %d char servers = %d, want %d", n, got, want)
		}
	}
}

func TestRefuseLoginResponse_Encode_ByteExact(t *testing.T) {
	t.Parallel()

	resp := RefuseLoginResponse{
		Error:       0x00000003, // rAthena REFUSE_SERVER_FULL
		UnblockTime: "2025-01-02 03:04:05",
	}

	var buf bytes.Buffer
	if err := resp.Encode(&buf); err != nil {
		t.Fatalf("Encode err = %v, want nil", err)
	}
	got := buf.Bytes()

	if len(got) != refuseLoginSize {
		t.Fatalf("len = %d, want %d", len(got), refuseLoginSize)
	}

	// Header bytes: little-endian 0x083e → 0x3e 0x08.
	if got[0] != 0x3e || got[1] != 0x08 {
		t.Errorf("header bytes = %02x %02x, want 3e 08", got[0], got[1])
	}

	// uint32 error at offset 2 (little-endian).
	wantErrLE := []byte{0x03, 0x00, 0x00, 0x00}
	if !bytes.Equal(got[2:6], wantErrLE) {
		t.Errorf("error field bytes = % x, want % x", got[2:6], wantErrLE)
	}

	// unblock_time[20] zero-padded.
	want := make([]byte, unblockTimeSlot)
	copy(want, resp.UnblockTime)
	if !bytes.Equal(got[6:6+unblockTimeSlot], want) {
		t.Errorf("unblock_time slot = % x, want % x", got[6:6+unblockTimeSlot], want)
	}

	// Confirm trailing tail is zero.
	for i := 6 + len(resp.UnblockTime); i < refuseLoginSize; i++ {
		if got[i] != 0 {
			t.Errorf("tail byte at %d = 0x%02x, want 0x00", i, got[i])
		}
	}
}

func TestRefuseLoginResponse_Encode_EmptyUnblockTime(t *testing.T) {
	t.Parallel()

	resp := RefuseLoginResponse{Error: 0}
	var buf bytes.Buffer
	if err := resp.Encode(&buf); err != nil {
		t.Fatalf("Encode err = %v, want nil", err)
	}
	got := buf.Bytes()
	if len(got) != refuseLoginSize {
		t.Fatalf("len = %d, want %d", len(got), refuseLoginSize)
	}
	// Whole packet except header + error should be zero.
	for i := 6; i < refuseLoginSize; i++ {
		if got[i] != 0 {
			t.Errorf("byte at offset %d = 0x%02x, want 0x00", i, got[i])
		}
	}
}

func TestRefuseLoginResponse_Encode_OverflowErrors(t *testing.T) {
	t.Parallel()

	tooLong := make([]byte, unblockTimeSlot+1)
	for i := range tooLong {
		tooLong[i] = 'u'
	}

	resp := RefuseLoginResponse{UnblockTime: string(tooLong)}
	var buf bytes.Buffer
	err := resp.Encode(&buf)
	if err == nil {
		t.Fatalf("Encode err = nil, want ErrUnblockTimeTooLong")
	}
	if !errors.Is(err, ErrUnblockTimeTooLong) {
		t.Errorf("Encode err = %v, want errors.Is(.., ErrUnblockTimeTooLong)", err)
	}
	if buf.Len() != 0 {
		t.Errorf("partial output written: %d bytes (want 0 on error)", buf.Len())
	}
}

func TestRefuseLoginResponse_Size(t *testing.T) {
	t.Parallel()

	if got := (RefuseLoginResponse{}).Size(); got != refuseLoginSize {
		t.Errorf("Size() = %d, want %d", got, refuseLoginSize)
	}
}
