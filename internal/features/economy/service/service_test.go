//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/bouroo/goAthena/internal/features/economy/domain"
	domainmock "github.com/bouroo/goAthena/internal/features/economy/domain/mock"
	inventorydomain "github.com/bouroo/goAthena/internal/features/inventory/domain"
)

// newSvc builds a shopService wired to fresh mocks and returns the mocks so
// each subtest configures exactly the calls it expects.
func newSvc(t *testing.T) (domain.ShopService, *domainmock.MockCharacterZenyRepository, *domainmock.MockLockStore) {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := domainmock.NewMockCharacterZenyRepository(ctrl)
	locks := domainmock.NewMockLockStore(ctrl)
	svc := NewShopService(repo, locks, 0)
	return svc, repo, locks
}

// stubLock stubs a successful lock acquisition and a best-effort release.
func stubLock(t *testing.T, locks *domainmock.MockLockStore, charID uint32) {
	t.Helper()
	locks.EXPECT().Acquire(gomock.Any(), domain.CharLockKey(charID), gomock.Any()).
		Return("tok", nil)
	locks.EXPECT().Release(gomock.Any(), domain.CharLockKey(charID), "tok").AnyTimes()
}

func TestBuyFromShop_Happy(t *testing.T) {
	svc, repo, locks := newSvc(t)
	const charID uint32 = 7
	stubLock(t, locks, charID)
	repo.EXPECT().ExecuteBuyTx(gomock.Any(), charID, uint32(150),
		[]domain.AcquiredItem{{ItemID: 11, Amount: 3}}).
		Return(uint32(900), nil)

	zeny, res, err := svc.BuyFromShop(context.Background(), charID,
		[]domain.ShopOrder{{ItemID: 11, Amount: 3, UnitPrice: 50}})
	require.NoError(t, err)
	assert.Equal(t, uint32(900), zeny)
	assert.Equal(t, domain.BuyOK, res)
}

func TestBuyFromShop_InsufficientZeny(t *testing.T) {
	svc, repo, locks := newSvc(t)
	const charID uint32 = 7
	stubLock(t, locks, charID)
	repo.EXPECT().ExecuteBuyTx(gomock.Any(), charID, uint32(50),
		[]domain.AcquiredItem{{ItemID: 11, Amount: 1}}).
		Return(uint32(0), fmt.Errorf("%w: need 50", domain.ErrInsufficientZeny))

	zeny, res, err := svc.BuyFromShop(context.Background(), charID,
		[]domain.ShopOrder{{ItemID: 11, Amount: 1, UnitPrice: 50}})
	require.NoError(t, err)
	assert.Equal(t, uint32(0), zeny)
	assert.Equal(t, domain.BuyFailInsufficientZeny, res)
}

func TestBuyFromShop_LockBusy(t *testing.T) {
	svc, _, locks := newSvc(t)
	const charID uint32 = 7
	// First (and only) Acquire fails busy; ExecuteBuyTx must never run.
	locks.EXPECT().Acquire(gomock.Any(), domain.CharLockKey(charID), gomock.Any()).
		Return("", domain.ErrLockBusy)

	zeny, res, err := svc.BuyFromShop(context.Background(), charID,
		[]domain.ShopOrder{{ItemID: 11, Amount: 1, UnitPrice: 50}})
	require.NoError(t, err)
	assert.Equal(t, uint32(0), zeny)
	assert.Equal(t, domain.BuyFailLockBusy, res)
}

func TestBuyFromShop_AcquireError(t *testing.T) {
	svc, _, locks := newSvc(t)
	const charID uint32 = 7
	locks.EXPECT().Acquire(gomock.Any(), domain.CharLockKey(charID), gomock.Any()).
		Return("", errors.New("valkey down"))

	_, _, err := svc.BuyFromShop(context.Background(), charID,
		[]domain.ShopOrder{{ItemID: 11, Amount: 1, UnitPrice: 50}})
	require.Error(t, err)
}

func TestBuyFromShop_EmptyOrders(t *testing.T) {
	svc, repo, locks := newSvc(t)
	const charID uint32 = 7
	stubLock(t, locks, charID)
	// No ExecuteBuyTx: zero-amount orders collapse to a no-op read.
	repo.EXPECT().GetZeny(gomock.Any(), charID).Return(uint32(500), nil)

	zeny, res, err := svc.BuyFromShop(context.Background(), charID,
		[]domain.ShopOrder{{ItemID: 11, Amount: 0, UnitPrice: 50}})
	require.NoError(t, err)
	assert.Equal(t, uint32(500), zeny)
	assert.Equal(t, domain.BuyOK, res)
}

