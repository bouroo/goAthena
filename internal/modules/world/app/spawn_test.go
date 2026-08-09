//go:build unit

package app_test

import (
	"errors"
	"log/slog"
	"testing"

	"github.com/bouroo/goAthena/internal/modules/world/app"
	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/internal/modules/world/infra"
)

func newSpawn() (*app.SpawnService, *app.WorldService) {
	repo := infra.NewMemoryWorldRepository()
	world := app.NewWorldService(repo, slog.Default(), 50)
	return app.NewSpawnService(world, nil, nil), world
}

func TestDropItem_PlacesFloorItem(t *testing.T) {
	s, _ := newSpawn()
	fi := s.DropItem(501, 3, "prontera", domain.Position{X: 100, Y: 100}, 0)
	if fi.NameID != 501 || fi.Amount != 3 || fi.GroundID == 0 {
		t.Errorf("floor item = %+v", fi)
	}
}

func TestPickupFloorItem_Success(t *testing.T) {
	s, _ := newSpawn()
	fi := s.DropItem(501, 2, "prontera", domain.Position{X: 50, Y: 50}, 0)
	picked, err := s.PickupFloorItem(fi.GroundID)
	if err != nil {
		t.Fatalf("pickup: %v", err)
	}
	if picked.NameID != 501 || picked.Amount != 2 {
		t.Errorf("picked = %+v", picked)
	}
}

func TestPickupFloorItem_AlreadyTaken(t *testing.T) {
	s, _ := newSpawn()
	fi := s.DropItem(501, 1, "prontera", domain.Position{X: 50, Y: 50}, 0)
	_, _ = s.PickupFloorItem(fi.GroundID)
	_, err := s.PickupFloorItem(fi.GroundID)
	if !errors.Is(err, domain.ErrEntityNotFound) {
		t.Errorf("err = %v, want ErrEntityNotFound", err)
	}
}

func TestFloorItems_FilteredByMap(t *testing.T) {
	s, _ := newSpawn()
	_ = s.DropItem(501, 1, "prontera", domain.Position{X: 50, Y: 50}, 0)
	_ = s.DropItem(502, 1, "geffen", domain.Position{X: 50, Y: 50}, 0)
	prontera := s.FloorItems("prontera")
	if len(prontera) != 1 || prontera[0].NameID != 501 {
		t.Errorf("prontera items = %+v", prontera)
	}
}

func TestSpawnMob_Registers(t *testing.T) {
	s, world := newSpawn()
	err := s.SpawnMob(domain.EntityID(1001), 1002, "prontera", domain.Position{X: 50, Y: 50}, "Poring", 50, 50, 0)
	if err != nil {
		t.Fatalf("spawn mob: %v", err)
	}
	mob, err := world.Get(domain.EntityID(1001))
	if err != nil {
		t.Fatalf("get mob: %v", err)
	}
	if mob.Type != domain.EntityTypeMob {
		t.Errorf("type = %d, want mob", mob.Type)
	}
}
