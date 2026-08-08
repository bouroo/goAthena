//go:build unit

package app_test

import (
	"context"
	"testing"

	"github.com/bouroo/goAthena/internal/modules/inventory/app"
	"github.com/bouroo/goAthena/internal/modules/inventory/infra"
)

func TestAdd_Load_Remove(t *testing.T) {
	svc := app.NewInventoryService(infra.NewMemoryItemRepository())

	it, err := svc.Add(context.Background(), 150001, 501, 10) // 10x Red Potion
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if it.Amount != 10 || it.NameID != 501 {
		t.Errorf("item = %+v", it)
	}

	loaded, err := svc.LoadByChar(context.Background(), 2000001, 150001)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(loaded) != 1 || loaded[0].Amount != 10 {
		t.Fatalf("loaded = %+v", loaded)
	}

	if err := svc.Remove(context.Background(), it.ID, 4); err != nil {
		t.Fatalf("remove 4: %v", err)
	}
	loaded, _ = svc.LoadByChar(context.Background(), 2000001, 150001)
	if loaded[0].Amount != 6 {
		t.Errorf("amount after remove = %d, want 6", loaded[0].Amount)
	}

	// Remove the rest → row deleted.
	if err := svc.Remove(context.Background(), it.ID, 6); err != nil {
		t.Fatalf("remove 6: %v", err)
	}
	loaded, _ = svc.LoadByChar(context.Background(), 2000001, 150001)
	if len(loaded) != 0 {
		t.Errorf("items after full remove = %d, want 0", len(loaded))
	}
}

func TestRemove_Insufficient(t *testing.T) {
	svc := app.NewInventoryService(infra.NewMemoryItemRepository())
	it, _ := svc.Add(context.Background(), 1, 501, 3)
	if err := svc.Remove(context.Background(), it.ID, 5); err == nil {
		t.Error("expected error removing more than owned")
	}
}

func TestAdd_InvalidAmount(t *testing.T) {
	svc := app.NewInventoryService(infra.NewMemoryItemRepository())
	if _, err := svc.Add(context.Background(), 1, 501, 0); err == nil {
		t.Error("expected error for zero amount")
	}
}
