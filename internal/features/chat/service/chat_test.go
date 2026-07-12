//go:build unit

package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bouroo/goAthena/internal/features/chat/domain"
	"github.com/bouroo/goAthena/internal/features/chat/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestService(t *testing.T) (*chatService, domain.ChatRepository, domain.FriendRepository, domain.PartyRepository, domain.LockStore) {
	chatRepo := repository.NewMemoryChatRepository()
	friendRepo := repository.NewMemoryFriendRepository()
	partyRepo := repository.NewMemoryPartyRepository()

	locks := &mockLockStore{
		locks:  make(map[string]string),
		tokens: make(map[string]string),
		mu:     make(map[string]struct{}),
	}

	svc := NewChatService(chatRepo, friendRepo, partyRepo, locks, 5*time.Second).(*chatService)

	return svc, chatRepo, friendRepo, partyRepo, locks
}

type mockLockStore struct {
	locks  map[string]string
	tokens map[string]string
	mu     map[string]struct{}
}

func (m *mockLockStore) Acquire(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if _, exists := m.mu[key]; exists {
		return "", domain.ErrPartyFull
	}

	token := "token-" + key
	m.locks[key] = token
	m.tokens[token] = key
	m.mu[key] = struct{}{}

	return token, nil
}

func (m *mockLockStore) Release(ctx context.Context, key, token string) error {
	if expectedToken, ok := m.locks[key]; !ok || expectedToken != token {
		return nil
	}

	delete(m.locks, key)
	delete(m.tokens, token)
	delete(m.mu, key)

	return nil
}

func TestWhisper_Success(t *testing.T) {
	svc, chatRepo, _, _, _ := setupTestService(t)
	ctx := context.Background()
	senderID := uint32(1001)
	targetID := uint32(1002)
	content := "Hello there!"

	err := svc.Whisper(ctx, senderID, targetID, content)

	require.NoError(t, err)

	messages, err := chatRepo.GetRecentMessages(ctx, senderID, 10)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	assert.Equal(t, content, messages[0].Content)
	assert.Equal(t, senderID, messages[0].SenderID)
	assert.Equal(t, targetID, messages[0].TargetID)
}

func TestWhisper_SelfMessage(t *testing.T) {
	svc, _, _, _, locks := setupTestService(t)
	_ = locks
	ctx := context.Background()
	senderID := uint32(1001)
	content := "Hello to myself"

	err := svc.Whisper(ctx, senderID, senderID, content)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot whisper to self")
}

func TestWhisper_EmptyMessage(t *testing.T) {
	svc, _, _, _, locks := setupTestService(t)
	_ = locks
	ctx := context.Background()
	senderID := uint32(1001)
	targetID := uint32(1002)
	content := ""

	err := svc.Whisper(ctx, senderID, targetID, content)

	assert.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrEmptyMessage)
}

func TestWhisper_MessageTooLong(t *testing.T) {
	svc, _, _, _, locks := setupTestService(t)
	_ = locks
	ctx := context.Background()
	senderID := uint32(1001)
	targetID := uint32(1002)
	content := strings.Repeat("a", 256)

	err := svc.Whisper(ctx, senderID, targetID, content)

	assert.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrMessageTooLong)
}

func TestSendPartyChat_Success(t *testing.T) {
	svc, _, _, partyRepo, _ := setupTestService(t)
	ctx := context.Background()
	charID := uint32(1001)
	content := "Party time!"

	party := domain.Party{
		ID:       "test-party-1",
		Name:     "Test Party",
		LeaderID: charID,
		Members: []domain.PartyMember{
			{CharID: charID, Name: "Leader", IsLeader: true},
		},
	}
	err := partyRepo.CreateParty(ctx, party)
	require.NoError(t, err)

	err = svc.SendPartyChat(ctx, charID, content)

	require.NoError(t, err)
}

