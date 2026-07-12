package repository

import (
	"context"
	"sync"

	"github.com/bouroo/goAthena/internal/features/trade/domain"
)

type memoryRepository struct {
	mu     sync.RWMutex
	trades map[string]domain.Trade
}

// NewMemoryTradeRepository creates an in-memory TradeRepository for testing.
func NewMemoryTradeRepository() domain.TradeRepository {
	return &memoryRepository{trades: make(map[string]domain.Trade)}
}

func (r *memoryRepository) CreateTrade(ctx context.Context, trade domain.Trade) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.trades[trade.ID] = trade
	return trade.ID, nil
}

func (r *memoryRepository) GetTrade(ctx context.Context, tradeID string) (domain.Trade, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	trade, ok := r.trades[tradeID]
	if !ok {
		return domain.Trade{}, domain.ErrTradeNotFound
	}
	return trade, nil
}

func (r *memoryRepository) UpdateTrade(ctx context.Context, trade domain.Trade) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.trades[trade.ID]; !ok {
		return domain.ErrTradeNotFound
	}

	r.trades[trade.ID] = trade
	return nil
}

func (r *memoryRepository) DeleteTrade(ctx context.Context, tradeID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.trades[tradeID]; !ok {
		return domain.ErrTradeNotFound
	}

	delete(r.trades, tradeID)
	return nil
}

func (r *memoryRepository) ExecuteTradeTransfer(ctx context.Context, trade domain.Trade) error {
	return nil
}
