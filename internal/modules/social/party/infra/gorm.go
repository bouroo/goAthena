// Package infra adapts the party domain to its GORM repository.
package infra

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"github.com/bouroo/goAthena/internal/modules/social/party/domain"
)

// GORMPartyRepository is the production party repository over GORM.
//
// Two tables are involved: `party` (one row per group) and `char` (whose
// `party_id` column IS the membership record — rAthena has no join table). Every
// membership mutation therefore touches both and runs inside one transaction.
type GORMPartyRepository struct {
	db *gorm.DB
}

// NewGORMPartyRepository wraps an open *gorm.DB.
func NewGORMPartyRepository(db *gorm.DB) *GORMPartyRepository {
	return &GORMPartyRepository{db: db}
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
	PartyID   uint32 `gorm:"column:party_id"`
}

func (charRow) TableName() string { return "char" }

// Create inserts the party row and sets the leader's char.party_id atomically.
func (r *GORMPartyRepository) Create(ctx context.Context, leaderAccountID, leaderCharID uint32, name string) (domain.Party, error) {
	var out domain.Party
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// One party per char (rAthena's sd.status.party_id > 0 guard,
		// src/map/party.cpp:151) and the char must exist. Checked in-transaction
		// so a concurrent join cannot slip between the read and the write; the
		// check is advisory rather than enforced by a DB constraint, so two
		// simultaneous creates for the same char remain a theoretical race.
		var cur charRow
		if err := tx.Select("char_id", "party_id").Where("char_id = ?", leaderCharID).
			First(&cur).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("party create: char %d not found", leaderCharID)
			}
			return err
		}
		if cur.PartyID != 0 {
			return domain.ErrAlreadyInParty
		}
		p := domain.Party{
			Name:       name,
			LeaderID:   leaderAccountID,
			LeaderChar: leaderCharID,
		}
		if err := tx.Create(&p).Error; err != nil {
			return err
		}
		if err := tx.Table("char").Where("char_id = ?", leaderCharID).
			Update("party_id", uint32(p.ID)).Error; err != nil {
			return err
		}
		out = p
		return nil
	})
	if err != nil {
		return domain.Party{}, fmt.Errorf("party create: %w", err)
	}
	return out, nil
}

// Get returns the party row.
func (r *GORMPartyRepository) Get(ctx context.Context, id domain.PartyID) (domain.Party, error) {
	var p domain.Party
	err := r.db.WithContext(ctx).Where("party_id = ?", id).First(&p).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return domain.Party{}, domain.ErrPartyNotFound
		}
		return domain.Party{}, err
	}
	return p, nil
}

// GetByMember returns the party the char belongs to.
func (r *GORMPartyRepository) GetByMember(ctx context.Context, charID uint32) (domain.Party, error) {
	var cur charRow
	if err := r.db.WithContext(ctx).Select("char_id", "party_id").
		Where("char_id = ?", charID).First(&cur).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return domain.Party{}, domain.ErrNotInParty
		}
		return domain.Party{}, err
	}
	if cur.PartyID == 0 {
		return domain.Party{}, domain.ErrNotInParty
	}
	return r.Get(ctx, domain.PartyID(cur.PartyID))
}

// Members returns the roster ordered by char_id, with Leader derived from the
// party's leader pair — the same derivation as
// `SELECT ... FROM char WHERE party_id = ?` + a leader_id/leader_char compare
// (src/char/int_party.cpp:237-250).
func (r *GORMPartyRepository) Members(ctx context.Context, id domain.PartyID) ([]domain.PartyMember, error) {
	p, err := r.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	var rows []charRow
	if err := r.db.WithContext(ctx).
		Select("char_id", "account_id", "name", "last_map", "class", "base_level", "online", "party_id").
		Where("party_id = ?", id).
		Order("char_id").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]domain.PartyMember, 0, len(rows))
	for _, cr := range rows {
		out = append(out, domain.PartyMember{
			CharID:    cr.CharID,
			AccountID: cr.AccountID,
			Name:      cr.Name,
			Map:       cr.LastMap,
			Class:     cr.Class,
			BaseLevel: cr.BaseLevel,
			Online:    cr.Online != 0,
			Leader:    cr.AccountID == p.LeaderID && cr.CharID == p.LeaderChar,
		})
	}
	return out, nil
}

// AddMember joins a char to the party, setting char.party_id.
func (r *GORMPartyRepository) AddMember(ctx context.Context, id domain.PartyID, charID uint32) error {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var partyCount int64
		if err := tx.Model(&domain.Party{}).Where("party_id = ?", id).
			Count(&partyCount).Error; err != nil {
			return err
		}
		if partyCount == 0 {
			return domain.ErrPartyNotFound
		}
		var cur charRow
		if err := tx.Select("char_id", "party_id").Where("char_id = ?", charID).
			First(&cur).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return domain.ErrNotInParty
			}
			return err
		}
		if cur.PartyID != 0 {
			return domain.ErrAlreadyInParty
		}
		var n int64
		if err := tx.Model(&charRow{}).Where("party_id = ?", id).Count(&n).Error; err != nil {
			return err
		}
		if int(n) >= domain.MaxPartySize {
			return domain.ErrPartyFull
		}
		return tx.Table("char").Where("char_id = ?", charID).
			Update("party_id", uint32(id)).Error
	})
	if err != nil {
		return fmt.Errorf("party add member: %w", err)
	}
	return nil
}

// RemoveMember clears the char's party_id. When the departing char is the
// leader the entire party is disbanded — every remaining member's party_id is
// cleared and the party row deleted, matching rAthena's
// mapif_parse_PartyLeave leader branch (src/char/int_party.cpp:651-676).
func (r *GORMPartyRepository) RemoveMember(ctx context.Context, id domain.PartyID, charID uint32) error {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var p domain.Party
		if err := tx.Where("party_id = ?", id).First(&p).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return domain.ErrPartyNotFound
			}
			return err
		}
		var cur charRow
		if err := tx.Select("char_id", "account_id", "party_id").
			Where("char_id = ?", charID).First(&cur).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return domain.ErrNotInParty
			}
			return err
		}
		if cur.PartyID != uint32(id) {
			return domain.ErrNotInParty
		}
		if cur.AccountID == p.LeaderID && cur.CharID == p.LeaderChar {
			if err := tx.Table("char").Where("party_id = ?", uint32(id)).
				Update("party_id", 0).Error; err != nil {
				return err
			}
			return tx.Where("party_id = ?", id).Delete(&domain.Party{}).Error
		}
		return tx.Table("char").Where("char_id = ?", charID).
			Update("party_id", 0).Error
	})
	if err != nil {
		return fmt.Errorf("party remove member: %w", err)
	}
	return nil
}

// SetOptions writes the exp/item share rules.
func (r *GORMPartyRepository) SetOptions(ctx context.Context, id domain.PartyID, exp, item uint8) error {
	res := r.db.WithContext(ctx).Model(&domain.Party{}).Where("party_id = ?", id).
		Updates(map[string]any{"exp": exp, "item": item})
	if res.Error != nil {
		return fmt.Errorf("party set options: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return domain.ErrPartyNotFound
	}
	return nil
}
