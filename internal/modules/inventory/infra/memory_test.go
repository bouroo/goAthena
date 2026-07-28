//go:build unit

package infra_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bouroo/goAthena/internal/modules/inventory/domain"
	"github.com/bouroo/goAthena/internal/modules/inventory/infra"
)

const (
	memAccID  uint32 = 2000700
	memCharID uint32 = 150002
)

// TestMemoryRepo_LoadByChar_Empty asserts the contract that a character with no
// rows yields an empty (non-nil) slice and no error — the enter-burst path and
// pickup both depend on this rather than a nil slice.
func TestMemoryRepo_LoadByChar_Empty(t *testing.T) {
	t.Parallel()
	repo := infra.NewMemoryInventoryRepository()
	got, err := repo.LoadByChar(context.Background(), memAccID, memCharID)
	require.NoError(t, err)
	assert.NotNil(t, got)
	assert.Empty(t, got)
}

// TestMemoryRepo_AddItem_InsertMergeIndex covers the merge/insert contract the
// pickup ack's Index field depends on: a fresh non-stackable row takes the next
// free slot; a stackable add merges into a matching row and keeps that row's
// index; a non-stackable add always takes a new slot even when a same-nameid row
// exists.
func TestMemoryRepo_AddItem_InsertMergeIndex(t *testing.T) {
	t.Parallel()
	repo := infra.NewMemoryInventoryRepository()
	ctx := context.Background()

	// First Jellopy (stackable) → new row, index 0.
	first, err := repo.AddItem(ctx, memAccID, memCharID, domain.NewItem{NameID: 909, Amount: 1, Stackable: true})
	require.NoError(t, err)
	assert.Equal(t, uint16(0), first.Index, "first row takes slot 0")
	assert.Equal(t, uint32(1), first.Amount)

	// Second Jellopy (stackable) → merges into row 0; no new slot, amount 2.
	merged, err := repo.AddItem(ctx, memAccID, memCharID, domain.NewItem{NameID: 909, Amount: 1, Stackable: true})
	require.NoError(t, err)
	assert.Equal(t, uint16(0), merged.Index, "merge keeps the matched row's index")
	assert.Equal(t, uint32(2), merged.Amount)

	// A Knife (non-stackable) → new row at index 1 even though Jellopy exists.
	knife, err := repo.AddItem(ctx, memAccID, memCharID, domain.NewItem{NameID: 1201, Amount: 1, Stackable: false})
	require.NoError(t, err)
	assert.Equal(t, uint16(1), knife.Index, "non-stackable insert takes the next slot")

	// LoadByChar reflects one Jellopy (amount 2) and one Knife (amount 1), in
	// id-ascending index order.
	rows, err := repo.LoadByChar(ctx, memAccID, memCharID)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, uint32(909), rows[0].NameID)
	assert.Equal(t, uint32(2), rows[0].Amount)
	assert.Equal(t, uint32(1201), rows[1].NameID)
}

// TestMemoryRepo_AddItem_NonStackableSameNameidNewRow asserts two non-stackable
// adds of the same NameID never merge (equipment stacks one-per-row).
func TestMemoryRepo_AddItem_NonStackableSameNameidNewRow(t *testing.T) {
	t.Parallel()
	repo := infra.NewMemoryInventoryRepository()
	ctx := context.Background()

	a, err := repo.AddItem(ctx, memAccID, memCharID, domain.NewItem{NameID: 1201, Amount: 1, Stackable: false})
	require.NoError(t, err)
	assert.Equal(t, uint16(0), a.Index)

	b, err := repo.AddItem(ctx, memAccID, memCharID, domain.NewItem{NameID: 1201, Amount: 1, Stackable: false})
	require.NoError(t, err)
	assert.Equal(t, uint16(1), b.Index, "non-stackable duplicate takes a fresh slot")

	rows, err := repo.LoadByChar(ctx, memAccID, memCharID)
	require.NoError(t, err)
	assert.Len(t, rows, 2)
}

