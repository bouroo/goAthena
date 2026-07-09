package domain

// MaxZeny is rAthena's pre-renewal limit for character currency.
const MaxZeny uint32 = 2000000000

// ShopOrder defines a single line item in a shop buy transaction.
// UnitPrice must be validated by the gateway/service layer and
// never trusted from client input.
type ShopOrder struct {
	ItemID    uint32
	Amount    uint32
	UnitPrice uint32
}

// AcquiredItem defines an item added to inventory after a transaction.
type AcquiredItem struct {
	ItemID uint32
	Amount uint32
}

// SellLine defines a single item line to be sold from inventory.
type SellLine struct {
	InvID     uint32
	Amount    uint32
	UnitPrice uint32
}
