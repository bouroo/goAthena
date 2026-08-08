// Package domain holds the social bounded context: chat message types and the
// player-resolution port for whisper routing.
package domain

import "context"

// ChatMessage is one public or private message.
type ChatMessage struct {
	SenderGID  uint32
	SenderName string
	Target     string // whisper target name (empty for public)
	Text       string
	IsWhisper  bool
}

// PlayerDirectory resolves online players by name (for whisper routing) and
// by charID (for reply targeting). The world's entity registry implements this.
type PlayerDirectory interface {
	// ResolveName returns the charID for an online player name, or false.
	ResolveName(ctx context.Context, name string) (uint32, bool)
	// ResolveConn returns the player's connection writer for a charID.
	// (In the monolith this is the gnet conn's AsyncWrite; extracted as a port
	// so the social module doesn't import gnet.)
	ResolveConn(ctx context.Context, charID uint32) (ConnWriter, bool)
}

// ConnWriter is the narrow write surface for sending packets to a player's
// connection. gnet's AsyncWrite satisfies this.
type ConnWriter interface {
	WritePacket(data []byte)
}
