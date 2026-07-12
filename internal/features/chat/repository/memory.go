package repository

import (
	"context"
	"fmt"
	"sync"

	"github.com/bouroo/goAthena/internal/features/chat/domain"
)

type memoryChatRepository struct {
	mu       sync.RWMutex
	messages []domain.Message
	index    map[uint32][]int
}

// NewMemoryChatRepository creates a new in-memory chat repository.
func NewMemoryChatRepository() domain.ChatRepository {
	return &memoryChatRepository{
		messages: []domain.Message{},
		index:    make(map[uint32][]int),
	}
}

func (r *memoryChatRepository) SaveMessage(ctx context.Context, msg domain.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.messages = append(r.messages, msg)
	idx := len(r.messages) - 1

	r.index[msg.SenderID] = append(r.index[msg.SenderID], idx)

	if msg.TargetID != 0 {
		r.index[msg.TargetID] = append(r.index[msg.TargetID], idx)
	}

	return nil
}

func (r *memoryChatRepository) GetRecentMessages(ctx context.Context, charID uint32, limit int) ([]domain.Message, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	indices, ok := r.index[charID]
	if !ok {
		return []domain.Message{}, nil
	}

	if limit <= 0 || limit > len(indices) {
		limit = len(indices)
	}

	start := max(len(indices)-limit, 0)

	messages := make([]domain.Message, 0, limit)
	for i := start; i < len(indices); i++ {
		idx := indices[i]
		messages = append(messages, r.messages[idx])
	}

	return messages, nil
}

type memoryFriendRepository struct {
	mu              sync.RWMutex
	friendships     map[uint32][]domain.Friendship
	friendRequests  map[uint64]domain.FriendRequest
	pendingRequests map[uint32][]uint64
	nextRequestID   uint64
}

// NewMemoryFriendRepository creates a new in-memory friend repository.
func NewMemoryFriendRepository() domain.FriendRepository {
	return &memoryFriendRepository{
		friendships:     make(map[uint32][]domain.Friendship),
		friendRequests:  make(map[uint64]domain.FriendRequest),
		pendingRequests: make(map[uint32][]uint64),
		nextRequestID:   1,
	}
}

func (r *memoryFriendRepository) IsFriend(ctx context.Context, accountID, friendAccountID uint32) (bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	friendships, ok := r.friendships[accountID]
	if !ok {
		return false, nil
	}

	for _, f := range friendships {
		if f.FriendAccountID == friendAccountID {
			return true, nil
		}
	}

	return false, nil
}

func (r *memoryFriendRepository) HasPendingRequest(ctx context.Context, fromAccountID, toAccountID uint32) (bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	requestIDs, ok := r.pendingRequests[fromAccountID]
	if !ok {
		return false, nil
	}

	for _, reqID := range requestIDs {
		req := r.friendRequests[reqID]
		if req.ToAccountID == toAccountID && req.Status == domain.FriendRequestPending {
			return true, nil
		}
	}

	return false, nil
}

func (r *memoryFriendRepository) GetFriendRequest(ctx context.Context, requestID uint64) (domain.FriendRequest, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	request, ok := r.friendRequests[requestID]
	if !ok {
		return domain.FriendRequest{}, fmt.Errorf("friend request %d not found", requestID)
	}

	return request, nil
}

func (r *memoryFriendRepository) CreateFriendRequest(ctx context.Context, request domain.FriendRequest) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	request.ID = r.nextRequestID
	r.nextRequestID++

	r.friendRequests[request.ID] = request
	r.pendingRequests[request.FromAccountID] = append(r.pendingRequests[request.FromAccountID], request.ID)

	return nil
}

func (r *memoryFriendRepository) UpdateFriendRequest(ctx context.Context, request domain.FriendRequest) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.friendRequests[request.ID]; !ok {
		return fmt.Errorf("friend request %d not found", request.ID)
	}

	r.friendRequests[request.ID] = request

	return nil
}

func (r *memoryFriendRepository) AddFriend(ctx context.Context, friendship domain.Friendship) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	friendship.ID = uint64(len(r.friendships[friendship.AccountID]) + 1)
	r.friendships[friendship.AccountID] = append(r.friendships[friendship.AccountID], friendship)

	return nil
}

