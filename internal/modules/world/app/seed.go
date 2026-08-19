// Package app seeding: the WorldSeeder turns a compiled script corpus into
// live world entities — dialog NPCs, shop NPCs, and mob spawns — so a
// production boot has a populated world instead of an empty grid.
package app

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/mobdb"
	"github.com/bouroo/goAthena/pkg/ro/script"
)

// npcGIDBase is where NPC entity GIDs start. PC GIDs are char_ids (small,
// identity-allocated) and mob GIDs come from the seeder's own allocator;
// rAthena's NPC ids are block-list ids with no client semantics beyond
// uniqueness, so a high fixed base keeps the spaces disjoint.
const npcGIDBase uint32 = 0x7000_0001

// mobGIDBase is where seeder-allocated mob GIDs start (below the NPC base).
const mobGIDBase uint32 = 0x6000_0001

// defaultRespawnSeconds bounds a spawn's respawn delay when the corpus gives
// none. 30 keeps an aggressive cadence without zero-delay respawn spam.
const defaultRespawnSeconds = 30

// nominalMobHP is the pool a corpus mob with no mob_db entry spawns with, so
// it is visible and killable rather than dropped (rAthena's loader skips it;
// an empty world is the worse failure for a rebuild).
const nominalMobHP int32 = 50

// WorldSeeder spawns script-corpus entities into the world. The content
// module's NPC/shop stores are fed GID→name mappings through the register
// callbacks supplied by the caller, keeping world free of content imports
// (structural, mirroring the ScriptWorld port).
type WorldSeeder struct {
	world  *WorldService
	spawn  *SpawnService
	mobs   *mobdb.Registry
	log    *slog.Logger
	next   uint32 // NPC GID allocator
	nextID uint32 // mob EntityID allocator
}

// NewWorldSeeder builds a seeder. mobs may be nil (corpus mobs then spawn at
// the nominal pool).
func NewWorldSeeder(world *WorldService, spawn *SpawnService, mobs *mobdb.Registry, log *slog.Logger) *WorldSeeder {
	return &WorldSeeder{world: world, spawn: spawn, mobs: mobs, log: log, next: npcGIDBase, nextID: mobGIDBase}
}

// SpawnNPC places one NPC entity and returns its GID. The sprite rides the
// Entity.Class field (the mob-class field; the combat service never resolves
// an EntityTypeNPC Class through mob_db, so the reuse is by design — the
// client renders ObjectType=6 + Class as the sprite).
func (s *WorldSeeder) SpawnNPC(name, mapName string, x, y, facing, sprite int) (uint32, error) {
	gid := s.next
	s.next++
	e := domain.Entity{
		ID:    domain.EntityID(gid),
		Type:  domain.EntityTypeNPC,
		Class: int32(sprite), //nolint:gosec // G115: sprite ids fit int32.
		Map:   mapName,
		Pos:   domain.Position{X: int16(x), Y: int16(y)}, //nolint:gosec // G115: map cells fit int16.
		Name:  name,
		Dir:   uint8(facing & 7), //nolint:gosec // G115: facing 0..7.
		Speed: 150,
	}
	if err := s.world.AddEntity(e); err != nil {
		return 0, fmt.Errorf("seed npc %q: %w", name, err)
	}
	return gid, nil
}

// Seed walks the compiled corpus: dialog NPCs get entities plus
// registerNPC(gid, scriptName); shops get entities plus
// registerShop(gid, shopName); mob spawns go to the spawn service. It
// returns (npcCount, mobCount) for boot logging. Either register callback
// may be nil (placement still happens; clicks just resolve no script).
func (s *WorldSeeder) Seed(set *script.CompiledScriptSet,
	registerNPC func(gid uint32, name string),
	registerShop func(gid uint32, shopName string),
) (npcs, mobs int) {
	if set == nil {
		return 0, 0
	}
	for _, def := range set.NPCs {
		if _, ok := set.Scripts[def.Name]; !ok {
			continue // placement without a compilable body — nothing to click
		}
		gid, err := s.SpawnNPC(def.Name, def.MapName, def.X, def.Y, def.Facing, def.Sprite)
		if err != nil {
			s.log.Warn("seed: npc spawn failed", "name", def.Name, "map", def.MapName, "err", err)
			continue
		}
		if registerNPC != nil {
			registerNPC(gid, def.Name)
		}
		npcs++
	}
	for _, def := range set.Shops {
		gid, err := s.SpawnNPC(def.Name, def.MapName, def.X, def.Y, def.Facing, def.Sprite)
		if err != nil {
			s.log.Warn("seed: shop npc spawn failed", "name", def.Name, "map", def.MapName, "err", err)
			continue
		}
		if registerShop != nil {
			registerShop(gid, def.Name)
		}
		npcs++
	}
	return npcs, s.SeedMobs(set.Spawns)
}

// SeedMobs spawns corpus mobs: for each SpawnDef, Amount mobs at cells
// inside the xs×ys box centered on (X,Y). HP comes from mob_db when the
// class resolves. The respawn delay is the corpus pair's bound (rAthena
// randomizes inside the window; the seeder uses the bound for determinism).
func (s *WorldSeeder) SeedMobs(defs []script.SpawnDef) int {
	n := 0
	for _, def := range defs {
		hp := nominalMobHP
		if s.mobs != nil {
			if m := s.mobs.Get(def.Class); m != nil {
				hp = m.Hp
			}
		}
		for i := 0; i < def.Amount; i++ {
			x, y := spawnCell(def, i)
			err := s.spawn.SpawnMob(
				s.nextMobID(),
				def.Class, def.MapName,
				domain.Position{X: int16(x), Y: int16(y)}, //nolint:gosec // G115: map cells fit int16.
				def.Name, hp, hp, respawnDelay(def),
			)
			if err != nil {
				s.log.Warn("seed: mob spawn failed", "class", def.Class, "map", def.MapName, "err", err)
				continue
			}
			n++
		}
	}
	return n
}

// nextMobID allocates a mob EntityID from the seeder's own space.
func (s *WorldSeeder) nextMobID() domain.EntityID {
	id := s.nextID
	s.nextID++
	return domain.EntityID(id)
}

// spawnCell picks the i-th cell of a deterministic walk around the box
// (no RNG so reboots place mobs identically).
func spawnCell(def script.SpawnDef, i int) (int, int) {
	if def.XSize <= 0 || def.YSize <= 0 {
		return def.X, def.Y
	}
	w := def.XSize * 2
	h := def.YSize * 2
	switch {
	case i < w:
		return def.X - def.XSize + i, def.Y - def.YSize
	case i < w+h:
		return def.X + def.XSize, def.Y - def.YSize + (i - w)
	case i < w+h+w:
		return def.X + def.XSize - (i - w - h), def.Y + def.YSize
	default:
		return def.X - def.XSize, def.Y + def.YSize - (i - w - h - w)
	}
}

// respawnDelay converts the corpus second pair to a Duration; zero or
// missing falls back to the default.
func respawnDelay(def script.SpawnDef) time.Duration {
	secs := def.Delay1
	if def.Delay2 > secs {
		secs = def.Delay2
	}
	if secs <= 0 {
		secs = defaultRespawnSeconds
	}
	return time.Duration(secs) * time.Second
}
