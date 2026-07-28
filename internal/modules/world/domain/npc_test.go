//go:build unit

package domain_test

import (
	"bytes"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/aoi"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// healerNPC is a Kafra-shaped static NPC (sprite class 46) carrying the values
// the M11b ZC_SET_UNIT_IDLE frame encodes. It is the fixture the byte-exact
// assertion builds from.
func healerNPC(eid aoi.EntityID) *domain.NPC {
	return &domain.NPC{
		EntityID: eid,
		Sprite:   46,
		Name:     "Healer",
		MapName:  "prontera",
		PosX:     155, PosY: 165, Dir: 4,
		ScriptName: "Healer#prt",
	}
}

// TestNPCRegistry_NextEntityID_FromNPCBase asserts the allocator starts at
// NPCIDBase so NPC EntityIDs never collide with account_ids, mob ids (from
// MobIDBase), or floor-item ids (from FloorItemIDBase), and that each allocation
// is unique under concurrency (the CAS loop).
func TestNPCRegistry_NextEntityID_FromNPCBase(t *testing.T) {
	t.Parallel()
	r := domain.NewNPCRegistry()

	first := r.NextEntityID()
	assert.Equal(t, domain.NPCIDBase, uint32(first), "first NPC EntityID is NPCIDBase")
	assert.Equal(t, domain.NPCIDBase+1, uint32(r.NextEntityID()), "second allocation ascends")

	const n = 512
	ids := make(chan aoi.EntityID, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ids <- r.NextEntityID()
		}()
	}
	wg.Wait()
	close(ids)

	seen := make(map[aoi.EntityID]struct{}, n)
	for id := range ids {
		assert.GreaterOrEqual(t, uint32(id), domain.NPCIDBase, "NPC id stays in the NPC partition")
		_, dup := seen[id]
		assert.False(t, dup, "allocated id %d is unique", id)
		seen[id] = struct{}{}
	}
	assert.Len(t, seen, n, "every allocation is distinct")
}

// TestNPCRegistry_DisjointFromMobAndFloorItem asserts the ID partitions do not
// overlap: NPCIDBase sits above the realistic mob range and below the floor-item
// range, so a CZ_CONTACT_NPC id never resolves to a mob or a dropped item.
func TestNPCRegistry_DisjointFromMobAndFloorItem(t *testing.T) {
	t.Parallel()
	assert.Greater(t, domain.NPCIDBase, domain.MobIDBase, "NPC base is above the mob base")
	assert.Less(t, domain.NPCIDBase, domain.FloorItemIDBase, "NPC base is below the floor-item base")
}

// TestNPCRegistry_RegisterByEntityOnMap asserts the primary by-id index, the
// per-map inverted index, and idempotent Unregister.
func TestNPCRegistry_RegisterByEntityOnMap(t *testing.T) {
	t.Parallel()
	r := domain.NewNPCRegistry()
	npc := healerNPC(r.NextEntityID())

	require.NoError(t, r.Register(npc))
	assert.Equal(t, 1, r.Len())

	got, ok := r.ByEntity(npc.EntityID)
	require.True(t, ok)
	assert.Same(t, npc, got, "ByEntity returns the same pointer (shared broadcast state)")

	onMap := r.OnMap("prontera")
	require.Len(t, onMap, 1)
	assert.Same(t, npc, onMap[0])
	assert.Empty(t, r.OnMap("geffen"), "an unseeded map yields an empty (non-nil) snapshot")

	_, dup := r.ByEntity(999999)
	assert.False(t, dup, "unknown id resolves to (nil,false)")

	r.Unregister(npc.EntityID)
	r.Unregister(npc.EntityID)
	_, ok = r.ByEntity(npc.EntityID)
	assert.False(t, ok, "NPC is gone after unregister")
	assert.Empty(t, r.OnMap("prontera"))
	assert.Equal(t, 0, r.Len())
}

// TestNPCRegistry_Register_RejectsDuplicate asserts a second Register of the same
// EntityID fails with the sentinel rather than shadowing the first.
func TestNPCRegistry_Register_RejectsDuplicate(t *testing.T) {
	t.Parallel()
	r := domain.NewNPCRegistry()
	npc := healerNPC(r.NextEntityID())
	require.NoError(t, r.Register(npc))

	assert.ErrorIs(t, r.Register(healerNPC(npc.EntityID)), domain.ErrNPCAlreadyRegistered)
}

// TestNPCRegistry_Register_RejectsNil asserts the nil guard.
func TestNPCRegistry_Register_RejectsNil(t *testing.T) {
	t.Parallel()
	r := domain.NewNPCRegistry()
	assert.Error(t, r.Register(nil))
}

// TestNPC_SpawnUnit_ByteExact asserts the NPC ZC_SET_UNIT_IDLE frame is
// byte-for-byte the rAthena BL_NPC encoding for PACKETVER 20250604: ObjectType=6
// (NPC_EVT_TYPE), AID=EntityID, GID=0, Speed=0 (static), Job=sprite class,
// XSize=YSize=0, CLevel=0, MaxHP=HP=0 (no combat stats), and every equipment/view
// slot zeroed. The expected frame is encoded independently from the SUT so a
// drift in NPC.SpawnUnit's field map fails as a byte mismatch.
func TestNPC_SpawnUnit_ByteExact(t *testing.T) {
	t.Parallel()
	npc := healerNPC(aoi.EntityID(domain.NPCIDBase))

	var expected bytes.Buffer
	require.NoError(t, (packet.SetUnitIdleResponse{
		ObjectType: 6, // NPC_EVT_TYPE
		AID:        uint32(npc.EntityID),
		GID:        0,
		Speed:      0,
		Job:        npc.Sprite,
		PosX:       npc.PosX,
		PosY:       npc.PosY,
		Dir:        npc.Dir,
		XSize:      0,
		YSize:      0,
		CLevel:     0,
		MaxHP:      0,
		HP:         0,
		Name:       npc.Name,
	}).Encode(&expected))

	var actual bytes.Buffer
	require.NoError(t, npc.SpawnUnit().Encode(&actual))
	assert.Equal(t, expected.Bytes(), actual.Bytes(), "NPC spawn frame must match the rAthena BL_NPC encoding")
	assert.Equal(t, packet.SetUnitIdleResponse{}.Size(), actual.Len(), "frame is the fixed-size set-unit-idle")
}
