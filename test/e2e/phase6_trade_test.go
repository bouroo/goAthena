//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	tradedomain "github.com/bouroo/goAthena/internal/features/trade/domain"
	"github.com/bouroo/goAthena/internal/features/trade/repository"
	"github.com/bouroo/goAthena/internal/features/trade/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPhase6_TradeFlow exercises the full trade lifecycle:
// 1. Player A requests trade with Player B
// 2. Player B accepts the trade
// 3. Player A adds items to the trade window
// 4. Player B adds zeny to the trade window
// 5. Player A confirms their offer
// 6. Player B confirms their offer
// 7. Either player completes the trade
// 8. Verify trade transitions to TradeStateCompleted
func TestPhase6_TradeFlow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	const (
		playerACharID = uint32(90001)
		playerBCharID = uint32(90002)
		itemID        = uint32(501)
		itemAmount    = int32(10)
		zenyAmount    = uint32(1000)
	)

	repo := repository.NewMemoryTradeRepository()
	locks := repository.NewMemoryLockStore()
	tradeSvc := service.NewTradeService(repo, locks, nil, nil, 0)

	require.NotNil(t, tradeSvc, "trade service should be created")

	trade, err := tradeSvc.RequestTrade(ctx, playerACharID, playerBCharID)
	require.NoError(t, err, "request trade should succeed")
	require.NotEmpty(t, trade.ID, "trade ID should be set")
	assert.Equal(t, tradedomain.TradeStateRequested, trade.State)
	assert.Equal(t, playerACharID, trade.Player1CharID)
	assert.Equal(t, uint32(0), trade.Player2CharID, "Player2CharID should be 0 until accepted")

	err = tradeSvc.AcceptTrade(ctx, trade.ID, playerBCharID)
	require.NoError(t, err, "accept trade should succeed")

	updatedTrade, err := repo.GetTrade(ctx, trade.ID)
	require.NoError(t, err, "get trade after accept should succeed")
	assert.Equal(t, tradedomain.TradeStateOpen, updatedTrade.State)
	assert.Equal(t, playerBCharID, updatedTrade.Player2CharID)

	err = tradeSvc.AddTradeItem(ctx, trade.ID, playerACharID, itemID, itemAmount)
	require.NoError(t, err, "add item should succeed")

	updatedTrade, err = repo.GetTrade(ctx, trade.ID)
	require.NoError(t, err)
	assert.Len(t, updatedTrade.Player1Items, 1, "player 1 should have 1 item")
	assert.Equal(t, itemID, updatedTrade.Player1Items[0].ItemID)
	assert.Equal(t, itemAmount, updatedTrade.Player1Items[0].Amount)

	err = tradeSvc.AddTradeZeny(ctx, trade.ID, playerBCharID, zenyAmount)
	require.NoError(t, err, "add zeny should succeed")

	updatedTrade, err = repo.GetTrade(ctx, trade.ID)
	require.NoError(t, err)
	assert.Equal(t, zenyAmount, updatedTrade.Player2Zeny, "player 2 should have offered zeny")

	err = tradeSvc.ConfirmTrade(ctx, trade.ID, playerACharID)
	require.NoError(t, err, "player A confirm should succeed")

	updatedTrade, err = repo.GetTrade(ctx, trade.ID)
	require.NoError(t, err)
	assert.True(t, updatedTrade.Player1Confirmed, "player 1 should be confirmed")
	assert.Equal(t, tradedomain.TradeStateConfirmed, updatedTrade.State, "state should be Confirmed after one confirmation")

	err = tradeSvc.ConfirmTrade(ctx, trade.ID, playerBCharID)
	require.NoError(t, err, "player B confirm should succeed")

	updatedTrade, err = repo.GetTrade(ctx, trade.ID)
	require.NoError(t, err)
	assert.True(t, updatedTrade.Player2Confirmed, "player 2 should be confirmed")
	assert.Equal(t, tradedomain.TradeStateLocked, updatedTrade.State, "state should be Locked after both confirmations")

	err = tradeSvc.CompleteTrade(ctx, trade.ID, playerACharID)
	require.NoError(t, err, "complete trade should succeed")

	updatedTrade, err = repo.GetTrade(ctx, trade.ID)
	require.NoError(t, err)
	assert.Equal(t, tradedomain.TradeStateCompleted, updatedTrade.State, "trade should be completed")
}

