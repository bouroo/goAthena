//go:build unit

package handler_test

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	identityv1 "github.com/bouroo/goAthena/api/pb/identity/v1"
	"github.com/bouroo/goAthena/internal/features/identity/domain"
	domainmock "github.com/bouroo/goAthena/internal/features/identity/domain/mock"
	"github.com/bouroo/goAthena/internal/features/identity/handler"
	"github.com/bouroo/goAthena/internal/features/identity/service"
)

func TestAuthenticate_HappyPath(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := domainmock.NewMockIdentityService(ctrl)
	h := handler.NewGRPCHandler(svc)

	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	svc.EXPECT().
		Login(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, req domain.LoginRequest) (*domain.LoginResponse, error) {
			assert.Equal(t, "alice", req.UserID)
			assert.Equal(t, "secret123", req.Password)
			assert.Equal(t, uint8(0), req.ClientType)
			assert.Equal(t, domain.PassEncodingPlain, req.Method)
			assert.Equal(t, netip.MustParseAddr("203.0.113.10"), req.RemoteIP)
			return &domain.LoginResponse{
				Account: &domain.Account{
					AccountID:    42,
					UserID:       "alice",
					Sex:          domain.SexFemale,
					LastIP:       "203.0.113.10",
					LastLogin:    now,
					WebAuthToken: "tok-abc",
				},
				Session: &domain.Session{
					AccountID: 42,
					LoginID1:  0xdeadbeef,
					LoginID2:  0xcafef00d,
				},
				CharServers: []domain.CharServerEndpoint{
					{IP: "10.0.0.1", Port: 6121, Name: "Char 1"},
				},
			}, nil
		})

	resp, err := h.Authenticate(context.Background(), &identityv1.AuthenticateRequest{
		Username:   "alice",
		Password:   []byte("secret123"),
		ClientType: 0,
		ClientIp:   "203.0.113.10",
		Method:     identityv1.AuthMethod_AUTH_METHOD_PASSWORD,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, identityv1.AuthResult_AUTH_RESULT_OK, resp.Result)
	assert.Equal(t, uint32(42), resp.AccountId)
	assert.Equal(t, uint64(0xdeadbeef), resp.LoginId1)
	assert.Equal(t, uint64(0xcafef00d), resp.LoginId2)
	assert.Equal(t, "F", resp.Sex)
	assert.Equal(t, "203.0.113.10", resp.LastIp)
	assert.Equal(t, "tok-abc", resp.Token)
	require.Len(t, resp.CharServers, 1)
	assert.Equal(t, "10.0.0.1", resp.CharServers[0].Ip)
	assert.Equal(t, uint32(6121), resp.CharServers[0].Port)
	assert.Equal(t, "Char 1", resp.CharServers[0].Name)
}

func TestAuthenticate_MD5Method_Mapped(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := domainmock.NewMockIdentityService(ctrl)
	h := handler.NewGRPCHandler(svc)

	svc.EXPECT().
		Login(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, req domain.LoginRequest) (*domain.LoginResponse, error) {
			assert.Equal(t, domain.PassEncodingMD5, req.Method,
				"AUTH_METHOD_MD5 must map to PassEncodingMD5")
			return &domain.LoginResponse{
				Account: &domain.Account{AccountID: 1, Sex: domain.SexMale},
				Session: &domain.Session{AccountID: 1, LoginID1: 1, LoginID2: 2},
			}, nil
		})

	resp, err := h.Authenticate(context.Background(), &identityv1.AuthenticateRequest{
		Username: "bob",
		Password: []byte("2ab96390c7dbe3439de74d0c9b0b1767"),
		Method:   identityv1.AuthMethod_AUTH_METHOD_MD5,
	})
	require.NoError(t, err)
	assert.Equal(t, identityv1.AuthResult_AUTH_RESULT_OK, resp.Result)
}

