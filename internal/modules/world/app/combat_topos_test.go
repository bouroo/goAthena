//go:build unit

package app_test

import (
	"context"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bouroo/goAthena/pkg/ro/packet"
	"github.com/bouroo/goAthena/pkg/ro/skilldb"
)

// groundSkillDB returns a Registry carrying one MG_STORMGUST entry (rAthena Id
// 89, Type=Magic, TargetType=Ground) — the ground-target skill the M14d slice
// resolves. Range is a small scalar (1) so the range gate can be exercised with
// a tile adjacent to the caster (see TestCombatService_UseSkillToPos_OutOfRange):
// the caster stands at (101,100); Range 1 admits the Poring's tile (100,100)
// (Chebyshev 1) and rejects a tile one further (99,100) (Chebyshev 2) while that
// rejected tile still keeps the Poring inside the splash radius. SpCost is
// omitted so the zero-SP fixture attacker can cast (SpCostAt returns 0).
func groundSkillDB(t *testing.T) *skilldb.Registry {
	t.Helper()
	const yaml = `Header:
  Type: SKILL_DB
  Version: 4
Body:
  - Id: 89
    Name: MG_STORMGUST
    MaxLevel: 5
    Type: Magic
    TargetType: Ground
    Range: 1
`
	reg, err := skilldb.Load(strings.NewReader(yaml))
	require.NoError(t, err)
	return reg
}

// nonGroundSkillDB returns a Registry carrying a single SM_BASH entry whose
// TargetType is Attack (NOT Ground) and whose Range is positive, so the only
// thing that can reject a topos cast in TestCombatService_UseSkillToPos_NonGround
// is the isGroundSkill gate — the range gate (Chebyshev 1 <= Range 9) passes.
func nonGroundSkillDB(t *testing.T) *skilldb.Registry {
	t.Helper()
	const yaml = `Header:
  Type: SKILL_DB
  Version: 4
Body:
  - Id: 5
    Name: SM_BASH
    MaxLevel: 10
    Type: Weapon
    TargetType: Attack
    Range: 9
`
	reg, err := skilldb.Load(strings.NewReader(yaml))
	require.NoError(t, err)
	return reg
}

// findFrame returns the first captured frame carrying cmd, or nil if none.
func findFrame(t *testing.T, frames [][]byte, cmd uint16) []byte {
	t.Helper()
	for _, fr := range frames {
		if len(fr) >= 2 && binary.LittleEndian.Uint16(fr[0:2]) == cmd {
			return fr
		}
	}
	return nil
}

// TestCombatService_UseSkillToPos_HitsMobInSplash proves the happy path: casting
// a ground skill on the Poring's tile (100,100) broadcasts ZC_NOTIFY_GROUNDSKILL
// (the poseffect: caster=AID, tile=x/y) and ZC_NOTIFY_SKILL (per-mob damage) and
// applies a hit to the mob. Str/Dex are raised so the MVP's melee computeDamage
// clears 0 (a naked novice all-1s can floor at 0). The cast tile is Chebyshev 1
// from the caster (101,100), within Range 1.
func TestCombatService_UseSkillToPos_HitsMobInSplash(t *testing.T) {
	f := newCombatFixture(t, 1002, 1000, 1, nil, withSkillDB(groundSkillDB(t)))
	f.attacker.Str = 50
	f.attacker.Dex = 50

	err := f.svc.UseSkillToPos(context.Background(), f.attacker.AccountID, packet.CZUseSkillToPos{
		SkillLv: 1, SkillID: 89, X: 100, Y: 100, // the Poring's tile, Chebyshev 1 from caster
	})
	require.NoError(t, err)

	frames := splitFixedFrames(t, f.conn.buf.Bytes())
	pose := findFrame(t, frames, packet.HeaderZCNOTIFYGROUNDSKILL)
	require.NotNil(t, pose, "expected a ZC_NOTIFY_GROUNDSKILL poseffect frame")
	assert.Equal(t, uint16(89), binary.LittleEndian.Uint16(pose[2:4]), "poseffect SKID = 89")
	assert.Equal(t, f.attacker.AccountID, binary.LittleEndian.Uint32(pose[4:8]), "poseffect AID = caster")
	assert.Equal(t, uint16(100), binary.LittleEndian.Uint16(pose[10:12]), "poseffect xPos = cast tile")
	assert.Equal(t, uint16(100), binary.LittleEndian.Uint16(pose[12:14]), "poseffect yPos = cast tile")

	require.NotNil(t, findFrame(t, frames, packet.HeaderZCNOTIFYSKILL), "expected a ZC_NOTIFY_SKILL damage frame")
	assert.Less(t, f.mob.HP, int32(1000), "the mob took damage from the ground skill")
}

// TestCombatService_UseSkillToPos_OutOfRangeNoOp proves the Chebyshev range gate
// SOLELY rejects an out-of-range cast. The cast tile (99,100) is Chebyshev 2 from
// the caster (101,100) — beyond Range 1 — YET the Poring (100,100) is inside the
// 3x3 splash of (99,100) and the caster is in view of it, so without the range
// gate the poseffect would be emitted and the mob hit. The observed no-poseffect
// / no-damage outcome is therefore attributable only to the range gate.
func TestCombatService_UseSkillToPos_OutOfRangeNoOp(t *testing.T) {
	f := newCombatFixture(t, 1002, 1000, 1, nil, withSkillDB(groundSkillDB(t)))
	f.attacker.Str = 50
	f.attacker.Dex = 50

	err := f.svc.UseSkillToPos(context.Background(), f.attacker.AccountID, packet.CZUseSkillToPos{
		SkillLv: 1, SkillID: 89, X: 99, Y: 100, // Chebyshev 2 from caster > Range 1; Poring (100,100) still in splash
	})
	require.NoError(t, err)

	frames := splitFixedFrames(t, f.conn.buf.Bytes())
	assert.Nil(t, findFrame(t, frames, packet.HeaderZCNOTIFYGROUNDSKILL), "out-of-range cast sends no poseffect")
	assert.Equal(t, int32(1000), f.mob.HP, "out-of-range cast deals no damage despite the mob being in the splash")
}

// TestCombatService_UseSkillToPos_NonGroundNoOp proves the isGroundSkill gate
// SOLELY rejects a non-ground skill. SM_BASH (Id 5, TargetType Attack, Range 9)
// is cast on the Poring's tile (Chebyshev 1 <= Range 9, so the range gate would
// pass); only TargetType != "Ground" rejects it. Without the isGroundSkill gate
// the poseffect would be emitted and the mob hit.
func TestCombatService_UseSkillToPos_NonGroundNoOp(t *testing.T) {
	f := newCombatFixture(t, 1002, 1000, 1, nil, withSkillDB(nonGroundSkillDB(t)))
	f.attacker.Str = 50
	f.attacker.Dex = 50

	err := f.svc.UseSkillToPos(context.Background(), f.attacker.AccountID, packet.CZUseSkillToPos{
		SkillLv: 1, SkillID: 5, X: 100, Y: 100, // Chebyshev 1 <= Range 9; only isGroundSkill can reject
	})
	require.NoError(t, err)

	frames := splitFixedFrames(t, f.conn.buf.Bytes())
	assert.Nil(t, findFrame(t, frames, packet.HeaderZCNOTIFYGROUNDSKILL), "non-ground skill sends no poseffect")
	assert.Equal(t, int32(1000), f.mob.HP, "non-ground skill deals no damage despite being in range")
}
