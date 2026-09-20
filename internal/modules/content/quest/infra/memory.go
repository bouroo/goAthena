package infra

import (
	"context"
	"sync"

	"github.com/bouroo/goAthena/internal/modules/content/quest/domain"
)

// MemoryQuestRepository is an in-memory quest store for unit tests. Same
// contract as GORMQuestRepository: upsert-by-key on Set, zero on miss.
type MemoryQuestRepository struct {
	mu   sync.RWMutex
	rows map[questKey]domain.QuestVar
}

type questKey struct {
	charID  uint32
	npcName string
	varName string
}

// NewMemoryQuestRepository creates an empty in-memory repo.
func NewMemoryQuestRepository() *MemoryQuestRepository {
	return &MemoryQuestRepository{rows: make(map[questKey]domain.QuestVar)}
}

// Get returns the row at the key, or false.
func (r *MemoryQuestRepository) Get(_ context.Context, charID uint32, npcName, varName string) (domain.QuestVar, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	row, ok := r.rows[questKey{charID, npcName, varName}]
	return row, ok, nil
}

// Set upserts the row.
func (r *MemoryQuestRepository) Set(_ context.Context, charID uint32, npcName, varName, value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rows[questKey{charID, npcName, varName}] = domain.QuestVar{
		CharID:  charID,
		NpcName: npcName,
		VarName: varName,
		Value:   value,
	}
	return nil
}

// ListByChar returns every row owned by charID. The order is map-iteration
// order — non-deterministic, but acceptable for admin/diagnostic tooling.
func (r *MemoryQuestRepository) ListByChar(_ context.Context, charID uint32) ([]domain.QuestVar, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]domain.QuestVar, 0, len(r.rows))
	for k, v := range r.rows {
		if k.charID == charID {
			out = append(out, v)
		}
	}
	return out, nil
}
