//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gomock "go.uber.org/mock/gomock"

	economymock "github.com/bouroo/goAthena/internal/features/economy/domain/mock"
	invdomain "github.com/bouroo/goAthena/internal/features/inventory/domain"
	invmock "github.com/bouroo/goAthena/internal/features/inventory/domain/mock"
	tradedomain "github.com/bouroo/goAthena/internal/features/trade/domain"
	trademock "github.com/bouroo/goAthena/internal/features/trade/domain/mock"
)

func setupTestService(t *testing.T) (*tradeService, *trademock.MockTradeRepository, *trademock.MockLockStore) {
	ctrl := gomock.NewController(t)
	repo := trademock.NewMockTradeRepository(ctrl)
	locks := trademock.NewMockLockStore(ctrl)
	svc := NewTradeService(repo, locks, nil, nil, 0)
	return svc.(*tradeService), repo, locks
}

func setupTestServiceWithMocks(t *testing.T) (*tradeService, *trademock.MockTradeRepository, *trademock.MockLockStore, *invmock.MockInventoryRepository, *economymock.MockCharacterZenyRepository) {
	ctrl := gomock.NewController(t)
	repo := trademock.NewMockTradeRepository(ctrl)
	locks := trademock.NewMockLockStore(ctrl)
	invRepo := invmock.NewMockInventoryRepository(ctrl)
	zenyRepo := economymock.NewMockCharacterZenyRepository(ctrl)
	svc := NewTradeService(repo, locks, invRepo, zenyRepo, 0)
	return svc.(*tradeService), repo, locks, invRepo, zenyRepo
}

func TestRequestTrade_Success(t *testing.T) {
	svc, repo, locks := setupTestService(t)
	ctx := context.Background()
	charID := uint32(1001)
	targetCharID := uint32(1002)

	token := "test-lock-token"
	lockKey := tradedomain.CharLockKey(charID)

	locks.EXPECT().Acquire(ctx, lockKey, svc.lockTTL).Return(token, nil)
	repo.EXPECT().CreateTrade(ctx, gomock.Any()).Return("trade-123", nil)
	locks.EXPECT().Release(gomock.Any(), lockKey, token).Return(nil)

	trade, err := svc.RequestTrade(ctx, charID, targetCharID)

	require.NoError(t, err)
	assert.Equal(t, tradedomain.TradeStateRequested, trade.State)
	assert.Equal(t, charID, trade.Player1CharID)
	assert.Equal(t, uint32(0), trade.Player2CharID)
	assert.NotEmpty(t, trade.ID)
}

func TestRequestTrade_LockBusy(t *testing.T) {
	svc, _, locks := setupTestService(t)
	ctx := context.Background()
	charID := uint32(1001)
	targetCharID := uint32(1002)

	lockKey := tradedomain.CharLockKey(charID)

	locks.EXPECT().Acquire(ctx, lockKey, svc.lockTTL).Return("", tradedomain.ErrLockBusy)

	trade, err := svc.RequestTrade(ctx, charID, targetCharID)

	assert.Error(t, err)
	assert.ErrorIs(t, err, tradedomain.ErrLockBusy)
	assert.Empty(t, trade.ID)
}

func TestRequestTrade_SystemError(t *testing.T) {
	svc, _, locks := setupTestService(t)
	ctx := context.Background()
	charID := uint32(1001)
	targetCharID := uint32(1002)

	lockKey := tradedomain.CharLockKey(charID)
	sysErr := assert.AnError

	locks.EXPECT().Acquire(ctx, lockKey, svc.lockTTL).Return("", sysErr)

	trade, err := svc.RequestTrade(ctx, charID, targetCharID)

	assert.Error(t, err)
	assert.ErrorIs(t, err, sysErr)
	assert.Empty(t, trade.ID)
}

func TestAcceptTrade_Success(t *testing.T) {
	svc, repo, locks := setupTestService(t)
	ctx := context.Background()
	tradeID := "trade-123"
	charID := uint32(1002)

	token := "test-lock-token"
	lockKey := tradedomain.CharLockKey(charID)

	existingTrade := tradedomain.Trade{
		ID:            tradeID,
		Player1CharID: 1001,
		Player2CharID: 0,
		State:         tradedomain.TradeStateRequested,
	}

	locks.EXPECT().Acquire(ctx, lockKey, svc.lockTTL).Return(token, nil)
	repo.EXPECT().GetTrade(ctx, tradeID).Return(existingTrade, nil)
	repo.EXPECT().UpdateTrade(ctx, gomock.Any()).Return(nil)
	locks.EXPECT().Release(gomock.Any(), lockKey, token).Return(nil)

	err := svc.AcceptTrade(ctx, tradeID, charID)

	require.NoError(t, err)
}

func TestAcceptTrade_InvalidState(t *testing.T) {
	svc, repo, locks := setupTestService(t)
	ctx := context.Background()
	tradeID := "trade-123"
	charID := uint32(1002)

	token := "test-lock-token"
	lockKey := tradedomain.CharLockKey(charID)

	existingTrade := tradedomain.Trade{
		ID:            tradeID,
		Player1CharID: 1001,
		Player2CharID: 0,
		State:         tradedomain.TradeStateOpen,
	}

	locks.EXPECT().Acquire(ctx, lockKey, svc.lockTTL).Return(token, nil)
	repo.EXPECT().GetTrade(ctx, tradeID).Return(existingTrade, nil)
	locks.EXPECT().Release(gomock.Any(), lockKey, token).Return(nil)

	err := svc.AcceptTrade(ctx, tradeID, charID)

	assert.Error(t, err)
	assert.ErrorIs(t, err, tradedomain.ErrInvalidTradeState)
}

func TestAcceptTrade_LockBusy(t *testing.T) {
	svc, _, locks := setupTestService(t)
	ctx := context.Background()
	tradeID := "trade-123"
	charID := uint32(1002)

	lockKey := tradedomain.CharLockKey(charID)

	locks.EXPECT().Acquire(ctx, lockKey, svc.lockTTL).Return("", tradedomain.ErrLockBusy)

	err := svc.AcceptTrade(ctx, tradeID, charID)

	assert.Error(t, err)
	assert.ErrorIs(t, err, tradedomain.ErrLockBusy)
}

