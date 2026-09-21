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

// SaveState stores the char's hp/sp plus the accumulated base_exp/job_exp,
// job_level, skill_point, and status_point so the memory repo (used by unit
// tests) reflects a persisted state write the same way the GORM repo does.
func (r *MemoryWorldRepository) SaveState(_ context.Context, charID uint32, baseLevel int16, jobLevel int16, maxHP, maxSP, hp, sp int32, baseExp, jobExp uint64, statusPoint, skillPoint uint32) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := domain.EntityID(charID)
	e, ok := r.data[id]
	if !ok {
		return domain.ErrEntityNotFound
	}
	e.Level = baseLevel
	e.JobLevel = jobLevel
	e.MaxHP = maxHP
	e.MaxSP = maxSP
	e.HP = hp
	e.SP = sp
	e.BaseExp = baseExp
	e.JobExp = jobExp
	e.StatusPoint = statusPoint
	e.SkillPoint = skillPoint
	r.data[id] = e
	return nil
}

// LoadSkills returns every learned skill for charID from the in-memory map.
func (r *MemoryWorldRepository) LoadSkills(_ context.Context, charID uint32) ([]domain.LearnedSkill, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.data[domain.EntityID(charID)]
	if !ok {
		return nil, domain.ErrEntityNotFound
	}
	if len(e.LearnedSkills) == 0 {
		return nil, nil
	}
	skills := make([]domain.LearnedSkill, 0, len(e.LearnedSkills))
	for sid, lvl := range e.LearnedSkills {
		skills = append(skills, domain.LearnedSkill{SkillID: sid, Level: lvl})
	}
	return skills, nil
}

// SaveSkills replaces the char's learned-skills map in the in-memory store.
func (r *MemoryWorldRepository) SaveSkills(_ context.Context, charID uint32, skills []domain.LearnedSkill) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := domain.EntityID(charID)
	e, ok := r.data[id]
	if !ok {
		return domain.ErrEntityNotFound
	}
	if len(skills) == 0 {
		e.LearnedSkills = nil
	} else {
		if e.LearnedSkills == nil {
			e.LearnedSkills = make(map[int32]int16, len(skills))
		} else {
			// Replace: remove all then insert new.
			for k := range e.LearnedSkills {
				delete(e.LearnedSkills, k)
			}
		}
		for _, s := range skills {
			e.LearnedSkills[s.SkillID] = s.Level
		}
	}
	r.data[id] = e
	return nil
}
