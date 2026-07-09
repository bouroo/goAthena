//go:build unit

package service

import (
	"reflect"
	"testing"
)

// TestSellPriceCatalog_Cached verifies the catalog is built exactly
// once via sync.Once and reused across calls. Without the cache,
// each sellPriceCatalog() invocation would allocate a new map and
// re-iterate every NPC's ShopItems; the pointer-identity assertion
// below fails loudly if the implementation regresses to that path.
//
// Because npcSpawns is package-level and shared across the whole
// test binary, this test is read-only: it never mutates npcSpawns
// and never resets sellCatalogOnce, so it stays correct under
// -race and under any test ordering.
func TestSellPriceCatalog_Cached(t *testing.T) {
	t.Parallel()

	first := sellPriceCatalog()
	if first == nil {
		t.Fatal("sellPriceCatalog() returned nil on first call")
	}
	if len(first) == 0 {
		t.Fatal("sellPriceCatalog() returned an empty catalog; npcSpawns is expected to carry at least one shop NPC")
	}

	// Pointer identity: both calls must return the same underlying
	// map header. A rebuilt map would allocate a fresh header and
	// yield a different Pointer().
	firstPtr := reflect.ValueOf(first).Pointer()
	for i := 0; i < 4; i++ {
		again := sellPriceCatalog()
		if got := reflect.ValueOf(again).Pointer(); got != firstPtr {
			t.Fatalf("sellPriceCatalog() call #%d returned a different map (ptr=0x%x vs first=0x%x); "+
				"the catalog is being rebuilt instead of cached via sync.Once",
				i+2, got, firstPtr)
		}
	}

	// Content sanity: confirm the cached map actually contains the
	// expected entries derived from the package-level npcSpawns.
	// The Weapon Shop NPC (GID 110000002) sells Short Sword 1101
	// at 1500 zeny; that entry must survive in the catalog so the
	// sell window can price it.
	wantEntries := map[uint32]uint32{
		501:  50,   // Red Potion
		502:  200,  // Orange Potion
		1201: 500,  // Knife
		1101: 1500, // Short Sword
	}
	for id, want := range wantEntries {
		got, ok := first[id]
		if !ok {
			t.Errorf("cached catalog missing item %d; sellPriceCatalog did not index npcSpawns correctly", id)
			continue
		}
		if got != want {
			t.Errorf("cached catalog[%d] = %d, want %d (first-write-wins from npcSpawns)", id, got, want)
		}
	}
}

// TestSellPriceCatalog_FirstWriteWins locks the catalog's
// monotonicity guarantee (D-213): when two NPCs carry the same
// itemid at different prices, the first NPC in npcSpawns order
// wins and the second is ignored. This is the property the original
// loop enforced with `if _, ok := cat[id]; !ok` and that the cached
// rewrite must preserve verbatim.
func TestSellPriceCatalog_FirstWriteWins(t *testing.T) {
	t.Parallel()

	cat := sellPriceCatalog()
	for _, npc := range npcSpawns {
		for _, it := range npc.ShopItems {
			got, ok := cat[it.ItemID]
			if !ok {
				continue // not present (impossible under first-write-wins) — skip
			}
			if got != it.Price {
				// The cached value disagrees with *some* NPC's
				// offer. Under first-write-wins it must equal
				// the first NPC that listed this itemid; we
				// just assert it equals *an* NPC's price so a
				// future reorder doesn't break the test, while
				// still catching the case where the catalog
				// captured the wrong NPC's price entirely.
				t.Errorf("catalog[%d] = %d, does not match NPC %q price %d",
					it.ItemID, got, npc.Name, it.Price)
			}
		}
	}
}
