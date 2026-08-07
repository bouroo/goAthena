package app

import (
	"context"
	"fmt"

	gwdomain "github.com/bouroo/goAthena/internal/modules/gateway/domain"
	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// Whisper ack results for ZC_ACK_WHISPER (clif.hpp:843-847).
const (
	whisperResultSuccess       uint8 = 0
	whisperResultTargetOffline uint8 = 1
)

// WhisperService owns the CZ_WHISPER use case: a private message addressed to
// a named recipient. It resolves the sender from the verified session, the
// recipient by nick through the registry, delivers a ZC_WHISPER to the
// recipient, and acks the sender with ZC_ACK_WHISPER. It mirrors rAthena's
// clif_parse_WisMessage → clif_wis_messagein → wis_toend flow: the ack is
// unconditional (offline→1, success→0), and a dead recipient conn is torn down
// so its stale registry entry stops receiving future whispers.
type WhisperService struct {
	registry *domain.PlayerRegistry
}

// NewWhisperService binds the whisper collaborator. The registry is the single
// live-session index; it resolves both the sender (ByAccount, impersonation-
// guarded) and the recipient (ByName, case-insensitive like map_nick2sd).
func NewWhisperService(registry *domain.PlayerRegistry) *WhisperService {
	return &WhisperService{registry: registry}
}

// Whisper resolves sender→recipient and delivers the message. accountID is the
// verified sender (from conn.Auth(), never the packet — CZ_WHISPER carries no
// sender id). A sender not in the registry is a late packet after disconnect,
// dropped silently. An absent recipient yields the offline ack (result=1) and
// no delivery. On a successful delivery the sender is acked success (result=0,
// CID=sender char_id). A recipient conn write failure tears down the recipient
// (idempotent) and still acks the sender success — the message was dispatched
// to the registry's best-known live recipient (rAthena's intif path acks
// success after dispatch).
func (s *WhisperService) Whisper(ctx context.Context, accountID uint32, targetNick, msg string) error {
	sender, ok := s.registry.ByAccount(accountID)
	if !ok {
		// Late packet after disconnect — no sender to ack from. Not an error.
		return nil
	}
	target, ok := s.registry.ByName(targetNick)
	if !ok {
		// Recipient offline: ack the sender with target-offline (result=1).
		// CID is the sender's char_id (rAthena wis_end writes sd.status.char_id).
		ack := packet.ZCAckWhisperResponse{Result: whisperResultTargetOffline, CID: sender.CharID}
		if err := ack.Encode(connWriter{sender.Conn}); err != nil {
			return fmt.Errorf("whisper: encode ZC_ACK_WHISPER (offline): %w", err)
		}
		return nil
	}
	resp := packet.ZCWhisperResponse{
		SenderGID:  sender.AccountID,
		SenderName: sender.Name,
		IsAdmin:    0,
		Message:    msg,
	}
	if err := resp.Encode(connWriter{target.Conn}); err != nil {
		// Recipient's conn is dead — tear it down so its stale entry stops
		// receiving future whispers/broadcasts. The message was dispatched to
		// the registry's best-known live recipient, so still ack the sender
		// success (mirrors rAthena's intif dispatch-then-ack path).
		s.registry.Unregister(target.AccountID)
	}
	ack := packet.ZCAckWhisperResponse{Result: whisperResultSuccess, CID: sender.CharID}
	if err := ack.Encode(connWriter{sender.Conn}); err != nil {
		return fmt.Errorf("whisper: encode ZC_ACK_WHISPER (success): %w", err)
	}
	return nil
}

// WhisperHandler serves CZ_WHISPER (0x0096) on the map-role dispatch table.
type WhisperHandler struct {
	svc *WhisperService
}

// NewWhisperHandler builds a CZ_WHISPER handler over the WhisperService.
func NewWhisperHandler(registry *domain.PlayerRegistry) *WhisperHandler {
	return &WhisperHandler{svc: NewWhisperService(registry)}
}

// Handle implements gateway/domain.PacketHandler for CZ_WHISPER. accountID is
// sourced from conn.Auth().AccountID — the impersonation guard shared with
// every other map-role handler; a connection that never passed CZ_ENTER is
// silently dropped.
func (h *WhisperHandler) Handle(ctx context.Context, conn gwdomain.Conn, frame gwdomain.Frame) error {
	req, err := packet.ParseCZWhisper(frame.Raw)
	if err != nil {
		return fmt.Errorf("parse CZ_WHISPER: %w", err)
	}
	accountID := conn.Auth().AccountID
	if accountID == 0 {
		// No cached auth ⇒ the connection never passed the CZ_ENTER gate.
		// Tolerated by the gateway (the conn stays open) but the whisper is
		// dropped.
		return nil
	}
	return h.svc.Whisper(ctx, accountID, req.TargetNick, req.Message)
}

// Compile-time check that WhisperHandler satisfies the gateway handler shape.
var _ gwdomain.PacketHandler = (*WhisperHandler)(nil).Handle
