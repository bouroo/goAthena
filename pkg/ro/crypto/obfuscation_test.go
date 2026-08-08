//go:build unit

package crypto

import (
	"testing"
)

// Test vectors for PACKETVER 20110817: keys = (0x053D5CED, 0x3DED6DED, 0x6DED6DED).
// Session-state derivation:
//
//	key0     = 0x053D5CED
//	step1    = key0*K+I = 0x053D5CED * 0x3DED6DED + 0x6DED6DED
//	step2    = step1 *K+I = step1 * 0x3DED6DED + 0x6DED6DED   (this is the initial session state)
//
// The obfuscator's initial session state is the result of two LCG steps from key0.
const (
	k0_20110817 = uint32(0x053D5CED)
	k1_20110817 = uint32(0x3DED6DED)
	k2_20110817 = uint32(0x6DED6DED)
)

// Pre-computed for the 20110817 triplet by the reference algorithm:
//
//	key0              = 0x053D5CED
//	step1             = (key0 * 0x3DED6DED + 0x6DED6DED) & 0xFFFFFFFF = 0xE8B65E56
//	step2 (session)   = (step1 * 0x3DED6DED + 0x6DED6DED) & 0xFFFFFFFF = 0x588B618B
const expectedSessionState_20110817 = uint32(0x588B618B)

// Pre-computed per-PACKETVER key for boundary tests.
const (
	k0_20180307 = uint32(0x47DA10EB)
	k1_20180307 = uint32(0x4B922CCF)
	k2_20180307 = uint32(0x765C5055)
)

func TestObfuscator_NewSessionKey(t *testing.T) {
	t.Parallel()

	// Compute the expected initial session state inline; the constant above
	// must match this computation. If you change the algorithm, you'll
	// change this.
	state := lcgStep(k0_20110817, k1_20110817, k2_20110817)
	state = lcgStep(state, k1_20110817, k2_20110817)
	if state != expectedSessionState_20110817 {
		t.Fatalf("session state for PACKETVER 20110817: got 0x%08X, want 0x%08X",
			state, expectedSessionState_20110817)
	}

	obf := NewObfuscator(k0_20110817, k1_20110817, k2_20110817)
	if obf.Disabled() {
		t.Fatalf("obfuscator must not be disabled for non-zero keys")
	}
	if obf.state != expectedSessionState_20110817 {
		t.Fatalf("NewObfuscator session state: got 0x%08X, want 0x%08X",
			obf.state, expectedSessionState_20110817)
	}
}

func TestObfuscator_DecodeUsesSessionMask(t *testing.T) {
	t.Parallel()

	obf := NewObfuscator(k0_20110817, k1_20110817, k2_20110817)
	const raw uint16 = 0x1234

	want := raw ^ uint16((expectedSessionState_20110817>>16)&0x7FFF)
	got := obf.Decode(raw)
	if got != want {
		t.Fatalf("Decode(0x%04X) using session state 0x%08X: got 0x%04X, want 0x%04X",
			raw, expectedSessionState_20110817, got, want)
	}
}

func TestObfuscator_DecodeAdvancesState(t *testing.T) {
	t.Parallel()

	obf := NewObfuscator(k0_20110817, k1_20110817, k2_20110817)
	stateBefore := obf.state
	_ = obf.Decode(0x1234)
	stateAfter := obf.state

	wantAfter := lcgStep(stateBefore, k1_20110817, k2_20110817)
	if stateAfter != wantAfter {
		t.Fatalf("Decode state advance: got 0x%08X, want 0x%08X", stateAfter, wantAfter)
	}
}

func TestObfuscator_EncodeDecodeRoundTrip(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		k0, k1, k2 uint32
		raw        []uint16
	}{
		{"20110817", k0_20110817, k1_20110817, k2_20110817, []uint16{0x0000, 0x0001, 0x0064, 0x0072, 0x0FFF, 0x1234, 0xFFFF}},
		{"20180307", k0_20180307, k1_20180307, k2_20180307, []uint16{0x0000, 0x0001, 0x0064, 0x0072, 0x0FFF, 0x1234, 0xFFFF}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for _, raw := range tc.raw {
				enc := NewObfuscator(tc.k0, tc.k1, tc.k2)
				dec := NewObfuscator(tc.k0, tc.k1, tc.k2)
				cipher := enc.Encode(raw)
				got := dec.Decode(cipher)
				if got != raw {
					t.Fatalf("Encode(0x%04X)=0x%04X → Decode=0x%04X (want 0x%04X)",
						raw, cipher, got, raw)
				}
			}
		})
	}
}

func TestObfuscator_EncodeIsIdenticalToDecode(t *testing.T) {
	t.Parallel()

	// XOR is symmetric over the same mask. Encode and Decode must produce
	// identical output for the same input when starting from the same
	// session state.
	obf1 := NewObfuscator(k0_20110817, k1_20110817, k2_20110817)
	obf2 := NewObfuscator(k0_20110817, k1_20110817, k2_20110817)

	for _, raw := range []uint16{0x0000, 0x0001, 0x0064, 0x1234, 0xFFFF} {
		if obf1.Encode(raw) != obf2.Decode(raw) {
			t.Fatalf("Encode != Decode for raw=0x%04X", raw)
		}
	}
}

