package app

import (
	"context"
	"errors"
	"maps"
	"strings"
	"testing"

	invdomain "github.com/bouroo/goAthena/internal/modules/inventory/domain"
	"github.com/bouroo/goAthena/internal/modules/social/mail/domain"
	"github.com/bouroo/goAthena/internal/modules/social/mail/infra"
)

// fakeEconomy is the EconomyPort double.
type fakeEconomy struct {
	zeny       map[uint32]int32
	deducted   []int32
	credited   []int32
	failDedct  bool
	failCredit bool
}

func newFakeEconomy(seed map[uint32]int32) *fakeEconomy {
	z := map[uint32]int32{}
	maps.Copy(z, seed)
	return &fakeEconomy{zeny: z}
}

func (f *fakeEconomy) GetZeny(_ context.Context, charID uint32) (int32, error) {
	return f.zeny[charID], nil
}

func (f *fakeEconomy) DeductZeny(_ context.Context, charID uint32, amount int32) error {
	if f.failDedct {
		return errors.New("deduct failed")
	}
	f.zeny[charID] -= amount
	f.deducted = append(f.deducted, amount)
	return nil
}

func (f *fakeEconomy) CreditZeny(_ context.Context, charID uint32, amount int32) error {
	if f.failCredit {
		return errors.New("credit failed")
	}
	f.zeny[charID] += amount
	f.credited = append(f.credited, amount)
	return nil
}

// fakeInventory is the InventoryPort double.
type fakeInventory struct {
	rows       map[uint32]invdomain.Item // row id → item
	nextID     uint32
	failRemove map[uint32]bool // row ids whose Remove always fails
}

func newFakeInventory(rows ...invdomain.Item) *fakeInventory {
	f := &fakeInventory{rows: map[uint32]invdomain.Item{}, nextID: 1}
	for _, it := range rows {
		if it.ID == 0 {
			it.ID = invdomain.ItemID(f.nextID)
			f.nextID++
		}
		f.rows[uint32(it.ID)] = it
	}
	return f
}

func (f *fakeInventory) Add(_ context.Context, charID, nameID uint32, amount int) (invdomain.Item, error) {
	for _, it := range f.rows {
		if it.CharID == charID && it.NameID == nameID && !it.IsEquipped() {
			it.Amount += uint32(amount)
			f.rows[uint32(it.ID)] = it
			return it, nil
		}
	}
	f.nextID++
	it := invdomain.Item{ID: invdomain.ItemID(f.nextID), CharID: charID, NameID: nameID, Amount: uint32(amount)}
	f.rows[uint32(it.ID)] = it
	return it, nil
}

func (f *fakeInventory) Remove(_ context.Context, id invdomain.ItemID, amount int) error {
	if f.failRemove[uint32(id)] {
		return invdomain.ErrInsufficientAmount
	}
	it, ok := f.rows[uint32(id)]
	if !ok || it.Amount < uint32(amount) {
		return invdomain.ErrInsufficientAmount
	}
	it.Amount -= uint32(amount)
	if it.Amount == 0 {
		delete(f.rows, uint32(id))
		return nil
	}
	f.rows[uint32(id)] = it
	return nil
}

// failingCreateRepo wraps a repository whose Create always fails.
type failingCreateRepo struct {
	domain.MailRepository
	err error
}

func (r *failingCreateRepo) Create(_ context.Context, _ *domain.Mail) error { return r.err }

type mailFixture struct {
	svc  *MailService
	econ *fakeEconomy
	inv  *fakeInventory
	repo *infra.MemoryMailRepository
}

func newFixture(senderZeny int32, bag ...invdomain.Item) mailFixture {
	repo := infra.NewMemoryMailRepository()
	repo.RegisterLookup(domain.Recipient{CharID: 150002, AccountID: 2000002, Name: "Partner", Class: 6, BaseLevel: 99})
	repo.RegisterLookup(domain.Recipient{CharID: 150001, AccountID: 2000001, Name: "Hero", Class: 6, BaseLevel: 99})
	econ := newFakeEconomy(map[uint32]int32{150001: senderZeny})
	inv := newFakeInventory(bag...)
	return mailFixture{
		svc:  NewMailService(repo, econ, inv),
		econ: econ,
		inv:  inv,
		repo: repo,
	}
}

