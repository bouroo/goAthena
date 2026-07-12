package domain

import (
	"time"
)

// MessageType classifies a chat message by delivery scope.
type MessageType uint8

const (
	// MessageTypeWhisper is a private message between two players (/w name message).
	MessageTypeWhisper MessageType = iota
	// MessageTypeParty is a message sent to all party members.
	MessageTypeParty
	// MessageTypeGuild is a message sent to all guild members (future).
	MessageTypeGuild
	// MessageTypeGlobal is a broadcast message to all players.
	MessageTypeGlobal
	// MessageTypeMap is a message sent to all players on the same map.
	MessageTypeMap
)

// Message represents a single chat message in the system.
type Message struct {
	ID        string // UUID
	SenderID  uint32 // Character ID of sender
	TargetID  uint32 // Character ID of target (0 for broadcast types)
	Type      MessageType
	Content   string
	Timestamp time.Time
}

// ChatChannel represents a logical chat channel (party, guild, etc.)
type ChatChannel struct {
	ID      string // UUID
	Type    MessageType
	Members []uint32 // Character IDs
	Name    string
}
