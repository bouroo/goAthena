// Package app implements the mail bounded context use cases: send with zeny /
// item attachments, inbox load, read, delete, and attachment collection.
//
// MailService owns the delivery state machine and the fee math. Where the
// result is delivered (each party's connection) and how attachments are staged
// while the compose window is open are gateway concerns, mirroring how rAthena
// keeps sd->mail staging on the session while intif/char-server own delivery.
package app

import (
	"context"
	"errors"
	"fmt"

	invdomain "github.com/bouroo/goAthena/internal/modules/inventory/domain"
	"github.com/bouroo/goAthena/internal/modules/social/mail/domain"
)

// maxZeny is rAthena MAX_ZENY (mmo.hpp) — the collect-side overflow guard.
const maxZeny = 2_000_000_000

// Fee defaults from conf/battle/mail.conf (misc.conf): 2% zeny tax, 2500 zeny
// per item attachment. mail_daily_count and the return/delete expiry timers
// are deferred (no cron in the monolith yet).
const (
	zenyFeePercent = 2
	attachPrice    = 2500
)

// EconomyPort is the narrow surface the mail service needs from the economy
// module (same local-port pattern as commerce/shop).
type EconomyPort interface {
	GetZeny(ctx context.Context, charID uint32) (int32, error)
	DeductZeny(ctx context.Context, charID uint32, amount int32) error
	CreditZeny(ctx context.Context, charID uint32, amount int32) error
}

// InventoryPort is the bag surface attachments move through (a subset of the
// inventory module's repository; *inventoryapp.InventoryService satisfies it).
// ponytail: Add stacks by nameid and drops card/refine identity, the same
// ceiling storage withdraw already has; both upgrade together when inventory
// gains a row-preserving insert.
type InventoryPort interface {
	Add(ctx context.Context, charID, nameID uint32, amount int) (invdomain.Item, error)
	Remove(ctx context.Context, id invdomain.ItemID, amount int) error
}