func row(id uint32, nameID uint32, amount uint32) invdomain.Item {
	return invdomain.Item{ID: invdomain.ItemID(id), CharID: 150001, NameID: nameID, Amount: amount}
}

func TestSendDeliversAndChargesFees(t *testing.T) {
	f := newFixture(10_000)
	stage := []domain.Attachment{{UniqueID: 7, NameID: 501, Amount: 3, Index: 0}}
	f.inv.rows[7] = row(7, 501, 5)

	dest, err := f.svc.Send(context.Background(), 150001, "Hero", "Partner", "hi", "body", 1000, stage)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if dest != 150002 {
		t.Fatalf("dest = %d, want 150002", dest)
	}
	// 1000 zeny + 2% fee (20) + one attachment price 2500 = 3520.
	if f.econ.zeny[150001] != 10_000-3520 {
		t.Fatalf("sender zeny = %d, want %d", f.econ.zeny[150001], 10_000-3520)
	}
	if got := f.inv.rows[7].Amount; got != 2 {
		t.Fatalf("bag row amount = %d, want 2", got)
	}
	res, err := f.svc.Inbox(context.Background(), 150002)
	if err != nil || len(res.Mails) != 1 {
		t.Fatalf("inbox = %+v err %v", res, err)
	}
	m := res.Mails[0]
	if m.SenderNam != "Hero" || m.DestName != "Partner" || m.Zeny != 1000 || len(m.Items) != 1 {
		t.Fatalf("mail = %+v", m)
	}
	if res.Unread != 1 {
		t.Fatalf("unread = %d, want 1", res.Unread)
	}
	if m.Status != domain.StatusUnread {
		t.Fatalf("status after inbox load = %d, want UNREAD", m.Status)
	}
}

func TestSendUnknownRecipientFails(t *testing.T) {
	f := newFixture(10_000)
	if _, err := f.svc.Send(context.Background(), 150001, "Hero", "Ghost", "hi", "", 0, nil); !errors.Is(err, domain.ErrRecipientNotFound) {
		t.Fatalf("err = %v, want ErrRecipientNotFound", err)
	}
}

func TestSendInsufficientZenyFails(t *testing.T) {
	f := newFixture(100) // fee+price (2500) unaffordable
	f.inv.rows[7] = row(7, 501, 5)
	if _, err := f.svc.Send(context.Background(), 150001, "Hero", "Partner", "hi", "", 0, []domain.Attachment{
		{UniqueID: 7, NameID: 501, Amount: 1, Index: 0},
	}); err == nil {
		t.Fatal("send with unpayable attachment fee succeeded")
	}
	// Nothing lifted from the bag.
	if _, ok := f.inv.rows[7]; !ok {
		t.Fatal("bag row was lifted despite failed send")
	}
}

func TestSendPartialLiftFailureCompensates(t *testing.T) {
	f := newFixture(10_000)
	f.inv.rows[7] = row(7, 501, 5)
	f.inv.rows[8] = row(8, 909, 1)
	f.inv.failRemove = map[uint32]bool{8: true}

	_, err := f.svc.Send(context.Background(), 150001, "Hero", "Partner", "hi", "", 1000, []domain.Attachment{
		{UniqueID: 7, NameID: 501, Amount: 2, Index: 0},
		{UniqueID: 8, NameID: 909, Amount: 1, Index: 1},
	})
	if err == nil {
		t.Fatal("send with a vanishing staged row succeeded")
	}
	// Charge (1000 + 20 fee + 2×2500) fully refunded.
	if got := f.econ.zeny[150001]; got != 10_000 {
		t.Fatalf("sender zeny after failed send = %d, want 10000", got)
	}
	// The first attachment went back into the bag (stacked onto its row).
	if got := f.inv.rows[7].Amount; got != 5 {
		t.Fatalf("bag row amount after compensation = %d, want 5", got)
	}
}

