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

// ErrCharacterNotFound: no char row matched the lookup key.
var ErrCharacterNotFound = errors.New("character not found")

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
}
