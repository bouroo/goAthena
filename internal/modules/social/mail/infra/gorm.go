// Package infra adapts the mail domain to its GORM repository.
package infra

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/bouroo/goAthena/internal/modules/social/mail/domain"
)

// GORMMailRepository is the production mail repository over GORM.
//
// Two tables: `mail` (one row per message) and `mail_attachments` (per-slot
// rows keyed (id, index), ON DELETE CASCADE). Recipient resolution reads the
// `char` table directly — the rAthena char-server does the same
// (int_mail.cpp:607), and this module owns no character state.
type GORMMailRepository struct {
	db *gorm.DB
}

// NewGORMMailRepository wraps an open *gorm.DB.
func NewGORMMailRepository(db *gorm.DB) *GORMMailRepository {
	return &GORMMailRepository{db: db}
}

// recipientRow is the minimal `char` projection (mirrors the guild repo's
// charRow; kept local to avoid a character-module dependency).
type recipientRow struct {
	CharID    uint32 `gorm:"column:char_id"`
	AccountID uint32 `gorm:"column:account_id"`
	Name      string `gorm:"column:name"`
	Class     uint16 `gorm:"column:class"`
	BaseLevel uint16 `gorm:"column:base_level"`
}

func (recipientRow) TableName() string { return "char" }

// Create inserts the mail and its attachments in one transaction. The inbox
// cap check inside the transaction is advisory (a plain COUNT takes no lock);
// rAthena's cap is advisory the same way — the char-server counts its
// in-memory inbox — so a concurrent-send overshoot by one row is accepted.
func (r *GORMMailRepository) Create(ctx context.Context, m *domain.Mail) error {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var n int64
		if err := tx.Model(&domain.Mail{}).Where("dest_id = ?", m.DestID).Count(&n).Error; err != nil {
			return err
		}
		if int(n) >= domain.MaxInbox {
			return domain.ErrInboxFull
		}
		if err := tx.Create(m).Error; err != nil {
			return err
		}
		for i := range m.Items {
			m.Items[i].MailID = int64(m.ID)
			if m.Items[i].Index == 0 {
				m.Items[i].Index = i
			}
			if err := tx.Create(&m.Items[i]).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("mail create: %w", err)
	}
	return nil
}

// Inbox returns up to MaxInbox mails ascending by id, flipping NEW rows to
// UNREAD (int_mail.cpp:60-74: the transition also counts as "unchecked").
func (r *GORMMailRepository) Inbox(ctx context.Context, destCharID uint32) ([]domain.Mail, error) {
	var rows []domain.Mail
	if err := r.db.WithContext(ctx).
		Where("dest_id = ?", destCharID).
		Order("id").Limit(domain.MaxInbox).
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("mail inbox: %w", err)
	}
	out := make([]domain.Mail, 0, len(rows))
	for _, m := range rows {
		if m.Status == domain.StatusNew {
			if err := r.db.WithContext(ctx).Model(&domain.Mail{}).
				Where("id = ?", m.ID).Update("status", domain.StatusUnread).Error; err != nil {
				return nil, err
			}
			m.Status = domain.StatusUnread
		}
		items, err := r.items(ctx, m.ID)
		if err != nil {
			return nil, err
		}
		m.Items = items
		out = append(out, m)
	}
	return out, nil
}

// Get returns one mail with attachments, or ErrMailNotFound when absent or
// addressed to someone else.
func (r *GORMMailRepository) Get(ctx context.Context, destCharID uint32, id domain.MailID) (domain.Mail, error) {
	var m domain.Mail
	err := r.db.WithContext(ctx).
		Where("id = ? AND dest_id = ?", id, destCharID).
		First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return domain.Mail{}, domain.ErrMailNotFound
		}
		return domain.Mail{}, err
	}
	items, err := r.items(ctx, m.ID)
	if err != nil {
		return domain.Mail{}, err
	}
	m.Items = items
	return m, nil
}

func (r *GORMMailRepository) items(ctx context.Context, id domain.MailID) ([]domain.Attachment, error) {
	var items []domain.Attachment
	// `index` is a reserved word in MariaDB (and quoted per dialect through
	// clause.Column — backticks on MariaDB, double quotes on postgres).
	if err := r.db.WithContext(ctx).
		Where("id = ?", id).Order(clause.Column{Name: "index"}).Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

// SetStatus persists a status change.
func (r *GORMMailRepository) SetStatus(ctx context.Context, id domain.MailID, status uint8) error {
	err := r.db.WithContext(ctx).Model(&domain.Mail{}).
		Where("id = ?", id).Update("status", status).Error
	if err != nil {
		return fmt.Errorf("mail set status: %w", err)
	}
	return nil
}

// ClearZeny zeroes the zeny column after a collect (int_mail.cpp:398).
func (r *GORMMailRepository) ClearZeny(ctx context.Context, id domain.MailID) error {
	err := r.db.WithContext(ctx).Model(&domain.Mail{}).
		Where("id = ?", id).Update("zeny", 0).Error
	if err != nil {
		return fmt.Errorf("mail clear zeny: %w", err)
	}
	return nil
}

// SetZeny restores the zeny column (collect-claim rollback).
func (r *GORMMailRepository) SetZeny(ctx context.Context, id domain.MailID, zeny int64) error {
	err := r.db.WithContext(ctx).Model(&domain.Mail{}).
		Where("id = ?", id).Update("zeny", zeny).Error
	if err != nil {
		return fmt.Errorf("mail set zeny: %w", err)
	}
	return nil
}

// ClearItems removes every attachment row after an item collect
// (mail_DeleteAttach, int_mail.cpp:362).
func (r *GORMMailRepository) ClearItems(ctx context.Context, id domain.MailID) error {
	err := r.db.WithContext(ctx).
		Where("id = ?", id).Delete(&domain.Attachment{}).Error
	if err != nil {
		return fmt.Errorf("mail clear items: %w", err)
	}
	return nil
}

// Delete removes the mail; attachments follow the schema's ON DELETE CASCADE.
func (r *GORMMailRepository) Delete(ctx context.Context, id domain.MailID) error {
	res := r.db.WithContext(ctx).Where("id = ?", id).Delete(&domain.Mail{})
	if res.Error != nil {
		return fmt.Errorf("mail delete: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return domain.ErrMailNotFound
	}
	return nil
}

// LookupRecipient resolves a char by exact name.
func (r *GORMMailRepository) LookupRecipient(ctx context.Context, name string) (domain.Recipient, error) {
	var row recipientRow
	err := r.db.WithContext(ctx).
		Where("name = ?", name).First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return domain.Recipient{}, domain.ErrRecipientNotFound
		}
		return domain.Recipient{}, err
	}
	return domain.Recipient{
		CharID:    row.CharID,
		AccountID: row.AccountID,
		Name:      row.Name,
		Class:     row.Class,
		BaseLevel: row.BaseLevel,
	}, nil
}

// CountUnread returns NEW+UNREAD rows for the inbox icon.
func (r *GORMMailRepository) CountUnread(ctx context.Context, destCharID uint32) (int, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&domain.Mail{}).
		Where("dest_id = ? AND status < ?", destCharID, domain.StatusRead).
		Count(&n).Error
	if err != nil {
		return 0, fmt.Errorf("mail count unread: %w", err)
	}
	return int(n), nil
}
