//go:build unit

package app

import (
	"context"
	"errors"
	"sync"
	"testing"

	storagedomain "github.com/bouroo/goAthena/internal/modules/commerce/storage/domain"
	storageinfra "github.com/bouroo/goAthena/internal/modules/commerce/storage/infra"
	invdomain "github.com/bouroo/goAthena/internal/modules/inventory/domain"
	invinfra "github.com/bouroo/goAthena/internal/modules/inventory/infra"
)

// fakeInv is an in-memory InventoryPort stand-in for the orchestrator tests.
// Same shape as inventory.InventoryService.LoadByChar/Add/Remove; stackable
// items merge into an existing same-NameID row.
type fakeInv struct {
	mu    sync.Mutex
	items map[invdomain.ItemID]invdomain.Item
	next  uint32
}

func newFakeInv() *fakeInv {
	return &fakeInv{items: make(map[invdomain.ItemID]invdomain.Item)}
}

func (f *fakeInv) LoadByChar(_ context.Context, _, charID uint32) ([]invdomain.Item, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]invdomain.Item, 0, len(f.items))
	for _, it := range f.items {
		if it.CharID == charID {
			out = append(out, it)
		}
	}
	return out, nil
}

func (f *fakeInv) Add(_ context.Context, charID, nameID uint32, amount int) (invdomain.Item, error) {
	if amount <= 0 {
		return invdomain.Item{}, invdomain.ErrInsufficientAmount
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	// Stack into an existing same-NameID row when the item is non-equipped.
	for id, it := range f.items {
		if it.CharID == charID && it.NameID == nameID && it.Equip == 0 {
			it.Amount += uint32(amount)
			f.items[id] = it
			return it, nil
		}
	}
	f.next++
	item := invdomain.Item{
		ID:     invdomain.ItemID(f.next),
		CharID: charID,
		NameID: nameID,
		Amount: uint32(amount),
	}
	f.items[item.ID] = item
	return item, nil
}

func (f *fakeInv) Remove(_ context.Context, id invdomain.ItemID, amount int) error {
	if amount <= 0 {
		return invdomain.ErrInsufficientAmount
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	it, ok := f.items[id]
	if !ok {
		return invdomain.ErrItemNotFound
	}
	if int(it.Amount) < amount {
		return invdomain.ErrInsufficientAmount
	}
	if int(it.Amount) == amount {
		delete(f.items, id)
		return nil
	}
	it.Amount -= uint32(amount)
	f.items[id] = it
	return nil
}

func (f *fakeInv) SetEquip(_ context.Context, id invdomain.ItemID, equip uint32) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	it, ok := f.items[id]
	if !ok {
		return invdomain.ErrItemNotFound
	}
	it.Equip = equip
	f.items[id] = it
	return nil
}

// fakeStorage mirrors the storage bounded-context repo for orchestrator tests.
type fakeStorage struct {
	*storageinfra.MemoryStorageRepository
}

func newFakeStorage() *fakeStorage {
	return &fakeStorage{MemoryStorageRepository: storageinfra.NewMemoryStorageRepository()}
}

// helper: build a StorageService with the fake ports.
func newStorageTestService() (*StorageService, *fakeInv, *fakeStorage) {
	inv := newFakeInv()
	sto := newFakeStorage()
	return NewStorageService(inv, sto), inv, sto
}

func TestStorage_MoveToStorage_Stackable(t *testing.T) {
	svc, inv, sto := newStorageTestService()
	ctx := context.Background()

	// Seed bag with 10x Red Potion.
	row, err := inv.Add(ctx, 150001, 501, 10)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	// wireIndex = server row 0 + 2 = 2.
	res, err := svc.MoveToStorage(ctx, 2000001, 150001, 2, 4)
	if err != nil {
		t.Fatalf("move to storage: %v", err)
	}
	if !res.IsFromBag {
		t.Errorf("IsFromBag = false, want true")
	}
	if res.Amount != 4 {
		t.Errorf("stored amount = %d, want 4", res.Amount)
	}

	// Bag row amount decremented to 6.
	bag, _ := inv.LoadByChar(ctx, 2000001, 150001)
	if len(bag) != 1 || bag[0].Amount != 6 {
		t.Errorf("bag after move = %+v, want 1 row amount=6", bag)
	}
	// Warehouse has the 4x Red Potion at row 0.
	warehouse, _ := sto.LoadByAccount(ctx, 2000001)
	if len(warehouse) != 1 || warehouse[0].Amount != 4 {
		t.Errorf("warehouse after move = %+v, want 1 row amount=4", warehouse)
	}

	// Wire index for the warehouse row = server row 0 + 2 = 2.
	if res.Index != 2 {
		t.Errorf("wire index = %d, want 2", res.Index)
	}
	_ = row
}

func TestStorage_MoveToStorage_MergesIntoExisting(t *testing.T) {
	svc, inv, sto := newStorageTestService()
	ctx := context.Background()

	// Seed bag with 5x Red Potion, then deposit 3 → warehouse has 3.
	_, _ = inv.Add(ctx, 150001, 501, 5)
	if _, err := svc.MoveToStorage(ctx, 2000001, 150001, 2, 3); err != nil {
		t.Fatalf("first move: %v", err)
	}
	// Bag now has 2x Red Potion.
	if _, err := inv.Add(ctx, 150001, 501, 8); err != nil {
		t.Fatalf("re-fill bag: %v", err)
	}
	// Deposit 4 more from bag → warehouse row merges into 7.
	res, err := svc.MoveToStorage(ctx, 2000001, 150001, 2, 4)
	if err != nil {
		t.Fatalf("second move: %v", err)
	}
	if res.Amount != 7 {
		t.Errorf("merged warehouse amount = %d, want 7", res.Amount)
	}
	warehouse, _ := sto.LoadByAccount(ctx, 2000001)
	if len(warehouse) != 1 || warehouse[0].Amount != 7 {
		t.Errorf("warehouse = %+v, want 1 row amount=7", warehouse)
	}
}

func TestStorage_MoveFromStorage_Stackable(t *testing.T) {
	svc, inv, _ := newStorageTestService()
	ctx := context.Background()

	// Seed warehouse with 5x Red Potion via direct repo access.
	_, err := svc.storage.Add(ctx, 2000001, 501, 5)
	if err != nil {
		t.Fatalf("seed warehouse: %v", err)
	}
	res, err := svc.MoveFromStorage(ctx, 2000001, 150001, 2, 2)
	if err != nil {
		t.Fatalf("move from storage: %v", err)
	}
	if res.IsFromBag {
		t.Errorf("IsFromBag = true, want false")
	}
	// Bag row now has 2x Red Potion.
	bag, _ := inv.LoadByChar(ctx, 2000001, 150001)
	if len(bag) != 1 || bag[0].Amount != 2 {
		t.Errorf("bag after withdraw = %+v, want 1 row amount=2", bag)
	}
}

func TestStorage_MoveToStorage_IndexOutOfRange(t *testing.T) {
	svc, _, _ := newStorageTestService()
	ctx := context.Background()

	// Empty bag — wire index 2 is out of range (rows start at 1).
	_, err := svc.MoveToStorage(ctx, 2000001, 150001, 2, 1)
	if !errors.Is(err, ErrStorageIndexOutOfRange) {
		t.Errorf("expected ErrStorageIndexOutOfRange, got %v", err)
	}
}

func TestStorage_MoveToStorage_ZeroIndex(t *testing.T) {
	svc, _, _ := newStorageTestService()
	_, err := svc.MoveToStorage(context.Background(), 2000001, 150001, 0, 1)
	if !errors.Is(err, ErrStorageIndexOutOfRange) {
		t.Errorf("expected ErrStorageIndexOutOfRange for wire index 0, got %v", err)
	}
}

func TestStorage_MoveToStorage_EquippedRejected(t *testing.T) {
	svc, inv, _ := newStorageTestService()
	ctx := context.Background()

	// Seed bag with an equipped item (Equip != 0).
	row, err := inv.Add(ctx, 150001, 1101, 1)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := inv.SetEquip(ctx, row.ID, 0x0001); err != nil {
		t.Fatalf("set equip: %v", err)
	}
	_, err = svc.MoveToStorage(ctx, 2000001, 150001, 2, 1)
	if !errors.Is(err, ErrStorageEquipped) {
		t.Errorf("expected ErrStorageEquipped, got %v", err)
	}
}

func TestStorage_MoveToStorage_Insufficient(t *testing.T) {
	svc, inv, _ := newStorageTestService()
	ctx := context.Background()

	if _, err := inv.Add(ctx, 150001, 501, 3); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := svc.MoveToStorage(ctx, 2000001, 150001, 2, 5)
	if !errors.Is(err, ErrStorageInsufficient) {
		t.Errorf("expected ErrStorageInsufficient, got %v", err)
	}
}

func TestStorage_MoveToStorage_WarehouseFull(t *testing.T) {
	svc, inv, sto := newStorageTestService()
	ctx := context.Background()

	// Fill account 2000001's warehouse to MAX_STORAGE with equipment (each
	// row distinct because Equip!=0 forces a new row).
	for i := 0; i < storagedomain.MaxStorage; i++ {
		row, err := sto.Add(ctx, 2000001, uint32(2000+i), 1) //nolint:gosec // G115: i fits uint32
		if err != nil {
			t.Fatalf("seed warehouse %d: %v", i, err)
		}
		if err := sto.SetEquip(ctx, row.ID, 0x0001); err != nil {
			t.Fatalf("set equip %d: %v", i, err)
		}
	}
	// Seed the bag with a stackable item.
	if _, err := inv.Add(ctx, 150001, 501, 1); err != nil {
		t.Fatalf("seed bag: %v", err)
	}
	// Move should fail with ErrStorageFull.
	_, err := svc.MoveToStorage(ctx, 2000001, 150001, 2, 1)
	if !errors.Is(err, storagedomain.ErrStorageFull) && !errors.Is(err, ErrStorageFull) {
		t.Errorf("expected ErrStorageFull, got %v", err)
	}
}

func TestStorage_LoadWarehouse(t *testing.T) {
	svc, _, sto := newStorageTestService()
	ctx := context.Background()

	if _, err := sto.Add(ctx, 2000001, 501, 5); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := sto.Add(ctx, 2000001, 502, 3); err != nil {
		t.Fatalf("seed 2: %v", err)
	}
	warehouse, err := svc.LoadWarehouse(ctx, 2000001)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(warehouse) != 2 {
		t.Errorf("warehouse rows = %d, want 2", len(warehouse))
	}
}

// Confirm storage_test.go uses the invinfra alias to keep gofmt happy (the
// package is referenced in the broader gateway harness tests).
var _ = invinfra.NewMemoryItemRepository
