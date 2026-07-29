package domain

// ShopItem is one entry in a shop's catalog: the item ID and its buy price in zeny.
type ShopItem struct {
	NameID uint32
	Price  uint32
}

// Shop is the catalog for one NPC shop.
type Shop struct {
	NPCID uint32 // EntityID assigned at boot
	Name  string
	Items []ShopItem
}

// ShopCatalog resolves an NPC EntityID to its Shop. Returns ok=false if the NPC is not a shop.
type ShopCatalog interface {
	Get(npcID uint32) (Shop, bool)
}
