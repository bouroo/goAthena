package repository

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/bouroo/goAthena/internal/features/economy/domain"
	inventorydomain "github.com/bouroo/goAthena/internal/features/inventory/domain"
	inventoryrepository "github.com/bouroo/goAthena/internal/features/inventory/repository"
)

type zenyRepo struct {
	db *gorm.DB
}

// NewCharacterZenyRepository returns a new CharacterZenyRepository.
func NewCharacterZenyRepository(db *gorm.DB) domain.CharacterZenyRepository {
	return &zenyRepo{db: db}
}

type charZenyModel struct {
	ID   uint32 `gorm:"column:char_id;primaryKey"`
	Zeny uint32 `gorm:"column:zeny"`
}

func (charZenyModel) TableName() string { return "char" }

const (
	zenyColumn   = "zeny"
	amountColumn = "amount"
	updateLock   = "UPDATE"
)

func (r *zenyRepo) GetZeny(ctx context.Context, charID uint32) (uint32, error) {
	if charID == 0 {
		return 0, fmt.Errorf("%w: charID=0", domain.ErrCharNotFound)
	}
	var m charZenyModel
	if err := r.db.WithContext(ctx).First(&m, "char_id = ?", charID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, fmt.Errorf("%w: charID=%d", domain.ErrCharNotFound, charID)
		}
		return 0, fmt.Errorf("get zeny for char %d: %w", charID, err)
	}
	return m.Zeny, nil
}

func (r *zenyRepo) ExecuteBuyTx(ctx context.Context, charID uint32, totalCost uint32, items []domain.AcquiredItem) (uint32, error) {
	if charID == 0 {
		return 0, fmt.Errorf("%w: charID=0", domain.ErrCharNotFound)
	}
	var newZeny uint32
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var m charZenyModel
		if err := tx.Clauses(clause.Locking{Strength: updateLock}).
			First(&m, "char_id = ?", charID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("%w: charID=%d", domain.ErrCharNotFound, charID)
			}
			return fmt.Errorf("lock char %d: %w", charID, err)
		}
		if m.Zeny < totalCost {
			return domain.ErrInsufficientZeny
		}
		newZeny = m.Zeny - totalCost
		if err := tx.Model(&m).Update(zenyColumn, newZeny).Error; err != nil {
			return fmt.Errorf("update zeny for char %d: %w", charID, err)
		}
		for _, item := range items {
			invModel := inventoryrepository.InventoryModel{
				CharID: charID,
				NameID: item.ItemID,
				Amount: item.Amount,
			}
			if err := tx.Create(&invModel).Error; err != nil {
				return fmt.Errorf("create inv row char %d (nameid=%d): %w", charID, item.ItemID, err)
			}
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("execute buy tx char %d: %w", charID, err)
	}
	return newZeny, nil
}

func (r *zenyRepo) ExecuteSellTx(ctx context.Context, charID uint32, totalCredit uint32, sales []domain.SellLine) (uint32, error) {
	if charID == 0 {
		return 0, fmt.Errorf("%w: charID=0", domain.ErrCharNotFound)
	}
	var newZeny uint32
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var m charZenyModel
		if err := tx.Clauses(clause.Locking{Strength: updateLock}).
			First(&m, "char_id = ?", charID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("%w: charID=%d", domain.ErrCharNotFound, charID)
			}
			return fmt.Errorf("lock char %d: %w", charID, err)
		}
		if uint64(m.Zeny)+uint64(totalCredit) > uint64(domain.MaxZeny) {
			return domain.ErrZenyOverflow
		}
		newZeny = m.Zeny + totalCredit
		if err := tx.Model(&m).Update(zenyColumn, newZeny).Error; err != nil {
			return fmt.Errorf("update zeny for char %d: %w", charID, err)
		}
		for _, sale := range sales {
			var inv inventoryrepository.InventoryModel
			if err := tx.Clauses(clause.Locking{Strength: updateLock}).
				First(&inv, "id = ?", sale.InvID).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return fmt.Errorf("%w: invID=%d", inventorydomain.ErrItemNotFound, sale.InvID)
				}
				return fmt.Errorf("lock inv row %d: %w", sale.InvID, err)
			}
			if inv.Amount < sale.Amount {
				// Player tried to sell more than they own. Without this
				// guard the `else` branch below would silently delete the
				// row AND the caller has already credited totalCredit for
				// the inflated quantity — an infinite-zeny exploit.
				// Map to ErrItemNotFound so the service layer surfaces
				// SellFailInvalidItem (a clean business outcome) instead
				// of committing an over-credit.
				return fmt.Errorf("%w: invID=%d has=%d want=%d", inventorydomain.ErrItemNotFound, sale.InvID, inv.Amount, sale.Amount)
			}
			if inv.Amount > sale.Amount {
				if err := tx.Model(&inv).Update(amountColumn, inv.Amount-sale.Amount).Error; err != nil {
					return fmt.Errorf("decrement inv row %d: %w", sale.InvID, err)
				}
			} else {
				if err := tx.Delete(&inv).Error; err != nil {
					return fmt.Errorf("delete inv row %d: %w", sale.InvID, err)
				}
			}
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("execute sell tx char %d: %w", charID, err)
	}
	return newZeny, nil
}