func TestBuyFromShop_RepoError(t *testing.T) {
	svc, repo, locks := newSvc(t)
	const charID uint32 = 7
	stubLock(t, locks, charID)
	repo.EXPECT().ExecuteBuyTx(gomock.Any(), charID, uint32(50),
		[]domain.AcquiredItem{{ItemID: 11, Amount: 1}}).
		Return(uint32(0), errors.New("db connection lost"))

	_, _, err := svc.BuyFromShop(context.Background(), charID,
		[]domain.ShopOrder{{ItemID: 11, Amount: 1, UnitPrice: 50}})
	require.Error(t, err)
}

func TestSellToShop_Happy(t *testing.T) {
	svc, repo, locks := newSvc(t)
	const charID uint32 = 9
	stubLock(t, locks, charID)
	repo.EXPECT().ExecuteSellTx(gomock.Any(), charID, uint32(120),
		[]domain.SellLine{{InvID: 31, Amount: 2, UnitPrice: 60}}).
		Return(uint32(620), nil)

	zeny, res, err := svc.SellToShop(context.Background(), charID,
		[]domain.SellLine{{InvID: 31, Amount: 2, UnitPrice: 60}})
	require.NoError(t, err)
	assert.Equal(t, uint32(620), zeny)
	assert.Equal(t, domain.SellOK, res)
}

func TestSellToShop_ZenyOverflow(t *testing.T) {
	svc, repo, locks := newSvc(t)
	const charID uint32 = 9
	stubLock(t, locks, charID)
	repo.EXPECT().ExecuteSellTx(gomock.Any(), charID, uint32(60),
		[]domain.SellLine{{InvID: 31, Amount: 1, UnitPrice: 60}}).
		Return(uint32(0), fmt.Errorf("%w: at cap", domain.ErrZenyOverflow))

	zeny, res, err := svc.SellToShop(context.Background(), charID,
		[]domain.SellLine{{InvID: 31, Amount: 1, UnitPrice: 60}})
	require.NoError(t, err)
	assert.Equal(t, uint32(0), zeny)
	assert.Equal(t, domain.SellFailZenyFull, res)
}

func TestSellToShop_InvalidItem(t *testing.T) {
	svc, repo, locks := newSvc(t)
	const charID uint32 = 9
	stubLock(t, locks, charID)
	// ExecuteSellTx returns the inventory sentinel for an unknown/short row.
	repo.EXPECT().ExecuteSellTx(gomock.Any(), charID, uint32(60),
		[]domain.SellLine{{InvID: 5, Amount: 1, UnitPrice: 60}}).
		Return(uint32(0), fmt.Errorf("%w: invID=5", inventorydomain.ErrItemNotFound))

	zeny, res, err := svc.SellToShop(context.Background(), charID,
		[]domain.SellLine{{InvID: 5, Amount: 1, UnitPrice: 60}})
	require.NoError(t, err)
	assert.Equal(t, uint32(0), zeny)
	assert.Equal(t, domain.SellFailInvalidItem, res)
}

// TestSellToShop_OverSell_ReturnsInvalidItem is the service-layer regression
// for the infinite-zeny exploit: a sale line whose Amount exceeds the
// locked inventory row. The repository guard surfaces this as
// inventorydomain.ErrItemNotFound, which the service maps to
// SellFailInvalidItem with a nil error (a clean business outcome, not an
// internal failure).
func TestSellToShop_OverSell_ReturnsInvalidItem(t *testing.T) {
	svc, repo, locks := newSvc(t)
	const charID uint32 = 9
	stubLock(t, locks, charID)
	// Locked inv row has 1 item; player is selling 5.
	repo.EXPECT().ExecuteSellTx(gomock.Any(), charID, uint32(300),
		[]domain.SellLine{{InvID: 42, Amount: 5, UnitPrice: 60}}).
		Return(uint32(0), fmt.Errorf("%w: invID=42 has=1 want=5", inventorydomain.ErrItemNotFound))

	zeny, res, err := svc.SellToShop(context.Background(), charID,
		[]domain.SellLine{{InvID: 42, Amount: 5, UnitPrice: 60}})
	require.NoError(t, err)
	assert.Equal(t, uint32(0), zeny)
	assert.Equal(t, domain.SellFailInvalidItem, res)
}

func TestSellToShop_EmptySales(t *testing.T) {
	svc, repo, locks := newSvc(t)
	const charID uint32 = 9
	stubLock(t, locks, charID)
	repo.EXPECT().GetZeny(gomock.Any(), charID).Return(uint32(800), nil)

	zeny, res, err := svc.SellToShop(context.Background(), charID,
		[]domain.SellLine{{InvID: 31, Amount: 0, UnitPrice: 60}})
	require.NoError(t, err)
	assert.Equal(t, uint32(800), zeny)
	assert.Equal(t, domain.SellOK, res)
}

