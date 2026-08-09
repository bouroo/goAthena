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

// TestWorldService_LeaveMap_PersistsVitals proves the warp/disconnect persist
// primitive flushes the entity's in-session vitals (combat/regen/heal changes)
// to the repo alongside the offline flag, so they survive a reconnect/restart.
func TestWorldService_LeaveMap_PersistsVitals(t *testing.T) {
	const cid uint32 = 150001
	eid := domain.EntityID(cid)
	repo := infra.NewMemoryWorldRepository(domain.Entity{
		ID: eid, Map: "new_1-1", HP: 1000, MaxHP: 1000, SP: 50, MaxSP: 50,
	})
	world := app.NewWorldService(repo, slog.Default(), 50)
	if err := world.AddEntity(domain.Entity{
		ID: eid, Type: domain.EntityTypePC, Map: "new_1-1",
		HP: 1000, MaxHP: 1000, SP: 50, MaxSP: 50,
	}); err != nil {
		t.Fatalf("AddEntity: %v", err)
	}

	// Simulate in-session combat mutating HP/SP before the leave.
	if _, _, err := world.AddVitals(cid, -250, -30); err != nil { // HP 750, SP 20
		t.Fatalf("AddVitals: %v", err)
	}

	if err := world.LeaveMap(context.Background(), cid); err != nil {
		t.Fatalf("LeaveMap: %v", err)
	}

	// The entity is despawned from the in-memory registry ...
	if _, err := world.Get(eid); !errors.Is(err, domain.ErrEntityNotFound) {
		t.Fatalf("after LeaveMap, Get err = %v, want ErrEntityNotFound", err)
	}
	// ... but the repo carries the final vitals (and the offline flag + position).
	got, err := repo.LoadEnterState(context.Background(), cid)
	if err != nil {
		t.Fatalf("LoadEnterState: %v", err)
	}
	if got.HP != 750 || got.SP != 20 {
		t.Errorf("persisted vitals = (hp %d, sp %d), want (750, 20)", got.HP, got.SP)
	}
}

// TestWorldService_LeaveMap_Idempotent proves a second LeaveMap on an already-
// removed entity is a clean no-op (nil), not an error. The CZ_RESTART type=1 path
// persists + despawns, then closing the conn re-enters LeaveMap via OnClose; that
// double call must not surface an error or flood logs.
func TestWorldService_LeaveMap_Idempotent(t *testing.T) {
	const cid uint32 = 150001
	eid := domain.EntityID(cid)
	repo := infra.NewMemoryWorldRepository(domain.Entity{ID: eid, Map: "new_1-1"})
	world := app.NewWorldService(repo, slog.Default(), 50)
	if err := world.AddEntity(domain.Entity{
		ID: eid, Type: domain.EntityTypePC, Map: "new_1-1",
		HP: 1000, MaxHP: 1000, SP: 50, MaxSP: 50,
	}); err != nil {
		t.Fatalf("AddEntity: %v", err)
	}

	if err := world.LeaveMap(context.Background(), cid); err != nil {
		t.Fatalf("first LeaveMap: %v", err)
	}
	// OnClose / char-select double-call re-enters LeaveMap on the removed entity.
	if err := world.LeaveMap(context.Background(), cid); err != nil {
		t.Errorf("second LeaveMap = %v, want nil (idempotent no-op)", err)
	}
	if _, err := world.Get(eid); !errors.Is(err, domain.ErrEntityNotFound) {
		t.Errorf("after double LeaveMap, Get err = %v, want ErrEntityNotFound", err)
	}
}

// TestWorldService_SaveAll_PersistsOnlinePCs proves the graceful-shutdown save-all
// flushes every online PC's vitals (skipping non-PCs like mobs), so a clean
// restart loses no in-session HP/SP.
func TestWorldService_SaveAll_PersistsOnlinePCs(t *testing.T) {
	const (
		pc1 uint32 = 150001
		pc2 uint32 = 150002
		mob        = 700001
	)
	seed := func(id uint32) domain.Entity {
		return domain.Entity{
			ID: domain.EntityID(id), Map: "prontera",
			HP: 1000, MaxHP: 1000, SP: 50, MaxSP: 50,
		}
	}
	repo := infra.NewMemoryWorldRepository(seed(pc1), seed(pc2))
	world := app.NewWorldService(repo, slog.Default(), 50)
	for _, id := range []uint32{pc1, pc2} {
		if err := world.AddEntity(domain.Entity{
			ID: domain.EntityID(id), Type: domain.EntityTypePC, Map: "prontera",
			HP: 1000, MaxHP: 1000, SP: 50, MaxSP: 50,
		}); err != nil {
			t.Fatalf("AddEntity pc %d: %v", id, err)
		}
	}
	// A mob shares the registry but must be skipped (it has no char-table row).
	if err := world.AddEntity(domain.Entity{
		ID: domain.EntityID(mob), Type: domain.EntityTypeMob, Map: "prontera", Class: 1002,
	}); err != nil {
		t.Fatalf("AddEntity mob: %v", err)
	}
	// Mutate vitals in-session.
	if _, _, err := world.AddVitals(pc1, -300, 0); err != nil { // HP 700
		t.Fatalf("AddVitals pc1: %v", err)
	}
	if _, _, err := world.AddVitals(pc2, 0, -20); err != nil { // SP 30
		t.Fatalf("AddVitals pc2: %v", err)
	}

	world.SaveAll(context.Background())

	for id, wantHP := range map[uint32]int32{pc1: 700, pc2: 1000} {
		got, err := repo.LoadEnterState(context.Background(), id)
		if err != nil {
			t.Fatalf("LoadEnterState pc %d: %v", id, err)
		}
		if got.HP != wantHP {
			t.Errorf("pc %d HP after SaveAll = %d, want %d", id, got.HP, wantHP)
		}
	}
	if got, err := repo.LoadEnterState(context.Background(), pc2); err != nil {
		t.Fatalf("LoadEnterState pc2: %v", err)
	} else if got.SP != 30 {
		t.Errorf("pc2 SP after SaveAll = %d, want 30", got.SP)
	}
}
