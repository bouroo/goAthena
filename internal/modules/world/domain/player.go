package domain

import (
	"errors"
	"sync"

	gwdomain "github.com/bouroo/goAthena/internal/modules/gateway/domain"
	"github.com/bouroo/goAthena/pkg/ro/aoi"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// Sentinel errors for the player registry.
var (
	// ErrPlayerAlreadyRegistered: the account already has a live player. RO
	// allows one character online per account, so a second enter for the same
	// account is a replay/double-login and is rejected rather than shadowing
	// the first session.
	ErrPlayerAlreadyRegistered = errors.New("player already registered")
)

// PCWalkSpeed is the default PC walk speed in kRO units (rAthena's
// DEFAULT_WALK_SPEED = 150). The char table carries no per-char speed column
// for the combat slice, so every PC spawns at the default.
const PCWalkSpeed int16 = 150

// Player is the world's in-memory representation of a logged-in PC: the
// connection to reach the client, the identity the AOI grid keys it by, and
// the appearance slice the ZC_SPAWN_UNIT packet serializes. It is built once
// from a loaded Character at enter-world and held by PlayerRegistry for the
// life of the session.
//
// Identity convention (PACKETVER 20250604, verified against rAthena
// clif.cpp:1268-1270): a PC's AOI EntityID equals its account_id — rAthena's
// block_list is keyed by account_id (bl->id), and the spawn packet's AID is
// that same value. The GID is the char_id. One account → one PC online, so
// account_id is a unique entity key.
type Player struct {
	// mu guards the mutable post-enter fields PosX, PosY, Dir. Every other
	// field is set once at enter-world and never mutated, so it is safe to
	// read without the lock. PosX/PosY/Dir, however, are written by the
	// movement worker (M4c) and read by a concurrent EnterWorld on a
	// different connection when it builds a neighbor's SpawnUnit — a
	// cross-goroutine read/write the Go race detector would flag without
	// this lock. The worker is the sole writer; enter-world and the
	// spawn/walk builders are readers.
	mu sync.RWMutex

	// Conn is the transport-agnostic link to the player's client. The spawn
	// use case writes the self-spawn frame through it; the AOI broadcast
	// writes neighbor spawns through other players' Conns. Write must be safe
	// to call from a goroutine other than the conn's own dispatch goroutine
	// (M4b switches TCP to gnet.AsyncWrite for exactly this).
	Conn gwdomain.Conn

	// EntityID is the AOI grid key, equal to AccountID for a PC. The AOI
	// grid stores a *aoi.Entity built from this ID and the spawn position;
	// the registry looks players up by EntityID when fanning out broadcasts.
	EntityID aoi.EntityID

	// AccountID (AID) and CharID (GID) are the spawn-packet identity pair.
	AccountID uint32
	CharID    uint32

	// MapName is the map the player is currently on (lower-case rAthena name
	// without extension, e.g. "prontera"). Drives the per-map inverted index.
	MapName string

	// Position at last spawn or move. Dir is the facing byte (0..15).
	PosX int16
	PosY int16
	Dir  uint8

	// Appearance — the char-table columns ZC_SPAWN_UNIT serializes. Field
	// names match the kernel SpawnUnitResponse; the SpawnUnit builder below
	// is the single place that maps Player → wire.
	Name   string
	Job    uint16 // class → SpawnUnitResponse.Job
	CLevel uint16 // base_level → CLevel
	// Base stats, snapshotted from the Character at enter-world. Combat reads
	// them to derive BaseATK (Str/Dex/Luk) and the mode-specific mob soft-DEF; they
	// are set once at spawn and, for the combat slice, never mutated (status-point
	// spend and equipment land at M10/M12, which will refresh this snapshot).
	Str         uint16
	Agi         uint16
	Vit         uint16
	Int         uint16
	Dex         uint16
	Luk         uint16
	Head        uint8  // hair → Head
	HeadPalette uint16 // hair_color → HeadPalette
	BodyPalette uint16 // clothes_color → BodyPalette
	Weapon      uint16
	Shield      uint16
	HeadTop     uint16 // → Accessory2 (LOOK_HEAD_TOP)
	HeadMid     uint16 // → Accessory3 (LOOK_HEAD_MID)
	HeadBottom  uint16 // → Accessory  (LOOK_HEAD_BOTTOM)
	Robe        uint16
	Body        uint16
	Sex         uint8  // wire byte (0=F, 1=M)
	MaxHP       uint32 // held for M7 status burst; spawn writes -1, not this
	HP          uint32
	MaxSP       uint32
	SP          uint32
	Option      uint32 // effect bits → EffectState
	Manner      uint16 // → Honor
	Karma       uint8  // → IsPKModeON (1 when non-zero)
}

// SpawnUnit builds the ZC_SPAWN_UNIT frame this player emits to a client —
// both for its own self-spawn and for the broadcast to AOI neighbors. The
// field mapping follows rAthena clif_spawn_unit (clif.cpp:1261-1341) for a PC
// at PACKETVER 20250604:
//
//   - AID = account_id, GID = char_id (PACKETVER >= 20131223 branch).
//   - accessory/accessory2/accessory3 = head_bottom/head_top/head_mid
//     (clif.cpp:1290-1292, the non-guild-flag branch).
//   - honor = manner, virtue = 0 (opt3, no status-change → 0).
//   - maxHP = HP = -1: the monster_hp_bars_info block is BL_MOB-only
//     (clif.cpp:1313-1319), so a PC advertises no HP bar here; the real HP
//     rides a separate status packet at M7.
//   - body = 0: PACKETVER >= 20231220 uses LOOK_BODY2, which is 0 for a
//     novice (clif.cpp:1330).
//   - xSize = ySize = 5 for a PC; ObjectType = 0 (PC).
func (p *Player) SpawnUnit() packet.SpawnUnitResponse {
	posX, posY, dir := p.Position()
	return packet.SpawnUnitResponse{
		ObjectType:  0, // PC
		AID:         p.AccountID,
		GID:         p.CharID,
		Speed:       PCWalkSpeed,
		BodyState:   0,
		HealthState: 0,
		EffectState: int32(p.Option), //nolint:gosec // G115: uint32 option → int32 wire slot, rAthena stores opt in int32
		Job:         int16(p.Job),    //nolint:gosec // G115: uint16 class → int16 wire slot
		Head:        uint16(p.Head),  //nolint:gosec // G115: uint8 hair → uint16 wire slot
		Weapon:      uint32(p.Weapon),
		Shield:      uint32(p.Shield),
		Accessory:   p.HeadBottom,         // LOOK_HEAD_BOTTOM
		Accessory2:  p.HeadTop,            // LOOK_HEAD_TOP
		Accessory3:  p.HeadMid,            // LOOK_HEAD_MID
		HeadPalette: int16(p.HeadPalette), //nolint:gosec // G115: uint16 → int16 wire slot
		BodyPalette: int16(p.BodyPalette), //nolint:gosec // G115: uint16 → int16 wire slot
		HeadDir:     0,
		Robe:        p.Robe,
		GUID:        0,
		GEmblemVer:  0,
		Honor:       int16(p.Manner), //nolint:gosec // G115: uint16 manner → int16 wire slot
		Virtue:      0,
		IsPKModeON:  pkModeFlag(p.Karma),
		Sex:         p.Sex,
		PosX:        posX,
		PosY:        posY,
		Dir:         dir,
		XSize:       5,
		YSize:       5,
		CLevel:      int16(p.CLevel), //nolint:gosec // G115: uint16 level → int16 wire slot
		Font:        0,
		MaxHP:       -1, // PC: no HP bar in spawn (clif.cpp:1317-1318)
		HP:          -1,
		IsBoss:      0, // BOSSTYPE_NONE for a PC
		Body:        0, // LOOK_BODY2 = 0 for novice
		Name:        p.Name,
	}
}

// Vitals returns the current HP and SP under the player's read lock.
func (p *Player) Vitals() (hp, sp uint32) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.HP, p.SP
}