func TestSendCreateFailureCompensates(t *testing.T) {
	f := newFixture(10_000)
	f.inv.rows[7] = row(7, 501, 5)
	f.svc = NewMailService(&failingCreateRepo{MailRepository: f.repo, err: errors.New("db down")}, f.econ, f.inv)

	_, err := f.svc.Send(context.Background(), 150001, "Hero", "Partner", "hi", "", 1000, []domain.Attachment{
		{UniqueID: 7, NameID: 501, Amount: 2, Index: 0},
	})
	if err == nil {
		t.Fatal("send past a failing insert succeeded")
	}
	// Charge (1000 + 20 fee + 2500) fully refunded, attachment back in the bag.
	if got := f.econ.zeny[150001]; got != 10_000 {
		t.Fatalf("sender zeny after failed insert = %d, want 10000", got)
	}
	if got := f.inv.rows[7].Amount; got != 5 {
		t.Fatalf("bag row amount after compensation = %d, want 5", got)
	}
}

func TestInboxCapAndOldestFirst(t *testing.T) {
	f := newFixture(0)
	for i := range domain.MaxInbox {
		if _, err := f.svc.Send(context.Background(), 150001, "Hero", "Partner", "t", "", 0, nil); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}
	// The (MaxInbox+1)th is refused by the repository.
	if _, err := f.svc.Send(context.Background(), 150001, "Hero", "Partner", "t", "", 0, nil); !errors.Is(err, domain.ErrInboxFull) {
		t.Fatalf("over-cap send err = %v, want ErrInboxFull", err)
	}
	res, err := f.svc.Inbox(context.Background(), 150002)
	if err != nil {
		t.Fatalf("inbox: %v", err)
	}
	if len(res.Mails) != domain.MaxInbox {
		t.Fatalf("inbox size = %d, want %d", len(res.Mails), domain.MaxInbox)
	}
	for i := 1; i < len(res.Mails); i++ {
		if res.Mails[i-1].ID >= res.Mails[i].ID {
			t.Fatalf("inbox not ascending at %d", i)
		}
	}
}

func TestReadMarksRead(t *testing.T) {
	f := newFixture(0)
	dest, _ := f.svc.Send(context.Background(), 150001, "Hero", "Partner", "hi", "b", 0, nil)
	res, _ := f.svc.Inbox(context.Background(), dest)
	m, err := f.svc.Read(context.Background(), dest, res.Mails[0].ID)
	if err != nil || m.Status != domain.StatusRead {
		t.Fatalf("read = %+v err %v", m, err)
	}
	if n, _ := f.svc.UnreadCount(context.Background(), dest); n != 0 {
		t.Fatalf("unread after read = %d, want 0", n)
	}
	if _, err := f.svc.Read(context.Background(), dest, 9999); !errors.Is(err, domain.ErrMailNotFound) {
		t.Fatalf("missing read err = %v", err)
	}
}

func TestDeleteBlockedWithAttachments(t *testing.T) {
	f := newFixture(10_000)
	f.inv.rows[7] = row(7, 501, 5)
	dest, _ := f.svc.Send(context.Background(), 150001, "Hero", "Partner", "hi", "", 500, []domain.Attachment{
		{UniqueID: 7, NameID: 501, Amount: 1, Index: 0},
	})
	res, _ := f.svc.Inbox(context.Background(), dest)
	id := res.Mails[0].ID
	if err := f.svc.Delete(context.Background(), dest, id); !errors.Is(err, domain.ErrMailHasAttachment) {
		t.Fatalf("delete err = %v, want ErrMailHasAttachment", err)
	}
	if err := f.svc.CollectZeny(context.Background(), dest, id); err != nil {
		t.Fatalf("collect zeny: %v", err)
	}
	if err := f.svc.CollectItems(context.Background(), dest, id); err != nil {
		t.Fatalf("collect items: %v", err)
	}
	if err := f.svc.Delete(context.Background(), dest, id); err != nil {
		t.Fatalf("delete after collect: %v", err)
	}
	if f.econ.zeny[dest] != 500 {
		t.Fatalf("receiver zeny = %d, want 500", f.econ.zeny[dest])
	}
	if len(f.inv.rows) == 0 {
		t.Fatal("item attachment never landed in the bag")
	}
}

