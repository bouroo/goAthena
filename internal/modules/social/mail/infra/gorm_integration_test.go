//go:build integration

package infra_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"gorm.io/gorm"

	"github.com/bouroo/goAthena/internal/config"
	"github.com/bouroo/goAthena/internal/infra/testdb"
	"github.com/bouroo/goAthena/internal/infrastructure/db"
	maildomain "github.com/bouroo/goAthena/internal/modules/social/mail/domain"
	mailinfra "github.com/bouroo/goAthena/internal/modules/social/mail/infra"
)

func mailDBForTest(t *testing.T) *gorm.DB {
	t.Helper()
	cfg := config.DBConfig{
		Driver:   envOr("DB_DRIVER", "mariadb"),
		Host:     envOr("DB_HOST", "127.0.0.1"),
		Port:     envInt("DB_PORT", 13306),
		Name:     envOr("DB_NAME", "n"),
		User:     envOr("DB_USER", "r"),
		Password: envOr("DB_PASSWORD", "r"),
		SSLMode:  "disable",
	}
	gdb, err := db.New(cfg)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(gdb) })
	return gdb
}

func mailTestMain(m *testing.M) {
	driver := os.Getenv("DB_DRIVER")
	if driver == "" {
		driver = "mariadb"
	}
	if _, err := testdb.Setup(driver); err != nil {
		fmt.Fprintf(os.Stderr, "testdb setup: %v\n", err)
		os.Exit(1)
	}
	code := m.Run()
	testdb.Terminate()
	os.Exit(code)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
			return n
		}
	}
	return fallback
}

// insertMailChar inserts a `char` row and returns its id (DB-assigned, read
// back through the unique name — the same shape the friend suite uses).
func insertMailChar(t *testing.T, gdb *gorm.DB, name string) uint32 {
	t.Helper()
	err := gdb.Table("char").Create(map[string]any{
		"account_id": 3000001,
		"char_num":   0,
		"name":       name,
		"class":      1,
		"base_level": 10,
		"last_map":   "prontera",
		"online":     0,
		"party_id":   0,
	}).Error
	if err != nil {
		t.Fatalf("insert char %s: %v", name, err)
	}
	var charID uint32
	if err := gdb.Table("char").Select("char_id").Where("name = ?", name).
		Scan(&charID).Error; err != nil {
		t.Fatalf("read back char %s: %v", name, err)
	}
	t.Cleanup(func() {
		_ = gdb.Table("mail_attachments").Where("id IN (?)",
			gdb.Table("mail").Select("id").Where("dest_id = ?", charID)).
			Delete(nil).Error
		_ = gdb.Table("mail").Where("dest_id = ?", charID).Delete(nil).Error
		_ = gdb.Table("char").Where("char_id = ?", charID).Delete(nil).Error
	})
	return charID
}