func TestAddTradeItem_Success(t *testing.T) {
	svc, repo, locks, invRepo, _ := setupTestServiceWithMocks(t)
	ctx := context.Background()
	tradeID := "trade-123"
	charID := uint32(1001)
	peerID := uint32(1002)
	itemID := uint32(501)
	amount := int32(5)

	token := "test-lock-token"
	charLockKey := tradedomain.CharLockKey(charID)
	peerLockKey := tradedomain.CharLockKey(peerID)

	existingTrade := tradedomain.Trade{
		ID:            tradeID,
		Player1CharID: 1001,
		Player2CharID: 1002,
		State:         tradedomain.TradeStateOpen,
		Player1Items:  []tradedomain.TradeItem{},
	}

	invItems := []invdomain.InventoryItem{
		{ID: 1, CharID: charID, NameID: itemID, Amount: 10},
	}

	// First acquire single lock
	locks.EXPECT().Acquire(ctx, charLockKey, svc.lockTTL).Return(token, nil)
	// First GetTrade (outside withBothLocks)
	repo.EXPECT().GetTrade(ctx, tradeID).Return(existingTrade, nil)
	// Release first lock before acquiring both
	locks.EXPECT().Release(gomock.Any(), charLockKey, token).Return(nil)
	// Acquire both locks in order (1001 < 1002)
	locks.EXPECT().Acquire(ctx, charLockKey, svc.lockTTL).Return("token1", nil)
	locks.EXPECT().Acquire(ctx, peerLockKey, svc.lockTTL).Return("token2", nil)
	// Second GetTrade (inside withBothLocks)
	repo.EXPECT().GetTrade(ctx, tradeID).Return(existingTrade, nil)
	// Validate inventory
	invRepo.EXPECT().ListByChar(ctx, charID).Return(invItems, nil)
	// Update trade
	repo.EXPECT().UpdateTrade(ctx, gomock.Any()).Return(nil)
	// Release both locks (defer)
	locks.EXPECT().Release(gomock.Any(), charLockKey, "token1").Return(nil)
	locks.EXPECT().Release(gomock.Any(), peerLockKey, "token2").Return(nil)

	err := svc.AddTradeItem(ctx, tradeID, charID, itemID, amount)

	require.NoError(t, err)
}

func TestAddTradeItem_InsufficientInventory(t *testing.T) {
	svc, repo, locks, invRepo, _ := setupTestServiceWithMocks(t)
	ctx := context.Background()
	tradeID := "trade-123"
	charID := uint32(1001)
	peerID := uint32(1002)
	itemID := uint32(501)
	amount := int32(15)

	token := "test-lock-token"
	charLockKey := tradedomain.CharLockKey(charID)
	peerLockKey := tradedomain.CharLockKey(peerID)

	existingTrade := tradedomain.Trade{
		ID:            tradeID,
		Player1CharID: 1001,
		Player2CharID: 1002,
		State:         tradedomain.TradeStateOpen,
	}

	invItems := []invdomain.InventoryItem{
		{ID: 1, CharID: charID, NameID: itemID, Amount: 10},
	}

	// First acquire single lock
	locks.EXPECT().Acquire(ctx, charLockKey, svc.lockTTL).Return(token, nil)
	// First GetTrade (outside withBothLocks)
	repo.EXPECT().GetTrade(ctx, tradeID).Return(existingTrade, nil)
	// Release first lock before acquiring both
	locks.EXPECT().Release(gomock.Any(), charLockKey, token).Return(nil)
	// Acquire both locks in order (1001 < 1002)
	locks.EXPECT().Acquire(ctx, charLockKey, svc.lockTTL).Return("token1", nil)
	locks.EXPECT().Acquire(ctx, peerLockKey, svc.lockTTL).Return("token2", nil)
	// Second GetTrade (inside withBothLocks)
	repo.EXPECT().GetTrade(ctx, tradeID).Return(existingTrade, nil)
	// Validate inventory
	invRepo.EXPECT().ListByChar(ctx, charID).Return(invItems, nil)
	// Release both locks (defer, even after error)
	locks.EXPECT().Release(gomock.Any(), charLockKey, "token1").Return(nil)
	locks.EXPECT().Release(gomock.Any(), peerLockKey, "token2").Return(nil)

	err := svc.AddTradeItem(ctx, tradeID, charID, itemID, amount)

	assert.Error(t, err)
	assert.ErrorIs(t, err, tradedomain.ErrInsufficientInventory)
}

func TestAddTradeItem_InvalidState(t *testing.T) {
	svc, repo, locks, _, _ := setupTestServiceWithMocks(t)
	ctx := context.Background()
	tradeID := "trade-123"
	charID := uint32(1001)
	itemID := uint32(501)
	amount := int32(5)

	token := "test-lock-token"
	lockKey := tradedomain.CharLockKey(charID)

	existingTrade := tradedomain.Trade{
		ID:            tradeID,
		Player1CharID: 1001,
		Player2CharID: 0,
		State:         tradedomain.TradeStateRequested,
	}

	locks.EXPECT().Acquire(ctx, lockKey, svc.lockTTL).Return(token, nil)
	repo.EXPECT().GetTrade(ctx, tradeID).Return(existingTrade, nil)
	locks.EXPECT().Release(gomock.Any(), lockKey, token).Return(nil)

	err := svc.AddTradeItem(ctx, tradeID, charID, itemID, amount)

	assert.Error(t, err)
	assert.ErrorIs(t, err, tradedomain.ErrInvalidTradeState)
}

func TestAddTradeItem_InvalidAmount(t *testing.T) {
	svc, _, _, _, _ := setupTestServiceWithMocks(t)
	ctx := context.Background()
	tradeID := "trade-123"
	charID := uint32(1001)
	itemID := uint32(501)
	amount := int32(0)

	err := svc.AddTradeItem(ctx, tradeID, charID, itemID, amount)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "item amount must be positive")
}

func TestAddTradeItem_NotParticipant(t *testing.T) {
	svc, repo, locks, _, _ := setupTestServiceWithMocks(t)
	ctx := context.Background()
	tradeID := "trade-123"
	charID := uint32(9999)
	itemID := uint32(501)
	amount := int32(5)

	token := "test-lock-token"
	lockKey := tradedomain.CharLockKey(charID)

	existingTrade := tradedomain.Trade{
		ID:            tradeID,
		Player1CharID: 1001,
		Player2CharID: 1002,
		State:         tradedomain.TradeStateOpen,
	}

	// Acquire lock
	locks.EXPECT().Acquire(ctx, lockKey, svc.lockTTL).Return(token, nil)
	// GetTrade
	repo.EXPECT().GetTrade(ctx, tradeID).Return(existingTrade, nil)
	// Release lock (error path)
	locks.EXPECT().Release(gomock.Any(), lockKey, token).Return(nil)

	err := svc.AddTradeItem(ctx, tradeID, charID, itemID, amount)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not a participant")
}

