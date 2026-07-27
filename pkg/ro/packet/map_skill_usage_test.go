//go:build unit

package packet

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// P3b-2: skill-usage codec tests — parse + encode round-trip for
// CZ_USE_SKILL2 (0x0438) and byte-exact Encode output for
// ZC_NOTIFY_SKILL (0x01de) and ZC_ACK_TOUSESKILL (0x0110). Layouts
// are pinned to rathena/src/map/packets_struct.hpp:2448-2460 (ZC_ACK_
// TOUSESKILL, PACKETVER_RE_NUM >= 20180704 branch) +
// :4658-4671 (ZC_NOTIFY_SKILL, PACKETVER >= 3 branch) and to
// rathena/src/map/clif_shuffle.hpp:4750 (CZ_USE_SKILL2, PACKETVER_RE
// _NUM >= 20190904 branch binds 0x0438 to clif_parse_UseSkillToId
// with offsets 2,4,6).

func TestParseCZUseSkill(t *testing.T) {
	t.Parallel()

	goodFrame := func() []byte {
		f := make([]byte, sizeCZUseSkill2)
		writeLE16(f[0:], HeaderCZUSESKILL)
		writeLE16(f[2:], 1)      // skillLv
		writeLE16(f[4:], 5)      // skillID (SM_BASH)
		writeLE32(f[6:], 0x1234) // targetID
		return f
	}()

	tests := []struct {
		name       string
		frame      []byte
		wantErr    bool
		wantErrSub string
		want       CZUseSkill
	}{
		{
			name:    "valid frame SM_BASH L1 on 0x1234",
			frame:   goodFrame,
			wantErr: false,
			want:    CZUseSkill{SkillLv: 1, SkillID: 5, TargetID: 0x1234},
		},
		{
			name:    "trailing bytes are ignored",
			frame:   append(goodFrame, 0x00, 0x00),
			wantErr: false,
			want:    CZUseSkill{SkillLv: 1, SkillID: 5, TargetID: 0x1234},
		},
		{
			name:       "frame too short",
			frame:      goodFrame[:sizeCZUseSkill2-1],
			wantErr:    true,
			wantErrSub: "want at least 10 bytes",
		},
		{
			name:       "wrong cmd",
			frame:      append([]byte(nil), append([]byte{0xff, 0xff}, goodFrame[2:]...)...),
			wantErr:    true,
			wantErrSub: "unexpected cmd",
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseCZUseSkill(tc.frame)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseCZUseSkill err = nil, want error")
				}
				if tc.wantErrSub != "" && !contains(err.Error(), tc.wantErrSub) {
					t.Errorf("ParseCZUseSkill err = %q, want substring %q", err.Error(), tc.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseCZUseSkill err = %v, want nil", err)
			}
			if got != tc.want {
				t.Errorf("ParseCZUseSkill = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestCZUseSkill_Encode(t *testing.T) {
	t.Parallel()

	r := CZUseSkill{SkillLv: 5, SkillID: 5, TargetID: 0x00002010}
	var buf bytes.Buffer
	require.NoError(t, r.Encode(&buf))
	got := buf.Bytes()

	require.Len(t, got, sizeCZUseSkill2, "frame length")
	// [2:cmd=0x0438 LE] → 38 04
	assert.Equal(t, []byte{0x38, 0x04}, got[0:2], "header bytes")
	// [2:skillLv=5 LE] → 05 00
	assert.Equal(t, int16(5), int16(binary.LittleEndian.Uint16(got[2:])), "skillLv")
	// [2:skillID=5 LE] → 05 00
	assert.Equal(t, uint16(5), binary.LittleEndian.Uint16(got[4:]), "skillID")
	// [4:targetID=0x2010 LE]
	assert.Equal(t, uint32(0x00002010), binary.LittleEndian.Uint32(got[6:]), "targetID")
}

func TestCZUseSkill_RoundTrip(t *testing.T) {
	t.Parallel()

	cases := []CZUseSkill{
		{SkillLv: 1, SkillID: 5, TargetID: 0x1234},
		{SkillLv: 10, SkillID: 5, TargetID: 0x7FFFFFFF},
		{SkillLv: -1, SkillID: 1, TargetID: 0}, // boundary: negative skillLv
	}
	for _, in := range cases {
		in := in
		t.Run("", func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			require.NoError(t, in.Encode(&buf))
			out, err := ParseCZUseSkill(buf.Bytes())
			require.NoError(t, err)
			assert.Equal(t, in, out, "round-trip preserves all fields")
		})
	}
}

func TestNotifySkillResponse_Encode_BashHit(t *testing.T) {
	t.Parallel()

	// SM_BASH L1 hit for 123 damage on a Poring.
	r := NotifySkillResponse{
		SKID:       5, // SM_BASH
		AID:        0x00001092,
		TargetID:   0x00002010,
		StartTime:  1_700_000_000,
		AttackMT:   0,
		AttackedMT: 0,
		Damage:     123,
		Level:      1,
		Count:      1, // DIV
		Action:     0, // DMG_NORMAL / ACT_ATTACK
	}
	var buf bytes.Buffer
	require.NoError(t, r.Encode(&buf))
	got := buf.Bytes()

	require.Len(t, got, sizeZCNotifySkill, "frame length must be 33")

	// Byte-exact expected layout (LE everywhere; int8.Action is the
	// last byte):
	//   [0..1]   cmd=0x01de       → de 01
	//   [2..3]   SKID=5           → 05 00
	//   [4..7]   AID=0x1092       → 92 10 00 00
	//   [8..11]  TargetID=0x2010  → 10 20 00 00
	//   [12..15] StartTime        → 00 c5 e4 65
	//   [16..19] AttackMT=0       → 00 00 00 00
	//   [20..23] AttackedMT=0     → 00 00 00 00
	//   [24..27] Damage=123       → 7b 00 00 00
	//   [28..29] Level=1          → 01 00
	//   [30..31] Count=1          → 01 00
	//   [32]     Action=0         → 00
	expected := make([]byte, sizeZCNotifySkill)
	expected[0] = 0xde
	expected[1] = 0x01
	expected[2] = 0x05
	binary.LittleEndian.PutUint32(expected[4:], 0x00001092)
	binary.LittleEndian.PutUint32(expected[8:], 0x00002010)
	binary.LittleEndian.PutUint32(expected[12:], 1_700_000_000)
	binary.LittleEndian.PutUint32(expected[24:], 123)
	binary.LittleEndian.PutUint16(expected[28:], 1)
	binary.LittleEndian.PutUint16(expected[30:], 1)
	// expected[16..23] stay 0 (AttackMT + AttackedMT), expected[32]=0 (Action).

	assert.Equal(t, expected, got, "byte-exact ZC_NOTIFY_SKILL frame for Bash L1")

	// Spot-check the field offsets against the spec (header size 2 +
	// SKID u16 + AID u32 + TargetID u32 + StartTime u32).
	assert.Equal(t, uint16(HeaderZCNOTIFYSKILL), binary.LittleEndian.Uint16(got[0:2]))
	assert.Equal(t, r.SKID, binary.LittleEndian.Uint16(got[2:4]))
	assert.Equal(t, r.AID, binary.LittleEndian.Uint32(got[4:8]))
	assert.Equal(t, r.TargetID, binary.LittleEndian.Uint32(got[8:12]))
	assert.Equal(t, r.Damage, int32(binary.LittleEndian.Uint32(got[24:28])))
	assert.Equal(t, r.Level, int16(binary.LittleEndian.Uint16(got[28:30])))
	assert.Equal(t, r.Count, int16(binary.LittleEndian.Uint16(got[30:32])))
	assert.Equal(t, uint8(r.Action), got[32])
}

func TestNotifySkillResponse_Encode_VariousValues(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   NotifySkillResponse
	}{
		{
			name: "all-zero frame",
			in:   NotifySkillResponse{},
		},
		{
			name: "negative damage slot encodes as unsigned 2^32 wrap",
			in:   NotifySkillResponse{SKID: 5, AID: 1, TargetID: 2, Damage: -1, Level: 1, Count: 1},
		},
		{
			name: "high level + multi-hit",
			in: NotifySkillResponse{
				SKID: 5, AID: 0xDEAD, TargetID: 0xBEEF, StartTime: 0xFFFFFFFF,
				AttackMT: -1, AttackedMT: -1, Damage: 0x7FFFFFFF,
				Level: 10, Count: 7, Action: -1,
			},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			require.NoError(t, tc.in.Encode(&buf))
			got := buf.Bytes()
			require.Len(t, got, sizeZCNotifySkill)
			assert.Equal(t, tc.in.SKID, binary.LittleEndian.Uint16(got[2:4]))
			assert.Equal(t, tc.in.AID, binary.LittleEndian.Uint32(got[4:8]))
			assert.Equal(t, tc.in.Damage, int32(binary.LittleEndian.Uint32(got[24:28])))
			assert.Equal(t, tc.in.Level, int16(binary.LittleEndian.Uint16(got[28:30])))
			assert.Equal(t, tc.in.Count, int16(binary.LittleEndian.Uint16(got[30:32])))
			assert.Equal(t, uint8(tc.in.Action), got[32])
		})
	}
}

func TestAckUseSkillResponse_Encode_SPInsufficient(t *testing.T) {
	t.Parallel()

	r := AckUseSkillResponse{
		SkillID: 5, // SM_BASH
		BType:   0, // BF_SHORT (pre-Renewal melee)
		ItemID:  0, // not item-driven
		Flag:    0, // hard fail
		Cause:   UseSkillFailSPInsufficient,
	}
	var buf bytes.Buffer
	require.NoError(t, r.Encode(&buf))
	got := buf.Bytes()

	require.Len(t, got, sizeZCAckToUseSkill, "frame length must be 14")
	// Byte-exact layout (LE everywhere):
	//   [0..1]   cmd=0x0110     → 10 01
	//   [2..3]   skillId=5      → 05 00
	//   [4..7]   btype=0        → 00 00 00 00
	//   [8..11]  itemId=0       → 00 00 00 00
	//   [12]     flag=0         → 00
	//   [13]     cause=12       → 0c
	expected := []byte{
		0x10, 0x01, // header
		0x05, 0x00, // skillId
		0x00, 0x00, 0x00, 0x00, // btype
		0x00, 0x00, 0x00, 0x00, // itemId
		0x00, // flag
		0x0c, // cause
	}
	assert.Equal(t, expected, got, "byte-exact ZC_ACK_TOUSESKILL frame for SP-insufficient Bash")

	assert.Equal(t, uint16(HeaderZCACKTOUSESKILL), binary.LittleEndian.Uint16(got[0:2]))
	assert.Equal(t, r.SkillID, binary.LittleEndian.Uint16(got[2:4]))
	assert.Equal(t, r.BType, int32(binary.LittleEndian.Uint32(got[4:8])))
	assert.Equal(t, r.ItemID, binary.LittleEndian.Uint32(got[8:12]))
	assert.Equal(t, r.Flag, got[12])
	assert.Equal(t, r.Cause, got[13])
}

func TestAckUseSkillResponse_Encode_VariousValues(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   AckUseSkillResponse
	}{
		{
			name: "all-zero frame",
			in:   AckUseSkillResponse{},
		},
		{
			name: "item-driven skill with max fields",
			in: AckUseSkillResponse{
				SkillID: 0xFFFF, BType: -1, ItemID: 0xFFFFFFFF, Flag: 0xFF, Cause: 0xFF,
			},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			require.NoError(t, tc.in.Encode(&buf))
			got := buf.Bytes()
			require.Len(t, got, sizeZCAckToUseSkill)
			assert.Equal(t, tc.in.SkillID, binary.LittleEndian.Uint16(got[2:4]))
			assert.Equal(t, tc.in.BType, int32(binary.LittleEndian.Uint32(got[4:8])))
			assert.Equal(t, tc.in.ItemID, binary.LittleEndian.Uint32(got[8:12]))
			assert.Equal(t, tc.in.Flag, got[12])
			assert.Equal(t, tc.in.Cause, got[13])
		})
	}
}

func TestSkillUsagePackets_DBEntries(t *testing.T) {
	t.Parallel()

	db := NewMapServerDB()
	cases := []struct {
		cmd       uint16
		name      string
		length    int
		direction Direction
	}{
		{HeaderCZUSESKILL, "CZ_USE_SKILL2", sizeCZUseSkill2, DirectionClientToServer},
		{HeaderZCNOTIFYSKILL, "ZC_NOTIFY_SKILL", sizeZCNotifySkill, DirectionServerToClient},
		{HeaderZCACKTOUSESKILL, "ZC_ACK_TOUSESKILL", sizeZCAckToUseSkill, DirectionServerToClient},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			def, ok := db.Lookup(c.cmd)
			require.True(t, ok, "Lookup(0x%04x) missing", c.cmd)
			assert.Equal(t, c.name, def.Name)
			assert.Equal(t, c.length, def.Length)
			assert.Equal(t, c.direction, def.Direction)
			gotLen, ok := db.Length(c.cmd)
			require.True(t, ok)
			assert.Equal(t, c.length, gotLen)
		})
	}
}
