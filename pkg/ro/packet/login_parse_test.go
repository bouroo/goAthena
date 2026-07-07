//go:build unit

package packet

import (
	"strings"
	"testing"
)

func TestParseCALogin(t *testing.T) {
	t.Parallel()

	// Known 55-byte frame: cmd 0x0064, version 0x12345678 LE, username
	// "testuser" + NUL pad to 24, password "p@ss" + NUL pad to 24,
	// client_type 0x01.
	goodFrame := func() []byte {
		f := make([]byte, sizeCALogin)
		writeLE16(f[0:], HeaderCALOGIN)
		writeLE32(f[2:], 0x12345678)
		copy(f[6:], "testuser")
		copy(f[30:], "p@ss")
		f[54] = 0x01
		return f
	}()

	// Username and password fill all 24 bytes with no NUL terminator.
	fullFrame := func() []byte {
		f := make([]byte, sizeCALogin)
		writeLE16(f[0:], HeaderCALOGIN)
		writeLE32(f[2:], 0xdeadbeef)
		user := strings.Repeat("a", 24)
		pass := strings.Repeat("b", 24)
		copy(f[6:], user)
		copy(f[30:], pass)
		f[54] = 0x02
		return f
	}()

	tests := []struct {
		name       string
		frame      []byte
		wantErr    bool
		wantErrSub string
		want       CALoginRequest
	}{
		{
			name:    "valid known frame",
			frame:   goodFrame,
			wantErr: false,
			want: CALoginRequest{
				Version:    0x12345678,
				Username:   "testuser",
				Password:   "p@ss",
				ClientType: 0x01,
			},
		},
		{
			name:    "full slot no NUL returns full 24-byte string",
			frame:   fullFrame,
			wantErr: false,
			want: CALoginRequest{
				Version:    0xdeadbeef,
				Username:   strings.Repeat("a", 24),
				Password:   strings.Repeat("b", 24),
				ClientType: 0x02,
			},
		},
		{
			name:       "short frame reports byte count",
			frame:      make([]byte, sizeCALogin-1),
			wantErr:    true,
			wantErrSub: "54",
		},
		{
			name:       "empty frame reports byte count",
			frame:      []byte{},
			wantErr:    true,
			wantErrSub: "0",
		},
		{
			name: "wrong cmd reports unexpected cmd id",
			frame: func() []byte {
				f := make([]byte, sizeCALogin)
				writeLE16(f[0:], 0x0065)
				return f
			}(),
			wantErr:    true,
			wantErrSub: "unexpected cmd",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseCALogin(tc.frame)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseCALogin() error = nil, want non-nil")
				}
				if tc.wantErrSub != "" && !strings.Contains(err.Error(), tc.wantErrSub) {
					t.Errorf("error %q does not contain %q", err.Error(), tc.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseCALogin() unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("ParseCALogin() = %+v, want %+v", got, tc.want)
			}
		})
	}
}
