package infra

import (
	"context"
	"sync"

	"github.com/bouroo/goAthena/internal/modules/world/domain"
)

// MemoryWorldRepository is an in-memory world repository for unit tests.
type MemoryWorldRepository struct {
	mu   sync.RWMutex
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
	r.mu.RLock()
	defer r.mu.RUnlock()
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

// SaveState stores the char's hp/sp plus the accumulated base_exp/job_exp so
// the memory repo (used by unit tests) reflects a persisted state write the
// same way the GORM repo does.
func (r *MemoryWorldRepository) SaveState(_ context.Context, charID uint32, baseLevel int16, maxHP, maxSP, hp, sp int32, baseExp, jobExp uint64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := domain.EntityID(charID)
	e, ok := r.data[id]
	if !ok {
		return domain.ErrEntityNotFound
	}
	e.Level = baseLevel
	e.MaxHP = maxHP
	e.MaxSP = maxSP
	e.HP = hp
	e.SP = sp
	e.BaseExp = baseExp
	e.JobExp = jobExp
	r.data[id] = e
	return nil
}