func TestSellToShop_LockBusy(t *testing.T) {
	svc, _, locks := newSvc(t)
	const charID uint32 = 9
	locks.EXPECT().Acquire(gomock.Any(), domain.CharLockKey(charID), gomock.Any()).
		Return("", domain.ErrLockBusy)

	zeny, res, err := svc.SellToShop(context.Background(), charID,
		[]domain.SellLine{{InvID: 31, Amount: 1, UnitPrice: 60}})
	require.NoError(t, err)
	assert.Equal(t, uint32(0), zeny)
	assert.Equal(t, domain.SellFailLockBusy, res)
}

func TestSellToShop_AcquireError(t *testing.T) {
	svc, _, locks := newSvc(t)
	const charID uint32 = 9
	locks.EXPECT().Acquire(gomock.Any(), domain.CharLockKey(charID), gomock.Any()).
		Return("", errors.New("valkey down"))

	_, _, err := svc.SellToShop(context.Background(), charID,
		[]domain.SellLine{{InvID: 31, Amount: 1, UnitPrice: 60}})
	require.Error(t, err)
}

func TestSellToShop_RepoError(t *testing.T) {
	svc, repo, locks := newSvc(t)
	const charID uint32 = 9
	stubLock(t, locks, charID)
	repo.EXPECT().ExecuteSellTx(gomock.Any(), charID, uint32(60),
		[]domain.SellLine{{InvID: 31, Amount: 1, UnitPrice: 60}}).
		Return(uint32(0), errors.New("db connection lost"))

	_, _, err := svc.SellToShop(context.Background(), charID,
		[]domain.SellLine{{InvID: 31, Amount: 1, UnitPrice: 60}})
	require.Error(t, err)
}

// TestBuyFromShop_TotalCostOverflow_ReturnsInsufficientZeny is the
// regression for the uint32-truncation dupe: totalCost=5e9 would silently
// wrap to 705M on the uint32 cast and undercharge the player. The guard
// must reject pre-repo as BuyFailInsufficientZeny; ExecuteBuyTx must
// never be called (gomock's strict mode fails on an unexpected call).
func TestBuyFromShop_TotalCostOverflow_ReturnsInsufficientZeny(t *testing.T) {
	svc, _, locks := newSvc(t)
	const charID uint32 = 7
	stubLock(t, locks, charID)

	zeny, res, err := svc.BuyFromShop(context.Background(), charID,
		[]domain.ShopOrder{{ItemID: 11, Amount: 100000, UnitPrice: 50000}})
	require.NoError(t, err)
	assert.Equal(t, uint32(0), zeny)
	assert.Equal(t, domain.BuyFailInsufficientZeny, res)
}

// TestSellToShop_TotalCreditOverflow_ReturnsZenyFull is the regression
// for the uint32-truncation credit wrap. totalCredit=5e9 would silently
// wrap to 705M on the uint32 cast and over-credit the player. The guard
// must reject pre-repo as SellFailZenyFull; ExecuteSellTx must never be
// called (gomock's strict mode fails on an unexpected call).
func TestSellToShop_TotalCreditOverflow_ReturnsZenyFull(t *testing.T) {
	svc, _, locks := newSvc(t)
	const charID uint32 = 9
	stubLock(t, locks, charID)

	zeny, res, err := svc.SellToShop(context.Background(), charID,
		[]domain.SellLine{{InvID: 31, Amount: 100000, UnitPrice: 50000}})
	require.NoError(t, err)
	assert.Equal(t, uint32(0), zeny)
	assert.Equal(t, domain.SellFailZenyFull, res)
}

