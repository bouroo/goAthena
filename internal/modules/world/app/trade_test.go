//go:build unit

package app_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"

	"github.com/bouroo/goAthena/internal/modules/inventory/domain"
	worldapp "github.com/bouroo/goAthena/internal/modules/world/app"
	worlddomain "github.com/bouroo/goAthena/internal/modules/world/domain"
)

// fakeInv is an in-memory TradeInventoryPort. Each Add appends a fresh row so a
// rolled-back swap restores items by nameID+amount (the row id changes, which the
// trade state machine never assumes for restore).
type fakeInv struct {
	mu              sync.Mutex
	next            domain.ItemID
	items           map[uint32][]domain.Item
	removeFailAfter int // fail the (N+1)th remove; 0 = never fail
	removeCount     int
}

func newFakeInv() *fakeInv {
	return &fakeInv{items: make(map[uint32][]domain.Item)}
}

func (f *fakeInv) seed(charID, accountID uint32, nameID domain.ItemID, amount uint32) domain.Item {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	it := domain.Item{ID: f.next, CharID: charID, NameID: uint32(nameID), Amount: amount}
	f.items[charID] = append(f.items[charID], it)
	return it
}

func (f *fakeInv) LoadByChar(_ context.Context, _, charID uint32) ([]domain.Item, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.Item, len(f.items[charID]))
	copy(out, f.items[charID])
	return out, nil
}

func (f *fakeInv) Add(_ context.Context, charID, nameID uint32, amount int) (domain.Item, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	it := domain.Item{ID: f.next, CharID: charID, NameID: nameID, Amount: uint32(amount)} //nolint:gosec // G115: test value.
	f.items[charID] = append(f.items[charID], it)
	return it, nil
}

func (f *fakeInv) Remove(_ context.Context, id domain.ItemID, amount int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removeCount++
	if f.removeFailAfter > 0 && f.removeCount > f.removeFailAfter {
		return errors.New("remove failed (raced)")
	}
	for c, list := range f.items {
		for i, it := range list {
			if it.ID != id {
				continue
			}
			if it.Amount < uint32(amount) { //nolint:gosec // G115: test value.
				return domain.ErrInsufficientAmount
			}
			it.Amount -= uint32(amount) //nolint:gosec // G115: test value.
			if it.Amount == 0 {
				f.items[c] = append(list[:i], list[i+1:]...)
			} else {
				f.items[c][i] = it
			}
			return nil
		}
	}
	return domain.ErrItemNotFound
}

func (f *fakeInv) amountOf(charID, nameID uint32) uint32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	var sum uint32
	for _, it := range f.items[charID] {
		if it.NameID == nameID {
			sum += it.Amount
		}
	}
	return sum
}

// fakeEcon is an in-memory TradeEconPort.
type fakeEcon struct {
	mu            sync.Mutex
	zeny          map[uint32]int32
	failCreditFor uint32 // credit to this char fails (0 = none)
	deductFailFor uint32 // deduct from this char fails after verify (0 = none)
}

func newFakeEcon() *fakeEcon { return &fakeEcon{zeny: make(map[uint32]int32)} }

func (f *fakeEcon) GetZeny(_ context.Context, charID uint32) (int32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.zeny[charID], nil
}

func (f *fakeEcon) DeductZeny(_ context.Context, charID uint32, amount int32) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deductFailFor != 0 && charID == f.deductFailFor {
		return errors.New("deduct failed")
	}
	if f.zeny[charID] < amount {
		return errors.New("insufficient zeny")
	}
	f.zeny[charID] -= amount
	return nil
}

func (f *fakeEcon) CreditZeny(_ context.Context, charID uint32, amount int32) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failCreditFor != 0 && charID == f.failCreditFor {
		return errors.New("credit failed")
	}
	f.zeny[charID] += amount
	return nil
}

func (f *fakeEcon) balance(charID uint32) int32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.zeny[charID]
}

