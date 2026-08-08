//go:build unit

package app_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/bouroo/goAthena/internal/modules/world/app"
	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/internal/modules/world/infra"
)

// newHealWorld builds a WorldService with charID on a map at the given vitals.
func newHealWorld(t *testing.T, charID uint32, hp, maxHP, sp, maxSP int32) (*app.WorldService, uint32) {
	t.Helper()
	eid := domain.EntityID(charID)
	repo := infra.NewMemoryWorldRepository(domain.Entity{ID: eid, Map: "new_1-1"})
	world := app.NewWorldService(repo, slog.Default(), 50)
	if err := world.AddEntity(domain.Entity{
		ID: eid, Map: "new_1-1", HP: hp, MaxHP: maxHP, SP: sp, MaxSP: maxSP,
	}); err != nil {
		t.Fatalf("AddEntity: %v", err)
	}
	return world, charID
}

func TestWorldService_HealPlayer_PartialAndFull(t *testing.T) {
	const cid uint32 = 150001
	world, _ := newHealWorld(t, cid, 250, 1000, 10, 100)

	hp, sp, err := world.HealPlayer(cid, 50, 50) // +500 HP, +50 SP
	if err != nil {
		t.Fatalf("HealPlayer: %v", err)
	}
	if hp != 750 || sp != 60 {
		t.Errorf("heal 50%% = (hp %d, sp %d), want (750, 60)", hp, sp)
	}

	hp, sp, err = world.HealPlayer(cid, 100, 100) // full
	if err != nil {
		t.Fatalf("HealPlayer: %v", err)
	}
	if hp != 1000 || sp != 100 {
		t.Errorf("heal 100%% = (hp %d, sp %d), want (1000, 100)", hp, sp)
	}
}

func TestWorldService_HealPlayer_ClampsToMax(t *testing.T) {
	const cid uint32 = 150001
	// HP 900, heal 50% (=500) would reach 1400; must clamp to MaxHP 1000.
	world, _ := newHealWorld(t, cid, 900, 1000, 90, 100)

	hp, sp, err := world.HealPlayer(cid, 50, 50)
	if err != nil {
		t.Fatalf("HealPlayer: %v", err)
	}
	if hp != 1000 {
		t.Errorf("hp = %d, want 1000 (clamped to max, no overflow)", hp)
	}
	if sp != 100 {
		t.Errorf("sp = %d, want 100 (clamped to max)", sp)
	}
}

func TestWorldService_HealPlayer_NegativeAndOverPctClamp(t *testing.T) {
	const cid uint32 = 150001
	world, _ := newHealWorld(t, cid, 250, 1000, 10, 100)

	// Negative percent restores nothing (must not damage): HP/SP unchanged.
	hp, sp, err := world.HealPlayer(cid, -50, -1000)
	if err != nil {
		t.Fatalf("HealPlayer: %v", err)
	}
	if hp != 250 || sp != 10 {
		t.Errorf("negative pct = (hp %d, sp %d), want unchanged (250, 10)", hp, sp)
	}

	// Percent > 100 clamps to 100 (full), must not overflow max.
	hp, sp, err = world.HealPlayer(cid, 200, 9999)
	if err != nil {
		t.Fatalf("HealPlayer: %v", err)
	}
	if hp != 1000 || sp != 100 {
		t.Errorf("over pct = (hp %d, sp %d), want full (1000, 100)", hp, sp)
	}
}

func TestWorldService_HealPlayer_NotOnMap(t *testing.T) {
	world, cid := newHealWorld(t, 150001, 250, 1000, 10, 100)
	_, _, err := world.HealPlayer(cid+1, 100, 100) // different char: no entity
	if !errors.Is(err, domain.ErrEntityNotFound) {
		t.Errorf("err = %v, want ErrEntityNotFound", err)
	}
}

func TestWorldService_WarpPlayer_PersistsDestination(t *testing.T) {
	const cid uint32 = 150001
	repo := infra.NewMemoryWorldRepository(domain.Entity{ID: domain.EntityID(cid), Map: "new_1-1", Pos: domain.Position{X: 0, Y: 0}})
	world := app.NewWorldService(repo, slog.Default(), 50)

	if err := world.WarpPlayer(cid, "prontera", 128, 200); err != nil {
		t.Fatalf("WarpPlayer: %v", err)
	}
	got, err := repo.LoadEnterState(context.Background(), cid)
	if err != nil {
		t.Fatalf("LoadEnterState: %v", err)
	}
	if got.Map != "prontera" || got.Pos.X != 128 || got.Pos.Y != 200 {
		t.Errorf("enter state = {map %s pos %d,%d}, want {prontera 128,200}", got.Map, got.Pos.X, got.Pos.Y)
	}
}

func TestWorldService_WarpPlayer_UnknownChar(t *testing.T) {
	repo := infra.NewMemoryWorldRepository(domain.Entity{ID: domain.EntityID(150001), Map: "new_1-1"})
	world := app.NewWorldService(repo, slog.Default(), 50)
	if err := world.WarpPlayer(999999, "prontera", 1, 2); !errors.Is(err, domain.ErrEntityNotFound) {
		t.Errorf("err = %v, want ErrEntityNotFound", err)
	}
}
