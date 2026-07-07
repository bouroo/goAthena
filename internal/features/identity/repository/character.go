package repository

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"github.com/bouroo/goAthena/internal/features/identity/domain"
)

// charSelectColumns is the column whitelist for the lobby roster query
// (HC_ACCEPT_ENTER 0x6b). Only fields the lobby needs are pulled; the
// full row is wide (~80 cols) and is loaded lazily by the zone service.
const charSelectColumns = "char_id, account_id, char_num, name, class, base_level, job_level, " +
	"base_exp, job_exp, zeny, max_hp, hp, max_sp, sp, hair, hair_color, clothes_color, " +
	"weapon, shield, head_top, head_mid, head_bottom, robe, last_map, " +
	"delete_date, unban_time, sex"

type characterRepo struct {
	db *gorm.DB
}

// NewCharacterRepository wires the GORM-backed CharacterRepository.
func NewCharacterRepository(db *gorm.DB) domain.CharacterRepository {
	return &characterRepo{db: db}
}

// ListByAccount returns the characters for an account, ordered by slot
// ascending. The query caps rows at maxSlots so accounts with a
// superseding `character_slots` value (from a VIP upgrade) get the wider
// window, but we never silently surface rows outside the union of the
// effective and ceiling slot counts.
//
// The `char_num < ?` predicate is the canonical rAthena form (see
// inter.cpp::char_clif_top): rows with char_num beyond the slot ceiling
// are tombstoned by the upgrade flow and must not enter the roster.
func (r *characterRepo) ListByAccount(ctx context.Context, accountID uint32, maxSlots int) ([]domain.CharacterSummary, error) {
	if maxSlots <= 0 {
		// Defensive: a misconfigured caller must not produce an
		// unbounded query. We surface zero rows and a wrapped error
		// so the service layer can decide whether to retry with the
		// MIN_CHARS default.
		return nil, fmt.Errorf("list characters for account %d: maxSlots must be > 0, got %d", accountID, maxSlots)
	}
	var models []CharModel
	err := r.db.WithContext(ctx).
		Select(charSelectColumns).
		Where("account_id = ? AND char_num < ?", accountID, maxSlots).
		Order("char_num ASC").
		Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("list characters for account %d: %w", accountID, err)
	}
	out := make([]domain.CharacterSummary, 0, len(models))
	for i := range models {
		out = append(out, charModelToDomain(&models[i]))
	}
	return out, nil
}

// GetByID fetches a single character by (accountID, charID). The
// compound key is the canonical rAthena ownership predicate: char_id
// alone is not unique to an account (rAthena mints a fresh id range
// per account, but a misconfigured DB or a client-supplied charID from
// another account must not read across the boundary), so we always
// pin both.
//
// Returns domain.ErrCharacterNotFound when no row matches; the
// service layer treats that as a soft failure (the gateway falls back
// to a zero-filled spawn packet) rather than a hard gRPC error.
func (r *characterRepo) GetByID(ctx context.Context, accountID, charID uint32) (*domain.CharacterSummary, error) {
	if accountID == 0 || charID == 0 {
		// Defensive: a zero key can never match a real rAthena row
		// (auto-increment starts at 150000+), so the query is doomed
		// to miss. Fail fast with the sentinel error before paying
		// for a round-trip.
		return nil, fmt.Errorf("get character (account=%d, char=%d): %w", accountID, charID, domain.ErrCharacterNotFound)
	}
	var model CharModel
	err := r.db.WithContext(ctx).
		Select(charSelectColumns).
		Where("account_id = ? AND char_id = ?", accountID, charID).
		Take(&model).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("get character (account=%d, char=%d): %w", accountID, charID, domain.ErrCharacterNotFound)
		}
		return nil, fmt.Errorf("get character (account=%d, char=%d): %w", accountID, charID, err)
	}
	out := charModelToDomain(&model)
	return &out, nil
}

// charModelToDomain maps a CharModel row to its domain summary.
func charModelToDomain(m *CharModel) domain.CharacterSummary {
	if m == nil {
		return domain.CharacterSummary{}
	}
	return domain.CharacterSummary{
		CharID:    m.CharID,
		AccountID: m.AccountID,
		//nolint:gosec // G115: schema is tinyint(1) NOT NULL DEFAULT 0; widens to uint8 for the domain.
		Slot:         uint8(m.CharNum),
		Name:         m.Name,
		Class:        m.Class,
		BaseLevel:    m.BaseLevel,
		JobLevel:     m.JobLevel,
		BaseExp:      m.BaseExp,
		JobExp:       m.JobExp,
		Zeny:         m.Zeny,
		HP:           m.HP,
		MaxHP:        m.MaxHP,
		SP:           m.SP,
		MaxSP:        m.MaxSP,
		Hair:         uint16(m.Hair),
		HairColor:    m.HairColor,
		ClothesColor: m.ClothesColor,
		Weapon:       m.Weapon,
		Shield:       m.Shield,
		HeadTop:      m.HeadTop,
		HeadMid:      m.HeadMid,
		HeadBottom:   m.HeadBottom,
		Robe:         m.Robe,
		LastMap:      m.LastMap,
		DeleteDate:   unixOrZero(m.DeleteDate),
		UnbanTime:    unixOrZero(m.UnbanTime),
		Sex:          m.Sex,
	}
}
