package domain

import (
	"time"
)

// TradeState is the current state of a player trade session.
type TradeState uint8

const (
	// TradeStateRequested is the initial state, waiting for target to accept.
	TradeStateRequested TradeState = iota
	// TradeStateOpen means both parties connected, accepting items.
	TradeStateOpen
	// TradeStateConfirmed means one party confirmed, waiting for other.
	TradeStateConfirmed
	// TradeStateLocked means both confirmed, ready to complete.
	TradeStateLocked
	// TradeStateCompleted means successfully transferred.
	TradeStateCompleted
	// TradeStateCancelled means cancelled by one party or timeout.
	TradeStateCancelled
)

// TradeItem represents an item offered in a trade window.
type TradeItem struct {
	InventoryID uint32 // Reference to inventory item
	ItemID      uint32 // Item type ID
	Amount      int32  // Stack amount (positive)
}

// Trade represents a player trading session.
type Trade struct {
	ID               string      // Unique trade session ID
	Player1CharID    uint32      // Requester character ID
	Player2CharID    uint32      // Target character ID (0 until accepted)
	Player1Items     []TradeItem // Items offered by player 1
	Player2Items     []TradeItem // Items offered by player 2
	Player1Zeny      uint32      // Zeny offered by player 1
	Player2Zeny      uint32      // Zeny offered by player 2
	Player1Confirmed bool        // Player 1 locked their offer
	Player2Confirmed bool        // Player 2 locked their offer
	State            TradeState  // Current state of the trade
	CreatedAt        time.Time   // Trade creation timestamp
	UpdatedAt        time.Time   // Last modification timestamp
}
