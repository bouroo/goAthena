//go:build unit

package app_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/bouroo/goAthena/internal/modules/world/app"
	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/internal/modules/world/infra"
	"github.com/bouroo/goAthena/pkg/ro/jobbasepoints"
	"github.com/bouroo/goAthena/pkg/ro/jobexp"
)

// writeTempYAML writes data to a temp .yml and returns its path (cleanup
// registered on t).
func writeTempYAML(t *testing.T, name, data string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// curveYAML is a tiny Novice curve: 1→2 costs 9, 2→3 costs 16, max level 3.
const curveYAML = `
Header:
  Type: JOB_STATS
  Version: 4
Body:
  - Jobs:
      Novice: true
    MaxBaseLevel: 3
    BaseExp:
      - Level: 1
        Exp: 9
      - Level: 2
        Exp: 16
`

// statsYAML is a tiny per-level HP/SP table: L1 100/10, L2 150/15, L3 220/20.
const statsYAML = `
Header:
  Type: JOB_STATS
  Version: 4
Body:
  - Jobs:
      Novice: true
    BaseHp:
      - Level: 1
        Hp: 100
      - Level: 2
        Hp: 150
      - Level: 3
        Hp: 220
    BaseSp:
      - Level: 1
        Sp: 10
      - Level: 2
        Sp: 15
      - Level: 3
        Sp: 20
`

// newLevelingWorld builds a world whose seed char is a level-1 Novice (Level 1,
// Job 0, MaxHP/MaxSP from the L1 table) with leveling wired via small
// in-memory registries built from the synthetic YAML above.
func newLevelingWorld(t *testing.T) (*app.WorldService, *infra.MemoryWorldRepository) {
	t.Helper()
	curve, err := jobexp.LoadFile(writeTempYAML(t, "curve.yml", curveYAML))
	if err != nil {
		t.Fatalf("load curve: %v", err)
	}
	stats, err := jobbasepoints.LoadFile(writeTempYAML(t, "stats.yml", statsYAML))
	if err != nil {
		t.Fatalf("load stats: %v", err)
	}
	repo := infra.NewMemoryWorldRepository(domain.Entity{
		ID: 150001, Account: 2000001, Type: domain.EntityTypePC, Job: 0,
		Map: "new_1-1", Pos: domain.Position{X: 53, Y: 111},
		Name: "Hero", Level: 1, HP: 100, MaxHP: 100, SP: 10, MaxSP: 10, Speed: 150,
	})
	w := app.NewWorldService(repo, slog.Default(), 50)
	w.SetLeveling(app.NewLevelingService(w, curve, stats, slog.Default()))
	if _, err := w.EnterMap(context.Background(), 150001); err != nil {
		t.Fatalf("EnterMap: %v", err)
	}
	return w, repo
}

// getEnt fetches the char as a value copy.
func getEnt(t *testing.T, w *app.WorldService, id domain.EntityID) domain.Entity {
	t.Helper()
	e, err := w.Get(id)
	if err != nil {
		t.Fatalf("Get %d: %v", id, err)
	}
	return e
}

// TestLeveling_ExpCrossesThreshold proves GrantExp drives the level-up: 9 EXP
// (the L1→L2 threshold) takes a level-1 Novice to level 2 with the table's
// maxima and a full heal.
func TestLeveling_ExpCrossesThreshold(t *testing.T) {
	w, _ := newLevelingWorld(t)
	var hookLevel int16
	var hookMaxHP, hookMaxSP int32
	w.OnLevelUp = func(_ uint32, newLevel int16, maxHP, maxSP int32, _ uint32) {
		hookLevel, hookMaxHP, hookMaxSP = newLevel, maxHP, maxSP
	}

	if _, _, err := w.GrantExp(context.Background(), 150001, 9, 0); err != nil {
		t.Fatalf("GrantExp: %v", err)
	}
	e := getEnt(t, w, 150001)
	if e.Level != 2 {
		t.Fatalf("level = %d, want 2 (threshold 9 consumed)", e.Level)
	}
	if e.BaseExp != 0 {
		t.Errorf("baseExp = %d, want 0 (threshold consumed)", e.BaseExp)
	}
	if e.MaxHP != 150 || e.MaxSP != 15 {
		t.Errorf("max vitals = %d/%d, want 150/15 (table L2)", e.MaxHP, e.MaxSP)
	}
	if e.HP != 150 || e.SP != 15 {
		t.Errorf("vitals = %d/%d, want full heal 150/15", e.HP, e.SP)
	}
	if hookLevel != 2 || hookMaxHP != 150 || hookMaxSP != 15 {
		t.Errorf("OnLevelUp hook = %d %d/%d, want 2 150/15", hookLevel, hookMaxHP, hookMaxSP)
	}
}

// TestLeveling_MultiLevelSingleGrant proves one grant crossing several
// thresholds levels up repeatedly: 9+16+1 EXP takes L1 → L3 + 1 overflow.
func TestLeveling_MultiLevelSingleGrant(t *testing.T) {
	w, _ := newLevelingWorld(t)
	if _, _, err := w.GrantExp(context.Background(), 150001, 26, 0); err != nil {
		t.Fatalf("GrantExp: %v", err)
	}
	e := getEnt(t, w, 150001)
	if e.Level != 3 {
		t.Fatalf("level = %d, want 3", e.Level)
	}
	if e.BaseExp != 1 {
		t.Errorf("baseExp = %d, want 1 (9+16 consumed, 1 remains)", e.BaseExp)
	}
	if e.MaxHP != 220 || e.MaxSP != 20 {
		t.Errorf("max vitals = %d/%d, want 220/20 (table L3)", e.MaxHP, e.MaxSP)
	}
}

// TestLeveling_AtMaxLevelAccruesWithoutLeveling proves EXP past the last
// threshold accrues silently (max level 3; no L3→L4 row).
func TestLeveling_AtMaxLevelAccruesWithoutLeveling(t *testing.T) {
	w, _ := newLevelingWorld(t)
	if _, _, err := w.GrantExp(context.Background(), 150001, 9, 0); err != nil {
		t.Fatalf("GrantExp L1: %v", err)
	}
	if _, _, err := w.GrantExp(context.Background(), 150001, 16, 0); err != nil {
		t.Fatalf("GrantExp L2: %v", err)
	}
	if _, _, err := w.GrantExp(context.Background(), 150001, 5000, 0); err != nil {
		t.Fatalf("GrantExp past max: %v", err)
	}
	e := getEnt(t, w, 150001)
	if e.Level != 3 {
		t.Fatalf("level = %d, want 3 (max; no further leveling)", e.Level)
	}
	if e.BaseExp != 5000 {
		t.Errorf("baseExp = %d, want 5000 (accrues at cap, unconsumed)", e.BaseExp)
	}
}

// TestLeveling_NilCurveNoLeveling proves backward compatibility: a world with
// no leveling wired still accrues EXP but never levels (the pre-Phase-26
// behavior every non-DI test relies on).
func TestLeveling_NilCurveNoLeveling(t *testing.T) {
	repo := infra.NewMemoryWorldRepository(domain.Entity{
		ID: 150001, Account: 2000001, Type: domain.EntityTypePC, Job: 0,
		Map: "new_1-1", Pos: domain.Position{X: 53, Y: 111},
		Name: "Hero", Level: 1, HP: 100, MaxHP: 100, SP: 10, MaxSP: 10, Speed: 150,
	})
	w := app.NewWorldService(repo, slog.Default(), 50)
	if _, err := w.EnterMap(context.Background(), 150001); err != nil {
		t.Fatalf("EnterMap: %v", err)
	}
	if _, _, err := w.GrantExp(context.Background(), 150001, 9, 0); err != nil {
		t.Fatalf("GrantExp: %v", err)
	}
	e := getEnt(t, w, 150001)
	if e.Level != 1 || e.MaxHP != 100 {
		t.Fatalf("level/maxHP = %d/%d, want 1/100 (no leveling wired)", e.Level, e.MaxHP)
	}
	if e.BaseExp != 9 {
		t.Errorf("baseExp = %d, want 9 (still accrues)", e.BaseExp)
	}
}

// TestLeveling_LevelPersistsViaLeaveMap proves the level reaches the repo via
// the disconnect persist path (SaveState carries base_level).
func TestLeveling_LevelPersistsViaLeaveMap(t *testing.T) {
	w, repo := newLevelingWorld(t)
	if _, _, err := w.GrantExp(context.Background(), 150001, 9, 0); err != nil {
		t.Fatalf("GrantExp: %v", err)
	}
	if err := w.LeaveMap(context.Background(), 150001); err != nil {
		t.Fatalf("LeaveMap: %v", err)
	}
	got, err := repo.LoadEnterState(context.Background(), 150001)
	if err != nil {
		t.Fatalf("LoadEnterState after leave: %v", err)
	}
	if got.Level != 2 {
		t.Errorf("reloaded level = %d, want 2 (SaveState carries base_level)", got.Level)
	}
	if got.MaxHP != 150 || got.MaxSP != 15 {
		t.Errorf("reloaded max vitals = %d/%d, want 150/15", got.MaxHP, got.MaxSP)
	}
}

// newStatWorld seeds a PC with spendable status points and known base stats.
func newStatWorld(t *testing.T) *app.WorldService {
	t.Helper()
	repo := infra.NewMemoryWorldRepository(domain.Entity{
		ID: 150001, Account: 2000001, Type: domain.EntityTypePC, Job: 0,
		Map: "new_1-1", Pos: domain.Position{X: 53, Y: 111},
		Name: "Hero", Level: 1, HP: 100, MaxHP: 100, SP: 10, MaxSP: 10, Speed: 150,
		Str: 5, Agi: 4, Vit: 3, Int: 2, Dex: 1, Luk: 0, StatusPoint: 10,
	})
	w := app.NewWorldService(repo, slog.Default(), 50)
	if _, err := w.EnterMap(context.Background(), 150001); err != nil {
		t.Fatalf("EnterMap: %v", err)
	}
	return w
}

// TestAllocateStat_RaisesStatAndSpendsPoints proves the happy path: raising Str
// from 5 costs the kernel rate (1+(5+9)/10 = 2) and leaves 8 points.
func TestAllocateStat_RaisesStatAndSpendsPoints(t *testing.T) {
	w := newStatWorld(t)
	newVal, remaining, cost, err := w.AllocateStat(150001, "Str")
	if err != nil {
		t.Fatalf("AllocateStat: %v", err)
	}
	if newVal != 6 {
		t.Errorf("newVal = %d, want 6", newVal)
	}
	if cost != 2 {
		t.Errorf("cost = %d, want 2 (kernel rate for cur=5)", cost)
	}
	if remaining != 8 {
		t.Errorf("remaining = %d, want 8", remaining)
	}
	e := getEnt(t, w, 150001)
	if e.Str != 6 || e.StatusPoint != 8 {
		t.Errorf("entity str/points = %d/%d, want 6/8", e.Str, e.StatusPoint)
	}
}

// TestAllocateStat_InsufficientPoints proves the sentinel: Luk at 0 costs 1, so
// 0 available points is refused and the stat is untouched.
func TestAllocateStat_InsufficientPoints(t *testing.T) {
	repo := infra.NewMemoryWorldRepository(domain.Entity{
		ID: 150001, Account: 2000001, Type: domain.EntityTypePC,
		Map: "new_1-1", Pos: domain.Position{X: 53, Y: 111},
		Name: "Broke", Level: 1, Speed: 150, Str: 5, StatusPoint: 0,
	})
	w := app.NewWorldService(repo, slog.Default(), 50)
	if _, err := w.EnterMap(context.Background(), 150001); err != nil {
		t.Fatalf("EnterMap: %v", err)
	}
	_, _, _, err := w.AllocateStat(150001, "Luk")
	if !errors.Is(err, domain.ErrNoStatusPoints) {
		t.Fatalf("err = %v, want ErrNoStatusPoints", err)
	}
	if e := getEnt(t, w, 150001); e.Luk != 0 {
		t.Errorf("luk = %d, want 0 (refused)", e.Luk)
	}
}

// TestAllocateStat_CapAt99 proves the cap sentinel: a 99 stat cannot rise.
func TestAllocateStat_CapAt99(t *testing.T) {
	repo := infra.NewMemoryWorldRepository(domain.Entity{
		ID: 150001, Account: 2000001, Type: domain.EntityTypePC,
		Map: "new_1-1", Pos: domain.Position{X: 53, Y: 111},
		Name: "Capped", Level: 1, Speed: 150, Str: 99, StatusPoint: 100,
	})
	w := app.NewWorldService(repo, slog.Default(), 50)
	if _, err := w.EnterMap(context.Background(), 150001); err != nil {
		t.Fatalf("EnterMap: %v", err)
	}
	if _, _, _, err := w.AllocateStat(150001, "Str"); !errors.Is(err, domain.ErrStatCapped) {
		t.Fatalf("err = %v, want ErrStatCapped", err)
	}
	if e := getEnt(t, w, 150001); e.Str != 99 || e.StatusPoint != 100 {
		t.Errorf("str/points = %d/%d, want 99/100 (untouched)", e.Str, e.StatusPoint)
	}
}

// TestAllocateStat_UnknownStatRefused proves the sentinel for an unmapped name.
func TestAllocateStat_UnknownStatRefused(t *testing.T) {
	w := newStatWorld(t)
	if _, _, _, err := w.AllocateStat(150001, "Pow"); !errors.Is(err, domain.ErrUnknownStat) {
		t.Fatalf("err = %v, want ErrUnknownStat", err)
	}
}

// TestLevelUp_GrantsStatusPoints proves each level-up grants +3 spendable points.
func TestLevelUp_GrantsStatusPoints(t *testing.T) {
	w, _ := newLevelingWorld(t)
	if _, _, err := w.GrantExp(context.Background(), 150001, 9, 0); err != nil {
		t.Fatalf("GrantExp: %v", err)
	}
	if e := getEnt(t, w, 150001); e.StatusPoint != 3 {
		t.Errorf("points after 1 level-up = %d, want 3", e.StatusPoint)
	}
	// And the points are spendable through the same world.
	if _, _, _, err := w.AllocateStat(150001, "Luk"); err != nil {
		t.Fatalf("spend granted points: %v", err)
	}
	if e := getEnt(t, w, 150001); e.Luk != 1 || e.StatusPoint != 2 {
		t.Errorf("luk/points = %d/%d, want 1/2", e.Luk, e.StatusPoint)
	}
}
