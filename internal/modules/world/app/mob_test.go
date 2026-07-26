//go:build unit

package app_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	chardomain "github.com/bouroo/goAthena/internal/modules/character/domain"
	gwdomain "github.com/bouroo/goAthena/internal/modules/gateway/domain"
	"github.com/bouroo/goAthena/internal/modules/world/app"
	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/aoi"
	"github.com/bouroo/goAthena/pkg/ro/mobdb"
	"github.com/bouroo/goAthena/pkg/ro/packet"
	"github.com/bouroo/goAthena/pkg/ro/romap"
)

// newMobMap builds a domain.Map with a real AOI grid AND an all-walkable
// romap.MapData. The Data is load-bearing: wanderStep checks destination
// walkability via mp.Data.IsWalkable, and newTestMap (AOI-only) would nil-panic
// there. Wander tests must use this, not newTestMap.
func newMobMap(width, height int) *domain.Map {
	walkable := make([]bool, width*height)
	for i := range walkable {
		walkable[i] = true
	}
	return &domain.Map{
		Data: &romap.MapData{Width: width, Height: height, Walkable: walkable},
		AOI:  aoi.NewGridManager(width, height),
	}
}

// TestMobService_WanderStep_MovesAndBroadcasts asserts one wander tick: the mob
// advances to the chosen adjacent walkable cell, and exactly one ZC_UNIT_WALKING
// frame — byte-identical to the independently-encoded expectation — reaches the
// observing player in AOI range. WanderStepForTest injects a fixed neighbor index
// so the move is deterministic; the dest is derived from the same offset table the
// tick reads (via the StepOffsetForTest seam), so the assertion is "the step the
// RNG selected actually committed and was broadcast," not a re-derivation of the
// table.
func TestMobService_WanderStep_MovesAndBroadcasts(t *testing.T) {
	t.Parallel()
	mobs := domain.NewMobRegistry()
	players := domain.NewPlayerRegistry()
	mp := newMobMap(200, 200)
	maps := &memMapStore{maps: map[string]*domain.Map{"prontera": mp}}
	const moveStart uint32 = 0x11223344
	svc := app.NewMobService(mobs, players, maps, nil, "", fixedClock{moveStart}, 0, nil)

	// A Poring at (100,100), registered and on the grid.
	mob := &domain.Mob{
		EntityID: mobs.NextEntityID(), MobID: 1002, MapName: "prontera",
		PosX: 100, PosY: 100, Level: 1, MaxHP: 50, HP: 50,
		Name: "Poring", WalkSpeed: 400,
	}
	require.NoError(t, mobs.Register(mob))
	require.NoError(t, mp.AOI.AddEntity(&aoi.Entity{ID: mob.EntityID, Type: aoi.EntityMob, X: 100, Y: 100}))

	// An observing player co-located on the source cell so it is within AOI range
	// of the broadcast (broadcastWalk queries visible entities at the source cell).
	// Its AOI entity id equals its account id — the key broadcastWalk resolves by.
	const obsAID uint32 = 2000500
	obsConn := &captureConn{role: gwdomain.RoleMap, remote: "obs"}
	require.NoError(t, players.Register(&domain.Player{
		Conn: obsConn, EntityID: aoi.EntityID(obsAID),
		AccountID: obsAID, MapName: "prontera", PosX: 100, PosY: 100,
	}))
	require.NoError(t, mp.AOI.AddEntity(&aoi.Entity{ID: aoi.EntityID(obsAID), Type: aoi.EntityPlayer, X: 100, Y: 100}))

	// stepIdx 3 → the east cardinal offset {1,0}; dest is (101,100), walkable.
	const stepIdx = 3
	off := app.StepOffsetForTest(stepIdx)
	destX, destY := 100+off[0], 100+off[1]
	svc.WanderStepForTest(mp, mob, stepIdx)

	// The mob's stored position advanced to the dest cell (SetPosition is the last
	// step of wanderStep, so this also implies MoveEntity committed).
	gotX, gotY, _ := mob.Position()
	assert.Equal(t, int16(destX), gotX, "mob moved to dest X")
	assert.Equal(t, int16(destY), gotY, "mob moved to dest Y")

	// The observer received exactly one walk frame, byte-exact.
	var want bytes.Buffer
	require.NoError(t, (packet.UnitWalkingResponse{
		ObjectType: 5, AID: uint32(mob.EntityID), GID: 0,
		Speed: 400, Job: 1002, MoveStartTime: moveStart,
		SrcX: 100, SrcY: 100, DestX: int16(destX), DestY: int16(destY),
		XSize: 0, YSize: 0, CLevel: 1, MaxHP: -1, HP: -1, Name: "Poring",
	}).Encode(&want))
	frames := splitFrames(t, obsConn.buf.Bytes())
	require.Len(t, frames, 1, "observer sees exactly one walk frame")
	assert.Equal(t, want.Bytes(), frames[0], "ZC_UNIT_WALKING frame bytes")
}

