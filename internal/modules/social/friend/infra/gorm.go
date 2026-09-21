// Package infra adapts the friend domain to its GORM repository.
package infra

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"github.com/bouroo/goAthena/internal/modules/social/friend/domain"
)

// GORMFriendRepository is the production friend repository over GORM.
//
// Two tables are involved: `friends` (the (char_id, friend_id) pair per
// direction — rAthena's exact shape) and `char` (identity for the roster
// read). Every mutation is bidirectional and runs inside one transaction.
type GORMFriendRepository struct {
	db *gorm.DB
}

// NewGORMFriendRepository wraps an open *gorm.DB.
func NewGORMFriendRepository(db *gorm.DB) *GORMFriendRepository {
	return &GORMFriendRepository{db: db}
}

// friendRow maps the rAthena `friends` table (sql-files/main.sql:424-428).
// The composite PK (char_id, friend_id) is one direction; the reverse row is
// the other.
type friendRow struct {
	CharID   uint32 `gorm:"column:char_id;primaryKey"`
	FriendID uint32 `gorm:"column:friend_id;primaryKey"`
}

func (friendRow) TableName() string { return "friends" }

// charRow is the minimal `char` projection the roster read needs. Kept local
// to avoid a dependency on the character module's domain.
type charRow struct {
	CharID    uint32 `gorm:"column:char_id"`
	AccountID uint32 `gorm:"column:account_id"`
	Name      string `gorm:"column:name"`
	Online    int    `gorm:"column:online"`
}

func (charRow) TableName() string { return "char" }

// List returns the owner's friends ordered by friend char id, identity read
// from the friend's `char` row. Pairs whose char row is gone (a deleted
// character) are skipped — rAthena's char-server reaps orphaned friend rows at
// load, this read-side skip is the equivalent.
//
// Two queries instead of a JOIN: `char` is a reserved word and GORM does not
// quote table names inside raw Joins strings, so a portable join would mean
// hand-quoting per dialect. The IN read keeps the statement portable.
func (r *GORMFriendRepository) List(ctx context.Context, charID uint32) ([]domain.Friend, error) {
	var friendIDs []uint32
	if err := r.db.WithContext(ctx).Model(&friendRow{}).
		Where("char_id = ?", charID).
		Order("friend_id").
		Pluck("friend_id", &friendIDs).Error; err != nil {
		return nil, fmt.Errorf("friend list: %w", err)
	}
	if len(friendIDs) == 0 {
		return []domain.Friend{}, nil
	}
	var rows []charRow
	if err := r.db.WithContext(ctx).
		Select("char_id", "account_id", "name", "online").
		Where("char_id IN ?", friendIDs).
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("friend list chars: %w", err)
	}
	byID := make(map[uint32]charRow, len(rows))
	for _, cr := range rows {
		byID[cr.CharID] = cr
	}
	out := make([]domain.Friend, 0, len(friendIDs))
	for _, id := range friendIDs { // PK order = friend_id order.
		cr, ok := byID[id]
		if !ok {
			continue // char row deleted; the pair is an orphan.
		}
		out = append(out, domain.Friend{
			CharID:          charID,
			FriendCharID:    id,
			FriendAccountID: cr.AccountID,
			Name:            cr.Name,
			Online:          cr.Online != 0,
		})
	}
	return out, nil
}

// Add inserts both directions in one transaction. Capacity is checked on both
// lists (requester → ErrFriendListFull, acceptor → ErrAcceptorListFull); the
// pair must be absent (ErrFriendExists). The in-transaction checks are
// advisory rather than enforced by DB constraints, so two simultaneous
// accepts for the same pair remain a theoretical race.
func (r *GORMFriendRepository) Add(ctx context.Context, ownerCharID, friendCharID uint32) error {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := r.hasPair(tx, ownerCharID, friendCharID); err == nil {
			return domain.ErrFriendExists
		} else if !errors.Is(err, domain.ErrFriendNotFound) {
			return err
		}
		for _, side := range []struct {
			charID uint32
			full   error
		}{
			{ownerCharID, domain.ErrFriendListFull},
			{friendCharID, domain.ErrAcceptorListFull},
		} {
			var n int64
			if err := tx.Model(&friendRow{}).Where("char_id = ?", side.charID).
				Count(&n).Error; err != nil {
				return err
			}
			if int(n) >= domain.MaxFriends {
				return side.full
			}
		}
		return tx.Create([]friendRow{
			{CharID: ownerCharID, FriendID: friendCharID},
			{CharID: friendCharID, FriendID: ownerCharID},
		}).Error
	})
	if err != nil {
		return fmt.Errorf("friend add: %w", err)
	}
	return nil
}

// Remove deletes both directions in one transaction. ErrFriendNotFound when
// the owner's list lacks the pair.
func (r *GORMFriendRepository) Remove(ctx context.Context, ownerCharID, friendCharID uint32) error {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := r.hasPair(tx, ownerCharID, friendCharID); err != nil {
			return err
		}
		return tx.Where("(char_id = ? AND friend_id = ?) OR (char_id = ? AND friend_id = ?)",
			ownerCharID, friendCharID, friendCharID, ownerCharID).
			Delete(&friendRow{}).Error
	})
	if err != nil {
		return fmt.Errorf("friend remove: %w", err)
	}
	return nil
}

// hasPair reports the pair's presence on the owner's side. Caller holds the tx.
func (r *GORMFriendRepository) hasPair(tx *gorm.DB, ownerCharID, friendCharID uint32) error {
	var n int64
	if err := tx.Model(&friendRow{}).
		Where("char_id = ? AND friend_id = ?", ownerCharID, friendCharID).
		Count(&n).Error; err != nil {
		return err
	}
	if n == 0 {
		return domain.ErrFriendNotFound
	}
	return nil
}