// Heal restores the player's HP and SP by the given signed deltas, clamping each
// resource to [0, Max]. A zero maximum leaves that resource unchanged.
func (p *Player) Heal(hp, sp int32) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.HP = healedValue(p.HP, p.MaxHP, hp)
	p.SP = healedValue(p.SP, p.MaxSP, sp)
}

func healedValue(current, maximum uint32, delta int32) uint32 {
	if maximum == 0 {
		return current
	}
	if delta >= 0 {
		increase := uint32(delta) //nolint:gosec // G115: non-negative int32 is safe to represent as uint32
		if increase >= maximum-current {
			return maximum
		}
		return current + increase
	}
	decrease := uint32(-int64(delta)) //nolint:gosec // G115: int64 negation keeps MinInt32 representable before narrowing
	if decrease >= current {
		return 0
	}
	return current - decrease
}

// Position returns the player's cell and facing under the read lock. SpawnUnit
// and the M4c movement worker read position through this so a concurrent
// SetPosition (a move resolving on the worker goroutine) cannot race a
// neighbor's spawn/walk broadcast building on a different goroutine.
func (p *Player) Position() (posX, posY int16, dir uint8) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.PosX, p.PosY, p.Dir
}

// SetPosition updates the player's cell and facing under the write lock. The
// movement worker is the sole caller (M4c): it pathfinds, then commits the
// destination atomically so a concurrent SpawnUnit reads either the old or the
// new cell, never a torn half-move.
func (p *Player) SetPosition(posX, posY int16, dir uint8) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.PosX, p.PosY, p.Dir = posX, posY, dir
}

