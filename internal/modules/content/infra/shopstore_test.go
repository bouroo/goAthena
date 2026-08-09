//go:build unit

package infra

import (
	"context"
	"testing"
)

// DevShopGIDForTest mirrors the commerce dev-seed GID; kept local to avoid a
// commerce import in the content infra test.
const DevShopGIDForTest uint32 = 110000000

func TestMemoryShopStoreShopForNPC(t *testing.T) {
	s := NewMemoryShopStore()

	if name, ok := s.ShopForNPC(context.Background(), DevShopGIDForTest); ok {
		t.Fatalf("unregistered GID resolved to %q, want ok=false", name)
	}

	s.RegisterShop(DevShopGIDForTest, "Tool Shop")

	name, ok := s.ShopForNPC(context.Background(), DevShopGIDForTest)
	if !ok {
		t.Fatal("registered GID not found, want ok=true")
	}
	if name != "Tool Shop" {
		t.Fatalf("got shop name %q, want %q", name, "Tool Shop")
	}

	if _, ok := s.ShopForNPC(context.Background(), 999); ok {
		t.Fatal("unregistered GID resolved, want ok=false")
	}
}
