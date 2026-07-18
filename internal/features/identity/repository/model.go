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

// CharModel maps to the rAthena `char` table. All rAthena `char` columns
// (sql-files/main.sql:209-296) are declared here so GORM can read them
// 1:1 instead of silently zeroing missing fields. The domain layer reads
// only the subset it needs; the repository may expose more.
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
	Str           uint16     `gorm:"column:str"`
	Agi           uint16     `gorm:"column:agi"`
	Vit           uint16     `gorm:"column:vit"`
	Int           uint16     `gorm:"column:int"`
	Dex           uint16     `gorm:"column:dex"`
	Luk           uint16     `gorm:"column:luk"`
	Pow           uint16     `gorm:"column:pow"` // smallint(4) unsigned
	Sta           uint16     `gorm:"column:sta"`
	Wis           uint16     `gorm:"column:wis"`
	Spl           uint16     `gorm:"column:spl"`
	Con           uint16     `gorm:"column:con"`
	Crt           uint16     `gorm:"column:crt"`
	MaxHP         uint32     `gorm:"column:max_hp"`
	HP            uint32     `gorm:"column:hp"`
	MaxSP         uint32     `gorm:"column:max_sp"`
	SP            uint32     `gorm:"column:sp"`
	MaxAP         uint32     `gorm:"column:max_ap"` // int(11) unsigned
	AP            uint32     `gorm:"column:ap"`
	StatusPoint   uint32     `gorm:"column:status_point"`
	SkillPoint    uint32     `gorm:"column:skill_point"`
	TraitPoint    uint32     `gorm:"column:trait_point"`
	Option        int32      `gorm:"column:option"` // int(11) NOT NULL signed
	Karma         int8       `gorm:"column:karma"`  // tinyint(3) NOT NULL signed
	Manner        int16      `gorm:"column:manner"` // smallint(6) NOT NULL signed
	PartyID       uint32     `gorm:"column:party_id"`
	GuildID       uint32     `gorm:"column:guild_id"`
	PetID         uint32     `gorm:"column:pet_id"`
	HomunID       uint32     `gorm:"column:homun_id"`
	ElementalID   uint32     `gorm:"column:elemental_id"`
	Hair          uint8      `gorm:"column:hair"`
	HairColor     uint16     `gorm:"column:hair_color"`
	ClothesColor  uint16     `gorm:"column:clothes_color"`
	Body          uint16     `gorm:"column:body"` // smallint(5) unsigned
	Weapon        uint16     `gorm:"column:weapon"`
	Shield        uint16     `gorm:"column:shield"`
	HeadTop       uint16     `gorm:"column:head_top"`
	HeadMid       uint16     `gorm:"column:head_mid"`
	HeadBottom    uint16     `gorm:"column:head_bottom"`
	Robe          uint16     `gorm:"column:robe"`
	LastMap       string     `gorm:"column:last_map"`
	LastX         uint16     `gorm:"column:last_x"` // smallint(4) unsigned
	LastY         uint16     `gorm:"column:last_y"`
	LastInstance  uint32     `gorm:"column:last_instanceid"`
	SaveMap       string     `gorm:"column:save_map"`
	SaveX         uint16     `gorm:"column:save_x"`
	SaveY         uint16     `gorm:"column:save_y"`
	PartnerID     uint32     `gorm:"column:partner_id"`
	Online        int8       `gorm:"column:online"`
	Father        uint32     `gorm:"column:father"`
	Mother        uint32     `gorm:"column:mother"`
	Child         uint32     `gorm:"column:child"`
	Fame          uint32     `gorm:"column:fame"`
	Rename        uint16     `gorm:"column:rename"` // smallint(3) unsigned
	DeleteDate    int64      `gorm:"column:delete_date"` // unix seconds, 0 = active
	Moves         uint32     `gorm:"column:moves"`
	UnbanTime     int64      `gorm:"column:unban_time"` // unix seconds, 0 = ok
	Font          uint8      `gorm:"column:font"`        // tinyint(3) unsigned
	UniqueItemCnt uint32     `gorm:"column:uniqueitem_counter"`
	Sex           domain.Sex `gorm:"column:sex"` // ENUM('M','F')
	HotkeyShift   uint8      `gorm:"column:hotkey_rowshift"`
	HotkeyShift2  uint8      `gorm:"column:hotkey_rowshift2"`
	ClanID        uint32     `gorm:"column:clan_id"`
	LastLogin     *time.Time `gorm:"column:last_login"` // nullable
	TitleID       uint32     `gorm:"column:title_id"`
	ShowEquip     uint8      `gorm:"column:show_equip"` // tinyint(3) unsigned
	InventorySize int16      `gorm:"column:inventory_slots"`
	BodyDirection uint8      `gorm:"column:body_direction"`   // tinyint(1) unsigned
	DisableCall   uint8      `gorm:"column:disable_call"`      // tinyint(3) unsigned
	DisableParty  uint8      `gorm:"column:disable_partyinvite"` // tinyint(1) unsigned
	DisableCostume uint8     `gorm:"column:disable_showcostumes"` // tinyint(1) unsigned
}

// TableName pins the rAthena-canonical table name.
func (CharModel) TableName() string { return "char" }