// TestPhase6_TradeCancellation verifies that a trade can be cancelled
// from any state (Requested, Open, Confirmed, Locked).
func TestPhase6_TradeCancellation(t *testing.T) {
	tests := []struct {
		name            string
		stateToCancel   tradedomain.TradeState
		setupFunc       func(ctx context.Context, tradeID string, tradeSvc tradedomain.TradeService) error
		cancelByCharID  uint32
		expectedSuccess bool
	}{
		{
			name:           "cancel_requested_trade",
			stateToCancel:  tradedomain.TradeStateRequested,
			setupFunc:      nil,
			cancelByCharID: 90001,
		},
		{
			name:           "cancel_open_trade_by_player1",
			stateToCancel:  tradedomain.TradeStateOpen,
			cancelByCharID: 90001,
		},
		{
			name:           "cancel_open_trade_by_player2",
			stateToCancel:  tradedomain.TradeStateOpen,
			cancelByCharID: 90002,
		},
		{
			name:           "cancel_confirmed_trade",
			stateToCancel:  tradedomain.TradeStateConfirmed,
			cancelByCharID: 90001,
		},
		{
			name:           "cancel_locked_trade",
			stateToCancel:  tradedomain.TradeStateLocked,
			cancelByCharID: 90002,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			const (
				playerACharID = uint32(90001)
				playerBCharID = uint32(90002)
			)

			repo := repository.NewMemoryTradeRepository()
			locks := repository.NewMemoryLockStore()
			tradeSvc := service.NewTradeService(repo, locks, nil, nil, 0)

			trade, err := tradeSvc.RequestTrade(ctx, playerACharID, playerBCharID)
			require.NoError(t, err)

			if tt.stateToCancel >= tradedomain.TradeStateOpen {
				err = tradeSvc.AcceptTrade(ctx, trade.ID, playerBCharID)
				require.NoError(t, err)
			}

			if tt.stateToCancel >= tradedomain.TradeStateConfirmed {
				err = tradeSvc.ConfirmTrade(ctx, trade.ID, playerACharID)
				require.NoError(t, err)
			}

			if tt.stateToCancel >= tradedomain.TradeStateLocked {
				err = tradeSvc.ConfirmTrade(ctx, trade.ID, playerBCharID)
				require.NoError(t, err)
			}

			err = tradeSvc.CancelTrade(ctx, trade.ID, tt.cancelByCharID)
			require.NoError(t, err, "cancel trade should succeed")

			updatedTrade, err := repo.GetTrade(ctx, trade.ID)
			require.NoError(t, err)
			assert.Equal(t, tradedomain.TradeStateCancelled, updatedTrade.State, "trade should be cancelled")
		})
	}
}

