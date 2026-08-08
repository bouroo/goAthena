// Package infra adapts the character domain to its GORM repository and Valkey
// session store.
package infra

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/bouroo/goAthena/internal/modules/character/domain"
)

// GORMCharacterRepository is the production character repository over GORM.
type GORMCharacterRepository struct {
	db *gorm.DB
}

// NewGORMCharacterRepository wraps an open *gorm.DB.
func NewGORMCharacterRepository(db *gorm.DB) *GORMCharacterRepository {
	return &GORMCharacterRepository{db: db}
}

// ListByAccount returns the account's characters ordered by slot.
func (r *GORMCharacterRepository) ListByAccount(ctx context.Context, accountID uint32) ([]domain.Character, error) {
	var chars []domain.Character
	err := r.db.WithContext(ctx).
		Where("account_id = ?", accountID).
		Order("char_num").
		Find(&chars).Error
	return chars, err
}

// Create inserts a new character row and returns it with the assigned ID.
func (r *GORMCharacterRepository) Create(ctx context.Context, c domain.Character) (domain.Character, error) {
	if err := r.db.WithContext(ctx).Create(&c).Error; err != nil {
		return domain.Character{}, err
	}
	return c, nil
}

// Delete removes a character owned by accountID. A no-row delete is not an
// error (idempotent), but returns ErrCharacterNotFound if the caller needs it.
func (r *GORMCharacterRepository) Delete(ctx context.Context, id domain.CharID, accountID uint32) error {
	res := r.db.WithContext(ctx).
		Where("char_id = ? AND account_id = ?", id, accountID).
		Delete(&domain.Character{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return domain.ErrCharacterNotFound
	}
	return nil
}

// NameExists reports whether a name is in use.
func (r *GORMCharacterRepository) NameExists(ctx context.Context, name string) (bool, error) {
	var c domain.Character
	err := r.db.WithContext(ctx).Select("char_id").Where("name = ?", name).First(&c).Error
	if err == nil {
		return true, nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	return false, err
}
