// Package domain holds the character bounded context's pure domain model: the
// Character aggregate (mirrors the rAthena `char` table) and the repository +
// session ports. No infrastructure imports here.
//
// NOTE: the `char` table has two MariaDB-reserved column names — `int` (the INT
// stat) and `rename` (the rename counter). GORM column tags keep them mapped
// correctly, but any raw SELECT must back-quote them or MariaDB errors with
// 1064 (only caught at L3 against a real DB).
package domain

import (
	"context"
	"errors"
)

// CharID is the rAthena char_id (auto-increment).
type CharID uint32

// Character mirrors the rAthena `char` table (sql-files/main.sql). Column tags
// match the legacy schema exactly; the schema is owned by golang-migrate.
type Character struct {
	ID           CharID `gorm:"column:char_id;primaryKey;autoIncrement"`
	AccountID    uint32 `gorm:"column:account_id"`
	CharNum      int8   `gorm:"column:char_num"`
	Name         string `gorm:"column:name"`
	Class        uint16 `gorm:"column:class"`
	BaseLevel    uint16 `gorm:"column:base_level"`
	JobLevel     uint16 `gorm:"column:job_level"`
	BaseExp      uint64 `gorm:"column:base_exp"`
	JobExp       uint64 `gorm:"column:job_exp"`
	Zeny         uint32 `gorm:"column:zeny"`
	Str          uint16 `gorm:"column:str"`
	Agi          uint16 `gorm:"column:agi"`
	Vit          uint16 `gorm:"column:vit"`
	Int          uint16 `gorm:"column:int"` // reserved word — keep quoted everywhere
	Dex          uint16 `gorm:"column:dex"`
	Luk          uint16 `gorm:"column:luk"`
	MaxHP        uint32 `gorm:"column:max_hp"`
	HP           uint32 `gorm:"column:hp"`
	MaxSP        uint32 `gorm:"column:max_sp"`
	SP           uint32 `gorm:"column:sp"`
	StatusPoint  uint32 `gorm:"column:status_point"`
	SkillPoint   uint32 `gorm:"column:skill_point"`
	Sex          int8   `gorm:"column:sex"`
	Hair         uint8  `gorm:"column:hair"`
	HairColor    uint16 `gorm:"column:hair_color"`
	ClothesColor uint16 `gorm:"column:clothes_color"`
	Weapon       uint16 `gorm:"column:weapon"`
	Shield       uint16 `gorm:"column:shield"`
	HeadTop      uint16 `gorm:"column:head_top"`
	HeadMid      uint16 `gorm:"column:head_mid"`
	HeadBottom   uint16 `gorm:"column:head_bottom"`
	LastMap      string `gorm:"column:last_map"`
	LastX        uint16 `gorm:"column:last_x"`
	LastY        uint16 `gorm:"column:last_y"`
	SaveMap      string `gorm:"column:save_map"`
	SaveX        uint16 `gorm:"column:save_x"`
	SaveY        uint16 `gorm:"column:save_y"`
	Fame         uint32 `gorm:"column:fame"`
	Rename       uint16 `gorm:"column:rename"` // reserved word
	DeleteDate   uint32 `gorm:"column:delete_date"`
}

// TableName fixes the legacy table name so GORM does not pluralize it.
func (Character) TableName() string { return "char" }

// Sentinel errors for character use cases.
var (
	// ErrCharacterNotFound is returned when no character exists for the id/name.
	ErrCharacterNotFound = errors.New("character not found")
	// ErrNameTaken is returned when a create/rename collides with an existing name.
	ErrNameTaken = errors.New("character name already taken")
	// ErrSlotTaken is returned when a create targets an occupied slot.
	ErrSlotTaken = errors.New("character slot already occupied")
	// ErrInvalidSession is returned when a CH_ENTER's LoginID1 doesn't match.
	ErrInvalidSession = errors.New("invalid login session")
)

// CharacterRepository is the persistence port for the character aggregate.
type CharacterRepository interface {
	// ListByAccount returns all characters owned by an account, slot-ordered.
	ListByAccount(ctx context.Context, accountID uint32) ([]Character, error)
	// Create inserts a new character at the given slot.
	Create(ctx context.Context, c Character) (Character, error)
	// Delete removes a character by id (and its owner check).
	Delete(ctx context.Context, id CharID, accountID uint32) error
	// NameExists reports whether a name is already in use.
	NameExists(ctx context.Context, name string) (bool, error)
	// UpdateZeny sets the zeny balance for charID.
	UpdateZeny(ctx context.Context, id CharID, zeny uint32) error
	// FindByID returns the character by char_id.
	FindByID(ctx context.Context, id CharID) (Character, error)
}

// SessionStore is the port for the login session handed off from the login
// server to the char server. The char server validates a CH_ENTER against this
// before serving the character list.
type SessionStore interface {
	// PutSession stores the login session (AID + loginID1 + loginID2 + sex).
	PutSession(ctx context.Context, s Session) error
	// GetSession returns the session for an account, or ErrSessionNotFound.
	GetSession(ctx context.Context, accountID uint32) (Session, error)
	// DeleteSession removes a session (after it's consumed into the char/map flow).
	DeleteSession(ctx context.Context, accountID uint32) error
}

// Session carries the login handshake state char/map servers need to validate
// the client's CH_ENTER.
type Session struct {
	AccountID uint32
	LoginID1  uint32
	LoginID2  uint32
	Sex       uint8
}

// ErrSessionNotFound is returned when no session exists for an account.
var ErrSessionNotFound = errors.New("session not found")
