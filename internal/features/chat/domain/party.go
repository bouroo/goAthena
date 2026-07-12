package domain

import (
	"time"
)

// Party represents a group of up to 12 characters adventuring together.
type Party struct {
	ID        string // UUID
	Name      string
	LeaderID  uint32 // Character ID of party leader
	Members   []PartyMember
	CreatedAt time.Time
	UpdatedAt time.Time
}

// PartyMember represents a character within a party.
type PartyMember struct {
	CharID   uint32
	Name     string
	IsLeader bool
	JoinedAt time.Time
}
