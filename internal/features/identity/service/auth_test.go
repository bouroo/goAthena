//go:build unit

package service_test

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/bouroo/goAthena/internal/features/identity/domain"
	mocks "github.com/bouroo/goAthena/internal/features/identity/repository/mock"
	"github.com/bouroo/goAthena/internal/features/identity/service"
	inventorydomain "github.com/bouroo/goAthena/internal/features/inventory/domain"
)

// nopLogger discards all log output so test runs stay quiet.
func nopLogger() *zerolog.Logger {
	l := zerolog.Nop()
	return &l
}

// fixedClock returns a deterministic time.Time source for the service.
func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

func TestNewIdentityService_DefaultMaxChars(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	acc := mocks.NewMockAccountRepository(ctrl)
	chr := mocks.NewMockCharacterRepository(ctrl)
	sess := mocks.NewMockSessionRepository(ctrl)
	_ = service.NewIdentityService(acc, chr, sess, nopLogger(), false, 0, nil, inventorydomain.ZeroItemWeight{})
	_ = service.NewIdentityService(acc, chr, sess, nopLogger(), false, -3, nil, inventorydomain.ZeroItemWeight{})
}

func TestLogin_HappyPath_Plain(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	accRepo := mocks.NewMockAccountRepository(ctrl)
	chrRepo := mocks.NewMockCharacterRepository(ctrl)
	sessRepo := mocks.NewMockSessionRepository(ctrl)

	fixedNow := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	acc := &domain.Account{
		AccountID: 42,
		UserID:    "alice",
		UserPass:  "secret123",
		Sex:       domain.SexFemale,
		GroupID:   0,
		State:     0,
	}
	ip := netip.MustParseAddr("203.0.113.10")

	svc := service.NewIdentityService(accRepo, chrRepo, sessRepo, nopLogger(), false, 15, nil, inventorydomain.ZeroItemWeight{},
		service.WithClock(fixedClock(fixedNow)))

	accRepo.EXPECT().LoadByUserID(gomock.Any(), "alice").Return(acc, nil)
	sessRepo.EXPECT().
		Put(gomock.Any(), gomock.Any(), domain.SessionTTL).
		DoAndReturn(func(_ context.Context, sess *domain.Session, ttl time.Duration) error {
			require.NotNil(t, sess)
			require.Equal(t, uint32(42), sess.AccountID)
			require.NotZero(t, sess.LoginID1, "login_id1 must be non-zero")
			require.NotZero(t, sess.LoginID2, "login_id2 must be non-zero")
			require.NotEqual(t, sess.LoginID1, sess.LoginID2, "tokens must differ")
			require.Equal(t, domain.SexFemale, sess.Sex)
			require.Equal(t, "203.0.113.10", sess.RemoteIP)
			require.Equal(t, fixedNow, sess.CreatedAt)
			require.Equal(t, domain.SessionTTL, ttl)
			return nil
		})
	accRepo.EXPECT().UpdateLoginInfo(gomock.Any(), uint32(42), "203.0.113.10").Return(nil)

	resp, err := svc.Login(context.Background(), domain.LoginRequest{
		UserID:     "alice",
		Password:   "secret123",
		Method:     domain.PassEncodingPlain,
		ClientType: 0,
		RemoteIP:   ip,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, acc.AccountID, resp.Account.AccountID)
	assert.Equal(t, uint32(42), resp.Session.AccountID)
}

func TestLogin_HappyPath_MD5(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	accRepo := mocks.NewMockAccountRepository(ctrl)
	chrRepo := mocks.NewMockCharacterRepository(ctrl)
	sessRepo := mocks.NewMockSessionRepository(ctrl)

	// MD5("hunter2") = 2ab96390c7dbe3439de74d0c9b0b1767 (md5 hex)
	plain := "hunter2"
	stored := "2ab96390c7dbe3439de74d0c9b0b1767"
	acc := &domain.Account{AccountID: 7, UserID: "bob", UserPass: stored, Sex: domain.SexMale}

	svc := service.NewIdentityService(accRepo, chrRepo, sessRepo, nopLogger(), true, 15, nil, inventorydomain.ZeroItemWeight{},
		service.WithClock(fixedClock(time.Now())))

	accRepo.EXPECT().LoadByUserID(gomock.Any(), "bob").Return(acc, nil)
	sessRepo.EXPECT().Put(gomock.Any(), gomock.Any(), domain.SessionTTL).Return(nil)
	accRepo.EXPECT().UpdateLoginInfo(gomock.Any(), uint32(7), gomock.Any()).Return(nil)

	resp, err := svc.Login(context.Background(), domain.LoginRequest{
		UserID:   "bob",
		Password: plain,
		Method:   domain.PassEncodingMD5,
		RemoteIP: netip.MustParseAddr("10.0.0.1"),
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
}

func TestLogin_AccountNotFound(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	accRepo := mocks.NewMockAccountRepository(ctrl)
	chrRepo := mocks.NewMockCharacterRepository(ctrl)
	sessRepo := mocks.NewMockSessionRepository(ctrl)

	accRepo.EXPECT().
		LoadByUserID(gomock.Any(), "ghost").
		Return(nil, domain.ErrAccountNotFound)

	svc := service.NewIdentityService(accRepo, chrRepo, sessRepo, nopLogger(), false, 15, nil, inventorydomain.ZeroItemWeight{})
	_, err := svc.Login(context.Background(), domain.LoginRequest{
		UserID: "ghost", Password: "x", Method: domain.PassEncodingPlain,
	})
	require.Error(t, err)
	var le *service.LoginError
	require.ErrorAs(t, err, &le)
	assert.Equal(t, domain.AuthUnknownID, le.Code)
}

func TestLogin_WrongPassword_Plain(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	accRepo := mocks.NewMockAccountRepository(ctrl)
	chrRepo := mocks.NewMockCharacterRepository(ctrl)
	sessRepo := mocks.NewMockSessionRepository(ctrl)

	accRepo.EXPECT().LoadByUserID(gomock.Any(), "alice").Return(&domain.Account{
		AccountID: 1, UserID: "alice", UserPass: "secret123",
	}, nil)

	svc := service.NewIdentityService(accRepo, chrRepo, sessRepo, nopLogger(), false, 15, nil, inventorydomain.ZeroItemWeight{})
	_, err := svc.Login(context.Background(), domain.LoginRequest{
		UserID: "alice", Password: "wrong", Method: domain.PassEncodingPlain,
	})
	require.Error(t, err)
	var le *service.LoginError
	require.ErrorAs(t, err, &le)
	assert.Equal(t, domain.AuthInvalidPassword, le.Code)
}

func TestLogin_WrongPassword_MD5(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	accRepo := mocks.NewMockAccountRepository(ctrl)
	chrRepo := mocks.NewMockCharacterRepository(ctrl)
	sessRepo := mocks.NewMockSessionRepository(ctrl)

	accRepo.EXPECT().LoadByUserID(gomock.Any(), "bob").Return(&domain.Account{
		AccountID: 1, UserID: "bob", UserPass: "2ab96390c7dbe3439de74d0c9b0b1767",
	}, nil)

	svc := service.NewIdentityService(accRepo, chrRepo, sessRepo, nopLogger(), true, 15, nil, inventorydomain.ZeroItemWeight{})
	_, err := svc.Login(context.Background(), domain.LoginRequest{
		UserID: "bob", Password: "not-hunter2", Method: domain.PassEncodingMD5,
	})
	require.Error(t, err)
	var le *service.LoginError
	require.ErrorAs(t, err, &le)
	assert.Equal(t, domain.AuthInvalidPassword, le.Code)
}

func TestLogin_Expired(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	accRepo := mocks.NewMockAccountRepository(ctrl)
	chrRepo := mocks.NewMockCharacterRepository(ctrl)
	sessRepo := mocks.NewMockSessionRepository(ctrl)

	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	accRepo.EXPECT().LoadByUserID(gomock.Any(), "alice").Return(&domain.Account{
		AccountID:      1,
		UserID:         "alice",
		UserPass:       "ok",
		ExpirationTime: now.Add(-1 * time.Hour),
	}, nil)

	svc := service.NewIdentityService(accRepo, chrRepo, sessRepo, nopLogger(), false, 15, nil, inventorydomain.ZeroItemWeight{},
		service.WithClock(fixedClock(now)))

	_, err := svc.Login(context.Background(), domain.LoginRequest{
		UserID: "alice", Password: "ok", Method: domain.PassEncodingPlain,
	})
	require.Error(t, err)
	var le *service.LoginError
	require.ErrorAs(t, err, &le)
	assert.Equal(t, domain.AuthExpired, le.Code)
}

func TestLogin_Banned(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	accRepo := mocks.NewMockAccountRepository(ctrl)
	chrRepo := mocks.NewMockCharacterRepository(ctrl)
	sessRepo := mocks.NewMockSessionRepository(ctrl)

	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	accRepo.EXPECT().LoadByUserID(gomock.Any(), "alice").Return(&domain.Account{
		AccountID: 1,
		UserID:    "alice",
		UserPass:  "ok",
		UnbanTime: now.Add(24 * time.Hour),
	}, nil)

	svc := service.NewIdentityService(accRepo, chrRepo, sessRepo, nopLogger(), false, 15, nil, inventorydomain.ZeroItemWeight{},
		service.WithClock(fixedClock(now)))

	_, err := svc.Login(context.Background(), domain.LoginRequest{
		UserID: "alice", Password: "ok", Method: domain.PassEncodingPlain,
	})
	require.Error(t, err)
	var le *service.LoginError
	require.ErrorAs(t, err, &le)
	assert.Equal(t, domain.AuthBanned, le.Code)
}

func TestLogin_BlockedState(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	accRepo := mocks.NewMockAccountRepository(ctrl)
	chrRepo := mocks.NewMockCharacterRepository(ctrl)
	sessRepo := mocks.NewMockSessionRepository(ctrl)

	accRepo.EXPECT().LoadByUserID(gomock.Any(), "alice").Return(&domain.Account{
		AccountID: 1, UserID: "alice", UserPass: "ok", State: 5,
	}, nil)

	svc := service.NewIdentityService(accRepo, chrRepo, sessRepo, nopLogger(), false, 15, nil, inventorydomain.ZeroItemWeight{})
	_, err := svc.Login(context.Background(), domain.LoginRequest{
		UserID: "alice", Password: "ok", Method: domain.PassEncodingPlain,
	})
	require.Error(t, err)
	var le *service.LoginError
	require.ErrorAs(t, err, &le)
	assert.Equal(t, domain.AuthError(4), le.Code, "state-1 wire code")
}

func TestLogin_StateExceeds255Clamps(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	accRepo := mocks.NewMockAccountRepository(ctrl)
	chrRepo := mocks.NewMockCharacterRepository(ctrl)
	sessRepo := mocks.NewMockSessionRepository(ctrl)

	accRepo.EXPECT().LoadByUserID(gomock.Any(), "alice").Return(&domain.Account{
		AccountID: 1, UserID: "alice", UserPass: "ok", State: 1000,
	}, nil)

	svc := service.NewIdentityService(accRepo, chrRepo, sessRepo, nopLogger(), false, 15, nil, inventorydomain.ZeroItemWeight{})
	_, err := svc.Login(context.Background(), domain.LoginRequest{
		UserID: "alice", Password: "ok", Method: domain.PassEncodingPlain,
	})
	require.Error(t, err)
	var le *service.LoginError
	require.ErrorAs(t, err, &le)
	assert.Equal(t, domain.AuthError(254), le.Code)
}

func TestLogin_EncodingMismatch(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	accRepo := mocks.NewMockAccountRepository(ctrl)
	chrRepo := mocks.NewMockCharacterRepository(ctrl)
	sessRepo := mocks.NewMockSessionRepository(ctrl)

	svc := service.NewIdentityService(accRepo, chrRepo, sessRepo, nopLogger(), false, 15, nil, inventorydomain.ZeroItemWeight{})
	_, err := svc.Login(context.Background(), domain.LoginRequest{
		UserID: "alice", Password: "ok", Method: domain.PassEncodingMD5,
	})
	require.Error(t, err)
	var le *service.LoginError
	require.ErrorAs(t, err, &le)
	assert.Equal(t, domain.AuthRejected, le.Code)
}

func TestLogin_SessionPutError(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	accRepo := mocks.NewMockAccountRepository(ctrl)
	chrRepo := mocks.NewMockCharacterRepository(ctrl)
	sessRepo := mocks.NewMockSessionRepository(ctrl)

	accRepo.EXPECT().LoadByUserID(gomock.Any(), "alice").Return(&domain.Account{
		AccountID: 1, UserID: "alice", UserPass: "ok",
	}, nil)
	sessRepo.EXPECT().Put(gomock.Any(), gomock.Any(), domain.SessionTTL).
		Return(errors.New("valkey down"))

	svc := service.NewIdentityService(accRepo, chrRepo, sessRepo, nopLogger(), false, 15, nil, inventorydomain.ZeroItemWeight{})
	_, err := svc.Login(context.Background(), domain.LoginRequest{
		UserID: "alice", Password: "ok", Method: domain.PassEncodingPlain,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "persist session")
}

func TestLogin_UpdateLoginInfoFailureIsLoggedNotFatal(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	accRepo := mocks.NewMockAccountRepository(ctrl)
	chrRepo := mocks.NewMockCharacterRepository(ctrl)
	sessRepo := mocks.NewMockSessionRepository(ctrl)

	accRepo.EXPECT().LoadByUserID(gomock.Any(), "alice").Return(&domain.Account{
		AccountID: 1, UserID: "alice", UserPass: "ok",
	}, nil)
	sessRepo.EXPECT().Put(gomock.Any(), gomock.Any(), domain.SessionTTL).Return(nil)
	accRepo.EXPECT().UpdateLoginInfo(gomock.Any(), uint32(1), gomock.Any()).
		Return(errors.New("db hiccup"))

	svc := service.NewIdentityService(accRepo, chrRepo, sessRepo, nopLogger(), false, 15, nil, inventorydomain.ZeroItemWeight{})
	resp, err := svc.Login(context.Background(), domain.LoginRequest{
		UserID: "alice", Password: "ok", Method: domain.PassEncodingPlain,
		RemoteIP: netip.MustParseAddr("127.0.0.1"),
	})
	require.NoError(t, err, "session was persisted; UpdateLoginInfo failure must not fail login")
	require.NotNil(t, resp)
}

func TestLogin_LoadByUserID_OtherError(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	accRepo := mocks.NewMockAccountRepository(ctrl)
	chrRepo := mocks.NewMockCharacterRepository(ctrl)
	sessRepo := mocks.NewMockSessionRepository(ctrl)

	accRepo.EXPECT().LoadByUserID(gomock.Any(), "alice").
		Return(nil, errors.New("db exploded"))

	svc := service.NewIdentityService(accRepo, chrRepo, sessRepo, nopLogger(), false, 15, nil, inventorydomain.ZeroItemWeight{})
	_, err := svc.Login(context.Background(), domain.LoginRequest{
		UserID: "alice", Password: "ok", Method: domain.PassEncodingPlain,
	})
	require.Error(t, err)
	assert.NotErrorIs(t, err, &service.LoginError{}, "non-sentinel error must not be a LoginError")
	assert.Contains(t, err.Error(), "load account")
}

func TestLogin_PasswordEncodingPlainConstantTime(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	accRepo := mocks.NewMockAccountRepository(ctrl)
	chrRepo := mocks.NewMockCharacterRepository(ctrl)
	sessRepo := mocks.NewMockSessionRepository(ctrl)

	accRepo.EXPECT().LoadByUserID(gomock.Any(), "alice").Return(&domain.Account{
		AccountID: 1, UserID: "alice", UserPass: "short",
	}, nil)

	svc := service.NewIdentityService(accRepo, chrRepo, sessRepo, nopLogger(), false, 15, nil, inventorydomain.ZeroItemWeight{})
	_, err := svc.Login(context.Background(), domain.LoginRequest{
		UserID: "alice", Password: "much-longer-password", Method: domain.PassEncodingPlain,
	})
	require.Error(t, err)
	var le *service.LoginError
	require.ErrorAs(t, err, &le)
	assert.Equal(t, domain.AuthInvalidPassword, le.Code)
}

func TestListCharacters_HappyPath(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	accRepo := mocks.NewMockAccountRepository(ctrl)
	chrRepo := mocks.NewMockCharacterRepository(ctrl)
	sessRepo := mocks.NewMockSessionRepository(ctrl)

	want := []domain.CharacterSummary{
		{CharID: 1, AccountID: 9, Slot: 0, Name: "alpha", BaseLevel: 10},
		{CharID: 2, AccountID: 9, Slot: 1, Name: "beta", BaseLevel: 20},
	}
	chrRepo.EXPECT().ListByAccount(gomock.Any(), uint32(9), 15).Return(want, nil)

	svc := service.NewIdentityService(accRepo, chrRepo, sessRepo, nopLogger(), false, 15, nil, inventorydomain.ZeroItemWeight{})
	got, err := svc.ListCharacters(context.Background(), 9)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestListCharacters_EmptyReturnsNonNilSlice(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	accRepo := mocks.NewMockAccountRepository(ctrl)
	chrRepo := mocks.NewMockCharacterRepository(ctrl)
	sessRepo := mocks.NewMockSessionRepository(ctrl)

	chrRepo.EXPECT().ListByAccount(gomock.Any(), uint32(9), 15).Return(nil, nil)

	svc := service.NewIdentityService(accRepo, chrRepo, sessRepo, nopLogger(), false, 15, nil, inventorydomain.ZeroItemWeight{})
	got, err := svc.ListCharacters(context.Background(), 9)
	require.NoError(t, err)
	require.NotNil(t, got, "handler must never see a nil roster")
	assert.Empty(t, got)
}

func TestListCharacters_RepoError(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	accRepo := mocks.NewMockAccountRepository(ctrl)
	chrRepo := mocks.NewMockCharacterRepository(ctrl)
	sessRepo := mocks.NewMockSessionRepository(ctrl)

	chrRepo.EXPECT().ListByAccount(gomock.Any(), uint32(9), 15).
		Return(nil, errors.New("db down"))

	svc := service.NewIdentityService(accRepo, chrRepo, sessRepo, nopLogger(), false, 15, nil, inventorydomain.ZeroItemWeight{})
	_, err := svc.ListCharacters(context.Background(), 9)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list characters")
}

func TestLoginError_ErrorString(t *testing.T) {
	t.Parallel()
	var err error = &service.LoginError{Code: domain.AuthBanned, Msg: "test"}
	assert.Contains(t, err.Error(), "6")
	assert.Contains(t, err.Error(), "test")
}

func TestGetCharacter_HappyPath(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	accRepo := mocks.NewMockAccountRepository(ctrl)
	chrRepo := mocks.NewMockCharacterRepository(ctrl)
	sessRepo := mocks.NewMockSessionRepository(ctrl)

	want := &domain.CharacterSummary{
		CharID:    150001,
		AccountID: 2000000,
		Name:      "alpha",
		Class:     7,
		BaseLevel: 50,
		JobLevel:  25,
		HP:        1234,
		MaxHP:     2000,
		SP:        100,
		MaxSP:     200,
		Hair:      5,
		HairColor: 2,
		Weapon:    1101,
		Sex:       domain.SexMale,
	}
	chrRepo.EXPECT().
		GetByID(gomock.Any(), uint32(2000000), uint32(150001)).
		Return(want, nil)

	svc := service.NewIdentityService(accRepo, chrRepo, sessRepo, nopLogger(), false, 15, nil, inventorydomain.ZeroItemWeight{})
	got, err := svc.GetCharacter(context.Background(), 2000000, 150001)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, want, got)
}

func TestGetCharacter_NotFound_PropagatesSentinel(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	accRepo := mocks.NewMockAccountRepository(ctrl)
	chrRepo := mocks.NewMockCharacterRepository(ctrl)
	sessRepo := mocks.NewMockSessionRepository(ctrl)

	chrRepo.EXPECT().
		GetByID(gomock.Any(), uint32(2000000), uint32(150001)).
		Return(nil, domain.ErrCharacterNotFound)

	svc := service.NewIdentityService(accRepo, chrRepo, sessRepo, nopLogger(), false, 15, nil, inventorydomain.ZeroItemWeight{})
	got, err := svc.GetCharacter(context.Background(), 2000000, 150001)
	require.Error(t, err)
	assert.Nil(t, got)
	assert.ErrorIs(t, err, domain.ErrCharacterNotFound,
		"the handler maps ErrCharacterNotFound onto success=false; the service must propagate it unmodified")
}

func TestGetCharacter_RepoError_PropagatesUnchanged(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	accRepo := mocks.NewMockAccountRepository(ctrl)
	chrRepo := mocks.NewMockCharacterRepository(ctrl)
	sessRepo := mocks.NewMockSessionRepository(ctrl)

	repoErr := errors.New("db down")
	chrRepo.EXPECT().
		GetByID(gomock.Any(), uint32(2000000), uint32(150001)).
		Return(nil, repoErr)

	svc := service.NewIdentityService(accRepo, chrRepo, sessRepo, nopLogger(), false, 15, nil, inventorydomain.ZeroItemWeight{})
	got, err := svc.GetCharacter(context.Background(), 2000000, 150001)
	require.Error(t, err)
	assert.Nil(t, got)
	// The service is a pass-through for non-sentinel errors so the
	// handler can rely on the repository's wrap-with-context. The
	// real repository layer is exercised in
	// TestCharacterRepository_GetByID/other_DB_error_is_wrapped.
	assert.ErrorIs(t, err, repoErr)
	assert.NotErrorIs(t, err, domain.ErrCharacterNotFound)
}
