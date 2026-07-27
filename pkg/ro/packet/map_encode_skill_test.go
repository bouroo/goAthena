//go:build unit

package packet

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// P2A: skill list encoder tests. These assert byte-exact output for
// the pre-2019 ZC_SKILLINFO_LIST (0x010f) variant: header + length +
// one 37-byte SKILLDATA per entry. The 0x0b32 (post-2019) variant is
// not produced here — the gateway only targets the pre-2019 client.

func TestSkillInfoListResponse_EmptyMatchesEncodeEmptySkillList(t *testing.T) {
	t.Parallel()

	var resp SkillInfoListResponse
	var buf bytes.Buffer
	require.NoError(t, resp.Encode(&buf))

	got := buf.Bytes()
	want := EncodeEmptySkillList()
	assert.Equal(t, []byte{0x0f, 0x01, 0x04, 0x00}, got,
		"empty SkillInfoListResponse must be the 4-byte header 0x010f + length 4")
	assert.Equal(t, want, got,
		"empty SkillInfoListResponse must match the shared emptySkillList bytes")
	assert.Len(t, got, sizeEmptyInventoryList)
	assert.Equal(t, uint16(HeaderZCSKILLINFOLIST), binary.LittleEndian.Uint16(got[0:2]))
	assert.Equal(t, uint16(4), binary.LittleEndian.Uint16(got[2:4]))
}

func TestSkillInfoListResponse_SingleNVBasic(t *testing.T) {
	t.Parallel()

	resp := SkillInfoListResponse{
		Skills: []SkillData{
			{
				ID:     1, // NV_BASIC
				Inf:    0,
				Level:  1,
				SP:     0,
				Range2: 0,
				Name:   "NV_BASIC",
				UpFlag: 0,
			},
		},
	}

	var buf bytes.Buffer
	require.NoError(t, resp.Encode(&buf))
	got := buf.Bytes()

	const wantLen = 4 + sizeSkillEntry // 41
	require.Len(t, got, wantLen, "total frame length")

	// Exact expected byte slice. Layout:
	//   [0..1]   cmd=0x010f LE         → 0f 01
	//   [2..3]   packetLength=41 LE    → 29 00
	//   [4..5]   id=1 LE               → 01 00
	//   [6..9]   inf=0 LE              → 00 00 00 00
	//   [10..11] level=1 LE            → 01 00
	//   [12..13] sp=0 LE               → 00 00
	//   [14..15] range2=0 LE           → 00 00
	//   [16..39] name="NV_BASIC" NUL-padded to 24 bytes
	//   [40]     upFlag=0
	expected := make([]byte, 4+sizeSkillEntry)
	expected[0] = 0x0f
	expected[1] = 0x01
	expected[2] = 0x29
	expected[3] = 0x00
	expected[4] = 0x01  // id low byte
	expected[10] = 0x01 // level low byte
	copy(expected[16:16+9], "NV_BASIC")
	// bytes 16+9..40 stay zero (NUL padding + upFlag).

	assert.Equal(t, expected, got, "byte-exact NV_BASIC frame")
}

func TestSkillInfoListResponse_TwoEntries(t *testing.T) {
	t.Parallel()

	resp := SkillInfoListResponse{
		Skills: []SkillData{
			{ID: 1, Inf: 0, Level: 1, SP: 0, Range2: 0, Name: "NV_BASIC", UpFlag: 0},
			{ID: 2, Inf: 0, Level: 1, SP: 0, Range2: 0, Name: "SM_SWORD", UpFlag: 1},
		},
	}

	var buf bytes.Buffer
	require.NoError(t, resp.Encode(&buf))
	got := buf.Bytes()

	const wantLen = 4 + 2*sizeSkillEntry // 78
	require.Len(t, got, wantLen)

	assert.Equal(t, uint16(HeaderZCSKILLINFOLIST), binary.LittleEndian.Uint16(got[0:2]))
	assert.Equal(t, uint16(wantLen), binary.LittleEndian.Uint16(got[2:4]),
		"packetLength = 4 + 2*37 = 78")

	// Second entry starts at off=4+37=41.
	off := 4 + sizeSkillEntry
	assert.Equal(t, uint16(2), binary.LittleEndian.Uint16(got[off:]),
		"second entry id")
	assert.Equal(t, uint8(1), got[off+36], "second entry upFlag")

	// Second entry name slot: "SM_SWORD" = 8 bytes, then 16 NULs.
	nameSlot := got[off+12 : off+12+24]
	assert.Equal(t, []byte("SM_SWORD"), nameSlot[:8])
	for i, b := range nameSlot[8:] {
		assert.Zero(t, b, "name padding byte %d must be NUL", i)
	}
	assert.Zero(t, nameSlot[23], "name slot must end with NUL")
}

func TestSkillInfoListResponse_LongNameTruncated(t *testing.T) {
	t.Parallel()

	long := "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-very-long-skill-name" // > 24 bytes
	require.Greater(t, len(long), 24, "test precondition: name must exceed slot")

	resp := SkillInfoListResponse{
		Skills: []SkillData{
			{ID: 99, Inf: 0, Level: 1, SP: 0, Range2: 0, Name: long, UpFlag: 0},
		},
	}

	var buf bytes.Buffer
	require.NoError(t, resp.Encode(&buf))
	got := buf.Bytes()

	require.Len(t, got, 4+sizeSkillEntry)

	nameSlot := got[4+12 : 4+12+24]
	// First 23 bytes of src, last byte forced to NUL.
	want := append([]byte(long[:23]), 0x00)
	assert.Equal(t, want, nameSlot,
		"long name is truncated to 23 usable bytes and the 24th byte is NUL")
	assert.Zero(t, nameSlot[23], "truncated name slot must end with NUL")

	// upFlag is still at off+36 regardless of name length.
	assert.Equal(t, uint8(0), got[4+36], "upFlag still at fixed offset")
}

func TestSkillInfoListResponse_OverflowRejected(t *testing.T) {
	t.Parallel()

	// 0xffff / 37 = 1772 entries max; build one over the limit.
	skills := make([]SkillData, 1773)
	for i := range skills {
		skills[i] = SkillData{ID: uint16(i + 1)}
	}
	resp := SkillInfoListResponse{Skills: skills}

	var buf bytes.Buffer
	err := resp.Encode(&buf)
	require.Error(t, err, "Encode must reject frames exceeding uint16 length")
}
