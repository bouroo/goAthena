package shop

import "github.com/bouroo/goAthena/internal/modules/commerce/shop/domain"

// devShopName is the dev catalog's shop name. Both the CatalogRegistry seed and
// the ShopStore GID binding use it so a shop click on DevShopGID resolves to this
// catalog entry.
const devShopName = "Tool Shop"

// DevShopGID is the NPC entity GID the dev shop is bound to, so a shop click on
// that NPC resolves to the dev shop via the content ShopStore. A starter value;
// production shops bind GIDs from rAthena NPC shop blocks during world load.
const DevShopGID uint32 = 110000000

// newDevCatalog builds the development/starter shop catalog. It is NOT
// production-complete: real shop catalogs populate from rAthena NPC shop blocks
// (item_db-priced sell/buy lists) once the data loader lands (M10 futurework).
// The dev shop lets the buy/sell path transact end-to-end against a known NPC.
// Prices are plausible rAthena starter values, not authoritative.
func newDevCatalog() *domain.CatalogRegistry {
	return domain.NewCatalogRegistry(domain.Shop{
		Name: devShopName,
		Items: []domain.ShopItem{
			{NameID: 501, Price: 50, SellPrice: 25},  // Red Potion (rAthena item_db id)
			{NameID: 1201, Price: 25, SellPrice: 12}, // Knife
		},
	})
}