func TestAddTradeItem_LockBusy(t *testing.T) {
	svc, _, locks, _, _ := setupTestServiceWithMocks(t)
	ctx := context.Background()
	tradeID := "trade-123"
	charID := uint32(1001)
	itemID := uint32(501)
	amount := int32(5)

	lockKey := tradedomain.CharLockKey(charID)

	locks.EXPECT().Acquire(ctx, lockKey, svc.lockTTL).Return("", tradedomain.ErrLockBusy)

	err := svc.AddTradeItem(ctx, tradeID, charID, itemID, amount)

	assert.Error(t, err)
	assert.ErrorIs(t, err, tradedomain.ErrLockBusy)
}

func TestAddTradeItem_NilInventoryRepo(t *testing.T) {
	svc, repo, locks := setupTestService(t)
	ctx := context.Background()
	tradeID := "trade-123"
	charID := uint32(1001)
	peerID := uint32(1002)
	itemID := uint32(501)
	amount := int32(5)

	token := "test-lock-token"
	charLockKey := tradedomain.CharLockKey(charID)
	peerLockKey := tradedomain.CharLockKey(peerID)

	existingTrade := tradedomain.Trade{
		ID:            tradeID,
		Player1CharID: 1001,
		Player2CharID: 1002,
		State:         tradedomain.TradeStateOpen,
		Player1Items:  []tradedomain.TradeItem{},
	}

	// First acquire single lock
	locks.EXPECT().Acquire(ctx, charLockKey, svc.lockTTL).Return(token, nil)
	// First GetTrade (outside withBothLocks)
	repo.EXPECT().GetTrade(ctx, tradeID).Return(existingTrade, nil)
	// Release first lock before acquiring both
	locks.EXPECT().Release(gomock.Any(), charLockKey, token).Return(nil)
	// Acquire both locks in order (1001 < 1002)
	locks.EXPECT().Acquire(ctx, charLockKey, svc.lockTTL).Return("token1", nil)
	locks.EXPECT().Acquire(ctx, peerLockKey, svc.lockTTL).Return("token2", nil)
	// Second GetTrade (inside withBothLocks)
	repo.EXPECT().GetTrade(ctx, tradeID).Return(existingTrade, nil)
	// Update trade (no inventory validation when repo is nil)
	repo.EXPECT().UpdateTrade(ctx, gomock.Any()).Return(nil)
	// Release both locks (defer)
	locks.EXPECT().Release(gomock.Any(), charLockKey, "token1").Return(nil)
	locks.EXPECT().Release(gomock.Any(), peerLockKey, "token2").Return(nil)

	err := svc.AddTradeItem(ctx, tradeID, charID, itemID, amount)

	require.NoError(t, err)
}

func TestAddTradeZeny_Success(t *testing.T) {
	svc, repo, locks, _, zenyRepo := setupTestServiceWithMocks(t)
	ctx := context.Background()
	tradeID := "trade-123"
	charID := uint32(1001)
	peerID := uint32(1002)
	zeny := uint32(1000)

	token := "test-lock-token"
	charLockKey := tradedomain.CharLockKey(charID)
	peerLockKey := tradedomain.CharLockKey(peerID)

	existingTrade := tradedomain.Trade{
		ID:            tradeID,
		Player1CharID: 1001,
		Player2CharID: 1002,
		State:         tradedomain.TradeStateOpen,
		Player1Zeny:   0,
	}

	// First acquire single lock
	locks.EXPECT().Acquire(ctx, charLockKey, svc.lockTTL).Return(token, nil)
	// First GetTrade (outside withBothLocks)
	repo.EXPECT().GetTrade(ctx, tradeID).Return(existingTrade, nil)
	// Release first lock before acquiring both
	locks.EXPECT().Release(gomock.Any(), charLockKey, token).Return(nil)
	// Acquire both locks in order (1001 < 1002)
	locks.EXPECT().Acquire(ctx, charLockKey, svc.lockTTL).Return("token1", nil)
	locks.EXPECT().Acquire(ctx, peerLockKey, svc.lockTTL).Return("token2", nil)
	// Second GetTrade (inside withBothLocks)
	repo.EXPECT().GetTrade(ctx, tradeID).Return(existingTrade, nil)
	// Get zeny for validation
	zenyRepo.EXPECT().GetZeny(ctx, charID).Return(uint32(50000), nil)
	// Update trade
	repo.EXPECT().UpdateTrade(ctx, gomock.Any()).Return(nil)
	// Release both locks (defer)
	locks.EXPECT().Release(gomock.Any(), charLockKey, "token1").Return(nil)
	locks.EXPECT().Release(gomock.Any(), peerLockKey, "token2").Return(nil)

	err := svc.AddTradeZeny(ctx, tradeID, charID, zeny)

	require.NoError(t, err)
}

func TestAddTradeZeny_InsufficientZeny(t *testing.T) {
	svc, repo, locks, _, zenyRepo := setupTestServiceWithMocks(t)
	ctx := context.Background()
	tradeID := "trade-123"
	charID := uint32(1001)
	peerID := uint32(1002)
	zeny := uint32(1000)

	token := "test-lock-token"
	charLockKey := tradedomain.CharLockKey(charID)
	peerLockKey := tradedomain.CharLockKey(peerID)

	existingTrade := tradedomain.Trade{
		ID:            tradeID,
		Player1CharID: 1001,
		Player2CharID: 1002,
		State:         tradedomain.TradeStateOpen,
		Player1Zeny:   500,
	}

	// First acquire single lock
	locks.EXPECT().Acquire(ctx, charLockKey, svc.lockTTL).Return(token, nil)
	// First GetTrade (outside withBothLocks)
	repo.EXPECT().GetTrade(ctx, tradeID).Return(existingTrade, nil)
	// Release first lock before acquiring both
	locks.EXPECT().Release(gomock.Any(), charLockKey, token).Return(nil)
	// Acquire both locks in order (1001 < 1002)
	locks.EXPECT().Acquire(ctx, charLockKey, svc.lockTTL).Return("token1", nil)
	locks.EXPECT().Acquire(ctx, peerLockKey, svc.lockTTL).Return("token2", nil)
	// Second GetTrade (inside withBothLocks)
	repo.EXPECT().GetTrade(ctx, tradeID).Return(existingTrade, nil)
	// Get zeny for validation
	zenyRepo.EXPECT().GetZeny(ctx, charID).Return(uint32(1000), nil)
	// Release both locks (defer, even after error)
	locks.EXPECT().Release(gomock.Any(), charLockKey, "token1").Return(nil)
	locks.EXPECT().Release(gomock.Any(), peerLockKey, "token2").Return(nil)

	err := svc.AddTradeZeny(ctx, tradeID, charID, zeny)

	assert.Error(t, err)
	assert.ErrorIs(t, err, tradedomain.ErrInsufficientInventory)
}