func TestSendPartyChat_NotInParty(t *testing.T) {
	svc, _, _, _, locks := setupTestService(t)
	_ = locks
	ctx := context.Background()
	charID := uint32(1001)
	content := "Party time!"

	err := svc.SendPartyChat(ctx, charID, content)

	assert.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrNotPartyMember)
}

func TestSendPartyChat_EmptyMessage(t *testing.T) {
	svc, _, _, _, locks := setupTestService(t)
	_ = locks
	ctx := context.Background()
	charID := uint32(1001)
	content := ""

	err := svc.SendPartyChat(ctx, charID, content)

	assert.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrEmptyMessage)
}

func TestSendPartyChat_MessageTooLong(t *testing.T) {
	svc, _, _, _, locks := setupTestService(t)
	_ = locks
	ctx := context.Background()
	charID := uint32(1001)
	content := strings.Repeat("a", 256)

	err := svc.SendPartyChat(ctx, charID, content)

	assert.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrMessageTooLong)
}

func TestSendMapChat_Success(t *testing.T) {
	svc, chatRepo, _, _, _ := setupTestService(t)
	ctx := context.Background()
	senderID := uint32(1001)
	mapName := "prontera"
	content := "Hello map!"

	err := svc.SendMapChat(ctx, senderID, mapName, content)

	require.NoError(t, err)

	messages, err := chatRepo.GetRecentMessages(ctx, senderID, 10)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	assert.Equal(t, content, messages[0].Content)
	assert.Equal(t, senderID, messages[0].SenderID)
	assert.Equal(t, domain.MessageTypeMap, messages[0].Type)
}

func TestSendMapChat_EmptyMap(t *testing.T) {
	svc, _, _, _, locks := setupTestService(t)
	_ = locks
	ctx := context.Background()
	senderID := uint32(1001)
	mapName := ""
	content := "Hello!"

	err := svc.SendMapChat(ctx, senderID, mapName, content)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "map name cannot be empty")
}

func TestSendMapChat_EmptyMessage(t *testing.T) {
	svc, _, _, _, locks := setupTestService(t)
	_ = locks
	ctx := context.Background()
	senderID := uint32(1001)
	mapName := "prontera"
	content := ""

	err := svc.SendMapChat(ctx, senderID, mapName, content)

	assert.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrEmptyMessage)
}

func TestSendFriendRequest_Success(t *testing.T) {
	svc, _, friendRepo, _, locks := setupTestService(t)
	ctx := context.Background()
	fromAccountID := uint32(1001)
	toAccountID := uint32(1002)
	fromName := "Sender"
	toName := "Receiver"
	_ = locks

	err := svc.SendFriendRequest(ctx, fromAccountID, toAccountID, fromName, toName)

	require.NoError(t, err)

	isFriend, err := friendRepo.IsFriend(ctx, fromAccountID, toAccountID)
	require.NoError(t, err)
	assert.False(t, isFriend)

	hasPending, err := friendRepo.HasPendingRequest(ctx, fromAccountID, toAccountID)
	require.NoError(t, err)
	assert.True(t, hasPending)
}

func TestSendFriendRequest_AlreadyFriends(t *testing.T) {
	svc, _, friendRepo, _, locks := setupTestService(t)
	ctx := context.Background()
	_ = locks
	fromAccountID := uint32(1001)
	toAccountID := uint32(1002)
	fromName := "Sender"
	toName := "Receiver"

	friendship := domain.Friendship{
		AccountID:       fromAccountID,
		FriendAccountID: toAccountID,
		FriendName:      toName,
		Status:          domain.FriendStatusOnline,
	}
	err := friendRepo.AddFriend(ctx, friendship)
	require.NoError(t, err)

	err = svc.SendFriendRequest(ctx, fromAccountID, toAccountID, fromName, toName)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already friends")
}

