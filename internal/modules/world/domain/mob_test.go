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

// poringMob is a Poring-shaped Mob (mob_db id 1002) carrying the values the
// M5 spawn/walk frames encode: EntityID from MobIDBase, sprite=1002, level 1,
// WalkSpeed 400. It is the fixture the byte-exact assertions build from.
func poringMob(eid aoi.EntityID) *domain.Mob {
	return &domain.Mob{
		EntityID: eid,
		MobID:    1002,
		MapName:  "prontera",
		SpawnX:   155, SpawnY: 165,
		PosX: 155, PosY: 165, Dir: 0,
		Level: 1,
		MaxHP: 50, HP: 50,
		Name:      "Poring",
		WalkSpeed: 400,
	}
}

// TestMobRegistry_NextEntityID_FromMobBase asserts the allocator starts at
// MobIDBase (rAthena START_NPC_NUM) so mob EntityIDs never collide with player
// account_ids (which the AOI grid keys by id alone), and that each allocation is
// unique under concurrency.
func TestMobRegistry_NextEntityID_FromMobBase(t *testing.T) {
	t.Parallel()
	r := domain.NewMobRegistry()

	first := r.NextEntityID()
	assert.Equal(t, domain.MobIDBase, uint32(first), "first mob EntityID is MobIDBase")
	assert.Equal(t, domain.MobIDBase+1, uint32(r.NextEntityID()), "second allocation ascends")

	// Concurrency: 512 allocations from many goroutines must all be unique and
	// all stay in the mob partition (>= MobIDBase). This guards the CAS loop.
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
		assert.GreaterOrEqual(t, uint32(id), domain.MobIDBase, "mob id stays in the mob partition")
		_, dup := seen[id]
		assert.False(t, dup, "allocated id %d is unique", id)
		seen[id] = struct{}{}
	}
	assert.Len(t, seen, n, "every allocation is distinct")
}

// TestMobRegistry_RegisterByEntityOnMapMaps asserts the primary by-id index, the
// per-map inverted index, the Maps() enumeration, and idempotent Unregister.
func TestMobRegistry_RegisterByEntityOnMapMaps(t *testing.T) {
	t.Parallel()
	r := domain.NewMobRegistry()
	mob := poringMob(r.NextEntityID())

	require.NoError(t, r.Register(mob))

	got, ok := r.ByEntity(mob.EntityID)
	require.True(t, ok)
	assert.Same(t, mob, got, "ByEntity returns the same pointer (shared broadcast state)")

	onMap := r.OnMap("prontera")
	require.Len(t, onMap, 1)
	assert.Same(t, mob, onMap[0])
	assert.Empty(t, r.OnMap("geffen"), "an unseeded map yields an empty (non-nil) snapshot")

	assert.Contains(t, r.Maps(), "prontera")

	_, dup := r.ByEntity(999999)
	assert.False(t, dup, "unknown id resolves to (nil,false)")

	// Unregister is idempotent: the second call is a no-op, not a panic.
	r.Unregister(mob.EntityID)
	r.Unregister(mob.EntityID)
	_, ok = r.ByEntity(mob.EntityID)
	assert.False(t, ok, "mob is gone after unregister")
	assert.Empty(t, r.OnMap("prontera"))
	assert.Empty(t, r.Maps(), "empty map is pruned from Maps()")
}

// TestMobRegistry_Register_RejectsDuplicate asserts the unique allocator's
// contract: a second Register of the same EntityID fails with the sentinel
// rather than shadowing the first.
func TestMobRegistry_Register_RejectsDuplicate(t *testing.T) {
	t.Parallel()
	r := domain.NewMobRegistry()
	mob := poringMob(r.NextEntityID())
	require.NoError(t, r.Register(mob))

	assert.ErrorIs(t, r.Register(poringMob(mob.EntityID)), domain.ErrMobAlreadyRegistered)
}

// TestMobRegistry_Register_RejectsNil asserts the nil guard.
func TestMobRegistry_Register_RejectsNil(t *testing.T) {
	t.Parallel()
	r := domain.NewMobRegistry()
	assert.Error(t, r.Register(nil))
}

