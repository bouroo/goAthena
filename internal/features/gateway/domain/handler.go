// Package domain contains entities and port interfaces for the gateway
// feature (WS-A): packet codec, TCP/WS ingress, gRPC routing.
package domain

import (
	"context"
	"sync"

	"github.com/bouroo/goAthena/pkg/ro/textenc"
)

// MonsterSpawn defines the minimal fields the domain layer needs to track
// monster HP and spawns.
type MonsterSpawn struct {
	GID   uint32
	MaxHP int32
}

// ConnectionInfo describes a single accepted TCP connection. It is built
// once at OnOpen time and threaded through the PacketHandler so handlers
// can log the peer and timestamp without re-querying gnet.Conn.
//
// AccountID and CharID are the only mutable fields: the dispatch handler
// sets them after a successful CZ_ENTER so subsequent CZ_REQUEST_MOVE
// packets can be attributed to the right zone entity without re-deriving
// the AID from the wire (the move packet carries no AID) and the
// post-actorinit status burst (M9) can fetch the character's stats via
// identity.GetCharacter. The handler chain takes the info by pointer so
// mutations persist across packets on the same connection.
type ConnectionInfo struct {
	mu        sync.Mutex // guards MonsterHP, BaseExp, JobExp against concurrent access (e.g. from respawn timers)
	ID        uint64
	RemoteIP  string
	OpenedAt  int64  // unix nanos
	AccountID uint32 // set by handleCZEnter on successful map enter
	CharID    uint32 // set by handleCZEnter on successful map enter
	MapName   string // set by handleCZEnter on successful map enter; drives session registry ForEachOnMap filtering
	// MonsterHP tracks per-connection monster HP for the M18 basic
	// attack path. Initialized during handleCZNotifyActorInit from
	// the static monsterSpawns table. When a monster's HP reaches 0
	// the handler sends ZC_NOTIFY_VANISH and removes the entry so
	// subsequent attacks on a dead monster are silently dropped.
	MonsterHP map[uint32]int32
	// BaseExp tracks the accumulated base experience (M19).
	BaseExp int32
	// JobExp tracks the accumulated job experience (M19).
	JobExp int32
	// BaseLevel is the character's base level, cached from GetCharacter on
	// map enter (handleCZNotifyActorInit). Used by applyMonsterKillExp to
	// detect base-level-up via stats/domain.ApplyBaseExpGain (D-213).
	BaseLevel uint32
	// Str, Dex, Luk are the character's base combat stats, cached from
	// GetCharacter on map enter.
	Str, Dex, Luk uint8
	// sp and maxSP are the character's current and maximum spell points
	// (SP, the mana pool), cached via SetSP from the identity
	// CharacterDetail populated during handleCZNotifyActorInit. Guarded
	// by mu alongside the other mutable combat fields.
	sp, maxSP uint32
	// invIndex maps 0-based inventory position to DB item ID.
	invIndex map[uint16]uint32
	// shopNPCID tracks the NPC GID the player is currently in a shop
	// dialog with. It is set on handleCZAckSelectDealType (Buy or Sell)
	// after the NPC resolves to a known shop entry, and cleared on
	// successful completion (or cancel) of a buy/sell transaction so
	// the next CZ_PC_PURCHASE_ITEMLIST / CZ_PC_SELL_ITEMLIST request
	// must re-anchor to a fresh deal-type selection.
	shopNPCID uint32
	// Codepage selects the wire text encoding for this connection's
	// character names / chat / NPC text. Set once at connection open from
	// the transport default (native TCP → configured gateway codepage;
	// WebSocket/roBrowser → UTF-8 passthrough). The zero value textenc.UTF8
	// is passthrough, so a connection that never sets it stays UTF-8.
	Codepage textenc.Codepage
	// Packetver is the per-connection PACKETVER selected at CA_LOGIN time.
	// It is set by handleCALogin from the client-supplied Version field
	// (with config fallback) and is immutable for the connection's
	// lifetime — both handleCALogin and handleCZEnter read it instead of
	// the global dispatch handler's defaultPacketver. N2 wires this
	// field; the codec still consumes a static *packet.DB, so per-shape
	// selection is deferred to N2.1.
	Packetver uint32
}

// SetCombatStats updates the character's base combat stats.
func (c *ConnectionInfo) SetCombatStats(str, dex, luk uint8) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Str = str
	c.Dex = dex
	c.Luk = luk
}

// CombatStats returns the character's base combat stats.
func (c *ConnectionInfo) CombatStats() (uint8, uint8, uint8) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Str, c.Dex, c.Luk
}

// SetInventoryIndex replaces the inventory index map.
func (c *ConnectionInfo) SetInventoryIndex(m map[uint16]uint32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.invIndex = m
}

// ResolveInventoryID returns the DB id for a 0-based position.
func (c *ConnectionInfo) ResolveInventoryID(pos uint16) (uint32, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	id, ok := c.invIndex[pos]
	return id, ok
}

