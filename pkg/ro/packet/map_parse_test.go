//go:build unit

package packet

import (
	"strings"
	"testing"
)

func TestParseCZEnter(t *testing.T) {
	t.Parallel()

	// Known 19-byte frame: cmd 0x0072, AID 0xAAAAAAAA, CID 0xBBBBBBBB,
	// authCode 0xCCCCCCCC, clientTime 0xDDDDDDDD, sex 0x01.
	goodFrame := func() []byte {
		f := make([]byte, sizeCZEnter)
		writeLE16(f[0:], HeaderCZENTER)
		writeLE32(f[2:], 0xAAAAAAAA)
		writeLE32(f[6:], 0xBBBBBBBB)
		writeLE32(f[10:], 0xCCCCCCCC)
		writeLE32(f[14:], 0xDDDDDDDD)
		f[18] = 0x01
		return f
	}()

	tests := []struct {
		name       string
		frame      []byte
		wantErr    bool
		wantErrSub string
		want       CZEnterRequest
	}{
		{
			name:    "valid known frame",
			frame:   goodFrame,
			wantErr: false,
			want: CZEnterRequest{
				AccountID:  0xAAAAAAAA,
				CharID:     0xBBBBBBBB,
				AuthCode:   0xCCCCCCCC,
				ClientTime: 0xDDDDDDDD,
				Sex:        0x01,
			},
		},
		{
			name:       "short frame reports byte count",
			frame:      make([]byte, sizeCZEnter-1),
			wantErr:    true,
			wantErrSub: "18",
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
				f := make([]byte, sizeCZEnter)
				writeLE16(f[0:], HeaderCZREQUESTMOVE) // 0x0085 instead of 0x0072
				return f
			}(),
			wantErr:    true,
			wantErrSub: "unexpected cmd",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseCZEnter(tc.frame)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseCZEnter() error = nil, want non-nil")
				}
				if tc.wantErrSub != "" && !strings.Contains(err.Error(), tc.wantErrSub) {
					t.Errorf("error %q does not contain %q", err.Error(), tc.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseCZEnter() unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("ParseCZEnter() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestParseCZEnter_AcceptsTrailingBytes(t *testing.T) {
	t.Parallel()

	// The parser allows trailing bytes after the fixed 19-byte header so
	// the gateway can hand in a buffered frame without first stripping the
	// tail.
	base := make([]byte, sizeCZEnter)
	writeLE16(base[0:], HeaderCZENTER)
	writeLE32(base[2:], 0x01020304)
	writeLE32(base[6:], 0x05060708)
	writeLE32(base[10:], 0x090A0B0C)
	writeLE32(base[14:], 0x0D0E0F10)
	base[18] = 0x00
	frame := append(append([]byte{}, base...), 0xAA, 0xBB, 0xCC)

	got, err := ParseCZEnter(frame)
	if err != nil {
		t.Fatalf("ParseCZEnter() unexpected error: %v", err)
	}
	want := CZEnterRequest{
		AccountID:  0x01020304,
		CharID:     0x05060708,
		AuthCode:   0x090A0B0C,
		ClientTime: 0x0D0E0F10,
		Sex:        0x00,
	}
	if got != want {
		t.Errorf("ParseCZEnter() = %+v, want %+v", got, want)
	}
}

func TestParseCZRequestMove(t *testing.T) {
	t.Parallel()

	// Known 5-byte frame: cmd 0x0085, dest = encodePos(150, 200) = [37, 140, 131].
	goodFrame := func() []byte {
		f := make([]byte, sizeCZRequestMove)
		writeLE16(f[0:], HeaderCZREQUESTMOVE)
		var pos [3]byte
		encodePos(pos[:], 150, 200, 0)
		copy(f[2:], pos[:])
		return f
	}()

	tests := []struct {
		name       string
		frame      []byte
		wantErr    bool
		wantErrSub string
		want       CZRequestMoveRequest
	}{
		{
			name:    "valid known frame",
			frame:   goodFrame,
			wantErr: false,
			want: CZRequestMoveRequest{
				DestX: 150,
				DestY: 200,
			},
		},
		{
			name:       "short frame reports byte count",
			frame:      make([]byte, sizeCZRequestMove-1),
			wantErr:    true,
			wantErrSub: "4",
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
				f := make([]byte, sizeCZRequestMove)
				writeLE16(f[0:], HeaderCZENTER) // 0x0072 instead of 0x0085
				return f
			}(),
			wantErr:    true,
			wantErrSub: "unexpected cmd",
		},
		{
			name: "dest at origin decodes to 0,0",
			frame: func() []byte {
				f := make([]byte, sizeCZRequestMove)
				writeLE16(f[0:], HeaderCZREQUESTMOVE)
				return f
			}(),
			wantErr: false,
			want: CZRequestMoveRequest{
				DestX: 0,
				DestY: 0,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseCZRequestMove(tc.frame)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseCZRequestMove() error = nil, want non-nil")
				}
				if tc.wantErrSub != "" && !strings.Contains(err.Error(), tc.wantErrSub) {
					t.Errorf("error %q does not contain %q", err.Error(), tc.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseCZRequestMove() unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("ParseCZRequestMove() = %+v, want %+v", got, tc.want)
			}
		})
	}
}
