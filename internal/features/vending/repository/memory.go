package repository

import (
	"context"
	"sync"
	"time"

	"github.com/bouroo/goAthena/internal/features/vending/domain"
)

// memoryVendingRepository is an in-memory implementation of domain.VendingRepository.
// It is safe for concurrent use and intended for unit tests and dev runs.
type memoryVendingRepository struct {
	mu    sync.RWMutex
	shops map[string]domain.VendingShop // shopID → shop
}

// NewMemoryVendingRepository returns a thread-safe in-memory vending repository.
func NewMemoryVendingRepository() domain.VendingRepository {
	return &memoryVendingRepository{
		shops: make(map[string]domain.VendingShop),
	}
}

// CreateShop stores a new vending shop. The shop.ID is preserved if non-empty,
// otherwise a new UUID-based ID is assigned by the service layer before calling.
func (r *memoryVendingRepository) CreateShop(_ context.Context, shop domain.VendingShop) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.shops[shop.ID] = shop
	return shop.ID, nil
}

// GetShop retrieves a shop by its ID.
func (r *memoryVendingRepository) GetShop(_ context.Context, shopID string) (domain.VendingShop, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	shop, ok := r.shops[shopID]
	if !ok {
		return domain.VendingShop{}, domain.ErrShopNotFound
	}
	return shop, nil
}

// GetShopByOwner retrieves the shop owned by the given character.
func (r *memoryVendingRepository) GetShopByOwner(_ context.Context, ownerID uint32) (domain.VendingShop, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, shop := range r.shops {
		if shop.OwnerID == ownerID {
			return shop, nil
		}
	}
	return domain.VendingShop{}, domain.ErrShopNotFound
}

// ListShopsOnMap returns all open shops on the given map.
func (r *memoryVendingRepository) ListShopsOnMap(_ context.Context, mapName string) ([]domain.VendingShop, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []domain.VendingShop
	for _, shop := range r.shops {
		if shop.MapName == mapName {
			result = append(result, shop)
		}
	}
	return result, nil
}

// UpdateShop persists changes to a shop.
func (r *memoryVendingRepository) UpdateShop(_ context.Context, shop domain.VendingShop) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.shops[shop.ID]; !ok {
		return domain.ErrShopNotFound
	}
	r.shops[shop.ID] = shop
	return nil
}

// DeleteShop removes a shop from the active registry.
func (r *memoryVendingRepository) DeleteShop(_ context.Context, shopID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.shops, shopID)
	return nil
}

// memoryLockStore is a simple in-memory lock implementation for dev/testing.
type memoryLockStore struct {
	mu    sync.Mutex
	locks map[string]string // key → token
}

// NewMemoryLockStore returns a thread-safe in-memory LockStore.
func NewMemoryLockStore() domain.LockStore {
	return &memoryLockStore{
		locks: make(map[string]string),
	}
}

// Acquire attempts to take the lock. Returns ErrLockBusy if already held.
func (m *memoryLockStore) Acquire(_ context.Context, key string, _ time.Duration) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.locks[key]; ok {
		return "", domain.ErrLockBusy
	}
	token := key + ":token"
	m.locks[key] = token
	return token, nil
}

// Release frees the lock if the token matches.
func (m *memoryLockStore) Release(_ context.Context, key, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if current, ok := m.locks[key]; ok && current == token {
		delete(m.locks, key)
	}
	return nil
}
