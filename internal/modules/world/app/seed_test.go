//go:build unit

package app_test

import (
	"log/slog"
	"testing"

	worldapp "github.com/bouroo/goAthena/internal/modules/world/app"
	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/mobdb"
	"github.com/bouroo/goAthena/pkg/ro/script"
)

// seedFixture builds a seeder over the shared newLevelingWorld harness (real
// WorldService + memory repo). The mob registry stays empty, so corpus mobs
// resolve the nominal pool — the fallback path worth proving.
func seedFixture(t *testing.T) (*worldapp.WorldSeeder, *worldapp.WorldService, *worldapp.SpawnService) {
	t.Helper()
	world, _ := newLevelingWorld(t)
	mobs := mobdb.NewRegistry()
	spawn := worldapp.NewSpawnService(world, mobs, nil)
	return worldapp.NewWorldSeeder(world, spawn, mobs, slog.Default()), world, spawn
}

func corpusSet() *script.CompiledScriptSet {
	set := script.NewCompiledScriptSet()
	set.Scripts["Shuger#pront"] = script.NewCompiledScript("Shuger#pront")
	set.NPCs = []script.NPCDef{{Name: "Shuger#pront", MapName: "prontera", X: 101, Y: 288, Facing: 3, Sprite: 98}}
	set.Shops = []script.ShopDef{{Name: "Butcher#iz", MapName: "izlude", X: 105, Y: 99, Facing: 0, Sprite: 54}}
	set.Spawns = []script.SpawnDef{
		{MapName: "gef_fild00", X: 0, Y: 0, Class: 1002, Name: "Poring", Amount: 3},
		{MapName: "gef_fild00", X: 54, Y: 212, XSize: 5, YSize: 5, Class: 1080, Name: "Green Plant", Amount: 3, Delay1: 3600},
	}
	return set
}

// Seed places NPCs, registers both store mappings, and spawns the full mob
// amount — the contract composition relies on for boot logging.
func TestSeeder_SeedPlacesEverything(t *testing.T) {
	seeder, world, _ := seedFixture(t)

	var npcReg, shopReg int
	npcs, mobs := seeder.Seed(corpusSet(),
		func(uint32, string) { npcReg++ },
		func(uint32, string) { shopReg++ },
	)
	if npcs != 2 {
		t.Fatalf("npcs = %d, want 2 (dialog + shop)", npcs)
	}
	if npcReg != 1 || shopReg != 1 {
		t.Fatalf("registers = npc %d shop %d, want 1/1", npcReg, shopReg)
	}
	if mobs != 6 {
		t.Fatalf("mobs = %d, want 6", mobs)
	}
	e, err := world.Get(domain.EntityID(0x7000_0001))
	if err != nil {
		t.Fatalf("first NPC not placed: %v", err)
	}
	if e.Type != domain.EntityTypeNPC || e.Map != "prontera" || e.Class != 98 || e.Pos.X != 101 || e.Pos.Y != 288 {
		t.Fatalf("npc entity = %+v", e)
	}
	if _, err := world.Get(domain.EntityID(0x7000_0002)); err != nil {
		t.Fatalf("shop NPC not placed: %v", err)
	}
}

// A corpus mob with no mob_db entry spawns at the nominal pool — never dropped.
func TestSeeder_MobHPFallback(t *testing.T) {
	seeder, world, _ := seedFixture(t)
	seeder.Seed(corpusSet(), nil, nil)
	e, err := world.Get(domain.EntityID(0x6000_0001))
	if err != nil {
		t.Fatalf("first mob: %v", err)
	}
	if e.HP != 50 || e.MaxHP != 50 {
		t.Fatalf("nominal pool = %d/%d, want 50/50", e.HP, e.MaxHP)
	}
	if e.Class != 1002 || e.Name != "Poring" || e.Map != "gef_fild00" {
		t.Fatalf("mob identity = class %d name %q map %q", e.Class, e.Name, e.Map)
	}
}

// Nil set and nil callbacks are no-ops, not panics (the reduced harness).
func TestSeeder_NilSafety(t *testing.T) {
	seeder, _, _ := seedFixture(t)
	if n, m := seeder.Seed(nil, nil, nil); n != 0 || m != 0 {
		t.Fatalf("nil set seeded %d/%d", n, m)
	}
	if n, m := seeder.Seed(corpusSet(), nil, nil); n != 2 || m != 6 {
		t.Fatalf("nil callbacks seeded %d/%d, want 2/6", n, m)
	}
}
