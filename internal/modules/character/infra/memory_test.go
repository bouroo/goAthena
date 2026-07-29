//go:build unit

package infra_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bouroo/goAthena/internal/modules/character/domain"
	"github.com/bouroo/goAthena/internal/modules/character/infra"
)

// TestMemoryCharacterRepository_SavePosition asserts the warp persistence port
// writes last_map/last_x/last_y under the (accountID, charID) scoping contract,
// and returns ErrCharacterNotFound for a char that does not belong to the
// account (the impersonation guard shared with SaveProgression).
func TestMemoryCharacterRepository_SavePosition(t *testing.T) {
	t.Parallel()

	const acct uint32 = 2000001
	repo := infra.NewMemoryCharacterRepository(domain.Character{
		CharID:    150000,
		AccountID: acct,
		Slot:      0,
		LastMap:   "prontera",
		LastX:     53,
		LastY:     111,
	})

	require.NoError(t, repo.SavePosition(context.Background(), acct, 150000, "izlude", 128, 128))

	got, err := repo.GetByID(context.Background(), acct, 150000)
	require.NoError(t, err)
	assert.Equal(t, "izlude", got.LastMap)
	assert.Equal(t, uint16(128), got.LastX)
	assert.Equal(t, uint16(128), got.LastY)

	// A char_id from another account (or a non-existent char) yields not-found,
	// never a cross-account write.
	assert.ErrorIs(t,
		repo.SavePosition(context.Background(), acct, 999999, "geffen", 1, 1),
		domain.ErrCharacterNotFound, "unknown char_id is not found, not a silent write")
}
