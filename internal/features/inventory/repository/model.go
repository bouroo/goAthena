// Package repository provides GORM-backed implementations of the inventory
// domain ports. Models in this file are an anti-corruption layer: they
// mirror the rAthena migration columns 1:1 (see
// internal/infrastructure/db/migrations/000003_inventory.up.sql) and
// exist separately from the domain entities so the domain stays
// decoupled from the persistence schema.
package repository

import (
	"github.com/bouroo/goAthena/internal/features/inventory/domain"
)

// InventoryModel maps to the rAthena `inventory` table
// (rathena/sql-files/main.sql L716-752). Column names match the
// migration verbatim; do not rename without a corresponding migration.
// The schema carries 5 random-option slots — we model them as a flat
// 5-element field per slot to keep the SELECT clause straight (no
// joins, no JSON, no GORM embedded structs that would obscure the
// 1:1 column correspondence).
type InventoryModel struct {
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

// TableName pins the rAthena-canonical table name.
func (InventoryModel) TableName() string { return "inventory" }

// toDomain maps a GORM row to its domain entity. The conversion is
// total (no panics on zero-value inputs) so it can be used in
// white-box tests without an explicit fixture.
func (m *InventoryModel) toDomain() domain.InventoryItem {
	if m == nil {
		return domain.InventoryItem{}
	}
	return domain.InventoryItem{
		ID:     m.ID,
		CharID: m.CharID,
		NameID: m.NameID,
		Amount: m.Amount,
		//nolint:gosec // G115: schema is int(11) unsigned carrying the EQP_* bitmask; widening is exact.
		Equip:    domain.EquipSlot(m.Equip),
		Identify: m.Identify,
		Refine:   m.Refine,
		//nolint:gosec // G115: schema is tinyint(4) unsigned; widens to uint8 exactly.
		Attribute: m.Attribute,
		Card0:     m.Card0,
		Card1:     m.Card1,
		Card2:     m.Card2,
		Card3:     m.Card3,
		Options: [5]domain.ItemOption{
			{ID: m.OptionID0, Value: m.OptionVal0, Parm: m.OptionParm0},
			{ID: m.OptionID1, Value: m.OptionVal1, Parm: m.OptionParm1},
			{ID: m.OptionID2, Value: m.OptionVal2, Parm: m.OptionParm2},
			{ID: m.OptionID3, Value: m.OptionVal3, Parm: m.OptionParm3},
			{ID: m.OptionID4, Value: m.OptionVal4, Parm: m.OptionParm4},
		},
		ExpireTime:   m.ExpireTime,
		Favorite:     m.Favorite,
		Bound:        m.Bound,
		UniqueID:     m.UniqueID,
		EquipSwitch:  m.EquipSwitch,
		EnchantGrade: m.EnchantGrade,
	}
}

// fromDomainMaterialize returns a GORM-friendly model pre-populated
// with the item's persistent fields. The ID is intentionally zero so
// gorm.AutoMigrate / Create calls leave the autoincrement to the DB.
// This is the inverse of toDomain; it is not symmetric for ID
// because the autoincrement value is read back via the Create result.
func fromDomainMaterialize(charID uint32, item domain.InventoryItem) InventoryModel {
	return InventoryModel{
		CharID:       charID,
		NameID:       item.NameID,
		Amount:       item.Amount,
		Equip:        uint32(item.Equip),
		Identify:     item.Identify,
		Refine:       item.Refine,
		Attribute:    item.Attribute,
		Card0:        item.Card0,
		Card1:        item.Card1,
		Card2:        item.Card2,
		Card3:        item.Card3,
		OptionID0:    item.Options[0].ID,
		OptionVal0:   item.Options[0].Value,
		OptionParm0:  item.Options[0].Parm,
		OptionID1:    item.Options[1].ID,
		OptionVal1:   item.Options[1].Value,
		OptionParm1:  item.Options[1].Parm,
		OptionID2:    item.Options[2].ID,
		OptionVal2:   item.Options[2].Value,
		OptionParm2:  item.Options[2].Parm,
		OptionID3:    item.Options[3].ID,
		OptionVal3:   item.Options[3].Value,
		OptionParm3:  item.Options[3].Parm,
		OptionID4:    item.Options[4].ID,
		OptionVal4:   item.Options[4].Value,
		OptionParm4:  item.Options[4].Parm,
		ExpireTime:   item.ExpireTime,
		Favorite:     item.Favorite,
		Bound:        item.Bound,
		UniqueID:     item.UniqueID,
		EquipSwitch:  item.EquipSwitch,
		EnchantGrade: item.EnchantGrade,
	}
}
