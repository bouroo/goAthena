// Package infra provides the inventory bounded context's persistence adapters:
// an in-memory implementation (unit tests) and a GORM implementation backed by
// the `inventory` table (migration 000003_inventory.up.sql). Both satisfy the
// domain ports.
package infra

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/bouroo/goAthena/internal/modules/inventory/domain"
)

// inventoryModel maps the rAthena `inventory` table (migration 000003). Every
// column the table owns is modeled so a LoadByChar round-trips the full row; the
// grid index is not a column (rAthena assigns it from array position at load), so
// it is absent here and computed in toDomain.
type inventoryModel struct {
	ID           uint32 `gorm:"column:id;primaryKey;autoIncrement"`
	CharID       uint32 `gorm:"column:char_id"`
	NameID       uint32 `gorm:"column:nameid"`
	Amount       uint32 `gorm:"column:amount"`
	Equip        uint32 `gorm:"column:equip"`
	Identify     int16  `gorm:"column:identify"`
	Refine       uint8  `gorm:"column:refine"`
	Attribute    uint8  `gorm:"column:attribute"`
	Card0        uint32 `gorm:"column:card0"`
	Card1        uint32 `gorm:"column:card1"`
	Card2        uint32 `gorm:"column:card2"`
	Card3        uint32 `gorm:"column:card3"`
	OptionID0    int16  `gorm:"column:option_id0"`
	OptionVal0   int16  `gorm:"column:option_val0"`
	OptionParm0  int8   `gorm:"column:option_parm0"`
	OptionID1    int16  `gorm:"column:option_id1"`
	OptionVal1   int16  `gorm:"column:option_val1"`
	OptionParm1  int8   `gorm:"column:option_parm1"`
	OptionID2    int16  `gorm:"column:option_id2"`
	OptionVal2   int16  `gorm:"column:option_val2"`
	OptionParm2  int8   `gorm:"column:option_parm2"`
	OptionID3    int16  `gorm:"column:option_id3"`
	OptionVal3   int16  `gorm:"column:option_val3"`
	OptionParm3  int8   `gorm:"column:option_parm3"`
	OptionID4    int16  `gorm:"column:option_id4"`
	OptionVal4   int16  `gorm:"column:option_val4"`
	OptionParm4  int8   `gorm:"column:option_parm4"`
	ExpireTime   uint32 `gorm:"column:expire_time"`
	Favorite     uint8  `gorm:"column:favorite"`
	Bound        uint8  `gorm:"column:bound"`
	UniqueID     uint64 `gorm:"column:unique_id"`
	EquipSwitch  uint32 `gorm:"column:equip_switch"`
	EnchantGrade uint8  `gorm:"column:enchantgrade"`
}

// TableName pins the model to the `inventory` table. GORM pluralizes struct
// names by default; rAthena's table is the singular `inventory`, so this is
// load-bearing — without it GORM targets `inventory_models` and every query
// errors out.
func (inventoryModel) TableName() string { return "inventory" }

// inventoryColumns is the explicit SELECT list covering every modeled column. It
// is shared by every read path (LoadByChar, the equip slot lookup) so a column
// added to inventoryModel is surfaced here rather than silently round-tripping
// as zero.
const inventoryColumns = "id, char_id, nameid, amount, equip, identify, refine, attribute, " +
	"card0, card1, card2, card3, " +
	"option_id0, option_val0, option_parm0, option_id1, option_val1, option_parm1, " +
	"option_id2, option_val2, option_parm2, option_id3, option_val3, option_parm3, " +
	"option_id4, option_val4, option_parm4, expire_time, favorite, bound, unique_id, " +
	"equip_switch, enchantgrade"