const (
	aidA, gidA = uint32(101), uint32(1)
	aidB, gidB = uint32(102), uint32(2)
	aidC, gidC = uint32(103), uint32(3)
)

// newTradeEnv builds a TradeService over a real WorldService (three PCs on
// "prontera") and fresh fake ports. A starts with item 501x5 + 1000 zeny; B with
// 502x3 + 500 zeny; C empty + 0 zeny.
func newTradeEnv() (*worldapp.TradeService, *fakeInv, *fakeEcon) {
	w := worldapp.NewWorldService(nil, slog.Default(), 50)
	for _, e := range []worlddomain.Entity{
		{ID: worlddomain.EntityID(gidA), Account: aidA, Type: worlddomain.EntityTypePC, Map: "prontera"},
		{ID: worlddomain.EntityID(gidB), Account: aidB, Type: worlddomain.EntityTypePC, Map: "prontera"},
		{ID: worlddomain.EntityID(gidC), Account: aidC, Type: worlddomain.EntityTypePC, Map: "prontera"},
	} {
		_ = w.AddEntity(e)
	}
	inv := newFakeInv()
	inv.seed(gidA, aidA, 501, 5)
	inv.seed(gidB, aidB, 502, 3)
	econ := newFakeEcon()
	econ.zeny[gidA] = 1000
	econ.zeny[gidB] = 500
	return worldapp.NewTradeService(w, inv, econ), inv, econ
}

func wantErrIs(t *testing.T, err error, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("err = %v, want %v", err, target)
	}
}

func wantNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
}

func TestTradeRequestHandshake(t *testing.T) {
	svc, _, _ := newTradeEnv()
	ctx := context.Background()

	t.Run("self request rejected", func(t *testing.T) {
		wantErrIs(t, svc.Request(ctx, gidA, gidA), worldapp.ErrTradeSelf)
	})
	t.Run("different map rejected", func(t *testing.T) {
		// Re-enter C on a different map by removing+re-adding is heavy; instead
		// reuse the offline path by requesting a non-PC: no such entity here, so
		// verify the map check via a second world with a split map.
		w := worldapp.NewWorldService(nil, slog.Default(), 50)
		_ = w.AddEntity(worlddomain.Entity{ID: worlddomain.EntityID(gidA), Account: aidA, Type: worlddomain.EntityTypePC, Map: "prontera"})
		_ = w.AddEntity(worlddomain.Entity{ID: worlddomain.EntityID(gidB), Account: aidB, Type: worlddomain.EntityTypePC, Map: "izlude"})
		svc2 := worldapp.NewTradeService(w, newFakeInv(), newFakeEcon())
		wantErrIs(t, svc2.Request(ctx, gidA, gidB), worldapp.ErrTradeDifferentMap)
	})
	t.Run("target offline rejected", func(t *testing.T) {
		// gid 999 is not in the world.
		wantErrIs(t, svc.Request(ctx, gidA, 999), worldapp.ErrTradeTargetOffline)
	})
	t.Run("happy request then double rejected", func(t *testing.T) {
		wantNoErr(t, svc.Request(ctx, gidA, gidB))
		// Either party already partnered.
		wantErrIs(t, svc.Request(ctx, gidB, gidA), worldapp.ErrTradeAlreadyTrading)
		wantErrIs(t, svc.Request(ctx, gidC, gidB), worldapp.ErrTradeAlreadyTrading)
	})
}

func TestTradeRejectCancels(t *testing.T) {
	svc, _, _ := newTradeEnv()
	ctx := context.Background()
	wantNoErr(t, svc.Request(ctx, gidA, gidB))
	wantNoErr(t, svc.Ack(ctx, gidB, false))
	// Both sessions gone: a fresh request succeeds again.
	wantNoErr(t, svc.Request(ctx, gidA, gidB))
	// Staging now (pending, not active) fails.
	_, err := svc.AddItem(ctx, gidA, 1, 1)
	wantErrIs(t, err, worldapp.ErrTradeNotActive)
}

