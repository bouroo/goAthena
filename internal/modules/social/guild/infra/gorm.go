// Package infra adapts the guild domain to its GORM repository.
package infra

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"github.com/bouroo/goAthena/internal/modules/social/guild/domain"
)

// GORMGuildRepository is the production guild repository over GORM.
//
// Two tables are involved: `guild` (one row per guild) and `char` (whose
// `guild_id` column is the membership record — the `guild_member` join table
// for per-member exp/position lands with guild-exp donation). Every membership
// mutation therefore touches both and runs inside one transaction.
type GORMGuildRepository struct {
	db *gorm.DB
}

// NewGORMGuildRepository wraps an open *gorm.DB.
func NewGORMGuildRepository(db *gorm.DB) *GORMGuildRepository {
	return &GORMGuildRepository{db: db}
}

// charRow is the minimal `char` projection the roster read needs. Kept local to
// avoid a dependency on the character module's domain.
type charRow struct {
	CharID    uint32 `gorm:"column:char_id"`
	AccountID uint32 `gorm:"column:account_id"`
	Name      string `gorm:"column:name"`
	LastMap   string `gorm:"column:last_map"`
	Class     uint16 `gorm:"column:class"`
	BaseLevel uint16 `gorm:"column:base_level"`
	Online    int    `gorm:"column:online"`
	GuildID   uint32 `gorm:"column:guild_id"`
}

func (charRow) TableName() string { return "char" }

// Create inserts the guild row and sets the master's char.guild_id atomically.
func (r *GORMGuildRepository) Create(ctx context.Context, _ uint32, masterCharID uint32, name string) (domain.Guild, error) {
	name = strings.TrimSpace(name)
	var out domain.Guild
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var cur charRow
		if err := tx.Select("char_id", "guild_id", "name").Where("char_id = ?", masterCharID).
			First(&cur).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("guild create: char %d not found", masterCharID)
			}
			return err
		}
		if cur.GuildID != 0 {
			return domain.ErrAlreadyInGuild
		}
		// Duplicate-name guard: rAthena rejects at inter-server create with
		// clif_guild_created flag 2 (guild.cpp:729).
		var dup int64
		if err := tx.Model(&domain.Guild{}).Where("name = ?", name).Count(&dup).Error; err != nil {
			return err
		}
		if dup > 0 {
			return domain.ErrNameExists
		}
		g := domain.Guild{
			Name:      name,
			Master:    masterCharID,
			MasterNam: cur.Name,
			GuildLv:   1,
			MaxMember: domain.MaxGuildSize,
			AverageLv: 1,
		}
		if err := tx.Create(&g).Error; err != nil {
			return err
		}
		if err := tx.Table("char").Where("char_id = ?", masterCharID).
			Update("guild_id", uint32(g.ID)).Error; err != nil {
			return err
		}
		out = g
		return nil
	})
	if err != nil {
		return domain.Guild{}, fmt.Errorf("guild create: %w", err)
	}
	return out, nil
}

// Get returns the guild row.
func (r *GORMGuildRepository) Get(ctx context.Context, id domain.GuildID) (domain.Guild, error) {
	var g domain.Guild
	err := r.db.WithContext(ctx).Where("guild_id = ?", id).First(&g).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return domain.Guild{}, domain.ErrGuildNotFound
		}
		return domain.Guild{}, err
	}
	return g, nil
}

// GetByMember returns the guild the char belongs to.
func (r *GORMGuildRepository) GetByMember(ctx context.Context, charID uint32) (domain.Guild, error) {
	var cur charRow
	if err := r.db.WithContext(ctx).Select("char_id", "guild_id").
		Where("char_id = ?", charID).First(&cur).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return domain.Guild{}, domain.ErrNotInGuild
		}
		return domain.Guild{}, err
	}
	if cur.GuildID == 0 {
		return domain.Guild{}, domain.ErrNotInGuild
	}
	return r.Get(ctx, domain.GuildID(cur.GuildID))
}

