//go:build integration

package app_test

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/bouroo/goAthena/internal/modules/world/app"
	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/internal/modules/world/infra"
	"github.com/bouroo/goAthena/pkg/ro/itemdb"
	"github.com/bouroo/goAthena/pkg/ro/mobdb"
)

// deathMobFixture is a 1-HP Poring whose drop table exercises three paths:
//   - Jellopy @ 10000 (guaranteed) and present in item_db → drops.
//   - Knife_  @ 0      → rate gate rejects (never drops).
//   - Ghost   @ 10000  → no item_db AegisName match → skipped.
//
// Exactly one floor item (Jellopy, NameID 909) must land.
const deathMobFixture = `Header:
  Type: MOB_DB
  Version: 5

Body:
  - Id: 1002
    Name: Poring
    Hp: 1
    Defense: 0
    Vit: 0
    Drops:
      - Item: Jellopy
        Rate: 10000
      - Item: Knife_
        Rate: 0
      - Item: Ghost
        Rate: 10000
`

const deathItemFixture = `Header:
  Type: ITEM_DB
  Version: 3

Body:
  - Id: 909
    AegisName: Jellopy
    Name: Jellopy
    Type: Etc
`

// TestMobDeathLoop_DropsDespawnRespawn drives the full mob death loop at the
// world layer (the gateway wires Attack→died→OnMobDeath; this test stands in for
// that orchestration): a 1-HP mob dies to one hit, its guaranteed drop lands as a
// floor item, the mob is despawned from the world registry, and it re-spawns at
// its origin after the registered delay. The respawn arrives off the hot path on
// a timer, so the assertion re-arms a generous deadline (a respawn frame can
// arrive at any read site within the window).
func TestMobDeathLoop_DropsDespawnRespawn(t *testing.T) {
	mobs, err := mobdb.Load(strings.NewReader(deathMobFixture))
	if err != nil {
		t.Fatalf("load mob_db: %v", err)
	}
	items, err := itemdb.Load(strings.NewReader(deathItemFixture))
	if err != nil {
		t.Fatalf("load item_db: %v", err)
	}
	world := app.NewWorldService(infra.NewMemoryWorldRepository(), slog.Default(), 50)
	spawn := app.NewSpawnService(world, mobs, items)
	defer spawn.Stop()
	combat := app.NewCombatService(world, mobs)

	// Attacker PC (zero-ATK → 1 damage floor) and a 1-HP mob.
	const pcID, mobID = domain.EntityID(1), domain.EntityID(2)
	if err := world.AddEntity(domain.Entity{ID: pcID, Type: domain.EntityTypePC, Map: "prontera"}); err != nil {
		t.Fatalf("add pc: %v", err)
	}
	const respawnDelay = 100 * time.Millisecond
	if err := spawn.SpawnMob(mobID, 1002, "prontera", domain.Position{X: 50, Y: 50}, "Poring", 1, 1, respawnDelay); err != nil {
		t.Fatalf("spawn mob: %v", err)
	}

	// One hit kills the 1-HP mob.
	dmg, died, err := combat.Attack(pcID, mobID)
	if err != nil {
		t.Fatalf("attack: %v", err)
	}
	if dmg < 1 {
		t.Fatalf("dmg = %d, want ≥1", dmg)
	}
	if !died {
		t.Fatalf("died = false, want true (mob HP reached 0)")
	}

	// Gateway orchestration: on death, roll drops + despawn.
	drops := spawn.OnMobDeath(1002, "prontera", domain.Position{X: 50, Y: 50}, mobID)
	if len(drops) != 1 || drops[0].NameID != 909 {
		t.Fatalf("drops = %+v, want exactly one Jellopy (NameID 909)", drops)
	}

	// The floor item persists (broadcast on the next map-enter via FloorItems).
	if fis := spawn.FloorItems("prontera"); len(fis) != 1 || fis[0].NameID != 909 {
		t.Fatalf("floor items = %+v, want one Jellopy", fis)
	}

	// The mob is despawned from the world registry + AOI grid.
	if _, err := world.Get(mobID); err == nil {
		t.Fatalf("mob still in world after death; want removed")
	}

	// Respawn: poll with a re-armed generous deadline. The timer fires off the
	// synchronous path, so the re-entry can land any time within the window.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := world.Get(mobID); err == nil {
			break // mob re-spawned
		}
		if time.Now().After(deadline) {
			t.Fatalf("mob did not respawn within deadline")
		}
		time.Sleep(5 * time.Millisecond)
	}
	respawned, err := world.Get(mobID)
	if err != nil {
		t.Fatalf("get respawned mob: %v", err)
	}
	if respawned.HP != 1 || respawned.Class != 1002 || respawned.Type != domain.EntityTypeMob {
		t.Fatalf("respawned mob = %+v, want full-HP mob class 1002", respawned)
	}
}
