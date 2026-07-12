package service

import (
	"context"
	"fmt"
	"time"

	"github.com/bouroo/goAthena/internal/features/chat/domain"
	"github.com/google/uuid"
)

const (
	maxMessageLength = 255
	maxPartySize     = 12
	releaseTimeout   = 2 * time.Second
)

type chatService struct {
	chatRepo   domain.ChatRepository
	friendRepo domain.FriendRepository
	partyRepo  domain.PartyRepository
	locks      domain.LockStore
	lockTTL    time.Duration
}

// NewChatService creates a new chat service with its dependencies.
func NewChatService(chatRepo domain.ChatRepository, friendRepo domain.FriendRepository, partyRepo domain.PartyRepository, locks domain.LockStore, lockTTL time.Duration) domain.ChatService {
	if lockTTL <= 0 {
		lockTTL = 5 * time.Second
	}
	return &chatService{
		chatRepo:   chatRepo,
		friendRepo: friendRepo,
		partyRepo:  partyRepo,
		locks:      locks,
		lockTTL:    lockTTL,
	}
}

func (s *chatService) Whisper(ctx context.Context, senderID, targetID uint32, content string) error {
	if content == "" {
		return domain.ErrEmptyMessage
	}
	if len(content) > maxMessageLength {
		return domain.ErrMessageTooLong
	}
	if senderID == targetID {
		return fmt.Errorf("cannot whisper to self (char %d)", senderID)
	}

	msg := domain.Message{
		ID:        uuid.New().String(),
		SenderID:  senderID,
		TargetID:  targetID,
		Type:      domain.MessageTypeWhisper,
		Content:   content,
		Timestamp: time.Now(),
	}

	if err := s.chatRepo.SaveMessage(ctx, msg); err != nil {
		return fmt.Errorf("save whisper message (char %d -> %d): %w", senderID, targetID, err)
	}

	return nil
}

func (s *chatService) SendPartyChat(ctx context.Context, senderID uint32, content string) error {
	if content == "" {
		return domain.ErrEmptyMessage
	}
	if len(content) > maxMessageLength {
		return domain.ErrMessageTooLong
	}

	party, err := s.partyRepo.GetPartyByMember(ctx, senderID)
	if err != nil {
		return fmt.Errorf("get party by member (char %d): %w", senderID, err)
	}
	if party.ID == "" {
		return domain.ErrNotPartyMember
	}

	msg := domain.Message{
		ID:        uuid.New().String(),
		SenderID:  senderID,
		TargetID:  0,
		Type:      domain.MessageTypeParty,
		Content:   content,
		Timestamp: time.Now(),
	}

	if err := s.chatRepo.SaveMessage(ctx, msg); err != nil {
		return fmt.Errorf("save party message (char %d): %w", senderID, err)
	}

	return nil
}

func (s *chatService) SendMapChat(ctx context.Context, senderID uint32, mapName, content string) error {
	if content == "" {
		return domain.ErrEmptyMessage
	}
	if len(content) > maxMessageLength {
		return domain.ErrMessageTooLong
	}
	if mapName == "" {
		return fmt.Errorf("map name cannot be empty")
	}

	msg := domain.Message{
		ID:        uuid.New().String(),
		SenderID:  senderID,
		TargetID:  0,
		Type:      domain.MessageTypeMap,
		Content:   content,
		Timestamp: time.Now(),
	}

	if err := s.chatRepo.SaveMessage(ctx, msg); err != nil {
		return fmt.Errorf("save map message (char %d, map %s): %w", senderID, mapName, err)
	}

	return nil
}

