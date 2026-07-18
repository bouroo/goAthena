//go:build unit

package skilltree

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const happyNovice = `Header:
  Type: SKILL_TREE_DB
  Version: 1
Body:
  - Job: Novice
    Tree:
      - Name: NV_BASIC
        MaxLevel: 9
      - Name: NV_FIRSTAID
        MaxLevel: 1
`

const singleHopInherit = `Header:
  Type: SKILL_TREE_DB
  Version: 1
Body:
  - Job: Novice
    Tree:
      - Name: NV_BASIC
        MaxLevel: 9
  - Job: Swordman
    Inherit:
      Novice: true
    Tree:
      - Name: SM_BASH
        MaxLevel: 10
`

// diamondInherit builds the diamond case:
//
//	A (has X)
//	B inherits A
//	C inherits A
//	D inherits {B, C}
//
// Single-hop means D pulls from B.own and C.own only, NOT A.own. Since B.own
// and C.own are empty, D's tree is empty. This matches rAthena's behavior.
const diamondInherit = `Header:
  Type: SKILL_TREE_DB
  Version: 1
Body:
  - Job: A
    Tree:
      - Name: SKILL_A_ONLY
        MaxLevel: 1
  - Job: B
    Inherit:
      A: true
    Tree:
      - Name: SKILL_B_OWN
        MaxLevel: 1
  - Job: C
    Inherit:
      A: true
    Tree:
      - Name: SKILL_C_OWN
        MaxLevel: 1
  - Job: D
    Inherit:
      B: true
      C: true
    Tree:
      - Name: SKILL_D_OWN
        MaxLevel: 1
`

const lordKnightStyle = `Header:
  Type: SKILL_TREE_DB
  Version: 1
Body:
  - Job: Novice
    Tree:
      - Name: NV_BASIC
        MaxLevel: 9
  - Job: Swordman
    Inherit:
      Novice: true
    Tree:
      - Name: SM_BASH
        MaxLevel: 10
  - Job: Knight
    Inherit:
      Novice: true
      Swordman: true
    Tree:
      - Name: KN_SPEARMASTERY
        MaxLevel: 10
  - Job: Lord_Knight
    Inherit:
      Novice: true
      Swordman: true
      Knight: true
    Tree:
      - Name: LK_AURABLADE
        MaxLevel: 5
`

const excludeBlocks = `Header:
  Type: SKILL_TREE_DB
  Version: 1
Body:
  - Job: Novice
    Tree:
      - Name: NV_BASIC
        MaxLevel: 9
      - Name: NV_TRICKDEAD
        MaxLevel: 1
        Exclude: true
  - Job: Swordman
    Inherit:
      Novice: true
    Tree:
      - Name: SM_BASH
        MaxLevel: 10
`

const ownSkillsWin = `Header:
  Type: SKILL_TREE_DB
  Version: 1
Body:
  - Job: A
    Tree:
      - Name: NV_BASIC
        MaxLevel: 9
  - Job: J
    Inherit:
      A: true
    Tree:
      - Name: NV_BASIC
        MaxLevel: 5
`

const fixturePrune = `Header:
  Type: SKILL_TREE_DB
  Version: 1
Body:
  - Job: A
    Tree:
      - Name: SKILL_GONE
        MaxLevel: 0
  - Job: J
    Inherit:
      A: true
    Tree:
      - Name: SKILL_ZERO_INHERITED
        MaxLevel: 0
      - Name: SKILL_OK
        MaxLevel: 1
`

const rejectBadHeaderType = `Header:
  Type: BOGUS
  Version: 1
Body:
  - Job: A
    Tree:
      - Name: SKILL_X
        MaxLevel: 1
`

const rejectBadVersion = `Header:
  Type: SKILL_TREE_DB
  Version: 99
Body:
  - Job: A
    Tree:
      - Name: SKILL_X
        MaxLevel: 1
`

const rejectEmptyBody = `Header:
  Type: SKILL_TREE_DB
  Version: 1
Body: []
`

const rejectDuplicateOwn = `Header:
  Type: SKILL_TREE_DB
  Version: 1
Body:
  - Job: A
    Tree:
      - Name: SKILL_X
        MaxLevel: 1
      - Name: SKILL_X
        MaxLevel: 2
`

const rejectUnknownAncestor = `Header:
  Type: SKILL_TREE_DB
  Version: 1
Body:
  - Job: A
    Tree:
      - Name: SKILL_X
        MaxLevel: 1
  - Job: B
    Inherit:
      NotARealJob: true
    Tree:
      - Name: SKILL_Y
        MaxLevel: 1
`

const rejectCircularInherit = `Header:
  Type: SKILL_TREE_DB
  Version: 1
Body:
  - Job: A
    Inherit:
      B: true
    Tree:
      - Name: SKILL_A
        MaxLevel: 1
  - Job: B
    Inherit:
      A: true
    Tree:
      - Name: SKILL_B
        MaxLevel: 1
`

