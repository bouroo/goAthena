package domain

import (
	"context"
	"fmt"
	"time"
)

//go:generate go run go.uber.org/mock/mockgen -destination=mock/chat_service_mock.go -package=domainmock . ChatService

//go:generate go run go.uber.org/mock/mockgen -destination=mock/chat_repository_mock.go -package=domainmock . ChatRepository

//go:generate go run go.uber.org/mock/mockgen -destination=mock/friend_repository_mock.go -package=domainmock . FriendRepository

//go:generate go run go.uber.org/mock/mockgen -destination=mock/party_repository_mock.go -package=domainmock . PartyRepository

//go:generate go run go.uber.org/mock/mockgen -destination=mock/lock_store_mock.go -package=domainmock . LockStore

// LockStore is the outbound port for distributed locks used in friend/party operations.
type LockStore interface {
	// Acquire obtains a lock identified by key, holding it for ttl.
	// Returns a unique token that must be passed to Release.
	Acquire(ctx context.Context, key string, ttl time.Duration) (string, error)

	// Release releases the lock identified by key and token.
	Release(ctx context.Context, key, token string) error
}

// FriendLockKey returns the lock key for an account's friend mutex.
func FriendLockKey(accountID uint32) string {
	return "friend:" + fmt.Sprint(accountID)
}

// PartyLockKey returns the lock key for a party mutex.
func PartyLockKey(partyID string) string {
	return "party:" + partyID
}

// ChatRepository is the outbound port for chat message persistence.
type ChatRepository interface {
	// SaveMessage stores a chat message.
	SaveMessage(ctx context.Context, msg Message) error

	// GetRecentMessages retrieves recent messages for a character.
	GetRecentMessages(ctx context.Context, charID uint32, limit int) ([]Message, error)
}

// FriendRepository is the outbound port for friend relationship persistence.
type FriendRepository interface {
	// AddFriend creates a new friendship entry.
	AddFriend(ctx context.Context, friendship Friendship) error

	// RemoveFriend deletes a friendship entry.
	RemoveFriend(ctx context.Context, accountID, friendAccountID uint32) error

	// ListFriends returns all friends for an account.
	ListFriends(ctx context.Context, accountID uint32) ([]Friendship, error)

	// CreateFriendRequest stores a new friend request.
	CreateFriendRequest(ctx context.Context, req FriendRequest) error

	// UpdateFriendRequest modifies a friend request status.
	UpdateFriendRequest(ctx context.Context, req FriendRequest) error

	// GetFriendRequest retrieves a friend request by ID.
	GetFriendRequest(ctx context.Context, requestID uint64) (FriendRequest, error)

	// GetPendingFriendRequests returns pending requests for an account.
	GetPendingFriendRequests(ctx context.Context, accountID uint32) ([]FriendRequest, error)

	// IsFriend checks if two accounts are friends.
	IsFriend(ctx context.Context, accountID, friendAccountID uint32) (bool, error)

	// HasPendingRequest checks if there's a pending friend request between two accounts.
	HasPendingRequest(ctx context.Context, fromAccountID, toAccountID uint32) (bool, error)
}

// PartyRepository is the outbound port for party persistence.
type PartyRepository interface {
	// CreateParty stores a new party.
	CreateParty(ctx context.Context, party Party) error

	// GetParty retrieves a party by ID.
	GetParty(ctx context.Context, partyID string) (Party, error)

	// GetPartyByMember finds the party containing a character.
	GetPartyByMember(ctx context.Context, charID uint32) (Party, error)

	// UpdateParty modifies party data.
	UpdateParty(ctx context.Context, party Party) error

	// DeleteParty removes a party from storage.
	DeleteParty(ctx context.Context, partyID string) error
}

// ChatService is the inbound port for chat and social use-cases.
// It manages whisper messages, party chat, friend requests, and parties.
type ChatService interface {
	// Whisper sends a private message from sender to target.
	Whisper(ctx context.Context, senderID, targetID uint32, content string) error

	// SendPartyChat sends a message to all party members.
	SendPartyChat(ctx context.Context, senderID uint32, content string) error

	// SendMapChat sends a message to all players on the same map.
	SendMapChat(ctx context.Context, senderID uint32, mapName string, content string) error

	// SendFriendRequest sends a friend request.
	SendFriendRequest(ctx context.Context, fromAccountID, toAccountID uint32, fromName, toName string) error

	// AcceptFriendRequest accepts a pending friend request.
	AcceptFriendRequest(ctx context.Context, requestID uint64) error

	// RejectFriendRequest rejects a pending friend request.
	RejectFriendRequest(ctx context.Context, requestID uint64) error

	// RemoveFriend removes a friend.
	RemoveFriend(ctx context.Context, accountID, friendAccountID uint32) error

	// ListFriends returns the friend list.
	ListFriends(ctx context.Context, accountID uint32) ([]Friendship, error)

	// CreateParty creates a new party.
	CreateParty(ctx context.Context, name string, leaderID uint32, leaderName string) (Party, error)

	// JoinParty adds a character to a party.
	JoinParty(ctx context.Context, partyID string, charID uint32, charName string) error

	// LeaveParty removes a character from a party.
	LeaveParty(ctx context.Context, partyID string, charID uint32) error

	// GetParty returns party info.
	GetParty(ctx context.Context, partyID string) (Party, error)
}
