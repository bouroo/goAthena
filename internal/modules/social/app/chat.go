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

// SendWhisper routes a private message to the named target. Returns an error
// if the target is offline.
func (s *ChatService) SendWhisper(ctx context.Context, msg domain.ChatMessage) error {
	targetID, ok := s.dir.ResolveName(ctx, msg.Target)
	if !ok {
		return fmt.Errorf("whisper target %q: not online", msg.Target)
	}
	conn, ok := s.dir.ResolveConn(ctx, targetID)
	if !ok {
		return fmt.Errorf("whisper target %q: no connection", msg.Target)
	}
	// The caller (dispatch handler) builds the ZC_WHISPER packet bytes and
	// passes them via the ConnWriter; ChatService only resolves routing.
	_ = conn // packet encoding + write done by the handler that has the packet codec
	return nil
}
