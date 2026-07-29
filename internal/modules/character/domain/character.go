// Package domain defines the character bounded context's value objects and
// outbound ports: the Character aggregate (the char-list and progression slice
// of the `char` table) and the repository the application layer depends on. It
// is pure — no transport or persistence dependencies — so the GORM and
// in-memory adapters, the char handlers, and tests program against these types
// rather than against each other.
package domain

import (
	"context"
	"errors"
)

// Sentinel errors returned by the repository and the application layer. Service
// code compares with errors.Is so wrapping is preserved; repository adapters
// must return these (wrapped) rather than their own driver-specific types.
// Character-creation sentinels map each to the matching HC_REFUSE_MAKECHAR error
// byte (rathena char_clif.cpp chclif_createnewchar_refuse: -1→0x00, -2→0xFF,
// -3→0x01, -4→0x03) — typed errors, not string matches, so the handler switches
// on errors.Is rather than message text.
var (
	// ErrCharacterNotFound: no char row matched the lookup key.
	ErrCharacterNotFound = errors.New("character not found")
	// ErrCharNameTaken: the requested name is already in use or reserved
	// (char_check_char_name -1; HC_REFUSE_MAKECHAR 0x00).
	ErrCharNameTaken = errors.New("character name already taken")
	// ErrSlotOccupied: the requested slot already holds a character
	// (char_make_new_char -2 path; HC_REFUSE_MAKECHAR 0xFF).
	ErrSlotOccupied = errors.New("character slot occupied")
	// ErrInvalidSlot: the slot index is outside [0, char_slots)
	// (char_make_new_char -4; HC_REFUSE_MAKECHAR 0x03).
	ErrInvalidSlot = errors.New("invalid character slot")
	// ErrInvalidInput: any other client-controlled input rejected by the
	// create path (empty/short/control-char/'#'/reserved name, bad sex, job not
	// allowed) — the -2 catch-all (HC_REFUSE_MAKECHAR 0xFF).
	ErrInvalidInput = errors.New("invalid character creation input")
)

// CreateCharacter is the validated input to CharacterRepository.Create. The
// fields mirror what the PACKETVER>=20151001 CH_MAKE_CHAR packet carries
// (packets.hpp:123-132): name, slot, hair_color, hair_style, job, sex. The
// server supplies base stats (1 each) and the starting economy/position, so
// those are not client inputs here.
type CreateCharacter struct {
	// AccountID is the owning account, trusted from the per-conn auth cache
	// (never from the packet).
	AccountID uint32
	// Name is the requested name (raw client bytes; validation trims/normalizes).
	Name string
	// Slot is the target char_num (0..char_slots-1).
	Slot uint8
	// HairColor is the requested hair-color palette.
	HairColor uint16
	// HairStyle is the requested hair style (stored in the char.hair column).
	HairStyle uint16
	// Job is the requested starting job (JOB_NOVICE etc.).
	Job uint32
	// Sex is the requested sex byte (0 = female, 1 = male).
	Sex uint8
}

// Character is the char-list slice of a `char`-table row: exactly the fields
// the CH_ENTER (char list) and CH_SELECT_CHAR (map redirect) flows read. The
// field set mirrors the columns pkg/ro/packet.CharacterInfo serializes, so the
// handler can map one straight to the other without loss. Time/state columns
// the char-list path does not touch (unban_time, fame, font, …) are omitted;
// GORM maps by column name so omitting them is safe.
type Character struct {
	CharID       uint32 // char_id  → CharacterInfo.GID
	AccountID    uint32 // account_id (lookup key; trusted from the conn auth cache)
	Slot         uint8  // char_num → CharacterInfo.CharNum
	Name         string // name     → CharacterInfo.Name
	Class        uint16 // class    → CharacterInfo.Job
	BaseLevel    uint16 // base_level → CharacterInfo.Level
	JobLevel     uint16 // job_level  → CharacterInfo.JobLevel
	BaseExp      uint64 // base_exp → CharacterInfo.Exp
	JobExp       uint64 // job_exp  → CharacterInfo.JobExp
	Zeny         uint32 // zeny     → CharacterInfo.Money
	Str          uint16
	Agi          uint16
	Vit          uint16
	Int          uint16
	Dex          uint16
	Luk          uint16
	MaxHP        uint32
	HP           uint32
	MaxSP        uint32
	SP           uint32
	StatusPoint  uint32 // status_point → CharacterInfo.SPPoint
	SkillPoint   uint32 // skill_point → CharacterInfo.JobPoint
	Option       uint32 // option → CharacterInfo.BodyState (effect bits)
	Karma        uint8  // karma  → CharacterInfo.Virtue
	Manner       uint16 // manner → CharacterInfo.Honor
	Hair         uint8  // hair        → CharacterInfo.Head
	HairColor    uint16 // hair_color  → CharacterInfo.HeadPalette + HairColor
	ClothesColor uint16 // clothes_color → CharacterInfo.BodyPalette
	Body         uint16 // body        → CharacterInfo.Body (sprite body override)
	Weapon       uint16
	Shield       uint16
	HeadTop      uint16
	HeadMid      uint16
	HeadBottom   uint16
	Robe         uint16
	LastMap      string // last_map → CharacterInfo.MapName
	LastX        uint16
	LastY        uint16
	Sex          uint8 // sex (wire byte: 0=F, 1=M) → CharacterInfo.Sex
	DeleteDate   uint32
	Moves        uint32
	Rename       uint32
}