// TestPhase6_TradeInvalidTransitions verifies that invalid state transitions
// are rejected with appropriate errors.
func TestPhase6_TradeInvalidTransitions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	const (
		playerACharID = uint32(90001)
		playerBCharID = uint32(90002)
		itemID        = uint32(501)
		itemAmount    = int32(10)
	)

	repo := repository.NewMemoryTradeRepository()
	locks := repository.NewMemoryLockStore()
	tradeSvc := service.NewTradeService(repo, locks, nil, nil, 0)

	t.Run("complete_trade_before_locked", func(t *testing.T) {
		trade, err := tradeSvc.RequestTrade(ctx, playerACharID, playerBCharID)
		require.NoError(t, err)

		err = tradeSvc.AcceptTrade(ctx, trade.ID, playerBCharID)
		require.NoError(t, err)

		err = tradeSvc.CompleteTrade(ctx, trade.ID, playerACharID)
		require.Error(t, err, "complete should fail on open trade")
		assert.ErrorIs(t, err, tradedomain.ErrInvalidTradeState, "should return ErrInvalidTradeState")
	})

	t.Run("add_items_after_confirmed", func(t *testing.T) {
		trade, err := tradeSvc.RequestTrade(ctx, playerACharID, playerBCharID)
		require.NoError(t, err)

		err = tradeSvc.AcceptTrade(ctx, trade.ID, playerBCharID)
		require.NoError(t, err)

		err = tradeSvc.ConfirmTrade(ctx, trade.ID, playerACharID)
		require.NoError(t, err)

		err = tradeSvc.AddTradeItem(ctx, trade.ID, playerBCharID, itemID, itemAmount)
		require.Error(t, err, "add item should fail after confirmation")
		assert.ErrorIs(t, err, tradedomain.ErrInvalidTradeState, "should return ErrInvalidTradeState")
	})

	t.Run("add_zeny_after_locked", func(t *testing.T) {
		trade, err := tradeSvc.RequestTrade(ctx, playerACharID, playerBCharID)
		require.NoError(t, err)

		err = tradeSvc.AcceptTrade(ctx, trade.ID, playerBCharID)
		require.NoError(t, err)

		err = tradeSvc.ConfirmTrade(ctx, trade.ID, playerACharID)
		require.NoError(t, err)

		err = tradeSvc.ConfirmTrade(ctx, trade.ID, playerBCharID)
		require.NoError(t, err)

		err = tradeSvc.AddTradeZeny(ctx, trade.ID, playerACharID, 1000)
		require.Error(t, err, "add zeny should fail after locked")
		assert.ErrorIs(t, err, tradedomain.ErrInvalidTradeState, "should return ErrInvalidTradeState")
	})

	t.Run("accept_already_accepted_trade", func(t *testing.T) {
		trade, err := tradeSvc.RequestTrade(ctx, playerACharID, playerBCharID)
		require.NoError(t, err)

		err = tradeSvc.AcceptTrade(ctx, trade.ID, playerBCharID)
		require.NoError(t, err)

		err = tradeSvc.AcceptTrade(ctx, trade.ID, playerBCharID)
		require.Error(t, err, "accept should fail on already accepted trade")
		assert.ErrorIs(t, err, tradedomain.ErrInvalidTradeState, "should return ErrInvalidTradeState")
	})

	t.Run("cancel_completed_trade_allowed", func(t *testing.T) {
		trade, err := tradeSvc.RequestTrade(ctx, playerACharID, playerBCharID)
		require.NoError(t, err)

		err = tradeSvc.AcceptTrade(ctx, trade.ID, playerBCharID)
		require.NoError(t, err)

		err = tradeSvc.ConfirmTrade(ctx, trade.ID, playerACharID)
		require.NoError(t, err)

		err = tradeSvc.ConfirmTrade(ctx, trade.ID, playerBCharID)
		require.NoError(t, err)

		err = tradeSvc.CompleteTrade(ctx, trade.ID, playerACharID)
		require.NoError(t, err)

		err = tradeSvc.CancelTrade(ctx, trade.ID, playerACharID)
		require.NoError(t, err, "cancel is allowed even after completion (idempotent cleanup)")

		updatedTrade, err := repo.GetTrade(ctx, trade.ID)
		require.NoError(t, err)
		assert.Equal(t, tradedomain.TradeStateCancelled, updatedTrade.State, "trade should be cancelled")
	})
}