// toDomain converts a model row to a domain item, assigning the grid Index from
// the caller-supplied position (id-ascending order). Cards and options map
// verbatim; the identify/favorite tinyints become bools.
func toDomain(m inventoryModel, index uint16) domain.InventoryItem {
	options := [5]domain.ItemOption{
		{ID: uint16(m.OptionID0), Value: m.OptionVal0, Param: m.OptionParm0}, //nolint:gosec // G115: smallint→uint16 id
		{ID: uint16(m.OptionID1), Value: m.OptionVal1, Param: m.OptionParm1}, //nolint:gosec // G115: smallint→uint16 id
		{ID: uint16(m.OptionID2), Value: m.OptionVal2, Param: m.OptionParm2}, //nolint:gosec // G115: smallint→uint16 id
		{ID: uint16(m.OptionID3), Value: m.OptionVal3, Param: m.OptionParm3}, //nolint:gosec // G115: smallint→uint16 id
		{ID: uint16(m.OptionID4), Value: m.OptionVal4, Param: m.OptionParm4}, //nolint:gosec // G115: smallint→uint16 id
	}
	return domain.InventoryItem{
		ID:           m.ID,
		CharID:       m.CharID,
		Index:        index,
		NameID:       m.NameID,
		Amount:       m.Amount,
		Equip:        m.Equip,
		Identified:   m.Identify != 0,
		Refine:       m.Refine,
		Attribute:    m.Attribute,
		Cards:        [4]uint32{m.Card0, m.Card1, m.Card2, m.Card3},
		Options:      options,
		ExpireTime:   m.ExpireTime,
		Favorite:     m.Favorite != 0,
		Bound:        m.Bound,
		UniqueID:     m.UniqueID,
		EquipSwitch:  m.EquipSwitch,
		EnchantGrade: m.EnchantGrade,
	}
}

// GORMInventoryRepository persists the bag against the `inventory` table.
type GORMInventoryRepository struct {
	db *gorm.DB
}

// NewGORMInventoryRepository binds the repository to a GORM connection targeting
// the `inventory` table.
func NewGORMInventoryRepository(db *gorm.DB) *GORMInventoryRepository {
	return &GORMInventoryRepository{db: db}
}

// LoadByChar returns the character's bag ordered by id so the assigned grid Index
// (0,1,2,…) is stable across loads — a session that reloads sees its slots in the
// same positions. Scoping by char_id is the read-side impersonation guard; the
// world layer pairs it with the verified accountID so a forged char_id from
// another account yields an empty bag here, never a cross-account read. A
// character with no items yields an empty (non-nil) slice.
func (r *GORMInventoryRepository) LoadByChar(ctx context.Context, _, charID uint32) ([]domain.InventoryItem, error) {
	var rows []inventoryModel
	if err := r.db.WithContext(ctx).
		Select(inventoryColumns).
		Where("char_id = ?", charID).
		Order("id").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("inventory: load char %d: %w", charID, err)
	}
	out := make([]domain.InventoryItem, len(rows))
	for i, m := range rows {
		out[i] = toDomain(m, uint16(i)) //nolint:gosec // G115: index < MaxInventorySlots (100) < uint16
	}
	return out, nil
}