// Members returns the roster ordered by char_id, with Master derived against
// the guild's master char_id — the rAthena roster read plus master compare
// (src/char/int_guild.cpp:355 SELECT ... FROM char WHERE guild_id).
func (r *GORMGuildRepository) Members(ctx context.Context, id domain.GuildID) ([]domain.GuildMember, error) {
	g, err := r.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	var rows []charRow
	if err := r.db.WithContext(ctx).
		Select("char_id", "account_id", "name", "last_map", "class", "base_level", "online", "guild_id").
		Where("guild_id = ?", id).
		Order("char_id").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]domain.GuildMember, 0, len(rows))
	for _, cr := range rows {
		pos := int32(1)
		master := cr.CharID == g.Master
		if master {
			pos = 0
		}
		out = append(out, domain.GuildMember{
			CharID:    cr.CharID,
			AccountID: cr.AccountID,
			Name:      cr.Name,
			Map:       cr.LastMap,
			Class:     cr.Class,
			BaseLevel: cr.BaseLevel,
			Online:    cr.Online != 0,
			Master:    master,
			Position:  pos,
		})
	}
	return out, nil
}

// AddMember joins a char to the guild, setting char.guild_id.
func (r *GORMGuildRepository) AddMember(ctx context.Context, id domain.GuildID, charID uint32) error {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var g domain.Guild
		if err := tx.Where("guild_id = ?", id).First(&g).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return domain.ErrGuildNotFound
			}
			return err
		}
		var cur charRow
		if err := tx.Select("char_id", "guild_id").Where("char_id = ?", charID).
			First(&cur).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return domain.ErrNotInGuild
			}
			return err
		}
		if cur.GuildID != 0 {
			return domain.ErrAlreadyInGuild
		}
		var n int64
		if err := tx.Model(&charRow{}).Where("guild_id = ?", id).Count(&n).Error; err != nil {
			return err
		}
		if int(n) >= int(g.MaxMember) {
			return domain.ErrGuildFull
		}
		return tx.Table("char").Where("char_id = ?", charID).
			Update("guild_id", uint32(id)).Error
	})
	if err != nil {
		return fmt.Errorf("guild add member: %w", err)
	}
	return nil
}

// RemoveMember clears the char's guild_id. When the departing char was the
// last member the guild row is deleted — rAthena's guild_check_empty →
// mapif_parse_BreakGuild (src/char/int_guild.cpp:1353). A master leaving does
// NOT disband; the guild survives leaderless, matching rAthena.
func (r *GORMGuildRepository) RemoveMember(ctx context.Context, id domain.GuildID, charID uint32) error {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var g domain.Guild
		if err := tx.Where("guild_id = ?", id).First(&g).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return domain.ErrGuildNotFound
			}
			return err
		}
		var cur charRow
		if err := tx.Select("char_id", "guild_id").
			Where("char_id = ?", charID).First(&cur).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return domain.ErrNotInGuild
			}
			return err
		}
		if cur.GuildID != uint32(id) {
			return domain.ErrNotInGuild
		}
		if err := tx.Table("char").Where("char_id = ?", charID).
			Update("guild_id", 0).Error; err != nil {
			return err
		}
		var n int64
		if err := tx.Model(&charRow{}).Where("guild_id = ?", id).Count(&n).Error; err != nil {
			return err
		}
		if n == 0 {
			return tx.Where("guild_id = ?", id).Delete(&domain.Guild{}).Error
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("guild remove member: %w", err)
	}
	return nil
}

// Break tears the guild down: every member's guild_id is cleared and the guild
// row deleted, in one transaction. Validation (key, master, online members) is
// the service's.
func (r *GORMGuildRepository) Break(ctx context.Context, id domain.GuildID) error {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Table("char").Where("guild_id = ?", uint32(id)).
			Update("guild_id", 0).Error; err != nil {
			return err
		}
		return tx.Where("guild_id = ?", id).Delete(&domain.Guild{}).Error
	})
	if err != nil {
		return fmt.Errorf("guild break: %w", err)
	}
	return nil
}
