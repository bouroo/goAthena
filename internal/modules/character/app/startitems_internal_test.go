//go:build unit

package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	invdomain "github.com/bouroo/goAthena/internal/modules/inventory/domain"
)

// recordingInv is an InventoryRepository stand-in that records AddItem and
// EquipItem calls so the seeder's one-row-per-entry, equip-on-nonzero-location
// behavior can be asserted. Its row counter hands each AddItem a distinct Index.
type recordingInv struct {
	added   []addCall
	equips  []equipCall
	nextIdx uint16
}

type addCall struct {
	accountID, charID uint32
	nameID, amount    uint32
	stackable         bool
}

type equipCall struct {
	accountID, charID uint32
	index             uint16
	equip             uint32
}

func (r *recordingInv) LoadByChar(context.Context, uint32, uint32) ([]invdomain.InventoryItem, error) {
	return nil, nil
}

func (r *recordingInv) AddItem(_ context.Context, accountID, charID uint32, item invdomain.NewItem) (invdomain.InventoryItem, error) {
	r.added = append(r.added, addCall{accountID, charID, item.NameID, item.Amount, item.Stackable})
	r.nextIdx++
	return invdomain.InventoryItem{Index: r.nextIdx, NameID: item.NameID}, nil
}

func (r *recordingInv) EquipItem(_ context.Context, accountID, charID uint32, index uint16, equip uint32) (invdomain.InventoryItem, error) {
	r.equips = append(r.equips, equipCall{accountID, charID, index, equip})
	return invdomain.InventoryItem{Index: index}, nil
}

func (r *recordingInv) UnequipItem(context.Context, uint32, uint32, uint16) (invdomain.InventoryItem, error) {
	return invdomain.InventoryItem{}, nil
}

func (r *recordingInv) ConsumeItem(context.Context, uint32, uint32, uint16, uint16) (invdomain.InventoryItem, bool, error) {
	return invdomain.InventoryItem{}, false, nil
}

// TestParseStartItems_Default parses rAthena's shipped default and asserts the
// three triples land with the right nameid/amount/equip: Knife in the right hand
// (EQP_HAND_R=2), Cotton Shirt on the body (EQP_ARMOR=16), the bonus usable loose
// (location 0).
func TestParseStartItems_Default(t *testing.T) {
	t.Parallel()
	items, err := ParseStartItems("1201,1,2:2301,1,16:23484,1,0")
	require.NoError(t, err)
	require.Len(t, items, 3)
	assert.Equal(t, StartItem{1201, 1, 2}, items[0])
	assert.Equal(t, StartItem{2301, 1, 16}, items[1])
	assert.Equal(t, StartItem{23484, 1, 0}, items[2])
}

// TestParseStartItems_BlankAndTolerant covers the disabled path (blank → no
// items) and rAthena's tolerant field split: a trailing/leading/doubled colon is
// skipped, and whitespace around fields is trimmed.
func TestParseStartItems_BlankAndTolerant(t *testing.T) {
	t.Parallel()
	items, err := ParseStartItems("")
	require.NoError(t, err)
	assert.Empty(t, items)

	items, err = ParseStartItems(" 1201 , 1 , 2 :")
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, StartItem{1201, 1, 2}, items[0])
}

// TestParseStartItems_ClampAndErrors asserts the amount clamp to MAX_AMOUNT
// (30000) and that malformed entries fail with a clear, indexed error: wrong
// field count, non-numeric, and zero amount.
func TestParseStartItems_ClampAndErrors(t *testing.T) {
	t.Parallel()
	items, err := ParseStartItems("501,99999,0")
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, uint32(30000), items[0].Amount, "amount clamped to MAX_AMOUNT")

	_, err = ParseStartItems("501,5") // only two fields
	require.Error(t, err)
	assert.Contains(t, err.Error(), "entry 1")

	_, err = ParseStartItems("501,abc,0") // non-numeric amount
	require.Error(t, err)
	assert.Contains(t, err.Error(), "amount")

	_, err = ParseStartItems("501,0,0") // zero amount
	require.Error(t, err)
	assert.Contains(t, err.Error(), "amount must be > 0")
}

// TestStartingItemSeeder_Seed_OneRowPerEntryEquipsNonZero proves Seed mirrors
// char.cpp:1518-1519: every configured entry becomes one AddItem (Stackable=false,
// never merged), and only entries with a nonzero location call EquipItem with
// that bitmask. Knife (loc 2) and Cotton Shirt (loc 16) equip; the loc-0 usable
// does not.
func TestStartingItemSeeder_Seed_OneRowPerEntryEquipsNonZero(t *testing.T) {
	t.Parallel()
	items := []StartItem{{1201, 1, 2}, {2301, 1, 16}, {23484, 1, 0}}
	repo := &recordingInv{}
	seeder := NewStartingItemSeeder(items, repo)

	require.NoError(t, seeder.Seed(context.Background(), 42, 7))

	require.Len(t, repo.added, 3, "one AddItem per entry, no merge")
	for _, a := range repo.added {
		assert.Equal(t, uint32(42), a.accountID)
		assert.Equal(t, uint32(7), a.charID)
		assert.False(t, a.stackable, "start items are never merged (Stackable=false)")
	}
	assert.Equal(t, uint32(1201), repo.added[0].nameID)
	assert.Equal(t, uint32(2301), repo.added[1].nameID)
	assert.Equal(t, uint32(23484), repo.added[2].nameID)

	require.Len(t, repo.equips, 2, "only the two equipped entries call EquipItem")
	assert.Equal(t, equipCall{42, 7, 1, 2}, repo.equips[0], "Knife → right hand at index 1")
	assert.Equal(t, equipCall{42, 7, 2, 16}, repo.equips[1], "Cotton Shirt → body at index 2")
}

// TestStartingItemSeeder_Seed_EmptyNoop asserts an empty list (start_items
// cleared) is a no-op: no AddItem, no EquipItem, no error.
func TestStartingItemSeeder_Seed_EmptyNoop(t *testing.T) {
	t.Parallel()
	repo := &recordingInv{}
	seeder := NewStartingItemSeeder(nil, repo)
	require.NoError(t, seeder.Seed(context.Background(), 1, 1))
	assert.Empty(t, repo.added)
	assert.Empty(t, repo.equips)
}
