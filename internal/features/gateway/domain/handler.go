// Package domain contains entities and port interfaces for the gateway
// feature (WS-A): packet codec, TCP/WS ingress, gRPC routing.
package domain

import (
	"context"
	"sync"
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
	// invIndex maps 0-based inventory position to DB item ID.
	invIndex map[uint16]uint32
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
