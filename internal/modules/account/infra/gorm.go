// Package infra adapts the account domain to its GORM repository. The repo
// reads the legacy rAthena `login` table read/write-compatibly.
package infra

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/bouroo/goAthena/internal/modules/account/domain"
)

// GORMAccountRepository is the production account repository over GORM.
type GORMAccountRepository struct {
	db *gorm.DB
}

// NewGORMAccountRepository wraps an open *gorm.DB.
func NewGORMAccountRepository(db *gorm.DB) *GORMAccountRepository {
	return &GORMAccountRepository{db: db}
}

// FindByUserID loads the account row for the login name. A gorm.ErrRecordNotFound
// is translated to domain.ErrAccountNotFound so callers branch on domain errors.
func (r *GORMAccountRepository) FindByUserID(ctx context.Context, userID string) (domain.Account, error) {
	var acc domain.Account
	err := r.db.WithContext(ctx).
		Select("account_id", "userid", "user_pass", "sex", "email", "group_id", "state", "unban_time", "expiration_time", "logincount", "character_slots", "vip_time").
		Where("userid = ?", userID).
		First(&acc).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return domain.Account{}, domain.ErrAccountNotFound
		}
		return domain.Account{}, err
	}
	return acc, nil
}

// FindByAccountID loads the account row by account_id.
func (r *GORMAccountRepository) FindByAccountID(ctx context.Context, id domain.AccountID) (domain.Account, error) {
	var acc domain.Account
	err := r.db.WithContext(ctx).
		Where("account_id = ?", id).
		First(&acc).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return domain.Account{}, domain.ErrAccountNotFound
		}
		return domain.Account{}, err
	}
	return acc, nil
}

// RecordLogin increments logincount and stamps lastlogin/last_ip.
func (r *GORMAccountRepository) RecordLogin(ctx context.Context, id domain.AccountID, ip string) error {
	now := time.Now()
	return r.db.WithContext(ctx).Table("login").
		Where("account_id = ?", id).
		Updates(map[string]any{
			"logincount": gorm.Expr("logincount + 1"),
			"lastlogin":  now,
			"last_ip":    ip,
		}).Error
}
