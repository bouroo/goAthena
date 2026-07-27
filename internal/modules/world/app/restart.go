// Package app: this file adds the M7 CZ_RESTART (0x00b2) handler — the packet a
// client sends to leave the world, either to respawn at its save point (type 0)
// or to return to the character-select screen (type 1).
//
// Only the char-select path (type 1) is wired in this combat slice: the player
// cannot die yet (mobs die, PCs do not), so the respawn button never appears and
// type 0 is unreachable. The char-select path sends ZC_RESTART_ACK(type=1), tears
// down the player's world membership, and closes the map connection — the client
// then reconnects to the char listener and re-enters CH_ENTER. rAthena achieves
// this via an async map↔char 0x2b02/0x2b03 round-trip; the monolith collapses it
// to an in-process call.
//
// Identity is resolved from the verified conn auth cache (conn.Auth().AccountID),
// never the packet — CZ_RESTART carries no account id, and even a client-
// controlled id would be ignored. The teardown mirrors SpawnService.dropNeighbor /
// SocialService.dropPlayer (registry unregister + idempotent AOI removal).
package app

import (
	"context"
	"errors"
	"fmt"

	gwdomain "github.com/bouroo/goAthena/internal/modules/gateway/domain"
	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/aoi"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// RestartService owns the CZ_RESTART use case. It holds only the collaborators
// the char-select teardown crosses — the live-PC registry (to drop the leaving
// player) and the map store (to remove its AOI entity) — and nothing else.
type RestartService struct {
	registry *domain.PlayerRegistry
	maps     domain.MapStore
}

// NewRestartService binds the char-select-return collaborators. The registry and
// map store are the same singletons SpawnService/SocialService share.
func NewRestartService(registry *domain.PlayerRegistry, maps domain.MapStore) *RestartService {
	return &RestartService{registry: registry, maps: maps}
}

// ReturnToCharSelect serves CZ_RESTART type 1 (return to character select). It
// resolves every precondition before mutating state so a failure leaves the world
// consistent:
//
//  1. Look up the live player. A missing player is a late or duplicate restart
//     (already torn down); the ack is still sent and the caller closes the conn
//     so the client leaves cleanly. There is nothing to tear down.
//  2. Load the player's map (propagated on failure — the map loaded at
//     enter-world, so a failure here is corrupt state worth surfacing, and the
//     client simply stays put and may retry, exactly like the change-dir path).
//  3. Send ZC_RESTART_ACK(type=1). A write failure aborts before any teardown —
//     the socket is dying and the disconnect path owns the rest.
//  4. Unregister the player (the critical step: PlayerRegistry.Register rejects a
//     duplicate account, so a player that char-select-returns and re-enters would
//     hit ErrPlayerAlreadyRegistered unless this runs). Unregister cannot fail.
//  5. Remove the player's AOI entity. ErrEntityMissing is benign (a concurrent
//     teardown already removed it) and ignored, matching dropPlayer/dropNeighbor.
//
// The caller (RestartHandler) closes the map connection after this returns nil;
// this method never closes the conn, keeping transport teardown out of the use
// case.
//
// PENDING: a neighbor observing the departure sees no ZC_NOTIFY_VANISH — vanish-
// on-leave is deferred to the holistic disconnect-cleanup path (today no
// disconnect, char-select or TCP-drop, broadcasts it). Removing the AOI entity
// ensures new entrants never see the departed player; existing observers' stale
// sprites clear on their next AOI refresh.
func (s *RestartService) ReturnToCharSelect(ctx context.Context, conn gwdomain.Conn, accountID uint32) error {
	player, ok := s.registry.ByAccount(accountID)

	var mp *domain.Map
	if ok {
		loaded, err := s.maps.Load(ctx, player.MapName)
		if err != nil {
			return fmt.Errorf("restart: load map %q: %w", player.MapName, err)
		}
		mp = loaded
	}

	if err := (packet.RestartAckResponse{Type: 1}).Encode(connWriter{conn}); err != nil {
		return fmt.Errorf("restart: encode ZC_RESTART_ACK: %w", err)
	}

	if !ok {
		return nil // late/duplicate restart: ack sent, nothing to tear down
	}

	s.registry.Unregister(accountID)
	if err := mp.AOI.RemoveEntity(player.EntityID); err != nil && !errors.Is(err, aoi.ErrEntityMissing) {
		// Only ErrEntityMissing is expected here (a concurrent teardown beat us);
		// any other failure is a corrupt AOI state worth surfacing even though
		// the registry is already clean.
		return fmt.Errorf("restart: remove AOI entity for account %d: %w", accountID, err)
	}
	return nil
}

// RestartHandler adapts CZ_RESTART to the gateway dispatch shape.
type RestartHandler struct {
	svc *RestartService
}

// NewRestartHandler builds a CZ_RESTART handler over the RestartService.
func NewRestartHandler(svc *RestartService) *RestartHandler {
	return &RestartHandler{svc: svc}
}

// Handle implements gateway/domain.PacketHandler for CZ_RESTART. accountID is
// sourced from conn.Auth().AccountID — the impersonation guard. On the
// char-select path it runs the use case and then closes the map connection so the
// client reconnects to the char listener; respawn (type 0) is a documented no-op
// pending the death/savepoint system; an unknown type is surfaced as a handler
// error (logged by ProcessBytes, session continues).
func (h *RestartHandler) Handle(ctx context.Context, conn gwdomain.Conn, frame gwdomain.Frame) error {
	req, err := packet.ParseCZRestart(frame.Raw)
	if err != nil {
		return fmt.Errorf("parse CZ_RESTART: %w", err)
	}
	accountID := conn.Auth().AccountID
	if accountID == 0 {
		return errors.New("restart: connection has no verified account (CZ_ENTER not completed)")
	}

	switch req.Type {
	case 0:
		// Respawn: deferred to the death/savepoint system. Unreachable in this
		// slice (PCs cannot die), so a no-op keeps the client on its current
		// cell without error.
		return nil
	case 1:
		if err := h.svc.ReturnToCharSelect(ctx, conn, accountID); err != nil {
			return err
		}
		// Drop the map connection; the client reconnects to the char listener and
		// re-enters CH_ENTER. Propagate (wrapped) any close failure so the gateway
		// logs it rather than masks it.
		if err := conn.Close(); err != nil {
			return fmt.Errorf("restart: close map conn after char-select return: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("restart: unknown CZ_RESTART type %d", req.Type)
	}
}

// Compile-time check that RestartHandler satisfies the gateway handler shape.
var _ gwdomain.PacketHandler = (*RestartHandler)(nil).Handle
