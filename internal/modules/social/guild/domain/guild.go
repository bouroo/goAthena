// Package domain holds the guild bounded context's pure model: the Guild
// aggregate (mirrors rAthena's `guild` table plus the `char.guild_id`
// membership column) and its persistence port.
//
// Shape note: rAthena splits membership across `guild_member` (exp/position
// per member) and `char.guild_id`. This slice models only the `char.guild_id`
// half — per-member contribution and position titles land with guild-exp
// donation, as its own commit. The master is the guild row's `char_id`, the
// rAthena `guild.char_id` column (src/char/int_guild.cpp:145).
package domain

import (
	"context"
	"errors"
)

// GuildID is rAthena's guild.guild_id (auto-increment per row).
type GuildID uint32

// Guild mirrors the rAthena `guild` table, first-slice columns. GuildLv starts
// at 1 (rAthena guild_calcinfo floors it at 1); MaxMember starts at 16
// (mmo.hpp MAX_GUILD base before GD_EXTENSION raises it — extension support is
// a later commit).
type Guild struct {
	ID        GuildID `gorm:"column:guild_id;primaryKey;autoIncrement"`
	Name      string  `gorm:"column:name;size:24"`
	Master    uint32  `gorm:"column:char_id"` // master's char_id
	MasterNam string  `gorm:"column:master;size:24"`
	GuildLv   uint8   `gorm:"column:guild_lv"`
	MaxMember uint8   `gorm:"column:max_member"`
	AverageLv uint16  `gorm:"column:average_lv"`
}

// TableName pins the singular table name so GORM's default pluralization
// cannot drift from migration 000010's CREATE TABLE "guild".
func (Guild) TableName() string { return "guild" }

// GuildMember is one roster entry, derived from the member's `char` row. The
// master's member position is 0 on the wire (rAthena guild_member.position).
type GuildMember struct {
	CharID    uint32
	AccountID uint32
	Name      string
	Map       string
	Class     uint16
	BaseLevel uint16
	Online    bool
	Master    bool
	// Position: 0 = master, 1 = member (derived; guild_position is not
	// modeled this slice).
	Position int32
}

// MaxGuildSize is this slice's member ceiling (rAthena base MAX_GUILD 16
// before the GD_EXTENSION skill raises it — extension lands later).
const MaxGuildSize = 16

var (
	// ErrGuildNotFound is returned when a guild row is absent.
	ErrGuildNotFound = errors.New("guild not found")

	// ErrGuildFull is returned when adding a member would exceed MaxGuildSize.
	// Maps to ZC_ACK_REQ_JOIN_GUILD result=3 (clif.cpp:948).
	ErrGuildFull = errors.New("guild full")

	// ErrAlreadyInGuild is returned when a char tries to create or join a
	// second guild. Maps to ZC_RESULT_MAKE_GUILD result=1 on create and
	// ZC_ACK_REQ_JOIN_GUILD result=0 on invite (clif.cpp:943).
	ErrAlreadyInGuild = errors.New("already in a guild")

	// ErrNotInGuild is returned for a leave/kick/break target with no
	// membership.
	ErrNotInGuild = errors.New("not in guild")

	// ErrNotMaster is returned for a master-only action (ban, break, invite)
	// issued by a non-master. Positions with invite/expel permissions are a
	// later commit, so master-only is the faithful floor this slice.
	ErrNotMaster = errors.New("not the guild master")

	// ErrEmptyGuildName is returned when CZ_REQ_MAKE_GUILD carries a blank
	// trimmed name (guild.cpp:696 — silent drop; the gateway acks 0 only on
	// real outcomes, so this maps to a log-and-ignore).
	ErrEmptyGuildName = errors.New("empty guild name")

	// ErrNameExists is returned when the chosen name is taken
	// (guild.cpp:729 — clif_guild_created flag 2).
	ErrNameExists = errors.New("guild name already exists")

	// ErrCantExpelMaster is returned when the ban target is the master
	// (guild.cpp:1208).
	ErrCantExpelMaster = errors.New("cannot expel the guild master")

	// ErrBreakNameMismatch is returned when CZ_REQ_DISORGANIZE_GUILD's key
	// does not echo the guild name (guild.cpp:2300).
	ErrBreakNameMismatch = errors.New("disorganize key does not match guild name")

	// ErrMembersOnline is returned when a break is requested while other
	// members are connected (guild.cpp:2310 — clif_guild_broken flag 2).
	ErrMembersOnline = errors.New("guild still has members online")
)

// GuildRepository is the persistence port for the guild aggregate.
//
// Membership lives on `char`, so every membership mutation is a two-table
// write (guild row + char.guild_id); the port exposes whole operations so each
// runs in one transaction and a caller can never update just one side.
type GuildRepository interface {
	// Create inserts a new guild led by the given char and sets that char's
	// guild_id, in one transaction. Returns ErrAlreadyInGuild if the char
	// already belongs to a guild, ErrNameExists if the name is taken.
	Create(ctx context.Context, masterAccountID, masterCharID uint32, name string) (Guild, error)

	// Get returns the guild row. Absent rows yield ErrGuildNotFound.
	Get(ctx context.Context, id GuildID) (Guild, error)

	// GetByMember returns the guild the char belongs to, or ErrNotInGuild.
	GetByMember(ctx context.Context, charID uint32) (Guild, error)

	// Members returns the roster, ordered by char_id, with Master and
	// Position derived against the guild's master char_id. Backs the
	// ZC_MEMBERMGR_INFO burst.
	Members(ctx context.Context, id GuildID) ([]GuildMember, error)

	// AddMember joins charID to the guild: sets char.guild_id in one
	// transaction. Returns ErrGuildFull at the cap, ErrAlreadyInGuild if the
	// char already belongs to any guild, ErrGuildNotFound if the guild is
	// gone.
	AddMember(ctx context.Context, id GuildID, charID uint32) error

	// RemoveMember takes charID out of the guild, clearing char.guild_id.
	//
	// Empty-guild semantics: rAthena breaks the guild when the last member
	// leaves (mapif_parse_GuildLeave → guild_check_empty, src/char/
	// int_guild.cpp:1353). A master leaving does NOT disband — the guild
	// survives leaderless. Returns ErrNotInGuild if charID is not a member.
	RemoveMember(ctx context.Context, id GuildID, charID uint32) error

	// Break disbands the guild: every member's guild_id is cleared and the
	// guild row deleted, in one transaction. The caller (service) validates
	// key match, master authority, and the no-online-members rule first;
	// Break is the pure teardown.
	Break(ctx context.Context, id GuildID) error
}