// clampRunes truncates to n runes (rAthena's safestrncpy truncates bytes; the
// wire bound is a byte budget, but Go strings must stay valid UTF-8, so a
// rune cap is the safe reading — the byte result is ≤ the cap for ASCII and
// well under the 500-byte body budget for CJK).
func clampRunes(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

// MailService is the mail use-case service.
type MailService struct {
	repo domain.MailRepository
	econ EconomyPort
	inv  InventoryPort
}

// NewMailService builds a MailService. econ and inv are optional (nil skips
// the corresponding movement) so pure-delivery harnesses still run.
func NewMailService(repo domain.MailRepository, econ EconomyPort, inv InventoryPort) *MailService {
	return &MailService{repo: repo, econ: econ, inv: inv}
}

// Send resolves the recipient, charges zeny + fees, lifts the staged items
// from the sender's bag, and inserts the mail. Returns the recipient's charID
// so the gateway can push the new-mail icon to an online receiver.
//
// rAthena clamps title/body to the wire bounds (safestrncpy), never rejects.
// Send errors map to ZC_ACK_WRITE_MAIL result 1 in the gateway.
func (s *MailService) Send(ctx context.Context, senderCharID uint32, senderName, receiverName, title, body string, zeny int64, items []domain.Attachment) (uint32, error) {
	title = clampRunes(title, domain.TitleMax)
	body = clampRunes(body, domain.BodyMax)
	if title == "" {
		return 0, domain.ErrRecipientNotFound // unreachable via the client; the blank-title send is silently dropped (clif.cpp:16863)
	}
	rec, err := s.repo.LookupRecipient(ctx, receiverName)
	if err != nil {
		return 0, fmt.Errorf("mail send: lookup recipient: %w", err)
	}
	if zeny < 0 {
		// A negative staged zeny would flip the charge total non-positive and
		// skip the balance check entirely — refuse before anything moves.
		return 0, fmt.Errorf("mail send: negative zeny %d", zeny)
	}
	total, err := s.charge(ctx, senderCharID, zeny, len(items))
	if err != nil {
		return 0, err
	}
	if err := s.lift(ctx, senderCharID, items, total); err != nil {
		return 0, err
	}

	now := domain.Now().Unix()
	// Stamp each attachment with the send time: rAthena snapshots
	// item->timestamp = time(NULL) at send (mail.cpp mail_setattachment),
	// and the ZC_ACK_READ_RODEX "expires" field renders it.
	for i := range items {
		items[i].Time = now
	}
	m := &domain.Mail{
		SenderID:  senderCharID,
		SenderNam: senderName,
		DestID:    rec.CharID,
		DestName:  rec.Name,
		Title:     title,
		Body:      body,
		Time:      now,
		Status:    domain.StatusNew,
		Zeny:      zeny,
		Type:      uint16(domain.TypeNormal),
		Items:     items,
	}
	if err := s.repo.Create(ctx, m); err != nil {
		// The insert failed after the zeny and items already left the
		// sender — compensate both back (rAthena's mail_deliveryfail
		// returns the attachments to the sender's inventory).
		if cerr := s.compensate(ctx, senderCharID, items, total); cerr != nil {
			return 0, fmt.Errorf("mail send: insert: %w (compensation failed: %v)", err, cerr)
		}
		return 0, fmt.Errorf("mail send: insert: %w", err)
	}
	return rec.CharID, nil
}

// charge deducts the staged zeny plus the send fee and per-item attachment
// price (conf/battle/mail.conf). Returns the total moved — 0 when there is
// nothing to charge.
func (s *MailService) charge(ctx context.Context, charID uint32, zeny int64, nItems int) (int64, error) {
	total := zeny + zeny*zenyFeePercent/100 + int64(nItems)*attachPrice
	if total <= 0 {
		return 0, nil
	}
	if total > maxZeny {
		return 0, fmt.Errorf("mail send: total %d exceeds cap", total)
	}
	if s.econ == nil {
		return 0, fmt.Errorf("mail send: economy not wired")
	}
	bal, err := s.econ.GetZeny(ctx, charID)
	if err != nil {
		return 0, fmt.Errorf("mail send: balance: %w", err)
	}
	if int64(bal) < total {
		return 0, fmt.Errorf("mail send: need %d zeny, have %d", total, bal)
	}
	if err := s.econ.DeductZeny(ctx, charID, int32(total)); err != nil { //nolint:gosec // G115: capped above.
		return 0, fmt.Errorf("mail send: deduct: %w", err)
	}
	return total, nil
}

// lift removes the staged attachments from the bag. A row that vanished
// mid-compose fails the whole send; compensate returns everything already
// moved, so a failed send never eats the sender's property (rAthena's
// mail_deliveryfail path).
func (s *MailService) lift(ctx context.Context, charID uint32, items []domain.Attachment, charged int64) error {
	if len(items) == 0 {
		return nil
	}
	if s.inv == nil {
		return fmt.Errorf("mail send: inventory not wired")
	}
	for i, it := range items {
		if err := s.inv.Remove(ctx, invdomain.ItemID(it.UniqueID), int(it.Amount)); err != nil { //nolint:gosec // G115: amounts are bounded far below 2^31.
			if cerr := s.compensate(ctx, charID, items[:i], charged); cerr != nil {
				return fmt.Errorf("mail send: lift attachment: %w (compensation failed: %v)", err, cerr)
			}
			return fmt.Errorf("mail send: lift attachment: %w", err)
		}
	}
	return nil
}

// compensate returns the charged zeny and re-adds the already-lifted items.
// Best effort: per-item failures are collected, not fatal, so one poisoned
// row cannot block the rest. The re-add stacks by nameid and drops
// refine/cards (the InventoryPort ceiling; see its ponytail note) — the same
// degenerate restoration rAthena accepts when the original row is gone.
func (s *MailService) compensate(ctx context.Context, charID uint32, items []domain.Attachment, charged int64) error {
	var errs []error
	if charged > 0 && s.econ != nil {
		if err := s.econ.CreditZeny(ctx, charID, int32(charged)); err != nil { //nolint:gosec // G115: capped above.
			errs = append(errs, err)
		}
	}
	if s.inv != nil {
		for _, it := range items {
			if _, err := s.inv.Add(ctx, charID, it.NameID, int(it.Amount)); err != nil { //nolint:gosec // G115: amounts are bounded far below 2^31.
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// InboxResult carries the inbox page plus the unread count the icon packet
// needs (rAthena's unread + unchecked blend, clif_Mail_new).
type InboxResult struct {
	Mails  []domain.Mail
	Unread int
}

// Inbox loads the destination's inbox (≤ MaxInbox, ascending), flipping NEW
// rows to UNREAD the way mail_fromsql does.
func (s *MailService) Inbox(ctx context.Context, charID uint32) (InboxResult, error) {
	mails, err := s.repo.Inbox(ctx, charID)
	if err != nil {
		return InboxResult{}, fmt.Errorf("mail inbox: %w", err)
	}
	unread, err := s.repo.CountUnread(ctx, charID)
	if err != nil {
		return InboxResult{}, fmt.Errorf("mail inbox: %w", err)
	}
	return InboxResult{Mails: mails, Unread: unread}, nil
}

// Read returns one mail, marking it READ on first open (intif_Mail_read).
func (s *MailService) Read(ctx context.Context, charID uint32, id domain.MailID) (domain.Mail, error) {
	m, err := s.repo.Get(ctx, charID, id)
	if err != nil {
		return domain.Mail{}, fmt.Errorf("mail read: %w", err)
	}
	if m.Status != domain.StatusRead {
		if err := s.repo.SetStatus(ctx, id, domain.StatusRead); err != nil {
			return domain.Mail{}, fmt.Errorf("mail read: %w", err)
		}
		m.Status = domain.StatusRead
	}
	return m, nil
}

// Delete removes a mail — refused while any attachment remains
// (clif_parse_Mail_delete, clif.cpp:16665-16676).
func (s *MailService) Delete(ctx context.Context, charID uint32, id domain.MailID) error {
	m, err := s.repo.Get(ctx, charID, id)
	if err != nil {
		return fmt.Errorf("mail delete: %w", err)
	}
	if m.Zeny > 0 || len(m.Items) > 0 {
		return domain.ErrMailHasAttachment
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("mail delete: %w", err)
	}
	return nil
}

// CollectZeny pays the mail's zeny into the receiver's balance and zeroes the
// column. The claim comes first (zero the column, then pay): a transient
// credit failure is rolled back by restoring the column, whereas the reverse
// order would leave a mail still showing zeny after a paid collect — the
// client's retry would then pay it twice. Result maps 1:1 onto the 09f2 ack:
// nil → OK, sentinel → failure.
func (s *MailService) CollectZeny(ctx context.Context, charID uint32, id domain.MailID) error {
	m, err := s.repo.Get(ctx, charID, id)
	if err != nil {
		return fmt.Errorf("mail collect zeny: %w", err)
	}
	if m.Zeny <= 0 {
		return domain.ErrMailNotFound // nothing to collect: rAthena silently ignores
	}
	if s.econ == nil {
		return fmt.Errorf("mail collect zeny: economy not wired")
	}
	bal, err := s.econ.GetZeny(ctx, charID)
	if err != nil {
		return fmt.Errorf("mail collect zeny: %w", err)
	}
	if int64(bal)+m.Zeny > maxZeny {
		return fmt.Errorf("mail collect zeny: %w", errZenyOverflow)
	}
	if err := s.repo.ClearZeny(ctx, id); err != nil {
		return fmt.Errorf("mail collect zeny: %w", err)
	}
	if err := s.econ.CreditZeny(ctx, charID, int32(m.Zeny)); err != nil { //nolint:gosec // G115: overflow-guarded above.
		if rerr := s.repo.SetZeny(ctx, id, m.Zeny); rerr != nil {
			return fmt.Errorf("mail collect zeny: %w (restore failed: %v)", err, rerr)
		}
		return fmt.Errorf("mail collect zeny: %w", err)
	}
	return nil
}

// errZenyOverflow maps to the 09f2/09f4 result 1.
var errZenyOverflow = errors.New("zeny would overflow the cap")

// CollectItems moves every attachment into the receiver's bag and clears the
// rows. Partial adds leave the rows in place (the client re-requests), the
// same degenerate outcome rAthena's pending-tracking produces.
func (s *MailService) CollectItems(ctx context.Context, charID uint32, id domain.MailID) error {
	m, err := s.repo.Get(ctx, charID, id)
	if err != nil {
		return fmt.Errorf("mail collect items: %w", err)
	}
	if len(m.Items) == 0 {
		return domain.ErrMailNotFound
	}
	if s.inv == nil {
		return fmt.Errorf("mail collect items: inventory not wired")
	}
	for _, it := range m.Items {
		if _, err := s.inv.Add(ctx, charID, it.NameID, int(it.Amount)); err != nil { //nolint:gosec // G115: amounts are bounded far below 2^31.
			return fmt.Errorf("mail collect items: %w", err)
		}
	}
	if err := s.repo.ClearItems(ctx, id); err != nil {
		return fmt.Errorf("mail collect items: %w", err)
	}
	return nil
}

// CheckReceiver resolves the compose window's recipient preview row.
func (s *MailService) CheckReceiver(ctx context.Context, name string) (domain.Recipient, error) {
	rec, err := s.repo.LookupRecipient(ctx, name)
	if err != nil {
		return domain.Recipient{}, fmt.Errorf("mail check receiver: %w", err)
	}
	return rec, nil
}

// UnreadCount feeds the ZC_NOTIFY_UNREADMAIL icon.
func (s *MailService) UnreadCount(ctx context.Context, charID uint32) (int, error) {
	n, err := s.repo.CountUnread(ctx, charID)
	if err != nil {
		return 0, fmt.Errorf("mail unread count: %w", err)
	}
	return n, nil
}