// WalkUnit builds the ZC_UNIT_WALKING frame this player emits to AOI observers
// when it begins a move. The appearance slice is identical to SpawnUnit (same
// PC defaults — a walk broadcast is a spawn-with-motion, not a different view);
// only the position fields differ: SrcX/SrcY is the cell the move leaves,
// DestX/DestY is the cell it targets, and MoveStartTime is the server's
// monotone tick at acceptance so observers interpolate the sprite in sync.
// rAthena clif_unit_walking (clif.cpp) writes the same appearance block as
// clif_spawn_unit and inserts moveStartTime + the 6-byte moveData at the
// PACKETVER >= 20131223 offsets the kernel encoder mirrors.
func (p *Player) WalkUnit(srcX, srcY, destX, destY int16, moveStartTime uint32) packet.UnitWalkingResponse {
	return packet.UnitWalkingResponse{
		ObjectType:    0, // PC
		AID:           p.AccountID,
		GID:           p.CharID,
		Speed:         PCWalkSpeed,
		BodyState:     0,
		HealthState:   0,
		EffectState:   int32(p.Option), //nolint:gosec // G115: uint32 option → int32 wire slot
		Job:           int16(p.Job),    //nolint:gosec // G115: uint16 class → int16 wire slot
		Head:          uint16(p.Head),  //nolint:gosec // G115: uint8 hair → uint16 wire slot
		Weapon:        uint32(p.Weapon),
		Shield:        uint32(p.Shield),
		Accessory:     p.HeadBottom, // LOOK_HEAD_BOTTOM
		Accessory2:    p.HeadTop,    // LOOK_HEAD_TOP
		Accessory3:    p.HeadMid,    // LOOK_HEAD_MID
		MoveStartTime: moveStartTime,
		HeadPalette:   int16(p.HeadPalette), //nolint:gosec // G115: uint16 → int16 wire slot
		BodyPalette:   int16(p.BodyPalette), //nolint:gosec // G115: uint16 → int16 wire slot
		HeadDir:       0,
		Robe:          p.Robe,
		GUID:          0,
		GEmblemVer:    0,
		Honor:         int16(p.Manner), //nolint:gosec // G115: uint16 manner → int16 wire slot
		Virtue:        0,
		IsPKModeON:    pkModeFlag(p.Karma),
		Sex:           p.Sex,
		SrcX:          srcX,
		SrcY:          srcY,
		DestX:         destX,
		DestY:         destY,
		XSize:         5,
		YSize:         5,
		CLevel:        int16(p.CLevel), //nolint:gosec // G115: uint16 level → int16 wire slot
		Font:          0,
		MaxHP:         -1, // PC: no HP bar (clif.cpp:1317-1318)
		HP:            -1,
		IsBoss:        0, // BOSSTYPE_NONE for a PC
		Body:          0, // LOOK_BODY2 = 0 for novice
		Name:          p.Name,
	}
}

