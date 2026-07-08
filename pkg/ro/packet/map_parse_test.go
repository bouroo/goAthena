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

func TestParseCZRequestTime(t *testing.T) {
	t.Parallel()

	// Known 6-byte frame: cmd 0x007e, clientTick = 0xDEADBEEF.
	goodFrame := func() []byte {
		f := make([]byte, sizeCZRequestTime)
		writeLE16(f[0:], HeaderCZREQUESTTIME)
		writeLE32(f[2:], 0xDEADBEEF)
		return f
	}()

	tests := []struct {
		name       string
		frame      []byte
		wantErr    bool
		wantErrSub string
		want       CZRequestTimeRequest
	}{
		{
			name:    "valid known frame",
			frame:   goodFrame,
			wantErr: false,
			want: CZRequestTimeRequest{
				ClientTick: 0xDEADBEEF,
			},
		},
		{
			name:       "short frame reports byte count",
			frame:      make([]byte, sizeCZRequestTime-1),
			wantErr:    true,
			wantErrSub: "5",
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
				f := make([]byte, sizeCZRequestTime)
				writeLE16(f[0:], HeaderCZENTER) // 0x0072 instead of 0x007e
				return f
			}(),
			wantErr:    true,
			wantErrSub: "unexpected cmd",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseCZRequestTime(tc.frame)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseCZRequestTime() error = nil, want non-nil")
				}
				if tc.wantErrSub != "" && !strings.Contains(err.Error(), tc.wantErrSub) {
					t.Errorf("error %q does not contain %q", err.Error(), tc.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseCZRequestTime() unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("ParseCZRequestTime() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestParseCZGlobalMessage(t *testing.T) {
	t.Parallel()

	// 4-byte header + "hi\0" = 7 bytes total. packetLength is filled
	// in correctly (7) so callers that also decode the length slot get
	// the expected value.
	goodFrame := func() []byte {
		f := []byte{0x8c, 0x00, 0x07, 0x00, 'h', 'i', 0x00}
		return f
	}()

	tests := []struct {
		name       string
		frame      []byte
		wantErr    bool
		wantErrSub string
		want       CZGlobalMessageRequest
	}{
		{
			name:    "valid known frame with NUL terminator",
			frame:   goodFrame,
			wantErr: false,
			want:    CZGlobalMessageRequest{Message: "hi"},
		},
		{
			name:  "valid frame with trailing extra bytes",
			frame: append(append([]byte{}, goodFrame...), 0xAA, 0xBB),
			// Trailing bytes past the packetLength boundary are
			// tolerated so the gateway can hand in a buffered frame
			// without first stripping the tail.
			wantErr: false,
			want:    CZGlobalMessageRequest{Message: "hi"},
		},
		{
			name:       "empty body returns error",
			frame:      []byte{0x8c, 0x00, 0x04, 0x00},
			wantErr:    true,
			wantErrSub: "empty message",
		},
		{
			name:       "short frame reports byte count",
			frame:      make([]byte, 3),
			wantErr:    true,
			wantErrSub: "3",
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
				f := make([]byte, 6)
				writeLE16(f[0:], HeaderCZACTIONREQUEST) // 0x0089 instead of 0x008c
				return f
			}(),
			wantErr:    true,
			wantErrSub: "unexpected cmd",
		},
		{
			name: "body without NUL terminator decodes to full body",
			frame: []byte{
				0x8c, 0x00, 0x07, 0x00,
				'h', 'e', 'l',
			},
			wantErr: false,
			want:    CZGlobalMessageRequest{Message: "hel"},
		},
		{
			name: "packetLength smaller than header reports too-short length",
			frame: []byte{
				0x8c, 0x00, 0x03, 0x00,
				'h', 'i', 0x00,
			},
			wantErr:    true,
			wantErrSub: "packet length 3 too short",
		},
		{
			name: "packetLength larger than frame reports frame/len mismatch",
			frame: []byte{
				0x8c, 0x00, 0x10, 0x00,
				'h', 'i', 0x00,
			},
			wantErr:    true,
			wantErrSub: "frame length 7 shorter than packet length 16",
		},
		{
			name: "trailing bytes past packetLength are not read into message",
			// Header says 7 bytes; the trailing 0xAA 0xBB belong to a
			// subsequent buffered packet and must not leak into the
			// parsed message body.
			frame: []byte{
				0x8c, 0x00, 0x07, 0x00,
				'h', 'i', 0x00,
				0xAA, 0xBB,
			},
			wantErr: false,
			want:    CZGlobalMessageRequest{Message: "hi"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseCZGlobalMessage(tc.frame)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseCZGlobalMessage() error = nil, want non-nil")
				}
				if tc.wantErrSub != "" && !strings.Contains(err.Error(), tc.wantErrSub) {
					t.Errorf("error %q does not contain %q", err.Error(), tc.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseCZGlobalMessage() unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("ParseCZGlobalMessage() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestParseCZActionRequest(t *testing.T) {
	t.Parallel()

	// Known 7-byte frame: cmd 0x0089, targetGID = 0xAABBCCDD,
	// action = 0x01 (sit per goAthena M11 mapping).
	goodFrame := func() []byte {
		f := make([]byte, sizeCZActionRequest)
		writeLE16(f[0:], HeaderCZACTIONREQUEST)
		writeLE32(f[2:], 0xAABBCCDD)
		f[6] = 0x01
		return f
	}()

	tests := []struct {
		name       string
		frame      []byte
		wantErr    bool
		wantErrSub string
		want       CZActionRequestRequest
	}{
		{
			name:    "valid known frame decodes targetGID and action",
			frame:   goodFrame,
			wantErr: false,
			want: CZActionRequestRequest{
				TargetGID: 0xAABBCCDD,
				Action:    0x01,
			},
		},
		{
			name:       "short frame reports byte count",
			frame:      make([]byte, sizeCZActionRequest-1),
			wantErr:    true,
			wantErrSub: "6",
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
				f := make([]byte, sizeCZActionRequest)
				writeLE16(f[0:], HeaderCZGLOBALMESSAGE) // 0x008c instead of 0x0089
				return f
			}(),
			wantErr:    true,
			wantErrSub: "unexpected cmd",
		},
		{
			name: "action selector 0 (stand) decodes verbatim",
			frame: func() []byte {
				f := make([]byte, sizeCZActionRequest)
				writeLE16(f[0:], HeaderCZACTIONREQUEST)
				writeLE32(f[2:], 0x00000001)
				f[6] = 0x00
				return f
			}(),
			wantErr: false,
			want: CZActionRequestRequest{
				TargetGID: 0x00000001,
				Action:    0x00,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseCZActionRequest(tc.frame)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseCZActionRequest() error = nil, want non-nil")
				}
				if tc.wantErrSub != "" && !strings.Contains(err.Error(), tc.wantErrSub) {
					t.Errorf("error %q does not contain %q", err.Error(), tc.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseCZActionRequest() unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("ParseCZActionRequest() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestParseCZActionRequest_AcceptsTrailingBytes confirms the parser
// tolerates bytes past the 7-byte fixed header — the gateway hands in
// buffered frames whose tail is still being drained.
func TestParseCZActionRequest_AcceptsTrailingBytes(t *testing.T) {
	t.Parallel()

	base := make([]byte, sizeCZActionRequest)
	writeLE16(base[0:], HeaderCZACTIONREQUEST)
	writeLE32(base[2:], 0x01020304)
	base[6] = 0x01
	frame := append(append([]byte{}, base...), 0xAA, 0xBB, 0xCC)

	got, err := ParseCZActionRequest(frame)
	if err != nil {
		t.Fatalf("ParseCZActionRequest() unexpected error: %v", err)
	}
	want := CZActionRequestRequest{TargetGID: 0x01020304, Action: 0x01}
	if got != want {
		t.Errorf("ParseCZActionRequest() = %+v, want %+v", got, want)
	}
}
