package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bouroo/goAthena/internal/features/economy/domain"
	inventorydomain "github.com/bouroo/goAthena/internal/features/inventory/domain"
	tradedomain "github.com/bouroo/goAthena/internal/features/trade/domain"
	"github.com/google/uuid"
)

// DefaultLockTTL bounds how long a character's trade mutex may be held.
// Trade ops are a single DB transaction, so a few seconds is ample and
// keeps a crashed holder from blocking the character for long.
const DefaultLockTTL = 5 * time.Second

// releaseTimeout bounds the detached Release call so a hung lock server
// can't wedge the deferred cleanup path indefinitely.
const releaseTimeout = 2 * time.Second

type tradeService struct {
	repo     tradedomain.TradeRepository
	locks    tradedomain.LockStore
	invRepo  inventorydomain.InventoryRepository
	zenyRepo domain.CharacterZenyRepository
	lockTTL  time.Duration
}

// NewTradeService wires the trade use-case. repo performs the atomic
// trade transfer; locks serializes per-character ops. lockTTL <= 0 falls
// back to DefaultLockTTL.
func NewTradeService(repo tradedomain.TradeRepository, locks tradedomain.LockStore, invRepo inventorydomain.InventoryRepository, zenyRepo domain.CharacterZenyRepository, lockTTL time.Duration) tradedomain.TradeService {
	if lockTTL <= 0 {
		lockTTL = DefaultLockTTL
	}
	return &tradeService{
		repo:     repo,
		locks:    locks,
		invRepo:  invRepo,
		zenyRepo: zenyRepo,
		lockTTL:  lockTTL,
	}
}