// TestBuyFromShop_ConcurrentDupeLock proves the per-character economy lock
// serializes concurrent ops: of two simultaneous buys for the same char,
// exactly one proceeds (BuyOK, ExecuteBuyTx once) and the other is rejected
// at the lock (BuyFailLockBusy, ExecuteBuyTx never called).
func TestBuyFromShop_ConcurrentDupeLock(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := domainmock.NewMockCharacterZenyRepository(ctrl)
	locks := domainmock.NewMockLockStore(ctrl)
	svc := NewShopService(repo, locks, 0)

	const charID uint32 = 5
	orders := []domain.ShopOrder{{ItemID: 1, Amount: 1, UnitPrice: 10}}

	var first sync.Once
	var acquired atomic.Bool
	// Deterministic ordering without sleeps: the first caller to reach Acquire
	// wins the lock, the second is told the lock is busy.
	locks.EXPECT().Acquire(gomock.Any(), domain.CharLockKey(charID), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _ any) (string, error) {
			got := false
			first.Do(func() { got = true })
			if got {
				acquired.Store(true)
				return "tok", nil
			}
			return "", domain.ErrLockBusy
		}).Times(2)
	locks.EXPECT().Release(gomock.Any(), domain.CharLockKey(charID), "tok").AnyTimes()
	repo.EXPECT().ExecuteBuyTx(gomock.Any(), charID, uint32(10),
		[]domain.AcquiredItem{{ItemID: 1, Amount: 1}}).
		Return(uint32(990), nil).Times(1)

	var (
		wg                 sync.WaitGroup
		okCount, busyCount int
		okMu               sync.Mutex
	)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, res, err := svc.BuyFromShop(context.Background(), charID, orders)
			assert.NoError(t, err)
			okMu.Lock()
			switch res {
			case domain.BuyOK:
				okCount++
			case domain.BuyFailLockBusy:
				busyCount++
			}
			okMu.Unlock()
		}()
	}
	wg.Wait()

	assert.Equal(t, 1, okCount, "exactly one buy must succeed")
	assert.Equal(t, 1, busyCount, "exactly one buy must be lock-rejected")
	assert.True(t, acquired.Load(), "the lock must have been taken at least once")
}

// TestRelease_DetachesFromCancelledParent is the regression for the lock-
// leak path: the request ctx is already cancelled (client disconnect,
// deadline exceeded) by the time the deferred release runs. The fix uses
// context.WithoutCancel + a short timeout, so Release must still be
// invoked with a context that is NOT cancelled and DOES carry the
// bounded deadline.
//
// We assert the context state inside DoAndReturn because release() defers
// its own cancel() — by the time s.release returns, the timeout wrapper
// is already cancelled and a post-call inspection would see the wrapper's
// own Canceled error, not the parent propagation we care about.
func TestRelease_DetachesFromCancelledParent(t *testing.T) {
	ctrl := gomock.NewController(t)
	locks := domainmock.NewMockLockStore(ctrl)
	s := &shopService{locks: locks, lockTTL: 0}

	const charID uint32 = 42
	locks.EXPECT().Release(gomock.Any(), domain.CharLockKey(charID), "tok").
		DoAndReturn(func(ctx context.Context, _ string, _ string) error {
			// Without the fix this would be ctx.Err() == context.Canceled
			// (parent cancellation propagated straight through) and the
			// real lock store would short-circuit the Release call.
			require.NoError(t, ctx.Err(), "release ctx must outlive parent cancellation")
			deadline, ok := ctx.Deadline()
			require.True(t, ok, "release ctx must carry a deadline")
			require.True(t, time.Until(deadline) > 0, "deadline must be in the future")
			require.LessOrEqual(t, time.Until(deadline), releaseTimeout,
				"deadline must be within the release timeout")
			return nil
		})

	parentCtx, cancel := context.WithCancel(context.Background())
	cancel() // simulate request cancellation before the deferred release

	s.release(parentCtx, charID, "tok")
}

// TestSellToShop_CancelledContext_StillReleases proves the same fix from
// the outside: a request whose ctx is already cancelled when it reaches
// SellToShop must still call Release on the lock mock. The pre-fix code
// passed the cancelled ctx straight through, so any real LockStore
// implementation would return context.Canceled and the lock would leak
// until TTL expiry.
func TestSellToShop_CancelledContext_StillReleases(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := domainmock.NewMockCharacterZenyRepository(ctrl)
	locks := domainmock.NewMockLockStore(ctrl)
	svc := NewShopService(repo, locks, 0)

	const charID uint32 = 9
	// Acquire sees the already-cancelled ctx but must still succeed (the
	// test mock ignores ctx state).
	locks.EXPECT().Acquire(gomock.Any(), domain.CharLockKey(charID), gomock.Any()).
		Return("tok", nil)
	// Release must run — that's the contract under test.
	locks.EXPECT().Release(gomock.Any(), domain.CharLockKey(charID), "tok").
		DoAndReturn(func(ctx context.Context, _ string, _ string) error {
			assert.NoError(t, ctx.Err(), "release ctx must not be cancelled")
			return nil
		})
	// The sell proceeds — we just need a repo call so SellToShop reaches
	// its deferred release.
	repo.EXPECT().ExecuteSellTx(gomock.Any(), charID, uint32(60),
		[]domain.SellLine{{InvID: 31, Amount: 1, UnitPrice: 60}}).
		Return(uint32(160), nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	zeny, res, err := svc.SellToShop(ctx, charID,
		[]domain.SellLine{{InvID: 31, Amount: 1, UnitPrice: 60}})
	require.NoError(t, err)
	assert.Equal(t, uint32(160), zeny)
	assert.Equal(t, domain.SellOK, res)
}
