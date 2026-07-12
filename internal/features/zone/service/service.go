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

	zonev1 "github.com/bouroo/goAthena/api/pb/zone/v1"
	"github.com/bouroo/goAthena/internal/features/zone/domain"
	"github.com/bouroo/goAthena/internal/infrastructure/agones"
	"github.com/bouroo/goAthena/pkg/ro/aoi"
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

// PickupItem processes a ground-item pickup request. Validates the item exists, is in range, and is owned by the requesting player. Removes it from the ground registry and publishes ItemPickedUp event.
func (s *ZoneService) PickupItem(ctx context.Context, groundItemID domain.EntityID, playerID domain.EntityID) (*domain.PickupResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("zone: pickup item: %w", err)
	}

	const attackRange = 3

	var itemID uint32
	var itemAmount int32
	itemX, itemY := 0, 0
	playerX, playerY := 0, 0

	// Read phase: lookup item and player, validate distance
	s.tickLoop.mu.RLock()
	item, itemExists := s.tickLoop.entities[groundItemID]
	if !itemExists {
		s.tickLoop.mu.RUnlock()
		return nil, fmt.Errorf("%w: ground item id=%d", ErrEntityMissing, groundItemID)
	}
	player, playerExists := s.tickLoop.entities[playerID]
	if !playerExists {
		s.tickLoop.mu.RUnlock()
		return nil, fmt.Errorf("%w: player id=%d", ErrEntityMissing, playerID)
	}
	itemX, itemY = item.X, item.Y
	playerX, playerY = player.X, player.Y
	itemID = item.ItemID
	itemAmount = item.ItemAmount
	s.tickLoop.mu.RUnlock()

	dx := itemX - playerX
	dy := itemY - playerY
	distance := max(abs(dx), abs(dy))
	if distance > attackRange {
		return nil, fmt.Errorf("%w: item too far (distance=%d, max=%d)", ErrEntityMissing, distance, attackRange)
	}

	if item.Owner != 0 && item.Owner != playerID {
		return nil, fmt.Errorf("%w: item id=%d, owner=%d, player=%d", ErrEntityMissing, groundItemID, item.Owner, playerID)
	}

	// Write phase: remove item from entities map and AOI grid
	s.tickLoop.mu.Lock()
	_, itemExists = s.tickLoop.entities[groundItemID]
	if !itemExists {
		s.tickLoop.mu.Unlock()
		return nil, fmt.Errorf("%w: ground item id=%d", ErrEntityMissing, groundItemID)
	}
	delete(s.tickLoop.entities, groundItemID)
	if err := s.tickLoop.grid.RemoveEntity(aoi.EntityID(groundItemID)); err != nil {
		s.tickLoop.logger.Warn().Err(err).Uint32("entity_id", uint32(groundItemID)).Msg("zone: AOI remove failed")
	}
	s.tickLoop.mu.Unlock()

	s.tickLoop.publish(ctx, &zonev1.ZoneEvent{
		Event: &zonev1.ZoneEvent_PickedUp{
			PickedUp: &zonev1.ItemPickedUp{
				GroundItemId: uint32(groundItemID),
				ItemId:       itemID,
				Amount:       itemAmount,
				PlayerId:     uint32(playerID),
			},
		},
	}, "pickup")

	resp := &domain.PickupResponse{
		Success: true,
		ItemID:  itemID,
		Amount:  itemAmount,
	}
	return resp, nil
}

// DamageEntity applies damage to an entity and returns the damage response.
func (s *ZoneService) DamageEntity(ctx context.Context, entityID domain.EntityID, damage int32, attackerID domain.EntityID, skillID, skillLevel uint32) (*domain.DamageResponse, error) {
	if damage <= 0 {
		return &domain.DamageResponse{
			Success:       false,
			TargetDied:    false,
			DamageApplied: 0,
			CurrentHP:     0,
			MaxHP:         0,
		}, nil
	}

	s.tickLoop.mu.RLock()
	e, ok := s.tickLoop.entities[entityID]
	if !ok {
		s.tickLoop.mu.RUnlock()
		return nil, fmt.Errorf("%w: entity id=%d", ErrEntityMissing, entityID)
	}

	if e.Type != domain.EntityMob && e.Type != domain.EntityPlayer {
		s.tickLoop.mu.RUnlock()
		return nil, fmt.Errorf("zone: invalid entity type for damage: %v", e.Type)
	}

	currentHP := e.HP
	maxHP := e.MaxHP
	s.tickLoop.mu.RUnlock()

	if currentHP <= 0 {
		return &domain.DamageResponse{
			Success:       true,
			TargetDied:    false,
			DamageApplied: 0,
			CurrentHP:     0,
			MaxHP:         maxHP,
		}, nil
	}

	appliedDamage := min(damage, currentHP)

	newHP := currentHP - appliedDamage
	targetDied := newHP <= 0

	s.tickLoop.mu.Lock()
	if e, ok := s.tickLoop.entities[entityID]; ok {
		e.HP = newHP
	}
	s.tickLoop.mu.Unlock()

	s.tickLoop.publish(ctx, &zonev1.ZoneEvent{
		Event: &zonev1.ZoneEvent_Damaged{
			Damaged: &zonev1.EntityDamaged{
				EntityId: uint32(entityID),
				Damage:   appliedDamage,
				NewHp:    newHP,
				MaxHp:    maxHP,
			},
		},
	}, "damage")

	if targetDied {
		if err := s.KillEntity(ctx, entityID); err != nil {
			s.tickLoop.logger.Warn().Err(err).Uint32("entity_id", uint32(entityID)).Msg("zone: kill entity after damage failed")
		}
	}

	return &domain.DamageResponse{
		Success:       true,
		TargetDied:    targetDied,
		DamageApplied: appliedDamage,
		CurrentHP:     newHP,
		MaxHP:         maxHP,
	}, nil
}

