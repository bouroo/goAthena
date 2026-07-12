//go:build unit

package handler

import (
	"context"
	"errors"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	zonev1 "github.com/bouroo/goAthena/api/pb/zone/v1"
	chatdomain "github.com/bouroo/goAthena/internal/features/chat/domain"
	chatdomainmock "github.com/bouroo/goAthena/internal/features/chat/domain/mock"
)

func setupTestHandler(t *testing.T) (*grpcHandler, *chatdomainmock.MockChatService, *zerolog.Logger) {
	ctrl := gomock.NewController(t)
	mockSvc := chatdomainmock.NewMockChatService(ctrl)

	logger := zerolog.New(nil)

	handler := NewGRPCHandler(mockSvc, &logger).(*grpcHandler)

	return handler, mockSvc, &logger
}

func TestWhisper_Success(t *testing.T) {
	handler, mockSvc, _ := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.WhisperRequest{
		SenderCharId: 1001,
		TargetCharId: 1002,
		Content:      "Hello there!",
	}

	mockSvc.EXPECT().Whisper(ctx, req.SenderCharId, req.TargetCharId, req.Content).Return(nil)

	resp, err := handler.Whisper(ctx, req)

	require.NoError(t, err)
	assert.True(t, resp.Success)
	assert.Empty(t, resp.ErrorMessage)
}

func TestWhisper_ServiceError(t *testing.T) {
	handler, mockSvc, _ := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.WhisperRequest{
		SenderCharId: 1001,
		TargetCharId: 1002,
		Content:      "Hello there!",
	}

	mockSvc.EXPECT().Whisper(ctx, req.SenderCharId, req.TargetCharId, req.Content).Return(errors.New("service error"))

	resp, err := handler.Whisper(ctx, req)

	require.NoError(t, err)
	assert.False(t, resp.Success)
	assert.NotEmpty(t, resp.ErrorMessage)
}

