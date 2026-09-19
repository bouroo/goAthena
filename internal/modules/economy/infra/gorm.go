// Package infra — GORM-backed ledger. The schema lives in
// internal/infrastructure/db/migrations/*/000005_zeny_ledger.up.sql. The struct
// column tags mirror that schema exactly so GORM never auto-migrates the table
// (the schema is owned by golang-migrate; auto-migration would race the
// migration runner).
package infra

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/bouroo/goAthena/internal/modules/economy/domain"
)

// LedgerRow is the GORM model for the zeny_ledger table. It carries the same
// shape as domain.ZenyTransaction but with column tags and a TableName method
// so GORM can map it without dragging the domain package into GORM.
type LedgerRow struct {
	ID         int64     `gorm:"column:id;primaryKey;autoIncrement"`
	AccountID  uint32    `gorm:"column:account_id"`
	CharID     uint32    `gorm:"column:char_id;index"`
	Amount     int32     `gorm:"column:amount"`
	Reason     string    `gorm:"column:reason;size:32;index"`
	PeerCharID uint32    `gorm:"column:peer_char_id"`
	MapName    string    `gorm:"column:map_name;size:16"`
	CreatedAt  time.Time `gorm:"column:created_at;index"`
}

// TableName fixes the legacy schema's table name so GORM does not pluralize.
func (LedgerRow) TableName() string { return "zeny_ledger" }

// toDomain maps a row back to the domain type (drops the column tag noise).
func (r LedgerRow) toDomain() domain.ZenyTransaction {
	return domain.ZenyTransaction{
		ID:         r.ID,
		AccountID:  r.AccountID,
		CharID:     r.CharID,
		Amount:     r.Amount,
		Reason:     domain.Reason(r.Reason),
		PeerCharID: r.PeerCharID,
		MapName:    r.MapName,
		CreatedAt:  r.CreatedAt,
	}
}

// GORMLedger is the production ledger over GORM.
type GORMLedger struct {
	db  *gorm.DB
	now func() time.Time
}

// NewGORMLedger wraps an open *gorm.DB. The now hook defaults to time.Now;
// tests inject a deterministic clock.
func NewGORMLedger(db *gorm.DB) *GORMLedger {
	return &GORMLedger{db: db, now: time.Now}
}

// Append writes one transaction and returns the populated row.
func (r *GORMLedger) Append(ctx context.Context, tx domain.ZenyTransaction) (domain.ZenyTransaction, error) {
	if tx.CreatedAt.IsZero() {
		tx.CreatedAt = r.now()
	}
	if !tx.Valid() {
		return domain.ZenyTransaction{}, errInvalid
	}
	row := LedgerRow{
		AccountID:  tx.AccountID,
		CharID:     tx.CharID,
		Amount:     tx.Amount,
		Reason:     string(tx.Reason),
		PeerCharID: tx.PeerCharID,
		MapName:    tx.MapName,
		CreatedAt:  tx.CreatedAt,
	}
	if err := r.db.WithContext(ctx).Create(&row).Error; err != nil {
		return domain.ZenyTransaction{}, err
	}
	return row.toDomain(), nil
}

// ListByChar returns up to limit transactions for charID, newest first.
func (r *GORMLedger) ListByChar(ctx context.Context, charID uint32, limit int) ([]domain.ZenyTransaction, error) {
	if limit <= 0 {
		limit = 100
	}
	var rows []LedgerRow
	if err := r.db.WithContext(ctx).
		Where("char_id = ?", charID).
		Order("id DESC").
		Limit(limit).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]domain.ZenyTransaction, len(rows))
	for i, row := range rows {
		out[i] = row.toDomain()
	}
	return out, nil
}

// SumByChar returns the signed sum of every transaction for charID.
func (r *GORMLedger) SumByChar(ctx context.Context, charID uint32) (int64, error) {
	var sum int64
	if err := r.db.WithContext(ctx).
		Model(&LedgerRow{}).
		Where("char_id = ?", charID).
		Select("COALESCE(SUM(amount), 0)").
		Scan(&sum).Error; err != nil {
		return 0, err
	}
	return sum, nil
}
