//go:build unit

package packet

import (
	"bytes"
	"errors"
	"testing"
)

func TestRefuseEnterResponse_Encode_ByteExact(t *testing.T) {
	t.Parallel()

	resp := RefuseEnterResponse{Error: 0x03}

	var buf bytes.Buffer
	if err := resp.Encode(&buf); err != nil {
		t.Fatalf("Encode err = %v, want nil", err)
	}
	got := buf.Bytes()

	if len(got) != sizeHCRefuseEnter {
		t.Fatalf("len = %d, want %d", len(got), sizeHCRefuseEnter)
	}

	// Header bytes: little-endian 0x006c → 0x6c 0x00.
	if got[0] != 0x6c || got[1] != 0x00 {
		t.Errorf("header bytes = %02x %02x, want 6c 00", got[0], got[1])
	}
	if got[2] != 0x03 {
		t.Errorf("error byte = 0x%02x, want 0x03", got[2])
	}
}

func TestRefuseEnterResponse_Encode_ZeroError(t *testing.T) {
	t.Parallel()

	resp := RefuseEnterResponse{}
	var buf bytes.Buffer
	if err := resp.Encode(&buf); err != nil {
		t.Fatalf("Encode err = %v, want nil", err)
	}
	got := buf.Bytes()
	if len(got) != sizeHCRefuseEnter {
		t.Fatalf("len = %d, want %d", len(got), sizeHCRefuseEnter)
	}
	if got[0] != 0x6c || got[1] != 0x00 {
		t.Errorf("header bytes = %02x %02x, want 6c 00", got[0], got[1])
	}
	if got[2] != 0x00 {
		t.Errorf("error byte = 0x%02x, want 0x00", got[2])
	}
}

func TestRefuseEnterResponse_Size(t *testing.T) {
	t.Parallel()

	if got := (RefuseEnterResponse{}).Size(); got != sizeHCRefuseEnter {
		t.Errorf("Size() = %d, want %d", got, sizeHCRefuseEnter)
	}
}

func TestNotifyZoneServerResponse_Encode_ByteExact(t *testing.T) {
	t.Parallel()

	resp := NotifyZoneServerResponse{
		CID:     0x12345678,
		MapName: "prontera",
		IP:      0x7f000001, // 127.0.0.1 in network-order uint32 form
		Port:    5121,
		Domain:  "ro.example.com",
	}

	var buf bytes.Buffer
	if err := resp.Encode(&buf); err != nil {
		t.Fatalf("Encode err = %v, want nil", err)
	}
	got := buf.Bytes()

	if len(got) != sizeHCNotifyZone {
		t.Fatalf("len = %d, want %d", len(got), sizeHCNotifyZone)
	}
	// Header bytes: little-endian 0x0ac5 → 0xc5 0x0a.
	if got[0] != 0xc5 || got[1] != 0x0a {
		t.Errorf("header bytes = %02x %02x, want c5 0a", got[0], got[1])
	}

	// uint32 CID at offset 2 (little-endian) — bytes 0x78 0x56 0x34 0x12.
	if !bytes.Equal(got[2:6], []byte{0x78, 0x56, 0x34, 0x12}) {
		t.Errorf("CID bytes = % x, want 78 56 34 12", got[2:6])
	}

	// mapname[16] at offset 6 — exact-fit string zero-padded.
	wantMap := make([]byte, mapNameExtSlot)
	copy(wantMap, "prontera")
	if !bytes.Equal(got[6:6+mapNameExtSlot], wantMap) {
		t.Errorf("mapname slot = % x, want % x", got[6:6+mapNameExtSlot], wantMap)
	}

	// uint32 ip at offset 22 (2+4+16) — bytes 0x01 0x00 0x00 0x7f.
	if !bytes.Equal(got[22:26], []byte{0x01, 0x00, 0x00, 0x7f}) {
		t.Errorf("ip bytes at offset 22 = % x, want 01 00 00 7f", got[22:26])
	}

	// uint16 port at offset 26 — bytes 0x01 0x14 (5121 LE = 0x1401).
	if !bytes.Equal(got[26:28], []byte{0x01, 0x14}) {
		t.Errorf("port bytes at offset 26 = % x, want 01 14", got[26:28])
	}

	// domain[128] at offset 28 — string zero-padded.
	wantDomain := make([]byte, domainSlot)
	copy(wantDomain, "ro.example.com")
	if !bytes.Equal(got[28:28+domainSlot], wantDomain) {
		t.Errorf("domain slot mismatch at offset 28")
	}

	// Spot-check the trailing tail is zero (string shorter than slot).
	for i := 28 + len("ro.example.com"); i < sizeHCNotifyZone; i++ {
		if got[i] != 0 {
			t.Errorf("tail byte at offset %d = 0x%02x, want 0x00", i, got[i])
		}
	}
}

