//go:build unit

package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/bouroo/goAthena/internal/modules/commerce/storage/app"
	"github.com/bouroo/goAthena/internal/modules/commerce/storage/domain"
	"github.com/bouroo/goAthena/internal/modules/commerce/storage/infra"
)

func TestAdd_Load_Remove(t *testing.T) {
	svc := app.NewStorageService(infra.NewMemoryStorageRepository())

	it, err := svc.Add(context.Background(), 2000001, 501, 10) // 10x Red Potion into warehouse
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if it.Amount != 10 || it.NameID != 501 {
		t.Errorf("item = %+v", it)
	}

	loaded, err := svc.LoadByAccount(context.Background(), 2000001)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(loaded) != 1 || loaded[0].Amount != 10 {
		t.Fatalf("loaded = %+v", loaded)
	}

	if err := svc.Remove(context.Background(), it.ID, 4); err != nil {
		t.Fatalf("remove 4: %v", err)
	}
	loaded, _ = svc.LoadByAccount(context.Background(), 2000001)
	if loaded[0].Amount != 6 {
		t.Errorf("amount after remove = %d, want 6", loaded[0].Amount)
	}

	// Remove the rest → row deleted.
	if err := svc.Remove(context.Background(), it.ID, 6); err != nil {
		t.Fatalf("remove 6: %v", err)
	}
	loaded, _ = svc.LoadByAccount(context.Background(), 2000001)
	if len(loaded) != 0 {
		t.Errorf("items after full remove = %d, want 0", len(loaded))
	}
}

func TestAdd_StacksIntoExistingRow(t *testing.T) {
	repo := infra.NewMemoryStorageRepository()
	svc := app.NewStorageService(repo)

	first, err := svc.Add(context.Background(), 42, 501, 3)
	if err != nil {
		t.Fatalf("first add: %v", err)
	}
	second, err := svc.Add(context.Background(), 42, 501, 5)
	if err != nil {
		t.Fatalf("second add: %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("stackable items should share row id; got %d and %d", first.ID, second.ID)
	}
	if second.Amount != 8 {
		t.Errorf("stacked amount = %d, want 8", second.Amount)
	}
	rows, _ := svc.LoadByAccount(context.Background(), 42)
	if len(rows) != 1 {
		t.Errorf("row count after stacking = %d, want 1", len(rows))
	}
}

func TestAdd_EquipmentInsertsSeparateRow(t *testing.T) {
	repo := infra.NewMemoryStorageRepository()
	svc := app.NewStorageService(repo)

	// Equip slot bitmask non-zero → always a new row, never stacks.
	row1, err := svc.Add(context.Background(), 42, 1101, 1) // some sword
	if err != nil {
		t.Fatalf("add1: %v", err)
	}
	if err := repo.SetEquip(context.Background(), row1.ID, 0x0001); err != nil {
		t.Fatalf("set equip: %v", err)
	}
	row2, err := svc.Add(context.Background(), 42, 1101, 1) // same sword nameid
	if err != nil {
		t.Fatalf("add2: %v", err)
	}
	if row1.ID == row2.ID {
		t.Errorf("equipment with same NameID should not stack (row1=%d row2=%d)", row1.ID, row2.ID)
	}
}

func TestRemove_Insufficient(t *testing.T) {
	svc := app.NewStorageService(infra.NewMemoryStorageRepository())
	it, _ := svc.Add(context.Background(), 1, 501, 3)
	if err := svc.Remove(context.Background(), it.ID, 5); err == nil {
		t.Error("expected error removing more than owned")
	}
}

func TestAdd_InvalidAmount(t *testing.T) {
	svc := app.NewStorageService(infra.NewMemoryStorageRepository())
	if _, err := svc.Add(context.Background(), 1, 501, 0); err == nil {
		t.Error("expected error for zero amount")
	}
	if _, err := svc.Add(context.Background(), 1, 501, -1); err == nil {
		t.Error("expected error for negative amount")
	}
}

func TestAdd_StorageFull(t *testing.T) {
	svc := app.NewStorageService(infra.NewMemoryStorageRepository())
	// Fill the warehouse to MaxStorage by inserting equipment rows (each item
	// is its own row because Equip!=0). We synthesize Equip on rows via the
	// repo (public for tests).
	repo := infra.NewMemoryStorageRepository()
	svc = app.NewStorageService(repo)
	for i := 0; i < domain.MaxStorage; i++ {
		row, err := svc.Add(context.Background(), 99, uint32(2000+i), 1) //nolint:gosec // G115: i fits uint32
		if err != nil {
			t.Fatalf("seed add %d: %v", i, err)
		}
		if err := repo.SetEquip(context.Background(), row.ID, 0x0001); err != nil {
			t.Fatalf("seed set equip %d: %v", i, err)
		}
	}
	// One more should fail with ErrStorageFull.
	if _, err := svc.Add(context.Background(), 99, 9999, 1); !errors.Is(err, domain.ErrStorageFull) {
		t.Errorf("expected ErrStorageFull, got %v", err)
	}
}