func TestTradeCancelCancels(t *testing.T) {
	svc, _, _ := newTradeEnv()
	ctx := context.Background()
	wantNoErr(t, svc.Request(ctx, gidA, gidB))
	wantNoErr(t, svc.Ack(ctx, gidB, true))
	svc.Cancel(ctx, gidA)
	// Both sessions gone: staging fails and a fresh request works.
	_, err := svc.AddItem(ctx, gidA, 1, 1)
	wantErrIs(t, err, worldapp.ErrTradeNotActive)
	wantNoErr(t, svc.Request(ctx, gidA, gidB))
}

func TestTradeAddItemValidation(t *testing.T) {
	svc, _, _ := newTradeEnv()
	ctx := context.Background()
	wantNoErr(t, svc.Request(ctx, gidA, gidB))
	wantNoErr(t, svc.Ack(ctx, gidB, true))

	t.Run("not active before handshake", func(t *testing.T) {
		s2, _, _ := newTradeEnv()
		_, err := s2.AddItem(ctx, gidA, 1, 1)
		wantErrIs(t, err, worldapp.ErrTradeNotActive)
	})
	t.Run("out of range", func(t *testing.T) {
		_, err := svc.AddItem(ctx, gidA, 99, 1)
		wantErrIs(t, err, worldapp.ErrTradeItemOutOfRange)
	})
	t.Run("insufficient stack", func(t *testing.T) {
		_, err := svc.AddItem(ctx, gidA, 1, 999)
		wantErrIs(t, err, worldapp.ErrTradeItemInsufficient)
	})
	t.Run("equipped rejected", func(t *testing.T) {
		w := worldapp.NewWorldService(nil, slog.Default(), 50)
		_ = w.AddEntity(worlddomain.Entity{ID: worlddomain.EntityID(gidA), Account: aidA, Type: worlddomain.EntityTypePC, Map: "prontera"})
		_ = w.AddEntity(worlddomain.Entity{ID: worlddomain.EntityID(gidB), Account: aidB, Type: worlddomain.EntityTypePC, Map: "prontera"})
		inv := newFakeInv()
		inv.items[gidA] = []domain.Item{{ID: 1, CharID: gidA, NameID: 501, Amount: 1, Equip: 0x10}}
		s2 := worldapp.NewTradeService(w, inv, newFakeEcon())
		wantNoErr(t, s2.Request(ctx, gidA, gidB))
		wantNoErr(t, s2.Ack(ctx, gidB, true))
		_, err := s2.AddItem(ctx, gidA, 1, 1)
		wantErrIs(t, err, worldapp.ErrTradeItemEquipped)
	})
	t.Run("insufficient zeny", func(t *testing.T) {
		_, err := svc.AddItem(ctx, gidA, 0, 99999)
		wantErrIs(t, err, worldapp.ErrTradeItemInsufficient)
	})
	t.Run("ok item staging does not move inventory", func(t *testing.T) {
		s3, inv, _ := newTradeEnv()
		wantNoErr(t, s3.Request(ctx, gidA, gidB))
		wantNoErr(t, s3.Ack(ctx, gidB, true))
		res, err := s3.AddItem(ctx, gidA, 1, 2)
		wantNoErr(t, err)
		if res.Index != 1 || res.Item.NameID != 501 {
			t.Fatalf("AddItemResult = %+v, want Index=1 NameID=501", res)
		}
		// Nothing removed yet: full stack still present.
		if got := inv.amountOf(gidA, 501); got != 5 {
			t.Fatalf("staged item already removed: amount=%d want 5", got)
		}
	})
}