func TestNotifyZoneServerResponse_Encode_EmptyStrings(t *testing.T) {
	t.Parallel()

	resp := NotifyZoneServerResponse{
		CID:     1,
		MapName: "",
		IP:      0,
		Port:    0,
		Domain:  "",
	}

	var buf bytes.Buffer
	if err := resp.Encode(&buf); err != nil {
		t.Fatalf("Encode err = %v, want nil", err)
	}
	got := buf.Bytes()
	if len(got) != sizeHCNotifyZone {
		t.Fatalf("len = %d, want %d", len(got), sizeHCNotifyZone)
	}

	// After the fixed fields (CID[2:6], mapname[6:22], ip[22:26],
	// port[26:28]) the rest must be zero.
	for i := 28; i < sizeHCNotifyZone; i++ {
		if got[i] != 0 {
			t.Errorf("byte at offset %d = 0x%02x, want 0x00", i, got[i])
		}
	}
}

func TestNotifyZoneServerResponse_Encode_MapNameExactFit(t *testing.T) {
	t.Parallel()

	// Exact-fit mapname (16 bytes) exercises the no-zero-padding path.
	mapName := bytes.Repeat([]byte("M"), mapNameExtSlot)
	resp := NotifyZoneServerResponse{
		CID:     42,
		MapName: string(mapName),
		Port:    5121,
	}

	var buf bytes.Buffer
	if err := resp.Encode(&buf); err != nil {
		t.Fatalf("Encode err = %v, want nil", err)
	}
	got := buf.Bytes()
	if !bytes.Equal(got[6:6+mapNameExtSlot], mapName) {
		t.Errorf("mapname slot = % x, want % x (exact-fit copy)", got[6:6+mapNameExtSlot], mapName)
	}
	// After the mapname slot, ip + port + domain must start clean.
	if !bytes.Equal(got[22:26], []byte{0, 0, 0, 0}) {
		t.Errorf("ip slot not zero at offset 22: % x", got[22:26])
	}
}

func TestNotifyZoneServerResponse_Encode_OverflowErrors(t *testing.T) {
	t.Parallel()

	tooLongMap := make([]byte, mapNameExtSlot+1)
	for i := range tooLongMap {
		tooLongMap[i] = 'a'
	}
	tooLongDomain := make([]byte, domainSlot+1)
	for i := range tooLongDomain {
		tooLongDomain[i] = 'd'
	}

	cases := []struct {
		name    string
		resp    NotifyZoneServerResponse
		wantErr error
	}{
		{
			name:    "mapname too long",
			resp:    NotifyZoneServerResponse{MapName: string(tooLongMap)},
			wantErr: ErrMapNameTooLong,
		},
		{
			name:    "domain too long",
			resp:    NotifyZoneServerResponse{Domain: string(tooLongDomain)},
			wantErr: ErrDomainTooLong,
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

func TestNotifyZoneServerResponse_Size(t *testing.T) {
	t.Parallel()

	if got := (NotifyZoneServerResponse{}).Size(); got != sizeHCNotifyZone {
		t.Errorf("Size() = %d, want %d", got, sizeHCNotifyZone)
	}
}
