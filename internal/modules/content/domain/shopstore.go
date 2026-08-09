package domain

import (
	"context"
)

// ShopStore resolves an NPC entity GID to the name of the shop that NPC sells,
// so a shop click (CZ_ACK_SELECT DEALTYPE) can find which catalog to open. It
// mirrors NPCStore (GID -> script name): the world/script NPC registry populates
// it when NPC shops spawn. The returned shop name is resolved against the
// commerce shop CatalogRegistry (name -> priced item catalog).
type ShopStore interface {
	// ShopForNPC returns the shop name registered for the NPC GID, or false when
	// the NPC sells no shop.
	ShopForNPC(ctx context.Context, npcGID uint32) (shopName string, ok bool)
	// RegisterShop associates an NPC GID with the name of the shop it sells.
	RegisterShop(npcGID uint32, shopName string)
}
