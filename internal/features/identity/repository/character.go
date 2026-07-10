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
	"delete_date, unban_time, sex, str, agi, vit, `int`, dex, luk, status_point, skill_point"

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
		Str:          m.Str,
		Agi:          m.Agi,
		Vit:          m.Vit,
		Int:          m.Int,
		Dex:          m.Dex,
		Luk:          m.Luk,
		StatusPoint:  m.StatusPoint,
		SkillPoint:   m.SkillPoint,
	}
}

// ApplyLevelUp atomically sets base_level=toLevel, adds grantedStatusPoints
// to status_point, and resets base_exp=0. The WHERE clause pins
// account_id, char_id, AND base_level=fromLevel (optimistic lock), so a
// concurrent level-up that already bumped base_level will fail (rows==0).
//
// After a successful update we re-read base_level and status_point to give
// the caller the authoritative values. When rows==0 we return
// (baseLevel=0, statusPoint=0, applied=false) so the caller can re-read and
// skip.
func (r *characterRepo) ApplyLevelUp(
	ctx context.Context,
	accountID, charID, fromLevel, toLevel, grantedStatusPoints uint32,
) (newLevel, newStatusPoint uint32, applied bool, err error) {
	if accountID == 0 || charID == 0 {
		return 0, 0, false, fmt.Errorf(
			"apply level up (account=%d, char=%d): %w", accountID, charID, domain.ErrCharacterNotFound)
	}

	res := r.db.WithContext(ctx).
		Model(&CharModel{}).
		Where("account_id = ? AND char_id = ? AND base_level = ?",
			accountID, charID, fromLevel).
		Updates(map[string]any{
			"base_level":   toLevel,
			"status_point": gorm.Expr("status_point + ?", grantedStatusPoints),
			"base_exp":     0,
		})
	if err := res.Error; err != nil {
		return 0, 0, false, fmt.Errorf(
			"apply level up (account=%d, char=%d): %w", accountID, charID, err)
	}

	if res.RowsAffected == 0 {
		// Distinguish a concurrent level-up (optimistic lock miss)
		// from a non-existent character. A missing row surfaces as
		// ErrCharacterNotFound so the caller can distinguish a
		// legitimate no-op from a genuine error.
		var exists CharModel
		existsErr := r.db.WithContext(ctx).
			Model(&CharModel{}).
			Where("account_id = ? AND char_id = ?", accountID, charID).
			Select("char_id").
			Take(&exists).Error
		if errors.Is(existsErr, gorm.ErrRecordNotFound) {
			return 0, 0, false, fmt.Errorf(
				"apply level up (account=%d, char=%d): %w", accountID, charID, domain.ErrCharacterNotFound)
		}
		if existsErr != nil {
			return 0, 0, false, fmt.Errorf(
				"apply level up (account=%d, char=%d): %w", accountID, charID, existsErr)
		}
		return 0, 0, false, nil
	}

	var model CharModel
	err = r.db.WithContext(ctx).
		Model(&CharModel{}).
		Where("account_id = ? AND char_id = ?", accountID, charID).
		Select("base_level", "status_point").
		Take(&model).Error
	if err != nil {
		return 0, 0, false, fmt.Errorf(
			"apply level up (account=%d, char=%d): %w", accountID, charID, err)
	}
	return model.BaseLevel, model.StatusPoint, true, nil
}

// AllocateStat atomically raises the named stat column by amount and
// deducts cost from status_point via a conditional UPDATE. The WHERE clause
// checks ownership (account_id+char_id) AND status_point>=cost AND
// stat+amount<=99 — if `rows == 0` the re-read distinguishes
// insufficient points from an over-cap stat (rathena pc.cpp:8872).
//
// statColumn is one of "str"|"agi"|"vit"|"int"|"dex"|"luk" and must have
// been validated by the caller (service layer). result: 0=applied,
// 1=insufficient_points, 2=stat_would_exceed_max.
func (r *characterRepo) AllocateStat(
	ctx context.Context,
	accountID, charID uint32,
	statColumn string,
	currentVal uint8,
	amount uint8,
	cost uint32,
) (newValue, newStatusPoint uint32, result int, err error) {
	if accountID == 0 || charID == 0 {
		return 0, 0, 0, fmt.Errorf(
			"allocate stat (account=%d, char=%d): %w", accountID, charID, domain.ErrCharacterNotFound)
	}

	newCol := gorm.Expr(statColumn+" + ?", amount)
	res := r.db.WithContext(ctx).
		Model(&CharModel{}).
		Where("account_id = ? AND char_id = ? AND "+statColumn+" = ? AND status_point >= ? AND "+statColumn+" + ? <= 99",
			accountID, charID, currentVal, cost, amount).
		Updates(map[string]any{
			statColumn:     newCol,
			"status_point": gorm.Expr("status_point - ?", cost),
		})

	if err := res.Error; err != nil {
		return 0, 0, 0, fmt.Errorf(
			"allocate stat (account=%d, char=%d): %w", accountID, charID, err)
	}

	var model CharModel
	err = r.db.WithContext(ctx).
		Model(&CharModel{}).
		Where("account_id = ? AND char_id = ?", accountID, charID).
		Select(statColumn, "status_point").
		Take(&model).Error
	if err != nil {
		return 0, 0, 0, fmt.Errorf(
			"allocate stat (account=%d, char=%d): %w", accountID, charID, err)
	}

	if res.RowsAffected > 0 {
		return getStatVal(&model, statColumn), model.StatusPoint, 0, nil
	}

	// rows == 0 — re-read to distinguish the failure mode.
	if model.StatusPoint < cost {
		return getStatVal(&model, statColumn), model.StatusPoint, 1, nil
	}
	// stat + amount > 99 (race or stale cache) — the client already has a
	// maxed stat so this is a no-op from its perspective.
	return getStatVal(&model, statColumn), model.StatusPoint, 2, nil
}

// getStatVal reads the stat column name from a CharModel. The columns are
// keyed by the exact table column in lowercase.
func getStatVal(m *CharModel, col string) uint32 {
	switch col {
	case "str":
		return uint32(m.Str)
	case "agi":
		return uint32(m.Agi)
	case "vit":
		return uint32(m.Vit)
	case "int":
		return uint32(m.Int)
	case "dex":
		return uint32(m.Dex)
	case "luk":
		return uint32(m.Luk)
	}
	return 0
}
