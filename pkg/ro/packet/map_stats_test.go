//go:build unit

package packet

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// P2C: stats & leveling packet encode/parse coverage.

func TestParseCZStatusChange(t *testing.T) {
	t.Parallel()

	good := make([]byte, sizeCZStatusChange)
	binary.LittleEndian.PutUint16(good[0:], HeaderCZSTATUSCHANGE)
	binary.LittleEndian.PutUint16(good[2:], 13) // SP_STR
	good[4] = 1                                 // amount

	got, err := ParseCZStatusChange(good)
	if err != nil {
		t.Fatalf("ParseCZStatusChange valid: unexpected error: %v", err)
	}
	if got.StatusID != 13 || got.Amount != 1 {
		t.Errorf("ParseCZStatusChange = %+v, want {StatusID:13 Amount:1}", got)
	}

	// Too-short frame must error, not panic.
	if _, err := ParseCZStatusChange(good[:3]); err == nil {
		t.Error("ParseCZStatusChange short frame: want error, got nil")
	}

	// Wrong header must error.
	bad := make([]byte, sizeCZStatusChange)
	binary.LittleEndian.PutUint16(bad[0:], 0xdead)
	if _, err := ParseCZStatusChange(bad); err == nil {
		t.Error("ParseCZStatusChange wrong cmd: want error, got nil")
	}
}

func TestCZStatusChangeRequest_EncodeRoundTrip(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	req := CZStatusChangeRequest{StatusID: 17, Amount: 2} // SP_DEX
	if err := req.Encode(&buf); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if buf.Len() != sizeCZStatusChange {
		t.Fatalf("encoded len = %d, want %d", buf.Len(), sizeCZStatusChange)
	}
	// Header byte 0 = 0xbb (little-endian low byte of 0x00bb).
	if buf.Bytes()[0] != 0xbb {
		t.Errorf("encoded header[0] = 0x%02x, want 0xbb", buf.Bytes()[0])
	}
	got, err := ParseCZStatusChange(buf.Bytes())
	if err != nil {
		t.Fatalf("round-trip parse: %v", err)
	}
	if got != req {
		t.Errorf("round-trip = %+v, want %+v", got, req)
	}
}

func TestZCStatusChangeAck_Encode(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	ack := ZCStatusChangeAck{StatusID: 13, Result: 0, Value: 5}
	if err := ack.Encode(&buf); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if buf.Len() != sizeZCStatusChangeAck {
		t.Fatalf("encoded len = %d, want %d", buf.Len(), sizeZCStatusChangeAck)
	}
	want := []byte{0xbc, 0x00, 13, 0x00, 0x00, 0x05}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("encoded = % x, want % x", buf.Bytes(), want)
	}
}

func TestZCNotifyEffect_Encode(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	eff := ZCNotifyEffect{AID: 0, EffectID: EffectBaseLevelUp}
	if err := eff.Encode(&buf); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if buf.Len() != sizeZCNotifyEffect {
		t.Fatalf("encoded len = %d, want %d", buf.Len(), sizeZCNotifyEffect)
	}
	// [0xbb9b? no]: header 0x019b LE = 0x9b,0x01; then AID(4)=0; effectID(4)=0.
	want := []byte{0x9b, 0x01, 0, 0, 0, 0, 0, 0, 0, 0}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("encoded = % x, want % x", buf.Bytes(), want)
	}

	// Non-zero AID + effect should round-trip the fields at their offsets.
	buf.Reset()
	eff = ZCNotifyEffect{AID: 0x01020304, EffectID: 7}
	if err := eff.Encode(&buf); err != nil {
		t.Fatalf("Encode non-zero: %v", err)
	}
	b := buf.Bytes()
	if got := binary.LittleEndian.Uint32(b[2:6]); got != 0x01020304 {
		t.Errorf("AID field = 0x%08x, want 0x01020304", got)
	}
	if got := binary.LittleEndian.Uint32(b[6:10]); got != 7 {
		t.Errorf("EffectID field = %d, want 7", got)
	}
}
