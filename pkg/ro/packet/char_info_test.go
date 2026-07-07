//go:build unit

package packet

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

// TestCharacterInfoSize_Constant pins the on-wire byte length of one
// CHARACTER_INFO entry for PACKETVER 20250604. Any drift here is a
// protocol break and must be reviewed, not silently fixed.
func TestCharacterInfoSize_Constant(t *testing.T) {
	t.Parallel()

	if CharacterInfoSize != 175 {
		t.Fatalf("CharacterInfoSize = %d, want 175 (PACKETVER 20250604)", CharacterInfoSize)
	}
}

// TestCharacterInfo_Encode_ByteExact asserts the per-field layout for a
// representative character. We populate enough fields to exercise every
// variable-width section (HP/MP/SP 64-bit fields, name + mapName zero-padding,
// the six uint8 stat slots, and the trailing sex byte), then spot-check the
// expected offsets against the captured bytes.
func TestCharacterInfo_Encode_ByteExact(t *testing.T) {
	t.Parallel()

	ci := CharacterInfo{
		GID:         1,
		Exp:         100,
		Money:       12345,
		JobExp:      200,
		JobLevel:    1,
		BodyState:   0,
		HealthState: 0,
		EffectState: 0,
		Virtue:      0,
		Honor:       0,
		JobPoint:    0,
		HP:          50,
		MaxHP:       50,
		SP:          10,
		MaxSP:       10,
		Speed:       150,
		Job:         0, // Novice
		Head:        1,
		Body:        0,
		Weapon:      0,
		Level:       1,
		SPPoint:     0,
		Accessory:   0,
		Shield:      0,
		Accessory2:  0,
		Accessory3:  0,
		HeadPalette: 0,
		BodyPalette: 0,
		Name:        "Test",
		Str:         1,
		Agi:         1,
		Vit:         1,
		Int:         1,
		Dex:         1,
		Luk:         1,
		CharNum:     0,
		HairColor:   0,
		MapName:     "prontera",
		Sex:         1,
	}

	var buf bytes.Buffer
	if err := ci.Encode(&buf); err != nil {
		t.Fatalf("Encode err = %v, want nil", err)
	}
	got := buf.Bytes()

	if len(got) != CharacterInfoSize {
		t.Fatalf("len = %d, want %d", len(got), CharacterInfoSize)
	}

	// uint32 GID at offset 0 (LE).
	if g := binary.LittleEndian.Uint32(got[0:4]); g != 1 {
		t.Errorf("GID at [0:4] = %d, want 1", g)
	}
	// int64 exp at offset 4 (LE).
	if e := binary.LittleEndian.Uint64(got[4:12]); e != 100 {
		t.Errorf("exp at [4:12] = %d, want 100", e)
	}
	// int64 hp at offset 50 (LE).
	if h := binary.LittleEndian.Uint64(got[50:58]); h != 50 {
		t.Errorf("HP at [50:58] = %d, want 50", h)
	}
	// int64 maxhp at offset 58 (LE).
	if h := binary.LittleEndian.Uint64(got[58:66]); h != 50 {
		t.Errorf("MaxHP at [58:66] = %d, want 50", h)
	}
	// int16 job at offset 84.
	if j := binary.LittleEndian.Uint16(got[84:86]); j != 0 {
		t.Errorf("job at [84:86] = %d, want 0 (Novice)", j)
	}
	// int16 level at offset 92.
	if l := binary.LittleEndian.Uint16(got[92:94]); l != 1 {
		t.Errorf("level at [92:94] = %d, want 1", l)
	}

	// char name[24] at offset 108 — "Test" zero-padded.
	wantName := make([]byte, nameSlot)
	copy(wantName, "Test")
	if !bytes.Equal(got[108:132], wantName) {
		t.Errorf("name slot at [108:132] = % x, want % x", got[108:132], wantName)
	}

	// Stat bytes 132..137.
	if got[132] != 1 || got[133] != 1 || got[134] != 1 || got[135] != 1 || got[136] != 1 || got[137] != 1 {
		t.Errorf("stats at [132:138] = %d %d %d %d %d %d, want all 1", got[132], got[133], got[134], got[135], got[136], got[137])
	}
	// CharNum at 138, HairColor at 139.
	if got[138] != 0 {
		t.Errorf("CharNum at 138 = %d, want 0", got[138])
	}
	if got[139] != 0 {
		t.Errorf("HairColor at 139 = %d, want 0", got[139])
	}

	// char mapName[16] at offset 142 — "prontera" zero-padded.
	wantMap := make([]byte, charMapNameSlot)
	copy(wantMap, "prontera")
	if !bytes.Equal(got[142:158], wantMap) {
		t.Errorf("mapName slot at [142:158] = % x, want % x", got[142:158], wantMap)
	}

	// sex at offset 174.
	if got[174] != 1 {
		t.Errorf("sex at 174 = %d, want 1", got[174])
	}
}

// TestCharacterInfo_Encode_NameTooLong asserts that a name exceeding 24 bytes
// returns the ErrCharNameTooLong sentinel and writes zero bytes to the
// output writer.
func TestCharacterInfo_Encode_NameTooLong(t *testing.T) {
	t.Parallel()

	tooLong := bytes.Repeat([]byte("A"), nameSlot+1)
	ci := CharacterInfo{
		GID:  1,
		Name: string(tooLong),
	}

	var buf bytes.Buffer
	err := ci.Encode(&buf)
	if err == nil {
		t.Fatalf("Encode err = nil, want error")
	}
	if !errors.Is(err, ErrCharNameTooLong) {
		t.Errorf("Encode err = %v, want errors.Is(.., ErrCharNameTooLong)", err)
	}
	if buf.Len() != 0 {
		t.Errorf("partial output written: %d bytes (want 0 on error)", buf.Len())
	}
}

// TestCharacterInfo_Encode_MapNameTooLong asserts that a mapName exceeding
// 16 bytes returns the ErrMapNameTooLong sentinel and writes zero bytes.
func TestCharacterInfo_Encode_MapNameTooLong(t *testing.T) {
	t.Parallel()

	tooLong := bytes.Repeat([]byte("M"), charMapNameSlot+1)
	ci := CharacterInfo{
		GID:     1,
		MapName: string(tooLong),
	}

	var buf bytes.Buffer
	err := ci.Encode(&buf)
	if err == nil {
		t.Fatalf("Encode err = nil, want error")
	}
	if !errors.Is(err, ErrMapNameTooLong) {
		t.Errorf("Encode err = %v, want errors.Is(.., ErrMapNameTooLong)", err)
	}
	if buf.Len() != 0 {
		t.Errorf("partial output written: %d bytes (want 0 on error)", buf.Len())
	}
}

// TestCharacterInfo_Size verifies the Size method returns the constant.
func TestCharacterInfo_Size(t *testing.T) {
	t.Parallel()

	if got := (CharacterInfo{}).Size(); got != CharacterInfoSize {
		t.Errorf("Size() = %d, want %d", got, CharacterInfoSize)
	}
}