// AddItem atomically merges item into an existing stackable row (same NameID) or
// inserts a new row. It runs inside a transaction so two concurrent pickups of
// the same stackable cannot both miss the match and double-insert. The matched
// row's index is recomputed from its id-order position (stable, since pickups
// only append). A non-stackable insert at the slot cap yields ErrInventoryFull.
// On success it returns the row as it now stands.
func (r *GORMInventoryRepository) AddItem(ctx context.Context, _, charID uint32, item domain.NewItem) (domain.InventoryItem, error) {
	var result domain.InventoryItem
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if item.Stackable {
			var hit inventoryModel
			if err := tx.Where("char_id = ? AND nameid = ?", charID, item.NameID).
				Order("id").
				Limit(1).
				Take(&hit).Error; err == nil {
				// Merge into the existing stack.
				if err := tx.Model(&inventoryModel{}).
					Where("id = ?", hit.ID).
					UpdateColumn("amount", gorm.Expr("amount + ?", item.Amount)).Error; err != nil {
					return fmt.Errorf("inventory: merge char %d nameid %d: %w", charID, item.NameID, err)
				}
				hit.Amount += item.Amount
				result = toDomain(hit, mergeIndex(charID, hit.ID, tx))
				return nil
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("inventory: find stack char %d nameid %d: %w", charID, item.NameID, err)
			}
			// No existing stack: fall through to insert a new row.
		}
		var count int64
		if err := tx.Model(&inventoryModel{}).Where("char_id = ?", charID).Count(&count).Error; err != nil {
			return fmt.Errorf("inventory: count char %d: %w", charID, err)
		}
		if int(count) >= domain.MaxInventorySlots {
			return domain.ErrInventoryFull
		}
		row := inventoryModel{CharID: charID, NameID: item.NameID, Amount: item.Amount}
		if err := tx.Create(&row).Error; err != nil {
			return fmt.Errorf("inventory: insert char %d nameid %d: %w", charID, item.NameID, err)
		}
		// The new row's id is the largest for the char (auto-increment), so its
		// index is the slot count it occupies.
		result = toDomain(row, uint16(count)) //nolint:gosec // G115: count < MaxInventorySlots (100) < uint16
		return nil
	})
	if err != nil {
		// ErrInventoryFull is a domain sentinel callers match with errors.Is, so it
		// passes through unwrapped; every gorm error the closure returns is already
		// %w-wrapped, but a commit/rollback fault arrives unwrapped, so wrap those.
		if errors.Is(err, domain.ErrInventoryFull) {
			return domain.InventoryItem{}, err //nolint:wrapcheck // sentinel must pass through unwrapped for errors.Is
		}
		return domain.InventoryItem{}, fmt.Errorf("inventory: add item char %d nameid %d: %w", charID, item.NameID, err)
	}
	return result, nil
}

// ConsumeItem atomically consumes qty units from the row at the character's grid
// index. The row is locked in a transaction before validating and mutating it;
// the returned item describes the row before consumption and deleted reports
// whether the row was removed.
func (r *GORMInventoryRepository) ConsumeItem(ctx context.Context, accountID, charID uint32, index, qty uint16) (item domain.InventoryItem, deleted bool, err error) {
	if qty == 0 {
		return domain.InventoryItem{}, false, domain.ErrItemNotFound
	}
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var m inventoryModel
		query := tx.Select(inventoryColumns).
			Where("char_id = ?", charID).
			Order("id").
			Offset(int(index)). //nolint:gosec // G115: index is a bag slot < 100, far within int range
			Limit(1).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Take(&m)
		if errors.Is(query.Error, gorm.ErrRecordNotFound) {
			return domain.ErrItemNotFound
		}
		if query.Error != nil {
			return fmt.Errorf("inventory: find consume account %d char %d index %d: %w", accountID, charID, index, query.Error)
		}
		if uint64(qty) > uint64(m.Amount) {
			return domain.ErrItemNotFound
		}
		item = toDomain(m, index)
		if uint64(qty) == uint64(m.Amount) {
			if err := tx.Delete(&inventoryModel{}, "id = ? AND char_id = ?", m.ID, charID).Error; err != nil {
				return fmt.Errorf("inventory: delete consumed item account %d char %d id %d: %w", accountID, charID, m.ID, err)
			}
			deleted = true
			return nil
		}
		if err := tx.Model(&inventoryModel{}).
			Where("id = ? AND char_id = ? AND amount >= ?", m.ID, charID, qty).
			UpdateColumn("amount", gorm.Expr("amount - ?", qty)).Error; err != nil {
			return fmt.Errorf("inventory: decrement item account %d char %d id %d: %w", accountID, charID, m.ID, err)
		}
		item.Amount -= uint32(qty) //nolint:gosec // G115: qty is uint16 and validated not greater than uint32 Amount
		return nil
	})
	if err != nil {
		if errors.Is(err, domain.ErrItemNotFound) {
			return domain.InventoryItem{}, false, err //nolint:wrapcheck // sentinel must pass through for errors.Is
		}
		return domain.InventoryItem{}, false, fmt.Errorf("inventory: consume account %d char %d index %d: %w", accountID, charID, index, err)
	}
	return item, deleted, nil
}

