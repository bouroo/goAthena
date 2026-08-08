// Package infra adapts the content domain to a simple in-memory NPC store and a
// gnet-backed PacketWriter.
package infra

import (
	"context"
	"sync"

	"github.com/panjf2000/gnet/v2"
)

// MemoryNPCStore maps NPC entity GID → script name. NPCs are registered when they
// spawn (the world NPC-spawn path populates this); clicks resolve through it.
type MemoryNPCStore struct {
	mu    sync.RWMutex
	byGID map[uint32]string
}

// NewMemoryNPCStore builds an empty NPC store.
func NewMemoryNPCStore() *MemoryNPCStore {
	return &MemoryNPCStore{byGID: make(map[uint32]string)}
}

// Register maps an NPC GID to its script name.
func (s *MemoryNPCStore) Register(gid uint32, scriptName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byGID[gid] = scriptName
}

// ScriptForNPC resolves the script name for an NPC GID.
func (s *MemoryNPCStore) ScriptForNPC(_ context.Context, gid uint32) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	name, ok := s.byGID[gid]
	return name, ok
}

// GnetPacketWriter adapts a gnet.Conn to domain.PacketWriter via AsyncWrite.
type GnetPacketWriter struct {
	Conn gnet.Conn
}

// WritePacket sends raw bytes to the connection (concurrency-safe via AsyncWrite).
func (w GnetPacketWriter) WritePacket(data []byte) {
	_ = w.Conn.AsyncWrite(data, nil)
}