// TestMobService_WanderStep_BlockedCellIsNoOp asserts the walkability guard: a
// step toward a wall cell leaves the mob in place and emits nothing. wanderStep
// must check IsWalkable before broadcasting, so a blocked dest is a silent no-op,
// not a phantom walk into a wall.
func TestMobService_WanderStep_BlockedCellIsNoOp(t *testing.T) {
	t.Parallel()
	mobs := domain.NewMobRegistry()
	players := domain.NewPlayerRegistry()
	mp := newMobMap(200, 200)
	// Wall the cell east of the mob so the east step (stepIdx 3) is blocked.
	mp.Data.Walkable[100*200+101] = false
	maps := &memMapStore{maps: map[string]*domain.Map{"prontera": mp}}
	svc := app.NewMobService(mobs, players, maps, nil, "", fixedClock{0}, 0, nil)

	mob := &domain.Mob{
		EntityID: mobs.NextEntityID(), MobID: 1002, MapName: "prontera",
		PosX: 100, PosY: 100, Level: 1, MaxHP: 50, HP: 50,
		Name: "Poring", WalkSpeed: 400,
	}
	require.NoError(t, mobs.Register(mob))
	require.NoError(t, mp.AOI.AddEntity(&aoi.Entity{ID: mob.EntityID, Type: aoi.EntityMob, X: 100, Y: 100}))

	const obsAID uint32 = 2000501
	obsConn := &captureConn{role: gwdomain.RoleMap, remote: "obs"}
	require.NoError(t, players.Register(&domain.Player{
		Conn: obsConn, EntityID: aoi.EntityID(obsAID),
		AccountID: obsAID, MapName: "prontera", PosX: 100, PosY: 100,
	}))
	require.NoError(t, mp.AOI.AddEntity(&aoi.Entity{ID: aoi.EntityID(obsAID), Type: aoi.EntityPlayer, X: 100, Y: 100}))

	svc.WanderStepForTest(mp, mob, 3) // east → (101,100), walled

	gotX, gotY, _ := mob.Position()
	assert.Equal(t, int16(100), gotX, "mob did not move into a wall")
	assert.Equal(t, int16(100), gotY)
	assert.Empty(t, obsConn.buf.Bytes(), "no walk broadcast for a blocked step")
}

// TestSpawnService_EnterWorld_ShowsMobToNewcomer asserts the M5c spawn exchange:
// a mob already on the map is surfaced to an entering player as a ZC_SPAWN_UNIT
// with the BL_MOB encoding (ObjectType=5, sprite=mob_db id, no HP bar). The
// newcomer's stream is [self-spawn, mob-spawn]; the mob frame must be byte-exact
// against an independently-encoded expectation, not the mob's own SpawnUnit().
func TestSpawnService_EnterWorld_ShowsMobToNewcomer(t *testing.T) {
	t.Parallel()
	const aid, cid uint32 = 2000000, 150000
	chars := &fakeCharGetter{chars: map[uint64]chardomain.Character{
		charKey(aid, cid): aNovice(aid, cid), // lands at last_x/last_y = 53,111
	}}
	mp := newTestMap(200, 200)
	maps := &memMapStore{maps: map[string]*domain.Map{"prontera": mp}}
	registry := domain.NewPlayerRegistry()
	mobs := domain.NewMobRegistry()
	svc := app.NewSpawnService(chars, maps, registry, mobs)

	// Pre-place a Poring co-located with the novice spawn cell so it is in AOI range.
	mob := &domain.Mob{
		EntityID: mobs.NextEntityID(), MobID: 1002, MapName: "prontera",
		PosX: 53, PosY: 111, Level: 1, MaxHP: 50, HP: 50,
		Name: "Poring", WalkSpeed: 400,
	}
	require.NoError(t, mobs.Register(mob))
	require.NoError(t, mp.AOI.AddEntity(&aoi.Entity{ID: mob.EntityID, Type: aoi.EntityMob, X: 53, Y: 111}))

	conn := &captureConn{role: gwdomain.RoleMap, remote: "c"}
	require.NoError(t, svc.EnterWorld(context.Background(), conn, aid, cid, app.SpawnPoint{}))

	// [0] self-spawn (PC), [1] the mob's spawn — both byte-exact.
	frames := splitFrames(t, conn.buf.Bytes())
	require.Len(t, frames, 2, "newcomer sees self-spawn + the mob")
	assert.Equal(t, expectSpawnUnit(t, aid, cid, 53, 111, 0, "Tester"), frames[0], "first frame is the PC self-spawn")

	var wantMob bytes.Buffer
	require.NoError(t, (packet.SpawnUnitResponse{
		ObjectType: 5, AID: uint32(mob.EntityID), GID: 0,
		Speed: 400, Job: 1002, PosX: 53, PosY: 111, Dir: 0,
		XSize: 0, YSize: 0, CLevel: 1, MaxHP: -1, HP: -1, Name: "Poring",
	}).Encode(&wantMob))
	assert.Equal(t, wantMob.Bytes(), frames[1], "mob spawn frame is the BL_MOB encoding")
}