func TestAddTradeZeny_ZeroZeny(t *testing.T) {
	svc, _, _ := setupTestService(t)
	ctx := context.Background()
	tradeID := "trade-123"
	charID := uint32(1001)
	zeny := uint32(0)

	err := svc.AddTradeZeny(ctx, tradeID, charID, zeny)

	assert.NoError(t, err)
}

func TestAddTradeZeny_InvalidState(t *testing.T) {
	svc, repo, locks, _, _ := setupTestServiceWithMocks(t)
	ctx := context.Background()
	tradeID := "trade-123"
	charID := uint32(1001)
	zeny := uint32(1000)

	token := "test-lock-token"
	lockKey := tradedomain.CharLockKey(charID)

	existingTrade := tradedomain.Trade{
		ID:            tradeID,
		Player1CharID: 1001,
		Player2CharID: 0,
		State:         tradedomain.TradeStateRequested,
	}

	locks.EXPECT().Acquire(ctx, lockKey, svc.lockTTL).Return(token, nil)
	repo.EXPECT().GetTrade(ctx, tradeID).Return(existingTrade, nil)
	locks.EXPECT().Release(gomock.Any(), lockKey, token).Return(nil)

	err := svc.AddTradeZeny(ctx, tradeID, charID, zeny)

	assert.Error(t, err)
	assert.ErrorIs(t, err, tradedomain.ErrInvalidTradeState)
}

func TestAddTradeZeny_NilZenyRepo(t *testing.T) {
	svc, repo, locks := setupTestService(t)
	ctx := context.Background()
	tradeID := "trade-123"
	charID := uint32(1001)
	peerID := uint32(1002)
	zeny := uint32(1000)

	token := "test-lock-token"
	charLockKey := tradedomain.CharLockKey(charID)
	peerLockKey := tradedomain.CharLockKey(peerID)

	existingTrade := tradedomain.Trade{
		ID:            tradeID,
		Player1CharID: 1001,
		Player2CharID: 1002,
		State:         tradedomain.TradeStateOpen,
		Player1Zeny:   0,
	}

	// First acquire single lock
	locks.EXPECT().Acquire(ctx, charLockKey, svc.lockTTL).Return(token, nil)
	// First GetTrade (outside withBothLocks)
	repo.EXPECT().GetTrade(ctx, tradeID).Return(existingTrade, nil)
	// Release first lock before acquiring both
	locks.EXPECT().Release(gomock.Any(), charLockKey, token).Return(nil)
	// Acquire both locks in order (1001 < 1002)
	locks.EXPECT().Acquire(ctx, charLockKey, svc.lockTTL).Return("token1", nil)
	locks.EXPECT().Acquire(ctx, peerLockKey, svc.lockTTL).Return("token2", nil)
	// Second GetTrade (inside withBothLocks)
	repo.EXPECT().GetTrade(ctx, tradeID).Return(existingTrade, nil)
	// Update trade (no zeny validation when repo is nil)
	repo.EXPECT().UpdateTrade(ctx, gomock.Any()).Return(nil)
	// Release both locks (defer)
	locks.EXPECT().Release(gomock.Any(), charLockKey, "token1").Return(nil)
	locks.EXPECT().Release(gomock.Any(), peerLockKey, "token2").Return(nil)

	err := svc.AddTradeZeny(ctx, tradeID, charID, zeny)

	require.NoError(t, err)
}

func TestAddTradeZeny_NotParticipant(t *testing.T) {
	svc, repo, locks, _, _ := setupTestServiceWithMocks(t)
	ctx := context.Background()
	tradeID := "trade-123"
	charID := uint32(9999)
	zeny := uint32(1000)

	token := "test-lock-token"
	lockKey := tradedomain.CharLockKey(charID)

	existingTrade := tradedomain.Trade{
		ID:            tradeID,
		Player1CharID: 1001,
		Player2CharID: 1002,
		State:         tradedomain.TradeStateOpen,
	}

	// Acquire lock
	locks.EXPECT().Acquire(ctx, lockKey, svc.lockTTL).Return(token, nil)
	// GetTrade
	repo.EXPECT().GetTrade(ctx, tradeID).Return(existingTrade, nil)
	// Release lock (error path)
	locks.EXPECT().Release(gomock.Any(), lockKey, token).Return(nil)

	err := svc.AddTradeZeny(ctx, tradeID, charID, zeny)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not a participant")
}

func TestConfirmTrade_OneParty(t *testing.T) {
	svc, repo, locks := setupTestService(t)
	ctx := context.Background()
	tradeID := "trade-123"
	charID := uint32(1001)
	peerID := uint32(1002)

	token := "test-lock-token"
	charLockKey := tradedomain.CharLockKey(charID)
	peerLockKey := tradedomain.CharLockKey(peerID)

	existingTrade := tradedomain.Trade{
		ID:               tradeID,
		Player1CharID:    1001,
		Player2CharID:    1002,
		State:            tradedomain.TradeStateOpen,
		Player1Confirmed: false,
		Player2Confirmed: false,
	}

	// First acquire single lock
	locks.EXPECT().Acquire(ctx, charLockKey, svc.lockTTL).Return(token, nil)
	// First GetTrade (outside withBothLocks)
	repo.EXPECT().GetTrade(ctx, tradeID).Return(existingTrade, nil)
	// Release first lock before acquiring both
	locks.EXPECT().Release(gomock.Any(), charLockKey, token).Return(nil)
	// Acquire both locks in order (1001 < 1002)
	locks.EXPECT().Acquire(ctx, charLockKey, svc.lockTTL).Return("token1", nil)
	locks.EXPECT().Acquire(ctx, peerLockKey, svc.lockTTL).Return("token2", nil)
	// Second GetTrade (inside withBothLocks)
	repo.EXPECT().GetTrade(ctx, tradeID).Return(existingTrade, nil)
	// Update trade
	repo.EXPECT().UpdateTrade(ctx, gomock.Any()).Return(nil)
	// Release both locks (defer)
	locks.EXPECT().Release(gomock.Any(), charLockKey, "token1").Return(nil)
	locks.EXPECT().Release(gomock.Any(), peerLockKey, "token2").Return(nil)

	err := svc.ConfirmTrade(ctx, tradeID, charID)

	require.NoError(t, err)
}

