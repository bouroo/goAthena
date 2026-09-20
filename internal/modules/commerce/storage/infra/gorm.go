// Package infra adapts the storage domain to its GORM repository.
package infra

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"github.com/bouroo/goAthena/internal/modules/commerce/storage/domain"
)

// GORMStorageRepository is the production storage repository over GORM.
type GORMStorageRepository struct {
	db *gorm.DB
}

// NewGORMStorageRepository wraps an open *gorm.DB.
func NewGORMStorageRepository(db *gorm.DB) *GORMStorageRepository {
	return &GORMStorageRepository{db: db}
}

// LoadByAccount returns all storage rows owned by accountID, ordered by id.
// The ORDER BY id is load-bearing: the warehouse index the client sends is
// an offset into this list.
func (r *GORMStorageRepository) LoadByAccount(ctx context.Context, accountID uint32) ([]domain.StorageItem, error) {
	var items []domain.StorageItem
	err := r.db.WithContext(ctx).
		Where("account_id = ?", accountID).
		Order("id").
		Find(&items).Error
	return items, err
}

// Add inserts a new row. Equipment always inserts a new row; stackable items
// (Equip==0) merge into an existing same-NameID row when one exists. Returns
// ErrStorageFull when the account is at the ceiling for a non-stacking insert.
//
// The merge-or-insert dance is wrapped in a transaction so two parallel
// deposits can't both fall into the "no existing row" branch and inflate
// the warehouse past MAX_STORAGE.
//
//nolint:gosec // G115: amount validated >0 before the uint32 cast.
func (r *GORMStorageRepository) Add(ctx context.Context, accountID, nameID uint32, amount int) (domain.StorageItem, error) {
	if amount <= 0 {
		return domain.StorageItem{}, domain.ErrStorageInsufficientAmount
	}
	var out domain.StorageItem
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Look for a stackable match first.
		var existing domain.StorageItem
		ferr := tx.Where("account_id = ? AND nameid = ? AND equip = 0", accountID, nameID).
			Order("id").First(&existing).Error
		if ferr == nil {
			existing.Amount += uint32(amount)
			return tx.Model(&domain.StorageItem{}).Where("id = ?", existing.ID).
				Update("amount", existing.Amount).Error
		}
		if !errors.Is(ferr, gorm.ErrRecordNotFound) {
			return ferr
		}
		// No stackable match → count rows and insert a new one if under the cap.
		var n int64
		if cerr := tx.Model(&domain.StorageItem{}).
			Where("account_id = ?", accountID).Count(&n).Error; cerr != nil {
			return cerr
		}
		if int(n) >= domain.MaxStorage {
			return domain.ErrStorageFull
		}
		item := domain.StorageItem{
			AccountID: accountID,
			NameID:    nameID,
			Amount:    uint32(amount),
			Identify:  1,
		}
		if cerr := tx.Create(&item).Error; cerr != nil {
			return cerr
		}
		out = item
		return nil
	})
	if err != nil {
		return domain.StorageItem{}, fmt.Errorf("storage add: %w", err)
	}
	return out, nil
}

// Remove decrements amount and deletes the row when it hits zero.
//
//nolint:gosec // G115: amount validated >0 before the uint32 cast.
func (r *GORMStorageRepository) Remove(ctx context.Context, id domain.StorageItemID, amount int) error {
	if amount <= 0 {
		return domain.ErrStorageInsufficientAmount
	}
	var item domain.StorageItem
	if err := r.db.WithContext(ctx).First(&item, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return domain.ErrStorageItemNotFound
		}
		return err
	}
	if int(item.Amount) < amount {
		return domain.ErrStorageInsufficientAmount
	}
	if int(item.Amount) == amount {
		return r.db.WithContext(ctx).Delete(&domain.StorageItem{}, id).Error
	}
	item.Amount -= uint32(amount)
	return r.db.WithContext(ctx).Model(&domain.StorageItem{}).Where("id = ?", id).
		Update("amount", item.Amount).Error
}

// SetEquip overwrites the equip column of one storage row. A zero
// RowsAffected means the row vanished between LoadByAccount and now; surface
// it as ErrStorageItemNotFound.
func (r *GORMStorageRepository) SetEquip(ctx context.Context, id domain.StorageItemID, equip uint32) error {
	res := r.db.WithContext(ctx).Model(&domain.StorageItem{}).Where("id = ?", id).
		Update("equip", equip)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return domain.ErrStorageItemNotFound
	}
	return nil
}
