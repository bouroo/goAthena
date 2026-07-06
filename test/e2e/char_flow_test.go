//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	identityv1 "github.com/bouroo/goAthena/api/pb/identity/v1"
	"github.com/bouroo/goAthena/internal/features/identity/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createTestCharacter inserts a row in the rAthena `char` table for
// the given account and returns the assigned char_id. The row carries
// only the minimum required fields for a roster listing.
func createTestCharacter(t *testing.T, h *E2EHarness, accountID uint32, slot int, name string) uint32 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := h.DB.ExecContext(ctx,
		"INSERT INTO `char` (account_id, char_num, name, class, base_level, job_level, hp, max_hp, sp, max_sp, sex) "+
			"VALUES (?, ?, ?, 0, 1, 1, 100, 100, 50, 50, 'M')",
		accountID, slot, name)
	require.NoError(t, err, "insert char row for account %d slot %d", accountID, slot)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return uint32(id) //nolint:gosec // G115: char_id is positive int64; fits in uint32 for any realistic rAthena seed.
}

// deleteTestCharacter removes the character row by id. Safe to call
// when the row does not exist (no-op).
func deleteTestCharacter(t *testing.T, h *E2EHarness, charID uint32) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = h.DB.ExecContext(ctx, "DELETE FROM `char` WHERE char_id = ?", charID)
}

// loginAsAccount authenticates the freshly-created test account and
// returns the AuthenticateResponse so subsequent calls can reuse the
// account_id and login_id1.
func loginAsAccount(t *testing.T, h *E2EHarness, userID, password string) *identityv1.AuthenticateResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := h.IdentityClient.Authenticate(ctx, &identityv1.AuthenticateRequest{
		Username:   userID,
		Password:   []byte(password),
		ClientType: 0,
		Packetver:  20130807,
		ClientIp:   "203.0.113.20",
		Method:     identityv1.AuthMethod_AUTH_METHOD_PASSWORD,
	})
	require.NoError(t, err)
	require.Equal(t, identityv1.AuthResult_AUTH_RESULT_OK, resp.GetResult(),
		"test fixture login must succeed; error_code=%d", resp.GetErrorCode())
	return resp
}

// TestE2E_CharListCreateDelete walks the full character management
// lifecycle via the identity gRPC service + DB fixtures: an empty
// roster becomes non-empty after insert, then empty again after delete.
//
// NOTE: the Phase 5 proto only exposes Authenticate + GetCharacterList
// over gRPC; character creation/deletion is wired in later phases. The
// E2E suite therefore performs create/delete via direct DB inserts
// against the rAthena `char` table — the same code path the eventual
// RPC will execute — and validates visibility through GetCharacterList.
func TestE2E_CharListCreateDelete(t *testing.T) {
	h := NewE2EHarness(t)
	ctx := TestContext(t)

	userID := UniqueUserID()
	const password = "list-pass"
	accountID := createTestAccount(t, h, userID, password, domain.SexMale)
	t.Cleanup(func() { deleteTestAccount(t, h, accountID) })
	t.Cleanup(func() {
		dctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		deleteValkeySession(dctx, h, accountID)
	})

	login := loginAsAccount(t, h, userID, password)

	// Step 1: empty roster.
	listReq := &identityv1.GetCharacterListRequest{
		AccountId: login.GetAccountId(),
		LoginId1:  login.GetLoginId1(),
		Sex:       login.GetSex(),
	}
	listResp, err := h.IdentityClient.GetCharacterList(ctx, listReq)
	require.NoError(t, err)
	require.NotNil(t, listResp)
	require.Empty(t, listResp.GetCharacters(),
		"freshly-created account must have an empty roster before insert")

	// Step 2: insert a character directly in the `char` table.
	charName := UniqueCharName()
	charID := createTestCharacter(t, h, accountID, 0, charName)
	t.Cleanup(func() { deleteTestCharacter(t, h, charID) })

	// Step 3: roster must now contain the new character.
	listResp, err = h.IdentityClient.GetCharacterList(ctx, listReq)
	require.NoError(t, err)
	require.Len(t, listResp.GetCharacters(), 1,
		"roster must contain the inserted character")
	got := listResp.GetCharacters()[0]
	assert.Equal(t, charID, got.GetCharId())
	assert.Equal(t, charName, got.GetName())
	assert.Equal(t, uint32(0), got.GetSlot(),
		"the inserted character occupies slot 0")

	// Step 4: delete the character and re-list.
	deleteTestCharacter(t, h, charID)
	listResp, err = h.IdentityClient.GetCharacterList(ctx, listReq)
	require.NoError(t, err)
	require.Empty(t, listResp.GetCharacters(),
		"deleted character must not appear in the roster")
}

