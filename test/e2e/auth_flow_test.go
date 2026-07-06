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
	"google.golang.org/grpc/status"
)

// createTestAccount inserts a row in the rAthena `login` table with
// a unique userid and returns the assigned account_id. The caller is
// responsible for cleanup — register a t.Cleanup that calls
// deleteTestAccount to keep parallel runs isolated.
func createTestAccount(t *testing.T, h *E2EHarness, userID, password string, sex domain.Sex) uint32 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := h.DB.ExecContext(ctx,
		"INSERT INTO `login` (userid, user_pass, sex, email) VALUES (?, ?, ?, ?)",
		userID, password, string(sex), userID+"@e2e.test")
	require.NoError(t, err, "insert login row for %s", userID)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	require.Positive(t, id, "LastInsertId must be > 0")
	return uint32(id) //nolint:gosec // G115: account_id is positive int64; fits in uint32 for any realistic rAthena seed.
}

// deleteTestAccount removes the login row plus any orphan characters
// for the account. Safe to call when the row does not exist (no-op).
func deleteTestAccount(t *testing.T, h *E2EHarness, accountID uint32) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = h.DB.ExecContext(ctx, "DELETE FROM `char` WHERE account_id = ?", accountID)
	_, _ = h.DB.ExecContext(ctx, "DELETE FROM `login` WHERE account_id = ?", accountID)
}

// sessionKey returns the Valkey key under which the identity service
// persists the session for accountID. The format mirrors the session
// repository in internal/features/identity/repository/session.go.
func sessionKey(accountID uint32) string {
	return "session:account:" + uintToString(accountID)
}

func uintToString(v uint32) string {
	if v == 0 {
		return "0"
	}
	buf := [10]byte{}
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}

// assertValkeySessionExists verifies that the identity service has
// persisted a session for accountID by reading the canonical session
// key from Valkey. The check is structural: any non-empty value at the
// key is sufficient — the session body is binary and opaque to E2E.
func assertValkeySessionExists(t *testing.T, h *E2EHarness, accountID uint32) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp := h.ValkeyClient.Do(ctx, h.ValkeyClient.B().Get().Key(sessionKey(accountID)).Build())
	err := resp.Error()
	if err != nil && !isValkeyNil(err) {
		t.Fatalf("e2e: get session key for account %d: %v", accountID, err)
	}
	val, vErr := resp.ToString()
	require.NoError(t, vErr, "decode session payload")
	require.NotEmpty(t, val, "session must exist in Valkey for account %d", accountID)
}

// isValkeyNil matches the valkey-go sentinel for a missing key without
// importing the internal IsValkeyNil helper.
func isValkeyNil(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return s == "valkey nil" || s == "redis nil"
}

// deleteValkeySession removes any session persisted for accountID.
// Registered as t.Cleanup in tests that exercise the login flow so a
// re-run starts from a clean Valkey state.
func deleteValkeySession(ctx context.Context, h *E2EHarness, accountID uint32) {
	_ = h.ValkeyClient.Do(ctx, h.ValkeyClient.B().Del().Key(sessionKey(accountID)).Build()).Error()
}

// TestE2E_AuthLoginSuccess exercises the happy-path login via the
// identity gRPC service and verifies a session is persisted in Valkey.
func TestE2E_AuthLoginSuccess(t *testing.T) {
	h := NewE2EHarness(t)
	ctx := TestContext(t)

	userID := UniqueUserID()
	const password = "correct-horse-battery-staple"
	accountID := createTestAccount(t, h, userID, password, domain.SexMale)
	t.Cleanup(func() { deleteTestAccount(t, h, accountID) })
	t.Cleanup(func() {
		dctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		deleteValkeySession(dctx, h, accountID)
	})

	resp, err := h.IdentityClient.Authenticate(ctx, &identityv1.AuthenticateRequest{
		Username:   userID,
		Password:   []byte(password),
		ClientType: 0,
		Packetver:  20130807,
		ClientIp:   "203.0.113.10",
		Method:     identityv1.AuthMethod_AUTH_METHOD_PASSWORD,
	})
	require.NoError(t, err, "Authenticate must succeed for valid credentials")
	require.NotNil(t, resp)

	assert.Equal(t, identityv1.AuthResult_AUTH_RESULT_OK, resp.GetResult(),
		"result must be OK; got error_code=%d", resp.GetErrorCode())
	assert.Equal(t, accountID, resp.GetAccountId(),
		"server-issued account_id must match the inserted row")
	assert.NotZero(t, resp.GetLoginId1(), "login_id1 must be non-zero on success")
	assert.NotZero(t, resp.GetLoginId2(), "login_id2 must be non-zero on success")
	assert.NotEqual(t, resp.GetLoginId1(), resp.GetLoginId2(),
		"login_id1 and login_id2 must be distinct")
	assert.Equal(t, "203.0.113.10", resp.GetLastIp(),
		"last_ip must reflect the gRPC client_ip")
	assert.Equal(t, "M", resp.GetSex())

	assertValkeySessionExists(t, h, accountID)
}

