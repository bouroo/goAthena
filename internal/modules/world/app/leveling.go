package app

import (
	"context"
	"log/slog"

	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/jobbasepoints"
	"github.com/bouroo/goAthena/pkg/ro/jobexp"
)

// LevelingService handles the conversion of accumulated EXP into base levels.
type LevelingService struct {
	curve    *jobexp.Registry
	jobStats *jobbasepoints.Registry
	world    *WorldService
	log      *slog.Logger
}

// NewLevelingService builds the EXP→level converter. A nil curve or stats
// registry (the loader fallback when job_exp.yml/job_basepoints.yml are
// unreadable) leaves leveling disabled: EXP still accrues, thresholds are
// never consumed.
func NewLevelingService(world *WorldService, curve *jobexp.Registry, stats *jobbasepoints.Registry, log *slog.Logger) *LevelingService {
	return &LevelingService{
		world:    world,
		curve:    curve,
		jobStats: stats,
		log:      log,
	}
}

// CheckLevelUp is called after EXP accrual to determine if the entity levels up.
// It consumes EXP and updates vitals. Returns the number of levels gained.
func (s *LevelingService) CheckLevelUp(_ context.Context, charID uint32) (int, error) {
	if s.curve == nil || s.jobStats == nil {
		return 0, nil // Leveling disabled (backward compat)
	}

	s.world.mu.Lock()
	e, ok := s.world.entities[domain.EntityID(charID)]
	if !ok {
		s.world.mu.Unlock()
		return 0, domain.ErrEntityNotFound
	}

	// We only support "Novice" for now (Class=0).
	jobName := "Novice"
	if e.Job != 0 {
		// For non-Novices, we accrue EXP but don't level up yet (Deferred).
		s.world.mu.Unlock()
		return 0, nil
	}

	levelsGained := 0
	for {
		nextExp, ok := s.curve.NextBaseExp(jobName, int(e.Level))
		if !ok {
			break // Max level or unknown job/level
		}

		if e.BaseExp < nextExp {
			break
		}

		// Level up!
		e.BaseExp -= nextExp
		e.Level++
		levelsGained++

		// Recalc MaxHP/MaxSP from jobbasepoints table for the new level.
		// Pre-re convention: a level-up restores the char to full HP/SP.
		maxHP := s.jobStats.BaseHpForLevel(jobName, int(e.Level))
		maxSP := s.jobStats.BaseSpForLevel(jobName, int(e.Level))

		if maxHP > 0 {
			e.MaxHP = int32(maxHP) //nolint:gosec // G115: job_basepoints HP rows are bounded game stats.
			e.HP = int32(maxHP)    //nolint:gosec // G115: full heal on level-up, bounded by the same row.
		}
		if maxSP > 0 {
			e.MaxSP = int32(maxSP) //nolint:gosec // G115: job_basepoints SP rows are bounded game stats.
			e.SP = int32(maxSP)    //nolint:gosec // G115: full heal on level-up, bounded by the same row.
		}
	}

	// Snapshot for notification.
	var (
		newLevel = e.Level
		newMaxHP = e.MaxHP
		newMaxSP = e.MaxSP
	)
	s.world.mu.Unlock()

	if levelsGained > 0 {
		if s.world.OnLevelUp != nil {
			s.world.OnLevelUp(charID, newLevel, newMaxHP, newMaxSP)
		}
	}

	return levelsGained, nil
}
