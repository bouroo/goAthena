package domain

import "errors"

// Sentinel errors returned by the chat service and repository.
// Service-layer callers compare these using errors.Is.
var (
	// ErrPlayerNotFound is returned when a player/character does not exist.
	ErrPlayerNotFound = errors.New("player not found")

	// ErrNotFriends is returned when sender and target are not friends.
	ErrNotFriends = errors.New("not friends")

	// ErrFriendAlreadyExists is returned when a friendship already exists.
	ErrFriendAlreadyExists = errors.New("friend already exists")

	// ErrFriendRequestPending is returned when a friend request is already pending.
	ErrFriendRequestPending = errors.New("friend request already pending")

	// ErrPartyNotFound is returned when a party does not exist.
	ErrPartyNotFound = errors.New("party not found")

	// ErrPartyFull is returned when a party has reached maximum capacity (12 members).
	ErrPartyFull = errors.New("party full")

	// ErrNotPartyMember is returned when a character is not a member of the party.
	ErrNotPartyMember = errors.New("not party member")

	// ErrAlreadyInParty is returned when a character is already in a party.
	ErrAlreadyInParty = errors.New("already in party")

	// ErrEmptyMessage is returned when message content is empty.
	ErrEmptyMessage = errors.New("empty message")

	// ErrMessageTooLong is returned when message exceeds max length (255 chars).
	ErrMessageTooLong = errors.New("message too long")
)
