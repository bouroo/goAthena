package domain

import (
	"time"
)

// FriendStatus represents the online status of a friend.
type FriendStatus uint8

const (
	// FriendStatusOffline indicates the friend is not logged in.
	FriendStatusOffline FriendStatus = iota
	// FriendStatusOnline indicates the friend is currently logged in.
	FriendStatusOnline
)

// Friendship represents a friend relationship between two accounts.
type Friendship struct {
	ID              uint64 // Primary key
	AccountID       uint32 // Account that owns this friend entry
	FriendAccountID uint32 // Account of the friend
	FriendName      string
	Status          FriendStatus
	CreatedAt       time.Time
}

// FriendRequestStatus represents the status of a friend request.
type FriendRequestStatus uint8

const (
	// FriendRequestPending is the initial state, waiting for acceptance/rejection.
	FriendRequestPending FriendRequestStatus = iota
	// FriendRequestAccepted indicates the request was accepted.
	FriendRequestAccepted
	// FriendRequestRejected indicates the request was rejected.
	FriendRequestRejected
)

// FriendRequest represents a pending or processed friend request.
type FriendRequest struct {
	ID            uint64
	FromAccountID uint32
	ToAccountID   uint32
	FromName      string
	ToName        string
	Status        FriendRequestStatus
	CreatedAt     time.Time
	UpdatedAt     time.Time
}
