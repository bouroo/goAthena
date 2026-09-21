// Package domain holds the party bounded context's pure model: the Party
// aggregate (mirrors rAthena's `party` table plus the `char.party_id` membership
// column) and its persistence port.
//
// Shape note: rAthena has NO `party_member` table. Membership is the single
// `char.party_id` column; the leader is the (account_id, char_id) pair stored on
// the party row (src/char/int_party.cpp:237-250). The roster is therefore a JOIN
// against `char`, not a stored list.
package domain

import (
	"context"
	"errors"
)

// PartyID is rAthena's party.party_id (auto-increment per row).
type PartyID uint32

// Party mirrors the rAthena `party` table — one row per group.
//
// LeaderID/LeaderChar are the leader's account_id and char_id. rAthena stores
// both because an account owns many chars, so char_id alone is not enough to
// resolve the leader's session.
type Party struct {
	ID         PartyID `gorm:"column:party_id;primaryKey;autoIncrement"`
	Name       string  `gorm:"column:name;size:24"`
	Exp        uint8   `gorm:"column:exp"`  // exp-share rule (0 = off)
	Item       uint8   `gorm:"column:item"` // item-share rule (bit 0 pickup, bit 1 loot)
	LeaderID   uint32  `gorm:"column:leader_id"`
	LeaderChar uint32  `gorm:"column:leader_char"`
}

// TableName pins the singular table name so GORM's default pluralization cannot
// drift from migration 000008's CREATE TABLE "party".
func (Party) TableName() string { return "party" }

// PartyMember is one roster entry, derived from the member's `char` row rather
// than stored in a join table. Leader is computed by comparing the char's
// (account_id, char_id) against the party's leader pair.
type PartyMember struct {
	CharID    uint32
	AccountID uint32
	Name      string
	Map       string
	Class     uint16
	BaseLevel uint16
	Online    bool
	Leader    bool
}

// MaxPartySize is the protocol's party-size ceiling (rAthena MAX_PARTY).
const MaxPartySize = 12

var (
	// ErrPartyNotFound is returned when a party row is absent.
	ErrPartyNotFound = errors.New("party not found")

	// ErrPartyFull is returned when adding a member would exceed MaxPartySize.
	// Maps to ZC_PARTY_JOIN_REQ_ACK result=3 (PARTY_REPLY_FULL).
	ErrPartyFull = errors.New("party full")

	// ErrAlreadyInParty is returned when a char tries to create or join a
	// second party. rAthena enforces one party per char; the client shows its
	// "already in party" dialog. Maps to ZC_PARTY_JOIN_REQ_ACK result=0
	// (PARTY_REPLY_JOIN_OTHER_PARTY).
	ErrAlreadyInParty = errors.New("already in a party")

	// ErrNotInParty is returned for a leave/kick target with no membership.
	ErrNotInParty = errors.New("not in party")

	// ErrNotLeader is returned for a leader-only action (kick, option change)
	// issued by a non-leader.
	ErrNotLeader = errors.New("not the party leader")

	// ErrEmptyPartyName is returned when CZ_MAKE_GROUP carries a blank name.
	// rAthena's party_create returns 0 early on an empty trimmed name
	// (src/map/party.cpp:148).
	ErrEmptyPartyName = errors.New("empty party name")
)

// PartyRepository is the persistence port for the party aggregate.
//
// Because membership lives on `char`, every membership mutation is a two-table
// write (party row + char.party_id). The port therefore exposes whole operations
// (Create, AddMember, RemoveMember) rather than separate party/roster setters, so
// each op runs in one transaction and a caller can never update just one side.
type PartyRepository interface {
	// Create inserts a new party led by (leaderAccountID, leaderCharID) with the
	// given name and sets that char's party_id, in one transaction. Returns
	// ErrAlreadyInParty if the char already belongs to a party.
	Create(ctx context.Context, leaderAccountID, leaderCharID uint32, name string) (Party, error)

	// Get returns the party row. Absent rows yield ErrPartyNotFound.
	Get(ctx context.Context, id PartyID) (Party, error)

	// GetByMember returns the party the char belongs to, or ErrNotInParty.
	GetByMember(ctx context.Context, charID uint32) (Party, error)

	// Members returns the roster, ordered by char_id, with Leader resolved
	// against the party's leader pair. Backs the ZC_GROUP_LIST burst.
	Members(ctx context.Context, id PartyID) ([]PartyMember, error)

	// AddMember joins charID to the party: sets char.party_id in one
	// transaction. Returns ErrPartyFull at the cap, ErrAlreadyInParty if the char
	// already belongs to any party, ErrPartyNotFound if the party is gone.
	AddMember(ctx context.Context, id PartyID, charID uint32) error

	// RemoveMember takes charID out of the party, clearing char.party_id.
	//
	// Leader semantics: rAthena disbands the WHOLE party when its leader leaves —
	// every remaining member is withdrawn and the party row deleted
	// (src/char/int_party.cpp:651-676). It never transfers leadership. A non-leader
	// leave just clears that one char.party_id. Returns ErrNotInParty if charID
	// is not a member.
	RemoveMember(ctx context.Context, id PartyID, charID uint32) error

	// SetOptions writes the exp/item share rules (CZ_CHANGE_GROUPEXPOPTION).
	SetOptions(ctx context.Context, id PartyID, exp, item uint8) error
}