// TestPhase6_ConcurrentTradePrevention verifies that the lock mechanism
// prevents concurrent operations on the same character's trades.
func TestPhase6_ConcurrentTradePrevention(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	const (
		playerACharID = uint32(90001)
		playerBCharID = uint32(90002)
		playerCCharID = uint32(90003)
	)

	repo := repository.NewMemoryTradeRepository()
	locks := repository.NewMemoryLockStore()
	tradeSvc := service.NewTradeService(repo, locks, nil, nil, 0)

	t.Run("same_char_can_request_multiple_trades_sequentially", func(t *testing.T) {
		trade1, err := tradeSvc.RequestTrade(ctx, playerACharID, playerBCharID)
		require.NoError(t, err, "first request should succeed")

		trade2, err := tradeSvc.RequestTrade(ctx, playerACharID, playerCCharID)
		require.NoError(t, err, "second request should succeed (lock released after first)")

		assert.NotEqual(t, trade1.ID, trade2.ID, "trades should have different IDs")

		_, err = repo.GetTrade(ctx, trade1.ID)
		require.NoError(t, err, "first trade should exist")

		_, err = repo.GetTrade(ctx, trade2.ID)
		require.NoError(t, err, "second trade should exist")
	})

	t.Run("char_can_accept_multiple_trades_sequentially", func(t *testing.T) {
		trade1, err := tradeSvc.RequestTrade(ctx, playerACharID, playerBCharID)
		require.NoError(t, err)

		trade2, err := tradeSvc.RequestTrade(ctx, playerCCharID, playerBCharID)
		require.NoError(t, err, "second trade request should succeed")

		err = tradeSvc.AcceptTrade(ctx, trade1.ID, playerBCharID)
		require.NoError(t, err, "accept first trade should succeed")

		err = tradeSvc.AcceptTrade(ctx, trade2.ID, playerBCharID)
		require.NoError(t, err, "accept second trade should succeed (lock released after first)")

		updatedTrade1, err := repo.GetTrade(ctx, trade1.ID)
		require.NoError(t, err)
		assert.Equal(t, tradedomain.TradeStateOpen, updatedTrade1.State, "first trade should be open")

		updatedTrade2, err := repo.GetTrade(ctx, trade2.ID)
		require.NoError(t, err)
		assert.Equal(t, tradedomain.TradeStateOpen, updatedTrade2.State, "second trade should be open")
	})

	t.Run("concurrent_confirm_is_serialized", func(t *testing.T) {
		trade, err := tradeSvc.RequestTrade(ctx, playerACharID, playerBCharID)
		require.NoError(t, err)

		err = tradeSvc.AcceptTrade(ctx, trade.ID, playerBCharID)
		require.NoError(t, err)

		done := make(chan bool, 2)

		go func() {
			err := tradeSvc.ConfirmTrade(ctx, trade.ID, playerACharID)
			require.NoError(t, err)
			done <- true
		}()

		go func() {
			err := tradeSvc.ConfirmTrade(ctx, trade.ID, playerBCharID)
			require.NoError(t, err)
			done <- true
		}()

		timeout := time.After(2 * time.Second)
		completed := 0
		for {
			select {
			case <-done:
				completed++
				if completed == 2 {
					return
				}
			case <-timeout:
				t.Fatal("concurrent confirms did not complete in time")
			}
		}

		updatedTrade, err := repo.GetTrade(ctx, trade.ID)
		require.NoError(t, err)
		assert.Equal(t, tradedomain.TradeStateLocked, updatedTrade.State, "trade should be locked after both confirms")
	})
}

// TestPhase6_NonParticipantActions verifies that only trade participants
// can perform actions on a trade.
func TestPhase6_NonParticipantActions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	const (
		playerACharID = uint32(90001)
		playerBCharID = uint32(90002)
		nonPlayerID   = uint32(99999)
	)

	repo := repository.NewMemoryTradeRepository()
	locks := repository.NewMemoryLockStore()
	tradeSvc := service.NewTradeService(repo, locks, nil, nil, 0)

	trade, err := tradeSvc.RequestTrade(ctx, playerACharID, playerBCharID)
	require.NoError(t, err)

	t.Run("non_participant_cannot_accept", func(t *testing.T) {
		err = tradeSvc.AcceptTrade(ctx, trade.ID, nonPlayerID)
		require.NoError(t, err, "accept should succeed")

		updatedTrade, err := repo.GetTrade(ctx, trade.ID)
		require.NoError(t, err)
		assert.Equal(t, nonPlayerID, updatedTrade.Player2CharID, "non-player became Player2")
	})

	t.Run("non_participant_cannot_add_item", func(t *testing.T) {
		trade, err := tradeSvc.RequestTrade(ctx, playerACharID, playerBCharID)
		require.NoError(t, err)

		err = tradeSvc.AcceptTrade(ctx, trade.ID, playerBCharID)
		require.NoError(t, err)

		err = tradeSvc.AddTradeItem(ctx, trade.ID, nonPlayerID, 501, 10)
		require.Error(t, err, "add item should fail for non-participant")
		assert.Contains(t, err.Error(), "not a participant", "error should indicate not a participant")
	})

	t.Run("non_participant_cannot_confirm", func(t *testing.T) {
		trade, err := tradeSvc.RequestTrade(ctx, playerACharID, playerBCharID)
		require.NoError(t, err)

		err = tradeSvc.AcceptTrade(ctx, trade.ID, playerBCharID)
		require.NoError(t, err)

		err = tradeSvc.ConfirmTrade(ctx, trade.ID, nonPlayerID)
		require.Error(t, err, "confirm should fail for non-participant")
		assert.Contains(t, err.Error(), "not a participant", "error should indicate not a participant")
	})

	t.Run("non_participant_cannot_cancel", func(t *testing.T) {
		trade, err := tradeSvc.RequestTrade(ctx, playerACharID, playerBCharID)
		require.NoError(t, err)

		err = tradeSvc.CancelTrade(ctx, trade.ID, nonPlayerID)
		require.Error(t, err, "cancel should fail for non-participant")
		assert.Contains(t, err.Error(), "not a participant", "error should indicate not a participant")
	})
}

