// Package app implements the friend bounded context use cases: list, accept
// (bidirectional add), and remove (bidirectional delete).
//
// FriendService owns only the pair state machine. Who may request whom (both
// sides must be online) and where each result is delivered are gateway
// concerns — the service stays free of gnet and of the world registry.
package app

import (
	"context"
	"fmt"

	"github.com/bouroo/goAthena/internal/modules/social/friend/domain"
)

// FriendService is the friend use-case service.
type FriendService struct {
	repo domain.FriendRepository
}

// NewFriendService builds a FriendService backed by repo.
func NewFriendService(repo domain.FriendRepository) *FriendService {
	return &FriendService{repo: repo}
}

// List returns the char's friends ordered by friend char id. This is the order
// the gateway writes into the ZC_FRIENDS_LIST burst.
func (s *FriendService) List(ctx context.Context, charID uint32) ([]domain.Friend, error) {
	friends, err := s.repo.List(ctx, charID)
	if err != nil {
		return nil, fmt.Errorf("list friends: %w", err)
	}
	return friends, nil
}

// Add makes (requester, acceptor) mutual friends, inserting both directions in
// one transaction. Mirrors rAthena's accept path with friend_auto_add on
// (default, src/map/battle.cpp:8613): both lists gain the pair, and the
// caller notifies each side with its own ZC_ADD_FRIENDS(result=0).
//
// Capacity on BOTH lists is enforced here (requester → ErrFriendListFull /
// result 2, acceptor → ErrAcceptorListFull / result 3), matching the two
// full-checks rAthena splits across request time (clif.cpp:15442-15447) and
// reply time (clif.cpp:15511-15528).
func (s *FriendService) Add(ctx context.Context, requesterCharID, acceptorCharID uint32) error {
	if requesterCharID == acceptorCharID {
		return domain.ErrFriendSelf
	}
	if err := s.repo.Add(ctx, requesterCharID, acceptorCharID); err != nil {
		return fmt.Errorf("add friend pair: %w", err)
	}
	return nil
}

// Remove dissolves the friendship, deleting both directions in one transaction
// (rAthena clif_parse_FriendsListRemove deletes from the friend's list first,
// online or via the char-server, then from the requester's — one atomic
// operation here reproduces both paths).
func (s *FriendService) Remove(ctx context.Context, charID, friendCharID uint32) error {
	if err := s.repo.Remove(ctx, charID, friendCharID); err != nil {
		return fmt.Errorf("remove friend pair: %w", err)
	}
	return nil
}
