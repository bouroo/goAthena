// Package infra adapts the world domain to its GORM repository. The repo reads
// the char table for map-enter state (last position/look) and writes online
// status back. It does NOT own game-loop state — that lives in the WorldService.
package infra

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/bouroo/goAthena/internal/modules/world/domain"
)

// GORMWorldRepository is the production world repository over GORM.
type GORMWorldRepository struct {
	db *gorm.DB
}

// NewGORMWorldRepository wraps an open *gorm.DB.
func NewGORMWorldRepository(db *gorm.DB) *GORMWorldRepository {
	return &GORMWorldRepository{db: db}
}

// charRow is a minimal read model for map-enter (only the columns the world
// needs). Kept local to avoid a dependency on the character module's domain.
type charRow struct {
	LastMap string `gorm:"column:last_map"`
	LastX   uint16 `gorm:"column:last_x"`
	LastY   uint16 `gorm:"column:last_y"`
	// SaveMap/SaveX/SaveY back the runtime save-point cache (Entity.SaveMap/SavePos)
	// used by respawn. Read-only here — the world repo never writes the save point.
	SaveMap   string `gorm:"column:save_map"`
	SaveX     uint16 `gorm:"column:save_x"`
	SaveY     uint16 `gorm:"column:save_y"`
	Sex       int8   `gorm:"column:sex"`
	Class     uint16 `gorm:"column:class"`
	BaseLevel uint16 `gorm:"column:base_level"`
	Hair      uint8  `gorm:"column:hair"`
	Weapon    uint16 `gorm:"column:weapon"`
	Shield    uint16 `gorm:"column:shield"`
	HeadTop   uint16 `gorm:"column:head_top"`
	HP        uint32 `gorm:"column:hp"`
	MaxHP     uint32 `gorm:"column:max_hp"`
	SP        uint32 `gorm:"column:sp"`
	MaxSP     uint32 `gorm:"column:max_sp"`
	Name      string `gorm:"column:name"`
	AccountID uint32 `gorm:"column:account_id"`
	Str       uint16 `gorm:"column:str"`
	Agi       uint16 `gorm:"column:agi"`
	Vit       uint16 `gorm:"column:vit"`
	Int       uint16 `gorm:"column:int"` // reserved word; GORM auto-SELECT quotes it
	Dex       uint16 `gorm:"column:dex"`
	Luk       uint16 `gorm:"column:luk"`
}

func (charRow) TableName() string { return "char" }

// LoadEnterState reads the char's map-enter fields from the char table.
//
//nolint:gosec // G115: narrowing from DB column types to domain types; values are inherently bounded game stats.
func (r *GORMWorldRepository) LoadEnterState(ctx context.Context, charID uint32) (domain.Entity, error) {
	var cr charRow
	err := r.db.WithContext(ctx).
		Where("char_id = ?", charID).
		First(&cr).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return domain.Entity{}, domain.ErrEntityNotFound
		}
		return domain.Entity{}, err
	}
	return domain.Entity{
		Account: cr.AccountID,
		Map:     cr.LastMap,
		Pos:     domain.Position{X: int16(cr.LastX), Y: int16(cr.LastY)},
		SaveMap: cr.SaveMap,
		SavePos: domain.Position{X: int16(cr.SaveX), Y: int16(cr.SaveY)},
		Sex:     uint8(cr.Sex),
		Job:     int16(cr.Class),
		Level:   int16(cr.BaseLevel),
		Head:    cr.HeadTop,
		Weapon:  uint32(cr.Weapon),
		Shield:  uint32(cr.Shield),
		HP:      int32(cr.HP),
		MaxHP:   int32(cr.MaxHP),
		SP:      int32(cr.SP),
		MaxSP:   int32(cr.MaxSP),
		Name:    cr.Name,
		Speed:   150, // rAthena default walk speed (150 ms per cell)
		Str:     cr.Str,
		Agi:     cr.Agi,
		Vit:     cr.Vit,
		Int:     cr.Int,
		Dex:     cr.Dex,
		Luk:     cr.Luk,
	}, nil
}

// SetOnline marks a char online/offline and updates its last position.
func (r *GORMWorldRepository) SetOnline(ctx context.Context, charID uint32, online bool, pos domain.Position) error {
	return r.db.WithContext(ctx).Table("char").
		Where("char_id = ?", charID).
		Updates(map[string]any{
			"online": boolToTiny(online),
			"last_x": pos.X,
			"last_y": pos.Y,
		}).Error
}

func boolToTiny(b bool) int {
	if b {
		return 1
	}
	return 0
}

// SetPosition persists the char's destination map + position (warp/transit).
func (r *GORMWorldRepository) SetPosition(ctx context.Context, charID uint32, mapName string, pos domain.Position) error {
	return r.db.WithContext(ctx).Table("char").
		Where("char_id = ?", charID).
		Updates(map[string]any{
			"last_map": mapName,
			"last_x":   pos.X,
			"last_y":   pos.Y,
		}).Error
}
