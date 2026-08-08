//go:build unit

package app_test

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bouroo/goAthena/internal/modules/world/app"
	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/internal/modules/world/infra"
)

func newWorld() *app.WorldService {
	repo := infra.NewMemoryWorldRepository(domain.Entity{
		ID: 150001, Account: 2000001,
		Map: "new_1-1", Pos: domain.Position{X: 53, Y: 111},
		Sex: 1, Job: 0, Level: 1, Name: "Hero", HP: 1000, MaxHP: 1000, Speed: 150,
	})
	return app.NewWorldService(repo, slog.Default(), 50)
}

func TestAddEntity_Success(t *testing.T) {
	w := newWorld()
	err := w.AddEntity(domain.Entity{ID: 1, Map: "prontera", Pos: domain.Position{X: 100, Y: 200}})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	e, _ := w.Get(1)
	if e.Pos.X != 100 {
		t.Errorf("x = %d", e.Pos.X)
	}
}

func TestAddEntity_Duplicate(t *testing.T) {
	w := newWorld()
	_ = w.AddEntity(domain.Entity{ID: 1, Map: "prontera"})
	err := w.AddEntity(domain.Entity{ID: 1, Map: "prontera"})
	if !errors.Is(err, domain.ErrEntityAlreadyExists) {
		t.Errorf("err = %v, want ErrEntityAlreadyExists", err)
	}
}

func TestRemoveEntity(t *testing.T) {
	w := newWorld()
	_ = w.AddEntity(domain.Entity{ID: 1, Map: "prontera", Pos: domain.Position{X: 50, Y: 50}})
	if err := w.RemoveEntity(1); err != nil {
		t.Fatalf("remove: %v", err)
	}
	_, err := w.Get(1)
	if !errors.Is(err, domain.ErrEntityNotFound) {
		t.Errorf("err = %v, want ErrEntityNotFound", err)
	}
}

func TestMoveEntity(t *testing.T) {
	w := newWorld()
	_ = w.AddEntity(domain.Entity{ID: 1, Map: "prontera", Pos: domain.Position{X: 50, Y: 50}})
	if err := w.MoveEntity(1, domain.Position{X: 60, Y: 60}); err != nil {
		t.Fatalf("move: %v", err)
	}
	e, _ := w.Get(1)
	if e.Pos.X != 60 || e.Pos.Y != 60 {
		t.Errorf("pos = %+v, want {60,60}", e.Pos)
	}
}

func TestQueryVisible_ReturnsNearby(t *testing.T) {
	w := newWorld()
	_ = w.AddEntity(domain.Entity{ID: 1, Map: "prontera", Pos: domain.Position{X: 100, Y: 100}})
	_ = w.AddEntity(domain.Entity{ID: 2, Map: "prontera", Pos: domain.Position{X: 101, Y: 101}})
	visible := w.QueryVisible("prontera", 100, 100)
	if len(visible) < 2 {
		t.Errorf("visible count = %d, want >= 2 (self + neighbor)", len(visible))
	}
}

func TestEnterMap_LoadsFromRepo(t *testing.T) {
	w := newWorld()
	e, err := w.EnterMap(context.Background(), 150001)
	if err != nil {
		t.Fatalf("enter map: %v", err)
	}
	if e.Map != "new_1-1" || e.Pos.X != 53 {
		t.Errorf("entity = %+v", e)
	}
	if e.Type != domain.EntityTypePC {
		t.Errorf("type = %d, want PC", e.Type)
	}
	// Entity should now be in the registry.
	got, _ := w.Get(domain.EntityID(150001))
	if got.Name != "Hero" {
		t.Errorf("name = %q", got.Name)
	}
}

func TestEnterMap_NotFound(t *testing.T) {
	w := newWorld()
	_, err := w.EnterMap(context.Background(), 99999)
	if !errors.Is(err, domain.ErrEntityNotFound) {
		t.Errorf("err = %v, want ErrEntityNotFound", err)
	}
}

func TestLeaveMap_RemovesEntity(t *testing.T) {
	w := newWorld()
	_, _ = w.EnterMap(context.Background(), 150001)
	if err := w.LeaveMap(context.Background(), 150001); err != nil {
		t.Fatalf("leave: %v", err)
	}
	_, err := w.Get(domain.EntityID(150001))
	if !errors.Is(err, domain.ErrEntityNotFound) {
		t.Errorf("err = %v, want ErrEntityNotFound", err)
	}
}

func TestStartTick_FiresUpdates(t *testing.T) {
	w := app.NewWorldService(infra.NewMemoryWorldRepository(), slog.Default(), 1000) // 1ms tick
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	var count atomic.Int32
	go w.StartTick(ctx, func(_ context.Context, _ time.Duration) { count.Add(1) })
	time.Sleep(50 * time.Millisecond)
	cancel()
	if count.Load() == 0 {
		t.Error("tick loop never fired update callback")
	}
}

func TestStartTick_IdleWithoutCallback(t *testing.T) {
	w := app.NewWorldService(infra.NewMemoryWorldRepository(), slog.Default(), 1000) // 1ms tick
	// With no update callback there is no periodic entity-state work to do
	// (spawn/combat are event-driven), so StartTick must return immediately
	// rather than spin the ticker, and Stop must drain idempotently.
	done := make(chan struct{})
	go func() {
		w.StartTick(context.Background(), nil)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("StartTick with nil callback blocked instead of returning idle")
	}
	w.Stop()
	w.Stop() // idempotent: must not panic on double-close
}