func TestSendFriendRequest_SelfRequest(t *testing.T) {
	svc, _, _, _, locks := setupTestService(t)
	ctx := context.Background()
	_ = locks
	fromAccountID := uint32(1001)
	toAccountID := uint32(1001)
	fromName := "Sender"
	toName := "Sender"

	err := svc.SendFriendRequest(ctx, fromAccountID, toAccountID, fromName, toName)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot send friend request to self")
}

func TestSendFriendRequest_AlreadyPending(t *testing.T) {
	svc, _, friendRepo, _, locks := setupTestService(t)
	ctx := context.Background()
	_ = locks
	fromAccountID := uint32(1001)
	toAccountID := uint32(1002)
	fromName := "Sender"
	toName := "Receiver"

	request := domain.FriendRequest{
		FromAccountID: fromAccountID,
		ToAccountID:   toAccountID,
		FromName:      fromName,
		ToName:        toName,
		Status:        domain.FriendRequestPending,
	}
	err := friendRepo.CreateFriendRequest(ctx, request)
	require.NoError(t, err)

	err = svc.SendFriendRequest(ctx, fromAccountID, toAccountID, fromName, toName)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "friend request already pending")
}

func TestAcceptFriendRequest_Success(t *testing.T) {
	svc, _, friendRepo, _, _ := setupTestService(t)
	ctx := context.Background()
	fromAccountID := uint32(1001)
	toAccountID := uint32(1002)
	fromName := "Sender"
	toName := "Receiver"

	request := domain.FriendRequest{
		FromAccountID: fromAccountID,
		ToAccountID:   toAccountID,
		FromName:      fromName,
		ToName:        toName,
		Status:        domain.FriendRequestPending,
	}
	err := friendRepo.CreateFriendRequest(ctx, request)
	require.NoError(t, err)

	requests, _ := friendRepo.GetPendingFriendRequests(ctx, toAccountID)
	requestID := requests[0].ID

	err = svc.AcceptFriendRequest(ctx, requestID)

	require.NoError(t, err)

	isFriend, err := friendRepo.IsFriend(ctx, fromAccountID, toAccountID)
	require.NoError(t, err)
	assert.True(t, isFriend)

	isFriendReverse, err := friendRepo.IsFriend(ctx, toAccountID, fromAccountID)
	require.NoError(t, err)
	assert.True(t, isFriendReverse)

	request, _ = friendRepo.GetFriendRequest(ctx, requestID)
	assert.Equal(t, domain.FriendRequestAccepted, request.Status)
}

func TestRejectFriendRequest_Success(t *testing.T) {
	svc, _, friendRepo, _, locks := setupTestService(t)
	ctx := context.Background()
	_ = locks
	fromAccountID := uint32(1001)
	toAccountID := uint32(1002)
	fromName := "Sender"
	toName := "Receiver"

	request := domain.FriendRequest{
		FromAccountID: fromAccountID,
		ToAccountID:   toAccountID,
		FromName:      fromName,
		ToName:        toName,
		Status:        domain.FriendRequestPending,
	}
	err := friendRepo.CreateFriendRequest(ctx, request)
	require.NoError(t, err)

	requests, _ := friendRepo.GetPendingFriendRequests(ctx, toAccountID)
	requestID := requests[0].ID

	err = svc.RejectFriendRequest(ctx, requestID)

	require.NoError(t, err)

	request, _ = friendRepo.GetFriendRequest(ctx, requestID)
	assert.Equal(t, domain.FriendRequestRejected, request.Status)

	isFriend, err := friendRepo.IsFriend(ctx, fromAccountID, toAccountID)
	require.NoError(t, err)
	assert.False(t, isFriend)
}

