package domain

import (
	"context"

	gwdomain "github.com/bouroo/goAthena/internal/modules/gateway/domain"
)

// World represents the world's use cases that scripts can invoke, bridged from
// the script module. It provides the capabilities required by the script.Host.
type World interface {
	// Warp moves a player to a new location.
	Warp(ctx context.Context, conn gwdomain.Conn, accountID, charID uint32, mapName string, x, y int) error

	// Heal restores a player's HP/SP by percentage.
	Heal(ctx context.Context, conn gwdomain.Conn, accountID, charID uint32, hpPct, spPct int) error
}

// DialogRegistry tracks active dialog sessions, preventing concurrent execution
// of scripts for the same player, and providing the channel that Next/Menu wait
// on to receive the client's progression packet.
type DialogRegistry interface {
	// Open checks and reserves a dialog session for the account. It returns a channel
	// that receives true on advancement (next, choose) and false on early termination
	// (disconnect, close). Returns an error if a dialog is already active.
	Open(accountID uint32) (chan bool, error)

	// Get returns the channel for an active dialog, or nil if none exists.
	Get(accountID uint32) chan bool

	// Close removes the active dialog session.
	Close(accountID uint32)
}