// the char's rows have a smaller id (its id-ascending position). This keeps the
// merged row's index stable: a merge does not reorder rows, so the matched row
// keeps the position LoadByChar would assign it.
func mergeIndex(charID, rowID uint32, tx *gorm.DB) uint16 {
	var pos int64
	if err := tx.Model(&inventoryModel{}).
		Where("char_id = ? AND id < ?", charID, rowID).
		Count(&pos).Error; err != nil {
		return 0
	}
	return uint16(pos) //nolint:gosec // G115: pos < MaxInventorySlots (100) < uint16
}

// rowAtIndex loads the single bag row occupying the char's id-ascending grid slot
// `index` — the wire slot CZ_REQ_WEAR_EQUIP / CZ_REQ_TAKEOFF_EQUIP name. ORDER BY
// id with OFFSET keeps the slot aligned with LoadByChar's numbering (the client
// reuses that slot from the pickup ack). A slot past the last row yields
// gorm.ErrRecordNotFound so the caller maps it to the domain sentinel.
func (r *GORMInventoryRepository) rowAtIndex(ctx context.Context, charID uint32, index uint16) (inventoryModel, error) {
	var m inventoryModel
	err := r.db.WithContext(ctx).
		Select(inventoryColumns).
		Where("char_id = ?", charID).
		Order("id").
		Offset(int(index)). //nolint:gosec // G115: index < MaxInventorySlots (100), far within int range
		Limit(1).
		Take(&m).Error
	return m, err
}

// setEquip is the shared body of EquipItem / UnequipItem: locate the row at the
// grid slot, assign its equip column (0 clears — faithful to rAthena's unequip),
// and return the row as it now stands. A missing slot yields ErrItemNotFound so
// the use case answers the fail ack rather than faulting.
func (r *GORMInventoryRepository) setEquip(ctx context.Context, charID uint32, index uint16, equip uint32) (domain.InventoryItem, error) {
	m, err := r.rowAtIndex(ctx, charID, index)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return domain.InventoryItem{}, domain.ErrItemNotFound //nolint:wrapcheck // sentinel must pass through unwrapped for errors.Is
		}
		return domain.InventoryItem{}, fmt.Errorf("inventory: find equip char %d index %d: %w", charID, index, err)
	}
	if err := r.db.WithContext(ctx).
		Model(&inventoryModel{}).
		Where("id = ? AND char_id = ?", m.ID, charID).
		UpdateColumn("equip", equip).Error; err != nil {
		return domain.InventoryItem{}, fmt.Errorf("inventory: update equip char %d id %d: %w", charID, m.ID, err)
	}
	m.Equip = equip
	return toDomain(m, index), nil
}

// EquipItem assigns the worn-location bitmask to the bag row at the given grid
// slot (CZ_REQ_WEAR_EQUIP names the slot), returning the row as it now stands.
// A missing slot yields ErrItemNotFound.
func (r *GORMInventoryRepository) EquipItem(ctx context.Context, _, charID uint32, index uint16, equip uint32) (domain.InventoryItem, error) {
	return r.setEquip(ctx, charID, index, equip)
}

// UnequipItem clears the worn-location bitmask on the bag row at the given grid
// slot (CZ_REQ_TAKEOFF_EQUIP names the slot), returning the row as it now stands.
// A missing slot yields ErrItemNotFound.
func (r *GORMInventoryRepository) UnequipItem(ctx context.Context, _, charID uint32, index uint16) (domain.InventoryItem, error) {
	return r.setEquip(ctx, charID, index, 0)
}