// TestMob_SpawnUnit_ByteExact asserts the mob ZC_SPAWN_UNIT frame is byte-for-byte
// the rAthena BL_MOB encoding for PACKETVER 20250604: ObjectType=5
// (NPC_MOB_TYPE), AID=EntityID, GID=0 (no map_session_data), Speed=mob WalkSpeed,
// Job=mob_db id (sprite), XSize=YSize=0 (mobs carry no 5/5 PC size hint),
// CLevel=mob Level, MaxHP=HP=-1 (HP bar hidden at full HP), and every
// equipment/view slot zeroed. The expected frame is encoded independently from
// the SUT so a drift in Mob.SpawnUnit's field map fails as a byte mismatch.
func TestMob_SpawnUnit_ByteExact(t *testing.T) {
	t.Parallel()
	mob := poringMob(aoi.EntityID(domain.MobIDBase))

	var expected bytes.Buffer
	require.NoError(t, (packet.SpawnUnitResponse{
		ObjectType: 5, // NPC_MOB_TYPE
		AID:        uint32(mob.EntityID),
		GID:        0, // non-PC char-code slot is unused
		Speed:      mob.WalkSpeed,
		Job:        int16(mob.MobID),
		PosX:       mob.PosX,
		PosY:       mob.PosY,
		Dir:        mob.Dir,
		XSize:      0, // mob size hint; 5/5 is PC-only
		YSize:      0,
		CLevel:     int16(mob.Level),
		MaxHP:      -1, // full-HP: HP bar appears only once damaged
		HP:         -1,
		Name:       mob.Name,
	}).Encode(&expected))

	var actual bytes.Buffer
	require.NoError(t, mob.SpawnUnit().Encode(&actual))
	assert.Equal(t, expected.Bytes(), actual.Bytes(), "mob spawn frame must match the rAthena BL_MOB encoding")
	assert.Equal(t, packet.SpawnUnitResponse{}.Size(), actual.Len(), "frame is the fixed 107-byte spawn unit")
}

// TestMob_WalkUnit_ByteExact asserts the mob ZC_UNIT_WALKING broadcast frame:
// the appearance slice mirrors SpawnUnit (a walk is a spawn-with-motion), with
// only the move fields (SrcX/SrcY/DestX/DestY/MoveStartTime) differing.
func TestMob_WalkUnit_ByteExact(t *testing.T) {
	t.Parallel()
	mob := poringMob(aoi.EntityID(domain.MobIDBase))
	const moveStart uint32 = 0x12345678
	const srcX, srcY, destX, destY int16 = 155, 165, 156, 165

	var expected bytes.Buffer
	require.NoError(t, (packet.UnitWalkingResponse{
		ObjectType:    5,
		AID:           uint32(mob.EntityID),
		GID:           0,
		Speed:         mob.WalkSpeed,
		Job:           int16(mob.MobID),
		MoveStartTime: moveStart,
		SrcX:          srcX,
		SrcY:          srcY,
		DestX:         destX,
		DestY:         destY,
		XSize:         0,
		YSize:         0,
		CLevel:        int16(mob.Level),
		MaxHP:         -1,
		HP:            -1,
		Name:          mob.Name,
	}).Encode(&expected))

	var actual bytes.Buffer
	require.NoError(t, mob.WalkUnit(srcX, srcY, destX, destY, moveStart).Encode(&actual))
	assert.Equal(t, expected.Bytes(), actual.Bytes(), "mob walk frame must match the rAthena BL_MOB walk encoding")
}

// TestMob_PositionSetPosition_RoundTrip asserts the locked position accessors
// round-trip a cell + facing, the contract the wander tick commits through.
func TestMob_PositionSetPosition_RoundTrip(t *testing.T) {
	t.Parallel()
	mob := poringMob(aoi.EntityID(domain.MobIDBase))
	mob.SetPosition(160, 170, 4)
	x, y, dir := mob.Position()
	assert.Equal(t, int16(160), x)
	assert.Equal(t, int16(170), y)
	assert.Equal(t, uint8(4), dir)
}
