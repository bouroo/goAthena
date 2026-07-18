//go:build unit

package domain

import (
	"strings"
	"testing"

	"github.com/bouroo/goAthena/pkg/ro/jobdb"
)

func TestStatusPointGrant(t *testing.T) {
	cases := []struct {
		level uint32
		want  uint32
	}{
		{1, 3},   // level 1->2 grants 51-48 = 3
		{2, 3},   // 54-51 = 3
		{5, 4},   // 64-60 = 4
		{10, 5},  // 85-80 = 5
		{15, 6},  // 111-105 = 6
		{98, 22}, // 1273-1251 = 22
	}
	for _, c := range cases {
		if got := StatusPointGrant(c.level); got != c.want {
			t.Errorf("StatusPointGrant(%d) = %d, want %d", c.level, got, c.want)
		}
	}
}

func TestStatPointsTotal(t *testing.T) {
	if got := StatPointsTotal(1); got != 48 {
		t.Errorf("StatPointsTotal(1) = %d, want 48", got)
	}
	if got := StatPointsTotal(99); got != 1273 {
		t.Errorf("StatPointsTotal(99) = %d, want 1273", got)
	}
	if got := StatPointsTotal(0); got != 0 {
		t.Errorf("StatPointsTotal(0) = %d, want 0", got)
	}
	// Above the table clamps to the max total.
	if got := StatPointsTotal(200); got != 1273 {
		t.Errorf("StatPointsTotal(200) = %d, want 1273 (clamp)", got)
	}
}

func TestBaseExpForLevel(t *testing.T) {
	if got := BaseExpForLevel(1); got != 9 {
		t.Errorf("BaseExpForLevel(1) = %d, want 9", got)
	}
	if got := BaseExpForLevel(2); got != 16 {
		t.Errorf("BaseExpForLevel(2) = %d, want 16", got)
	}
	if got := BaseExpForLevel(98); got != 99999998 {
		t.Errorf("BaseExpForLevel(98) = %d, want 99999998", got)
	}
	if got := BaseExpForLevel(99); got != 0 {
		t.Errorf("BaseExpForLevel(99) = %d, want 0 (max level)", got)
	}
	if got := BaseExpForLevel(0); got != 0 {
		t.Errorf("BaseExpForLevel(0) = %d, want 0", got)
	}
}

func TestApplyBaseExpGain_NoLevelUp(t *testing.T) {
	// Level 1, threshold 9. Gain 5 -> no level-up, exp=5.
	r := ApplyBaseExpGain(1, 0, 5)
	if r.LeveledUp || r.NewLevel != 1 || r.NewExp != 5 || r.GrantedStatusPoints != 0 {
		t.Errorf("ApplyBaseExpGain(1,0,5) = %+v, want {1 5 0 false}", r)
	}
}

func TestApplyBaseExpGain_ExactLevelUp(t *testing.T) {
	// Gain exactly the threshold (9) -> level-up, exp resets to 0.
	r := ApplyBaseExpGain(1, 0, 9)
	if !r.LeveledUp || r.NewLevel != 2 || r.NewExp != 0 || r.GrantedStatusPoints != 3 {
		t.Errorf("ApplyBaseExpGain(1,0,9) = %+v, want {2 0 3 true}", r)
	}
}

func TestApplyBaseExpGain_Carryover(t *testing.T) {
	// Level 1, exp 8, gain 5 -> 13. Threshold 9 -> level 2, carry 4.
	r := ApplyBaseExpGain(1, 8, 5)
	if !r.LeveledUp || r.NewLevel != 2 || r.NewExp != 4 || r.GrantedStatusPoints != 3 {
		t.Errorf("ApplyBaseExpGain(1,8,5) = %+v, want {2 4 3 true}", r)
	}
}

func TestApplyBaseExpGain_MultiLevelUp(t *testing.T) {
	// Level 1, gain enough for 3 levels: thresholds 9+16+25 = 50.
	// Gain 55 -> level 4, exp 5 (55-50), grants 3+3+3 = 9.
	r := ApplyBaseExpGain(1, 0, 55)
	if !r.LeveledUp || r.NewLevel != 4 || r.NewExp != 5 || r.GrantedStatusPoints != 9 {
		t.Errorf("ApplyBaseExpGain(1,0,55) = %+v, want {4 5 9 true}", r)
	}
}

func TestApplyBaseExpGain_MaxLevelCap(t *testing.T) {
	// Already at max level: a gain pushing exp past the cap is clamped.
	r := ApplyBaseExpGain(99, 50, 200_000_000)
	if r.LeveledUp || r.NewLevel != 99 {
		t.Errorf("max-level gain should not level up, got %+v", r)
	}
	if r.NewExp != MaxLevelExp {
		t.Errorf("max-level exp = %d, want %d", r.NewExp, MaxLevelExp)
	}
}

func TestApplyBaseExpGain_LevelIntoMax(t *testing.T) {
	// Level 98, threshold 99999998. Gain enough to hit 99.
	r := ApplyBaseExpGain(98, 0, 99999998)
	if !r.LeveledUp || r.NewLevel != 99 {
		t.Errorf("level 98->99 should level up, got %+v", r)
	}
	// Grant for level 98->99 is StatusPointGrant(98) = 22.
	if r.GrantedStatusPoints != 22 {
		t.Errorf("granted = %d, want 22", r.GrantedStatusPoints)
	}
	if r.NewExp != 0 {
		t.Errorf("new exp = %d, want 0", r.NewExp)
	}
}