// TestMemoryRepo_AddItem_FullBag asserts the slot-cap guard and that it does not
// trap a stackable merge: at 100 rows a non-stackable add fails with
// ErrInventoryFull, but a stackable add into an existing row still merges.
func TestMemoryRepo_AddItem_FullBag(t *testing.T) {
	t.Parallel()
	var seeds []domain.InventoryItem
	for i := 0; i < domain.MaxInventorySlots; i++ {
		seeds = append(seeds, domain.InventoryItem{
			ID:     uint32(i + 1),
			CharID: memCharID,
			NameID: uint32(1000 + i), // distinct NameID per row so nothing pre-merges
			Amount: 1,
		})
	}
	// Seed one stackable Jellopy at id 1 so a merge is still possible at cap.
	seeds[0] = domain.InventoryItem{ID: 1, CharID: memCharID, NameID: 909, Amount: 1}
	repo := infra.NewMemoryInventoryRepository(seeds...)
	ctx := context.Background()

	// Non-stackable add at the cap → full.
	_, err := repo.AddItem(ctx, memAccID, memCharID, domain.NewItem{NameID: 9999, Amount: 1, Stackable: false})
	assert.True(t, errors.Is(err, domain.ErrInventoryFull), "non-stackable add at cap must be full, got %v", err)

	// Stackable merge into the existing Jellopy row succeeds even at cap.
	merged, err := repo.AddItem(ctx, memAccID, memCharID, domain.NewItem{NameID: 909, Amount: 3, Stackable: true})
	require.NoError(t, err)
	assert.Equal(t, uint32(4), merged.Amount, "stackable merge must bypass the slot cap")
}

// TestMemoryRepo_AddItem_CharScoped asserts two characters never share a stack —
// the (accountID, charID) impersonation guard the port documents.
func TestMemoryRepo_AddItem_CharScoped(t *testing.T) {
	t.Parallel()
	const otherChar uint32 = 150099
	repo := infra.NewMemoryInventoryRepository()
	ctx := context.Background()

	_, err := repo.AddItem(ctx, memAccID, memCharID, domain.NewItem{NameID: 909, Amount: 1, Stackable: true})
	require.NoError(t, err)
	// Same NameID, different char → must not merge into the first char's row.
	_, err = repo.AddItem(ctx, memAccID, otherChar, domain.NewItem{NameID: 909, Amount: 1, Stackable: true})
	require.NoError(t, err)

	got, err := repo.LoadByChar(ctx, memAccID, memCharID)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, uint32(1), got[0].Amount, "cross-char add must not merge")

	other, err := repo.LoadByChar(ctx, memAccID, otherChar)
	require.NoError(t, err)
	require.Len(t, other, 1)
}

// TestMemoryRepo_LoadByChar_PreservesEquip asserts the EQP_* equip bitmask
// round-trips through LoadByChar — the M10b equip loop and the enter-burst
// inventory list both read it from the loaded row.
func TestMemoryRepo_LoadByChar_PreservesEquip(t *testing.T) {
	t.Parallel()
	const eqpArmor uint32 = 0x10
	repo := infra.NewMemoryInventoryRepository(domain.InventoryItem{
		ID: 1, CharID: memCharID, NameID: 2301, Amount: 1, Equip: eqpArmor,
	})
	rows, err := repo.LoadByChar(context.Background(), memAccID, memCharID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, eqpArmor, rows[0].Equip)
	assert.Equal(t, uint16(0), rows[0].Index, "seeded single row takes index 0")
}

// TestMemoryRepo_EquipItem_SetLoadClearRoundTrip is the M10b anchor: equip a
// slot by its grid index, see it reflected through LoadByChar, then unequip and
// see the bitmask cleared — the full equip-loop persistence contract the equip
// use case (and its stat recompute) depends on.
func TestMemoryRepo_EquipItem_SetLoadClearRoundTrip(t *testing.T) {
	t.Parallel()
	// Knife (1201, non-stackable) at index 0; Jellopy (909) at index 1. The equip
	// request names the Knife's grid slot (0), which AddItem assigns id-ascending.
	repo := infra.NewMemoryInventoryRepository()
	ctx := context.Background()
	_, err := repo.AddItem(ctx, memAccID, memCharID, domain.NewItem{NameID: 1201, Amount: 1, Stackable: false})
	require.NoError(t, err)
	_, err = repo.AddItem(ctx, memAccID, memCharID, domain.NewItem{NameID: 909, Amount: 1, Stackable: true})
	require.NoError(t, err)

	const eqpHandR uint32 = 0x0002
	// Equip the Knife in the right hand. The returned row carries the new bitmask
	// and the slot's stable index.
	equipped, err := repo.EquipItem(ctx, memAccID, memCharID, 0, eqpHandR)
	require.NoError(t, err)
	assert.Equal(t, eqpHandR, equipped.Equip)
	assert.Equal(t, uint16(0), equipped.Index)
	assert.Equal(t, uint32(1201), equipped.NameID, "the equipped row is the Knife at slot 0")

	// LoadByChar sees the persisted bitmask; the Jellopy row stays unequipped.
	rows, err := repo.LoadByChar(ctx, memAccID, memCharID)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, eqpHandR, rows[0].Equip, "Knife slot reflects the worn bitmask")
	assert.Equal(t, uint32(0), rows[1].Equip, "Jellopy slot is untouched")

	// Unequip the Knife → bitmask cleared, row still present at the same index.
	unequipped, err := repo.UnequipItem(ctx, memAccID, memCharID, 0)
	require.NoError(t, err)
	assert.Equal(t, uint32(0), unequipped.Equip)
	assert.Equal(t, uint16(0), unequipped.Index)

	rows, err = repo.LoadByChar(ctx, memAccID, memCharID)
	require.NoError(t, err)
	assert.Equal(t, uint32(0), rows[0].Equip, "Knife slot cleared after unequip")
}

