// Package infra provides the character bounded context's persistence adapters:
// an in-memory implementation (unit tests) and a GORM implementation backed by
// the `char` table (migration 000002_identity.up.sql). Both satisfy the domain
// ports.
package infra

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"github.com/bouroo/goAthena/internal/modules/character/domain"
)

// charModel maps the rAthena `char` table (migration 000002_identity.up.sql).
// Only the columns the char-list path reads are modeled; the table owns many
// more (party_id, guild_id, homun_id, hotkey_*, title_id, …) that CH_ENTER /
// CH_SELECT_CHAR do not touch. GORM maps by column name, so omitting them is
// safe — SELECTs name their columns. The SQL column `int` is a reserved word
// in Go; it maps to the field Int via an explicit gorm column tag.
type charModel struct {
	CharID       uint32 `gorm:"column:char_id;primaryKey"`
	AccountID    uint32 `gorm:"column:account_id"`
	Slot         uint8  `gorm:"column:char_num"`
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
	Int          uint16 `gorm:"column:int"`
	Dex          uint16 `gorm:"column:dex"`
	Luk          uint16 `gorm:"column:luk"`
	MaxHP        uint32 `gorm:"column:max_hp"`
	HP           uint32 `gorm:"column:hp"`
	MaxSP        uint32 `gorm:"column:max_sp"`
	SP           uint32 `gorm:"column:sp"`
	StatusPoint  uint32 `gorm:"column:status_point"`
	SkillPoint   uint32 `gorm:"column:skill_point"`
	Option       uint32 `gorm:"column:option"`
	Karma        uint8  `gorm:"column:karma"`
	Manner       uint16 `gorm:"column:manner"`
	Hair         uint8  `gorm:"column:hair"`
	HairColor    uint16 `gorm:"column:hair_color"`
	ClothesColor uint16 `gorm:"column:clothes_color"`
	Body         uint16 `gorm:"column:body"`
	Weapon       uint16 `gorm:"column:weapon"`
	Shield       uint16 `gorm:"column:shield"`
	HeadTop      uint16 `gorm:"column:head_top"`
	HeadMid      uint16 `gorm:"column:head_mid"`
	HeadBottom   uint16 `gorm:"column:head_bottom"`
	Robe         uint16 `gorm:"column:robe"`
	LastMap      string `gorm:"column:last_map"`
	LastX        uint16 `gorm:"column:last_x"`
	LastY        uint16 `gorm:"column:last_y"`
	Sex          string `gorm:"column:sex"`
	DeleteDate   uint32 `gorm:"column:delete_date"`
	Moves        uint32 `gorm:"column:moves"`
	Rename       uint32 `gorm:"column:rename"`
}

// TableName fixes the rAthena table name regardless of GORM's pluralizer.
func (charModel) TableName() string { return "char" }

// charColumns is the explicit SELECT list, keeping the query independent of the
// table's full column set and avoiding SELECT * (which would break if the
// table grew a column the model does not map). `int` and `rename` are MariaDB
// reserved words (INTEGER type and RENAME TABLE) and must be back-quoted or the
// SELECT is a syntax error; `option` is non-reserved in MariaDB and needs none.
const charColumns = "char_id, account_id, char_num, name, class, base_level, " +
	"job_level, base_exp, job_exp, zeny, str, agi, vit, `int`, dex, luk, " +
	"max_hp, hp, max_sp, sp, status_point, skill_point, option, karma, manner, " +
	"hair, hair_color, clothes_color, body, weapon, shield, head_top, head_mid, " +
	"head_bottom, robe, last_map, last_x, last_y, sex, delete_date, moves, `rename`"