// TestMail_GORMRoundTrip proves the mail repository's full lifecycle against a
// real DB: Create inserts the mail with its attachment rows, Inbox orders
// ascending and flips NEW→UNREAD, the collect claims (ClearZeny/SetZeny,
// ClearItems) mutate only what they name, Delete cascades the attachment rows,
// and the sentinels fire (not-found, wrong dest, inbox cap).
func TestMail_GORMRoundTrip(t *testing.T) {
	gdb := mailDBForTest(t)
	repo := mailinfra.NewGORMMailRepository(gdb)
	ctx := context.Background()

	hero := insertMailChar(t, gdb, "MailHero")
	partner := insertMailChar(t, gdb, "MailPartner")

	// Create: the id is assigned and both attachment rows persist.
	m := &maildomain.Mail{
		SenderID: hero, SenderNam: "MailHero",
		DestID: partner, DestName: "MailPartner",
		Title: "hello", Body: "world", Status: maildomain.StatusNew,
		Zeny: 500, Type: uint16(maildomain.TypeNormal),
		Items: []maildomain.Attachment{
			{NameID: 501, Amount: 3, Identify: 1},
			{NameID: 1201, Amount: 1, Identify: 1, Refine: 7, Card0: 4001},
		},
	}
	if err := repo.Create(ctx, m); err != nil {
		t.Fatalf("create: %v", err)
	}
	if m.ID == 0 {
		t.Fatal("create left the mail id unassigned")
	}

	// Get is dest-scoped; the wrong dest sees nothing.
	got, err := repo.Get(ctx, partner, m.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Zeny != 500 || len(got.Items) != 2 || got.Items[1].Refine != 7 || got.Items[1].Card0 != 4001 {
		t.Fatalf("get = %+v", got)
	}
	if _, err := repo.Get(ctx, hero, m.ID); !errors.Is(err, maildomain.ErrMailNotFound) {
		t.Fatalf("get as sender err = %v, want ErrMailNotFound", err)
	}

	// Inbox flips NEW→UNREAD and counts the row as unread.
	mails, err := repo.Inbox(ctx, partner)
	if err != nil || len(mails) != 1 {
		t.Fatalf("inbox = %d mails, err %v", len(mails), err)
	}
	if mails[0].Status != maildomain.StatusUnread {
		t.Fatalf("inbox status = %d, want UNREAD", mails[0].Status)
	}
	if n, err := repo.CountUnread(ctx, partner); err != nil || n != 1 {
		t.Fatalf("count unread = %d, err %v", n, err)
	}

	// The zeny claim: clear then restore, both observable.
	if err := repo.ClearZeny(ctx, m.ID); err != nil {
		t.Fatalf("clear zeny: %v", err)
	}
	got, _ = repo.Get(ctx, partner, m.ID)
	if got.Zeny != 0 {
		t.Fatalf("zeny after clear = %d, want 0", got.Zeny)
	}
	if err := repo.SetZeny(ctx, m.ID, 500); err != nil {
		t.Fatalf("set zeny: %v", err)
	}

	// Read-side status flip.
	if err := repo.SetStatus(ctx, m.ID, maildomain.StatusRead); err != nil {
		t.Fatalf("set status: %v", err)
	}
	if n, _ := repo.CountUnread(ctx, partner); n != 0 {
		t.Fatalf("count unread after read = %d, want 0", n)
	}

	// ClearItems removes the attachment rows.
	if err := repo.ClearItems(ctx, m.ID); err != nil {
		t.Fatalf("clear items: %v", err)
	}
	got, _ = repo.Get(ctx, partner, m.ID)
	if len(got.Items) != 0 {
		t.Fatalf("items after clear = %d, want 0", len(got.Items))
	}

	// Delete cascades: a fresh mail with attachments loses both rows.
	cascade := &maildomain.Mail{
		SenderID: hero, SenderNam: "MailHero",
		DestID: partner, DestName: "MailPartner",
		Title: "cascade", Status: maildomain.StatusNew,
		Items: []maildomain.Attachment{{NameID: 501, Amount: 1, Identify: 1}},
	}
	if err := repo.Create(ctx, cascade); err != nil {
		t.Fatalf("create cascade mail: %v", err)
	}
	if err := repo.Delete(ctx, cascade.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	var attachRows int64
	if err := gdb.Table("mail_attachments").Where("id = ?", cascade.ID).
		Count(&attachRows).Error; err != nil {
		t.Fatalf("count attachment rows: %v", err)
	}
	if attachRows != 0 {
		t.Fatalf("attachment rows survived delete = %d, want 0 (cascade)", attachRows)
	}
	if err := repo.Delete(ctx, cascade.ID); !errors.Is(err, maildomain.ErrMailNotFound) {
		t.Fatalf("repeat delete err = %v, want ErrMailNotFound", err)
	}

	// LookupRecipient resolves the char row; an unknown name is the sentinel.
	rec, err := repo.LookupRecipient(ctx, "MailPartner")
	if err != nil || rec.CharID != partner {
		t.Fatalf("lookup = %+v, err %v", rec, err)
	}
	if _, err := repo.LookupRecipient(ctx, "Nobody"); !errors.Is(err, maildomain.ErrRecipientNotFound) {
		t.Fatalf("unknown lookup err = %v, want ErrRecipientNotFound", err)
	}
}

// TestMail_GORMInboxCap proves the 30-row inbox cap: the 31st Create is
// refused with ErrInboxFull and the receiver's Inbox stays at the cap.
func TestMail_GORMInboxCap(t *testing.T) {
	gdb := mailDBForTest(t)
	repo := mailinfra.NewGORMMailRepository(gdb)
	ctx := context.Background()

	hero := insertMailChar(t, gdb, "MailCapHero")
	filler := insertMailChar(t, gdb, "MailCapFiller")

	for i := 0; i < maildomain.MaxInbox; i++ {
		m := &maildomain.Mail{
			SenderID: hero, SenderNam: "MailCapHero",
			DestID: filler, DestName: "MailCapFiller",
			Title: fmt.Sprintf("mail %02d", i), Status: maildomain.StatusNew,
		}
		if err := repo.Create(ctx, m); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}
	over := &maildomain.Mail{
		SenderID: hero, SenderNam: "MailCapHero",
		DestID: filler, DestName: "MailCapFiller",
		Title: "one too many", Status: maildomain.StatusNew,
	}
	if err := repo.Create(ctx, over); !errors.Is(err, maildomain.ErrInboxFull) {
		t.Fatalf("over-cap create err = %v, want ErrInboxFull", err)
	}
	mails, err := repo.Inbox(ctx, filler)
	if err != nil {
		t.Fatalf("inbox: %v", err)
	}
	if len(mails) != maildomain.MaxInbox {
		t.Fatalf("inbox size = %d, want %d", len(mails), maildomain.MaxInbox)
	}
	for i := 1; i < len(mails); i++ {
		if mails[i-1].ID >= mails[i].ID {
			t.Fatalf("inbox not ascending at %d", i)
		}
	}
}

func TestMain(m *testing.M) { mailTestMain(m) }