func (s *chatService) SendFriendRequest(ctx context.Context, fromAccountID, toAccountID uint32, fromName, toName string) error {
	if fromAccountID == toAccountID {
		return fmt.Errorf("cannot send friend request to self (account %d)", fromAccountID)
	}

	isFriend, err := s.friendRepo.IsFriend(ctx, fromAccountID, toAccountID)
	if err != nil {
		return fmt.Errorf("check friendship (account %d -> %d): %w", fromAccountID, toAccountID, err)
	}
	if isFriend {
		return fmt.Errorf("already friends with account %d", toAccountID)
	}

	hasPending, err := s.friendRepo.HasPendingRequest(ctx, fromAccountID, toAccountID)
	if err != nil {
		return fmt.Errorf("check pending request (account %d -> %d): %w", fromAccountID, toAccountID, err)
	}
	if hasPending {
		return fmt.Errorf("friend request already pending (account %d -> %d)", fromAccountID, toAccountID)
	}

	request := domain.FriendRequest{
		FromAccountID: fromAccountID,
		ToAccountID:   toAccountID,
		FromName:      fromName,
		ToName:        toName,
		Status:        domain.FriendRequestPending,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	if err := s.friendRepo.CreateFriendRequest(ctx, request); err != nil {
		return fmt.Errorf("create friend request (account %d -> %d): %w", fromAccountID, toAccountID, err)
	}

	return nil
}

func (s *chatService) AcceptFriendRequest(ctx context.Context, requestID uint64) error {
	request, err := s.friendRepo.GetFriendRequest(ctx, requestID)
	if err != nil {
		return fmt.Errorf("get friend request (id %d): %w", requestID, err)
	}

	fromLockKey := domain.FriendLockKey(request.FromAccountID)
	toLockKey := domain.FriendLockKey(request.ToAccountID)

	first, second := fromLockKey, toLockKey
	if first > second {
		first, second = second, first
	}

	token1, err := s.locks.Acquire(ctx, first, s.lockTTL)
	if err != nil {
		return fmt.Errorf("acquire friend lock (key %s): %w", first, err)
	}
	defer s.release(ctx, first, token1)

	token2, err := s.locks.Acquire(ctx, second, s.lockTTL)
	if err != nil {
		return fmt.Errorf("acquire friend lock (key %s): %w", second, err)
	}
	defer s.release(ctx, second, token2)

	request.Status = domain.FriendRequestAccepted
	request.UpdatedAt = time.Now()

	if err := s.friendRepo.UpdateFriendRequest(ctx, request); err != nil {
		return fmt.Errorf("update friend request (id %d): %w", requestID, err)
	}

	friendship1 := domain.Friendship{
		AccountID:       request.FromAccountID,
		FriendAccountID: request.ToAccountID,
		FriendName:      request.ToName,
		Status:          domain.FriendStatusOnline,
		CreatedAt:       time.Now(),
	}

	friendship2 := domain.Friendship{
		AccountID:       request.ToAccountID,
		FriendAccountID: request.FromAccountID,
		FriendName:      request.FromName,
		Status:          domain.FriendStatusOnline,
		CreatedAt:       time.Now(),
	}

	if err := s.friendRepo.AddFriend(ctx, friendship1); err != nil {
		return fmt.Errorf("add friend (account %d -> %d): %w", request.FromAccountID, request.ToAccountID, err)
	}

	if err := s.friendRepo.AddFriend(ctx, friendship2); err != nil {
		return fmt.Errorf("add friend (account %d -> %d): %w", request.ToAccountID, request.FromAccountID, err)
	}

	return nil
}

func (s *chatService) RejectFriendRequest(ctx context.Context, requestID uint64) error {
	request, err := s.friendRepo.GetFriendRequest(ctx, requestID)
	if err != nil {
		return fmt.Errorf("get friend request (id %d): %w", requestID, err)
	}

	request.Status = domain.FriendRequestRejected
	request.UpdatedAt = time.Now()

	if err := s.friendRepo.UpdateFriendRequest(ctx, request); err != nil {
		return fmt.Errorf("update friend request (id %d): %w", requestID, err)
	}

	return nil
}

func (s *chatService) RemoveFriend(ctx context.Context, accountID, friendAccountID uint32) error {
	fromLockKey := domain.FriendLockKey(accountID)
	toLockKey := domain.FriendLockKey(friendAccountID)

	first, second := fromLockKey, toLockKey
	if first > second {
		first, second = second, first
	}

	token1, err := s.locks.Acquire(ctx, first, s.lockTTL)
	if err != nil {
		return fmt.Errorf("acquire friend lock (key %s): %w", first, err)
	}
	defer s.release(ctx, first, token1)

	token2, err := s.locks.Acquire(ctx, second, s.lockTTL)
	if err != nil {
		return fmt.Errorf("acquire friend lock (key %s): %w", second, err)
	}
	defer s.release(ctx, second, token2)

	if err := s.friendRepo.RemoveFriend(ctx, accountID, friendAccountID); err != nil {
		return fmt.Errorf("remove friend (account %d -> %d): %w", accountID, friendAccountID, err)
	}

	if err := s.friendRepo.RemoveFriend(ctx, friendAccountID, accountID); err != nil {
		return fmt.Errorf("remove friend (account %d -> %d): %w", friendAccountID, accountID, err)
	}

	return nil
}

func (s *chatService) ListFriends(ctx context.Context, accountID uint32) ([]domain.Friendship, error) {
	friendships, err := s.friendRepo.ListFriends(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("list friends (account %d): %w", accountID, err)
	}

	return friendships, nil
}

func (s *chatService) CreateParty(ctx context.Context, name string, leaderID uint32, leaderName string) (domain.Party, error) {
	if name == "" {
		return domain.Party{}, fmt.Errorf("party name cannot be empty")
	}

	partyID := uuid.New().String()
	now := time.Now()

	party := domain.Party{
		ID:       partyID,
		Name:     name,
		LeaderID: leaderID,
		Members: []domain.PartyMember{
			{
				CharID:   leaderID,
				Name:     leaderName,
				IsLeader: true,
				JoinedAt: now,
			},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := s.partyRepo.CreateParty(ctx, party); err != nil {
		return domain.Party{}, fmt.Errorf("create party (name %s, leader %d): %w", name, leaderID, err)
	}

	return party, nil
}

func (s *chatService) JoinParty(ctx context.Context, partyID string, charID uint32, charName string) error {
	party, err := s.partyRepo.GetParty(ctx, partyID)
	if err != nil {
		return fmt.Errorf("get party (id %s): %w", partyID, err)
	}

	if len(party.Members) >= maxPartySize {
		return fmt.Errorf("party is full (max %d members)", maxPartySize)
	}

	for _, member := range party.Members {
		if member.CharID == charID {
			return fmt.Errorf("character %d is already a member of party %s", charID, partyID)
		}
	}

	lockKey := domain.PartyLockKey(partyID)
	token, err := s.locks.Acquire(ctx, lockKey, s.lockTTL)
	if err != nil {
		return fmt.Errorf("acquire party lock (key %s): %w", lockKey, err)
	}
	defer s.release(ctx, lockKey, token)

	party.Members = append(party.Members, domain.PartyMember{
		CharID:   charID,
		Name:     charName,
		IsLeader: false,
		JoinedAt: time.Now(),
	})
	party.UpdatedAt = time.Now()

	if err := s.partyRepo.UpdateParty(ctx, party); err != nil {
		return fmt.Errorf("update party (id %s): %w", partyID, err)
	}

	return nil
}

func (s *chatService) LeaveParty(ctx context.Context, partyID string, charID uint32) error {
	party, err := s.partyRepo.GetParty(ctx, partyID)
	if err != nil {
		return fmt.Errorf("get party (id %s): %w", partyID, err)
	}

	lockKey := domain.PartyLockKey(partyID)
	token, err := s.locks.Acquire(ctx, lockKey, s.lockTTL)
	if err != nil {
		return fmt.Errorf("acquire party lock (key %s): %w", lockKey, err)
	}
	defer s.release(ctx, lockKey, token)

	found := false
	memberIndex := -1
	for i, member := range party.Members {
		if member.CharID == charID {
			found = true
			memberIndex = i
			break
		}
	}

	if !found {
		return fmt.Errorf("character %d is not a member of party %s", charID, partyID)
	}

	isLeader := party.Members[memberIndex].IsLeader

	party.Members = append(party.Members[:memberIndex], party.Members[memberIndex+1:]...)
	party.UpdatedAt = time.Now()

	if isLeader && len(party.Members) > 0 {
		party.Members[0].IsLeader = true
		party.LeaderID = party.Members[0].CharID
	}

	if len(party.Members) == 0 {
		if err := s.partyRepo.DeleteParty(ctx, partyID); err != nil {
			return fmt.Errorf("delete party (id %s): %w", partyID, err)
		}
		return nil
	}

	if err := s.partyRepo.UpdateParty(ctx, party); err != nil {
		return fmt.Errorf("update party (id %s): %w", partyID, err)
	}

	return nil
}

func (s *chatService) GetParty(ctx context.Context, partyID string) (domain.Party, error) {
	party, err := s.partyRepo.GetParty(ctx, partyID)
	if err != nil {
		return domain.Party{}, fmt.Errorf("get party (id %s): %w", partyID, err)
	}

	return party, nil
}

func (s *chatService) release(ctx context.Context, lockKey string, token string) {
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
	defer cancel()
	_ = s.locks.Release(releaseCtx, lockKey, token)
}