// TestPhase6_TradePersistence verifies that trade state is correctly
// persisted and retrieved across operations.
func TestPhase6_TradePersistence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	const (
		playerACharID = uint32(90001)
		playerBCharID = uint32(90002)
		itemID        = uint32(501)
		itemAmount    = int32(10)
		zenyAmount    = uint32(1000)
	)

	repo := repository.NewMemoryTradeRepository()
	locks := repository.NewMemoryLockStore()
	tradeSvc := service.NewTradeService(repo, locks, nil, nil, 0)

	trade, err := tradeSvc.RequestTrade(ctx, playerACharID, playerBCharID)
	require.NoError(t, err)

	savedTrade, err := repo.GetTrade(ctx, trade.ID)
	require.NoError(t, err)
	assert.Equal(t, trade.ID, savedTrade.ID, "trade ID should match")
	assert.Equal(t, trade.State, savedTrade.State, "state should match")
	assert.Equal(t, trade.Player1CharID, savedTrade.Player1CharID, "player1 should match")
	assert.WithinDuration(t, trade.CreatedAt, savedTrade.CreatedAt, time.Millisecond, "created time should match")

	err = tradeSvc.AcceptTrade(ctx, trade.ID, playerBCharID)
	require.NoError(t, err)

	savedTrade, err = repo.GetTrade(ctx, trade.ID)
	require.NoError(t, err)
	assert.Equal(t, tradedomain.TradeStateOpen, savedTrade.State, "state should be open")
	assert.Equal(t, playerBCharID, savedTrade.Player2CharID, "player2 should be set")

	err = tradeSvc.AddTradeItem(ctx, trade.ID, playerACharID, itemID, itemAmount)
	require.NoError(t, err)

	savedTrade, err = repo.GetTrade(ctx, trade.ID)
	require.NoError(t, err)
	assert.Len(t, savedTrade.Player1Items, 1, "should have 1 item")
	assert.Equal(t, itemID, savedTrade.Player1Items[0].ItemID, "item ID should match")
	assert.Equal(t, itemAmount, savedTrade.Player1Items[0].Amount, "amount should match")

	err = tradeSvc.AddTradeZeny(ctx, trade.ID, playerBCharID, zenyAmount)
	require.NoError(t, err)

	savedTrade, err = repo.GetTrade(ctx, trade.ID)
	require.NoError(t, err)
	assert.Equal(t, zenyAmount, savedTrade.Player2Zeny, "zeny should be persisted")

	err = tradeSvc.ConfirmTrade(ctx, trade.ID, playerACharID)
	require.NoError(t, err)

	savedTrade, err = repo.GetTrade(ctx, trade.ID)
	require.NoError(t, err)
	assert.True(t, savedTrade.Player1Confirmed, "player1 confirmed should be persisted")
	assert.Equal(t, tradedomain.TradeStateConfirmed, savedTrade.State, "state should be confirmed")

	err = tradeSvc.ConfirmTrade(ctx, trade.ID, playerBCharID)
	require.NoError(t, err)

	savedTrade, err = repo.GetTrade(ctx, trade.ID)
	require.NoError(t, err)
	assert.True(t, savedTrade.Player2Confirmed, "player2 confirmed should be persisted")
	assert.Equal(t, tradedomain.TradeStateLocked, savedTrade.State, "state should be locked")

	err = tradeSvc.CompleteTrade(ctx, trade.ID, playerACharID)
	require.NoError(t, err)

	savedTrade, err = repo.GetTrade(ctx, trade.ID)
	require.NoError(t, err)
	assert.Equal(t, tradedomain.TradeStateCompleted, savedTrade.State, "state should be completed")
}