func (r *memoryFriendRepository) RemoveFriend(ctx context.Context, accountID, friendAccountID uint32) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	friendships, ok := r.friendships[accountID]
	if !ok {
		return nil
	}

	updated := make([]domain.Friendship, 0, len(friendships))
	for _, f := range friendships {
		if f.FriendAccountID != friendAccountID {
			updated = append(updated, f)
		}
	}

	r.friendships[accountID] = updated

	return nil
}

func (r *memoryFriendRepository) ListFriends(ctx context.Context, accountID uint32) ([]domain.Friendship, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	friendships, ok := r.friendships[accountID]
	if !ok {
		return []domain.Friendship{}, nil
	}

	result := make([]domain.Friendship, len(friendships))
	copy(result, friendships)

	return result, nil
}

func (r *memoryFriendRepository) ListFriendRequests(ctx context.Context, accountID uint32) ([]domain.FriendRequest, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var requests []domain.FriendRequest

	for _, req := range r.friendRequests {
		if (req.FromAccountID == accountID || req.ToAccountID == accountID) && req.Status == domain.FriendRequestPending {
			requests = append(requests, req)
		}
	}

	return requests, nil
}

func (r *memoryFriendRepository) GetPendingFriendRequests(ctx context.Context, accountID uint32) ([]domain.FriendRequest, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var requests []domain.FriendRequest

	for _, req := range r.friendRequests {
		if (req.FromAccountID == accountID || req.ToAccountID == accountID) && req.Status == domain.FriendRequestPending {
			requests = append(requests, req)
		}
	}

	return requests, nil
}

type memoryPartyRepository struct {
	mu        sync.RWMutex
	parties   map[string]domain.Party
	charIndex map[uint32]string
}

// NewMemoryPartyRepository creates a new in-memory party repository.
func NewMemoryPartyRepository() domain.PartyRepository {
	return &memoryPartyRepository{
		parties:   make(map[string]domain.Party),
		charIndex: make(map[uint32]string),
	}
}

func (r *memoryPartyRepository) CreateParty(ctx context.Context, party domain.Party) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.parties[party.ID] = party

	for _, member := range party.Members {
		r.charIndex[member.CharID] = party.ID
	}

	return nil
}

func (r *memoryPartyRepository) GetParty(ctx context.Context, partyID string) (domain.Party, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	party, ok := r.parties[partyID]
	if !ok {
		return domain.Party{}, fmt.Errorf("party %s not found", partyID)
	}

	return party, nil
}

func (r *memoryPartyRepository) GetPartyByMember(ctx context.Context, charID uint32) (domain.Party, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	partyID, ok := r.charIndex[charID]
	if !ok {
		return domain.Party{}, nil
	}

	party, ok := r.parties[partyID]
	if !ok {
		return domain.Party{}, nil
	}

	return party, nil
}

func (r *memoryPartyRepository) UpdateParty(ctx context.Context, party domain.Party) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.parties[party.ID]; !ok {
		return fmt.Errorf("party %s not found", party.ID)
	}

	for _, member := range party.Members {
		r.charIndex[member.CharID] = party.ID
	}

	oldParty := r.parties[party.ID]
	for _, oldMember := range oldParty.Members {
		stillInParty := false
		for _, newMember := range party.Members {
			if oldMember.CharID == newMember.CharID {
				stillInParty = true
				break
			}
		}
		if !stillInParty {
			delete(r.charIndex, oldMember.CharID)
		}
	}

	r.parties[party.ID] = party

	return nil
}

func (r *memoryPartyRepository) DeleteParty(ctx context.Context, partyID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	party, ok := r.parties[partyID]
	if !ok {
		return fmt.Errorf("party %s not found", partyID)
	}

	for _, member := range party.Members {
		delete(r.charIndex, member.CharID)
	}

	delete(r.parties, partyID)

	return nil
}

func (r *memoryPartyRepository) ListParties(ctx context.Context) ([]domain.Party, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	parties := make([]domain.Party, 0, len(r.parties))
	for _, party := range r.parties {
		parties = append(parties, party)
	}

	return parties, nil
}