func TestLoad_Happy_NoviceOnly(t *testing.T) {
	t.Parallel()
	reg, err := Load(bytes.NewReader([]byte(happyNovice)))
	require.NoError(t, err)
	require.NotNil(t, reg)
	assert.Equal(t, 1, reg.Len())
	jt, ok := reg.Get("Novice")
	require.True(t, ok)
	require.NotNil(t, jt)
	assert.Equal(t, "Novice", jt.Job)
	assert.Len(t, jt.Skills, 2)
	_, has := jt.Skills["NV_BASIC"]
	assert.True(t, has)
	_, has = jt.Skills["NV_FIRSTAID"]
	assert.True(t, has)
}

func TestLoad_SingleHopInherit(t *testing.T) {
	t.Parallel()
	reg, err := Load(bytes.NewReader([]byte(singleHopInherit)))
	require.NoError(t, err)
	require.Equal(t, 2, reg.Len())
	sw, ok := reg.Get("Swordman")
	require.True(t, ok)
	assert.Len(t, sw.Skills, 2, "Swordman should have NV_BASIC (inherited) + SM_BASH (own)")
	_, has := sw.Skills["NV_BASIC"]
	assert.True(t, has)
	_, has = sw.Skills["SM_BASH"]
	assert.True(t, has)
}

func TestLoad_DiamondInherit(t *testing.T) {
	t.Parallel()
	// Diamond: A has X; B inherits A; C inherits A; D inherits {B, C}.
	// Inheritance is single-hop from each ancestor's OWN tree. B.own and
	// C.own do not contain SKILL_A_ONLY, so D does NOT get it. rAthena
	// matches this behavior (pc.cpp SkillTreeDatabase::loadingFinished).
	reg, err := Load(bytes.NewReader([]byte(diamondInherit)))
	require.NoError(t, err)
	d, ok := reg.Get("D")
	require.True(t, ok)
	// D's resolved tree = B.own (SKILL_B_OWN) + C.own (SKILL_C_OWN) + D.own
	// (SKILL_D_OWN). A's SKILL_A_ONLY must NOT appear: single-hop inheritance
	// uses each ancestor's own parsed tree, not the ancestor's resolved tree.
	assert.Len(t, d.Skills, 3, "D should have B.own + C.own + D.own")
	for _, sn := range []string{"SKILL_B_OWN", "SKILL_C_OWN", "SKILL_D_OWN"} {
		_, has := d.Skills[sn]
		assert.True(t, has, "D should have %s", sn)
	}
	_, has := d.Skills["SKILL_A_ONLY"]
	assert.False(t, has, "single-hop inheritance must NOT pull A's X through B and C")
}

func TestLoad_LordKnightStyle(t *testing.T) {
	t.Parallel()
	reg, err := Load(bytes.NewReader([]byte(lordKnightStyle)))
	require.NoError(t, err)
	lk, ok := reg.Get("Lord_Knight")
	require.True(t, ok)
	// All three ancestors' own skills must be present.
	for _, name := range []string{"NV_BASIC", "SM_BASH", "KN_SPEARMASTERY", "LK_AURABLADE"} {
		_, has := lk.Skills[name]
		assert.True(t, has, "Lord_Knight should have %s", name)
	}
	// Phase-15 regression guard: NV_BASIC entry pointer must NOT be shared
	// between Novice and Lord_Knight.
	nov, _ := reg.Get("Novice")
	require.NotNil(t, nov.Skills["NV_BASIC"])
	require.NotNil(t, lk.Skills["NV_BASIC"])
	assert.NotSame(t, nov.Skills["NV_BASIC"], lk.Skills["NV_BASIC"],
		"NV_BASIC *SkillEntry must be unique per job")
	nov.Skills["NV_BASIC"].MaxLevel = 99
	assert.Equal(t, int16(9), lk.Skills["NV_BASIC"].MaxLevel,
		"mutating Novice's NV_BASIC must not affect Lord_Knight's copy")
}

func TestLoad_ExcludePreventsInheritance(t *testing.T) {
	t.Parallel()
	reg, err := Load(bytes.NewReader([]byte(excludeBlocks)))
	require.NoError(t, err)
	nov, _ := reg.Get("Novice")
	_, has := nov.Skills["NV_TRICKDEAD"]
	assert.True(t, has, "Novice's own tree still has NV_TRICKDEAD")
	sw, _ := reg.Get("Swordman")
	_, has = sw.Skills["NV_TRICKDEAD"]
	assert.False(t, has, "Excluded skill must not be inherited by Swordman")
}

func TestLoad_OwnSkillsWin(t *testing.T) {
	t.Parallel()
	reg, err := Load(bytes.NewReader([]byte(ownSkillsWin)))
	require.NoError(t, err)
	j, _ := reg.Get("J")
	require.NotNil(t, j)
	entry, has := j.Skills["NV_BASIC"]
	require.True(t, has)
	assert.Equal(t, int16(5), entry.MaxLevel, "own MaxLevel wins over inherited")
}

