// Package app implements the world bounded context's use cases. M3 shipped
// the CZ_ENTER trust gate; this file adds the M4b spawn-on-enter use case that
// the gate calls after it has accepted the connection.
package app

import (
	"context"
	"fmt"

	chardomain "github.com/bouroo/goAthena/internal/modules/character/domain"
	gwdomain "github.com/bouroo/goAthena/internal/modules/gateway/domain"
	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/aoi"
)

// SpawnService owns the enter-world flow: load the character, load the map,
// build and register the player, add it to the map's AOI grid, then drive the
// two-way spawn exchange — the entering client sees the players already on the
// map, and those players see the newcomer. It holds the three collaborators it
// crosses (character lookup, map load, the live-session registry) and nothing
// else; combat, movement, and the tick are later use cases.
//
// Concurrency note: the registry and AOI grid are concurrency-safe; the
// broadcast writes to *other* players' Conns from this goroutine, so the TCP
// adapter must be on AsyncWrite by the time EnterWorld is wired (task #22).
type SpawnService struct {
	chars    domain.CharacterGetter
	maps     domain.MapStore
	registry *domain.PlayerRegistry
}

// NewSpawnService binds the three enter-world collaborators. The registry is
// shared across the whole process (one per server), so callers must pass the
// same instance to every SpawnService and to the disconnect/movement paths.
func NewSpawnService(chars domain.CharacterGetter, maps domain.MapStore, registry *domain.PlayerRegistry) *SpawnService {
	return &SpawnService{chars: chars, maps: maps, registry: registry}
}

// EnterWorld is the spawn-on-enter use case. It runs after MapEnterHandler has
// verified the session and cached auth on the conn; the verified accountID and
// the packet's charID are the lookup keys. On any failure it returns a wrapped
// error so the handler can log it and drop the connection; it does not send a
// refuse frame (that is the gate's job, already done by the time we get here).
//
// spawn overrides the character's last_x/last_y only when the caller supplies
// non-zero values (the M3 default spawn is the novice start cell; M4b uses the
// character's persisted last position by passing zeros).
func (s *SpawnService) EnterWorld(ctx context.Context, conn gwdomain.Conn, accountID, charID uint32, spawn SpawnPoint) error {
	char, err := s.chars.GetByID(ctx, accountID, charID)
	if err != nil {
		return fmt.Errorf("spawn: load character account %d char %d: %w", accountID, charID, err)
	}

	mp, err := s.maps.Load(ctx, char.LastMap)
	if err != nil {
		return fmt.Errorf("spawn: load map %q: %w", char.LastMap, err)
	}

	posX, posY, dir := resolveSpawn(char, spawn)

	player := &domain.Player{
		Conn:        conn,
		EntityID:    aoi.EntityID(accountID), // PC: AOI key = account_id
		AccountID:   accountID,
		CharID:      charID,
		MapName:     char.LastMap,
		PosX:        posX,
		PosY:        posY,
		Dir:         dir,
		Name:        char.Name,
		Job:         char.Class,
		CLevel:      char.BaseLevel,
		Head:        char.Hair,
		HeadPalette: char.HairColor,
		BodyPalette: char.ClothesColor,
		Weapon:      char.Weapon,
		Shield:      char.Shield,
		HeadTop:     char.HeadTop,
		HeadMid:     char.HeadMid,
		HeadBottom:  char.HeadBottom,
		Robe:        char.Robe,
		Body:        char.Body,
		Sex:         char.Sex,
		MaxHP:       char.MaxHP,
		HP:          char.HP,
		Option:      char.Option,
		Manner:      char.Manner,
		Karma:       char.Karma,
	}

	if err := s.registry.Register(player); err != nil {
		return fmt.Errorf("spawn: register account %d: %w", accountID, err)
	}
	// Roll back a successful Register if any later step fails. A defer cannot
	// be used because the happy path must keep the player registered past the
	// return; the caller (MapEnterHandler) owns Unregister on disconnect, and a
	// mid-spawn failure rolls back explicitly here.
	rollback := func() { s.registry.Unregister(accountID) }

	entity := &aoi.Entity{
		ID:   player.EntityID,
		Type: aoi.EntityPlayer,
		X:    int(posX),
		Y:    int(posY),
	}
	if err := mp.AOI.AddEntity(entity); err != nil {
		rollback()
		return fmt.Errorf("spawn: add AOI entity account %d at (%d,%d): %w", accountID, posX, posY, err)
	}

	// Self-spawn: the entering client sees its own character.
	selfFrame := player.SpawnUnit()
	if err := selfFrame.Encode(connWriter{conn}); err != nil {
		_ = mp.AOI.RemoveEntity(player.EntityID)
		rollback()
		return fmt.Errorf("spawn: encode self ZC_SPAWN_UNIT: %w", err)
	}

	// Two-way spawn exchange with everyone already on the map within AOI range:
	// the newcomer's spawn goes to each neighbor, and each neighbor's spawn goes
	// to the newcomer. A neighbor whose Conn write fails is dead — tear it down
	// (idempotent) so its stale AOI entity does not keep polluting broadcasts.
	// The entering player's spawn is not aborted by a neighbor's dead socket.
	visible := mp.AOI.QueryVisible(int(posX), int(posY))
	for _, e := range visible {
		if e.ID == player.EntityID {
			continue // do not self-spawn twice
		}
		neighbor, ok := s.registry.ByAccount(uint32(e.ID))
		if !ok {
			// An AOI entity without a registry entry is an NPC/mob (M5+) or a
			// torn-down player the grid has not yet removed; skip it for the
			// PC-only spawn exchange M4b ships.
			continue
		}
		if err := selfFrame.Encode(connWriter{neighbor.Conn}); err != nil {
			s.dropNeighbor(mp, neighbor)
			continue
		}
		if err := neighbor.SpawnUnit().Encode(connWriter{conn}); err != nil {
			_ = mp.AOI.RemoveEntity(player.EntityID)
			rollback()
			return fmt.Errorf("spawn: encode neighbor ZC_SPAWN_UNIT for account %d: %w", e.ID, err)
		}
	}

	return nil
}

// dropNeighbor cleans up a neighbor whose Conn write failed: unregister the
// session and remove its AOI entity. Both are idempotent, so a concurrent
// disconnect path racing this teardown leaves the registry consistent. The
// neighbor's own dispatch goroutine will observe the dead socket and close;
// this just stops the world from broadcasting to it.
func (s *SpawnService) dropNeighbor(mp *domain.Map, p *domain.Player) {
	if p == nil {
		return
	}
	s.registry.Unregister(p.AccountID)
	_ = mp.AOI.RemoveEntity(p.EntityID)
}

// resolveSpawn picks the spawn cell: the caller-supplied override (non-zero)
// when present, otherwise the character's persisted last_x/last_y. Dir falls
// back to 0 (face north) when the override does not supply one — the char
// table has no facing column, so a freshly loaded character always faces 0.
func resolveSpawn(char *chardomain.Character, spawn SpawnPoint) (posX int16, posY int16, dir uint8) {
	if spawn.PosX != 0 || spawn.PosY != 0 {
		return spawn.PosX, spawn.PosY, spawn.Dir
	}
	return int16(char.LastX), int16(char.LastY), 0 //nolint:gosec // G115: uint16 cell coord → int16 wire slot, map dims fit
}