func TestConfirmTrade_BothParties(t *testing.T) {
	svc, repo, locks := setupTestService(t)
	ctx := context.Background()
	tradeID := "trade-123"
	charID := uint32(1002)
	peerID := uint32(1001)

	token := "test-lock-token"
	charLockKey := tradedomain.CharLockKey(charID)
	peerLockKey := tradedomain.CharLockKey(peerID)

	existingTrade := tradedomain.Trade{
		ID:               tradeID,
		Player1CharID:    1001,
		Player2CharID:    1002,
		State:            tradedomain.TradeStateConfirmed,
		Player1Confirmed: true,
		Player2Confirmed: false,
	}

	// First acquire single lock
	locks.EXPECT().Acquire(ctx, charLockKey, svc.lockTTL).Return(token, nil)
	// First GetTrade (outside withBothLocks)
	repo.EXPECT().GetTrade(ctx, tradeID).Return(existingTrade, nil)
	// Release first lock before acquiring both
	locks.EXPECT().Release(gomock.Any(), charLockKey, token).Return(nil)
	// Acquire both locks in order (1001 < 1002)
	locks.EXPECT().Acquire(ctx, peerLockKey, svc.lockTTL).Return("token1", nil)
	locks.EXPECT().Acquire(ctx, charLockKey, svc.lockTTL).Return("token2", nil)
	// Second GetTrade (inside withBothLocks)
	repo.EXPECT().GetTrade(ctx, tradeID).Return(existingTrade, nil)
	// Update trade
	repo.EXPECT().UpdateTrade(ctx, gomock.Any()).Return(nil)
	// Release both locks (defer)
	locks.EXPECT().Release(gomock.Any(), peerLockKey, "token1").Return(nil)
	locks.EXPECT().Release(gomock.Any(), charLockKey, "token2").Return(nil)

	err := svc.ConfirmTrade(ctx, tradeID, charID)

	require.NoError(t, err)
}

func TestConfirmTrade_InvalidState(t *testing.T) {
	svc, repo, locks := setupTestService(t)
	ctx := context.Background()
	tradeID := "trade-123"
	charID := uint32(1001)

	token := "test-lock-token"
	lockKey := tradedomain.CharLockKey(charID)

	existingTrade := tradedomain.Trade{
		ID:    tradeID,
		State: tradedomain.TradeStateRequested,
	}

	locks.EXPECT().Acquire(ctx, lockKey, svc.lockTTL).Return(token, nil)
	repo.EXPECT().GetTrade(ctx, tradeID).Return(existingTrade, nil)
	locks.EXPECT().Release(gomock.Any(), lockKey, token).Return(nil)

	err := svc.ConfirmTrade(ctx, tradeID, charID)

	assert.Error(t, err)
	assert.ErrorIs(t, err, tradedomain.ErrInvalidTradeState)
}

func TestConfirmTrade_NotParticipant(t *testing.T) {
	svc, repo, locks := setupTestService(t)
	ctx := context.Background()
	tradeID := "trade-123"
	charID := uint32(9999)

	token := "test-lock-token"
	lockKey := tradedomain.CharLockKey(charID)

	existingTrade := tradedomain.Trade{
		ID:               tradeID,
		Player1CharID:    1001,
		Player2CharID:    1002,
		State:            tradedomain.TradeStateOpen,
		Player1Confirmed: false,
		Player2Confirmed: false,
	}

	// Acquire lock
	locks.EXPECT().Acquire(ctx, lockKey, svc.lockTTL).Return(token, nil)
	// GetTrade
	repo.EXPECT().GetTrade(ctx, tradeID).Return(existingTrade, nil)
	// Release lock (error path)
	locks.EXPECT().Release(gomock.Any(), lockKey, token).Return(nil)

	err := svc.ConfirmTrade(ctx, tradeID, charID)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not a participant")
}

func TestConfirmTrade_LockBusy(t *testing.T) {
	svc, _, locks := setupTestService(t)
	ctx := context.Background()
	tradeID := "trade-123"
	charID := uint32(1001)

	lockKey := tradedomain.CharLockKey(charID)

	locks.EXPECT().Acquire(ctx, lockKey, svc.lockTTL).Return("", tradedomain.ErrLockBusy)

	err := svc.ConfirmTrade(ctx, tradeID, charID)

	assert.Error(t, err)
	assert.ErrorIs(t, err, tradedomain.ErrLockBusy)
}

func TestCompleteTrade_Success(t *testing.T) {
	svc, repo, locks := setupTestService(t)
	ctx := context.Background()
	tradeID := "trade-123"
	charID := uint32(1001)

	player1LockKey := tradedomain.CharLockKey(1001)
	player2LockKey := tradedomain.CharLockKey(1002)

	existingTrade := tradedomain.Trade{
		ID:               tradeID,
		Player1CharID:    1001,
		Player2CharID:    1002,
		State:            tradedomain.TradeStateLocked,
		Player1Confirmed: true,
		Player2Confirmed: true,
	}

	// First GetTrade (before withBothLocks)
	repo.EXPECT().GetTrade(ctx, tradeID).Return(existingTrade, nil)
	// Acquire both locks in order (1001 < 1002)
	locks.EXPECT().Acquire(ctx, player1LockKey, svc.lockTTL).Return("token1", nil)
	locks.EXPECT().Acquire(ctx, player2LockKey, svc.lockTTL).Return("token2", nil)
	// Second GetTrade (inside withBothLocks)
	repo.EXPECT().GetTrade(ctx, tradeID).Return(existingTrade, nil)
	// Execute trade transfer
	repo.EXPECT().ExecuteTradeTransfer(ctx, existingTrade).Return(nil)
	// Update trade state
	repo.EXPECT().UpdateTrade(ctx, gomock.Any()).Return(nil)
	// Release both locks (defer)
	locks.EXPECT().Release(gomock.Any(), player1LockKey, "token1").Return(nil)
	locks.EXPECT().Release(gomock.Any(), player2LockKey, "token2").Return(nil)

	err := svc.CompleteTrade(ctx, tradeID, charID)

	require.NoError(t, err)
}

