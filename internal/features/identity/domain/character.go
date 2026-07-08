package domain

import "time"

// CharacterSummary is the subset of the `char` table returned in the
// character-list response (HC_ACCEPT_ENTER 0x6b). Full character state
// (inventory, status, equipment, scripts) is loaded lazily by the zone
// service on map enter; this struct intentionally carries only the fields
// the lobby UI needs to render the roster.
type CharacterSummary struct {
	// CharID is the numeric primary key (`char_id`).
	CharID uint32
	// AccountID is the owning account (`account_id`).
	AccountID uint32
	// Slot is the per-account position, 0..MAX_CHARS-1 (`char_num`).
	Slot uint8
	// Name is the character name, max NAME_LENGTH (30) bytes (`name`).
	Name string
	// Class is the job ID (`class`).
	Class uint16
	// BaseLevel is the character's base level (`base_level`).
	BaseLevel uint32
	// JobLevel is the character's job level (`job_level`).
	JobLevel uint32
	// BaseExp is the accumulated base experience (`base_exp`).
	BaseExp uint64
	// JobExp is the accumulated job experience (`job_exp`).
	JobExp uint64
	// Zeny is the on-hand currency (`zeny`).
	Zeny uint32
	// HP is the current HP (`hp`).
	HP uint32
	// MaxHP is the maximum HP (`max_hp`).
	MaxHP uint32
	// SP is the current SP (`sp`).
	SP uint32
	// MaxSP is the maximum SP (`max_sp`).
	MaxSP uint32
	// Hair is the hair style ID (`hair`).
	Hair uint16
	// HairColor is the hair palette index (`hair_color`).
	HairColor uint16
	// ClothesColor is the body/clothes dye palette index (`clothes_color`).
	ClothesColor uint16
	// Weapon is the equipped weapon view ID (`weapon`).
	Weapon uint16
	// Shield is the equipped shield view ID (`shield`).
	Shield uint16
	// HeadTop is the equipped top-headgear view ID (`head_top`).
	HeadTop uint16
	// HeadMid is the equipped mid-headgear view ID (`head_mid`).
	HeadMid uint16
	// HeadBottom is the equipped lower-headgear view ID (`head_bottom`).
	HeadBottom uint16
	// Robe is the equipped robe view ID (`robe`).
	Robe uint16
	// LastMap is the last map the character was on; max
	// MAP_NAME_LENGTH_EXT (16) bytes (`last_map`).
	LastMap string
	// DeleteDate is the scheduled deletion timestamp; the zero value means
	// the character is not pending deletion (`delete_date`).
	DeleteDate time.Time
	// UnbanTime is the per-character ban expiry; the zero value means OK
	// (`unban_time`).
	UnbanTime time.Time
	// Sex is the character's sex; falls back to the account sex when
	// not overridden (`sex`).
	Sex Sex
	// Str is the base strength stat (`str`).
	Str uint16
	// Agi is the base agility stat (`agi`).
	Agi uint16
	// Vit is the base vitality stat (`vit`).
	Vit uint16
	// Int is the base intelligence stat (`int`).
	Int uint16
	// Dex is the base dexterity stat (`dex`).
	Dex uint16
	// Luk is the base luck stat (`luk`).
	Luk uint16
	// StatusPoint is the unspent status points available to allocate
	// into base stats (`status_point`).
	StatusPoint uint32
	// SkillPoint is the unspent skill points available to allocate
	// into job skills (`skill_point`).
	SkillPoint uint32
}
