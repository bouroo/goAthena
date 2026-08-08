// Package domain holds the account bounded context's pure domain model: the
// Account aggregate (mirrors the rAthena `login` table), value objects, and the
// authentication port other contexts depend on. No infrastructure imports here.
package domain

import (
	"errors"
	"time"
)

// AccountID is the rAthena account_id (auto-increment from 2000000).
type AccountID uint32

// Sex is the rAthena account sex enum: M, F, or S (server).
type Sex string

// rAthena sex enum wire values.
const (
	SexMale   Sex = "M"
	SexFemale Sex = "F"
	SexServer Sex = "S"
)

// Account mirrors the rAthena `login` table (sql-files/main.sql). Column tags
// match the legacy schema exactly so GORM reads/writes stay compatible; the
// schema itself is owned by golang-migrate, never AutoMigrate.
type Account struct {
	ID             AccountID  `gorm:"column:account_id;primaryKey;autoIncrement"`
	UserID         string     `gorm:"column:userid"`
	UserPass       string     `gorm:"column:user_pass"`
	Sex            Sex        `gorm:"column:sex"`
	Email          string     `gorm:"column:email"`
	GroupID        int8       `gorm:"column:group_id"`
	State          uint32     `gorm:"column:state"`
	UnbanTime      uint32     `gorm:"column:unban_time"`
	ExpirationTime uint32     `gorm:"column:expiration_time"`
	LoginCount     uint32     `gorm:"column:logincount"`
	LastLogin      *time.Time `gorm:"column:lastlogin"`
	LastIP         string     `gorm:"column:last_ip"`
	Birthdate      *time.Time `gorm:"column:birthdate"`
	CharacterSlots uint8      `gorm:"column:character_slots"`
	Pincode        string     `gorm:"column:pincode"`
	VipTime        uint32     `gorm:"column:vip_time"`
}

// TableName fixes the legacy table name so GORM does not pluralize it.
func (Account) TableName() string { return "login" }

// IsBanned reports whether the account is currently blocked from login.
func (a Account) IsBanned() bool { return a.State != 0 || a.UnbanTime != 0 }

// errors  are sentinel auth outcomes mapped to AC_REFUSE_LOGIN codes on the
// wire. The values mirror rAthena's REFUSE_* enum (packets.hpp).
var (
	ErrAccountNotFound = errors.New("account not found") // -> REFUSE_INVALID_ID
	ErrInvalidPassword = errors.New("invalid password")  // -> REFUSE_INVALID_PASSWD
	ErrAccountBanned   = errors.New("account banned")    // -> REFUSE_BLOCKED
)
