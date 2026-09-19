//go:build unit

package app_test

import (
	"context"
	"errors"
	"testing"

	chardomain "github.com/bouroo/goAthena/internal/modules/character/domain"
	"github.com/bouroo/goAthena/internal/modules/economy/app"
	economydomain "github.com/bouroo/goAthena/internal/modules/economy/domain"
	economyinfra "github.com/bouroo/goAthena/internal/modules/economy/infra"
)

// fakeCharRepo is an in-memory chardomain.CharacterRepository for economy tests.
type fakeCharRepo struct {
	chars map[chardomain.CharID]chardomain.Character
}

func (f *fakeCharRepo) ListByAccount(_ context.Context, accountID uint32) ([]chardomain.Character, error) {
	var out []chardomain.Character
	for _, c := range f.chars {
		if c.AccountID == accountID {
			out = append(out, c)
		}
	}
	return out, nil
}

func (f *fakeCharRepo) Create(_ context.Context, c chardomain.Character) (chardomain.Character, error) {
	f.chars[c.ID] = c
	return c, nil
}

func (f *fakeCharRepo) Delete(_ context.Context, id chardomain.CharID, _ uint32) error {
	delete(f.chars, id)
	return nil
}

func (f *fakeCharRepo) NameExists(_ context.Context, name string) (bool, error) {
	for _, c := range f.chars {
		if c.Name == name {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeCharRepo) UpdateZeny(_ context.Context, id chardomain.CharID, zeny uint32) error {
	c, ok := f.chars[id]
	if !ok {
		return chardomain.ErrCharacterNotFound
	}
	c.Zeny = zeny
	f.chars[id] = c
	return nil
}

func (f *fakeCharRepo) SetDeleteDate(_ context.Context, id chardomain.CharID, _ uint32, _ uint32) error {
	return nil
}

func (f *fakeCharRepo) FindByID(_ context.Context, id chardomain.CharID) (chardomain.Character, error) {
	c, ok := f.chars[id]
	if !ok {
		return chardomain.Character{}, chardomain.ErrCharacterNotFound
	}
	return c, nil
}

func TestEconomyGetZeny(t *testing.T) {
	repo := &fakeCharRepo{chars: map[chardomain.CharID]chardomain.Character{
		1: {ID: 1, AccountID: 100, Zeny: 123456},
	}}
	// GetZeny doesn't touch the ledger; nil is fine.
	svc := app.NewEconomyService(repo, nil)

	got, err := svc.GetZeny(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetZeny: %v", err)
	}
	if got != 123456 {
		t.Errorf("GetZeny = %d, want 123456", got)
	}

	if _, err := svc.GetZeny(context.Background(), 2); !errors.Is(err, chardomain.ErrCharacterNotFound) {
		t.Errorf("missing char err = %v, want ErrCharacterNotFound", err)
	}
}

func TestEconomyDeductCreditGetZenyRoundTrip(t *testing.T) {
	repo := &fakeCharRepo{chars: map[chardomain.CharID]chardomain.Character{
		7: {ID: 7, AccountID: 7, Zeny: 1000},
	}}
	ledger := economyinfra.NewMemoryLedger()
	svc := app.NewEconomyService(repo, ledger)
	ctx := context.Background()

	if err := svc.DeductZeny(ctx, 7, 250); err != nil {
		t.Fatalf("deduct: %v", err)
	}
	if got, _ := svc.GetZeny(ctx, 7); got != 750 {
		t.Errorf("after deduct = %d, want 750", got)
	}
	if err := svc.CreditZeny(ctx, 7, 50); err != nil {
		t.Fatalf("credit: %v", err)
	}
	if got, _ := svc.GetZeny(ctx, 7); got != 800 {
		t.Errorf("after credit = %d, want 800", got)
	}
	// Overdraft is rejected and leaves the balance intact.
	if err := svc.DeductZeny(ctx, 7, 999999); err == nil {
		t.Errorf("overdraft deduct: want error, got nil")
	}
	if got, _ := svc.GetZeny(ctx, 7); got != 800 {
		t.Errorf("after rejected overdraft = %d, want 800", got)
	}
}

// TestEconomyLedgerAppendOnEveryMovement is the audit guarantee: every
// successful movement produces one ledger row carrying the right delta and
// reason. The list reflects both legs of the round-trip above (one deduct, one
// credit) and the overdraft rejection produces no row.
func TestEconomyLedgerAppendOnEveryMovement(t *testing.T) {
	repo := &fakeCharRepo{chars: map[chardomain.CharID]chardomain.Character{
		42: {ID: 42, AccountID: 42, Zeny: 1000},
	}}
	ledger := economyinfra.NewMemoryLedger()
	svc := app.NewEconomyService(repo, ledger)
	ctx := context.Background()

	if err := svc.DeductZenyFor(ctx, 42, 100, app.LedgerEntry{Reason: economydomain.ReasonShopBuy, MapName: "prontera"}); err != nil {
		t.Fatalf("deduct: %v", err)
	}
	if err := svc.CreditZenyFor(ctx, 42, 25, app.LedgerEntry{Reason: economydomain.ReasonMobKill, MapName: "prontera"}); err != nil {
		t.Fatalf("credit: %v", err)
	}
	// Overdraft rejected → no audit row.
	if err := svc.DeductZenyFor(ctx, 42, 99999, app.LedgerEntry{Reason: economydomain.ReasonShopBuy}); err == nil {
		t.Fatal("overdraft: want error, got nil")
	}

	txs, err := svc.RecentTransactions(ctx, 42, 100)
	if err != nil {
		t.Fatalf("RecentTransactions: %v", err)
	}
	if len(txs) != 2 {
		t.Fatalf("ledger len = %d, want 2 (one deduct, one credit; overdraft appended nothing)", len(txs))
	}

	// ListByChar is newest-first: index 0 is the credit, index 1 is the deduct.
	want := []struct {
		amount int32
		reason economydomain.Reason
		mapn   string
	}{
		{25, economydomain.ReasonMobKill, "prontera"},
		{-100, economydomain.ReasonShopBuy, "prontera"},
	}
	for i, w := range want {
		got := txs[i]
		if got.Amount != w.amount {
			t.Errorf("tx[%d].Amount = %d, want %d", i, got.Amount, w.amount)
		}
		if got.Reason != w.reason {
			t.Errorf("tx[%d].Reason = %q, want %q", i, got.Reason, w.reason)
		}
		if got.MapName != w.mapn {
			t.Errorf("tx[%d].MapName = %q, want %q", i, got.MapName, w.mapn)
		}
	}

	// SumByChar matches the new balance (the audit trail is the source of
	// truth modulo the initial endowment — here we know the starting value).
	sum, err := ledger.SumByChar(ctx, 42)
	if err != nil {
		t.Fatalf("SumByChar: %v", err)
	}
	if sum != -75 {
		t.Errorf("SumByChar = %d, want -75", sum)
	}
	balance, _ := svc.GetZeny(ctx, 42)
	// Invariant: balance == initial + sum(deltas). Equivalently, balance - sum
	// equals the initial endowment (here 1000).
	if int64(balance)-sum != 1000 {
		t.Errorf("balance %d - sum %d = %d, want initial 1000", balance, sum, int64(balance)-sum)
	}
}

// TestEconomyLedgerRejectsMovement proves the precondition: a missing or
// failing ledger aborts the call before the balance moves.
func TestEconomyLedgerRejectsMovement(t *testing.T) {
	repo := &fakeCharRepo{chars: map[chardomain.CharID]chardomain.Character{
		9: {ID: 9, AccountID: 9, Zeny: 500},
	}}
	svc := app.NewEconomyService(repo, nil) // no ledger
	ctx := context.Background()

	if err := svc.DeductZeny(ctx, 9, 100); !errors.Is(err, app.ErrLedgerAppendFailed) {
		t.Errorf("DeductZeny without ledger = %v, want ErrLedgerAppendFailed", err)
	}
	if got, _ := svc.GetZeny(ctx, 9); got != 500 {
		t.Errorf("balance after rejected deduct = %d, want 500", got)
	}
}

// TestEconomyPeerCharRecorded is the trade/vending leg proof: the peer char
// surfaces on the ledger row so the audit can answer "who paid whom".
func TestEconomyPeerCharRecorded(t *testing.T) {
	repo := &fakeCharRepo{chars: map[chardomain.CharID]chardomain.Character{
		1: {ID: 1, AccountID: 1, Zeny: 1000},
	}}
	ledger := economyinfra.NewMemoryLedger()
	svc := app.NewEconomyService(repo, ledger)
	ctx := context.Background()

	if err := svc.CreditZenyFor(ctx, 1, 500, app.LedgerEntry{
		Reason:     economydomain.ReasonTrade,
		PeerCharID: 2,
		MapName:    "prontera",
	}); err != nil {
		t.Fatalf("credit: %v", err)
	}
	txs, _ := svc.RecentTransactions(ctx, 1, 10)
	if len(txs) != 1 || txs[0].PeerCharID != 2 {
		t.Fatalf("ledger = %+v, want one row with PeerCharID=2", txs)
	}
	if txs[0].Reason != economydomain.ReasonTrade {
		t.Errorf("reason = %q, want trade", txs[0].Reason)
	}
}
