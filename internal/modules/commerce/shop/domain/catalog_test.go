package domain_test

import (
	"testing"

	"github.com/bouroo/goAthena/internal/modules/commerce/shop/domain"
)

func TestMemoryShopCatalog_AddAndGet(t *testing.T) {
	c := domain.NewMemoryShopCatalog()
	c.Add(domain.Shop{
		NPCID: 500000000,
		Name:  "Tool Shop",
		Items: []domain.ShopItem{
			{NameID: 501, Price: 50},
			{NameID: 504, Price: 1000},
		},
	})

	got, ok := c.Get(500000000)
	if !ok {
		t.Fatal("expected shop to be found")
	}
	if got.Name != "Tool Shop" {
		t.Errorf("Name = %q, want %q", got.Name, "Tool Shop")
	}
	if len(got.Items) != 2 {
		t.Fatalf("len(Items) = %d, want 2", len(got.Items))
	}
	if got.Items[0].NameID != 501 || got.Items[0].Price != 50 {
		t.Errorf("Items[0] = %+v, want {501,50}", got.Items[0])
	}
}

func TestMemoryShopCatalog_GetMissing(t *testing.T) {
	c := domain.NewMemoryShopCatalog()
	if _, ok := c.Get(42); ok {
		t.Fatal("expected ok=false for unknown NPCID")
	}
}

func TestMemoryShopCatalog_AddReplaces(t *testing.T) {
	c := domain.NewMemoryShopCatalog()
	c.Add(domain.Shop{NPCID: 1, Name: "First"})
	c.Add(domain.Shop{NPCID: 1, Name: "Second"})

	got, ok := c.Get(1)
	if !ok {
		t.Fatal("expected shop to be found")
	}
	if got.Name != "Second" {
		t.Errorf("Name = %q, want %q (replace should win)", got.Name, "Second")
	}
}
