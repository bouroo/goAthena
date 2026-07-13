package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	economydomain "github.com/bouroo/goAthena/internal/features/economy/domain"
	inventorydomain "github.com/bouroo/goAthena/internal/features/inventory/domain"
	"github.com/bouroo/goAthena/internal/features/vending/domain"
)

// DefaultLockTTL bounds how long a character's vending mutex may be held.
// Vending ops are short interactions (open/close/buy), so a few seconds is ample.
const DefaultLockTTL = 5 * time.Second

// releaseTimeout bounds the detached Release call so a hung lock server
// can't wedge the deferred cleanup path indefinitely.
const releaseTimeout = 2 * time.Second

// MaxItemsPerShop is the rAthena default vending slot limit (MAX_VENDING_ITEM2).
const MaxItemsPerShop = 12

// MaxShopTitleLength bounds the player-visible shop title (MAX_SHOP_NAME).
const MaxShopTitleLength = 80

// MaxItemPrice bounds the per-item price (ZENY_MAX in pre-renewal is 2,000,000,000).
const MaxItemPrice uint32 = 2_000_000_000

type vendingService struct {
	repo     domain.VendingRepository
	locks    domain.LockStore
	invRepo  inventorydomain.InventoryRepository
	zenyRepo economydomain.CharacterZenyRepository
	lockTTL  time.Duration
}

// NewVendingService wires the vending use-case. repo persists shops; locks
// serializes per-character ops. invRepo and zenyRepo validate ownership and
// process atomic transfers; nil disables validation (dev/test fallback).
// lockTTL <= 0 falls back to DefaultLockTTL.
func NewVendingService(
	repo domain.VendingRepository,
	locks domain.LockStore,
	invRepo inventorydomain.InventoryRepository,
	zenyRepo economydomain.CharacterZenyRepository,
	lockTTL time.Duration,
) domain.VendingService {
	if lockTTL <= 0 {
		lockTTL = DefaultLockTTL
	}
	return &vendingService{
		repo:     repo,
		locks:    locks,
		invRepo:  invRepo,
		zenyRepo: zenyRepo,
		lockTTL:  lockTTL,
	}
}

// acquireResult communicates the lock outcome without an error, so callers
// can distinguish "busy" from "system error" cleanly.
type acquireResult int

const (
	acquireOK acquireResult = iota
	acquireLockBusy
)

// acquire takes the character's vending lock. Returns a token, a result
// indicating success or busy, and an error for system failures only.
func (s *vendingService) acquire(ctx context.Context, charID uint32) (string, acquireResult, error) {
	token, err := s.locks.Acquire(ctx, domain.CharLockKey(charID), s.lockTTL)
	if err != nil {
		if errors.Is(err, domain.ErrLockBusy) {
			return "", acquireLockBusy, nil
		}
		return "", 0, fmt.Errorf("acquire lock (char %d): %w", charID, err)
	}
	return token, acquireOK, nil
}

// release frees the character's vending lock in a detached context so a
// cancelled parent context doesn't skip cleanup.
func (s *vendingService) release(_ context.Context, charID uint32, token string) {
	if token == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), releaseTimeout)
	defer cancel()
	if err := s.locks.Release(ctx, domain.CharLockKey(charID), token); err != nil {
		// Lock release failures are non-fatal; the TTL will reclaim.
		_ = err
	}
}

// validateShopInput validates the shop fields before opening.
func validateShopInput(shop domain.VendingShop) error {
	if shop.OwnerID == 0 {
		return fmt.Errorf("open shop: owner_id must be > 0")
	}
	if shop.Title == "" {
		return fmt.Errorf("open shop: title is required")
	}
	if len(shop.Title) > MaxShopTitleLength {
		return fmt.Errorf("open shop: title exceeds %d chars", MaxShopTitleLength)
	}
	if len(shop.Items) == 0 {
		return fmt.Errorf("open shop: at least one item is required")
	}
	if len(shop.Items) > MaxItemsPerShop {
		return fmt.Errorf("open shop: exceeds %d items", MaxItemsPerShop)
	}
	return nil
}

// validateShopItems validates each item's amount and price.
func validateShopItems(items []domain.VendingItem) error {
	for i, item := range items {
		if item.Amount <= 0 {
			return fmt.Errorf("open shop: item %d has non-positive amount", i)
		}
		if item.Price == 0 {
			return fmt.Errorf("open shop: item %d has zero price", i)
		}
		if item.Price > MaxItemPrice {
			return fmt.Errorf("open shop: item %d price exceeds max", i)
		}
	}
	return nil
}

