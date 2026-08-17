//go:build unit

package shop

import (
	"strings"
	"testing"

	"github.com/bouroo/goAthena/pkg/ro/itemdb"
	"github.com/bouroo/goAthena/pkg/ro/script"
)

// mustItemDB loads an in-memory item_db stream for tests.
func mustItemDB(t *testing.T, src string) *itemdb.Registry {
	t.Helper()
	reg, err := itemdb.Load(strings.NewReader(src))
	if err != nil {
		t.Fatalf("itemdb.Load: %v", err)
	}
	return reg
}

// TestBuildCatalog_PriceResolution proves the three pricing paths: explicit
// table price keeps its buy price but takes item_db's Sell; price -1 resolves
// both from item_db; an item with -1 and no item_db row is dropped rather
// than sold free.
func TestBuildCatalog_PriceResolution(t *testing.T) {
	t.Parallel()
	items := mustItemDB(t, `
Header:
  Type: ITEM_DB
  Version: 3
Body:
  - Id: 501
    AegisName: RedPotion
    Name: Red Potion
    Buy: 50
    Sell: 25
  - Id: 1201
    AegisName: Knife
    Name: Knife
    Buy: 25
    Sell: 12
`)

	src := []byte("prontera,100,100,4\tshop\tDealer\t90,501:-1,1201:40,99999:-1\n")
	set, err := script.Compile(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if len(set.Shops) != 1 {
		t.Fatalf("shops = %d, want 1", len(set.Shops))
	}

	cat := buildCatalog(set, items)
	shop, ok := cat.Get("Dealer")
	if !ok {
		t.Fatalf("Dealer shop missing from catalog")
	}
	if len(shop.Items) != 2 {
		t.Fatalf("items = %d, want 2 (unpriceable 99999 dropped)", len(shop.Items))
	}
	// 501:-1 → item_db Buy/Sell.
	if it, ok := shop.FindBuy(501); !ok || it.Price != 50 || it.SellPrice != 25 {
		t.Errorf("501: got %+v, want price 50 sell 25 (item_db default)", it)
	}
	// 1201:40 → explicit buy 40, item_db Sell 12.
	if it, ok := shop.FindBuy(1201); !ok || it.Price != 40 || it.SellPrice != 12 {
		t.Errorf("1201: got %+v, want price 40 (explicit) sell 12 (item_db)", it)
	}
}

// TestBuildCatalog_DevFallback proves an empty script set keeps the dev shop.
func TestBuildCatalog_DevFallback(t *testing.T) {
	t.Parallel()
	cat := buildCatalog(script.NewCompiledScriptSet(), nil)
	if _, ok := cat.Get(devShopName); !ok {
		t.Fatalf("dev shop %q missing on empty scripts", devShopName)
	}
}