// jobExpYAML returns a JOB_STATS v4 job_exp.yml fragment with two jobs
// (Novice + Swordman) carrying distinctive BaseExp curves, suitable for
// loading via jobdb.Load in tests.
func jobExpYAML() string {
	return `Header:
  Type: JOB_STATS
  Version: 4
Body:
  - Jobs:
      Novice: true
    MaxBaseLevel: 60
    BaseExp:
      - Level: 1
        Exp: 11
      - Level: 2
        Exp: 22
      - Level: 3
        Exp: 33
      - Level: 10
        Exp: 100
      - Level: 60
        Exp: 600
  - Jobs:
      Swordman: true
    MaxBaseLevel: 70
    BaseExp:
      - Level: 1
        Exp: 100
      - Level: 2
        Exp: 200
`
}

func TestBaseExpForJobLevel_Fallback(t *testing.T) {
	// Ensure no registry is configured (other tests may have set one).
	SetExpRegistry(nil)
	t.Cleanup(func() { SetExpRegistry(nil) })

	if got := BaseExpForJobLevel("", 1); got != 9 {
		t.Errorf("fallback level 1 = %d, want 9", got)
	}
	if got := BaseExpForJobLevel("", 2); got != 16 {
		t.Errorf("fallback level 2 = %d, want 16", got)
	}
	if got := BaseExpForJobLevel("", 98); got != 99999998 {
		t.Errorf("fallback level 98 = %d, want 99999998", got)
	}
	if got := BaseExpForJobLevel("", 0); got != 0 {
		t.Errorf("fallback level 0 = %d, want 0", got)
	}
	if got := BaseExpForJobLevel("", 99); got != 0 {
		t.Errorf("fallback level 99 = %d, want 0", got)
	}
}

func TestBaseExpForJobLevel_WithRegistry(t *testing.T) {
	db, err := jobdb.Load(strings.NewReader(jobExpYAML()))
	if err != nil {
		t.Fatalf("jobdb.Load: %v", err)
	}
	if db.Len() != 2 {
		t.Fatalf("registry len = %d, want 2", db.Len())
	}

	reg := NewExpRegistryFromJobDB(db)
	if reg == nil {
		t.Fatal("NewExpRegistryFromJobDB returned nil for non-empty registry")
	}
	SetExpRegistry(reg)
	t.Cleanup(func() { SetExpRegistry(nil) })

	// Novice curve from the YAML.
	if got := BaseExpForJobLevel("Novice", 1); got != 11 {
		t.Errorf("Novice level 1 = %d, want 11", got)
	}
	if got := BaseExpForJobLevel("Novice", 10); got != 100 {
		t.Errorf("Novice level 10 = %d, want 100", got)
	}

	// Swordman curve.
	if got := BaseExpForJobLevel("Swordman", 1); got != 100 {
		t.Errorf("Swordman level 1 = %d, want 100", got)
	}
	if got := BaseExpForJobLevel("Swordman", 2); got != 200 {
		t.Errorf("Swordman level 2 = %d, want 200", got)
	}

	// Reset and confirm fallback returns.
	SetExpRegistry(nil)
	if got := BaseExpForJobLevel("", 1); got != 9 {
		t.Errorf("post-reset fallback level 1 = %d, want 9", got)
	}
}

func TestApplyBaseExpGainForJob_UsesRegistry(t *testing.T) {
	db, err := jobdb.Load(strings.NewReader(jobExpYAML()))
	if err != nil {
		t.Fatalf("jobdb.Load: %v", err)
	}
	SetExpRegistry(NewExpRegistryFromJobDB(db))
	t.Cleanup(func() { SetExpRegistry(nil) })

	// Novice thresholds: 11, 22, 33, ... — gain 30 should yield two
	// level-ups (11+22=33 -> level 3, carry 0 with three grants of 3 each).
	r := ApplyBaseExpGainForJob("Novice", 1, 0, 33)
	if !r.LeveledUp || r.NewLevel != 3 {
		t.Errorf("Novice multi-level-up: NewLevel = %d, want 3", r.NewLevel)
	}
	if r.NewExp != 0 {
		t.Errorf("Novice carry: NewExp = %d, want 0", r.NewExp)
	}
	if r.GrantedStatusPoints != 6 {
		t.Errorf("Novice grants = %d, want 6", r.GrantedStatusPoints)
	}

	// Hardcoded table for "" would have used thresholds 9, 16 -> gain 30
	// would yield two level-ups (9+16=25) with 5 carry. Confirm Novice
	// differs.
	if r.NewExp == 5 {
		t.Errorf("Novice appears to use hardcoded table (carry 5 == fallback)")
	}
}

func TestNewExpRegistryFromJobDB_NilInput(t *testing.T) {
	if reg := NewExpRegistryFromJobDB(nil); reg != nil {
		t.Errorf("NewExpRegistryFromJobDB(nil) = %v, want nil", reg)
	}
}
