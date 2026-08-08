// Package app implements the transit bounded context: cross-map movement. A
// warp (script warp, portal walk) persists the char's destination position,
// removes the entity from its current map's AOI. The caller then sends
// ZC_NPC_ACK_MAPMOVE so the client reconnects and re-enters via CZ_ENTER, at
// which point WorldService.EnterMap loads the updated position and re-inserts
// the PC into the new map's AOI.
package app

import (
	"context"
	"fmt"

	chardomain "github.com/bouroo/goAthena/internal/modules/character/domain"
	worldapp "github.com/bouroo/goAthena/internal/modules/world/app"
	worlddomain "github.com/bouroo/goAthena/internal/modules/world/domain"
)

// TransitService handles cross-map transfers.
type TransitService struct {
	world *worldapp.WorldService
	repos chardomain.CharacterRepository
}

// NewTransitService builds a TransitService.
func NewTransitService(world *worldapp.WorldService, repos chardomain.CharacterRepository) *TransitService {
	return &TransitService{world: world, repos: repos}
}

// Warp moves a player to a new map tile: removes the entity from the current
// map's AOI and persists the destination map+position. The caller sends
// MapMoveResponse so the client reconnects; EnterMap then loads the new position.
func (s *TransitService) Warp(ctx context.Context, charID uint32, mapName string, x, y int16) error {
	pos := worlddomain.Position{X: x, Y: y}
	if err := s.world.SetPosition(ctx, charID, mapName, pos); err != nil {
		return fmt.Errorf("transit persist position: %w", err)
	}
	// Remove from the old map's AOI. A not-found is benign (already left).
	if err := s.world.LeaveMap(ctx, charID); err != nil {
		_ = err //nolint:errcheck // best-effort; the reconnect re-inserts cleanly
	}
	return nil
}