// pkModeFlag returns the isPKModeON byte rAthena derives from karma
// (clif.cpp:1304: `(sd && sd->status.karma) ? 1 : 0`).
func pkModeFlag(karma uint8) uint8 {
	if karma != 0 {
		return 1
	}
	return 0
}

// PlayerRegistry is the in-process index of live PC sessions: the primary
// by-account index (for routing an inbound packet from a conn to its player)
// and a per-map inverted index (for AOI broadcast fan-out). It is the
// SessionRegistry the plan calls for — simpler than the legacy Valkey
// cross-zone registry because the combat slice runs one zone in one process.
//
// Concurrency: all access is guarded by one mutex. The dispatch goroutine
// reads/writes per-conn on its own, but AOI broadcast writes to *other*
// players' Conns and the tick + enter paths mutate the registry from
// multiple goroutines, so the registry is the synchronization point.
type PlayerRegistry struct {
	mu        sync.RWMutex
	byAccount map[uint32]*Player            // accountID → player (primary)
	byMap     map[string]map[uint32]*Player // mapName → accountID → player
}

// NewPlayerRegistry returns an empty registry.
func NewPlayerRegistry() *PlayerRegistry {
	return &PlayerRegistry{
		byAccount: make(map[uint32]*Player),
		byMap:     make(map[string]map[uint32]*Player),
	}
}

// Register indexes a player by account and map. It rejects a second player
// for the same account (one PC online per account). The caller is expected to
// have already added the player's aoi.Entity to the map's GridManager; this
// method only owns the registry indices.
func (r *PlayerRegistry) Register(p *Player) error {
	if p == nil {
		return errors.New("register nil player")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.byAccount[p.AccountID]; exists {
		return ErrPlayerAlreadyRegistered
	}
	r.byAccount[p.AccountID] = p
	mp, ok := r.byMap[p.MapName]
	if !ok {
		mp = make(map[uint32]*Player)
		r.byMap[p.MapName] = mp
	}
	mp[p.AccountID] = p
	return nil
}

// Unregister removes a player from both indices. It is idempotent: an absent
// account is a no-op (a late cleanup after a duplicate unregister, or a
// disconnect arriving after enter failed). It returns the removed player so
// the caller can tear down the AOI entity and broadcast a vanish with the
// last-known position.
func (r *PlayerRegistry) Unregister(accountID uint32) *Player {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.byAccount[accountID]
	if !ok {
		return nil
	}
	delete(r.byAccount, accountID)
	if mp := r.byMap[p.MapName]; mp != nil {
		delete(mp, accountID)
		if len(mp) == 0 {
			delete(r.byMap, p.MapName)
		}
	}
	return p
}

// ByAccount returns the live player for an account, or (nil,false) if none.
// The returned pointer is shared; callers must not mutate it.
func (r *PlayerRegistry) ByAccount(accountID uint32) (*Player, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.byAccount[accountID]
	return p, ok
}

// OnMap returns a defensive snapshot of the players currently on mapName, so
// a broadcast fan-out can iterate without holding the registry lock while
// writing to other players' Conns. Returns an empty (non-nil) slice for an
// unknown or empty map.
func (r *PlayerRegistry) OnMap(mapName string) []*Player {
	r.mu.RLock()
	defer r.mu.RUnlock()
	src := r.byMap[mapName]
	out := make([]*Player, 0, len(src))
	for _, p := range src {
		out = append(out, p)
	}
	return out
}