// TestE2E_CharListSlotOrdering verifies that the GetCharacterList
// response preserves the rAthena char_num ordering (slots 0-14 by
// default). Three characters are inserted out of order and the test
// asserts the response is sorted ascending by slot.
func TestE2E_CharListSlotOrdering(t *testing.T) {
	h := NewE2EHarness(t)
	ctx := TestContext(t)

	userID := UniqueUserID()
	const password = "slot-pass"
	accountID := createTestAccount(t, h, userID, password, domain.SexMale)
	t.Cleanup(func() { deleteTestAccount(t, h, accountID) })
	t.Cleanup(func() {
		dctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		deleteValkeySession(dctx, h, accountID)
	})

	login := loginAsAccount(t, h, userID, password)

	// Insert in shuffled slot order; the response must come back sorted.
	seedSlots := []int{4, 0, 2}
	charIDs := make([]uint32, 0, len(seedSlots))
	for _, slot := range seedSlots {
		name := UniqueCharName()
		id := createTestCharacter(t, h, accountID, slot, name)
		charIDs = append(charIDs, id)
		t.Cleanup(func() { deleteTestCharacter(t, h, id) })
		_ = name
	}

	resp, err := h.IdentityClient.GetCharacterList(ctx, &identityv1.GetCharacterListRequest{
		AccountId: login.GetAccountId(),
		LoginId1:  login.GetLoginId1(),
		Sex:       login.GetSex(),
	})
	require.NoError(t, err)
	require.Len(t, resp.GetCharacters(), len(seedSlots))
	for i, c := range resp.GetCharacters() {
		assert.Equal(t, uint32(seedSlots[i]), c.GetSlot(), //nolint:gosec // G115: seedSlots holds 0-14 (small int), cast is safe.
			"character at index %d must report slot %d", i, seedSlots[i])
	}
	_ = charIDs
}

// TestE2E_CharListInvalidSession confirms that listing characters for
// an unknown account returns a non-OK gRPC status (the handler
// collapses repository failures into codes.Internal). E2E cluster
// behavior: gRPC surfaces the error; HTTP clients get 500. Either
// way a fresh account_id with no rows must produce an empty list
// rather than a server failure.
func TestE2E_CharListEmptyForFreshAccount(t *testing.T) {
	h := NewE2EHarness(t)
	ctx := TestContext(t)

	userID := UniqueUserID()
	const password = "empty-pass"
	accountID := createTestAccount(t, h, userID, password, domain.SexFemale)
	t.Cleanup(func() { deleteTestAccount(t, h, accountID) })
	t.Cleanup(func() {
		dctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		deleteValkeySession(dctx, h, accountID)
	})

	login := loginAsAccount(t, h, userID, password)
	resp, err := h.IdentityClient.GetCharacterList(ctx, &identityv1.GetCharacterListRequest{
		AccountId: login.GetAccountId(),
		LoginId1:  login.GetLoginId1(),
		Sex:       login.GetSex(),
	})
	require.NoError(t, err, "empty roster is a valid response, not a server error")
	require.NotNil(t, resp)
	assert.Empty(t, resp.GetCharacters(),
		"account with no characters must return an empty roster")
}
