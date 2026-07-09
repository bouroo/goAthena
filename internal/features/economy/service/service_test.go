//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

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
