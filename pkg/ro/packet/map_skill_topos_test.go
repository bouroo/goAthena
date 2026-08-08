//go:build unit

package packet

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseCZUseSkillToPos(t *testing.T) {
	t.Parallel()
	// Known 11-byte frame: cmd 0x0AF4, skillLv 3, skillID 89, x 100, y 165,
	// moreinfo@10 (server-ignored, here 0xAB).
	goodFrame := func() []byte {
		f := make([]byte, sizeCZUseSkillToPos)
		writeLE16(f[0:], HeaderCZUSESKILLTOPOS)
		writeLE16(f[2:], 3)
		writeLE16(f[4:], 89)
		writeLE16(f[6:], 100)
		writeLE16(f[8:], 165)
		f[10] = 0xAB
		return f
	}()

	tests := []struct {
		name       string
		frame      []byte
		wantErr    bool
		wantErrSub string
		want       CZUseSkillToPos
	}{
		{
			name:    "valid frame decodes fields, discards moreinfo@10",
			frame:   goodFrame,
			wantErr: false,
			want:    CZUseSkillToPos{SkillLv: 3, SkillID: 89, X: 100, Y: 165},
		},
		{
			name:       "short frame reports byte count",
			frame:      make([]byte, sizeCZUseSkillToPos-1),
			wantErr:    true,
			wantErrSub: "want at least 11",
		},
		{
			name: "wrong cmd reports opcode",
			frame: func() []byte {
				f := make([]byte, sizeCZUseSkillToPos)
				writeLE16(f[0:], 0x0438)
				return f
			}(),
			wantErr:    true,
			wantErrSub: "unexpected cmd",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseCZUseSkillToPos(tt.frame)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErrSub)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestCZUseSkillToPos_EncodeRoundTrip(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	in := CZUseSkillToPos{SkillLv: 1, SkillID: 89, X: 155, Y: 165}
	require.NoError(t, in.Encode(&buf))
	assert.Len(t, buf.Bytes(), sizeCZUseSkillToPos, "CZ_USE_SKILL_TOPOS fixed 11 bytes")
	// moreinfo@10 is written 0 (server-ignored).
	assert.Equal(t, byte(0), buf.Bytes()[10], "moreinfo byte is 0 on the wire")
	out, err := ParseCZUseSkillToPos(buf.Bytes())
	require.NoError(t, err)
	assert.Equal(t, in, out, "encode → parse round-trips the ground-skill request")
}

func TestGroundSkillPoseEffect_Encode(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	require.NoError(t, (GroundSkillPoseEffect{
		SKID: 89, AID: 0x12345678, Level: 5, XPos: 100, YPos: 200, StartTime: 0xCAFEBABE,
	}).Encode(&buf))
	out := buf.Bytes()
	assert.Len(t, out, sizeZCNotifyGroundSkill, "ZC_NOTIFY_GROUNDSKILL fixed 18 bytes")
	assert.Equal(t, HeaderZCNOTIFYGROUNDSKILL, binary.LittleEndian.Uint16(out[0:2]), "opcode")
	assert.Equal(t, uint16(89), binary.LittleEndian.Uint16(out[2:4]), "SKID")
	assert.Equal(t, uint32(0x12345678), binary.LittleEndian.Uint32(out[4:8]), "AID caster")
	assert.Equal(t, int16(5), int16(binary.LittleEndian.Uint16(out[8:10])), "level")
	assert.Equal(t, int16(100), int16(binary.LittleEndian.Uint16(out[10:12])), "xPos")
	assert.Equal(t, int16(200), int16(binary.LittleEndian.Uint16(out[12:14])), "yPos")
	assert.Equal(t, uint32(0xCAFEBABE), binary.LittleEndian.Uint32(out[14:18]), "startTime")
	assert.Equal(t, 18, (GroundSkillPoseEffect{}).Size(), "Size()==18")
}