func TestTradeOKWaitsForPartner(t *testing.T) {
	svc, _, _ := newTradeEnv()
	ctx := context.Background()
	wantNoErr(t, svc.Request(ctx, gidA, gidB))
	wantNoErr(t, svc.Ack(ctx, gidB, true))
	_, err := svc.AddItem(ctx, gidA, 1, 2)
	wantNoErr(t, err)

	concluded, err := svc.OK(ctx, gidA)
	wantNoErr(t, err)
	if concluded {
		t.Fatalf("concluded = true, want false (partner not locked)")
	}
	// Once locked, the side can no longer change its offer.
	_, err = svc.AddItem(ctx, gidA, 1, 1)
	wantErrIs(t, err, worldapp.ErrTradeLocked)
}

func TestTradeConcludeSwapsItemsAndZeny(t *testing.T) {
	svc, inv, econ := newTradeEnv()
	ctx := context.Background()
	wantNoErr(t, svc.Request(ctx, gidA, gidB))
	wantNoErr(t, svc.Ack(ctx, gidB, true))

	// A stages 2 of item 501 + 200 zeny; B stages 1 of item 502 + 100 zeny.
	_, err := svc.AddItem(ctx, gidA, 1, 2)
	wantNoErr(t, err)
	_, err = svc.AddItem(ctx, gidA, 0, 200)
	wantNoErr(t, err)
	_, err = svc.AddItem(ctx, gidB, 1, 1)
	wantNoErr(t, err)
	_, err = svc.AddItem(ctx, gidB, 0, 100)
	wantNoErr(t, err)

	concluded, err := svc.OK(ctx, gidA)
	wantNoErr(t, err)
	if concluded {
		t.Fatalf("first OK: concluded = true, want false")
	}
	concluded, err = svc.OK(ctx, gidB)
	wantNoErr(t, err)
	if !concluded {
		t.Fatalf("second OK: concluded = false, want true")
	}

	// Items swapped: A keeps 3 of 501 and gains 1 of 502; B keeps 2 of 502 and
	// gains 2 of 501.
	if got := inv.amountOf(gidA, 501); got != 3 {
		t.Errorf("A 501 = %d, want 3", got)
	}
	if got := inv.amountOf(gidA, 502); got != 1 {
		t.Errorf("A 502 = %d, want 1", got)
	}
	if got := inv.amountOf(gidB, 502); got != 2 {
		t.Errorf("B 502 = %d, want 2", got)
	}
	if got := inv.amountOf(gidB, 501); got != 2 {
		t.Errorf("B 501 = %d, want 2", got)
	}
	// Zeny swapped: A 1000-200+100=900; B 500-100+200=600.
	if got := econ.balance(gidA); got != 900 {
		t.Errorf("A zeny = %d, want 900", got)
	}
	if got := econ.balance(gidB); got != 600 {
		t.Errorf("B zeny = %d, want 600", got)
	}
	// Sessions torn down.
	_, err = svc.AddItem(ctx, gidA, 1, 1)
	wantErrIs(t, err, worldapp.ErrTradeNotActive)
}