func TestWhisper_NilRequest(t *testing.T) {
	handler, _, _ := setupTestHandler(t)
	ctx := context.Background()

	resp, err := handler.Whisper(ctx, nil)

	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestWhisper_ZeroSenderCharId(t *testing.T) {
	handler, _, _ := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.WhisperRequest{
		SenderCharId: 0,
		TargetCharId: 1002,
		Content:      "Hello!",
	}

	resp, err := handler.Whisper(ctx, req)

	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestWhisper_ZeroTargetCharId(t *testing.T) {
	handler, _, _ := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.WhisperRequest{
		SenderCharId: 1001,
		TargetCharId: 0,
		Content:      "Hello!",
	}

	resp, err := handler.Whisper(ctx, req)

	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestWhisper_EmptyContent(t *testing.T) {
	handler, _, _ := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.WhisperRequest{
		SenderCharId: 1001,
		TargetCharId: 1002,
		Content:      "",
	}

	resp, err := handler.Whisper(ctx, req)

	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestSendPartyChat_Success(t *testing.T) {
	handler, mockSvc, _ := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.SendPartyChatRequest{
		SenderCharId: 1001,
		Content:      "Party time!",
	}

	mockSvc.EXPECT().SendPartyChat(ctx, req.SenderCharId, req.Content).Return(nil)

	resp, err := handler.SendPartyChat(ctx, req)

	require.NoError(t, err)
	assert.True(t, resp.Success)
	assert.Empty(t, resp.ErrorMessage)
}

func TestSendPartyChat_NilRequest(t *testing.T) {
	handler, _, _ := setupTestHandler(t)
	ctx := context.Background()

	resp, err := handler.SendPartyChat(ctx, nil)

	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestSendPartyChat_ZeroSenderCharId(t *testing.T) {
	handler, _, _ := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.SendPartyChatRequest{
		SenderCharId: 0,
		Content:      "Party time!",
	}

	resp, err := handler.SendPartyChat(ctx, req)

	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestSendPartyChat_EmptyContent(t *testing.T) {
	handler, _, _ := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.SendPartyChatRequest{
		SenderCharId: 1001,
		Content:      "",
	}

	resp, err := handler.SendPartyChat(ctx, req)

	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestSendMapChat_Success(t *testing.T) {
	handler, mockSvc, _ := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.SendMapChatRequest{
		SenderCharId: 1001,
		MapName:      "prontera",
		Content:      "Hello map!",
	}

	mockSvc.EXPECT().SendMapChat(ctx, req.SenderCharId, req.MapName, req.Content).Return(nil)

	resp, err := handler.SendMapChat(ctx, req)

	require.NoError(t, err)
	assert.True(t, resp.Success)
	assert.Empty(t, resp.ErrorMessage)
}

func TestSendMapChat_NilRequest(t *testing.T) {
	handler, _, _ := setupTestHandler(t)
	ctx := context.Background()

	resp, err := handler.SendMapChat(ctx, nil)

	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestSendMapChat_ZeroSenderCharId(t *testing.T) {
	handler, _, _ := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.SendMapChatRequest{
		SenderCharId: 0,
		MapName:      "prontera",
		Content:      "Hello!",
	}

	resp, err := handler.SendMapChat(ctx, req)

	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestSendMapChat_EmptyMapName(t *testing.T) {
	handler, _, _ := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.SendMapChatRequest{
		SenderCharId: 1001,
		MapName:      "",
		Content:      "Hello!",
	}

	resp, err := handler.SendMapChat(ctx, req)

	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestSendMapChat_EmptyContent(t *testing.T) {
	handler, _, _ := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.SendMapChatRequest{
		SenderCharId: 1001,
		MapName:      "prontera",
		Content:      "",
	}

	resp, err := handler.SendMapChat(ctx, req)

	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestSendFriendRequest_Success(t *testing.T) {
	handler, mockSvc, _ := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.SendFriendRequestRequest{
		FromAccountId: 1001,
		ToAccountId:   1002,
		FromName:      "Sender",
		ToName:        "Receiver",
	}

	mockSvc.EXPECT().SendFriendRequest(ctx, req.FromAccountId, req.ToAccountId, req.FromName, req.ToName).Return(nil)

	resp, err := handler.SendFriendRequest(ctx, req)

	require.NoError(t, err)
	assert.True(t, resp.Success)
	assert.Empty(t, resp.ErrorMessage)
}

func TestSendFriendRequest_NilRequest(t *testing.T) {
	handler, _, _ := setupTestHandler(t)
	ctx := context.Background()

	resp, err := handler.SendFriendRequest(ctx, nil)

	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestSendFriendRequest_ZeroFromAccountId(t *testing.T) {
	handler, _, _ := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.SendFriendRequestRequest{
		FromAccountId: 0,
		ToAccountId:   1002,
		FromName:      "Sender",
		ToName:        "Receiver",
	}

	resp, err := handler.SendFriendRequest(ctx, req)

	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestSendFriendRequest_ZeroToAccountId(t *testing.T) {
	handler, _, _ := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.SendFriendRequestRequest{
		FromAccountId: 1001,
		ToAccountId:   0,
		FromName:      "Sender",
		ToName:        "Receiver",
	}

	resp, err := handler.SendFriendRequest(ctx, req)

	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestAcceptFriendRequest_Success(t *testing.T) {
	handler, mockSvc, _ := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.AcceptFriendRequestRequest{
		RequestId: 12345,
	}

	mockSvc.EXPECT().AcceptFriendRequest(ctx, req.RequestId).Return(nil)

	resp, err := handler.AcceptFriendRequest(ctx, req)

	require.NoError(t, err)
	assert.True(t, resp.Success)
	assert.Empty(t, resp.ErrorMessage)
}

func TestAcceptFriendRequest_NilRequest(t *testing.T) {
	handler, _, _ := setupTestHandler(t)
	ctx := context.Background()

	resp, err := handler.AcceptFriendRequest(ctx, nil)

	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestAcceptFriendRequest_ZeroRequestId(t *testing.T) {
	handler, _, _ := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.AcceptFriendRequestRequest{
		RequestId: 0,
	}

	resp, err := handler.AcceptFriendRequest(ctx, req)

	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestRejectFriendRequest_Success(t *testing.T) {
	handler, mockSvc, _ := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.RejectFriendRequestRequest{
		RequestId: 12345,
	}

	mockSvc.EXPECT().RejectFriendRequest(ctx, req.RequestId).Return(nil)

	resp, err := handler.RejectFriendRequest(ctx, req)

	require.NoError(t, err)
	assert.True(t, resp.Success)
	assert.Empty(t, resp.ErrorMessage)
}

func TestRejectFriendRequest_NilRequest(t *testing.T) {
	handler, _, _ := setupTestHandler(t)
	ctx := context.Background()

	resp, err := handler.RejectFriendRequest(ctx, nil)

	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestRejectFriendRequest_ZeroRequestId(t *testing.T) {
	handler, _, _ := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.RejectFriendRequestRequest{
		RequestId: 0,
	}

	resp, err := handler.RejectFriendRequest(ctx, req)

	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestRemoveFriend_Success(t *testing.T) {
	handler, mockSvc, _ := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.RemoveFriendRequest{
		AccountId:       1001,
		FriendAccountId: 1002,
	}

	mockSvc.EXPECT().RemoveFriend(ctx, req.AccountId, req.FriendAccountId).Return(nil)

	resp, err := handler.RemoveFriend(ctx, req)

	require.NoError(t, err)
	assert.True(t, resp.Success)
	assert.Empty(t, resp.ErrorMessage)
}

func TestRemoveFriend_NilRequest(t *testing.T) {
	handler, _, _ := setupTestHandler(t)
	ctx := context.Background()

	resp, err := handler.RemoveFriend(ctx, nil)

	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestRemoveFriend_ZeroAccountId(t *testing.T) {
	handler, _, _ := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.RemoveFriendRequest{
		AccountId:       0,
		FriendAccountId: 1002,
	}

	resp, err := handler.RemoveFriend(ctx, req)

	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestRemoveFriend_ZeroFriendAccountId(t *testing.T) {
	handler, _, _ := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.RemoveFriendRequest{
		AccountId:       1001,
		FriendAccountId: 0,
	}

	resp, err := handler.RemoveFriend(ctx, req)

	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestListFriends_Success(t *testing.T) {
	handler, mockSvc, _ := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.ListFriendsRequest{
		AccountId: 1001,
	}

	expectedFriends := []chatdomain.Friendship{
		{
			AccountID:       1001,
			FriendAccountID: 1002,
			FriendName:      "Friend1",
			Status:          chatdomain.FriendStatusOnline,
		},
		{
			AccountID:       1001,
			FriendAccountID: 1003,
			FriendName:      "Friend2",
			Status:          chatdomain.FriendStatusOffline,
		},
	}

	mockSvc.EXPECT().ListFriends(ctx, req.AccountId).Return(expectedFriends, nil)

	resp, err := handler.ListFriends(ctx, req)

	require.NoError(t, err)
	assert.True(t, resp.Success)
	assert.Empty(t, resp.ErrorMessage)
	assert.Len(t, resp.Friends, 2)
	assert.Equal(t, uint32(1002), resp.Friends[0].FriendAccountId)
	assert.Equal(t, "Friend1", resp.Friends[0].FriendName)
	assert.True(t, resp.Friends[0].Online)
	assert.Equal(t, uint32(1003), resp.Friends[1].FriendAccountId)
	assert.Equal(t, "Friend2", resp.Friends[1].FriendName)
	assert.False(t, resp.Friends[1].Online)
}

func TestListFriends_EmptyList(t *testing.T) {
	handler, mockSvc, _ := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.ListFriendsRequest{
		AccountId: 1001,
	}

	mockSvc.EXPECT().ListFriends(ctx, req.AccountId).Return([]chatdomain.Friendship{}, nil)

	resp, err := handler.ListFriends(ctx, req)

	require.NoError(t, err)
	assert.True(t, resp.Success)
	assert.Empty(t, resp.ErrorMessage)
	assert.Empty(t, resp.Friends)
}

func TestListFriends_NilRequest(t *testing.T) {
	handler, _, _ := setupTestHandler(t)
	ctx := context.Background()

	resp, err := handler.ListFriends(ctx, nil)

	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestListFriends_ZeroAccountId(t *testing.T) {
	handler, _, _ := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.ListFriendsRequest{
		AccountId: 0,
	}

	resp, err := handler.ListFriends(ctx, req)

	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestCreateParty_Success(t *testing.T) {
	handler, mockSvc, _ := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.CreatePartyRequest{
		Name:         "Test Party",
		LeaderCharId: 1001,
		LeaderName:   "Leader",
	}

	expectedParty := chatdomain.Party{
		ID:       "test-party-id",
		Name:     "Test Party",
		LeaderID: 1001,
		Members: []chatdomain.PartyMember{
			{CharID: 1001, Name: "Leader", IsLeader: true},
		},
	}

	mockSvc.EXPECT().CreateParty(ctx, req.Name, req.LeaderCharId, req.LeaderName).Return(expectedParty, nil)

	resp, err := handler.CreateParty(ctx, req)

	require.NoError(t, err)
	assert.True(t, resp.Success)
	assert.Empty(t, resp.ErrorMessage)
	assert.Equal(t, "test-party-id", resp.PartyId)
}

func TestCreateParty_NilRequest(t *testing.T) {
	handler, _, _ := setupTestHandler(t)
	ctx := context.Background()

	resp, err := handler.CreateParty(ctx, nil)

	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestCreateParty_EmptyName(t *testing.T) {
	handler, _, _ := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.CreatePartyRequest{
		Name:         "",
		LeaderCharId: 1001,
		LeaderName:   "Leader",
	}

	resp, err := handler.CreateParty(ctx, req)

	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestCreateParty_ZeroLeaderCharId(t *testing.T) {
	handler, _, _ := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.CreatePartyRequest{
		Name:         "Test Party",
		LeaderCharId: 0,
		LeaderName:   "Leader",
	}

	resp, err := handler.CreateParty(ctx, req)

	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestJoinParty_Success(t *testing.T) {
	handler, mockSvc, _ := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.JoinPartyRequest{
		PartyId:  "test-party-1",
		CharId:   1002,
		CharName: "Member",
	}

	mockSvc.EXPECT().JoinParty(ctx, req.PartyId, req.CharId, req.CharName).Return(nil)

	resp, err := handler.JoinParty(ctx, req)

	require.NoError(t, err)
	assert.True(t, resp.Success)
	assert.Empty(t, resp.ErrorMessage)
}

func TestJoinParty_NilRequest(t *testing.T) {
	handler, _, _ := setupTestHandler(t)
	ctx := context.Background()

	resp, err := handler.JoinParty(ctx, nil)

	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestJoinParty_EmptyPartyId(t *testing.T) {
	handler, _, _ := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.JoinPartyRequest{
		PartyId:  "",
		CharId:   1002,
		CharName: "Member",
	}

	resp, err := handler.JoinParty(ctx, req)

	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestJoinParty_ZeroCharId(t *testing.T) {
	handler, _, _ := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.JoinPartyRequest{
		PartyId:  "test-party-1",
		CharId:   0,
		CharName: "Member",
	}

	resp, err := handler.JoinParty(ctx, req)

	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestLeaveParty_Success(t *testing.T) {
	handler, mockSvc, _ := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.LeavePartyRequest{
		PartyId: "test-party-1",
		CharId:  1002,
	}

	mockSvc.EXPECT().LeaveParty(ctx, req.PartyId, req.CharId).Return(nil)

	resp, err := handler.LeaveParty(ctx, req)

	require.NoError(t, err)
	assert.True(t, resp.Success)
	assert.Empty(t, resp.ErrorMessage)
}

func TestLeaveParty_NilRequest(t *testing.T) {
	handler, _, _ := setupTestHandler(t)
	ctx := context.Background()

	resp, err := handler.LeaveParty(ctx, nil)

	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestLeaveParty_EmptyPartyId(t *testing.T) {
	handler, _, _ := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.LeavePartyRequest{
		PartyId: "",
		CharId:  1002,
	}

	resp, err := handler.LeaveParty(ctx, req)

	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestLeaveParty_ZeroCharId(t *testing.T) {
	handler, _, _ := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.LeavePartyRequest{
		PartyId: "test-party-1",
		CharId:  0,
	}

	resp, err := handler.LeaveParty(ctx, req)

	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestGetParty_Success(t *testing.T) {
	handler, mockSvc, _ := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.GetPartyRequest{
		PartyId: "test-party-1",
	}

	expectedParty := chatdomain.Party{
		ID:       "test-party-1",
		Name:     "Test Party",
		LeaderID: 1001,
		Members: []chatdomain.PartyMember{
			{CharID: 1001, Name: "Leader", IsLeader: true},
			{CharID: 1002, Name: "Member", IsLeader: false},
		},
	}

	mockSvc.EXPECT().GetParty(ctx, req.PartyId).Return(expectedParty, nil)

	resp, err := handler.GetParty(ctx, req)

	require.NoError(t, err)
	assert.True(t, resp.Success)
	assert.Empty(t, resp.ErrorMessage)
	assert.NotNil(t, resp.Party)
	assert.Equal(t, "test-party-1", resp.Party.Id)
	assert.Equal(t, "Test Party", resp.Party.Name)
	assert.Equal(t, uint32(1001), resp.Party.LeaderCharId)
	assert.Len(t, resp.Party.Members, 2)
	assert.Equal(t, uint32(1001), resp.Party.Members[0].CharId)
	assert.Equal(t, "Leader", resp.Party.Members[0].Name)
	assert.True(t, resp.Party.Members[0].IsLeader)
	assert.Equal(t, uint32(1002), resp.Party.Members[1].CharId)
	assert.Equal(t, "Member", resp.Party.Members[1].Name)
	assert.False(t, resp.Party.Members[1].IsLeader)
}

func TestGetParty_NilRequest(t *testing.T) {
	handler, _, _ := setupTestHandler(t)
	ctx := context.Background()

	resp, err := handler.GetParty(ctx, nil)

	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestGetParty_EmptyPartyId(t *testing.T) {
	handler, _, _ := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.GetPartyRequest{
		PartyId: "",
	}

	resp, err := handler.GetParty(ctx, req)

	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestMapChatError_ErrorsAreMapped(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		expectedMsg string
	}{
		{
			name:        "ErrPlayerNotFound",
			err:         chatdomain.ErrPlayerNotFound,
			expectedMsg: "Player not found",
		},
		{
			name:        "ErrNotFriends",
			err:         chatdomain.ErrNotFriends,
			expectedMsg: "Not friends",
		},
		{
			name:        "ErrFriendAlreadyExists",
			err:         chatdomain.ErrFriendAlreadyExists,
			expectedMsg: "Already friends",
		},
		{
			name:        "ErrFriendRequestPending",
			err:         chatdomain.ErrFriendRequestPending,
			expectedMsg: "Friend request already pending",
		},
		{
			name:        "ErrPartyNotFound",
			err:         chatdomain.ErrPartyNotFound,
			expectedMsg: "Party not found",
		},
		{
			name:        "ErrPartyFull",
			err:         chatdomain.ErrPartyFull,
			expectedMsg: "Party is full",
		},
		{
			name:        "ErrNotPartyMember",
			err:         chatdomain.ErrNotPartyMember,
			expectedMsg: "Not a party member",
		},
		{
			name:        "ErrAlreadyInParty",
			err:         chatdomain.ErrAlreadyInParty,
			expectedMsg: "Already in a party",
		},
		{
			name:        "ErrEmptyMessage",
			err:         chatdomain.ErrEmptyMessage,
			expectedMsg: "Message is empty",
		},
		{
			name:        "ErrMessageTooLong",
			err:         chatdomain.ErrMessageTooLong,
			expectedMsg: "Message is too long",
		},
		{
			name:        "Generic error",
			err:         errors.New("some other error"),
			expectedMsg: "Chat error: some other error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mapChatError(tt.err)
			assert.Equal(t, tt.expectedMsg, result)
		})
	}
}

func TestToProtoFriendInfo(t *testing.T) {
	friendship := chatdomain.Friendship{
		AccountID:       1001,
		FriendAccountID: 1002,
		FriendName:      "TestFriend",
		Status:          chatdomain.FriendStatusOnline,
	}

	result := toProtoFriendInfo(friendship)

	assert.Equal(t, uint32(1001), result.AccountId)
	assert.Equal(t, uint32(1002), result.FriendAccountId)
	assert.Equal(t, "TestFriend", result.FriendName)
	assert.True(t, result.Online)
}

func TestToProtoFriendInfo_Offline(t *testing.T) {
	friendship := chatdomain.Friendship{
		AccountID:       1001,
		FriendAccountID: 1002,
		FriendName:      "TestFriend",
		Status:          chatdomain.FriendStatusOffline,
	}

	result := toProtoFriendInfo(friendship)

	assert.False(t, result.Online)
}

func TestToProtoPartyInfo(t *testing.T) {
	party := chatdomain.Party{
		ID:       "test-party-1",
		Name:     "Test Party",
		LeaderID: 1001,
		Members: []chatdomain.PartyMember{
			{CharID: 1001, Name: "Leader", IsLeader: true},
			{CharID: 1002, Name: "Member", IsLeader: false},
		},
	}

	result := toProtoPartyInfo(party)

	assert.Equal(t, "test-party-1", result.Id)
	assert.Equal(t, "Test Party", result.Name)
	assert.Equal(t, uint32(1001), result.LeaderCharId)
	assert.Len(t, result.Members, 2)
	assert.Equal(t, uint32(1001), result.Members[0].CharId)
	assert.Equal(t, "Leader", result.Members[0].Name)
	assert.True(t, result.Members[0].IsLeader)
	assert.Equal(t, uint32(1002), result.Members[1].CharId)
	assert.Equal(t, "Member", result.Members[1].Name)
	assert.False(t, result.Members[1].IsLeader)
}
