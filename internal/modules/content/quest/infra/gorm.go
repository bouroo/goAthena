// Package infra adapts the quest domain to its GORM repository.
package infra

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/bouroo/goAthena/internal/modules/content/quest/domain"
)

// GORMQuestRepository is the production quest repository over GORM.
type GORMQuestRepository struct {
	db *gorm.DB
}

// NewGORMQuestRepository wraps an open *gorm.DB.
func NewGORMQuestRepository(db *gorm.DB) *GORMQuestRepository {
	return &GORMQuestRepository{db: db}
}

// Get returns the row at the (charID, npcName, varName) key, or false. A
// query error other than ErrRecordNotFound is returned to the caller.
func (r *GORMQuestRepository) Get(ctx context.Context, charID uint32, npcName, varName string) (domain.QuestVar, bool, error) {
	var row domain.QuestVar
	err := r.db.WithContext(ctx).
		Where("char_id = ? AND npc_name = ? AND var_name = ?", charID, npcName, varName).
		First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return domain.QuestVar{}, false, nil
		}
		return domain.QuestVar{}, false, fmt.Errorf("quest get: %w", err)
	}
	return row, true, nil
}

// Set upserts the row, replacing the existing value. Matches rAthena's
// quest table semantics: no version column, last-writer-wins.
func (r *GORMQuestRepository) Set(ctx context.Context, charID uint32, npcName, varName, value string) error {
	row := domain.QuestVar{
		CharID:  charID,
		NpcName: npcName,
		VarName: varName,
		Value:   value,
	}
	// gorm.io/gorm/clause.OnConflict emits ON CONFLICT DO UPDATE (postgres) /
	// ON DUPLICATE KEY UPDATE (mariadb) — the rAthena semantics for the
	// quest table. The "updated_at" column is touched by the DB's
	// ON UPDATE CURRENT_TIMESTAMP (mariadb) or by the engine on postgres.
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "char_id"}, {Name: "npc_name"}, {Name: "var_name"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"value": row.Value,
		}),
	}).Create(&row).Error
}

// ListByChar returns every row owned by charID.
func (r *GORMQuestRepository) ListByChar(ctx context.Context, charID uint32) ([]domain.QuestVar, error) {
	var rows []domain.QuestVar
	err := r.db.WithContext(ctx).
		Where("char_id = ?", charID).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("quest list: %w", err)
	}
	return rows, nil
}