func TestTradeConcludeRollbackOnRemoveFailure(t *testing.T) {
	svc, inv, econ := newTradeEnv()
	ctx := context.Background()
	// Simulate the documented TOCTOU: an item present at verify is gone by the
	// remove (race with a drop). The second remove (B's) fails, so the only
	// prior mutation (A's remove) must be undone — A restored, B untouched, no
	// crossover, no zeny moved.
	inv.removeFailAfter = 1

	wantNoErr(t, svc.Request(ctx, gidA, gidB))
	wantNoErr(t, svc.Ack(ctx, gidB, true))
	_, err := svc.AddItem(ctx, gidA, 1, 2)
	wantNoErr(t, err)
	_, err = svc.AddItem(ctx, gidB, 1, 1)
	wantNoErr(t, err)

	concluded, err := svc.OK(ctx, gidA)
	wantNoErr(t, err)
	if concluded {
		t.Fatalf("first OK: concluded = true, want false")
	}
	concluded, err = svc.OK(ctx, gidB)
	if concluded {
		t.Fatalf("failed conclude: concluded = true, want false")
	}
	wantErrIs(t, err, worldapp.ErrTradeConcludeFailed)

	// A's staged remove was undone; B's remove never applied.
	if got := inv.amountOf(gidA, 501); got != 5 {
		t.Errorf("rollback A 501 = %d, want 5 (restored)", got)
	}
	if got := inv.amountOf(gidA, 502); got != 0 {
		t.Errorf("rollback A 502 = %d, want 0", got)
	}
	if got := inv.amountOf(gidB, 502); got != 3 {
		t.Errorf("rollback B 502 = %d, want 3 (untouched)", got)
	}
	if got := inv.amountOf(gidB, 501); got != 0 {
		t.Errorf("rollback B 501 = %d, want 0", got)
	}
	// No zeny moved (zeny swap is after the item phase that failed).
	if got := econ.balance(gidA); got != 1000 {
		t.Errorf("rollback A zeny = %d, want 1000", got)
	}
	if got := econ.balance(gidB); got != 500 {
		t.Errorf("rollback B zeny = %d, want 500", got)
	}
	// Sessions torn down despite failure.
	_, err = svc.AddItem(ctx, gidA, 1, 1)
	wantErrIs(t, err, worldapp.ErrTradeNotActive)
}

func TestTradeConcludeRollbackOnZenyFailure(t *testing.T) {
	svc, inv, econ := newTradeEnv()
	ctx := context.Background()
	// Only A stages zeny (200); B stages an item only. A's zeny deduct fails at
	// swap (after verify passed on the cached balance), so the items that already
	// crossed must be undone and no zeny may move. Single-sided zeny keeps the
	// failing op the only zeny op, so no zeny undo can itself fail.
	econ.deductFailFor = gidA

	wantNoErr(t, svc.Request(ctx, gidA, gidB))
	wantNoErr(t, svc.Ack(ctx, gidB, true))
	_, err := svc.AddItem(ctx, gidA, 1, 2)
	wantNoErr(t, err)
	_, err = svc.AddItem(ctx, gidA, 0, 200)
	wantNoErr(t, err)
	_, err = svc.AddItem(ctx, gidB, 1, 1)
	wantNoErr(t, err)

	concluded, err := svc.OK(ctx, gidA)
	wantNoErr(t, err)
	if concluded {
		t.Fatalf("first OK: concluded = true, want false")
	}
	concluded, err = svc.OK(ctx, gidB)
	if concluded {
		t.Fatalf("failed conclude: concluded = true, want false")
	}
	wantErrIs(t, err, worldapp.ErrTradeConcludeFailed)

	// Items restored to original owners; nothing crossed.
	if got := inv.amountOf(gidA, 501); got != 5 {
		t.Errorf("rollback A 501 = %d, want 5", got)
	}
	if got := inv.amountOf(gidA, 502); got != 0 {
		t.Errorf("rollback A 502 = %d, want 0", got)
	}
	if got := inv.amountOf(gidB, 502); got != 3 {
		t.Errorf("rollback B 502 = %d, want 3", got)
	}
	if got := inv.amountOf(gidB, 501); got != 0 {
		t.Errorf("rollback B 501 = %d, want 0", got)
	}
	// The deduct failed before any zeny moved.
	if got := econ.balance(gidA); got != 1000 {
		t.Errorf("rollback A zeny = %d, want 1000", got)
	}
	if got := econ.balance(gidB); got != 500 {
		t.Errorf("rollback B zeny = %d, want 500", got)
	}
}

