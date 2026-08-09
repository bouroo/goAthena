package infra

import (
	"context"
	"sync"
)

// MemoryShopStore maps NPC entity GID -> shop name. Mirrors MemoryNPCStore: NPC
// shops are registered when they spawn (the dev seed in commerce/shop registers a
// starter shop); a click resolves the shop name through it, then the commerce
// CatalogRegistry resolves that name to a priced catalog.
type MemoryShopStore struct {
	mu    sync.RWMutex
	byGID map[uint32]string
}

// NewMemoryShopStore builds an empty shop store.
func NewMemoryShopStore() *MemoryShopStore {
	return &MemoryShopStore{byGID: make(map[uint32]string)}
}

// RegisterShop maps an NPC GID to its shop name.
func (s *MemoryShopStore) RegisterShop(gid uint32, shopName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byGID[gid] = shopName
}

// ShopForNPC resolves the shop name for an NPC GID.
func (s *MemoryShopStore) ShopForNPC(_ context.Context, gid uint32) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	name, ok := s.byGID[gid]
	return name, ok
}
