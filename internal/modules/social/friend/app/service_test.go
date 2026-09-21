package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/bouroo/goAthena/internal/modules/social/friend/app"
	"github.com/bouroo/goAthena/internal/modules/social/friend/domain"
	"github.com/bouroo/goAthena/internal/modules/social/friend/infra"
)

// buildFriendService wires a FriendService over an in-memory repo with two
// chars registered (Hero 150001, Partner 150002) plus a third (Rookie 150003).
func buildFriendService(t *testing.T) (*app.FriendService, *infra.MemoryFriendRepository) {
	t.Helper()
	repo := infra.NewMemoryFriendRepository()
	repo.SetChar(150001, infra.CharInfo{AccountID: 2000001, Name: "Hero", Online: true})
	repo.SetChar(150002, infra.CharInfo{AccountID: 2000002, Name: "Partner", Online: true})
	repo.SetChar(150003, infra.CharInfo{AccountID: 2000003, Name: "Rookie", Online: false})
	return app.NewFriendService(repo), repo
}

// TestFriend_AddIsBidirectional proves the auto_add shape: one Add makes BOTH
// lists read the other side, and the roster carries the joined identity.
func TestFriend_AddIsBidirectional(t *testing.T) {
	svc, _ := buildFriendService(t)
	ctx := context.Background()

	if err := svc.Add(ctx, 150001, 150002); err != nil {
		t.Fatalf("add: %v", err)
	}
	for owner, want := range map[uint32]uint32{150001: 150002, 150002: 150001} {
		friends, err := svc.List(ctx, owner)
		if err != nil {
			t.Fatalf("list %d: %v", owner, err)
		}
		if len(friends) != 1 {
			t.Fatalf("list %d: %d friends, want 1", owner, len(friends))
		}
		f := friends[0]
		if f.FriendCharID != want {
			t.Errorf("owner %d: friend = %d, want %d", owner, f.FriendCharID, want)
		}
		if f.Name != "Partner" && f.Name != "Hero" {
			t.Errorf("owner %d: friend name = %q", owner, f.Name)
		}
		if f.FriendAccountID == 0 {
			t.Errorf("owner %d: friend account id not joined", owner)
		}
		if !f.Online {
			t.Errorf("owner %d: friend should read online", owner)
		}
	}
}

// TestFriend_Rejects proves every sentinel: self-add, duplicate pair, and the
// two distinct full-list errors.
func TestFriend_Rejects(t *testing.T) {
	svc, repo := buildFriendService(t)
	ctx := context.Background()

	if err := svc.Add(ctx, 150001, 150001); !errors.Is(err, domain.ErrFriendSelf) {
		t.Errorf("self add: err = %v, want ErrFriendSelf", err)
	}
	if err := svc.Add(ctx, 150001, 150002); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := svc.Add(ctx, 150001, 150002); !errors.Is(err, domain.ErrFriendExists) {
		t.Errorf("duplicate add: err = %v, want ErrFriendExists", err)
	}

	// Fill the requester's list to the cap.
	for id := uint32(10); id < 10+domain.MaxFriends; id++ {
		repo.SetChar(id, infra.CharInfo{AccountID: id, Name: "Filler", Online: false})
		if err := svc.Add(ctx, 150003, id); err != nil {
			t.Fatalf("fill list: %v", err)
		}
	}
	if err := svc.Add(ctx, 150003, 150001); !errors.Is(err, domain.ErrFriendListFull) {
		t.Errorf("full requester: err = %v, want ErrFriendListFull", err)
	}
	// A full ACCEPTOR list surfaces as the distinct sentinel. Fill a fresh
	// char's list via reverse rows while the requester keeps room.
	const fullAcceptor = uint32(150004)
	repo.SetChar(fullAcceptor, infra.CharInfo{AccountID: 2000004, Name: "Crowded", Online: true})
	for id := uint32(100); id < 100+domain.MaxFriends; id++ {
		repo.SetChar(id, infra.CharInfo{AccountID: id, Name: "Filler2", Online: false})
		if err := svc.Add(ctx, id, fullAcceptor); err != nil {
			t.Fatalf("fill acceptor list: %v", err)
		}
	}
	if err := svc.Add(ctx, 150001, fullAcceptor); !errors.Is(err, domain.ErrAcceptorListFull) {
		t.Errorf("full acceptor: err = %v, want ErrAcceptorListFull", err)
	}
}

// TestFriend_RemoveIsBidirectional proves one Remove empties both lists and a
// repeat reports ErrFriendNotFound.
func TestFriend_RemoveIsBidirectional(t *testing.T) {
	svc, _ := buildFriendService(t)
	ctx := context.Background()

	if err := svc.Add(ctx, 150001, 150002); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := svc.Remove(ctx, 150001, 150002); err != nil {
		t.Fatalf("remove: %v", err)
	}
	for owner := range map[uint32]bool{150001: true, 150002: true} {
		friends, err := svc.List(ctx, owner)
		if err != nil {
			t.Fatalf("list %d: %v", owner, err)
		}
		if len(friends) != 0 {
			t.Errorf("owner %d: %d friends left after remove, want 0", owner, len(friends))
		}
	}
	if err := svc.Remove(ctx, 150001, 150002); !errors.Is(err, domain.ErrFriendNotFound) {
		t.Errorf("repeat remove: err = %v, want ErrFriendNotFound", err)
	}
}

// TestFriend_ListSkipsDeletedChars proves an orphaned pair (char row gone) is
// dropped from the read instead of surfacing a zero-identity friend.
func TestFriend_ListSkipsDeletedChars(t *testing.T) {
	svc, repo := buildFriendService(t)
	ctx := context.Background()

	if err := svc.Add(ctx, 150001, 150002); err != nil {
		t.Fatalf("add: %v", err)
	}
	repo.DropChar(150002) // the character row was deleted out from under the pair
	friends, err := svc.List(ctx, 150001)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(friends) != 0 {
		t.Errorf("list = %d friends, want 0 (char row gone)", len(friends))
	}
}