// SetShopNPC records the NPC GID the player has just opened a shop
// dialog with. Called on handleCZAckSelectDealType after the NPC is
// resolved to a known shop entry; cleared on transaction completion
// or when the deal-type window is cancelled.
func (c *ConnectionInfo) SetShopNPC(npcID uint32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.shopNPCID = npcID
}

// ShopNPC returns the NPC GID of the active shop dialog (0 if none).
// Used by the buy/sell request handlers to re-anchor the request to
// the NPC whose price catalog governs the transaction.
func (c *ConnectionInfo) ShopNPC() uint32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.shopNPCID
}

// InitMonsterHP initializes the ConnectionInfo's MonsterHP map from a slice of MonsterSpawns.
func (c *ConnectionInfo) InitMonsterHP(spawns []MonsterSpawn) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.MonsterHP == nil {
		c.MonsterHP = make(map[uint32]int32, len(spawns))
	}
	for _, s := range spawns {
		c.MonsterHP[s.GID] = s.MaxHP
	}
}

// ApplyDamage applies damage to the specified monster's HP.
// Returns the remaining HP and whether the operation succeeded (true if the monster exists).
func (c *ConnectionInfo) ApplyDamage(gid uint32, damage int32) (int32, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	hp, ok := c.MonsterHP[gid]
	if !ok {
		return 0, false
	}
	hp -= damage
	c.MonsterHP[gid] = hp
	return hp, true
}

// RemoveMonster deletes a monster from the tracked HP map.
func (c *ConnectionInfo) RemoveMonster(gid uint32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.MonsterHP, gid)
}

// HasMonster reports whether a monster with the given GID is currently
// tracked in the per-connection HP map. Handlers consult this before
// spending a resource (SP, items, casts) so that an invalid or already-
// dead target fails fast without leaving the client's resource display
// out of sync with the server cache.
func (c *ConnectionInfo) HasMonster(gid uint32) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.MonsterHP[gid]
	return ok
}

// RespawnMonster re-inserts a monster into the HP map with its max HP.
// Returns false if the monster was not previously tracked or if the GID is not valid (no-op).
func (c *ConnectionInfo) RespawnMonster(gid uint32, maxHP int32) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.MonsterHP == nil {
		c.MonsterHP = make(map[uint32]int32)
	}
	c.MonsterHP[gid] = maxHP
	return true
}

// AddExp accumulates base and job experience.
func (c *ConnectionInfo) AddExp(base, job int32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.BaseExp += base
	c.JobExp += job
}

// ExpValues returns the current BaseExp and JobExp values.
func (c *ConnectionInfo) ExpValues() (int32, int32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.BaseExp, c.JobExp
}

// SetBaseExp sets the accumulated base experience to v. Used after a
// level-up to reset the in-band EXP to the carry-over (gain.NewExp),
// preventing the already-consumed EXP from re-triggering a level-up
// on the next kill.
func (c *ConnectionInfo) SetBaseExp(v int32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.BaseExp = v
}

// SetSP updates the cached current and maximum spell points (SP).
func (c *ConnectionInfo) SetSP(sp, maxSP uint32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sp = sp
	c.maxSP = maxSP
}

// SP returns the current spell points (SP).
func (c *ConnectionInfo) SP() uint32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sp
}

// MaxSP returns the maximum spell points (SP).
func (c *ConnectionInfo) MaxSP() uint32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.maxSP
}

// SpendSP deducts cost from the cached SP if the current SP covers it,
// returning the remaining SP and ok=true. If sp < cost the cache is left
// untouched and (sp, false) is returned; the sp<cost guard ensures no
// uint32 underflow.
func (c *ConnectionInfo) SpendSP(cost uint32) (uint32, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sp < cost {
		return c.sp, false
	}
	c.sp -= cost
	return c.sp, true
}

// Responder sends serialized packets back to the client. Each transport
// (gnet TCP, coder/websocket) supplies its own implementation; the port
// abstracts over async-write semantics so handlers stay transport-agnostic.
//
// SendPacket MUST be safe to call from the dispatch goroutine. For the TCP
// transport it delegates to gnet.Conn.AsyncWrite, which queues the buffer
// on the connection's outbound ring and returns immediately; for the WS
// transport it serializes over the active WebSocket read context.
type Responder interface {
	SendPacket(p []byte) error
}

// PacketHandler processes a decoded kRO packet. The gateway calls this for
// each packet extracted from the TCP stream by the codec.
//
// The handler uses resp to send replies (AC_ACCEPT_LOGIN / AC_REFUSE_LOGIN
// / …) back over the originating transport. Returning a non-nil error
// signals that the connection should be closed; the gnet layer treats
// handler errors as fatal and tears the connection down. Handlers that
// want the connection to stay open after a transient backend failure must
// log the cause and return nil.
//
// conn is passed by pointer so handlers can persist per-connection state
// (e.g. AccountID after CZ_ENTER) across successive packets on the same
// connection.
type PacketHandler interface {
	HandlePacket(ctx context.Context, conn *ConnectionInfo, resp Responder, cmd uint16, frame []byte) error
}