func TestCompleteTrade_InvalidState(t *testing.T) {
	svc, repo, _ := setupTestService(t)
	ctx := context.Background()
	tradeID := "trade-123"
	charID := uint32(1001)

	existingTrade := tradedomain.Trade{
		ID:    tradeID,
		State: tradedomain.TradeStateOpen,
	}

	repo.EXPECT().GetTrade(ctx, tradeID).Return(existingTrade, nil)

	err := svc.CompleteTrade(ctx, tradeID, charID)

	assert.Error(t, err)
	assert.ErrorIs(t, err, tradedomain.ErrInvalidTradeState)
}

func TestCompleteTrade_NotParticipant(t *testing.T) {
	svc, repo, _ := setupTestService(t)
	ctx := context.Background()
	tradeID := "trade-123"
	charID := uint32(9999)

	existingTrade := tradedomain.Trade{
		ID:               tradeID,
		Player1CharID:    1001,
		Player2CharID:    1002,
		State:            tradedomain.TradeStateLocked,
		Player1Confirmed: true,
		Player2Confirmed: true,
	}

	repo.EXPECT().GetTrade(ctx, tradeID).Return(existingTrade, nil)

	err := svc.CompleteTrade(ctx, tradeID, charID)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not a participant")
}

func TestCompleteTrade_Player2LockBusy(t *testing.T) {
	svc, repo, locks := setupTestService(t)
	ctx := context.Background()
	tradeID := "trade-123"
	charID := uint32(1001)

	player1LockKey := tradedomain.CharLockKey(1001)
	player2LockKey := tradedomain.CharLockKey(1002)

	existingTrade := tradedomain.Trade{
		ID:               tradeID,
		Player1CharID:    1001,
		Player2CharID:    1002,
		State:            tradedomain.TradeStateLocked,
		Player1Confirmed: true,
		Player2Confirmed: true,
	}

	// First GetTrade (before withBothLocks)
	repo.EXPECT().GetTrade(ctx, tradeID).Return(existingTrade, nil)
	// Acquire first lock
	locks.EXPECT().Acquire(ctx, player1LockKey, svc.lockTTL).Return("token1", nil)
	// Second lock is busy
	locks.EXPECT().Acquire(ctx, player2LockKey, svc.lockTTL).Return("", tradedomain.ErrLockBusy)
	// Release first lock (defer on error)
	locks.EXPECT().Release(gomock.Any(), player1LockKey, "token1").Return(nil)

	err := svc.CompleteTrade(ctx, tradeID, charID)

	assert.Error(t, err)
	assert.ErrorIs(t, err, tradedomain.ErrLockBusy)
}

func TestCompleteTrade_TransferFails(t *testing.T) {
	svc, repo, locks := setupTestService(t)
	ctx := context.Background()
	tradeID := "trade-123"
	charID := uint32(1001)

	player1LockKey := tradedomain.CharLockKey(1001)
	player2LockKey := tradedomain.CharLockKey(1002)

	existingTrade := tradedomain.Trade{
		ID:               tradeID,
		Player1CharID:    1001,
		Player2CharID:    1002,
		State:            tradedomain.TradeStateLocked,
		Player1Confirmed: true,
		Player2Confirmed: true,
	}

	// First GetTrade (before withBothLocks)
	repo.EXPECT().GetTrade(ctx, tradeID).Return(existingTrade, nil)
	// Acquire both locks in order (1001 < 1002)
	locks.EXPECT().Acquire(ctx, player1LockKey, svc.lockTTL).Return("token1", nil)
	locks.EXPECT().Acquire(ctx, player2LockKey, svc.lockTTL).Return("token2", nil)
	// Second GetTrade (inside withBothLocks)
	repo.EXPECT().GetTrade(ctx, tradeID).Return(existingTrade, nil)
	// Execute trade transfer fails
	repo.EXPECT().ExecuteTradeTransfer(ctx, existingTrade).Return(assert.AnError)
	// Release both locks (defer, even after error)
	locks.EXPECT().Release(gomock.Any(), player1LockKey, "token1").Return(nil)
	locks.EXPECT().Release(gomock.Any(), player2LockKey, "token2").Return(nil)

	err := svc.CompleteTrade(ctx, tradeID, charID)

	assert.Error(t, err)
}

func TestCancelTrade_Success(t *testing.T) {
	svc, repo, locks := setupTestService(t)
	ctx := context.Background()
	tradeID := "trade-123"
	charID := uint32(1001)
	peerID := uint32(1002)

	token := "test-lock-token"
	charLockKey := tradedomain.CharLockKey(charID)
	peerLockKey := tradedomain.CharLockKey(peerID)

	existingTrade := tradedomain.Trade{
		ID:            tradeID,
		Player1CharID: 1001,
		Player2CharID: 1002,
		State:         tradedomain.TradeStateOpen,
	}

	// First acquire single lock
	locks.EXPECT().Acquire(ctx, charLockKey, svc.lockTTL).Return(token, nil)
	// First GetTrade (outside withBothLocks)
	repo.EXPECT().GetTrade(ctx, tradeID).Return(existingTrade, nil)
	// Release first lock before acquiring both
	locks.EXPECT().Release(gomock.Any(), charLockKey, token).Return(nil)
	// Acquire both locks in order (1001 < 1002)
	locks.EXPECT().Acquire(ctx, charLockKey, svc.lockTTL).Return("token1", nil)
	locks.EXPECT().Acquire(ctx, peerLockKey, svc.lockTTL).Return("token2", nil)
	// Second GetTrade (inside withBothLocks)
	repo.EXPECT().GetTrade(ctx, tradeID).Return(existingTrade, nil)
	// Update trade
	repo.EXPECT().UpdateTrade(ctx, gomock.Any()).Return(nil)
	// Release both locks (defer)
	locks.EXPECT().Release(gomock.Any(), charLockKey, "token1").Return(nil)
	locks.EXPECT().Release(gomock.Any(), peerLockKey, "token2").Return(nil)

	err := svc.CancelTrade(ctx, tradeID, charID)

	require.NoError(t, err)
}

