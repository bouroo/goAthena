// Package service implements the economy use-cases (ShopService): NPC-shop
// buy and sell, each guarded by the per-character distributed lock and
// backed by an atomic zeny/inventory transaction.
package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bouroo/goAthena/internal/features/economy/domain"
	inventorydomain "github.com/bouroo/goAthena/internal/features/inventory/domain"
)

// DefaultLockTTL bounds how long a character's economy mutex may be held.
// Economy ops are a single DB transaction, so a few seconds is ample and
// keeps a crashed holder from blocking the character for long.
const DefaultLockTTL = 5 * time.Second

type shopService struct {
	repo    domain.CharacterZenyRepository
	locks   domain.LockStore
	lockTTL time.Duration
}

// NewShopService wires the economy use-case. repo performs the atomic
// zeny/inventory transaction; locks serializes per-character ops. lockTTL
// <= 0 falls back to DefaultLockTTL.
func NewShopService(repo domain.CharacterZenyRepository, locks domain.LockStore, lockTTL time.Duration) domain.ShopService {
	if lockTTL <= 0 {
		lockTTL = DefaultLockTTL
	}
	return &shopService{repo: repo, locks: locks, lockTTL: lockTTL}
}

// BuyFromShop acquires the character economy lock, then runs the atomic
// buy transaction (zeny deduct + inventory add). Insufficient zeny and a
// busy lock are business outcomes (mapped result codes, nil err); any
// other failure is an error.
func (s *shopService) BuyFromShop(ctx context.Context, charID uint32, orders []domain.ShopOrder) (uint32, domain.BuyResult, error) {
	token, res, err := s.acquire(ctx, charID)
	if err != nil {
		return 0, 0, err
	}
	if res == buyLockBusy {
		return 0, domain.BuyFailLockBusy, nil
	}
	defer s.release(ctx, charID, token)

	var totalCost uint64
	acquired := make([]domain.AcquiredItem, 0, len(orders))
	for _, o := range orders {
		if o.Amount == 0 {
			continue
		}
		totalCost += uint64(o.Amount) * uint64(o.UnitPrice)
		acquired = append(acquired, domain.AcquiredItem{ItemID: o.ItemID, Amount: o.Amount})
	}
	if len(acquired) == 0 {
		// Nothing to buy is a no-op success; keep the client flow moving.
		zeny, err := s.repo.GetZeny(ctx, charID)
		if err != nil {
			return 0, 0, fmt.Errorf("economy buy get zeny (char %d): %w", charID, err)
		}
		return zeny, domain.BuyOK, nil
	}
	// Guard against uint32 truncation: totalCost is uint64 but the repo
	// narrows to uint32, so any value > MaxZeny would silently wrap and
	// undercharge. A buy costing more than the entire zeny cap is
	// unaffordable by definition (no character can hold that much), so
	// reject it as insufficient zeny before the repo call.
	if totalCost > uint64(domain.MaxZeny) {
		return 0, domain.BuyFailInsufficientZeny, nil
	}

	newZeny, err := s.repo.ExecuteBuyTx(ctx, charID, uint32(totalCost), acquired)
	switch {
	case err == nil:
		return newZeny, domain.BuyOK, nil
	case errors.Is(err, domain.ErrInsufficientZeny):
		return 0, domain.BuyFailInsufficientZeny, nil
	default:
		return 0, 0, fmt.Errorf("economy buy (char %d): %w", charID, err)
	}
}

// SellToShop acquires the character economy lock, then runs the atomic
// sell transaction (inventory remove + zeny add). Zeny-overflow and an
// invalid sale line are business outcomes; a busy lock is too.
func (s *shopService) SellToShop(ctx context.Context, charID uint32, sales []domain.SellLine) (uint32, domain.SellResult, error) {
	token, res, err := s.acquire(ctx, charID)
	if err != nil {
		return 0, 0, err
	}
	if res == buyLockBusy {
		return 0, domain.SellFailLockBusy, nil
	}
	defer s.release(ctx, charID, token)

	var totalCredit uint64
	validated := make([]domain.SellLine, 0, len(sales))
	for _, sl := range sales {
		if sl.Amount == 0 {
			continue
		}
		totalCredit += uint64(sl.Amount) * uint64(sl.UnitPrice)
		validated = append(validated, sl)
	}
	if len(validated) == 0 {
		zeny, err := s.repo.GetZeny(ctx, charID)
		if err != nil {
			return 0, 0, fmt.Errorf("economy sell get zeny (char %d): %w", charID, err)
		}
		return zeny, domain.SellOK, nil
	}
	// Guard against uint32 truncation: totalCredit is uint64 but the repo
	// narrows to uint32, so any value > MaxZeny would silently wrap and
	// over-credit. A sale crediting more than the zeny cap cannot be
	// represented in the column, so reject it as zeny-full before the
	// repo call.
	if totalCredit > uint64(domain.MaxZeny) {
		return 0, domain.SellFailZenyFull, nil
	}

	newZeny, err := s.repo.ExecuteSellTx(ctx, charID, uint32(totalCredit), validated)
	switch {
	case err == nil:
		return newZeny, domain.SellOK, nil
	case errors.Is(err, domain.ErrZenyOverflow):
		return 0, domain.SellFailZenyFull, nil
	case errors.Is(err, inventorydomain.ErrItemNotFound):
		// Unknown/short inventory row — the player tried to sell an item
		// they don't own. A clean fail result, not an internal error.
		return 0, domain.SellFailInvalidItem, nil
	default:
		return 0, 0, fmt.Errorf("economy sell (char %d): %w", charID, err)
	}
}

// acquireResult discriminates the acquire outcome without overloading error
// semantics: a busy lock is expected, not an error.
type acquireResult uint8

const (
	acquireOK acquireResult = iota
	buyLockBusy
)

// acquire wraps LockStore.Acquire, mapping ErrLockBusy to a non-error
// result so callers can return a shop result code instead of erroring.
func (s *shopService) acquire(ctx context.Context, charID uint32) (string, acquireResult, error) {
	token, err := s.locks.Acquire(ctx, domain.CharLockKey(charID), s.lockTTL)
	switch {
	case err == nil:
		return token, acquireOK, nil
	case errors.Is(err, domain.ErrLockBusy):
		return "", buyLockBusy, nil
	default:
		return "", 0, fmt.Errorf("economy lock acquire (char %d): %w", charID, err)
	}
}

// releaseTimeout bounds the detached Release call so a hung lock server
// can't wedge the deferred cleanup path indefinitely.
const releaseTimeout = 2 * time.Second

// release best-effort releases the lock. A release failure is logged via
// the error return but must not override the transaction outcome, so
// callers invoke it via defer and discard the value.
//
// The call uses context.WithoutCancel on the request ctx: if the request
// was cancelled (client disconnect, deadline exceeded) before the deferred
// release ran, passing the raw ctx would cause Release to fail immediately
// and the lock would leak until its TTL expired. We detach from parent
// cancellation and apply a short timeout so the cleanup still completes.
func (s *shopService) release(ctx context.Context, charID uint32, token string) {
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
	defer cancel()
	_ = s.locks.Release(releaseCtx, domain.CharLockKey(charID), token)
}