func TestRemoveFriend_Success(t *testing.T) {
	svc, _, friendRepo, _, _ := setupTestService(t)
	ctx := context.Background()
	accountID := uint32(1001)
	friendAccountID := uint32(1002)

	friendship1 := domain.Friendship{
		AccountID:       accountID,
		FriendAccountID: friendAccountID,
		FriendName:      "Friend",
		Status:          domain.FriendStatusOnline,
	}
	friendship2 := domain.Friendship{
		AccountID:       friendAccountID,
		FriendAccountID: accountID,
		FriendName:      "Account",
		Status:          domain.FriendStatusOnline,
	}
	err := friendRepo.AddFriend(ctx, friendship1)
	require.NoError(t, err)
	err = friendRepo.AddFriend(ctx, friendship2)
	require.NoError(t, err)

	err = svc.RemoveFriend(ctx, accountID, friendAccountID)

	require.NoError(t, err)

	isFriend, err := friendRepo.IsFriend(ctx, accountID, friendAccountID)
	require.NoError(t, err)
	assert.False(t, isFriend)

	isFriendReverse, err := friendRepo.IsFriend(ctx, friendAccountID, accountID)
	require.NoError(t, err)
	assert.False(t, isFriendReverse)
}

func TestListFriends_Success(t *testing.T) {
	svc, _, friendRepo, _, _ := setupTestService(t)
	ctx := context.Background()
	accountID := uint32(1001)

	friendship1 := domain.Friendship{
		AccountID:       accountID,
		FriendAccountID: 1002,
		FriendName:      "Friend1",
		Status:          domain.FriendStatusOnline,
	}
	friendship2 := domain.Friendship{
		AccountID:       accountID,
		FriendAccountID: 1003,
		FriendName:      "Friend2",
		Status:          domain.FriendStatusOffline,
	}
	err := friendRepo.AddFriend(ctx, friendship1)
	require.NoError(t, err)
	err = friendRepo.AddFriend(ctx, friendship2)
	require.NoError(t, err)

	friends, err := svc.ListFriends(ctx, accountID)

	require.NoError(t, err)
	require.Len(t, friends, 2)
	assert.Equal(t, uint32(1002), friends[0].FriendAccountID)
	assert.Equal(t, uint32(1003), friends[1].FriendAccountID)
}

func TestListFriends_Empty(t *testing.T) {
	svc, _, _, _, locks := setupTestService(t)
	_ = locks
	ctx := context.Background()
	accountID := uint32(1001)

	friends, err := svc.ListFriends(ctx, accountID)

	require.NoError(t, err)
	assert.Empty(t, friends)
}

func TestCreateParty_Success(t *testing.T) {
	svc, _, _, partyRepo, locks := setupTestService(t)
	ctx := context.Background()
	_ = locks
	name := "Test Party"
	leaderID := uint32(1001)
	leaderName := "Leader"

	party, err := svc.CreateParty(ctx, name, leaderID, leaderName)

	require.NoError(t, err)
	assert.NotEmpty(t, party.ID)
	assert.Equal(t, name, party.Name)
	assert.Equal(t, leaderID, party.LeaderID)
	require.Len(t, party.Members, 1)
	assert.Equal(t, leaderID, party.Members[0].CharID)
	assert.True(t, party.Members[0].IsLeader)

	retrievedParty, err := partyRepo.GetParty(ctx, party.ID)
	require.NoError(t, err)
	assert.Equal(t, party.ID, retrievedParty.ID)
	assert.Equal(t, name, retrievedParty.Name)
}

func TestCreateParty_EmptyName(t *testing.T) {
	svc, _, _, _, locks := setupTestService(t)
	ctx := context.Background()
	_ = locks
	name := ""
	leaderID := uint32(1001)
	leaderName := "Leader"

	_, err := svc.CreateParty(ctx, name, leaderID, leaderName)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "party name cannot be empty")
}