func TestAuthenticate_LoginError_UnknownID(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := domainmock.NewMockIdentityService(ctrl)
	h := handler.NewGRPCHandler(svc)

	svc.EXPECT().
		Login(gomock.Any(), gomock.Any()).
		Return(nil, &service.LoginError{Code: domain.AuthUnknownID, Msg: "ghost"})

	resp, err := h.Authenticate(context.Background(), &identityv1.AuthenticateRequest{
		Username: "ghost", Password: []byte("x"), Method: identityv1.AuthMethod_AUTH_METHOD_PASSWORD,
	})
	require.NoError(t, err)
	assert.Equal(t, identityv1.AuthResult_AUTH_RESULT_REJECTED, resp.Result)
	assert.Equal(t, uint32(domain.AuthUnknownID), resp.ErrorCode)
}

func TestAuthenticate_LoginError_InvalidPassword(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := domainmock.NewMockIdentityService(ctrl)
	h := handler.NewGRPCHandler(svc)

	svc.EXPECT().
		Login(gomock.Any(), gomock.Any()).
		Return(nil, &service.LoginError{Code: domain.AuthInvalidPassword, Msg: "wrong"})

	resp, err := h.Authenticate(context.Background(), &identityv1.AuthenticateRequest{
		Username: "alice", Password: []byte("nope"), Method: identityv1.AuthMethod_AUTH_METHOD_PASSWORD,
	})
	require.NoError(t, err, "wire failures are not gRPC errors")
	require.NotNil(t, resp)
	assert.Equal(t, identityv1.AuthResult_AUTH_RESULT_REJECTED, resp.Result)
	assert.Equal(t, uint32(domain.AuthInvalidPassword), resp.ErrorCode)
	assert.Equal(t, uint32(0), resp.AccountId, "no session token on rejection")
}

func TestAuthenticate_LoginError_Banned(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := domainmock.NewMockIdentityService(ctrl)
	h := handler.NewGRPCHandler(svc)

	svc.EXPECT().
		Login(gomock.Any(), gomock.Any()).
		Return(nil, &service.LoginError{Code: domain.AuthBanned, Msg: "banned until X"})

	resp, err := h.Authenticate(context.Background(), &identityv1.AuthenticateRequest{
		Username: "alice", Password: []byte("x"), Method: identityv1.AuthMethod_AUTH_METHOD_PASSWORD,
	})
	require.NoError(t, err)
	assert.Equal(t, identityv1.AuthResult_AUTH_RESULT_BANNED, resp.Result)
	assert.Equal(t, uint32(domain.AuthBanned), resp.ErrorCode)
}

func TestAuthenticate_LoginError_AlreadyLoggedIn(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := domainmock.NewMockIdentityService(ctrl)
	h := handler.NewGRPCHandler(svc)

	svc.EXPECT().
		Login(gomock.Any(), gomock.Any()).
		Return(nil, &service.LoginError{Code: domain.AuthAlreadyOnline, Msg: "dup"})

	resp, err := h.Authenticate(context.Background(), &identityv1.AuthenticateRequest{
		Username: "alice", Password: []byte("x"), Method: identityv1.AuthMethod_AUTH_METHOD_PASSWORD,
	})
	require.NoError(t, err)
	assert.Equal(t, identityv1.AuthResult_AUTH_RESULT_ALREADY_LOGGED_IN, resp.Result)
	assert.Equal(t, uint32(domain.AuthAlreadyOnline), resp.ErrorCode)
}

func TestAuthenticate_UnknownError_CollapsesToServerClosed(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := domainmock.NewMockIdentityService(ctrl)
	h := handler.NewGRPCHandler(svc)

	svc.EXPECT().
		Login(gomock.Any(), gomock.Any()).
		Return(nil, errors.New("valkey exploded"))

	resp, err := h.Authenticate(context.Background(), &identityv1.AuthenticateRequest{
		Username: "alice", Password: []byte("x"), Method: identityv1.AuthMethod_AUTH_METHOD_PASSWORD,
	})
	require.NoError(t, err, "unknown errors are still wire failures, not gRPC errors")
	assert.Equal(t, identityv1.AuthResult_AUTH_RESULT_SERVER_CLOSED, resp.Result)
	assert.Equal(t, uint32(99), resp.ErrorCode, "sentinel code for non-auth failures")
}

