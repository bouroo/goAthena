package app

import (
	"context"
	"fmt"

	gwdomain "github.com/bouroo/goAthena/internal/modules/gateway/domain"
	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// ChatService owns the CZ_GLOBAL_MESSAGE use case. It shares the
// movement/spawn collaborators — the live-session registry (to resolve the
// player and its neighbors) and the map store (to reach the player's AOI) — and
// adds nothing else. The method resolves synchronously on the conn goroutine,
// like movement; it does not enqueue work.
type ChatService struct {
	registry *domain.PlayerRegistry
	maps     domain.MapStore
}

// NewChatService binds the chat collaborators.
func NewChatService(registry *domain.PlayerRegistry, maps domain.MapStore) *ChatService {
	return &ChatService{registry: registry, maps: maps}
}

// HandleChat resolves one CZ_GLOBAL_MESSAGE from accountID and broadcasts
// ZC_NOTIFY_CHAT to the sender and every AOI neighbor. A player not in the
// registry is a late packet after disconnect — dropped with nil error. A
// map-store failure is unexpected (the map loaded at enter-world) and is
// returned so the gateway can log it; the session is kept either way.
func (s *ChatService) HandleChat(ctx context.Context, accountID uint32, msg string) error {
	player, ok := s.registry.ByAccount(accountID)
	if !ok {
		// Late packet after disconnect — no player to echo from. Not an error.
		return nil
	}
	mp, err := s.maps.Load(ctx, player.MapName)
	if err != nil {
		// The map loaded at enter-world; a failure here is a transient/corrupt
		// map-store fault, not a client error. Propagate it so the gateway logs
		// the unexpected condition (ProcessBytes logs handler errors and keeps
		// the session) rather than swallowing it.
		return fmt.Errorf("chat: load map %q: %w", player.MapName, err)
	}
	resp := packet.NotifyChatResponse{GID: player.AccountID, Message: msg}
	s.broadcast(mp, player, resp)
	return nil
}

// broadcast writes the notification to the originator and every live PC in AOI
// range of the originator's cell. A neighbor whose write fails is torn down
// (idempotent) so its stale entity stops polluting future broadcasts — the same
// contract as SocialService.broadcast and MoveService's observer loop.
func (s *ChatService) broadcast(mp *domain.Map, player *domain.Player, resp packet.NotifyChatResponse) {
	x, y, _ := player.Position()

	// Self first: the originator sees its own message. If its socket is dead,
	// tear it down and stop — there is no one to broadcast to.
	if err := resp.Encode(connWriter{player.Conn}); err != nil {
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
		if err := resp.Encode(connWriter{neighbor.Conn}); err != nil {
			s.dropPlayer(mp, neighbor)
		}
	}
}

// dropPlayer tears down a player whose Conn write failed, idempotently, so its
// stale AOI entity stops receiving broadcasts. Mirrors SocialService.dropPlayer.
func (s *ChatService) dropPlayer(mp *domain.Map, player *domain.Player) {
	if player == nil {
		return
	}
	s.registry.Unregister(player.AccountID)
	_ = mp.AOI.RemoveEntity(player.EntityID)
}

// ChatHandler serves CZ_GLOBAL_MESSAGE (0x008c) on the map-role dispatch table.
type ChatHandler struct {
	svc *ChatService
}

// NewChatHandler builds a CZ_GLOBAL_MESSAGE handler over the ChatService.
func NewChatHandler(registry *domain.PlayerRegistry, maps domain.MapStore) *ChatHandler {
	return &ChatHandler{svc: NewChatService(registry, maps)}
}

// Handle implements gateway/domain.PacketHandler for CZ_GLOBAL_MESSAGE. accountID
// is sourced from conn.Auth().AccountID — the impersonation guard shared with
// every other map-role handler.
func (h *ChatHandler) Handle(ctx context.Context, conn gwdomain.Conn, frame gwdomain.Frame) error {
	req, err := packet.ParseCZGlobalMessage(frame.Raw)
	if err != nil {
		return fmt.Errorf("parse CZ_GLOBAL_MESSAGE: %w", err)
	}
	accountID := conn.Auth().AccountID
	if accountID == 0 {
		// No cached auth ⇒ the connection never passed the CZ_ENTER gate.
		// Tolerated by the gateway (the conn stays open) but the chat is
		// dropped.
		return nil
	}
	return h.svc.HandleChat(ctx, accountID, req.Message)
}

// Compile-time check that ChatHandler satisfies the gateway handler shape.
var _ gwdomain.PacketHandler = (*ChatHandler)(nil).Handle
