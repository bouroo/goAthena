//go:build unit

package packet

import "testing"

func TestEncodeDecodePos_RoundTrip(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		x, y        int16
		dir         uint8
		wantByteLen int
	}{
		// The kRO 3-byte packed position carries 10 bits of x and 10 bits of
		// y in [0, 1023]. Negative inputs lose their sign through the
		// truncation (matches rathena/src/map/clif.cpp:173-211 WBUFPOS /
		// RBUFPOS exactly), so all round-trip cases stay non-negative.
		{"mid cell", 150, 200, 3, 3},
		{"origin", 0, 0, 0, 3},
		{"upper cell", 512, 512, 15, 3},
		{"east step from origin", 1, 0, 1, 3},
		{"south step from origin", 0, 1, 3, 3},
		{"max representable", 1023, 1023, 0, 3},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf [3]byte
			encodePos(buf[:], tc.x, tc.y, tc.dir)

			if got, want := len(buf[:]), tc.wantByteLen; got != want {
				t.Fatalf("encoded len = %d, want %d", got, want)
			}

			gotX, gotY, gotDir := decodePos(buf[:])
			if gotX != tc.x || gotY != tc.y || gotDir != tc.dir {
				t.Errorf("decodePos = (%d, %d, %d), want (%d, %d, %d) for bytes %v",
					gotX, gotY, gotDir, tc.x, tc.y, tc.dir, []byte{buf[0], buf[1], buf[2]})
			}
		})
	}
}

func TestEncodePos_KnownBytes(t *testing.T) {
	t.Parallel()

	// Hand-computed reference values for (x=100, y=200, dir=0). The bit
	// layout matches rathena/src/map/clif.cpp:173-178 WBUFPOS:
	//
	//	p[0] = (100 >> 2)                                   = 25
	//	p[1] = ((100 << 6) | ((200 >> 4) & 0x3f)) & 0xff    = 12
	//	p[2] = ((200 << 4) | (0  & 0x0f))        & 0xff    = 128
	var buf [3]byte
	encodePos(buf[:], 100, 200, 0)

	want := [3]byte{0x19, 0x0c, 0x80}
	if buf != want {
		t.Errorf("encodePos(100,200,0) = %v, want %v", []byte{buf[0], buf[1], buf[2]}, []byte{want[0], want[1], want[2]})
	}
}

func TestEncodeDecodePos_KnownBytes_SecondValue(t *testing.T) {
	t.Parallel()

	// Cross-check with the (150, 200, 3) cell used by the move tests:
	//
	//	p[0] = (150 >> 2)                                       = 37
	//	p[1] = ((150 << 6) | ((200 >> 4) & 0x3f))        & 0xff  = 140
	//	p[2] = ((200 << 4) | (3   & 0x0f))             & 0xff  = 131
	var buf [3]byte
	encodePos(buf[:], 150, 200, 3)

	want := [3]byte{0x25, 0x8c, 0x83}
	if buf != want {
		t.Errorf("encodePos(150,200,3) = %v, want %v", []byte{buf[0], buf[1], buf[2]}, []byte{want[0], want[1], want[2]})
	}

	gotX, gotY, gotDir := decodePos(buf[:])
	if gotX != 150 || gotY != 200 || gotDir != 3 {
		t.Errorf("decodePos of those bytes = (%d, %d, %d), want (150, 200, 3)", gotX, gotY, gotDir)
	}
}
