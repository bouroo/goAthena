package domain

import "sync"

// MemoryShopCatalog is a map-backed ShopCatalog. The catalog is fully populated
// at boot from data/shop/*.yml before any player can connect, so the read path
// is the only one exercised at runtime; an RWMutex keeps the door open for a
// future admin-driven reload without changing the public contract.
type MemoryShopCatalog struct {
	mu    sync.RWMutex
	shops map[uint32]Shop
}

// NewMemoryShopCatalog creates an empty catalog ready to receive Add calls.
func NewMemoryShopCatalog() *MemoryShopCatalog {
	return &MemoryShopCatalog{shops: make(map[uint32]Shop)}
}

// Add inserts or replaces a shop keyed by its NPC EntityID. Intended for the
// boot-time publish loop; not safe to call concurrently with reads.
func (c *MemoryShopCatalog) Add(shop Shop) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.shops[shop.NPCID] = shop
}

// Get returns the shop registered for npcID. The second result is false when
// no shop is registered for that id, so callers can distinguish "not a shop"
// from "shop with zero items".
func (c *MemoryShopCatalog) Get(npcID uint32) (Shop, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s, ok := c.shops[npcID]
	return s, ok
}