func TestAuthenticate_InvalidIP_FallsBackToZero(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := domainmock.NewMockIdentityService(ctrl)
	h := handler.NewGRPCHandler(svc)

	svc.EXPECT().
		Login(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, req domain.LoginRequest) (*domain.LoginResponse, error) {
			assert.Equal(t, netip.Addr{}, req.RemoteIP,
				"unparseable IP must become the zero-value addr, not panic")
			return &domain.LoginResponse{
				Account: &domain.Account{AccountID: 1, Sex: domain.SexMale},
				Session: &domain.Session{AccountID: 1, LoginID1: 1, LoginID2: 2},
			}, nil
		})

	resp, err := h.Authenticate(context.Background(), &identityv1.AuthenticateRequest{
		Username: "alice",
		Password: []byte("x"),
		ClientIp: "not-an-ip-address",
		Method:   identityv1.AuthMethod_AUTH_METHOD_PASSWORD,
	})
	require.NoError(t, err)
	assert.Equal(t, identityv1.AuthResult_AUTH_RESULT_OK, resp.Result)
}

func TestGetCharacterList_HappyPath(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := domainmock.NewMockIdentityService(ctrl)
	h := handler.NewGRPCHandler(svc)

	want := []domain.CharacterSummary{
		{CharID: 101, AccountID: 9, Slot: 0, Name: "alpha", Class: 0, BaseLevel: 50, JobLevel: 25},
		{CharID: 102, AccountID: 9, Slot: 1, Name: "beta", Class: 12, BaseLevel: 80, JobLevel: 40},
	}
	svc.EXPECT().
		ListCharacters(gomock.Any(), uint32(9)).
		Return(want, nil)

	resp, err := h.GetCharacterList(context.Background(), &identityv1.GetCharacterListRequest{
		AccountId: 9,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Len(t, resp.Characters, 2)

	assert.Equal(t, uint32(101), resp.Characters[0].CharId)
	assert.Equal(t, uint32(0), resp.Characters[0].Slot)
	assert.Equal(t, "alpha", resp.Characters[0].Name)
	assert.Equal(t, uint32(0), resp.Characters[0].ClassId)
	assert.Equal(t, uint32(50), resp.Characters[0].BaseLevel)
	assert.Equal(t, uint32(25), resp.Characters[0].JobLevel)

	assert.Equal(t, uint32(102), resp.Characters[1].CharId)
	assert.Equal(t, uint32(12), resp.Characters[1].ClassId)
	assert.Equal(t, "beta", resp.Characters[1].Name)
}

func TestGetCharacterList_Empty(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := domainmock.NewMockIdentityService(ctrl)
	h := handler.NewGRPCHandler(svc)

	svc.EXPECT().
		ListCharacters(gomock.Any(), uint32(9)).
		Return([]domain.CharacterSummary{}, nil)

	resp, err := h.GetCharacterList(context.Background(), &identityv1.GetCharacterListRequest{
		AccountId: 9,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.NotNil(t, resp.Characters, "empty roster must serialize as [], not null")
	assert.Empty(t, resp.Characters)
}

func TestGetCharacterList_Error(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := domainmock.NewMockIdentityService(ctrl)
	h := handler.NewGRPCHandler(svc)

	svc.EXPECT().
		ListCharacters(gomock.Any(), uint32(9)).
		Return(nil, errors.New("db down"))

	resp, err := h.GetCharacterList(context.Background(), &identityv1.GetCharacterListRequest{
		AccountId: 9,
	})
	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
	assert.Contains(t, st.Message(), "list characters")
}

func TestAuthenticate_NilRequest_ReturnsInvalidArgument(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := domainmock.NewMockIdentityService(ctrl)
	h := handler.NewGRPCHandler(svc)

	resp, err := h.Authenticate(context.Background(), nil)
	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestGetCharacterList_NilRequest_ReturnsInvalidArgument(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := domainmock.NewMockIdentityService(ctrl)
	h := handler.NewGRPCHandler(svc)

	resp, err := h.GetCharacterList(context.Background(), nil)
	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}