// TestMobService_SpawnAll_RegistersAllGroups asserts SpawnAll parses a spawn file
// of four groups, resolves each against mob_db, and registers four mobs — one per
// group — into both the MobRegistry and the map's AOI grid, each carrying its
// mob_db id/name/level/HP. A temp mob_db + temp prontera.yml keep the test hermetic
// (no dependency on repo-root data paths); the spawn file's basename drives the map
// name, so it is named prontera.yml to match the in-memory map store.
func TestMobService_SpawnAll_RegistersAllGroups(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Minimal mob_db: only the fields spawnOne reads (Id, Name, Level, Hp,
	// WalkSpeed). UnmarshalYAML defaults the rest to zero, so a sparse file parses.
	const mobDBYAML = `Header:
  Type: MOB_DB
  Version: 5
Body:
  - {Id: 1002, Name: Poring,  Level: 1,  Hp: 50,  WalkSpeed: 400}
  - {Id: 1063, Name: Lunatic, Level: 3,  Hp: 60,  WalkSpeed: 200}
  - {Id: 1113, Name: Drops,   Level: 3,  Hp: 55,  WalkSpeed: 400}
  - {Id: 1014, Name: Spore,   Level: 16, Hp: 510, WalkSpeed: 200}
`
	mobDBPath := filepath.Join(dir, "mob_db.yml")
	require.NoError(t, os.WriteFile(mobDBPath, []byte(mobDBYAML), 0o644))
	mobDB, err := mobdb.LoadFile(mobDBPath)
	require.NoError(t, err)
	require.Equal(t, 4, mobDB.Len())

	// Four spawn groups, count=1, pinned cells (ranges zero) — mirrors
	// data/mob_spawns/prontera.yml. Named prontera.yml ⇒ mapName "prontera".
	const spawnYAML = `spawns:
  - {mob_id: 1002, count: 1, x: 155, y: 165, x_range: 0, y_range: 0, respawn_ms: 5000}
  - {mob_id: 1063, count: 1, x: 165, y: 165, x_range: 0, y_range: 0, respawn_ms: 5000}
  - {mob_id: 1113, count: 1, x: 155, y: 175, x_range: 0, y_range: 0, respawn_ms: 5000}
  - {mob_id: 1014, count: 1, x: 165, y: 175, x_range: 0, y_range: 0, respawn_ms: 5000}
`
	spawnPath := filepath.Join(dir, "prontera.yml")
	require.NoError(t, os.WriteFile(spawnPath, []byte(spawnYAML), 0o644))

	mp := newTestMap(200, 200) // spawn does not pathfind; AOI only
	maps := &memMapStore{maps: map[string]*domain.Map{"prontera": mp}}
	mobs := domain.NewMobRegistry()
	svc := app.NewMobService(mobs, domain.NewPlayerRegistry(), maps, mobDB, spawnPath, fixedClock{0}, 0, nil)

	require.NoError(t, svc.SpawnAll(context.Background()))

	spawned := mobs.OnMap("prontera")
	require.Len(t, spawned, 4, "one mob per group registered")
	byID := make(map[int32]*domain.Mob, len(spawned))
	for _, m := range spawned {
		byID[m.MobID] = m
	}
	assert.Equal(t, "Poring", byID[1002].Name)
	assert.Equal(t, int32(1), byID[1002].Level)
	assert.Equal(t, "Lunatic", byID[1063].Name)
	assert.Equal(t, "Drops", byID[1113].Name)
	assert.Equal(t, "Spore", byID[1014].Name)
	assert.Equal(t, int32(510), byID[1014].MaxHP, "mob HP carries the mob_db value")
	// Poring was placed at its group's pinned cell.
	assert.Equal(t, int16(155), byID[1002].PosX)
	assert.Equal(t, int16(165), byID[1002].PosY)
	assert.Equal(t, 4, mp.AOI.EntityCount(), "every mob is also an AOI entity")
}