func (m charModel) toDomain() domain.Character {
	return domain.Character{
		CharID:       m.CharID,
		AccountID:    m.AccountID,
		Slot:         m.Slot,
		Name:         m.Name,
		Class:        m.Class,
		BaseLevel:    m.BaseLevel,
		JobLevel:     m.JobLevel,
		BaseExp:      m.BaseExp,
		JobExp:       m.JobExp,
		Zeny:         m.Zeny,
		Str:          m.Str,
		Agi:          m.Agi,
		Vit:          m.Vit,
		Int:          m.Int,
		Dex:          m.Dex,
		Luk:          m.Luk,
		MaxHP:        m.MaxHP,
		HP:           m.HP,
		MaxSP:        m.MaxSP,
		SP:           m.SP,
		StatusPoint:  m.StatusPoint,
		SkillPoint:   m.SkillPoint,
		Option:       m.Option,
		Karma:        m.Karma,
		Manner:       m.Manner,
		Hair:         m.Hair,
		HairColor:    m.HairColor,
		ClothesColor: m.ClothesColor,
		Body:         m.Body,
		Weapon:       m.Weapon,
		Shield:       m.Shield,
		HeadTop:      m.HeadTop,
		HeadMid:      m.HeadMid,
		HeadBottom:   m.HeadBottom,
		Robe:         m.Robe,
		LastMap:      m.LastMap,
		LastX:        m.LastX,
		LastY:        m.LastY,
		Sex:          sexToWire(m.Sex),
		DeleteDate:   m.DeleteDate,
		Moves:        m.Moves,
		Rename:       m.Rename,
	}
}

// sexToWire maps the char.sex enum('M','F') to the wire byte CharacterInfo.Sex
// expects: 'F'→0 (SEX_FEMALE), 'M'→1 (SEX_MALE). Mirrors account.Sex.WireByte
// but lives here because the char table carries its own (non-'S') sex column.
func sexToWire(s string) uint8 {
	if s == "F" {
		return 0
	}
	return 1
}

// GORMCharacterRepository is the MariaDB/Postgres-backed
// domain.CharacterRepository.
type GORMCharacterRepository struct {
	db *gorm.DB
}

// NewGORMCharacterRepository binds the adapter to a GORM session. Each query
// re-derives its connection from the request context (WithContext).
func NewGORMCharacterRepository(db *gorm.DB) *GORMCharacterRepository {
	return &GORMCharacterRepository{db: db}
}

// ListByAccount returns the account's characters ordered by slot. An empty
// result is a normal "no characters" outcome, not an error.
func (r *GORMCharacterRepository) ListByAccount(ctx context.Context, accountID uint32) ([]domain.Character, error) {
	var rows []charModel
	err := r.db.WithContext(ctx).
		Select(charColumns).
		Where("account_id = ?", accountID).
		Order("char_num").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list characters for account %d: %w", accountID, err)
	}
	out := make([]domain.Character, len(rows))
	for i := range rows {
		out[i] = rows[i].toDomain()
	}
	return out, nil
}

// GetBySlot returns one character at (accountID, slot). Take (not First) avoids
// an implicit ORDER BY on a unique (account_id, char_num) lookup.
func (r *GORMCharacterRepository) GetBySlot(ctx context.Context, accountID uint32, slot uint8) (*domain.Character, error) {
	var m charModel
	err := r.db.WithContext(ctx).
		Select(charColumns).
		Where("account_id = ? AND char_num = ?", accountID, slot).
		Take(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, domain.ErrCharacterNotFound
		}
		return nil, fmt.Errorf("get character account %d slot %d: %w", accountID, slot, err)
	}
	c := m.toDomain()
	return &c, nil
}

