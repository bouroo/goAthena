package domain

import "context"

//go:generate go run go.uber.org/mock/mockgen -destination=mock/shop_service_mock.go -package=domainmock . ShopService

// BuyResult is the outcome of a BuyFromShop use-case. Transport handlers
// collapse the non-OK values onto the ZC_PC_PURCHASE_RESULT result byte
// (0 = success, 1 = fail).
type BuyResult uint8

const (
	// BuyOK means the purchase committed: zeny deducted, items added.
	BuyOK BuyResult = iota
	// BuyFailInsufficientZeny means total cost exceeded the char's zeny.
	BuyFailInsufficientZeny
	// BuyFailLockBusy means another economy op for this character was
	// already in flight (Valkey lock held).
	BuyFailLockBusy
)

// SellResult is the outcome of a SellToShop use-case. Transport handlers
// collapse the non-OK values onto the ZC_PC_SELL_RESULT result byte.
type SellResult uint8

const (
	// SellOK means the sale committed: items removed, zeny added.
	SellOK SellResult = iota
	// SellFailZenyFull means the credit would push zeny past MaxZeny.
	SellFailZenyFull
	// SellFailInvalidItem means a sale line targeted an item the character
	// does not own or has in insufficient quantity.
	SellFailInvalidItem
	// SellFailLockBusy means another economy op for this character was
	// already in flight.
	SellFailLockBusy
)

// ShopService is the inbound port for NPC-shop economy use-cases. Each
// method acquires the per-character economy lock (D-203), performs the
// atomic zeny/inventory transaction, and releases the lock. A non-OK
// result is an expected business outcome (err == nil); an error means the
// operation could not complete (DB / lock failure) and should surface as
// an internal error, not a shop result byte.
//
// Prices in the orders/sales are gateway-validated (D-204): the gateway
// computes them from its own shop catalog and never trusts client input.
type ShopService interface {
	// BuyFromShop deducts the total cost of orders from charID's zeny and
	// adds the items to inventory, atomically. Returns the new zeny
	// balance on BuyOK.
	BuyFromShop(ctx context.Context, charID uint32, orders []ShopOrder) (newZeny uint32, result BuyResult, err error)

	// SellToShop credits charID's zeny for sales (items removed from
	// inventory) atomically. Returns the new zeny balance on SellOK.
	SellToShop(ctx context.Context, charID uint32, sales []SellLine) (newZeny uint32, result SellResult, err error)
}
