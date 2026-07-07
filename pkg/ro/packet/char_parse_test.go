//go:build unit

package packet

import (
	"strings"
	"testing"
)

func TestParseCHEnter(t *testing.T) {
	t.Parallel()

	// Known 17-byte frame: cmd 0x0065, AID 0xAAAAAAAA, login_id1 0xBBBBBBBB,
	// login_id2 0xCCCCCCCC, reserved[14:16] = 0, sex 0x01.
	goodFrame := func() []byte {
		f := make([]byte, sizeCHEnter)
		writeLE16(f[0:], HeaderCHENTER)
		writeLE32(f[2:], 0xAAAAAAAA)
		writeLE32(f[6:], 0xBBBBBBBB)
		writeLE32(f[10:], 0xCCCCCCCC)
		f[16] = 0x01
		return f
	}()

	tests := []struct {
		name       string
		frame      []byte
		wantErr    bool
		wantErrSub string
		want       CHEnterRequest
	}{
		{
			name:    "valid known frame",
			frame:   goodFrame,
			wantErr: false,
			want: CHEnterRequest{
				AccountID: 0xAAAAAAAA,
				LoginID1:  0xBBBBBBBB,
				LoginID2:  0xCCCCCCCC,
				Sex:       0x01,
			},
		},
		{
			name:       "short frame reports byte count",
			frame:      make([]byte, sizeCHEnter-1),
			wantErr:    true,
			wantErrSub: "16",
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
				f := make([]byte, sizeCHEnter)
				writeLE16(f[0:], HeaderCHSELECTCHAR) // 0x0066 instead of 0x0065
				return f
			}(),
			wantErr:    true,
			wantErrSub: "unexpected cmd",
		},
		{
			name: "oversize frame reports byte count",
			frame: func() []byte {
				f := make([]byte, sizeCHEnter+1)
				writeLE16(f[0:], HeaderCHENTER)
				return f
			}(),
			wantErr:    true,
			wantErrSub: "18",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseCHEnter(tc.frame)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseCHEnter() error = nil, want non-nil")
				}
				if tc.wantErrSub != "" && !strings.Contains(err.Error(), tc.wantErrSub) {
					t.Errorf("error %q does not contain %q", err.Error(), tc.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseCHEnter() unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("ParseCHEnter() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestParseCHSelectChar(t *testing.T) {
	t.Parallel()

	// Known 3-byte frame: cmd 0x0066, slot 0x05.
	goodFrame := func() []byte {
		f := make([]byte, sizeCHSelectChar)
		writeLE16(f[0:], HeaderCHSELECTCHAR)
		f[2] = 0x05
		return f
	}()

	tests := []struct {
		name       string
		frame      []byte
		wantErr    bool
		wantErrSub string
		want       CHSelectCharRequest
	}{
		{
			name:    "valid known frame",
			frame:   goodFrame,
			wantErr: false,
			want:    CHSelectCharRequest{Slot: 0x05},
		},
		{
			name: "zero slot",
			frame: func() []byte {
				f := make([]byte, sizeCHSelectChar)
				writeLE16(f[0:], HeaderCHSELECTCHAR)
				return f
			}(),
			wantErr: false,
			want:    CHSelectCharRequest{Slot: 0x00},
		},
		{
			name:       "short frame reports byte count",
			frame:      make([]byte, sizeCHSelectChar-1),
			wantErr:    true,
			wantErrSub: "2",
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
				f := make([]byte, sizeCHSelectChar)
				writeLE16(f[0:], HeaderCHENTER) // 0x0065 instead of 0x0066
				return f
			}(),
			wantErr:    true,
			wantErrSub: "unexpected cmd",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseCHSelectChar(tc.frame)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseCHSelectChar() error = nil, want non-nil")
				}
				if tc.wantErrSub != "" && !strings.Contains(err.Error(), tc.wantErrSub) {
					t.Errorf("error %q does not contain %q", err.Error(), tc.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseCHSelectChar() unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("ParseCHSelectChar() = %+v, want %+v", got, tc.want)
			}
		})
	}
}