func TestJoinParty_Success(t *testing.T) {
	svc, _, _, partyRepo, locks := setupTestService(t)
	ctx := context.Background()
	_ = locks
	leaderID := uint32(1001)
	leaderName := "Leader"
	charID := uint32(1002)
	charName := "Member"

	party := domain.Party{
		ID:       "test-party-1",
		Name:     "Test Party",
		LeaderID: leaderID,
		Members: []domain.PartyMember{
			{CharID: leaderID, Name: leaderName, IsLeader: true},
		},
	}
	err := partyRepo.CreateParty(ctx, party)
	require.NoError(t, err)

	err = svc.JoinParty(ctx, party.ID, charID, charName)

	require.NoError(t, err)

	retrievedParty, err := partyRepo.GetParty(ctx, party.ID)
	require.NoError(t, err)
	assert.Len(t, retrievedParty.Members, 2)

	found := false
	for _, member := range retrievedParty.Members {
		if member.CharID == charID {
			found = true
			assert.Equal(t, charName, member.Name)
			assert.False(t, member.IsLeader)
			break
		}
	}
	assert.True(t, found)
}

func TestJoinParty_FullParty(t *testing.T) {
	svc, _, _, partyRepo, _ := setupTestService(t)
	ctx := context.Background()
	leaderID := uint32(1001)
	partyID := "test-party-full"

	members := make([]domain.PartyMember, 12)
	for i := 0; i < 12; i++ {
		members[i] = domain.PartyMember{
			CharID:   uint32(2000 + i),
			Name:     "Member",
			IsLeader: i == 0,
		}
	}

	party := domain.Party{
		ID:       partyID,
		Name:     "Full Party",
		LeaderID: leaderID,
		Members:  members,
	}
	err := partyRepo.CreateParty(ctx, party)
	require.NoError(t, err)

	newCharID := uint32(3000)
	err = svc.JoinParty(ctx, partyID, newCharID, "New Member")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "party is full")
}

func TestJoinParty_AlreadyMember(t *testing.T) {
	svc, _, _, partyRepo, _ := setupTestService(t)
	ctx := context.Background()
	leaderID := uint32(1001)
	partyID := "test-party-dup"

	party := domain.Party{
		ID:       partyID,
		Name:     "Test Party",
		LeaderID: leaderID,
		Members: []domain.PartyMember{
			{CharID: leaderID, Name: "Leader", IsLeader: true},
			{CharID: 1002, Name: "Member", IsLeader: false},
		},
	}
	err := partyRepo.CreateParty(ctx, party)
	require.NoError(t, err)

	err = svc.JoinParty(ctx, partyID, 1002, "Member")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already a member")
}

func TestLeaveParty_Success(t *testing.T) {
	svc, _, _, partyRepo, _ := setupTestService(t)
	ctx := context.Background()
	leaderID := uint32(1001)
	memberID := uint32(1002)
	partyID := "test-party-leave"

	party := domain.Party{
		ID:       partyID,
		Name:     "Test Party",
		LeaderID: leaderID,
		Members: []domain.PartyMember{
			{CharID: leaderID, Name: "Leader", IsLeader: true},
			{CharID: memberID, Name: "Member", IsLeader: false},
		},
	}
	err := partyRepo.CreateParty(ctx, party)
	require.NoError(t, err)

	err = svc.LeaveParty(ctx, partyID, memberID)

	require.NoError(t, err)

	retrievedParty, err := partyRepo.GetParty(ctx, partyID)
	require.NoError(t, err)
	assert.Len(t, retrievedParty.Members, 1)
	assert.Equal(t, leaderID, retrievedParty.Members[0].CharID)
}

func TestLeaveParty_LeaderLeaves(t *testing.T) {
	svc, _, _, partyRepo, _ := setupTestService(t)
	ctx := context.Background()
	leaderID := uint32(1001)
	memberID := uint32(1002)
	partyID := "test-party-leader"

	party := domain.Party{
		ID:       partyID,
		Name:     "Test Party",
		LeaderID: leaderID,
		Members: []domain.PartyMember{
			{CharID: leaderID, Name: "Leader", IsLeader: true},
			{CharID: memberID, Name: "Member", IsLeader: false},
		},
	}
	err := partyRepo.CreateParty(ctx, party)
	require.NoError(t, err)

	err = svc.LeaveParty(ctx, partyID, leaderID)

	require.NoError(t, err)

	retrievedParty, err := partyRepo.GetParty(ctx, partyID)
	require.NoError(t, err)
	assert.Len(t, retrievedParty.Members, 1)
	assert.Equal(t, memberID, retrievedParty.Members[0].CharID)
	assert.True(t, retrievedParty.Members[0].IsLeader)
	assert.Equal(t, memberID, retrievedParty.LeaderID)
}

