// Package repository provides GORM-backed implementations of the identity
// domain ports. Models in this file are an anti-corruption layer: they mirror
// the rAthena migration columns 1:1 (see
// internal/infrastructure/db/migrations/000002_identity.up.sql) and exist
// separately from the domain entities so the domain stays decoupled from
// the persistence schema.
package repository

import (
	"time"

	"github.com/bouroo/goAthena/internal/features/identity/domain"
)

// LoginModel maps to the rAthena `login` table. Column names match the
// migration verbatim; do not rename without a corresponding migration. We
// embed no `gorm.Model` mixin because the real schema's autoincrement
// starts at 2,000,000 and the table carries its own audit columns.
type LoginModel struct {
	AccountID           uint32     `gorm:"column:account_id;primaryKey;autoIncrement"`
	UserID              string     `gorm:"column:userid"`
	UserPass            string     `gorm:"column:user_pass"`
	Sex                 string     `gorm:"column:sex"`
	Email               string     `gorm:"column:email"`
	GroupID             int8       `gorm:"column:group_id"`
	State               uint32     `gorm:"column:state"`
	UnbanTime           int64      `gorm:"column:unban_time"`      // unix seconds, 0 = not banned
	ExpirationTime      int64      `gorm:"column:expiration_time"` // unix seconds, 0 = unlimited
	LoginCount          uint32     `gorm:"column:logincount"`
	LastLogin           *time.Time `gorm:"column:lastlogin"` // nullable
	LastIP              string     `gorm:"column:last_ip"`
	Birthdate           *time.Time `gorm:"column:birthdate"` // nullable DATE
	CharacterSlots      uint8      `gorm:"column:character_slots"`
	Pincode             string     `gorm:"column:pincode"`
	PincodeChange       uint32     `gorm:"column:pincode_change"`
	VipTime             int64      `gorm:"column:vip_time"`
	OldGroup            int8       `gorm:"column:old_group"`
	WebAuthToken        *string    `gorm:"column:web_auth_token"`         // nullable
	WebAuthTokenEnabled int8       `gorm:"column:web_auth_token_enabled"` // 0/1 soft-disable
}

// TableName pins the GORM table so AutoMigrate (we do not call it) and
// any future schema introspection use the rAthena-canonical name.
func (LoginModel) TableName() string { return "login" }

// CharModel maps to the rAthena `char` table. Only the columns used by the
// lobby roster (HC_ACCEPT_ENTER) are declared here; the full table is wide
// (~80 columns) and is loaded lazily by the zone service on map enter.
type CharModel struct {
	CharID        uint32     `gorm:"column:char_id;primaryKey;autoIncrement"`
	AccountID     uint32     `gorm:"column:account_id"`
	CharNum       int8       `gorm:"column:char_num"` // tinyint(1)
	Name          string     `gorm:"column:name"`
	Class         uint16     `gorm:"column:class"`
	BaseLevel     uint32     `gorm:"column:base_level"`
	JobLevel      uint32     `gorm:"column:job_level"`
	BaseExp       uint64     `gorm:"column:base_exp"`
	JobExp        uint64     `gorm:"column:job_exp"`
	Zeny          uint32     `gorm:"column:zeny"`
	MaxHP         uint32     `gorm:"column:max_hp"`
	HP            uint32     `gorm:"column:hp"`
	MaxSP         uint32     `gorm:"column:max_sp"`
	SP            uint32     `gorm:"column:sp"`
	Hair          uint8      `gorm:"column:hair"`
	HairColor     uint16     `gorm:"column:hair_color"`
	ClothesColor  uint16     `gorm:"column:clothes_color"`
	Weapon        uint16     `gorm:"column:weapon"`
	Shield        uint16     `gorm:"column:shield"`
	HeadTop       uint16     `gorm:"column:head_top"`
	HeadMid       uint16     `gorm:"column:head_mid"`
	HeadBottom    uint16     `gorm:"column:head_bottom"`
	Robe          uint16     `gorm:"column:robe"`
	LastMap       string     `gorm:"column:last_map"`
	DeleteDate    int64      `gorm:"column:delete_date"` // unix seconds, 0 = active
	UnbanTime     int64      `gorm:"column:unban_time"`  // unix seconds, 0 = ok
	Sex           domain.Sex `gorm:"column:sex"`         // ENUM('M','F')
	Online        int8       `gorm:"column:online"`
	LastLogin     *time.Time `gorm:"column:last_login"` // nullable
	ShowEquip     int8       `gorm:"column:show_equip"`
	DisableCall   int8       `gorm:"column:disable_call"`
	TitleID       uint32     `gorm:"column:title_id"`
	PartnerID     uint32     `gorm:"column:partner_id"`
	InventorySize int16      `gorm:"column:inventory_slots"`
	Fame          int32      `gorm:"column:fame"`
	Manner        int16      `gorm:"column:manner"`
}

// TableName pins the rAthena-canonical table name.
func (CharModel) TableName() string { return "char" }
