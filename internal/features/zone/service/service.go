// Package service implements the zone service use-case layer: entity
// registration, A* path requests, AOI queries, and the Agones lifecycle
// integration driven by player count.
package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/bouroo/goAthena/internal/features/zone/domain"
	"github.com/bouroo/goAthena/internal/infrastructure/agones"
)

// ErrEntityExists is returned by AddEntity when the ID is already tracked.
var ErrEntityExists = errors.New("zone: entity already exists")

// ErrEntityMissing is returned when an entity ID is unknown.
var ErrEntityMissing = errors.New("zone: entity not found")

// ErrInvalidCoordinates is returned when coordinates fall outside the map.
var ErrInvalidCoordinates = errors.New("zone: coordinates out of bounds")

// ErrDestinationNotWalkable is returned when the target cell is a wall.
var ErrDestinationNotWalkable = errors.New("zone: destination cell is not walkable")

// ShutdownChannel exposes the lifecycle hook so the zone app composition
// root can wait on the service-driven Agones shutdown signal. The channel
// is closed when the service schedules a shutdown (last player removed).
type ShutdownChannel interface {
	// Done returns a channel that is closed when the service schedules a
	// shutdown. The composition root uses this to call agones.Shutdown.
	Done() <-chan struct{}
}

// ZoneService is the zone use-case implementation. It owns the TickLoop
// (the physics thread) and brokers all inbound calls to it under the
// appropriate locks.
type ZoneService struct {
	tickLoop      *TickLoop
	agones        agones.Lifecycle
	defaultSpeed  int
	shutdownGrace int // milliseconds; 0 = immediate

	mu       sync.Mutex
	shutdown bool
	done     chan struct{}
}

// NewZoneService wires the zone use-case. The TickLoop must already be
// running (composition root calls TickLoop.Start in a goroutine). The
// agones lifecycle may be a no-op Local in dev/CI.
func NewZoneService(
	tl *TickLoop,
	ag agones.Lifecycle,
	defaultSpeed int,
	shutdownGrace int,
	logger *zerolog.Logger,
) *ZoneService {
	if tl == nil {
		panic(errors.New("zone: tick loop is required"))
	}
	if ag == nil {
		panic(errors.New("zone: agones lifecycle is required"))
	}
	if logger == nil {
		panic(errors.New("zone: logger is required"))
	}
	if defaultSpeed <= 0 {
		defaultSpeed = 150
	}
	if shutdownGrace < 0 {
		shutdownGrace = 0
	}

	s := &ZoneService{
		tickLoop:      tl,
		agones:        ag,
		defaultSpeed:  defaultSpeed,
		shutdownGrace: shutdownGrace,
		done:          make(chan struct{}),
	}
	tl.setOnEmpty(s.scheduleShutdown)
	return s
}

// AddEntity registers e in the tick loop and AOI grid. The first player
// added triggers an Agones Allocate.
func (s *ZoneService) AddEntity(ctx context.Context, e *domain.Entity) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("zone: add entity: %w", err)
	}
	if e == nil {
		return fmt.Errorf("zone: add entity: nil entity")
	}
	if e.MoveSpeed <= 0 {
		e.MoveSpeed = s.defaultSpeed
	}

	isFirst, err := s.tickLoop.addEntity(ctx, e)
	if err != nil {
		return fmt.Errorf("zone: add entity %d: %w", e.ID, err)
	}

	if isFirst && e.Type == domain.EntityPlayer {
		if err := s.agones.Allocate(ctx); err != nil {
			// Allocation failure is non-fatal: the entity is still tracked.
			// The next allocation attempt will be a no-op (idempotent).
			s.tickLoop.logger.Warn().Err(err).
				Uint32("entity_id", uint32(e.ID)).
				Msg("zone: agones allocate failed")
		}
	}

	return nil
}

// RemoveEntity unregisters the entity and, if no players remain,
// schedules an Agones Shutdown after the configured grace period.
func (s *ZoneService) RemoveEntity(ctx context.Context, id domain.EntityID) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("zone: remove entity: %w", err)
	}
	if err := s.tickLoop.removeEntity(ctx, id); err != nil {
		return fmt.Errorf("zone: remove entity %d: %w", id, err)
	}
	return nil
}

// MoveEntity sets a movement target for the entity. Computes an A* path
// and stores it on the entity; the tick loop will consume it cell by cell.
func (s *ZoneService) MoveEntity(ctx context.Context, id domain.EntityID, x, y int) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("zone: move entity: %w", err)
	}
	if err := s.tickLoop.moveEntity(ctx, id, x, y); err != nil {
		return fmt.Errorf("zone: move entity %d to (%d,%d): %w", id, x, y, err)
	}
	return nil
}

// GetVisible returns entities in the AOI viewport around the entity.
func (s *ZoneService) GetVisible(ctx context.Context, id domain.EntityID) ([]*domain.Entity, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("zone: get visible: %w", err)
	}
	out, err := s.tickLoop.getVisible(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("zone: get visible for %d: %w", id, err)
	}
	return out, nil
}

// GetEntity returns a snapshot copy of the entity.
func (s *ZoneService) GetEntity(ctx context.Context, id domain.EntityID) (*domain.Entity, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("zone: get entity: %w", err)
	}
	out, err := s.tickLoop.getEntity(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("zone: get entity %d: %w", id, err)
	}
	return out, nil
}

// EntityCount returns the number of entities in the zone.
func (s *ZoneService) EntityCount(_ context.Context) int {
	return s.tickLoop.entityCount()
}

// Done implements ShutdownChannel. The channel is closed when the
// service schedules an Agones shutdown (last player left).
func (s *ZoneService) Done() <-chan struct{} {
	return s.done
}

// scheduleShutdown is invoked by the TickLoop when the entity map
// transitions to empty AND no players remain. Idempotent: only the
// first call closes the channel and starts the grace timer.
func (s *ZoneService) scheduleShutdown() {
	s.mu.Lock()
	if s.shutdown {
		s.mu.Unlock()
		return
	}
	s.shutdown = true
	close(s.done)
	s.mu.Unlock()

	go func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if s.shutdownGrace > 0 {
			t := time.NewTimer(time.Duration(s.shutdownGrace) * time.Millisecond)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
		if err := s.agones.Shutdown(ctx); err != nil {
			s.tickLoop.logger.Warn().Err(err).Msg("zone: agones shutdown failed")
		}
	}()
}
