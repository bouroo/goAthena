// Package domain holds the shop commerce sub-context: the Shop aggregate (an
// NPC shop with a priced item catalog) and the buy/sell port.
package domain

// ShopItem is one entry in an NPC shop's catalog.
type ShopItem struct {
	NameID    uint32 // item_db id
	Price     int32  // buy price (what the player pays)
	SellPrice int32  // sell price (what the shop pays the player); typically Price/2
}

// Shop is an NPC shop with a named catalog of priced items.
type Shop struct {
	Name  string
	Items []ShopItem
}

// FindBuy returns the catalog entry for nameID, or false.
func (s Shop) FindBuy(nameID uint32) (ShopItem, bool) {
	for _, it := range s.Items {
		if it.NameID == nameID {
			return it, true
		}
	}
	return ShopItem{}, false
}

// CatalogRegistry holds named NPC shops resolved by name.
type CatalogRegistry struct {
	shops map[string]Shop
}

// NewCatalogRegistry builds a registry from the given shops.
func NewCatalogRegistry(shops ...Shop) *CatalogRegistry {
	r := &CatalogRegistry{shops: make(map[string]Shop, len(shops))}
	for _, s := range shops {
		r.shops[s.Name] = s
	}
	return r
}

// Get returns the shop by name, or false.
func (r *CatalogRegistry) Get(name string) (Shop, bool) {
	s, ok := r.shops[name]
	return s, ok
}
