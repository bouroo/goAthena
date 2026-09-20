package infra

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/bouroo/goAthena/internal/modules/social/mail/domain"
)

// MemoryMailRepository is the in-memory mail repository for unit tests and
// DB-less harnesses.
type MemoryMailRepository struct {
	mu    sync.Mutex
	next  domain.MailID
	mails map[domain.MailID]*domain.Mail
	chars map[string]domain.Recipient // exact name → recipient
}

// NewMemoryMailRepository builds an empty repository. RegisterLookup seeds the
// recipient table.
func NewMemoryMailRepository() *MemoryMailRepository {
	return &MemoryMailRepository{
		next:  1,
		mails: map[domain.MailID]*domain.Mail{},
		chars: map[string]domain.Recipient{},
	}
}

// RegisterLookup seeds a resolvable char name (test helper).
func (r *MemoryMailRepository) RegisterLookup(rec domain.Recipient) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.chars[rec.Name] = rec
}

// Create inserts the mail with its attachments.
func (r *MemoryMailRepository) Create(_ context.Context, m *domain.Mail) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, other := range r.mails {
		if other.DestID == m.DestID {
			n++
		}
	}
	if n >= domain.MaxInbox {
		return domain.ErrInboxFull
	}
	cp := *m
	cp.ID = r.next
	r.next++
	cp.Items = append([]domain.Attachment(nil), m.Items...)
	for i := range cp.Items {
		cp.Items[i].MailID = int64(cp.ID)
		cp.Items[i].Index = i
	}
	r.mails[cp.ID] = &cp
	*m = cp
	return nil
}

// Inbox returns up to MaxInbox mails ascending by id, flipping NEW to UNREAD.
func (r *MemoryMailRepository) Inbox(_ context.Context, destCharID uint32) ([]domain.Mail, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]domain.Mail, 0, len(r.mails))
	for _, m := range r.mails {
		if m.DestID != destCharID {
			continue
		}
		if m.Status == domain.StatusNew {
			m.Status = domain.StatusUnread
		}
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) > domain.MaxInbox {
		out = out[:domain.MaxInbox]
	}
	return out, nil
}

// Get returns one mail or ErrMailNotFound.
func (r *MemoryMailRepository) Get(_ context.Context, destCharID uint32, id domain.MailID) (domain.Mail, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, ok := r.mails[id]
	if !ok || m.DestID != destCharID {
		return domain.Mail{}, domain.ErrMailNotFound
	}
	return *m, nil
}

// SetStatus persists a status change.
func (r *MemoryMailRepository) SetStatus(_ context.Context, id domain.MailID, status uint8) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, ok := r.mails[id]
	if !ok {
		return domain.ErrMailNotFound
	}
	m.Status = status
	return nil
}

// ClearZeny zeroes the zeny column.
func (r *MemoryMailRepository) ClearZeny(_ context.Context, id domain.MailID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, ok := r.mails[id]
	if !ok {
		return domain.ErrMailNotFound
	}
	m.Zeny = 0
	return nil
}

// SetZeny restores the zeny column.
func (r *MemoryMailRepository) SetZeny(_ context.Context, id domain.MailID, zeny int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, ok := r.mails[id]
	if !ok {
		return domain.ErrMailNotFound
	}
	m.Zeny = zeny
	return nil
}

// ClearItems removes every attachment row.
func (r *MemoryMailRepository) ClearItems(_ context.Context, id domain.MailID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, ok := r.mails[id]
	if !ok {
		return domain.ErrMailNotFound
	}
	m.Items = nil
	return nil
}

// Delete removes the mail.
func (r *MemoryMailRepository) Delete(_ context.Context, id domain.MailID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.mails[id]; !ok {
		return domain.ErrMailNotFound
	}
	delete(r.mails, id)
	return nil
}

// LookupRecipient resolves an exact char name.
func (r *MemoryMailRepository) LookupRecipient(_ context.Context, name string) (domain.Recipient, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rec, ok := r.chars[name]; ok {
		return rec, nil
	}
	return domain.Recipient{}, fmt.Errorf("mail lookup: %w", domain.ErrRecipientNotFound)
}

// CountUnread returns NEW+UNREAD rows.
func (r *MemoryMailRepository) CountUnread(_ context.Context, destCharID uint32) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, m := range r.mails {
		if m.DestID == destCharID && m.Status < domain.StatusRead {
			n++
		}
	}
	return n, nil
}