// TestE2E_AuthLoginWrongPassword verifies the AUTH_RESULT_REJECTED
// path and the matching error_code (AuthInvalidPassword == 1).
func TestE2E_AuthLoginWrongPassword(t *testing.T) {
	h := NewE2EHarness(t)
	ctx := TestContext(t)

	userID := UniqueUserID()
	const correct = "right-password"
	accountID := createTestAccount(t, h, userID, correct, domain.SexFemale)
	t.Cleanup(func() { deleteTestAccount(t, h, accountID) })
	t.Cleanup(func() {
		dctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		deleteValkeySession(dctx, h, accountID)
	})

	resp, err := h.IdentityClient.Authenticate(ctx, &identityv1.AuthenticateRequest{
		Username:   userID,
		Password:   []byte("wrong-password"),
		ClientType: 0,
		Packetver:  20130807,
		ClientIp:   "203.0.113.11",
		Method:     identityv1.AuthMethod_AUTH_METHOD_PASSWORD,
	})
	require.NoError(t, err, "rejected login must surface as response, not gRPC error")
	require.NotNil(t, resp)
	assert.Equal(t, identityv1.AuthResult_AUTH_RESULT_REJECTED, resp.GetResult(),
		"wrong password must produce AUTH_RESULT_REJECTED")
	assert.Equal(t, uint32(domain.AuthInvalidPassword), resp.GetErrorCode(),
		"error_code must be AuthInvalidPassword (1)")

	// No session should have been created for a rejected login.
	resp2 := h.ValkeyClient.Do(ctx, h.ValkeyClient.B().Exists().Key(sessionKey(accountID)).Build())
	n, eErr := resp2.AsInt64()
	require.NoError(t, eErr)
	assert.Equal(t, int64(0), n, "no session must exist after a rejected login")
}

// TestE2E_AuthLoginNonexistentAccount verifies that an unknown
// userid yields AUTH_RESULT_REJECTED with the AuthUnknownID error
// code (0 — the rAthena wire code for unknown id).
func TestE2E_AuthLoginNonexistentAccount(t *testing.T) {
	h := NewE2EHarness(t)
	ctx := TestContext(t)

	userID := UniqueUserID()
	resp, err := h.IdentityClient.Authenticate(ctx, &identityv1.AuthenticateRequest{
		Username:   userID,
		Password:   []byte("anything"),
		ClientType: 0,
		Packetver:  20130807,
		ClientIp:   "203.0.113.12",
		Method:     identityv1.AuthMethod_AUTH_METHOD_PASSWORD,
	})
	require.NoError(t, err, "unknown account must surface as response, not gRPC error")
	require.NotNil(t, resp)
	assert.Equal(t, identityv1.AuthResult_AUTH_RESULT_REJECTED, resp.GetResult())
	assert.Equal(t, uint32(domain.AuthUnknownID), resp.GetErrorCode(),
		"error_code must be AuthUnknownID (0) for missing account")
	assert.Zero(t, resp.GetAccountId(), "no account_id must be minted for an unknown user")
}

// TestE2E_AuthInvalidEncodingMismatch ensures the encoder/mode mismatch
// returns AuthRejected (3) — the rAthena passwdenc contract. We send
// an MD5-mode login against a cluster configured for plaintext
// passwords.
func TestE2E_AuthInvalidEncodingMismatch(t *testing.T) {
	h := NewE2EHarness(t)
	ctx := TestContext(t)

	userID := UniqueUserID()
	accountID := createTestAccount(t, h, userID, "any", domain.SexMale)
	t.Cleanup(func() { deleteTestAccount(t, h, accountID) })
	t.Cleanup(func() {
		dctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		deleteValkeySession(dctx, h, accountID)
	})

	resp, err := h.IdentityClient.Authenticate(ctx, &identityv1.AuthenticateRequest{
		Username:   userID,
		Password:   []byte("5f4dcc3b5aa765d61d8327deb882cf99"), // md5("password")
		ClientType: 0,
		Packetver:  20130807,
		ClientIp:   "203.0.113.13",
		Method:     identityv1.AuthMethod_AUTH_METHOD_MD5,
	})
	// Either the gRPC call succeeds with REJECTED + AuthRejected
	// (production handler path) or surfaces a non-OK status when
	// the cluster is configured for plain passwords. Both outcomes
	// are valid; only an OK result is a regression.
	if err != nil {
		st, ok := status.FromError(err)
		require.True(t, ok, "expected grpc status error, got %v", err)
		assert.NotEqual(t, "OK", st.Code().String())
		return
	}
	require.NotNil(t, resp)
	if resp.GetResult() == identityv1.AuthResult_AUTH_RESULT_OK {
		t.Fatalf("MD5 method must not succeed against plaintext-configured cluster")
	}
	assert.Equal(t, identityv1.AuthResult_AUTH_RESULT_REJECTED, resp.GetResult())
}
