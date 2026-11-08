//go:build unit

package app_test

import (
	"context"
	"testing"

	"github.com/bouroo/goAthena/internal/modules/world/domain"
)

// TestOnlinePlayerCountCountsOnlyPlayers proves the count the Agones
// autoscaler scales on is the number of connected player characters.
//
// The count is published to Agones as the shard's player counter, and the
// fleet is sized from it. Counting anything else — a mob, a floor item —
// would inflate the load the autoscaler thinks it is serving and scale
// the fleet up for players who are not there.
func TestOnlinePlayerCountCountsOnlyPlayers(t *testing.T) {
	w := newWorld()
	ctx := context.Background()

	if got := w.OnlinePlayerCount(); got != 0 {
		t.Fatalf("count on an empty world = %d, want 0", got)
	}

	// A player who enters the world counts.
	if _, err := w.EnterMap(ctx, 150001); err != nil {
		t.Fatalf("enter map: %v", err)
	}
	if got := w.OnlinePlayerCount(); got != 1 {
		t.Errorf("count with one player = %d, want 1", got)
	}

	// A mob is not a player.
	if err := w.AddEntity(domain.Entity{ID: 900001, Map: "new_1-1", Type: domain.EntityTypeMob}); err != nil {
		t.Fatalf("add mob: %v", err)
	}
	if got := w.OnlinePlayerCount(); got != 1 {
		t.Errorf("count with a mob = %d, want 1 (a mob is not a player)", got)
	}

	// A second player counts.
	if err := w.AddEntity(domain.Entity{ID: 900002, Map: "new_1-1", Type: domain.EntityTypePC}); err != nil {
		t.Fatalf("add player: %v", err)
	}
	if got := w.OnlinePlayerCount(); got != 2 {
		t.Errorf("count with two players = %d, want 2", got)
	}

	// A departing player stops counting.
	if err := w.RemoveEntity(900002); err != nil {
		t.Fatalf("remove player: %v", err)
	}
	if got := w.OnlinePlayerCount(); got != 1 {
		t.Errorf("count after a player leaves = %d, want 1", got)
	}
}