func TestCancelTrade_FromRequestedState(t *testing.T) {
	svc, repo, locks := setupTestService(t)
	ctx := context.Background()
	tradeID := "trade-123"
	charID := uint32(1001)

	token := "test-lock-token"
	lockKey := tradedomain.CharLockKey(charID)

	existingTrade := tradedomain.Trade{
		ID:            tradeID,
		Player1CharID: 1001,
		Player2CharID: 0,
		State:         tradedomain.TradeStateRequested,
	}

	locks.EXPECT().Acquire(ctx, lockKey, svc.lockTTL).Return(token, nil)
	repo.EXPECT().GetTrade(ctx, tradeID).Return(existingTrade, nil)
	repo.EXPECT().UpdateTrade(ctx, gomock.Any()).Return(nil)
	locks.EXPECT().Release(gomock.Any(), lockKey, token).Return(nil)

	err := svc.CancelTrade(ctx, tradeID, charID)

	require.NoError(t, err)
}

func TestCancelTrade_NotParticipant(t *testing.T) {
	svc, repo, locks := setupTestService(t)
	ctx := context.Background()
	tradeID := "trade-123"
	charID := uint32(9999)

	token := "test-lock-token"
	lockKey := tradedomain.CharLockKey(charID)

	existingTrade := tradedomain.Trade{
		ID:            tradeID,
		Player1CharID: 1001,
		Player2CharID: 1002,
		State:         tradedomain.TradeStateOpen,
	}

	// Acquire lock
	locks.EXPECT().Acquire(ctx, lockKey, svc.lockTTL).Return(token, nil)
	// GetTrade
	repo.EXPECT().GetTrade(ctx, tradeID).Return(existingTrade, nil)
	// Release lock (error path)
	locks.EXPECT().Release(gomock.Any(), lockKey, token).Return(nil)

	err := svc.CancelTrade(ctx, tradeID, charID)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not a participant")
}

func TestCancelTrade_LockBusy(t *testing.T) {
	svc, _, locks := setupTestService(t)
	ctx := context.Background()
	tradeID := "trade-123"
	charID := uint32(1001)

	lockKey := tradedomain.CharLockKey(charID)

	locks.EXPECT().Acquire(ctx, lockKey, svc.lockTTL).Return("", tradedomain.ErrLockBusy)

	err := svc.CancelTrade(ctx, tradeID, charID)

	assert.Error(t, err)
	assert.ErrorIs(t, err, tradedomain.ErrLockBusy)
}

func TestDupePrevention_ConcurrentTradeRequest(t *testing.T) {
	svc, repo, locks := setupTestService(t)
	ctx := context.Background()
	charID := uint32(1001)
	targetCharID := uint32(1002)

	lockKey := tradedomain.CharLockKey(charID)

	firstToken := "first-token"
	locks.EXPECT().Acquire(ctx, lockKey, svc.lockTTL).Return(firstToken, nil)
	repo.EXPECT().CreateTrade(ctx, gomock.Any()).Return("trade-123", nil)
	locks.EXPECT().Release(gomock.Any(), lockKey, firstToken).Return(nil)

	_, err1 := svc.RequestTrade(ctx, charID, targetCharID)
	require.NoError(t, err1)

	locks.EXPECT().Acquire(ctx, lockKey, svc.lockTTL).Return("", tradedomain.ErrLockBusy)

	_, err2 := svc.RequestTrade(ctx, charID, targetCharID)

	assert.Error(t, err2)
	assert.ErrorIs(t, err2, tradedomain.ErrLockBusy)
}

func TestReleaseLockBestEffort(t *testing.T) {
	svc, repo, locks := setupTestService(t)
	ctx := context.Background()
	charID := uint32(1001)
	targetCharID := uint32(1002)

	token := "test-lock-token"
	lockKey := tradedomain.CharLockKey(charID)

	locks.EXPECT().Acquire(ctx, lockKey, svc.lockTTL).Return(token, nil)
	repo.EXPECT().CreateTrade(ctx, gomock.Any()).Return("trade-123", nil)
	locks.EXPECT().Release(gomock.Any(), lockKey, token).Return(assert.AnError)

	trade, err := svc.RequestTrade(ctx, charID, targetCharID)

	require.NoError(t, err)
	assert.NotEmpty(t, trade.ID)
}

func TestLockKeyFormat(t *testing.T) {
	charID := uint32(1001)
	expectedKey := "trade:char:1001"
	actualKey := tradedomain.CharLockKey(charID)
	assert.Equal(t, expectedKey, actualKey)
}

func TestDefaultLockTTL(t *testing.T) {
	svc, _, _ := setupTestService(t)
	assert.Equal(t, 5*time.Second, svc.lockTTL)
}

func TestCustomLockTTL(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := trademock.NewMockTradeRepository(ctrl)
	locks := trademock.NewMockLockStore(ctrl)
	customTTL := 10 * time.Second
	svc := NewTradeService(repo, locks, nil, nil, customTTL).(*tradeService)

	assert.Equal(t, customTTL, svc.lockTTL)
}

func TestNewTradeService_ZeroTTL(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := trademock.NewMockTradeRepository(ctrl)
	locks := trademock.NewMockLockStore(ctrl)
	svc := NewTradeService(repo, locks, nil, nil, 0).(*tradeService)

	assert.Equal(t, 5*time.Second, svc.lockTTL)
}

func TestConfirmTrade_ConfirmedStateAllowed(t *testing.T) {
	svc, repo, locks := setupTestService(t)
	ctx := context.Background()
	tradeID := "trade-123"
	charID := uint32(1002)
	peerID := uint32(1001)

	token := "test-lock-token"
	charLockKey := tradedomain.CharLockKey(charID)
	peerLockKey := tradedomain.CharLockKey(peerID)

	existingTrade := tradedomain.Trade{
		ID:               tradeID,
		Player1CharID:    1001,
		Player2CharID:    1002,
		State:            tradedomain.TradeStateConfirmed,
		Player1Confirmed: true,
		Player2Confirmed: false,
	}

	// First acquire single lock
	locks.EXPECT().Acquire(ctx, charLockKey, svc.lockTTL).Return(token, nil)
	// First GetTrade (outside withBothLocks)
	repo.EXPECT().GetTrade(ctx, tradeID).Return(existingTrade, nil)
	// Release first lock before acquiring both
	locks.EXPECT().Release(gomock.Any(), charLockKey, token).Return(nil)
	// Acquire both locks in order (1001 < 1002)
	locks.EXPECT().Acquire(ctx, peerLockKey, svc.lockTTL).Return("token1", nil)
	locks.EXPECT().Acquire(ctx, charLockKey, svc.lockTTL).Return("token2", nil)
	// Second GetTrade (inside withBothLocks)
	repo.EXPECT().GetTrade(ctx, tradeID).Return(existingTrade, nil)
	// Update trade
	repo.EXPECT().UpdateTrade(ctx, gomock.Any()).Return(nil)
	// Release both locks (defer)
	locks.EXPECT().Release(gomock.Any(), peerLockKey, "token1").Return(nil)
	locks.EXPECT().Release(gomock.Any(), charLockKey, "token2").Return(nil)

	err := svc.ConfirmTrade(ctx, tradeID, charID)

	require.NoError(t, err)
}