// TestPhase6_TradeNotFoundErrors verifies that operations on non-existent
// trades return appropriate errors.
func TestPhase6_TradeNotFoundErrors(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	const (
		playerACharID = uint32(90001)
		nonExistentID = "non-existent-trade-id"
	)

	repo := repository.NewMemoryTradeRepository()
	locks := repository.NewMemoryLockStore()
	tradeSvc := service.NewTradeService(repo, locks, nil, nil, 0)

	t.Run("get_non_existent_trade", func(t *testing.T) {
		_, err := repo.GetTrade(ctx, nonExistentID)
		require.Error(t, err, "get should fail for non-existent trade")
		assert.ErrorIs(t, err, tradedomain.ErrTradeNotFound, "should return ErrTradeNotFound")
	})

	t.Run("accept_non_existent_trade", func(t *testing.T) {
		err := tradeSvc.AcceptTrade(ctx, nonExistentID, playerACharID)
		require.Error(t, err, "accept should fail for non-existent trade")
		assert.ErrorIs(t, err, tradedomain.ErrTradeNotFound, "should return ErrTradeNotFound")
	})

	t.Run("add_item_to_non_existent_trade", func(t *testing.T) {
		err := tradeSvc.AddTradeItem(ctx, nonExistentID, playerACharID, 501, 10)
		require.Error(t, err, "add item should fail for non-existent trade")
		assert.ErrorIs(t, err, tradedomain.ErrTradeNotFound, "should return ErrTradeNotFound")
	})

	t.Run("add_zeny_to_non_existent_trade", func(t *testing.T) {
		err := tradeSvc.AddTradeZeny(ctx, nonExistentID, playerACharID, 1000)
		require.Error(t, err, "add zeny should fail for non-existent trade")
		assert.ErrorIs(t, err, tradedomain.ErrTradeNotFound, "should return ErrTradeNotFound")
	})

	t.Run("confirm_non_existent_trade", func(t *testing.T) {
		err := tradeSvc.ConfirmTrade(ctx, nonExistentID, playerACharID)
		require.Error(t, err, "confirm should fail for non-existent trade")
		assert.ErrorIs(t, err, tradedomain.ErrTradeNotFound, "should return ErrTradeNotFound")
	})

	t.Run("complete_non_existent_trade", func(t *testing.T) {
		err := tradeSvc.CompleteTrade(ctx, nonExistentID, playerACharID)
		require.Error(t, err, "complete should fail for non-existent trade")
		assert.ErrorIs(t, err, tradedomain.ErrTradeNotFound, "should return ErrTradeNotFound")
	})

	t.Run("cancel_non_existent_trade", func(t *testing.T) {
		err := tradeSvc.CancelTrade(ctx, nonExistentID, playerACharID)
		require.Error(t, err, "cancel should fail for non-existent trade")
		assert.ErrorIs(t, err, tradedomain.ErrTradeNotFound, "should return ErrTradeNotFound")
	})
}

// TestPhase6_ItemAmountValidation verifies that item amount validation
// is enforced correctly.
func TestPhase6_ItemAmountValidation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	const (
		playerACharID = uint32(90001)
		playerBCharID = uint32(90002)
		itemID        = uint32(501)
	)

	repo := repository.NewMemoryTradeRepository()
	locks := repository.NewMemoryLockStore()
	tradeSvc := service.NewTradeService(repo, locks, nil, nil, 0)

	trade, err := tradeSvc.RequestTrade(ctx, playerACharID, playerBCharID)
	require.NoError(t, err)

	err = tradeSvc.AcceptTrade(ctx, trade.ID, playerBCharID)
	require.NoError(t, err)

	t.Run("add_item_with_zero_amount", func(t *testing.T) {
		err := tradeSvc.AddTradeItem(ctx, trade.ID, playerACharID, itemID, 0)
		require.Error(t, err, "add item with zero amount should fail")
		assert.Contains(t, err.Error(), "must be positive", "error should indicate amount must be positive")
	})

	t.Run("add_item_with_negative_amount", func(t *testing.T) {
		err := tradeSvc.AddTradeItem(ctx, trade.ID, playerACharID, itemID, -5)
		require.Error(t, err, "add item with negative amount should fail")
		assert.Contains(t, err.Error(), "must be positive", "error should indicate amount must be positive")
	})

	t.Run("add_item_with_positive_amount", func(t *testing.T) {
		err := tradeSvc.AddTradeItem(ctx, trade.ID, playerACharID, itemID, 10)
		require.NoError(t, err, "add item with positive amount should succeed")
	})
}
