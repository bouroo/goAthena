// Package app implements the social bounded context use cases. ChatService
// routes whispers (private messages) to named recipients.
package app

import (
	"context"
	"fmt"

	"github.com/bouroo/goAthena/internal/modules/social/domain"
)

// ChatService routes chat messages.
type ChatService struct {
	dir domain.PlayerDirectory
}

// NewChatService builds a ChatService backed by the player directory.
func NewChatService(dir domain.PlayerDirectory) *ChatService {
	return &ChatService{dir: dir}
}

// SendWhisper routes a private message to the named target and reports delivery.
// The handler (which owns the packet codec) encodes ZC_WHISPER through the
// resolved ConnWriter; ChatService only resolves routing. Returns an error only
// when the target is not an online player (the wire-level target-offline case).
func (s *ChatService) SendWhisper(ctx context.Context, msg domain.ChatMessage) error {
	targetID, ok := s.dir.ResolveName(ctx, msg.Target)
	if !ok {
		return fmt.Errorf("whisper target %q: not online", msg.Target)
	}
	_, ok = s.dir.ResolveConn(ctx, targetID)
	if !ok {
		return fmt.Errorf("whisper target %q: no connection", msg.Target)
	}
	return nil
}
