//go:build unit

// Package app: combat_skill_internal_test.go holds the unit tests for the
// unexported skill-cast helpers (skillDamage, bashSkillRatio, clampSkillLevel).
// It lives in the internal package app (not app_test) so the tests can call the
// unexported helpers directly without an export_test.go seam — the helpers are
// pure functions whose semantics are part of the skill-cast contract. The
// behaviour-level UseSkill tests (state changes, frame emission, SP spending)
// live in combat_test.go under package app_test.
//
// golangci-lint's `unused` check never sees this file: `task lint` runs without
// the unit build tag, so this file is excluded from the lint build. When
// `task test-unit` compiles it (tag present), the assertions reference the
// helpers so they are not unused there either.
package app

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestSkillDamage_RatioMatchesPreRenewalBash locks the SM_BASH damage formula to
// rAthena's bash.cpp:14 (`base_skillratio += 30 * skill_lv`). At level 10 the
// ratio is 400% (100 + 30×10) so a 1-dmg melee hit becomes 4 dmg — the e2e
// damage math the kill-test relies on.
func TestSkillDamage_RatioMatchesPreRenewalBash(t *testing.T) {
	t.Parallel()
	cases := []struct {
		lvl  int32
		base int32
		want int32
	}{
		{1, 1, 1},  // 130% × 1 / 100 = 1 (1.3 → integer division floor)
		{5, 1, 2},  // 250% × 1 / 100 = 2 (2.5 → floor)
		{6, 1, 2},  // 280% × 1 / 100 = 2
		{10, 1, 4}, // 400% × 1 / 100 = 4
		{10, 10, 40},
		{10, 50, 200},
		{10, -7, 0}, // negative base clamps to 0 (a negative hit should never pass through Attack, but skillDamage guards it)
	}
	for _, tc := range cases {
		got := skillDamage(tc.base, tc.lvl)
		assert.Equalf(t, tc.want, got, "skillDamage(base=%d, level=%d)", tc.base, tc.lvl)
	}
}

// TestBashSkillRatio_PerLevel guards the formula directly so a future edit to
// the ratio (a downstream "balance" pass, say) surfaces as a unit failure
// rather than an e2e mismatch. The 100+30×level progression matches rAthena's
// bash.cpp:14.
func TestBashSkillRatio_PerLevel(t *testing.T) {
	t.Parallel()
	for lvl := int32(1); lvl <= 10; lvl++ {
		assert.Equal(t, int32(100+30*lvl), bashSkillRatio(lvl),
			"SM_BASH ratio at level %d", lvl)
	}
}

// TestClampSkillLevel_BoundsAndValidRange guards the client-claimed level
// clamp: levels > MaxLevel resolve at MaxLevel (a level-0 cast resolves at 1).
// UseSkill relies on this so a malicious client cannot pay less SP by claiming
// a low level on a high-cost skill or vice-versa.
func TestClampSkillLevel_BoundsAndValidRange(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in  int16
		max int32
		out int32
	}{
		{1, 10, 1},
		{5, 10, 5},
		{10, 10, 10},
		{11, 10, 10}, // above max → clamp to max
		{50, 10, 10},
		{0, 10, 1}, // below 1 → clamp to 1
		{-7, 10, 1},
		{1, 0, 1}, // max<=0 means no cap; clamp only the lower bound
		{1, -5, 1},
	}
	for _, tc := range cases {
		assert.Equalf(t, tc.out, clampSkillLevel(tc.in, tc.max),
			"clampSkillLevel(lv=%d, max=%d)", tc.in, tc.max)
	}
}