func TestCollectZenyOverflowRejected(t *testing.T) {
	f := newFixture(10_000)
	f.econ.zeny[150002] = int32(maxZeny)
	dest, _ := f.svc.Send(context.Background(), 150001, "Hero", "Partner", "hi", "", 100, nil)
	res, _ := f.svc.Inbox(context.Background(), dest)
	if err := f.svc.CollectZeny(context.Background(), dest, res.Mails[0].ID); err == nil {
		t.Fatal("overflow collect succeeded")
	}
	// Mail zeny stays put.
	m, _ := f.svc.repo.Get(context.Background(), dest, res.Mails[0].ID)
	if m.Zeny != 100 {
		t.Fatalf("zeny after rejected collect = %d", m.Zeny)
	}
}

func TestCollectZenyCreditFailureRestoresClaim(t *testing.T) {
	f := newFixture(10_000)
	dest, _ := f.svc.Send(context.Background(), 150001, "Hero", "Partner", "hi", "", 500, nil)
	res, _ := f.svc.Inbox(context.Background(), dest)
	id := res.Mails[0].ID

	f.econ.failCredit = true
	if err := f.svc.CollectZeny(context.Background(), dest, id); err == nil {
		t.Fatal("collect succeeded despite a failing credit")
	}
	// The claim was rolled back: the mail still carries its zeny, so the
	// client's retry collects it exactly once.
	m, err := f.svc.repo.Get(context.Background(), dest, id)
	if err != nil || m.Zeny != 500 {
		t.Fatalf("mail zeny after failed collect = %d, err %v, want 500", m.Zeny, err)
	}
	f.econ.failCredit = false
	if err := f.svc.CollectZeny(context.Background(), dest, id); err != nil {
		t.Fatalf("retry collect: %v", err)
	}
	if got := f.econ.zeny[dest]; got != 500 {
		t.Fatalf("receiver zeny after retry = %d, want 500", got)
	}
}

func TestSendNegativeZenyRefused(t *testing.T) {
	f := newFixture(10_000)
	if _, err := f.svc.Send(context.Background(), 150001, "Hero", "Partner", "hi", "", -1<<62, nil); err == nil {
		t.Fatal("negative-zeny send accepted")
	}
	// Nothing charged, nothing delivered.
	if got := f.econ.zeny[150001]; got != 10_000 {
		t.Fatalf("sender zeny = %d, want 10000", got)
	}
	res, _ := f.svc.Inbox(context.Background(), 150002)
	if len(res.Mails) != 0 {
		t.Fatalf("delivered %d mails for a refused send", len(res.Mails))
	}
}

func TestTitleAndBodyClamped(t *testing.T) {
	f := newFixture(0)
	longTitle := strings.Repeat("x", domain.TitleMax+20)
	longBody := strings.Repeat("x", domain.BodyMax+20)
	dest, err := f.svc.Send(context.Background(), 150001, "Hero", "Partner", longTitle, longBody, 0, nil)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	res, _ := f.svc.Inbox(context.Background(), dest)
	if len(res.Mails[0].Title) != domain.TitleMax || len(res.Mails[0].Body) != domain.BodyMax {
		t.Fatalf("clamp failed: title %d body %d", len(res.Mails[0].Title), len(res.Mails[0].Body))
	}
	// Bytes beyond the cap are dropped, not left as raw control bytes.
}

func TestCheckReceiver(t *testing.T) {
	f := newFixture(0)
	rec, err := f.svc.CheckReceiver(context.Background(), "Partner")
	if err != nil || rec.CharID != 150002 || rec.Class != 6 || rec.BaseLevel != 99 {
		t.Fatalf("rec = %+v err %v", rec, err)
	}
	if _, err := f.svc.CheckReceiver(context.Background(), "Nobody"); !errors.Is(err, domain.ErrRecipientNotFound) {
		t.Fatalf("err = %v", err)
	}
}