// validateItemOwnership checks that the shop owner has sufficient items.
func (s *vendingService) validateItemOwnership(ctx context.Context, shop domain.VendingShop) error {
	if s.invRepo == nil {
		return nil
	}
	invItems, err := s.invRepo.ListByChar(ctx, shop.OwnerID)
	if err != nil {
		return fmt.Errorf("open shop: list inventory (char %d): %w", shop.OwnerID, err)
	}
	invMap := make(map[uint32]uint32, len(invItems)) // inventoryID → amount
	for _, inv := range invItems {
		invMap[inv.ID] = inv.Amount
	}
	for i, item := range shop.Items {
		have, ok := invMap[item.InventoryID]
		// item.Amount is validated > 0 in validateShopItems before reaching here.
		if !ok || have < uint32(item.Amount) { //nolint:gosec // G115: validated positive above
			return fmt.Errorf("open shop: item %d not owned or insufficient", i)
		}
	}
	return nil
}

// OpenShop creates a vending shop for the owner at the given location.
// The owner must not already have an open shop, and the number of items
// must not exceed MaxItemsPerShop.
func (s *vendingService) OpenShop(ctx context.Context, shop domain.VendingShop) (domain.VendingShop, error) {
	if err := validateShopInput(shop); err != nil {
		return domain.VendingShop{}, err
	}
	if err := validateShopItems(shop.Items); err != nil {
		return domain.VendingShop{}, err
	}

	token, res, err := s.acquire(ctx, shop.OwnerID)
	if err != nil {
		return domain.VendingShop{}, err
	}
	if res == acquireLockBusy {
		return domain.VendingShop{}, domain.ErrLockBusy
	}
	defer s.release(ctx, shop.OwnerID, token)

	// Check for existing shop
	if existing, err := s.repo.GetShopByOwner(ctx, shop.OwnerID); err == nil && existing.ID != "" {
		return domain.VendingShop{}, domain.ErrShopAlreadyOpen
	}

	if err := s.validateItemOwnership(ctx, shop); err != nil {
		return domain.VendingShop{}, err
	}

	shop.ID = uuid.New().String()
	now := time.Now()
	shop.CreatedAt = now
	shop.UpdatedAt = now

	createdID, err := s.repo.CreateShop(ctx, shop)
	if err != nil {
		return domain.VendingShop{}, fmt.Errorf("open shop (char %d): %w", shop.OwnerID, err)
	}
	shop.ID = createdID

	return shop, nil
}

// CloseShop closes the owner's vending shop.
func (s *vendingService) CloseShop(ctx context.Context, ownerID uint32) error {
	if ownerID == 0 {
		return fmt.Errorf("close shop: owner_id must be > 0")
	}

	token, res, err := s.acquire(ctx, ownerID)
	if err != nil {
		return err
	}
	if res == acquireLockBusy {
		return domain.ErrLockBusy
	}
	defer s.release(ctx, ownerID, token)

	shop, err := s.repo.GetShopByOwner(ctx, ownerID)
	if err != nil {
		if errors.Is(err, domain.ErrShopNotFound) {
			return domain.ErrShopClosed
		}
		return fmt.Errorf("close shop (char %d): %w", ownerID, err)
	}

	if err := s.repo.DeleteShop(ctx, shop.ID); err != nil {
		return fmt.Errorf("close shop delete (char %d): %w", ownerID, err)
	}

	return nil
}

// findShopItem locates an item by inventoryID in the shop's listing.
// Returns the index and true if found, -1 and false otherwise.
func findShopItem(shop domain.VendingShop, inventoryID uint32) (int, bool) {
	for i, item := range shop.Items {
		if item.InventoryID == inventoryID {
			return i, true
		}
	}
	return -1, false
}

// processPurchaseWithZeny handles a purchase when the zeny repo is available.
// It validates funds, executes the transaction, and credits the owner.
func (s *vendingService) processPurchaseWithZeny(
	ctx context.Context,
	buyerID uint32,
	shop domain.VendingShop,
	idx int,
	amount int32,
	totalCost uint32,
) (uint32, error) {
	buyerZeny, err := s.zenyRepo.GetZeny(ctx, buyerID)
	if err != nil {
		return 0, fmt.Errorf("buy item: get buyer zeny (char %d): %w", buyerID, err)
	}
	if buyerZeny < totalCost {
		return 0, domain.ErrInsufficientFunds
	}

	shopItem := shop.Items[idx]
	// amount is validated > 0 at the start of BuyItem.
	acquiredItems := []economydomain.AcquiredItem{
		{ItemID: shopItem.ItemID, Amount: uint32(amount)}, //nolint:gosec // G115: validated positive
	}
	newZeny, err := s.zenyRepo.ExecuteBuyTx(ctx, buyerID, totalCost, acquiredItems)
	if err != nil {
		return 0, fmt.Errorf("buy item: execute buy tx (char %d): %w", buyerID, err)
	}

	if err := s.reduceOwnerInventory(ctx, shopItem, amount); err != nil {
		return newZeny, err
	}

	if err := s.creditOwner(ctx, shop, shopItem, amount); err != nil {
		return newZeny, err
	}

	s.updateShopStock(ctx, shop, idx, amount)
	return newZeny, nil
}

