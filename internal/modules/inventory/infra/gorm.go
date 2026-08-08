// Package infra adapts the inventory domain to its GORM repository.
package infra

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/bouroo/goAthena/internal/modules/inventory/domain"
)

// GORMItemRepository is the production inventory repository over GORM.
type GORMItemRepository struct {
	db *gorm.DB
}

// NewGORMItemRepository wraps an open *gorm.DB.
func NewGORMItemRepository(db *gorm.DB) *GORMItemRepository {
	return &GORMItemRepository{db: db}
}

// LoadByChar returns all inventory rows owned by charID, ordered by id.
func (r *GORMItemRepository) LoadByChar(ctx context.Context, _, charID uint32) ([]domain.Item, error) {
	var items []domain.Item
	err := r.db.WithContext(ctx).
		Where("char_id = ?", charID).
		Order("id").
		Find(&items).Error
	return items, err
}

// Add inserts a new item row (equipment always separate; stackable items stack).
//
//nolint:gosec // G115: amount is validated >0 before the uint32 cast.
func (r *GORMItemRepository) Add(ctx context.Context, charID, nameID uint32, amount int) (domain.Item, error) {
	if amount <= 0 {
		return domain.Item{}, domain.ErrInsufficientAmount
	}
	item := domain.Item{CharID: charID, NameID: nameID, Amount: uint32(amount), Identify: 1}
	if err := r.db.WithContext(ctx).Create(&item).Error; err != nil {
		return domain.Item{}, err
	}
	return item, nil
}

// Remove decrements amount and deletes the row when it hits zero.
//
//nolint:gosec // G115: amount validated >0 before the uint32 cast.
func (r *GORMItemRepository) Remove(ctx context.Context, id domain.ItemID, amount int) error {
	if amount <= 0 {
		return domain.ErrInsufficientAmount
	}
	var item domain.Item
	if err := r.db.WithContext(ctx).First(&item, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return domain.ErrItemNotFound
		}
		return err
	}
	if int(item.Amount) < amount {
		return domain.ErrInsufficientAmount
	}
	if int(item.Amount) == amount {
		return r.db.WithContext(ctx).Delete(&domain.Item{}, id).Error
	}
	item.Amount -= uint32(amount)
	return r.db.WithContext(ctx).Model(&domain.Item{}).Where("id = ?", id).
		Update("amount", item.Amount).Error
}