// Create inserts a new character with the server-assigned novice defaults and
// returns the persisted row (char_id is the DB-assigned auto-increment). It
// guards name uniqueness and slot occupancy at the DB before insert and maps a
// lost concurrent-name race onto ErrCharNameTaken via the UNIQUE(name) backstop.
// The handler has already run the pure input validation; this method trusts
// that and concerns itself only with persistence-level integrity.
func (r *GORMCharacterRepository) Create(ctx context.Context, in domain.CreateCharacter) (*domain.Character, error) {
	name := normalizeCharName(in.Name)

	var hit charModel
	// Name uniqueness — char.name has a UNIQUE KEY. Default collation is
	// case-insensitive, matching rAthena's name_ignoring_case=false path.
	if err := r.db.WithContext(ctx).Select("char_id").Where("name = ?", name).Limit(1).Take(&hit).Error; err == nil {
		return nil, domain.ErrCharNameTaken
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("create char: check name %q: %w", name, err)
	}
	// Slot occupancy — no UNIQUE KEY on (account_id, char_num), so this is the
	// only guard for slot reuse.
	if err := r.db.WithContext(ctx).Select("char_id").
		Where("account_id = ? AND char_num = ?", in.AccountID, in.Slot).
		Limit(1).Take(&hit).Error; err == nil {
		return nil, domain.ErrSlotOccupied
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("create char: check slot account %d slot %d: %w", in.AccountID, in.Slot, err)
	}

	m := newNoviceModel(in, name)
	if err := r.db.WithContext(ctx).Create(&m).Error; err != nil {
		// Concurrent name-create raced the check above; the UNIQUE(name) key
		// is the authority. The mysql driver translates 1062 → ErrDuplicatedKey.
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, domain.ErrCharNameTaken
		}
		return nil, fmt.Errorf("create char account %d slot %d: %w", in.AccountID, in.Slot, err)
	}
	c := m.toDomain()
	return &c, nil
}

// Novice starting values, mirroring char_make_new_char (char.cpp:1504-1509) and
// the rAthena defaults start_status_points (inter_athena.conf:16 = 48),
// start_zeny (char_athena.conf:129 = 0), and the novice base/job levels. HP/SP
// follow the create formula 40*(100+vit)/100 and 11*(100+int)/100 with vit=int=1.
//
// startMap is Prontera for the combat slice, not rAthena's new_1-1/iz_int training
// grounds — the slice only ships Prontera, so a created character must spawn there.
const (
	noviceMaxHP        = 40
	noviceMaxSP        = 11
	noviceStatusPoints = 48
	noviceStartMap     = "prontera"
	noviceStartX       = 53
	noviceStartY       = 111
)

// newNoviceModel builds the char row for a freshly created character. Every
// column with a non-zero DB default (base_level, job_level, last_x/y) is set
// explicitly so GORM's full-row INSERT does not overwrite it with the struct
// zero. Columns the char-list path never reads (party_id, guild_id, save_map,
// …) are left to their DB defaults.
func newNoviceModel(in domain.CreateCharacter, name string) charModel {
	return charModel{
		AccountID:   in.AccountID,
		Slot:        in.Slot,
		Name:        name,
		Class:       uint16(in.Job), //nolint:gosec // G115: packet job→uint16 (char class/body columns are smallint), rAthena narrows on store
		BaseLevel:   1,
		JobLevel:    1,
		Zeny:        0,
		Str:         1,
		Agi:         1,
		Vit:         1,
		Int:         1,
		Dex:         1,
		Luk:         1,
		MaxHP:       noviceMaxHP,
		HP:          noviceMaxHP,
		MaxSP:       noviceMaxSP,
		SP:          noviceMaxSP,
		StatusPoint: noviceStatusPoints,
		SkillPoint:  0,
		Hair:        uint8(in.HairStyle), //nolint:gosec // G115: packet hair_style→uint8 (char hair column is tinyint), rAthena narrows on store
		HairColor:   in.HairColor,
		Body:        uint16(in.Job), //nolint:gosec // G115: packet job→uint16 (char class/body columns are smallint), rAthena narrows on store
		LastMap:     noviceStartMap,
		LastX:       noviceStartX,
		LastY:       noviceStartY,
		Sex:         wireToSex(in.Sex),
	}
}

// wireToSex is the inverse of sexToWire: the CH_MAKE_CHAR sex byte (0 = female,
// 1 = male) → the char.sex ENUM value the column stores. Mirrors
// char_make_new_char's SEX_FEMALE→'F' / SEX_MALE→'M' switch (char.cpp:1424-1430).
func wireToSex(sex uint8) string {
	if sex == 0 {
		return "F"
	}
	return "M"
}

// normalizeCharName trims surrounding whitespace, mirroring rAthena's
// normalize_name(name, TRIM_CHARS). Control-character and length validation is
// the handler's job (pure rules); the repository only stores the trimmed form.
func normalizeCharName(name string) string {
	return strings.TrimSpace(name)
}
