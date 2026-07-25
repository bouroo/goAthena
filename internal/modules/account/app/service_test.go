//go:build unit

package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bouroo/goAthena/internal/modules/account/app"
	"github.com/bouroo/goAthena/internal/modules/account/domain"
	"github.com/bouroo/goAthena/internal/modules/account/infra"
)

// stubClock returns a fixed instant so the expiration/ban checks are deterministic.
type stubClock struct{ t time.Time }

func (s stubClock) Now() time.Time { return s.t }

// errRepo wraps the in-memory repo to inject an arbitrary error from LoadByUserID.
type errRepo struct {
	domain.AccountRepository
	err error
}

func (r *errRepo) LoadByUserID(_ context.Context, _ string) (*domain.Account, error) {
	return nil, r.err
}

var baseTime = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

// maleAccount is a valid, login-able account (sex 'M', no state/ban/expiry).
func maleAccount() *domain.Account {
	return &domain.Account{
		AccountID: 2000000, UserID: "test", UserPass: "test", Sex: domain.SexMale,
	}
}

// newSvc builds an AuthService over fresh in-memory stores with a fixed clock
// and a deterministic minter (1 then 2, so id1/id2 are distinguishable).
func newSvc(t *testing.T, accs ...*domain.Account) (*app.AuthService, *infra.MemoryAccountRepository, *infra.MemorySessionStore) {
	t.Helper()
	repo := infra.NewMemoryAccountRepository(accs...)
	store := infra.NewMemorySessionStore()
	var n uint32
	mint := func() uint32 { n++; return n }
	return app.NewAuthService(repo, store, stubClock{baseTime}, mint), repo, store
}

func TestAuthService_Login_Success(t *testing.T) {
	t.Parallel()
	svc, repo, store := newSvc(t, maleAccount())

	res, err := svc.Login(context.Background(), domain.LoginRequest{
		UserID: "test", Password: "test", IP: "10.0.0.1",
	})
	require.NoError(t, err)
	require.True(t, res.Accepted)
	assert.Equal(t, uint32(2000000), res.Account.AccountID)
	assert.Equal(t, uint32(1), res.LoginID1) // first mint call
	assert.Equal(t, uint32(2), res.LoginID2) // second mint call

	// Session persisted with the minted tokens + sex.
	sess, err := store.Get(context.Background(), 2000000)
	require.NoError(t, err)
	assert.Equal(t, res.LoginID1, sess.LoginID1)
	assert.Equal(t, res.LoginID2, sess.LoginID2)
	assert.Equal(t, domain.SexMale, sess.Sex)

	// Login recorded: logincount incremented, last_ip set.
	acc, err := repo.LoadByUserID(context.Background(), "test")
	require.NoError(t, err)
	assert.Equal(t, uint32(1), acc.LoginCount)
	assert.Equal(t, "10.0.0.1", acc.LastIP)
	assert.Equal(t, baseTime, acc.LastLogin)
}

func TestAuthService_Login_Refusals(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		acc  *domain.Account
		user string // UserID in the login request — independent of acc (nil for the no-row case)
		pass string
		want domain.AuthCode
	}{
		{"unregistered", nil, "ghost", "x", domain.AuthUnregistered},
		{"server account", &domain.Account{AccountID: 1, UserID: "s1", UserPass: "p1", Sex: domain.SexServer}, "s1", "p1", domain.AuthUnregistered},
		{"wrong password", maleAccount(), "test", "wrong", domain.AuthInvalidPassword},
		{"expired", &domain.Account{AccountID: 9, UserID: "e", UserPass: "e", Sex: domain.SexMale, ExpirationTime: baseTime.Unix() - 3600}, "e", "e", domain.AuthExpired},
		{"banned", &domain.Account{AccountID: 8, UserID: "b", UserPass: "b", Sex: domain.SexMale, UnbanTime: baseTime.Unix() + 3600}, "b", "b", domain.AuthBanned},
		{"state=7 -> code 6", &domain.Account{AccountID: 7, UserID: "st", UserPass: "st", Sex: domain.SexMale, State: 7}, "st", "st", domain.AuthCode(6)},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var accs []*domain.Account
			if tc.acc != nil {
				accs = append(accs, tc.acc)
			}
			svc, _, store := newSvc(t, accs...)
			res, err := svc.Login(context.Background(), domain.LoginRequest{
				UserID: tc.user, Password: tc.pass, IP: "10.0.0.1",
			})
			require.NoError(t, err, "an expected refusal must not be an error")
			assert.False(t, res.Accepted)
			assert.Equal(t, tc.want, res.Code)
			// No session granted on refusal.
			if tc.acc != nil {
				_, err := store.Get(context.Background(), tc.acc.AccountID)
				assert.ErrorIs(t, err, domain.ErrSessionNotFound)
			}
		})
	}
}

func TestAuthService_Login_RepoError(t *testing.T) {
	t.Parallel()
	injected := errors.New("db down")
	svc := app.NewAuthService(&errRepo{err: injected}, infra.NewMemorySessionStore(), stubClock{baseTime}, func() uint32 { return 1 })

	res, err := svc.Login(context.Background(), domain.LoginRequest{UserID: "test", Password: "test"})
	require.Error(t, err)
	assert.False(t, res.Accepted)
	assert.ErrorIs(t, err, injected) // infra fault propagates, not swallowed
}

// TestAuthService_Login_DefaultMinterNonZero proves the production crypto minter
// (wired when mint is nil) never returns 0, honoring rAthena's [1, MAX] range.
func TestAuthService_Login_DefaultMinterNonZero(t *testing.T) {
	t.Parallel()
	svc := app.NewAuthService(
		infra.NewMemoryAccountRepository(maleAccount()), infra.NewMemorySessionStore(),
		stubClock{baseTime}, nil, // nil -> cryptoMinter
	)
	for i := 0; i < 20; i++ {
		res, err := svc.Login(context.Background(), domain.LoginRequest{UserID: "test", Password: "test"})
		require.NoError(t, err)
		require.NotZero(t, res.LoginID1)
		require.NotZero(t, res.LoginID2)
	}
}