// Progression is the levelable slice of a `char`-table row: exactly the columns
// that change through play (EXP, levels, zeny, status/skill points, HP/SP) and
// nothing else. It is the write surface for SaveProgression: a caller loads a
// Character, applies a play delta (EXP gained, a level-up), and hands back only
// these fields. Identity is passed separately (accountID, charID) so the write
// stays scoped by the trusted account — never a client-supplied id — and the
// appearance/identity columns (name, class, hair, last_map, …) are structurally
// outside what progression can touch.
type Progression struct {
	BaseExp     uint64 // base_exp
	JobExp      uint64 // job_exp
	BaseLevel   uint16 // base_level
	JobLevel    uint16 // job_level
	Zeny        uint32 // zeny
	StatusPoint uint32 // status_point
	SkillPoint  uint32 // skill_point
	HP          uint32 // hp
	MaxHP       uint32 // max_hp
	SP          uint32 // sp
	MaxSP       uint32 // max_sp
}

// ProgressionOf projects the levelable fields of a Character into a Progression.
// Combat (and future play use cases) load a Character, mutate the result, and
// pass it to SaveProgression; this helper fills the unchanged fields so the
// caller overrides only the ones its delta touched.
func ProgressionOf(c *Character) Progression {
	return Progression{
		BaseExp:     c.BaseExp,
		JobExp:      c.JobExp,
		BaseLevel:   c.BaseLevel,
		JobLevel:    c.JobLevel,
		Zeny:        c.Zeny,
		StatusPoint: c.StatusPoint,
		SkillPoint:  c.SkillPoint,
		HP:          c.HP,
		MaxHP:       c.MaxHP,
		SP:          c.SP,
		MaxSP:       c.MaxSP,
	}
}

// CharacterRepository is the outbound persistence port for characters. The GORM
// and in-memory adapters implement it.
type CharacterRepository interface {
	// ListByAccount returns every character owned by accountID, ordered by slot
	// so the char-list display is stable. An account with no characters returns
	// an empty (non-nil) slice and a nil error — that is an expected outcome,
	// not a fault.
	ListByAccount(ctx context.Context, accountID uint32) ([]Character, error)
	// GetBySlot returns the character at (accountID, slot). Returns
	// ErrCharacterNotFound when no row matches — the handler maps this to a
	// HC_REFUSE_ENTER or a silent drop depending on the flow.
	GetBySlot(ctx context.Context, accountID uint32, slot uint8) (*Character, error)
	// GetByID returns the character at (accountID, charID). CZ_ENTER carries the
	// char_id (not the slot), so the map-enter flow resolves the spawn appearance
	// + last position through this lookup. Returns ErrCharacterNotFound when no
	// row matches — the world spawn use case treats a mismatch as a stale or
	// replayed enter and drops the connection.
	GetByID(ctx context.Context, accountID uint32, charID uint32) (*Character, error)
	// Create inserts a new character for the account at the requested slot,
	// applying the server-side novice defaults (base stats, starting HP/SP,
	// status points, zeny, position). It validates name uniqueness and slot
	// availability and returns a typed sentinel (ErrCharNameTaken,
	// ErrSlotOccupied, ErrInvalidSlot, ErrInvalidInput) on client-controlled
	// rejection, so the handler can map it to the right HC_REFUSE_MAKECHAR byte
	// without inspecting the underlying message.
	Create(ctx context.Context, in CreateCharacter) (*Character, error)
	// SaveProgression writes the levelable columns (EXP, levels, zeny,
	// status/skill points, HP/SP) for the character at (accountID, charID). It
	// is a column-selective update: appearance, identity, and position columns
	// are untouched. Scoping by accountID is the impersonation guard — a charID
	// belonging to another account yields ErrCharacterNotFound, never a cross-
	// account write. RowsAffected==0 (the char does not exist under that
	// account) is reported as ErrCharacterNotFound.
	SaveProgression(ctx context.Context, accountID, charID uint32, p Progression) error
	// SavePosition writes the location columns (last_map, last_x, last_y) for the
	// character at (accountID, charID) after a warp/teleport. These are
	// deliberately outside Progression (identity/location, not levelable), so a
	// map change needs its own column-selective write. Scoping by accountID is the
	// same impersonation guard as SaveProgression; RowsAffected==0 is reported as
	// ErrCharacterNotFound.
	SavePosition(ctx context.Context, accountID, charID uint32, mapName string, x, y uint16) error
}
