// Package app: this file adds the M7 player-expression use cases — the three
// stateless-ish map-role handlers a live client spams after it enters the world:
//
//   - CZ_REQUEST_TIME (0x007e) → ZC_NOTIFY_TIME: the client's clock-skew probe.
//     A pure echo of the server tick; no domain state.
//   - CZ_CHANGE_DIR (0x009b) → ZC_CHANGE_DIR: persist the player's new facing and
//     broadcast it to the player and AOI neighbors (rAthena pc_setdir +
//     clif_changedir AREA).
//   - CZ_REQ_EMOTION (0x00bf) → ZC_EMOTION: broadcast the emotion icon to the
//     player and AOI neighbors (rAthena clif_emotion AREA).
//
// All three resolve identity from the verified conn auth cache (conn.Auth().
// AccountID), never the packet — none of these packets carry an account id, and
// even if one did it would be client-controlled. The broadcast + dead-neighbor
// teardown mirrors MoveService exactly (same collaborators, same dropPlayer).
package app

import (
	"context"
	"errors"
	"fmt"
	"io"

	gwdomain "github.com/bouroo/goAthena/internal/modules/gateway/domain"
	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// TimeHandler serves CZ_REQUEST_TIME (0x007e). The client sends its local tick
// and expects the server's tick back (ZC_NOTIFY_TIME, 0x007f) to estimate
// round-trip latency and correct clock skew. rAthena's clif_tick_send writes
// gettick() (monotone milliseconds); the same Clock the move worker stamps
// MoveStartTime with supplies that value here, so one clock source drives every
// time-stamped packet. The request's own ClientTick is parsed only to validate
// the frame and is not round-tripped (rAthena logs it, nothing more).
type TimeHandler struct {
	clock Clock
}

// NewTimeHandler builds a CZ_REQUEST_TIME handler over the shared server Clock.
func NewTimeHandler(clock Clock) *TimeHandler {
	return &TimeHandler{clock: clock}
}

// Handle implements gateway/domain.PacketHandler for CZ_REQUEST_TIME. Unlike the
// dir/emotion handlers it needs no verified account: it is a stateless clock
// echo with no access to player or world state, so it cannot be abused for
// identity and is harmless before CZ_ENTER completes.
func (h *TimeHandler) Handle(_ context.Context, conn gwdomain.Conn, frame gwdomain.Frame) error {
	if _, err := packet.ParseCZRequestTime(frame.Raw); err != nil {
		return fmt.Errorf("parse CZ_REQUEST_TIME: %w", err)
	}
	resp := packet.NotifyTimeResponse{Time: h.clock.MoveStart()}
	if err := resp.Encode(connWriter{conn}); err != nil {
		return fmt.Errorf("encode ZC_NOTIFY_TIME: %w", err)
	}
	return nil
}

// Compile-time check that TimeHandler satisfies the gateway handler shape.
var _ gwdomain.PacketHandler = (*TimeHandler)(nil).Handle

// SocialService owns the change-direction and emotion use cases. It shares the
// movement/spawn collaborators — the live-session registry (to resolve the
// player and its neighbors) and the map store (to reach the player's AOI) — and
// adds nothing else. Both methods resolve synchronously on the conn goroutine,
// like movement; neither enqueues work.
type SocialService struct {
	registry *domain.PlayerRegistry
	maps     domain.MapStore
}

// NewSocialService binds the social collaborators.
func NewSocialService(registry *domain.PlayerRegistry, maps domain.MapStore) *SocialService {
	return &SocialService{registry: registry, maps: maps}
}

// ChangeDir persists the player's new body facing and broadcasts ZC_CHANGE_DIR
// to the player and every AOI neighbor. HeadDir is forwarded verbatim (rAthena
// clamps 0..2 upstream; the server does not re-validate), and the body Dir is
// committed to the player's cached position so the next spawn/walk broadcast
// reads the updated facing. A player not in the registry is a late packet after
// disconnect — dropped with nil error. A map-store failure is unexpected (the
// map loaded at enter-world) and is returned so the gateway can log it; the
// session is kept either way.
func (s *SocialService) ChangeDir(ctx context.Context, accountID uint32, headDir uint16, dir uint8) error {
	player, ok := s.registry.ByAccount(accountID)
	if !ok {
		// Late packet after disconnect — no player to re-face. Not an error.
		return nil
	}
	mp, err := s.maps.Load(ctx, player.MapName)
	if err != nil {
		// The map loaded at enter-world; a failure here is a transient/corrupt
		// map-store fault, not a client error. Propagate it so the gateway logs
		// the unexpected condition (ProcessBytes logs handler errors and keeps
		// the session) rather than swallowing it.
		return fmt.Errorf("change-dir: load map %q: %w", player.MapName, err)
	}
	// Persist the body facing without moving the cell: re-stamp the current
	// cell with the new dir. SetPosition is locked, so a concurrent SpawnUnit/
	// WalkUnit reads either the old or the new facing, never a torn half-write.
	posX, posY, _ := player.Position()
	player.SetPosition(posX, posY, dir)

	resp := packet.ChangeDirResponse{SrcID: player.AccountID, HeadDir: headDir, Dir: dir}
	s.broadcast(mp, player, resp.Encode)
	return nil
}

// Emotion broadcasts ZC_EMOTION to the player and every AOI neighbor. The type
// byte (rAthena's emotion_type enum) is forwarded verbatim; rAthena's
// clif_emotion sends to AREA on its own and ignores block type. The entity-id
// field is the player's account_id (= bl->id; see the AID-vs-GID convention on
// SpawnUnitResponse) so the client attributes the icon to the sprite it spawned.
func (s *SocialService) Emotion(ctx context.Context, accountID uint32, emotionType uint8) error {
	player, ok := s.registry.ByAccount(accountID)
	if !ok {
		// Late packet after disconnect — no player to emote. Not an error.
		return nil
	}
	mp, err := s.maps.Load(ctx, player.MapName)
	if err != nil {
		return fmt.Errorf("emotion: load map %q: %w", player.MapName, err)
	}
	resp := packet.EmotionResponse{GID: player.AccountID, Type: emotionType}
	s.broadcast(mp, player, resp.Encode)
	return nil
}

// broadcast writes enc to the originator and every live PC in AOI range of the
// originator's cell. A neighbor whose write fails is torn down (idempotent) so
// its stale entity stops polluting future broadcasts — the same contract as
// MoveService's observer loop. enc is a bound Encode method; both
// ChangeDirResponse and EmotionResponse expose Encode(io.Writer) error, so the
// helper is the single place that walks the AOI for both use cases.
func (s *SocialService) broadcast(mp *domain.Map, player *domain.Player, enc func(io.Writer) error) {
	x, y, _ := player.Position()

	// Self first: the originator sees its own dir/emotion. If its socket is
	// dead, tear it down and stop — there is no one to broadcast to.
	if err := enc(connWriter{player.Conn}); err != nil {
		s.dropPlayer(mp, player)
		return
	}
	for _, e := range mp.AOI.QueryVisible(int(x), int(y)) {
		if e.ID == player.EntityID {
			continue // originator already got it above
		}
		neighbor, ok := s.registry.ByAccount(uint32(e.ID))
		if !ok {
			continue // mob/NPC (no registry entry) or a torn-down player
		}
		if err := enc(connWriter{neighbor.Conn}); err != nil {
			s.dropPlayer(mp, neighbor)
		}
	}
}

// dropPlayer tears down a player whose Conn write failed, idempotently, so its
// stale AOI entity stops receiving broadcasts. Mirrors MoveService.dropPlayer
// and SpawnService.dropNeighbor; kept per-service (rather than shared) so the
// social use case keeps the gateway domain its only cross-module dependency.
func (s *SocialService) dropPlayer(mp *domain.Map, player *domain.Player) {
	if player == nil {
		return
	}
	s.registry.Unregister(player.AccountID)
	_ = mp.AOI.RemoveEntity(player.EntityID)
}

// ChangeDirHandler serves CZ_CHANGE_DIR (0x009b) on the map-role dispatch table.
type ChangeDirHandler struct {
	svc *SocialService
}

// NewChangeDirHandler builds a CZ_CHANGE_DIR handler over the SocialService.
func NewChangeDirHandler(svc *SocialService) *ChangeDirHandler {
	return &ChangeDirHandler{svc: svc}
}

// Handle implements gateway/domain.PacketHandler for CZ_CHANGE_DIR. accountID is
// sourced from conn.Auth().AccountID — the impersonation guard shared with
// CZ_REQUEST_MOVE and CZ_ACTION_REQUEST.
func (h *ChangeDirHandler) Handle(ctx context.Context, conn gwdomain.Conn, frame gwdomain.Frame) error {
	req, err := packet.ParseCZChangeDir(frame.Raw)
	if err != nil {
		return fmt.Errorf("parse CZ_CHANGE_DIR: %w", err)
	}
	accountID := conn.Auth().AccountID
	if accountID == 0 {
		return errors.New("change-dir: connection has no verified account (CZ_ENTER not completed)")
	}
	return h.svc.ChangeDir(ctx, accountID, req.HeadDir, req.Dir)
}

// Compile-time check that ChangeDirHandler satisfies the gateway handler shape.
var _ gwdomain.PacketHandler = (*ChangeDirHandler)(nil).Handle

// EmotionHandler serves CZ_REQ_EMOTION (0x00bf) on the map-role dispatch table.
type EmotionHandler struct {
	svc *SocialService
}

// NewEmotionHandler builds a CZ_REQ_EMOTION handler over the SocialService.
func NewEmotionHandler(svc *SocialService) *EmotionHandler {
	return &EmotionHandler{svc: svc}
}

// Handle implements gateway/domain.PacketHandler for CZ_REQ_EMOTION. accountID
// is sourced from conn.Auth().AccountID — the impersonation guard.
func (h *EmotionHandler) Handle(ctx context.Context, conn gwdomain.Conn, frame gwdomain.Frame) error {
	req, err := packet.ParseCZReqEmotion(frame.Raw)
	if err != nil {
		return fmt.Errorf("parse CZ_REQ_EMOTION: %w", err)
	}
	accountID := conn.Auth().AccountID
	if accountID == 0 {
		return errors.New("emotion: connection has no verified account (CZ_ENTER not completed)")
	}
	return h.svc.Emotion(ctx, accountID, req.EmotionType)
}

// Compile-time check that EmotionHandler satisfies the gateway handler shape.
var _ gwdomain.PacketHandler = (*EmotionHandler)(nil).Handle