func TestObfuscator_DisabledIsIdentity(t *testing.T) {
	t.Parallel()

	obf := NewObfuscator(0, 0, 0)
	if !obf.Disabled() {
		t.Fatalf("obfuscator with (0,0,0) must be disabled")
	}

	for _, raw := range []uint16{0x0000, 0x0001, 0x0064, 0x1234, 0xFFFF, 0x7FFF, 0x8000} {
		if got := obf.Decode(raw); got != raw {
			t.Fatalf("disabled Decode(0x%04X)=0x%04X; want identity", raw, got)
		}
		if got := obf.Encode(raw); got != raw {
			t.Fatalf("disabled Encode(0x%04X)=0x%04X; want identity", raw, got)
		}
	}
}

func TestObfuscator_DisabledDoesNotAdvanceState(t *testing.T) {
	t.Parallel()

	obf := NewObfuscator(0, 0, 0)
	stateBefore := obf.state
	for i := 0; i < 100; i++ {
		_ = obf.Decode(0x1234)
		_ = obf.Encode(0x1234)
	}
	if obf.state != stateBefore {
		t.Fatalf("disabled obfuscator state changed: 0x%08X → 0x%08X",
			stateBefore, obf.state)
	}
}

func TestFirstPacketDecode(t *testing.T) {
	t.Parallel()

	// Per clif.cpp:25713:
	//   state   = key0*key1 + key2
	//   decoded = raw ^ ((state >> 16) & 0x7FFF)
	state := lcgStep(k0_20110817, k1_20110817, k2_20110817)
	const raw uint16 = 0x0E5F // CZ_ENTER (WantToConnection) raw cmd after obfuscation
	want := raw ^ uint16((state>>16)&0x7FFF)
	got := FirstPacketDecode(k0_20110817, k1_20110817, k2_20110817, raw)
	if got != want {
		t.Fatalf("FirstPacketDecode(0x%04X) using state 0x%08X: got 0x%04X, want 0x%04X",
			raw, state, got, want)
	}
}

func TestFirstPacketDecode_OffModeIsIdentity(t *testing.T) {
	t.Parallel()

	for _, raw := range []uint16{0x0000, 0x0E5F, 0x1234, 0xFFFF} {
		if got := FirstPacketDecode(0, 0, 0, raw); got != raw {
			t.Fatalf("FirstPacketDecode(0,0,0,0x%04X)=0x%04X; want identity", raw, got)
		}
	}
}

func TestKeysForVersion(t *testing.T) {
	t.Parallel()

	t.Run("known-version returns non-zero keys", func(t *testing.T) {
		t.Parallel()
		k0, k1, k2 := KeysForVersion(20130807)
		if k0 == 0 && k1 == 0 && k2 == 0 {
			t.Fatalf("KeysForVersion(20130807) returned (0,0,0); want non-zero triplet")
		}
		if k0 == 0 || k1 == 0 || k2 == 0 {
			t.Fatalf("KeysForVersion(20130807) returned partial zeros: (0x%X,0x%X,0x%X)", k0, k1, k2)
		}
	})
	t.Run("first-version matches rathena table", func(t *testing.T) {
		t.Parallel()
		k0, k1, k2 := KeysForVersion(20110817)
		if k0 != k0_20110817 || k1 != k1_20110817 || k2 != k2_20110817 {
			t.Fatalf("KeysForVersion(20110817)=(%#X,%#X,%#X); want (%#X,%#X,%#X)",
				k0, k1, k2, k0_20110817, k1_20110817, k2_20110817)
		}
	})
	t.Run("cutoff-version returns non-zero keys", func(t *testing.T) {
		t.Parallel()
		k0, k1, k2 := KeysForVersion(20180307)
		if k0 == 0 && k1 == 0 && k2 == 0 {
			t.Fatalf("KeysForVersion(20180307) returned (0,0,0); want non-zero triplet")
		}
	})
	t.Run("post-cutoff returns zero keys", func(t *testing.T) {
		t.Parallel()
		k0, k1, k2 := KeysForVersion(20200101)
		if k0 != 0 || k1 != 0 || k2 != 0 {
			t.Fatalf("KeysForVersion(20200101)=(%#X,%#X,%#X); want (0,0,0)",
				k0, k1, k2)
		}
	})
	t.Run("unknown version returns zero keys", func(t *testing.T) {
		t.Parallel()
		k0, k1, k2 := KeysForVersion(19990101)
		if k0 != 0 || k1 != 0 || k2 != 0 {
			t.Fatalf("KeysForVersion(19990101)=(%#X,%#X,%#X); want (0,0,0)",
				k0, k1, k2)
		}
	})
}

func TestGeneratedKeyTable(t *testing.T) {
	t.Parallel()

	// Sanity-check the generated artifact that ships in this package.
	if got := len(obfuscationKeys); got < 100 {
		t.Fatalf("obfuscationKeys has %d entries; want at least 100", got)
	}
	for _, v := range []int{20110817, 20180307} {
		if _, ok := obfuscationKeys[v]; !ok {
			t.Fatalf("obfuscationKeys missing PACKETVER %d", v)
		}
	}
	for v, keys := range obfuscationKeys {
		if v <= obfuscationCutoff {
			if keys == [3]uint32{} {
				t.Fatalf("obfuscationKeys[%d] is (0,0,0) for an in-range PACKETVER", v)
			}
		}
	}

	if obfuscationCutoff < 20180307 {
		t.Fatalf("obfuscationCutoff=%d; want >=20180307 (last obfuscated client per rAthena)",
			obfuscationCutoff)
	}
}
