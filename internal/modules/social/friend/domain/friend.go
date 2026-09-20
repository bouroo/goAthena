// Package domain holds the friend bounded context's pure model: the friend
// pair (mirrors rAthena's `friends` table — one (char_id, friend_id) row per
// direction) and its persistence port.
//
// Shape note: rAthena stores ONLY the id pairs. Name, account id and online
// flag are resolved by joining `char` when the list is loaded (sql-files/main.sql:424-428;
// src/common/mmo.hpp s_friend is runtime-only).
package domain

import (
	"context"
	"errors"
)

// Friend is one roster entry as the wire needs it: the friend's identity
// resolved from their `char` row. The owner (CharID) is the list holder.
type Friend struct {
	CharID          uint32 // list owner
	FriendCharID    uint32
	FriendAccountID uint32
	Name            string
	Online          bool
}

// MaxFriends is rAthena's MAX_FRIENDS (src/common/mmo.hpp:168).
const MaxFriends = 40

var (
	// ErrFriendNotFound is returned when the pair to remove is absent from the
	// owner's list (rAthena msg_txt 672 "Name not found in list.",
	// src/map/clif.cpp:15565).
	ErrFriendNotFound = errors.New("friend not in list")

	// ErrFriendExists is returned when the pair to add is already present.
	// rAthena shows msg_txt 671 "Friend already exists." at request time
	// (src/map/clif.cpp:15446).
	ErrFriendExists = errors.New("already friends")

	// ErrFriendListFull is returned when the REQUESTER's list is at MaxFriends.
	// Maps to ZC_ADD_FRIENDS result=2 "Your Friend List is full."
	ErrFriendListFull = errors.New("friend list full")

	// ErrAcceptorListFull is returned when the ACCEPTOR's list is at
	// MaxFriends. Maps to ZC_ADD_FRIENDS result=3 "(%s)'s Friend List is full."
	ErrAcceptorListFull = errors.New("acceptor's friend list full")

	// ErrFriendSelf is returned when a char tries to friend itself (rAthena
	// silently returns at both request and reply, src/map/clif.cpp:15440, :15496).
	ErrFriendSelf = errors.New("cannot add oneself as friend")
)

// FriendRepository is the persistence port for the friend pairs.
//
// Every production mutation is bidirectional (accept inserts both rows,
// remove deletes both — rAthena's friend_auto_add default is on,
// src/map/battle.cpp:8613, and clif_parse_FriendsListRemove deletes from the
// friend's list first), so the port exposes whole-pair operations rather than
// single-row setters, each atomic in one transaction.
type FriendRepository interface {
	// List returns the owner's friends ordered by friend char id, each with
	// identity resolved from the friend's `char` row. Absent char rows (a
	// deleted character) are skipped.
	List(ctx context.Context, charID uint32) ([]Friend, error)

	// Add inserts BOTH directions (owner→friend and friend→owner) in one
	// transaction, enforcing capacity on both lists and absence of the pair.
	Add(ctx context.Context, ownerCharID, friendCharID uint32) error

	// Remove deletes BOTH directions in one transaction. Returns
	// ErrFriendNotFound when the owner's list lacks the pair.
	Remove(ctx context.Context, ownerCharID, friendCharID uint32) error
}
