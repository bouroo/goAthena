package repository

import (
	"context"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/bouroo/goAthena/internal/features/inventory/domain"
)

// inventorySelectColumns is the column whitelist for reads. We
// deliberately select every column rather than relying on `SELECT *`
// because:
//  1. the schema is wide and stable — there is no reason to hide
//     columns behind a SELECT *, and
//  2. wrapcheck + static analysis can reason about the column set
//     from this constant.
const inventorySelectColumns = "id, char_id, nameid, amount, equip, identify, refine, attribute, " +
	"card0, card1, card2, card3, " +
	"option_id0, option_val0, option_parm0, " +
	"option_id1, option_val1, option_parm1, " +
	"option_id2, option_val2, option_parm2, " +
	"option_id3, option_val3, option_parm3, " +
	"option_id4, option_val4, option_parm4, " +
	"expire_time, favorite, bound, unique_id, equip_switch, enchantgrade"

type inventoryRepo struct {
	db *gorm.DB
}

// NewInventoryRepository wires the MariaDB/Postgres-backed
// InventoryRepository.
func NewInventoryRepository(db *gorm.DB) domain.InventoryRepository {
	return &inventoryRepo{db: db}
}

// ListByChar returns every item owned by charID, ordered by the
// autoincrement id (which is the closest stable proxy for slot order
// in rAthena's item grid). An unknown charID returns an empty slice
// with a nil error — "no rows" is not a typed-not-found here, it is
// the steady state for a fresh character.
func (r *inventoryRepo) ListByChar(ctx context.Context, charID uint32) ([]domain.InventoryItem, error) {
	if charID == 0 {
		return nil, fmt.Errorf("list inventory for char %d: charID must be > 0", charID)
	}
	var models []InventoryModel
	err := r.db.WithContext(ctx).
		Select(inventorySelectColumns).
		Where("char_id = ?", charID).
		Order("id ASC").
		Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("list inventory for char %d: %w", charID, err)
	}
	out := make([]domain.InventoryItem, 0, len(models))
	for i := range models {
		out = append(out, models[i].toDomain())
	}
	return out, nil
}

// Add inserts a new inventory row for charID and returns the
// autoincrement id assigned by the database. The repository does not
// attempt a stack-merge — callers that want stack semantics must do
// the lookup/merge themselves before calling Add. The returned id is
// always > 0 on success.
func (r *inventoryRepo) Add(ctx context.Context, charID uint32, item domain.InventoryItem) (uint32, error) {
	if charID == 0 {
		return 0, fmt.Errorf("add inventory for char %d: charID must be > 0", charID)
	}
	model := fromDomainMaterialize(charID, item)
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		return 0, fmt.Errorf("add inventory for char %d (nameid=%d): %w", charID, item.NameID, err)
	}
	if model.ID == 0 {
		// Defensive: a successful Create on an autoincrement PK must
		// populate the field. A zero here means a driver bug, so
		// surface it rather than papering over it.
		return 0, fmt.Errorf("add inventory for char %d: driver returned id=0 after insert", charID)
	}
	return model.ID, nil
}

// UpdateAmount sets the stack count for the given item id. Returns
// domain.ErrItemNotFound when no row matches; the service layer is
// expected to convert that into a "you used a stale slot" error.
func (r *inventoryRepo) UpdateAmount(ctx context.Context, id uint32, amount uint32) error {
	if id == 0 {
		return fmt.Errorf("%w: id=0", domain.ErrItemNotFound)
	}
	res := r.db.WithContext(ctx).
		Model(&InventoryModel{}).
		Where("id = ?", id).
		Update("amount", amount)
	if err := res.Error; err != nil {
		return fmt.Errorf("update inventory amount for id %d: %w", id, err)
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("%w: id=%d", domain.ErrItemNotFound, id)
	}
	return nil
}

// Remove deletes the row with the given id. Returns
// domain.ErrItemNotFound when no row matches — mirrors UpdateAmount's
// not-found semantics so callers can branch on a single sentinel.
func (r *inventoryRepo) Remove(ctx context.Context, id uint32) error {
	if id == 0 {
		return fmt.Errorf("%w: id=0", domain.ErrItemNotFound)
	}
	res := r.db.WithContext(ctx).
		Where("id = ?", id).
		Delete(&InventoryModel{})
	if err := res.Error; err != nil {
		return fmt.Errorf("remove inventory id %d: %w", id, err)
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("%w: id=%d", domain.ErrItemNotFound, id)
	}
	return nil
}

// ConsumeOne atomically decrements the stack of the row with the
// given id. It takes a row lock via a transaction containing
// SELECT ... FOR UPDATE so concurrent concurrent use-item calls are
// serialized at the DB level. When the resulting amount is 0 the row
// is deleted rather than persisted with Amount=0, and 0 is returned
// as remaining. The row's char_id is not re-checked here — the
// repository layer owns every row it must lock.
func (r *inventoryRepo) ConsumeOne(ctx context.Context, id uint32) (uint32, error) {
	if id == 0 {
		return 0, fmt.Errorf("%w: id=0", domain.ErrItemNotFound)
	}
	var remaining uint32
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var model InventoryModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", id).
			First(&model).Error; err != nil {
			return fmt.Errorf("consume row (id=%d): %w", id, err)
		}
		if model.Amount <= 1 {
			// Row is drained after this use — delete it thin.
			if deleteErr := tx.Where("id = ?", id).Delete(&InventoryModel{}).Error; deleteErr != nil {
				return fmt.Errorf("remove emptied row (id=%d): %w", id, deleteErr)
			}
			remaining = 0
			return nil
		}
		remaining = model.Amount - 1
		if updateErr := tx.Model(&InventoryModel{}).Where("id = ?", id).Update("amount", remaining).Error; updateErr != nil {
			return fmt.Errorf("update row amount (id=%d): %w", id, updateErr)
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("consume one (id=%d): %w", id, err)
	}
	return remaining, nil
}

// SetEquip overwrites the equip-position bitfield for the given
// item id. Returns domain.ErrItemNotFound when no row matches.
func (r *inventoryRepo) SetEquip(ctx context.Context, id uint32, equipMask uint32) error {
	if id == 0 {
		return fmt.Errorf("%w: id=0", domain.ErrItemNotFound)
	}
	res := r.db.WithContext(ctx).
		Model(&InventoryModel{}).
		Where("id = ?", id).
		Update("equip", equipMask)
	if err := res.Error; err != nil {
		return fmt.Errorf("set inventory equip for id %d: %w", id, err)
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("%w: id=%d", domain.ErrItemNotFound, id)
	}
	return nil
}

// InventoryModelToDomainForTest exposes the in-memory conversion to
// white-box tests so they can verify field mapping without a DB round
// trip. It is the same function used by the repository methods at
// runtime; the underscore-suffixed name is the canonical
// repository-layer pattern for test-only exports.
func InventoryModelToDomainForTest(m *InventoryModel) domain.InventoryItem {
	return m.toDomain()
}

// FromDomainMaterializeForTest exposes the inverse mapping so tests
// can verify the Add path constructs a row correctly without going
// through GORM's Create.
func FromDomainMaterializeForTest(charID uint32, item domain.InventoryItem) InventoryModel {
	return fromDomainMaterialize(charID, item)
}