// KillEntity kills an entity, handles item drops, and broadcasts the death.
func (s *ZoneService) KillEntity(ctx context.Context, entityID domain.EntityID) error {
	s.tickLoop.mu.RLock()
	e, ok := s.tickLoop.entities[entityID]
	if !ok {
		s.tickLoop.mu.RUnlock()
		return fmt.Errorf("%w: entity id=%d", ErrEntityMissing, entityID)
	}

	entityType := e.Type
	mobID := e.MobID
	x, y := e.X, e.Y
	s.tickLoop.mu.RUnlock()

	s.tickLoop.publishKill(ctx, entityID)

	if entityType != domain.EntityMob || mobID <= 0 {
		return s.tickLoop.removeEntityInternal(ctx, entityID, 0)
	}

	const redPotionItemID = 501
	const groundItemIDStart = 1000000

	s.tickLoop.mu.Lock()
	nextGroundItemID := domain.EntityID(groundItemIDStart)
	for id, ent := range s.tickLoop.entities {
		if ent.Type != domain.EntityMob {
			continue
		}
		if uint32(id) >= uint32(nextGroundItemID) {
			nextGroundItemID = domain.EntityID(uint32(id) + 1)
		}
	}
	groundItemID := nextGroundItemID

	groundItem := &domain.Entity{
		ID:         groundItemID,
		Type:       domain.EntityMob,
		X:          x,
		Y:          y,
		ItemID:     redPotionItemID,
		ItemAmount: 1,
		Owner:      0,
	}

	aoiEnt := &aoi.Entity{
		ID:   aoi.EntityID(groundItemID),
		Type: toAOIType(groundItem.Type),
		X:    groundItem.X,
		Y:    groundItem.Y,
	}

	if err := s.tickLoop.grid.AddEntity(aoiEnt); err != nil {
		s.tickLoop.mu.Unlock()
		s.tickLoop.logger.Warn().Err(err).Uint32("ground_item_id", uint32(groundItemID)).Msg("zone: failed to add ground item to AOI")
		return s.tickLoop.removeEntityInternal(ctx, entityID, 0)
	}

	s.tickLoop.entities[groundItemID] = groundItem
	s.tickLoop.mu.Unlock()

	s.tickLoop.publish(ctx, &zonev1.ZoneEvent{
		Event: &zonev1.ZoneEvent_Spawned{
			Spawned: &zonev1.EntitySpawned{
				EntityId:   uint32(groundItemID),    //nolint:gosec // G115: entity ID is safe to convert
				EntityType: uint32(groundItem.Type), //nolint:gosec // G115: entity type is safe to convert
				X:          uint32(groundItem.X),    //nolint:gosec // G115: X coordinate is safe to convert
				Y:          uint32(groundItem.Y),    //nolint:gosec // G115: Y coordinate is safe to convert
				Name:       "",
			},
		},
	}, "spawn")

	s.tickLoop.publish(ctx, &zonev1.ZoneEvent{
		Event: &zonev1.ZoneEvent_Dropped{
			Dropped: &zonev1.ItemDropped{
				GroundItemId: uint32(groundItemID),
				ItemId:       groundItem.ItemID,
				Amount:       groundItem.ItemAmount,
				X:            uint32(x), //nolint:gosec // G115: coordinate is safe to convert
				Y:            uint32(y), //nolint:gosec // G115: coordinate is safe to convert
				MapName:      s.tickLoop.mapData.Name,
			},
		},
	}, "drop")

	if err := s.tickLoop.removeEntityInternal(ctx, entityID, 0); err != nil {
		s.tickLoop.logger.Warn().Err(err).Uint32("entity_id", uint32(entityID)).Msg("zone: remove entity after kill failed")
	}

	return nil
}