// TestMemoryRepo_EquipItem_AssignsNotORs asserts a re-equip overwrites the prior
// bitmask rather than OR-ing into it — faithful to rAthena's
// inventory.u.items_inventory[n].equip = flag (each item is worn in one place).
func TestMemoryRepo_EquipItem_AssignsNotORs(t *testing.T) {
	t.Parallel()
	repo := infra.NewMemoryInventoryRepository(domain.InventoryItem{
		ID: 1, CharID: memCharID, NameID: 1201, Amount: 1,
	})
	ctx := context.Background()

	const (
		eqpHandR uint32 = 0x0002
		eqpHandL uint32 = 0x0020
	)
	_, err := repo.EquipItem(ctx, memAccID, memCharID, 0, eqpHandR)
	require.NoError(t, err)
	re, err := repo.EquipItem(ctx, memAccID, memCharID, 0, eqpHandL)
	require.NoError(t, err)
	assert.Equal(t, eqpHandL, re.Equip, "re-equip assigns, leaving no leftover right-hand bit")
}

// TestMemoryRepo_EquipItem_MissingSlot asserts a slot past the last row (forged
// index, stale client UI after a drop) yields ErrItemNotFound — the use case
// maps it to the fail ack rather than faulting.
func TestMemoryRepo_EquipItem_MissingSlot(t *testing.T) {
	t.Parallel()
	repo := infra.NewMemoryInventoryRepository(domain.InventoryItem{
		ID: 1, CharID: memCharID, NameID: 1201, Amount: 1,
	})
	ctx := context.Background()

	_, err := repo.EquipItem(ctx, memAccID, memCharID, 5, 0x0002)
	assert.True(t, errors.Is(err, domain.ErrItemNotFound), "out-of-range equip index → ErrItemNotFound, got %v", err)

	_, err = repo.UnequipItem(ctx, memAccID, memCharID, 5)
	assert.True(t, errors.Is(err, domain.ErrItemNotFound), "out-of-range unequip index → ErrItemNotFound, got %v", err)
}

// TestMemoryRepo_EquipItem_CharScoped asserts the slot lookup is scoped by char:
// equipping a slot on one character never touches another's row, and a slot that
// is in range for char A but out of range for char B is ErrItemNotFound for B.
func TestMemoryRepo_EquipItem_CharScoped(t *testing.T) {
	t.Parallel()
	const otherChar uint32 = 150099
	repo := infra.NewMemoryInventoryRepository(
		domain.InventoryItem{ID: 1, CharID: memCharID, NameID: 1201, Amount: 1},
	)
	ctx := context.Background()

	equipped, err := repo.EquipItem(ctx, memAccID, memCharID, 0, 0x0002)
	require.NoError(t, err)
	assert.Equal(t, uint32(0x0002), equipped.Equip)

	// Other char has no rows: slot 0 is out of range → ErrItemNotFound.
	_, err = repo.EquipItem(ctx, memAccID, otherChar, 0, 0x0002)
	assert.True(t, errors.Is(err, domain.ErrItemNotFound), "foreign char slot → ErrItemNotFound, got %v", err)

	// The first char's row is untouched by the foreign equip attempt.
	rows, err := repo.LoadByChar(ctx, memAccID, memCharID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, uint32(0x0002), rows[0].Equip)
}
