//go:build unit

package shop

import (
	"testing"
)

func TestNewDevCatalog(t *testing.T) {
	catalog := newDevCatalog()

	shop, ok := catalog.Get(devShopName)
	if !ok {
		t.Fatalf("dev catalog missing %q shop", devShopName)
	}
	if shop.Name != devShopName {
		t.Fatalf("shop name = %q, want %q", shop.Name, devShopName)
	}
	if len(shop.Items) != 2 {
		t.Fatalf("got %d items, want 2", len(shop.Items))
	}

	potion, ok := shop.FindBuy(501) // Red Potion
	if !ok {
		t.Fatal("Red Potion (nameID 501) not in dev shop")
	}
	if potion.Price <= 0 || potion.SellPrice <= 0 {
		t.Fatalf("Red Potion prices invalid: buy=%d sell=%d", potion.Price, potion.SellPrice)
	}

	knife, ok := shop.FindBuy(1201) // Knife
	if !ok {
		t.Fatal("Knife (nameID 1201) not in dev shop")
	}
	if knife.Price <= 0 || knife.SellPrice <= 0 {
		t.Fatalf("Knife prices invalid: buy=%d sell=%d", knife.Price, knife.SellPrice)
	}
}