func TestLoad_PruneMaxLevelZero(t *testing.T) {
	t.Parallel()
	reg, err := Load(bytes.NewReader([]byte(fixturePrune)))
	require.NoError(t, err)
	a, _ := reg.Get("A")
	assert.Empty(t, a.Skills, "MaxLevel=0 own skill is pruned")
	j, _ := reg.Get("J")
	_, has := j.Skills["SKILL_ZERO_INHERITED"]
	assert.False(t, has, "MaxLevel=0 inherited skill is pruned")
	_, has = j.Skills["SKILL_OK"]
	assert.True(t, has)
}

func TestLoad_RejectBadHeaderType(t *testing.T) {
	t.Parallel()
	_, err := Load(bytes.NewReader([]byte(rejectBadHeaderType)))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "BOGUS")
	assert.Contains(t, err.Error(), "SKILL_TREE_DB")
}

func TestLoad_RejectBadVersion(t *testing.T) {
	t.Parallel()
	_, err := Load(bytes.NewReader([]byte(rejectBadVersion)))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "99")
	assert.Contains(t, err.Error(), "1")
}

func TestLoad_RejectEmptyBody(t *testing.T) {
	t.Parallel()
	_, err := Load(bytes.NewReader([]byte(rejectEmptyBody)))
	require.Error(t, err)
}

func TestLoad_RejectDuplicateOwnSkill(t *testing.T) {
	t.Parallel()
	_, err := Load(bytes.NewReader([]byte(rejectDuplicateOwn)))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate")
}

func TestLoad_RejectUnknownAncestor(t *testing.T) {
	t.Parallel()
	_, err := Load(bytes.NewReader([]byte(rejectUnknownAncestor)))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "NotARealJob")
}

func TestLoad_RejectCircularInherit(t *testing.T) {
	t.Parallel()
	_, err := Load(bytes.NewReader([]byte(rejectCircularInherit)))
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrCircularInherit), "must wrap ErrCircularInherit")
}

func TestLoad_NilRegistrySafe(t *testing.T) {
	t.Parallel()
	var reg *Registry
	_, ok := reg.Get("X")
	assert.False(t, ok)
	assert.Equal(t, 0, reg.Len())
	assert.Nil(t, reg.Jobs())
}

func TestLoadFile_RealFile(t *testing.T) {
	t.Parallel()
	const rel = "../../../third_party/rathena/db/pre-re/skill_tree.yml"
	abs, _ := filepath.Abs(rel)
	if _, err := os.Stat(abs); err != nil {
		t.Skipf("real rAthena skill_tree.yml not present at %s: %v", abs, err)
	}
	reg, err := LoadFile(abs)
	require.NoError(t, err)
	require.NotNil(t, reg)
	assert.GreaterOrEqual(t, reg.Len(), 70, "real file has 70+ jobs")
	nov, ok := reg.Get("Novice")
	require.True(t, ok)
	_, has := nov.Skills["NV_BASIC"]
	assert.True(t, has)
	lk, ok := reg.Get("Lord_Knight")
	require.True(t, ok)
	_, has = lk.Skills["NV_BASIC"]
	assert.True(t, has, "Lord_Knight inherits NV_BASIC from Novice")
}

func TestLoad_NilRequirementIsSkipped(t *testing.T) {
	t.Parallel()
	// Approach: call the unexported parseOwnSkills directly with a hand-built
	// []*skillEntryInner that contains a nil pointer in Requires. This is the
	// reliable way to reproduce a nil-hole: yaml.v3's typed decode rejects `- `
	// (empty list element) inside a []*requirementInner, so a YAML-stream test
	// would fail at decode before ever reaching parseOwnSkills. By constructing
	// the inner slice in code, we exercise the exact branch the fix targets.
	entries := []*skillEntryInner{
		{
			Name:     "SKILL_WITH_NIL_REQ",
			MaxLevel: 3,
			Requires: []*requirementInner{
				nil,
				{Name: "X", Level: 1},
			},
		},
	}
	out, err := parseOwnSkills("J", entries)
	require.NoError(t, err)
	require.Len(t, out, 1)
	got, ok := out["SKILL_WITH_NIL_REQ"]
	require.True(t, ok)
	require.NotNil(t, got)
	// Regression guard: nil entries must not leave zero-valued holes in the
	// pre-allocated slice. The slice must contain exactly one real prereq.
	require.Len(t, got.Requires, 1, "nil requirement must be skipped, not zero-padded")
	assert.Equal(t, SkillRequirement{Name: "X", Level: 1}, got.Requires[0])

	// Also confirm the YAML-stream path (no nil case) still works, so this
	// test does not silently mask a regression in the decode + parse pipeline.
	const streamOK = `Header:
  Type: SKILL_TREE_DB
  Version: 1
Body:
  - Job: J
    Tree:
      - Name: SKILL_OK
        MaxLevel: 1
        Requires:
          - Name: X
            Level: 1
`
	reg, err := Load(strings.NewReader(streamOK))
	require.NoError(t, err)
	require.Equal(t, 1, reg.Len())
	j, ok := reg.Get("J")
	require.True(t, ok)
	okSkill, has := j.Skills["SKILL_OK"]
	require.True(t, has)
	require.Len(t, okSkill.Requires, 1)
	assert.Equal(t, SkillRequirement{Name: "X", Level: 1}, okSkill.Requires[0])
}

