//go:build unit

package packet

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestCALoginRequest_Encode_RoundTrip(t *testing.T) {
	t.Parallel()

	req := CALoginRequest{
		Version:    0x12345678,
		Username:   "testuser",
		Password:   "p@ss",
		ClientType: 0x01,
	}

	var buf bytes.Buffer
	if err := req.Encode(&buf); err != nil {
		t.Fatalf("Encode err = %v, want nil", err)
	}
	if got := buf.Len(); got != sizeCALogin {
		t.Fatalf("Encode wrote %d bytes, want %d", got, sizeCALogin)
	}

	got, err := ParseCALogin(buf.Bytes())
	if err != nil {
		t.Fatalf("ParseCALogin err = %v, want nil", err)
	}
	if got != req {
		t.Errorf("round-trip mismatch:\n got = %+v\nwant = %+v", got, req)
	}
}

func TestCALoginRequest_Encode_ZeroValues(t *testing.T) {
	t.Parallel()

	req := CALoginRequest{}

	var buf bytes.Buffer
	if err := req.Encode(&buf); err != nil {
		t.Fatalf("Encode err = %v, want nil", err)
	}
	if got := buf.Len(); got != sizeCALogin {
		t.Fatalf("Encode wrote %d bytes, want %d", got, sizeCALogin)
	}

	got, err := ParseCALogin(buf.Bytes())
	if err != nil {
		t.Fatalf("ParseCALogin err = %v, want nil", err)
	}
	if got != req {
		t.Errorf("zero round-trip mismatch:\n got = %+v\nwant = %+v", got, req)
	}
}

func TestCALoginRequest_Encode_FullSlotStrings(t *testing.T) {
	t.Parallel()

	req := CALoginRequest{
		Version:    0xdeadbeef,
		Username:   strings.Repeat("a", caLoginUsernameSlot),
		Password:   strings.Repeat("b", caLoginPasswordSlot),
		ClientType: 0x02,
	}

	var buf bytes.Buffer
	if err := req.Encode(&buf); err != nil {
		t.Fatalf("Encode err = %v, want nil", err)
	}

	got, err := ParseCALogin(buf.Bytes())
	if err != nil {
		t.Fatalf("ParseCALogin err = %v, want nil", err)
	}
	if got.Username != req.Username {
		t.Errorf("username round-trip = %q, want %q", got.Username, req.Username)
	}
	if got.Password != req.Password {
		t.Errorf("password round-trip = %q, want %q", got.Password, req.Password)
	}
	if got.Version != req.Version || got.ClientType != req.ClientType {
		t.Errorf("scalars round-trip = %+v, want version=0x%x clienttype=%d",
			got, req.Version, req.ClientType)
	}
}

func TestCALoginRequest_Encode_OverflowErrors(t *testing.T) {
	t.Parallel()

	tooLong25 := strings.Repeat("x", caLoginUsernameSlot+1)
	tooLong25pw := strings.Repeat("y", caLoginPasswordSlot+1)

	cases := []struct {
		name    string
		req     CALoginRequest
		wantErr error
	}{
		{
			name:    "username too long",
			req:     CALoginRequest{Username: tooLong25},
			wantErr: ErrCALoginUsernameTooLong,
		},
		{
			name:    "password too long",
			req:     CALoginRequest{Password: tooLong25pw},
			wantErr: ErrCALoginPasswordTooLong,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			err := tc.req.Encode(&buf)
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

func TestCALoginRequest_Encode_HeaderBytesExact(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	if err := (CALoginRequest{Username: "u", Password: "p"}).Encode(&buf); err != nil {
		t.Fatalf("Encode err = %v", err)
	}
	got := buf.Bytes()
	// CA_LOGIN cmd 0x0064 little-endian → 0x64 0x00.
	if got[0] != 0x64 || got[1] != 0x00 {
		t.Errorf("header bytes = %02x %02x, want 64 00", got[0], got[1])
	}
}