// RequestTrade initiates a trade session between charID and targetCharID.
// The target must accept before the trade moves to TradeStateOpen.
// Returns the created trade session.
func (s *tradeService) RequestTrade(ctx context.Context, charID uint32, targetCharID uint32) (tradedomain.Trade, error) {
	token, res, err := s.acquire(ctx, charID)
	if err != nil {
		return tradedomain.Trade{}, err
	}
	if res == acquireLockBusy {
		return tradedomain.Trade{}, tradedomain.ErrLockBusy
	}
	defer s.release(ctx, charID, token)

	tradeID := uuid.New().String()
	now := time.Now()

	trade := tradedomain.Trade{
		ID:            tradeID,
		Player1CharID: charID,
		Player2CharID: 0, // 0 until accepted
		State:         tradedomain.TradeStateRequested,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	if _, err := s.repo.CreateTrade(ctx, trade); err != nil {
		return tradedomain.Trade{}, fmt.Errorf("create trade (char %d -> %d): %w", charID, targetCharID, err)
	}

	return trade, nil
}

// AcceptTrade accepts a trade request and transitions to TradeStateOpen.
// Updates Player2CharID and allows both parties to add items/zeny.
func (s *tradeService) AcceptTrade(ctx context.Context, tradeID string, charID uint32) error {
	token, res, err := s.acquire(ctx, charID)
	if err != nil {
		return err
	}
	if res == acquireLockBusy {
		return tradedomain.ErrLockBusy
	}
	defer s.release(ctx, charID, token)

	trade, err := s.repo.GetTrade(ctx, tradeID)
	if err != nil {
		return fmt.Errorf("get trade (id %s): %w", tradeID, err)
	}

	if trade.State != tradedomain.TradeStateRequested {
		return fmt.Errorf("%w: trade is in state %d, expected %d", tradedomain.ErrInvalidTradeState, trade.State, tradedomain.TradeStateRequested)
	}

	trade.Player2CharID = charID
	trade.State = tradedomain.TradeStateOpen
	trade.UpdatedAt = time.Now()

	if err := s.repo.UpdateTrade(ctx, trade); err != nil {
		return fmt.Errorf("update trade accept (id %s): %w", tradeID, err)
	}

	return nil
}

// AddTradeItem adds an item to the trade window for the offering character.
// Fails if the trade is not in TradeStateOpen or the item is not owned.
func (s *tradeService) AddTradeItem(ctx context.Context, tradeID string, charID uint32, itemID uint32, amount int32) error {
	if amount <= 0 {
		return fmt.Errorf("item amount must be positive: %d", amount)
	}

	token, res, err := s.acquire(ctx, charID)
	if err != nil {
		return err
	}
	if res == acquireLockBusy {
		return tradedomain.ErrLockBusy
	}
	defer s.release(ctx, charID, token)

	trade, err := s.repo.GetTrade(ctx, tradeID)
	if err != nil {
		return fmt.Errorf("get trade (id %s): %w", tradeID, err)
	}

	if trade.State != tradedomain.TradeStateOpen {
		return fmt.Errorf("%w: trade is in state %d, expected %d", tradedomain.ErrInvalidTradeState, trade.State, tradedomain.TradeStateOpen)
	}

	if err := s.validateItemOwnership(ctx, charID, itemID, amount); err != nil {
		return err
	}

	tradeItem := tradedomain.TradeItem{
		ItemID: itemID,
		Amount: amount,
	}

	switch {
	case trade.Player1CharID == charID:
		trade.Player1Items = append(trade.Player1Items, tradeItem)
	case trade.Player2CharID == charID:
		trade.Player2Items = append(trade.Player2Items, tradeItem)
	default:
		return fmt.Errorf("character %d is not a participant in trade %s", charID, tradeID)
	}

	trade.UpdatedAt = time.Now()

	if err := s.repo.UpdateTrade(ctx, trade); err != nil {
		return fmt.Errorf("update trade item (id %s): %w", tradeID, err)
	}

	return nil
}

// AddTradeZeny adds zeny to the trade window for the offering character.
// Fails if the trade is not in TradeStateOpen or zeny is insufficient.
func (s *tradeService) AddTradeZeny(ctx context.Context, tradeID string, charID uint32, zeny uint32) error {
	if zeny == 0 {
		return nil
	}

	token, res, err := s.acquire(ctx, charID)
	if err != nil {
		return err
	}
	if res == acquireLockBusy {
		return tradedomain.ErrLockBusy
	}
	defer s.release(ctx, charID, token)

	trade, err := s.repo.GetTrade(ctx, tradeID)
	if err != nil {
		return fmt.Errorf("get trade (id %s): %w", tradeID, err)
	}

	if trade.State != tradedomain.TradeStateOpen {
		return fmt.Errorf("%w: trade is in state %d, expected %d", tradedomain.ErrInvalidTradeState, trade.State, tradedomain.TradeStateOpen)
	}

	if s.zenyRepo == nil {
		return s.addZenyWithoutValidation(ctx, &trade, charID, zeny, tradeID)
	}

	return s.addZenyWithValidation(ctx, &trade, charID, zeny, tradeID)
}

func (s *tradeService) addZenyWithoutValidation(ctx context.Context, trade *tradedomain.Trade, charID uint32, zeny uint32, tradeID string) error {
	switch {
	case trade.Player1CharID == charID:
		trade.Player1Zeny += zeny
	case trade.Player2CharID == charID:
		trade.Player2Zeny += zeny
	default:
		return fmt.Errorf("character %d is not a participant in trade %s", charID, tradeID)
	}
	trade.UpdatedAt = time.Now()
	if err := s.repo.UpdateTrade(ctx, *trade); err != nil {
		return fmt.Errorf("update trade zeny (id %s): %w", tradeID, err)
	}
	return nil
}

func (s *tradeService) addZenyWithValidation(ctx context.Context, trade *tradedomain.Trade, charID uint32, zeny uint32, tradeID string) error {
	currentZeny, err := s.zenyRepo.GetZeny(ctx, charID)
	if err != nil {
		return fmt.Errorf("get zeny (char %d): %w", charID, err)
	}

	switch {
	case trade.Player1CharID == charID:
		if trade.Player1Zeny+zeny > currentZeny {
			return tradedomain.ErrInsufficientInventory
		}
		trade.Player1Zeny += zeny
	case trade.Player2CharID == charID:
		if trade.Player2Zeny+zeny > currentZeny {
			return tradedomain.ErrInsufficientInventory
		}
		trade.Player2Zeny += zeny
	default:
		return fmt.Errorf("character %d is not a participant in trade %s", charID, tradeID)
	}

	trade.UpdatedAt = time.Now()

	if err := s.repo.UpdateTrade(ctx, *trade); err != nil {
		return fmt.Errorf("update trade zeny (id %s): %w", tradeID, err)
	}

	return nil
}

// ConfirmTrade locks the character's offer, preventing further modifications.
// Once both parties confirm, the trade moves to TradeStateLocked.
func (s *tradeService) ConfirmTrade(ctx context.Context, tradeID string, charID uint32) error {
	token, res, err := s.acquire(ctx, charID)
	if err != nil {
		return err
	}
	if res == acquireLockBusy {
		return tradedomain.ErrLockBusy
	}
	defer s.release(ctx, charID, token)

	trade, err := s.repo.GetTrade(ctx, tradeID)
	if err != nil {
		return fmt.Errorf("get trade (id %s): %w", tradeID, err)
	}

	if trade.State != tradedomain.TradeStateOpen && trade.State != tradedomain.TradeStateConfirmed {
		return fmt.Errorf("%w: trade is in state %d, expected open or confirmed", tradedomain.ErrInvalidTradeState, trade.State)
	}

	switch {
	case trade.Player1CharID == charID:
		trade.Player1Confirmed = true
	case trade.Player2CharID == charID:
		trade.Player2Confirmed = true
	default:
		return fmt.Errorf("character %d is not a participant in trade %s", charID, tradeID)
	}

	if trade.Player1Confirmed && trade.Player2Confirmed {
		trade.State = tradedomain.TradeStateLocked
	} else {
		trade.State = tradedomain.TradeStateConfirmed
	}

	trade.UpdatedAt = time.Now()

	if err := s.repo.UpdateTrade(ctx, trade); err != nil {
		return fmt.Errorf("update trade confirm (id %s): %w", tradeID, err)
	}

	return nil
}

// CompleteTrade executes the atomic transfer when both parties have confirmed.
// Moves the trade to TradeStateCompleted on success.
func (s *tradeService) CompleteTrade(ctx context.Context, tradeID string, charID uint32) error {
	trade, err := s.repo.GetTrade(ctx, tradeID)
	if err != nil {
		return fmt.Errorf("get trade (id %s): %w", tradeID, err)
	}

	if trade.State != tradedomain.TradeStateLocked {
		return fmt.Errorf("%w: trade is in state %d, expected locked", tradedomain.ErrInvalidTradeState, trade.State)
	}

	if trade.Player1CharID != charID && trade.Player2CharID != charID {
		return fmt.Errorf("character %d is not a participant in trade %s", charID, tradeID)
	}

	token1, res1, err1 := s.acquire(ctx, trade.Player1CharID)
	if err1 != nil {
		return err1
	}
	if res1 == acquireLockBusy {
		return tradedomain.ErrLockBusy
	}
	defer s.release(ctx, trade.Player1CharID, token1)

	token2, res2, err2 := s.acquire(ctx, trade.Player2CharID)
	if err2 != nil {
		return err2
	}
	if res2 == acquireLockBusy {
		return tradedomain.ErrLockBusy
	}
	defer s.release(ctx, trade.Player2CharID, token2)

	if err := s.repo.ExecuteTradeTransfer(ctx, trade); err != nil {
		return fmt.Errorf("execute trade transfer (id %s): %w", tradeID, err)
	}

	trade.State = tradedomain.TradeStateCompleted
	trade.UpdatedAt = time.Now()

	if err := s.repo.UpdateTrade(ctx, trade); err != nil {
		return fmt.Errorf("update trade complete (id %s): %w", tradeID, err)
	}

	return nil
}

// CancelTrade aborts the trade session from any state.
// Moves the trade to TradeStateCancelled.
func (s *tradeService) CancelTrade(ctx context.Context, tradeID string, charID uint32) error {
	token, res, err := s.acquire(ctx, charID)
	if err != nil {
		return err
	}
	if res == acquireLockBusy {
		return tradedomain.ErrLockBusy
	}
	defer s.release(ctx, charID, token)

	trade, err := s.repo.GetTrade(ctx, tradeID)
	if err != nil {
		return fmt.Errorf("get trade (id %s): %w", tradeID, err)
	}

	if trade.Player1CharID != charID && trade.Player2CharID != charID {
		return fmt.Errorf("character %d is not a participant in trade %s", charID, tradeID)
	}

	trade.State = tradedomain.TradeStateCancelled
	trade.UpdatedAt = time.Now()

	if err := s.repo.UpdateTrade(ctx, trade); err != nil {
		return fmt.Errorf("update trade cancel (id %s): %w", tradeID, err)
	}

	return nil
}

// validateItemOwnership checks that the character owns the item and has sufficient quantity.
func (s *tradeService) validateItemOwnership(ctx context.Context, charID uint32, itemID uint32, amount int32) error {
	if s.invRepo == nil {
		return nil
	}

	items, err := s.invRepo.ListByChar(ctx, charID)
	if err != nil {
		return fmt.Errorf("list inventory (char %d): %w", charID, err)
	}

	var totalAmount int64
	for _, item := range items {
		if item.NameID == itemID {
			totalAmount += int64(item.Amount)
		}
	}

	if totalAmount < int64(amount) {
		return tradedomain.ErrInsufficientInventory
	}

	return nil
}

// acquireResult discriminates the acquire outcome without overloading error
// semantics: a busy lock is expected, not an error.
type acquireResult uint8

const (
	acquireOK acquireResult = iota
	acquireLockBusy
)

// acquire wraps LockStore.Acquire, mapping ErrLockBusy to a non-error
// result so callers can return a business result instead of erroring.
func (s *tradeService) acquire(ctx context.Context, charID uint32) (string, acquireResult, error) {
	token, err := s.locks.Acquire(ctx, tradedomain.CharLockKey(charID), s.lockTTL)
	switch {
	case err == nil:
		return token, acquireOK, nil
	case errors.Is(err, tradedomain.ErrLockBusy):
		return "", acquireLockBusy, nil
	default:
		return "", 0, fmt.Errorf("trade lock acquire (char %d): %w", charID, err)
	}
}

// release best-effort releases the lock. A release failure is logged via
// the error return but must not override the transaction outcome, so
// callers invoke it via defer and discard the value.
//
// The call uses context.WithoutCancel on the request ctx: if the request
// was cancelled (client disconnect, deadline exceeded) before the deferred
// release ran, passing the raw ctx would cause Release to fail immediately
// and the lock would leak until its TTL expired. We detach from parent
// cancellation and apply a short timeout so the cleanup still completes.
func (s *tradeService) release(ctx context.Context, charID uint32, token string) {
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
	defer cancel()
	_ = s.locks.Release(releaseCtx, tradedomain.CharLockKey(charID), token)
}