func TestTradeConcludeVerifyAbortsClean(t *testing.T) {
	svc, inv, econ := newTradeEnv()
	ctx := context.Background()
	wantNoErr(t, svc.Request(ctx, gidA, gidB))
	wantNoErr(t, svc.Ack(ctx, gidB, true))
	_, err := svc.AddItem(ctx, gidA, 1, 2)
	wantNoErr(t, err)
	_, err = svc.AddItem(ctx, gidB, 1, 1)
	wantNoErr(t, err)

	// Race: A drops the staged item out from under the trade between staging and
	// conclude. The verify phase must abort with no mutation.
	inv.mu.Lock()
	inv.items[gidA] = nil
	inv.mu.Unlock()

	concluded, err := svc.OK(ctx, gidA)
	wantNoErr(t, err)
	if concluded {
		t.Fatalf("first OK: concluded = true, want false")
	}
	concluded, err = svc.OK(ctx, gidB)
	if concluded {
		t.Fatalf("verify abort: concluded = true, want false")
	}
	wantErrIs(t, err, worldapp.ErrTradeConcludeFailed)
	// B's staged item untouched by the aborted verify.
	if got := inv.amountOf(gidB, 502); got != 3 {
		t.Errorf("verify abort B 502 = %d, want 3", got)
	}
	if got := econ.balance(gidB); got != 500 {
		t.Errorf("verify abort B zeny = %d, want 500", got)
	}
}

func TestTradeAckWithoutRequest(t *testing.T) {
	svc, _, _ := newTradeEnv()
	ctx := context.Background()
	wantErrIs(t, svc.Ack(ctx, gidA, true), worldapp.ErrTradeNotActive)
}

func TestTradeOKWithoutSession(t *testing.T) {
	svc, _, _ := newTradeEnv()
	ctx := context.Background()
	concluded, err := svc.OK(ctx, gidA)
	if concluded {
		t.Fatalf("concluded = true, want false")
	}
	wantErrIs(t, err, worldapp.ErrTradeNotActive)
}

func TestTradeZenyRestageReplaces(t *testing.T) {
	svc, _, econ := newTradeEnv()
	ctx := context.Background()
	wantNoErr(t, svc.Request(ctx, gidA, gidB))
	wantNoErr(t, svc.Ack(ctx, gidB, true))

	res, err := svc.AddItem(ctx, gidA, 0, 200)
	wantNoErr(t, err)
	if res.Zeny != 200 {
		t.Fatalf("first stage zeny = %d, want 200", res.Zeny)
	}
	// Restage a smaller amount; the offer replaces (not accumulates).
	res, err = svc.AddItem(ctx, gidA, 0, 50)
	wantNoErr(t, err)
	if res.Zeny != 50 {
		t.Fatalf("restage zeny = %d, want 50", res.Zeny)
	}
	_, err = svc.AddItem(ctx, gidB, 1, 1)
	wantNoErr(t, err)
	concluded, err := svc.OK(ctx, gidA)
	wantNoErr(t, err)
	if concluded {
		t.Fatalf("first OK should not conclude")
	}
	concluded, err = svc.OK(ctx, gidB)
	wantNoErr(t, err)
	if !concluded {
		t.Fatalf("second OK should conclude")
	}
	// A staged 50 (not 200): A 1000-50, B 500+50.
	if got := econ.balance(gidA); got != 950 {
		t.Errorf("A zeny = %d, want 950", got)
	}
	if got := econ.balance(gidB); got != 550 {
		t.Errorf("B zeny = %d, want 550", got)
	}
}

func TestTradeStageSameRowTwiceExceedsStack(t *testing.T) {
	svc, _, _ := newTradeEnv()
	ctx := context.Background()
	wantNoErr(t, svc.Request(ctx, gidA, gidB))
	wantNoErr(t, svc.Ack(ctx, gidB, true))
	// A has 5 of 501. Stage 3, then 3 again -> second exceeds the 5 stack.
	_, err := svc.AddItem(ctx, gidA, 1, 3)
	wantNoErr(t, err)
	_, err = svc.AddItem(ctx, gidA, 1, 3)
	wantErrIs(t, err, worldapp.ErrTradeItemInsufficient)
	// Staging 2 more (3+2=5) is fine.
	_, err = svc.AddItem(ctx, gidA, 1, 2)
	wantNoErr(t, err)
}