func TestLeaveParty_LastMemberDisbands(t *testing.T) {
	svc, _, _, partyRepo, _ := setupTestService(t)
	ctx := context.Background()
	leaderID := uint32(1001)
	partyID := "test-party-disband"

	party := domain.Party{
		ID:       partyID,
		Name:     "Test Party",
		LeaderID: leaderID,
		Members: []domain.PartyMember{
			{CharID: leaderID, Name: "Leader", IsLeader: true},
		},
	}
	err := partyRepo.CreateParty(ctx, party)
	require.NoError(t, err)

	err = svc.LeaveParty(ctx, partyID, leaderID)

	require.NoError(t, err)

	_, err = partyRepo.GetParty(ctx, partyID)
	assert.Error(t, err)
}

func TestGetParty_Success(t *testing.T) {
	svc, _, _, partyRepo, locks := setupTestService(t)
	ctx := context.Background()
	_ = locks
	_ = locks
	leaderID := uint32(1001)
	partyID := "test-party-get"

	party := domain.Party{
		ID:       partyID,
		Name:     "Test Party",
		LeaderID: leaderID,
		Members: []domain.PartyMember{
			{CharID: leaderID, Name: "Leader", IsLeader: true},
		},
	}
	err := partyRepo.CreateParty(ctx, party)
	require.NoError(t, err)

	retrievedParty, err := svc.GetParty(ctx, partyID)

	require.NoError(t, err)
	assert.Equal(t, partyID, retrievedParty.ID)
	assert.Equal(t, "Test Party", retrievedParty.Name)
	assert.Equal(t, leaderID, retrievedParty.LeaderID)
	assert.Len(t, retrievedParty.Members, 1)
}

func TestGetParty_NotFound(t *testing.T) {
	svc, _, _, _, locks := setupTestService(t)
	_ = locks
	ctx := context.Background()
	partyID := "non-existent-party"

	_, err := svc.GetParty(ctx, partyID)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "party not found")
}

func TestDefaultLockTTL(t *testing.T) {
	svc, _, _, _, locks := setupTestService(t)
	_ = locks
	assert.Equal(t, 5*time.Second, svc.lockTTL)
}

func TestCustomLockTTL(t *testing.T) {
	chatRepo := repository.NewMemoryChatRepository()
	friendRepo := repository.NewMemoryFriendRepository()
	partyRepo := repository.NewMemoryPartyRepository()
	locks := &mockLockStore{
		locks:  make(map[string]string),
		tokens: make(map[string]string),
		mu:     make(map[string]struct{}),
	}

	customTTL := 10 * time.Second
	svc := NewChatService(chatRepo, friendRepo, partyRepo, locks, customTTL).(*chatService)

	assert.Equal(t, customTTL, svc.lockTTL)
}

func TestNewChatService_ZeroTTL(t *testing.T) {
	chatRepo := repository.NewMemoryChatRepository()
	friendRepo := repository.NewMemoryFriendRepository()
	partyRepo := repository.NewMemoryPartyRepository()
	locks := &mockLockStore{
		locks:  make(map[string]string),
		tokens: make(map[string]string),
		mu:     make(map[string]struct{}),
	}

	svc := NewChatService(chatRepo, friendRepo, partyRepo, locks, 0).(*chatService)

	assert.Equal(t, 5*time.Second, svc.lockTTL)
}
