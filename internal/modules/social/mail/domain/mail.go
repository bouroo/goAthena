// Package domain holds the mail bounded context's pure model: the Mail
// aggregate (mirrors rAthena's `mail` + `mail_attachments` tables) and its
// persistence port.
//
// Scope note: this slice models the RODEX core — send with zeny/item
// attachments, inbox, read, delete, collect. Item random options and
// enchantgrade are not modeled anywhere in goAthena yet, so attachment rows
// carry the rAthena columns that exist in the inventory Item aggregate.
package domain

import (
	"context"
	"errors"
	"time"
)

// MailID is rAthena's mail.id (auto-increment per row).
type MailID int64

// Inbox types and status values mirror mmo.hpp mail_inbox_type / mail_status.
const (
	TypeNormal   uint8 = 0
	TypeAccount  uint8 = 1
	TypeReturned uint8 = 2

	StatusNew    uint8 = 0
	StatusUnread uint8 = 1
	StatusRead   uint8 = 2
)

// RODEX ceilings (mmo.hpp:192-199): 30 inbox rows, 40-byte title (45-char
// column headroom), 500-byte body, 5 attachment slots.
const (
	MaxInbox   = 30
	TitleMax   = 40
	BodyMax    = 500
	MaxItems   = 5
	FakeExpiry = 365 * 24 * 60 * 60 // seconds; rAthena fakes the delete countdown (clif.cpp refreshinbox)
)

// Attachment is one mail attachment slot (rAthena mail_attachments row,
// keyed (id, index) with ON DELETE CASCADE). The inventory row is snapshotted
// at send time; ID/CharID are the sender's row identity and are not
// meaningful to the receiver.
type Attachment struct {
	MailID    int64  `gorm:"column:id;primaryKey"`
	Index     int    `gorm:"column:index;primaryKey"`
	NameID    uint32 `gorm:"column:nameid"`
	Amount    uint32 `gorm:"column:amount"`
	Refine    uint8  `gorm:"column:refine"`
	Attribute uint8  `gorm:"column:attribute"`
	Identify  int16  `gorm:"column:identify"`
	Card0     uint32 `gorm:"column:card0"`
	Card1     uint32 `gorm:"column:card1"`
	Card2     uint32 `gorm:"column:card2"`
	Card3     uint32 `gorm:"column:card3"`
	Bound     uint8  `gorm:"column:bound"`
	UniqueID  uint64 `gorm:"column:unique_id"` // carries the sender's inventory row id through Send's Remove; persists as the rAthena GUID column
	Time      int64  `gorm:"column:timestamp"` // send time (unix seconds); rAthena snapshots item->timestamp at send (mail.cpp)
}

// TableName pins the singular table name so GORM does not pluralize it.
func (Mail) TableName() string { return "mail" }

// TableName pins the singular table name so GORM does not pluralize it.
func (Attachment) TableName() string { return "mail_attachments" }

// Mail mirrors the rAthena `mail` table plus its attachment rows.
type Mail struct {
	ID        MailID `gorm:"column:id;primaryKey;autoIncrement"`
	SenderID  uint32 `gorm:"column:send_id"`
	SenderNam string `gorm:"column:send_name;size:30"`
	DestID    uint32 `gorm:"column:dest_id"`
	DestName  string `gorm:"column:dest_name;size:30"`
	Title     string `gorm:"column:title;size:45"`
	Body      string `gorm:"column:message;size:500"`
	Time      int64  `gorm:"column:time"` // unix seconds
	Status    uint8  `gorm:"column:status"`
	Zeny      int64  `gorm:"column:zeny"`
	Type      uint16 `gorm:"column:type"`

	Items []Attachment `gorm:"-"`
}

// Recipient is the `char` projection send and receiver-check need.
type Recipient struct {
	CharID    uint32
	AccountID uint32
	Name      string
	Class     uint16
	BaseLevel uint16
}

var (
	// ErrMailNotFound is returned when no mail row matches the id, or the
	// requester is not the destination.
	ErrMailNotFound = errors.New("mail not found")

	// ErrRecipientNotFound is returned when the send target name resolves to
	// no char row (clif_Mail_send WRITE_MAIL_FAILED, intif path).
	ErrRecipientNotFound = errors.New("mail recipient not found")

	// ErrMailHasAttachment is returned when deleting a mail that still holds
	// zeny or items (clif_parse_Mail_delete refusal, clif.cpp:16665-16676).
	ErrMailHasAttachment = errors.New("mail still has attachments")

	// ErrInboxFull is returned when the inbox already holds MaxInbox mails.
	ErrInboxFull = errors.New("inbox full")
)

// MailRepository is the persistence port for the mail aggregate.
type MailRepository interface {
	// Create inserts the mail (dest pre-resolved) with its attachments in one
	// transaction, assigns the id, and returns it. Rejects with ErrInboxFull
	// when the destination already holds MaxInbox mails.
	Create(ctx context.Context, m *Mail) error

	// Inbox returns up to MaxInbox mails for dest ascending by id — the
	// rAthena mail_fromsql read (int_mail.cpp:37). NEW rows are flipped to
	// UNREAD (int_mail.cpp:64-71); the caller counts them from the result.
	Inbox(ctx context.Context, destCharID uint32) ([]Mail, error)

	// Get returns one mail with attachments. ErrMailNotFound when absent or
	// not addressed to destCharID.
	Get(ctx context.Context, destCharID uint32, id MailID) (Mail, error)

	// SetStatus persists a status change (NEW→UNREAD on inbox load, UNREAD→
	// READ on open — intif_Mail_read).
	SetStatus(ctx context.Context, id MailID, status uint8) error

	// ClearZeny zeroes the zeny column after a successful collect
	// (mapif_Mail_getattach UPDATE mail SET zeny = 0).
	ClearZeny(ctx context.Context, id MailID) error

	// SetZeny restores the zeny column (the collect-claim rollback when the
	// credit side of CollectZeny fails).
	SetZeny(ctx context.Context, id MailID, zeny int64) error

	// ClearItems removes every attachment row after a successful item collect
	// (mail_DeleteAttach).
	ClearItems(ctx context.Context, id MailID) error

	// Delete removes the mail and (via the schema's ON DELETE CASCADE) its
	// attachments. ErrMailNotFound when absent.
	Delete(ctx context.Context, id MailID) error

	// LookupRecipient resolves a char by exact name — the mapif_parse_Mail_send
	// and mapif_parse_Mail_receiver_check reads (int_mail.cpp:607, :702).
	LookupRecipient(ctx context.Context, name string) (Recipient, error)

	// CountUnread returns how many inbox rows are NEW or UNREAD for the icon
	// packet (clif_Mail_new: unread + unchecked).
	CountUnread(ctx context.Context, destCharID uint32) (int, error)
}

// Now is the clock seam (tests pin it).
var Now = time.Now
