package infra

import (
	"context"
	"fmt"

	"github.com/bouroo/goAthena/internal/modules/content/domain"
	gwdomain "github.com/bouroo/goAthena/internal/modules/gateway/domain"
	worlddomain "github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/aoi"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// connWriter adapts a gateway domain.Conn (Write returns only error) to the
// io.Writer the kernel packet encoders target. Duplicated from world/app: each
// module keeps its own copy so the gateway domain is the only cross-module
// dependency a feature module needs.
type connWriter struct{ conn gwdomain.Conn }

func (w connWriter) Write(p []byte) (int, error) {
	if err := w.conn.Write(p); err != nil {
		return 0, fmt.Errorf("write to conn: %w", err)
	}
	return len(p), nil
}

// scriptWorld implements the domain.World interface used by builtins to effect changes
// upon the player/world, wrapping the underlying worlddomain registries.
type scriptWorld struct {
	players   *worlddomain.PlayerRegistry
	maps      worlddomain.MapStore
	positions worlddomain.PositionStore
}

// NewScriptWorld constructs the domain.World interface implementation using the
// cross-module worlddomain.PlayerRegistry, MapStore (to switch AOI grids across
// maps), and PositionStore (to persist last_map/last_x/last_y so a fresh
// CZ_ENTER re-enter spawns on the destination map).
func NewScriptWorld(players *worlddomain.PlayerRegistry, maps worlddomain.MapStore, positions worlddomain.PositionStore) domain.World {
	return &scriptWorld{players: players, maps: maps, positions: positions}
}

// Warp implements domain.World.Warp: migrate the live player from their current
// map to the destination map. The player leaves the old map's AOI grid and
// registry bucket, takes the destination's map name + position, joins the
// destination's AOI grid, and the new location is persisted so a follow-up
// CZ_ENTER re-enter spawns on it. The client is told to change maps last, via
// ZC_NPCACK_MAPMOVE — every server-side transition (AOI + registry + persist) is
// consistent before the client reconnects, so a reconnect that races the packet
// still finds the player on the destination map.
//
// A missing source map (the player's current map could not be loaded) is not
// fatal: the AOI entity for a grid we cannot reach is simply not removed there,
// and the migration proceeds. An unloadable destination, however, aborts before
// any state changes to avoid dropping the player onto a map the server cannot
// drive. Each step wraps its error so the caller (scriptHost.Warp) logs the cause.
func (w *scriptWorld) Warp(ctx context.Context, conn gwdomain.Conn, accountID, charID uint32, mapName string, x, y int) error {
	player, ok := w.players.ByAccount(accountID)
	if !ok {
		return fmt.Errorf("warp: player not found for account %d", accountID)
	}

	// Load the destination and join its AOI grid FIRST, before any mutation. A
	// failure here aborts with the player still fully on the source map — no
	// half-migrated state. Never drop the player onto a map the server cannot
	// drive. (Adding to the destination before removing from the source is safe:
	// the two maps have independent AOI grids keyed by the same EntityID.)
	newMap, err := w.maps.Load(ctx, mapName)
	if err != nil {
		return fmt.Errorf("warp: load destination map %q: %w", mapName, err)
	}
	entity := &aoi.Entity{
		ID:   player.EntityID,
		Type: aoi.EntityPlayer,
		X:    x,
		Y:    y,
	}
	if err := newMap.AOI.AddEntity(entity); err != nil {
		return fmt.Errorf("warp: add AOI entity account %d on map %q: %w", accountID, mapName, err)
	}

	// Leave the source map's AOI grid. A missing source map (a stale MapName) is
	// non-fatal: the entity is simply not removed there, and the migration
	// proceeds. RemoveEntity is idempotent.
	if oldMap, oldErr := w.maps.Load(ctx, player.MapName); oldErr == nil && oldMap != nil {
		_ = oldMap.AOI.RemoveEntity(player.EntityID)
	}

	// Re-key the registry bucket and adopt the destination map name + cell. Dir
	// is preserved. ByAccount succeeded above; a concurrent unregister between
	// then and now would make Relocate return false — report it (the AOI/entity
	// bookkeeping above is harmless against a player no longer live).
	if !w.players.Relocate(accountID, mapName) {
		return fmt.Errorf("warp: player %d unregistered mid-warp", accountID)
	}
	_, _, dir := player.Position()
	player.SetPosition(int16(x), int16(y), dir) //nolint:gosec // G115: warp coords are map-cell values that fit int16

	// Persist the new location so a fresh CZ_ENTER re-enter spawns on it. The
	// dialog handler does not know the character id (ConnAuth carries the account
	// only), so the authoritative charID is read from the live Player session —
	// the same session-by-account lookup above resolved. The charID parameter is
	// therefore ignored here; it survives in the signature for interface symmetry
	// with Heal.
	if err := w.positions.SavePosition(ctx, accountID, player.CharID, mapName, uint16(x), uint16(y)); err != nil { //nolint:gosec // G115: map-cell coords fit uint16
		return fmt.Errorf("warp: save position account %d char %d: %w", accountID, player.CharID, err)
	}

	// Tell the client to change maps (ZC_NPCACK_MAPMOVE). Sent last so the
	// AOI/registry/persistence are consistent before the client reconnects with a
	// fresh CZ_ENTER — a reconnect that races the packet still finds the player on
	// the destination map.
	if err := (packet.MapMoveResponse{MapName: mapName, X: uint16(x), Y: uint16(y)}).Encode(connWriter{conn}); err != nil { //nolint:gosec // G115: map-cell coords fit uint16
		return fmt.Errorf("warp: encode ZC_NPCACK_MAPMOVE: %w", err)
	}

	return nil
}

// Heal implements domain.World.Heal, updating the player's HP/SP by percentage
// and broadcasting ZC_PAR_CHANGE.
func (w *scriptWorld) Heal(ctx context.Context, conn gwdomain.Conn, accountID, charID uint32, hpPct, spPct int) error {
	player, ok := w.players.ByAccount(accountID)
	if !ok {
		return fmt.Errorf("heal: player not found for account %d", accountID)
	}

	player.HP = (player.MaxHP * uint32(hpPct)) / 100 //nolint:gosec // G115: HP/SP percentages are bounded 0–100 game values
	player.SP = (player.MaxSP * uint32(spPct)) / 100 //nolint:gosec // G115: HP/SP percentages are bounded 0–100 game values

	// Note: in RAthena, clipping happens if > max
	if player.HP > player.MaxHP {
		player.HP = player.MaxHP
	}
	if player.SP > player.MaxSP {
		player.SP = player.MaxSP
	}

	cw := connWriter{conn: conn}

	if err := (packet.ParChangeResponse{VarID: packet.SPHP, Count: int32(player.HP)}).Encode(cw); err != nil { //nolint:gosec // G115: HP/SP are bounded after Max-clip above
		return fmt.Errorf("encode ZC_PAR_CHANGE SPHP: %w", err)
	}
	if err := (packet.ParChangeResponse{VarID: packet.SPSP, Count: int32(player.SP)}).Encode(cw); err != nil { //nolint:gosec // G115: HP/SP are bounded after Max-clip above
		return fmt.Errorf("encode ZC_PAR_CHANGE SPSP: %w", err)
	}

	return nil
}
