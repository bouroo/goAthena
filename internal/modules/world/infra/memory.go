package infra

import (
	"context"
	"sync"

	"github.com/bouroo/goAthena/internal/modules/world/domain"
)

// MemoryWorldRepository is an in-memory world repository for unit tests.
type MemoryWorldRepository struct {
	mu   sync.Mutex
	data map[domain.EntityID]domain.Entity
}

// NewMemoryWorldRepository seeds the store with enter states keyed by EntityID.
func NewMemoryWorldRepository(states ...domain.Entity) *MemoryWorldRepository {
	r := &MemoryWorldRepository{data: make(map[domain.EntityID]domain.Entity)}
	for _, e := range states {
		r.data[e.ID] = e
	}
	return r
}

// LoadEnterState returns the stored enter state for charID.
func (r *MemoryWorldRepository) LoadEnterState(_ context.Context, charID uint32) (domain.Entity, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.data[domain.EntityID(charID)]
	if !ok {
		return domain.Entity{}, domain.ErrEntityNotFound
	}
	return e, nil
}

// SetOnline updates the stored position (online flag is no-op in memory).
func (r *MemoryWorldRepository) SetOnline(_ context.Context, charID uint32, _ bool, pos domain.Position) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := domain.EntityID(charID)
	e, ok := r.data[id]
	if !ok {
		return domain.ErrEntityNotFound
	}
	e.Pos = pos
	r.data[id] = e
	return nil
}

// SetPosition persists the char's destination map + position (in-memory).
func (r *MemoryWorldRepository) SetPosition(_ context.Context, charID uint32, mapName string, pos domain.Position) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := domain.EntityID(charID)
	e, ok := r.data[id]
	if !ok {
		return domain.ErrEntityNotFound
	}
	e.Map = mapName
	e.Pos = pos
	r.data[id] = e
	return nil
}