func TestAddTradeItem_Player2(t *testing.T) {
	svc, repo, locks, invRepo, _ := setupTestServiceWithMocks(t)
	ctx := context.Background()
	tradeID := "trade-123"
	charID := uint32(1002)
	peerID := uint32(1001)
	itemID := uint32(501)
	amount := int32(5)

	token := "test-lock-token"
	charLockKey := tradedomain.CharLockKey(charID)
	peerLockKey := tradedomain.CharLockKey(peerID)

	existingTrade := tradedomain.Trade{
		ID:            tradeID,
		Player1CharID: 1001,
		Player2CharID: 1002,
		State:         tradedomain.TradeStateOpen,
		Player2Items:  []tradedomain.TradeItem{},
	}

	invItems := []invdomain.InventoryItem{
		{ID: 1, CharID: charID, NameID: itemID, Amount: 10},
	}

	// First acquire single lock
	locks.EXPECT().Acquire(ctx, charLockKey, svc.lockTTL).Return(token, nil)
	// First GetTrade (outside withBothLocks)
	repo.EXPECT().GetTrade(ctx, tradeID).Return(existingTrade, nil)
	// Release first lock before acquiring both
	locks.EXPECT().Release(gomock.Any(), charLockKey, token).Return(nil)
	// Acquire both locks in order (1001 < 1002)
	locks.EXPECT().Acquire(ctx, peerLockKey, svc.lockTTL).Return("token1", nil)
	locks.EXPECT().Acquire(ctx, charLockKey, svc.lockTTL).Return("token2", nil)
	// Second GetTrade (inside withBothLocks)
	repo.EXPECT().GetTrade(ctx, tradeID).Return(existingTrade, nil)
	// Validate inventory
	invRepo.EXPECT().ListByChar(ctx, charID).Return(invItems, nil)
	// Update trade
	repo.EXPECT().UpdateTrade(ctx, gomock.Any()).Return(nil)
	// Release both locks (defer)
	locks.EXPECT().Release(gomock.Any(), peerLockKey, "token1").Return(nil)
	locks.EXPECT().Release(gomock.Any(), charLockKey, "token2").Return(nil)

	err := svc.AddTradeItem(ctx, tradeID, charID, itemID, amount)

	require.NoError(t, err)
}

func TestAddTradeZeny_Player2(t *testing.T) {
	svc, repo, locks, _, zenyRepo := setupTestServiceWithMocks(t)
	ctx := context.Background()
	tradeID := "trade-123"
	charID := uint32(1002)
	peerID := uint32(1001)
	zeny := uint32(1000)

	token := "test-lock-token"
	charLockKey := tradedomain.CharLockKey(charID)
	peerLockKey := tradedomain.CharLockKey(peerID)

	existingTrade := tradedomain.Trade{
		ID:            tradeID,
		Player1CharID: 1001,
		Player2CharID: 1002,
		State:         tradedomain.TradeStateOpen,
		Player2Zeny:   0,
	}

	// First acquire single lock
	locks.EXPECT().Acquire(ctx, charLockKey, svc.lockTTL).Return(token, nil)
	// First GetTrade (outside withBothLocks)
	repo.EXPECT().GetTrade(ctx, tradeID).Return(existingTrade, nil)
	// Release first lock before acquiring both
	locks.EXPECT().Release(gomock.Any(), charLockKey, token).Return(nil)
	// Acquire both locks in order (1001 < 1002)
	locks.EXPECT().Acquire(ctx, peerLockKey, svc.lockTTL).Return("token1", nil)
	locks.EXPECT().Acquire(ctx, charLockKey, svc.lockTTL).Return("token2", nil)
	// Second GetTrade (inside withBothLocks)
	repo.EXPECT().GetTrade(ctx, tradeID).Return(existingTrade, nil)
	// Get zeny for validation
	zenyRepo.EXPECT().GetZeny(ctx, charID).Return(uint32(50000), nil)
	// Update trade
	repo.EXPECT().UpdateTrade(ctx, gomock.Any()).Return(nil)
	// Release both locks (defer)
	locks.EXPECT().Release(gomock.Any(), peerLockKey, "token1").Return(nil)
	locks.EXPECT().Release(gomock.Any(), charLockKey, "token2").Return(nil)

	err := svc.AddTradeZeny(ctx, tradeID, charID, zeny)

	require.NoError(t, err)
}

func TestCancelTrade_Player2(t *testing.T) {
	svc, repo, locks := setupTestService(t)
	ctx := context.Background()
	tradeID := "trade-123"
	charID := uint32(1002)
	peerID := uint32(1001)

	token := "test-lock-token"
	charLockKey := tradedomain.CharLockKey(charID)
	peerLockKey := tradedomain.CharLockKey(peerID)

	existingTrade := tradedomain.Trade{
		ID:            tradeID,
		Player1CharID: 1001,
		Player2CharID: 1002,
		State:         tradedomain.TradeStateOpen,
	}

	// First acquire single lock
	locks.EXPECT().Acquire(ctx, charLockKey, svc.lockTTL).Return(token, nil)
	// First GetTrade (outside withBothLocks)
	repo.EXPECT().GetTrade(ctx, tradeID).Return(existingTrade, nil)
	// Release first lock before acquiring both
	locks.EXPECT().Release(gomock.Any(), charLockKey, token).Return(nil)
	// Acquire both locks in order (1001 < 1002)
	locks.EXPECT().Acquire(ctx, peerLockKey, svc.lockTTL).Return("token1", nil)
	locks.EXPECT().Acquire(ctx, charLockKey, svc.lockTTL).Return("token2", nil)
	// Second GetTrade (inside withBothLocks)
	repo.EXPECT().GetTrade(ctx, tradeID).Return(existingTrade, nil)
	// Update trade
	repo.EXPECT().UpdateTrade(ctx, gomock.Any()).Return(nil)
	// Release both locks (defer)
	locks.EXPECT().Release(gomock.Any(), peerLockKey, "token1").Return(nil)
	locks.EXPECT().Release(gomock.Any(), charLockKey, "token2").Return(nil)

	err := svc.CancelTrade(ctx, tradeID, charID)

	require.NoError(t, err)
}