// reduceOwnerInventory decrements the sold items from the owner's inventory.
func (s *vendingService) reduceOwnerInventory(ctx context.Context, shopItem domain.VendingItem, amount int32) error {
	if s.invRepo == nil {
		return nil
	}
	// shopItem.Amount and amount are both validated positive; result is non-negative.
	newAmount := uint32(shopItem.Amount - amount) //nolint:gosec // G115: validated positive, result non-negative
	if err := s.invRepo.UpdateAmount(ctx, shopItem.InventoryID, newAmount); err != nil {
		return fmt.Errorf("buy item: reduce owner inventory: %w", err)
	}
	return nil
}

// creditOwner credits the shop owner with zeny from the sale.
func (s *vendingService) creditOwner(ctx context.Context, shop domain.VendingShop, shopItem domain.VendingItem, amount int32) error {
	// amount is validated positive in BuyItem.
	amt := uint32(amount) //nolint:gosec // G115: validated positive
	ownerCreditItems := []economydomain.SellLine{
		{InvID: shopItem.InventoryID, Amount: amt, UnitPrice: shopItem.Price},
	}
	if _, err := s.zenyRepo.ExecuteSellTx(ctx, shop.OwnerID, shopItem.Price*amt, ownerCreditItems); err != nil {
		return fmt.Errorf("buy item: credit owner zeny (char %d): %w", shop.OwnerID, err)
	}
	return nil
}

// updateShopStock reduces the shop item stock and removes the listing or
// auto-closes the shop when all items are sold out.
func (s *vendingService) updateShopStock(ctx context.Context, shop domain.VendingShop, idx int, amount int32) {
	shop.Items[idx].Amount -= amount
	shop.UpdatedAt = time.Now()
	if shop.Items[idx].Amount == 0 {
		shop.Items = append(shop.Items[:idx], shop.Items[idx+1:]...)
	}
	if len(shop.Items) == 0 {
		_ = s.repo.DeleteShop(ctx, shop.ID)
		return
	}
	_ = s.repo.UpdateShop(ctx, shop)
}

// BuyItem processes a purchase from a vending shop. The buyer's zeny is
// deducted, items are transferred, and the shop's stock is reduced.
func (s *vendingService) BuyItem(ctx context.Context, buyerID uint32, shopID string, inventoryID uint32, amount int32) (uint32, error) {
	if buyerID == 0 {
		return 0, fmt.Errorf("buy item: buyer_id must be > 0")
	}
	if shopID == "" {
		return 0, fmt.Errorf("buy item: shop_id is required")
	}
	if amount <= 0 {
		return 0, fmt.Errorf("buy item: amount must be positive")
	}

	token, res, err := s.acquire(ctx, buyerID)
	if err != nil {
		return 0, err
	}
	if res == acquireLockBusy {
		return 0, domain.ErrLockBusy
	}
	defer s.release(ctx, buyerID, token)

	shop, err := s.repo.GetShop(ctx, shopID)
	if err != nil {
		return 0, fmt.Errorf("buy item: get shop %q: %w", shopID, err)
	}

	idx, found := findShopItem(shop, inventoryID)
	if !found {
		return 0, domain.ErrInvalidItem
	}

	shopItem := shop.Items[idx]
	if shopItem.Amount < amount {
		return 0, domain.ErrInsufficientItems
	}

	totalCost := shopItem.Price * uint32(amount)
	if totalCost < shopItem.Price {
		return 0, fmt.Errorf("buy item: total cost overflow")
	}

	if s.zenyRepo != nil {
		return s.processPurchaseWithZeny(ctx, buyerID, shop, idx, amount, totalCost)
	}

	// No zeny repo: process without real zeny validation (dev/test mode)
	s.updateShopStock(ctx, shop, idx, amount)
	return 0, nil
}

// ListShopItems returns the items currently listed in a shop.
func (s *vendingService) ListShopItems(ctx context.Context, shopID string) ([]domain.VendingItem, error) {
	shop, err := s.repo.GetShop(ctx, shopID)
	if err != nil {
		return nil, fmt.Errorf("list shop items: %w", err)
	}
	return shop.Items, nil
}

// ListShopsOnMap returns all open vending shops on the given map.
func (s *vendingService) ListShopsOnMap(ctx context.Context, mapName string) ([]domain.VendingShop, error) {
	shops, err := s.repo.ListShopsOnMap(ctx, mapName)
	if err != nil {
		return nil, fmt.Errorf("list shops on map %q: %w", mapName, err)
	}
	return shops, nil
}

// GetShop returns the shop owned by the given character, if any.
func (s *vendingService) GetShop(ctx context.Context, ownerID uint32) (domain.VendingShop, error) {
	shop, err := s.repo.GetShopByOwner(ctx, ownerID)
	if err != nil {
		return domain.VendingShop{}, fmt.Errorf("get shop (char %d): %w", ownerID, err)
	}
	return shop, nil
}
