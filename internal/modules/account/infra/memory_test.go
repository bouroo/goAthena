//go:build unit

package infra_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bouroo/goAthena/internal/modules/account/domain"
	"github.com/bouroo/goAthena/internal/modules/account/infra"
)

// baseTime is a fixed instant local to this package.
var baseTime = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

func TestMemoryAccountRepository_LoadAndMutate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	seed := &domain.Account{AccountID: 2000000, UserID: "test", UserPass: "test", Sex: domain.SexMale}
	repo := infra.NewMemoryAccountRepository(seed)

	acc, err := repo.LoadByUserID(ctx, "test")
	require.NoError(t, err)
	assert.Equal(t, uint32(2000000), acc.AccountID)

	// Returned copy must not alias the stored row.
	acc.UserID = "mutated"
	again, err := repo.LoadByUserID(ctx, "test")
	require.NoError(t, err)
	assert.Equal(t, "test", again.UserID, "LoadByUserID must return a copy, not the stored row")

	_, err = repo.LoadByUserID(ctx, "ghost")
	assert.ErrorIs(t, err, domain.ErrAccountNotFound)

	require.NoError(t, repo.UpdateLoginInfo(ctx, 2000000, "10.0.0.1", baseTime))
	acc, err = repo.LoadByUserID(ctx, "test")
	require.NoError(t, err)
	assert.Equal(t, uint32(1), acc.LoginCount)
	assert.Equal(t, "10.0.0.1", acc.LastIP)
	assert.Equal(t, baseTime, acc.LastLogin)

	assert.ErrorIs(t, repo.UpdateLoginInfo(ctx, 999, "x", baseTime), domain.ErrAccountNotFound)
}

func TestMemorySessionStore_RoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := infra.NewMemorySessionStore()

	_, err := store.Get(ctx, 5)
	assert.ErrorIs(t, err, domain.ErrSessionNotFound)

	sess := &domain.Session{AccountID: 5, LoginID1: 1, LoginID2: 2, Sex: domain.SexFemale}
	require.NoError(t, store.Put(ctx, sess))

	got, err := store.Get(ctx, 5)
	require.NoError(t, err)
	assert.Equal(t, sess.LoginID1, got.LoginID1)
	assert.Equal(t, sess.LoginID2, got.LoginID2)
	assert.Equal(t, domain.SexFemale, got.Sex)

	// Put overwrites; Delete removes.
	sess.LoginID1 = 99
	require.NoError(t, store.Put(ctx, sess))
	got, _ = store.Get(ctx, 5)
	assert.Equal(t, uint32(99), got.LoginID1)

	require.NoError(t, store.Delete(ctx, 5))
	_, err = store.Get(ctx, 5)
	assert.ErrorIs(t, err, domain.ErrSessionNotFound)

	// Delete of a missing key is idempotent.
	assert.NoError(t, store.Delete(ctx, 5))
}
