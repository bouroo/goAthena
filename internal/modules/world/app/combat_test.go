//go:build unit

package app_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	chardomain "github.com/bouroo/goAthena/internal/modules/character/domain"
	gwdomain "github.com/bouroo/goAthena/internal/modules/gateway/domain"
	"github.com/bouroo/goAthena/internal/modules/world/app"
	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/aoi"
	"github.com/bouroo/goAthena/pkg/ro/itemdb"
	"github.com/bouroo/goAthena/pkg/ro/mobdb"
	"github.com/bouroo/goAthena/pkg/ro/mode"
	"github.com/bouroo/goAthena/pkg/ro/packet"
	"github.com/bouroo/goAthena/pkg/ro/skilldb"
	"github.com/bouroo/goAthena/pkg/ro/statcalc"
)

// splitFixedFrames slices a captured byte stream of fixed-length map packets into
// per-frame slices by reading each packet's [0:2] command id and looking up its
// declared on-wire size. Combat emits only fixed-size packets (ZC_NOTIFY_ACT=34,
// ZC_NOTIFY_VANISH=7) — these carry no length prefix, unlike the variable-length
// spawn/walk packets splitFrames handles, so the [2:4]-length-walk helper would
// misread the body bytes as a bogus length and overrun. packet.LenFor returns the
// declared size for a fixed cmd; an unknown or truncated frame is a hard failure.
func splitFixedFrames(t *testing.T, b []byte) [][]byte {
	t.Helper()
	sizes := map[uint16]int{
		packet.HeaderZCNOTIFYACT:         packet.NotifyActResponse{}.Size(),
		packet.HeaderZCNOTIFYVANISH:      packet.NotifyVanishResponse{}.Size(),
		packet.HeaderZCLONGLONGPARCHANGE: packet.LongLongParChangeResponse{}.Size(),
		packet.HeaderZCPARCHANGE:         packet.ParChangeResponse{}.Size(),
		packet.HeaderZCItemFallEntry:     (&packet.ItemFallEntryResponse{}).Size(),
		packet.HeaderZCItemPickupAck:     (&packet.ItemPickupAckResponse{}).Size(),
		packet.HeaderZCItemDisappear:     (&packet.ItemDisappearResponse{}).Size(),
		packet.HeaderZCUSEITEMACK2:       packet.UseItemAck2Response{}.Size(),
		// M14b: the cast-result ack (14 B) and the skill-hit broadcast (33 B).
		// Both are fixed-layout map packets; without these the fixture's
		// splitFixedFrames would hard-fail on the first cast (splitFixedFrames is
		// the universal combat-test frame splitter).
		packet.HeaderZCACKTOUSESKILL: packet.AckUseSkillResponse{}.Size(),
		packet.HeaderZCNOTIFYSKILL:   packet.NotifySkillResponse{}.Size(),
	}
	var out [][]byte
	for len(b) >= 2 {
		cmd := binary.LittleEndian.Uint16(b[0:2])
		size, ok := sizes[cmd]
		require.True(t, ok, "unknown fixed cmd 0x%04x in captured stream", cmd)
		require.LessOrEqual(t, size, len(b), "frame for cmd 0x%04x overruns the buffer", cmd)
		out = append(out, b[:size])
		b = b[size:]
	}
	require.Empty(t, b, "no trailing bytes after the last fixed frame")
	return out
}

// expectNotifyAct encodes the ZC_NOTIFY_ACT frame the combat service should emit
// for one melee hit, built independently from the SUT so a drift in any field —
// the damage value, the SrcID/TargetID wiring, the motion slots, or the server
// tick — surfaces as a byte mismatch. The field values mirror CombatService's
// broadcastDamage exactly (noviceAmotion/defaultDmotion, single non-SP hit,
// DMGNormal, no off-hand).
func expectNotifyAct(t *testing.T, srcID, targetGID, serverTick uint32, damage int32) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, (packet.NotifyActResponse{
		SrcID:      srcID,
		TargetID:   targetGID,
		ServerTick: serverTick,
		SrcSpeed:   432, // noviceAmotion (combat.go)
		DmgSpeed:   288, // defaultDmotion (combat.go)
		Damage:     damage,
		IsSPDamage: 0,
		Div:        1,
		Type:       packet.DMGNormal,
		Damage2:    0,
	}).Encode(&buf))
	return buf.Bytes()
}

// expectNotifyVanish encodes the ZC_NOTIFY_VANISH (death) frame for a mob, built
// independently so a drift in the GID or the vanish type is caught.
func expectNotifyVanish(t *testing.T, gid uint32) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, (packet.NotifyVanishResponse{
		GID:  gid,
		Type: packet.VanishDead,
	}).Encode(&buf))
	return buf.Bytes()
}

// expectItemFallEntry encodes a ZC_ITEM_FALL_ENTRY (0x0ADD) frame for a dropped
// item, built independently so a drift in any field — ground-item object id, name
// id, IT_* type, cell, or amount — surfaces as a byte mismatch. Mirrors
// broadcastFloorItem's field values (an unidentified drop, no MVP/random-option
// drop effect, sub-cell at the tile center).
func expectItemFallEntry(t *testing.T, id, nameID uint32, itemType uint16, x, y int16) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, (&packet.ItemFallEntryResponse{
		ID:             id,
		NameID:         nameID,
		Type:           itemType,
		Identified:     0,
		X:              uint16(x), //nolint:gosec // G115: map cell in int16 wire slot
		Y:              uint16(y), //nolint:gosec // G115: map cell in int16 wire slot
		Amount:         1,
		ShowDropEffect: 0,
		DropEffectMode: 0,
	}).Encode(&buf))
	return buf.Bytes()
}

// expectLongLongParChange encodes a ZC_LONGLONGPAR_CHANGE (64-bit status update)
// frame for the EXP path — at PACKETVER >= 20170830 EXP rides int64, so the
// expected frame is built from the 64-bit variant. Built independently so a drift
// in the var id or amount surfaces as a byte mismatch.
func expectLongLongParChange(t *testing.T, varID uint16, amount int64) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, (packet.LongLongParChangeResponse{VarID: varID, Amount: amount}).Encode(&buf))
	return buf.Bytes()
}

// expectParChange encodes a ZC_PAR_CHANGE (status update) frame for the
// level/status-point path.
func expectParChange(t *testing.T, varID uint16, count int32) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, (packet.ParChangeResponse{VarID: varID, Count: count}).Encode(&buf))
	return buf.Bytes()
}

// expectNotifySkill encodes a ZC_NOTIFY_SKILL (0x01de, 33-byte) frame the
// combat service should emit for one offensive-skill hit, built independently
// from the SUT so a drift in any field — SKID/AID/TargetID/Damage/Level/Count/
// Action — surfaces as a byte mismatch. Mirrors broadcastSkill's field values:
// noviceAmotion (432), defaultDmotion (288), Count=1 (BASH is single-hit),
// Action=DMGNormal (0). StartTime comes from the clock value the fixture's
// fixedClock stamps (0x11223344) so a wire-level match is feasible.
func expectNotifySkill(t *testing.T, skillID uint16, srcAccountID, targetEntityID uint32, damage int32, level int16, serverTick uint32) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, (packet.NotifySkillResponse{
		SKID:       skillID,
		AID:        srcAccountID,
		TargetID:   targetEntityID,
		StartTime:  serverTick,
		AttackMT:   432, // noviceAmotion (combat.go)
		AttackedMT: 288, // defaultDmotion (combat.go)
		Damage:     damage,
		Level:      level,
		Count:      1,
		Action:     int8(packet.DMGNormal), //nolint:gosec // int8 wire slot; DMG_NORMAL = regular offensive hit
	}).Encode(&buf))
	return buf.Bytes()
}

// expectAckUseSkill encodes a ZC_ACK_TOUSESKILL (0x0110, 14-byte) frame the
// combat service emits on the SP-insufficient reject path. Built independently
// so a drift in the SkillID or the Cause byte is caught. The pre-Renewal Bash
// slice only emits the negative path (Cause = UseSkillFailSPInsufficient = 12).
func expectAckUseSkill(t *testing.T, skillID uint16, cause uint8) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, (packet.AckUseSkillResponse{
		SkillID: skillID,
		Cause:   cause,
	}).Encode(&buf))
	return buf.Bytes()
}

// expectMobSpawnUnit encodes a ZC_SPAWN_UNIT frame for a (re)spawned mob, built
// from the same fields the domain Mob.SpawnUnit writes so a drift in either side
// surfaces as a byte mismatch. Mirrors spawn_test.go's expectSpawnUnit but for a
// mob (ObjectType=5, AID=EntityID, GID=0, no view/equipment fields).
func expectMobSpawnUnit(t *testing.T, m *domain.Mob) []byte {
	t.Helper()
	posX, posY, dir := m.Position()
	var buf bytes.Buffer
	require.NoError(t, (packet.SpawnUnitResponse{
		ObjectType: 5,
		AID:        uint32(m.EntityID), //nolint:gosec // G115: test EntityID → uint32 wire slot
		GID:        0,
		Speed:      m.WalkSpeed,
		Job:        int16(m.MobID), //nolint:gosec // G115: test mob id → int16 wire slot
		PosX:       posX,
		PosY:       posY,
		Dir:        dir,
		XSize:      0,
		YSize:      0,
		CLevel:     int16(m.Level), //nolint:gosec // G115: test level → int16 wire slot
		MaxHP:      -1,
		HP:         -1,
		Name:       m.Name,
	}).Encode(&buf))
	return buf.Bytes()
}

// recordingRespawnScheduler is the unit-test RespawnScheduler: it records each
// armed closure and its delay instead of firing it on a real timer, so the M6d
// respawn test drives the callback deterministically and asserts on its effect.
// It implements app.RespawnScheduler structurally.
type recordingRespawnScheduler struct {
	mu        sync.Mutex
	delay     time.Duration
	armed     int
	callbacks []func()
}

// After records the delay and closure; Run fires every recorded closure in arm
// order.
func (r *recordingRespawnScheduler) After(delay time.Duration, fn func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.delay = delay
	r.armed++
	r.callbacks = append(r.callbacks, fn)
}

// Armed reports how many respawns have been armed but not yet fired.
func (r *recordingRespawnScheduler) Armed() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.armed
}

// Delay reports the delay the most-recently-armed respawn carried.
func (r *recordingRespawnScheduler) Delay() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.delay
}

// Run fires every armed closure in arm order and returns the count fired.
func (r *recordingRespawnScheduler) Run() int {
	r.mu.Lock()
	cbs := r.callbacks
	r.mu.Unlock()
	for _, cb := range cbs {
		cb()
	}
	return len(cbs)
}

// combatFixture assembles the collaborators CombatService needs plus a mob and an
// attacking player co-located within melee range, returning everything the tests
// inspect. The attacker stands one cell east of the mob so the range check passes
// and the attacker sits inside the mob's AOI tower (so broadcastDamage reaches its
// conn). mobHP and mobDBEntry let each test dial the kill/level/DEF scenario.
type combatFixture struct {
	svc        *app.CombatService
	mobs       *domain.MobRegistry
	players    *domain.PlayerRegistry
	mp         *domain.Map
	mob        *domain.Mob
	attacker   *domain.Player
	conn       *captureConn // attacker's capturing conn (owns its buf)
	respawn    *recordingRespawnScheduler
	floorItems *domain.FloorItemRegistry
}

// combatFixtureOpt configures an optional combat-fixture collaborator. Defaults
// (drop-free testMobDB, no item_db) keep every non-drop test's frame counts
// exact; drop tests pass withItemDB/withMobDB to arm the M9c-1 drop path.
type combatFixtureOpt func(*combatFixtureCfg)

type combatFixtureCfg struct {
	items  *itemdb.Registry
	mobDB  *mobdb.Registry
	skills *skilldb.Registry
}

// withItemDB arms the M9c-1 drop path: a non-nil item_db lets dropLoot resolve a
// winning drop's AegisName to an item id/type. The default (nil items) disables
// drops so non-drop tests' frame counts stay exact.
func withItemDB(db *itemdb.Registry) combatFixtureOpt {
	return func(c *combatFixtureCfg) { c.items = db }
}

// withMobDB overrides the default drop-free testMobDB so a drop test can load a
// mob_db carrying a drop table.
func withMobDB(db *mobdb.Registry) combatFixtureOpt {
	return func(c *combatFixtureCfg) { c.mobDB = db }
}

// withSkillDB arms the M14b skill-cast path: a non-nil skill_db lets UseSkill
// resolve the SM_BASH entry, pay its SP cost, and emit ZC_NOTIFY_SKILL +
// ZC_ACK_TOUSESKILL. The default (nil skills) keeps every non-skill test's
// UseSkill a silent no-op, mirroring the nil-mob_db contract.
func withSkillDB(db *skilldb.Registry) combatFixtureOpt {
	return func(c *combatFixtureCfg) { c.skills = db }
}

// newCombatFixture seeds one mob (id, hp) at (100,100) on "prontera" and one
// level-clevel player at (101,100) whose conn buffers every broadcast. The mob_db
// entry for mobID comes from testMobDB (or a withMobDB override); combat reads
// DEF/VIT from it. chars is the character progression store awardKill
// reads/writes on a kill; pass nil for the M6b damage/vanish tests (awardKill then
// no-ops). The drop path is armed only when withItemDB supplies an item_db;
// otherwise dropLoot is a no-op so non-drop tests' frame counts stay exact. The
// attacker's (accountID, charID) pair is the fixed testIdentity below, so a fake
// store seeded against it resolves the killer.
func newCombatFixture(t *testing.T, mobID int32, mobHP int32, clevel uint16, chars domain.ProgressionStore, opts ...combatFixtureOpt) combatFixture {
	t.Helper()
	cfg := combatFixtureCfg{}
	for _, opt := range opts {
		opt(&cfg)
	}
	mobDB := cfg.mobDB
	if mobDB == nil {
		mobDB = testMobDB(t)
	}
	mobs := domain.NewMobRegistry()
	players := domain.NewPlayerRegistry()
	mp := newTestMap(200, 200)
	maps := &memMapStore{maps: map[string]*domain.Map{"prontera": mp}}

	mob := &domain.Mob{
		EntityID: mobs.NextEntityID(), MobID: mobID, MapName: "prontera",
		PosX: 100, PosY: 100, SpawnX: 100, SpawnY: 100,
		Level: 1, MaxHP: mobHP, HP: mobHP, Name: "Poring", WalkSpeed: 400,
	}
	require.NoError(t, mobs.Register(mob))
	require.NoError(t, mp.AOI.AddEntity(&aoi.Entity{ID: mob.EntityID, Type: aoi.EntityMob, X: 100, Y: 100}))

	const (
		aid    uint32 = 2000600
		charID uint32 = 150001
	)
	conn := &captureConn{role: gwdomain.RoleMap, remote: "attacker"}
	attacker := &domain.Player{
		Conn:      conn,
		EntityID:  aoi.EntityID(aid),
		AccountID: aid, CharID: charID, MapName: "prontera",
		PosX: 101, PosY: 100, CLevel: clevel,
		// Naked-novice base stats (all 1) — what the world spawns. computeDamage
		// reads these to build statcalc.Base; the damage-vectors test overrides
		// Str/Dex/Luk on selected cases to exercise a non-floored BaseATK.
		Str: 1, Agi: 1, Vit: 1, Int: 1, Dex: 1, Luk: 1,
	}
	require.NoError(t, players.Register(attacker))
	require.NoError(t, mp.AOI.AddEntity(&aoi.Entity{ID: aoi.EntityID(aid), Type: aoi.EntityPlayer, X: 101, Y: 100}))

	respawn := &recordingRespawnScheduler{}
	floorItems := domain.NewFloorItemRegistry()
	// M14b: the M14b skill-cast slice's last collaborator. The default (nil) keeps
	// every non-skill test's UseSkill a silent no-op; withSkillDB arms it.
	svc := app.NewCombatService(players, mobs, maps, mobDB, chars, fixedClock{0x11223344}, respawn, statcalc.PreRenewalSet, mode.PreRenewal, cfg.items, floorItems, nil, cfg.skills)
	return combatFixture{svc: svc, mobs: mobs, players: players, mp: mp, mob: mob, attacker: attacker, conn: conn, respawn: respawn, floorItems: floorItems}
}

// testMobDB returns a Registry with three mobs whose DEF/VIT exercise the
// damage-reduction paths the tests assert: Poring (no hard DEF, vit 1), Tank
// (hard DEF + vit soft-DEF), and Floored (hard DEF so high the pre-re floor at 1
// binds). BaseExp is included so the EXP tests have a nonzero award without a
// separate corpus; the damage-only tests never reach awardKill (they use
// chars=nil or non-killing HP), so the BaseExp values do not perturb those
// assertions.
func testMobDB(t *testing.T) *mobdb.Registry {
	t.Helper()
	const yaml = `Header:
  Type: MOB_DB
  Version: 5
Body:
  - {Id: 1002, Name: Poring,  Level: 1, Hp: 50,  Defense: 0,   Vit: 1, BaseExp: 5,   WalkSpeed: 400}
  - {Id: 9001, Name: Tank,    Level: 5, Hp: 100, Defense: 5,   Vit: 4, BaseExp: 100, WalkSpeed: 400}
  - {Id: 9002, Name: Floored, Level: 1, Hp: 100, Defense: 100, Vit: 0, BaseExp: 0,   WalkSpeed: 400}
`
	reg, err := mobdb.Load(strings.NewReader(yaml))
	require.NoError(t, err)
	return reg
}

// testSkillDB returns a Registry carrying a single SM_BASH entry (rAthena Id 5,
// Type=Weapon, TargetType=Attack, MaxLevel=10) — the only offensive weapon skill
// the M14b cast slice resolves. SpCost matches third_party/.../bash.cpp:8 (lv1-5
// cost 8 SP, lv6-10 cost 15 SP), so the SP-insufficient assertion can hold
// against the fixture's pre-seeded attacker SP. Built once per test via
// skilldb.Load.
func testSkillDB(t *testing.T) *skilldb.Registry {
	t.Helper()
	const yaml = `Header:
  Type: SKILL_DB
  Version: 4
Body:
  - Id: 5
    Name: SM_BASH
    MaxLevel: 10
    Type: Weapon
    TargetType: Attack
    Range: -1
    Requires:
      SpCost:
        - {Level: 1, Amount: 8}
        - {Level: 5, Amount: 8}
        - {Level: 6, Amount: 15}
        - {Level: 10, Amount: 15}
`
	reg, err := skilldb.Load(strings.NewReader(yaml))
	require.NoError(t, err)
	return reg
}

// dropItemDB is a minimal ITEM_DB v3 with the AegisNames the drop tests drop
// (Jellopy → Etc, Knife_ → Weapon). Built once per test via itemdb.Load.
func dropItemDB(t *testing.T) *itemdb.Registry {
	t.Helper()
	const yaml = `Header:
  Type: ITEM_DB
  Version: 3
Body:
  - {Id: 909, AegisName: Jellopy, Name: Jellopy, Type: Etc}
  - {Id: 919, AegisName: Knife_,  Name: Knife,   Type: Weapon}
`
	reg, err := itemdb.Load(strings.NewReader(yaml))
	require.NoError(t, err)
	return reg
}

// dropMobDB is a MOB_DB v5 with three one-hit (Hp 1) mobs exercising the drop
// paths the tests assert: 5001 Guaranteed (a rate-10000 Jellopy that always drops
// plus a rate-10000 Z_Unknown whose AegisName the drop item_db cannot resolve, so
// the unresolved path is covered in one kill); 5002 NeverDrops (a rate-0 entry
// that never rolls a win); 5003 EquipDrop (a rate-10000 Knife_ weapon drop,
// exercising the IT_WEAPON branch of itemdb.WireType).
func dropMobDB(t *testing.T) *mobdb.Registry {
	t.Helper()
	const yaml = `Header:
  Type: MOB_DB
  Version: 5
Body:
  - Id: 5001
    AegisName: GUARANTEED
    Name: Guaranteed
    Level: 1
    Hp: 1
    Defense: 0
    Vit: 1
    BaseExp: 0
    WalkSpeed: 400
    Drops:
      - Item: Jellopy
        Rate: 10000
      - Item: Z_Unknown
        Rate: 10000
  - Id: 5002
    AegisName: NEVERDROPS
    Name: NeverDrops
    Level: 1
    Hp: 1
    Defense: 0
    Vit: 1
    BaseExp: 0
    WalkSpeed: 400
    Drops:
      - Item: Jellopy
        Rate: 0
  - Id: 5003
    AegisName: EQUIPDROP
    Name: EquipDrop
    Level: 1
    Hp: 1
    Defense: 0
    Vit: 1
    BaseExp: 0
    WalkSpeed: 400
    Drops:
      - Item: Knife_
        Rate: 10000
`
	reg, err := mobdb.Load(strings.NewReader(yaml))
	require.NoError(t, err)
	return reg
}

// TestCombatService_Kill_DropsItemToGround is the M9c-1 happy path: killing a mob
// whose drop table has a rate-10000 (guaranteed) Jellopy drop — with an item_db
// that resolves Jellopy → id 909 (IT_ETC) — spawns exactly one floor item at the
// mob's death cell and broadcasts one byte-exact ZC_ITEM_FALL_ENTRY (0x0ADD) to the
// adjacent killer. The rate-10000 Z_Unknown drop in the same table is skipped
// (AegisName unresolved in item_db), proving the skip-without-crash path.
func TestCombatService_Kill_DropsItemToGround(t *testing.T) {
	t.Parallel()
	f := newCombatFixture(t, 5001, 1, 1, nil, withMobDB(dropMobDB(t)), withItemDB(dropItemDB(t)))

	require.NoError(t, f.svc.Attack(context.Background(), f.attacker.AccountID, packet.CZActionRequestRequest{
		TargetGID: uint32(f.mob.EntityID), Action: 0,
	}))

	// Exactly one floor item landed: Jellopy (909). Z_Unknown was skipped.
	require.Equal(t, 1, f.floorItems.Len(), "exactly one resolved drop landed")
	fi, ok := f.floorItems.ByEntity(aoi.EntityID(domain.FloorItemIDBase))
	require.True(t, ok, "first floor item drew the base EntityID")
	assert.Equal(t, uint32(909), fi.NameID, "Jellopy id")
	assert.Equal(t, uint16(3), fi.Type, "IT_ETC")
	assert.Equal(t, uint16(1), fi.Amount)
	assert.Equal(t, "prontera", fi.MapName)
	assert.Equal(t, int16(100), fi.PosX, "floor item lands on the mob's death cell")
	assert.Equal(t, int16(100), fi.PosY)

	// Frames: act, vanish, then the 0x0ADD drop frame.
	frames := splitFixedFrames(t, f.conn.buf.Bytes())
	require.Len(t, frames, 3, "act + vanish + drop")
	assert.Equal(t,
		expectItemFallEntry(t, uint32(fi.EntityID), 909, itemdb.WireType("Etc"), 100, 100),
		frames[2], "byte-exact ItemFallEntry for the Jellopy drop",
	)
}

// TestCombatService_Kill_RateZeroNeverDrops guards the roll floor: a rate-0 entry
// never rolls a win (rand.Intn(10000) < 0 is always false), so no floor item is
// spawned and no drop frame is broadcast — just act + vanish.
func TestCombatService_Kill_RateZeroNeverDrops(t *testing.T) {
	t.Parallel()
	f := newCombatFixture(t, 5002, 1, 1, nil, withMobDB(dropMobDB(t)), withItemDB(dropItemDB(t)))

	require.NoError(t, f.svc.Attack(context.Background(), f.attacker.AccountID, packet.CZActionRequestRequest{
		TargetGID: uint32(f.mob.EntityID), Action: 0,
	}))

	assert.Equal(t, 0, f.floorItems.Len(), "a rate-0 drop never lands")
	frames := splitFixedFrames(t, f.conn.buf.Bytes())
	require.Len(t, frames, 2, "act + vanish only; no drop frame")
}

// TestCombatService_Kill_NoItemDBDisablesDrops guards the items-nil contract: with
// no item_db wired, dropLoot is a no-op even for a rate-10000 drop, so a kill
// emits no floor item and no drop frame. This is the production default for a zone
// with no item_db configured.
func TestCombatService_Kill_NoItemDBDisablesDrops(t *testing.T) {
	t.Parallel()
	// No withItemDB ⇒ items nil. Mob 5001 carries a rate-10000 Jellopy drop.
	f := newCombatFixture(t, 5001, 1, 1, nil, withMobDB(dropMobDB(t)))

	require.NoError(t, f.svc.Attack(context.Background(), f.attacker.AccountID, packet.CZActionRequestRequest{
		TargetGID: uint32(f.mob.EntityID), Action: 0,
	}))

	assert.Equal(t, 0, f.floorItems.Len(), "no item_db ⇒ dropLoot no-ops")
	frames := splitFixedFrames(t, f.conn.buf.Bytes())
	require.Len(t, frames, 2, "act + vanish only")
}

// TestCombatService_Kill_DropsEquipmentType verifies the IT_* mapping for a
// non-Etc drop: a rate-10000 Knife_ (Weapon) drop resolves to IT_WEAPON (5) in the
// broadcast, exercising itemdb.WireType's weapon branch.
func TestCombatService_Kill_DropsEquipmentType(t *testing.T) {
	t.Parallel()
	f := newCombatFixture(t, 5003, 1, 1, nil, withMobDB(dropMobDB(t)), withItemDB(dropItemDB(t)))

	require.NoError(t, f.svc.Attack(context.Background(), f.attacker.AccountID, packet.CZActionRequestRequest{
		TargetGID: uint32(f.mob.EntityID), Action: 0,
	}))

	require.Equal(t, 1, f.floorItems.Len())
	fi, _ := f.floorItems.ByEntity(aoi.EntityID(domain.FloorItemIDBase))
	assert.Equal(t, uint32(919), fi.NameID, "Knife_ id")
	assert.Equal(t, uint16(5), fi.Type, "IT_WEAPON")
	assert.Equal(t,
		expectItemFallEntry(t, uint32(fi.EntityID), 919, itemdb.WireType("Weapon"), 100, 100),
		splitFixedFrames(t, f.conn.buf.Bytes())[2],
		"byte-exact ItemFallEntry for the Knife_ drop",
	)
}

// TestCombatService_Attack_HitBroadcastsAndWounds is the M6b happy path: a level-1
// player one cell east of a Poring attacks it. The hit broadcasts one byte-exact
// ZC_NOTIFY_ACT to the attacker, the mob's HP drops by the computed damage
// (1 = pre-re naked-novice BaseATK reduced by Poring's vit-softDEF and floored at
// 1), and the still-living mob is neither unregistered nor removed from the AOI
// grid.
func TestCombatService_Attack_HitBroadcastsAndWounds(t *testing.T) {
	t.Parallel()
	f := newCombatFixture(t, 1002, 50, 1, nil)

	require.NoError(t, f.svc.Attack(context.Background(), f.attacker.AccountID, packet.CZActionRequestRequest{
		TargetGID: uint32(f.mob.EntityID), Action: 0,
	}))

	// HP 50 − 1 = 49; the mob is wounded but alive.
	assert.Equal(t, int32(49), f.mob.HP, "mob HP reduced by exactly the melee damage")

	// Exactly one frame: the damage notification. No vanish on a non-killing hit.
	frames := splitFixedFrames(t, f.conn.buf.Bytes())
	require.Len(t, frames, 1, "one NotifyAct frame, no vanish while alive")
	assert.Equal(t,
		expectNotifyAct(t, f.attacker.AccountID, uint32(f.mob.EntityID), 0x11223344, 1),
		frames[0], "NotifyAct bytes (damage=1, SrcID=attacker, TargetID=mob)",
	)

	// The mob survives: still registered, still an AOI entity.
	_, stillRegistered := f.mobs.ByEntity(f.mob.EntityID)
	assert.True(t, stillRegistered, "living mob stays registered")
	assert.Equal(t, 2, f.mp.AOI.EntityCount(), "mob + attacker both remain in AOI")
}

// TestCombatService_Attack_KillingBlowVanishesAndTearsDown drives the death path:
// a mob with exactly the hit's HP (15) is killed in one blow. The attacker
// receives NotifyAct then NotifyVanish (in that order), the vanish frame is
// byte-exact, and the mob is torn out of both the registry and the AOI grid.
func TestCombatService_Attack_KillingBlowVanishesAndTearsDown(t *testing.T) {
	t.Parallel()
	f := newCombatFixture(t, 1002, 1, 1, nil) // HP == damage(1) ⇒ one-blow kill

	require.NoError(t, f.svc.Attack(context.Background(), f.attacker.AccountID, packet.CZActionRequestRequest{
		TargetGID: uint32(f.mob.EntityID), Action: 0,
	}))

	assert.Equal(t, int32(0), f.mob.HP, "mob HP driven to zero")

	// Two frames in order: the hit, then the death. The vanish must follow the act
	// so the client lands the damage number before the sprite disappears.
	frames := splitFixedFrames(t, f.conn.buf.Bytes())
	require.Len(t, frames, 2, "NotifyAct followed by NotifyVanish on the killing blow")
	assert.Equal(t,
		expectNotifyAct(t, f.attacker.AccountID, uint32(f.mob.EntityID), 0x11223344, 1),
		frames[0], "NotifyAct bytes on the killing blow",
	)
	assert.Equal(t,
		expectNotifyVanish(t, uint32(f.mob.EntityID)),
		frames[1], "NotifyVanish bytes (VanishDead)",
	)

	// Teardown: the dead mob is gone from both indexes.
	_, stillRegistered := f.mobs.ByEntity(f.mob.EntityID)
	assert.False(t, stillRegistered, "dead mob is unregistered")
	assert.Equal(t, 1, f.mp.AOI.EntityCount(), "mob removed from AOI; only the attacker remains")
}

// TestCombatService_Attack_DamageFollowsFormula asserts the pre-renewal damage
// pipeline through the emitted NotifyAct Damage field: BaseATK (str + (str/10)² +
// dex/5 + luk/5) is reduced by hard-DEF and the mob's vit-softDEF, then floored
// at 1. The fixture's attacker starts as a naked novice (all stats 1); each case
// overrides Str/Dex/Luk to dial the BaseATK. Pre-re BaseATK has no level term, so
// a naked novice deals 1 to every mob — the strong-attacker cases exercise the
// reduction paths distinctly.
func TestCombatService_Attack_DamageFollowsFormula(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		mobID         int32
		str, dex, luk uint16 // overrides the naked-novice baseline (all 1)
		wantDmg       int32
	}{
		// pre-re batk = str + (str/10)² + dex/5 + luk/5; dmg = batk*(100-def1)/100 - vit_def, floor 1
		{"naked_novice_vs_poring_floored", 1002, 1, 1, 1, 1}, // batk 1; 1·100/100 − 1 = 0 → floor 1
		{"strong_vs_poring", 1002, 50, 30, 10, 82},           // batk 83; 83·100/100 − 1 = 82
		{"strong_vs_tank_reduced", 9001, 50, 30, 10, 74},     // batk 83; 83·95/100 − 4 = 78 − 4 = 74
		{"strong_vs_floored_clamped", 9002, 50, 30, 10, 1},   // batk 83; 83·0/100 − 0 = 0 → floor 1
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newCombatFixture(t, tc.mobID, 10000, 1, nil) // HP huge ⇒ never kills
			f.attacker.Str, f.attacker.Dex, f.attacker.Luk = tc.str, tc.dex, tc.luk

			require.NoError(t, f.svc.Attack(context.Background(), f.attacker.AccountID, packet.CZActionRequestRequest{
				TargetGID: uint32(f.mob.EntityID), Action: 0,
			}))

			frames := splitFixedFrames(t, f.conn.buf.Bytes())
			require.Len(t, frames, 1)
			assert.Equal(t,
				expectNotifyAct(t, f.attacker.AccountID, uint32(f.mob.EntityID), 0x11223344, tc.wantDmg),
				frames[0], "damage bytes for %s", tc.name,
			)
		})
	}
}

// TestCombatService_Attack_OutOfRangeIsSilent asserts the melee-range guard: a
// player 5 cells from the mob issues an attack that is dropped silently — no frame
// is written and the mob's HP is untouched. The client is expected to have moved
// adjacent first; the server does not rubber-band it.
func TestCombatService_Attack_OutOfRangeIsSilent(t *testing.T) {
	t.Parallel()
	f := newCombatFixture(t, 1002, 50, 1, nil)
	// Move the attacker five cells away from the mob at (100,100).
	f.attacker.SetPosition(105, 105, 0)
	require.NoError(t, f.mp.AOI.RemoveEntity(aoi.EntityID(f.attacker.AccountID)))
	require.NoError(t, f.mp.AOI.AddEntity(&aoi.Entity{ID: aoi.EntityID(f.attacker.AccountID), Type: aoi.EntityPlayer, X: 105, Y: 105}))

	require.NoError(t, f.svc.Attack(context.Background(), f.attacker.AccountID, packet.CZActionRequestRequest{
		TargetGID: uint32(f.mob.EntityID), Action: 0,
	}))

	assert.Empty(t, f.conn.buf.Bytes(), "out-of-range attack writes nothing")
	assert.Equal(t, int32(50), f.mob.HP, "out-of-range attack deals no damage")
}

// TestCombatService_Attack_NonMobTargetIsSilent asserts a TargetGID that resolves
// to no live mob (a player, an NPC, or a stale id) is a silent no-op: the slice
// attacks mobs only, so a miss drops without error or broadcast.
func TestCombatService_Attack_NonMobTargetIsSilent(t *testing.T) {
	t.Parallel()
	f := newCombatFixture(t, 1002, 50, 1, nil)

	require.NoError(t, f.svc.Attack(context.Background(), f.attacker.AccountID, packet.CZActionRequestRequest{
		TargetGID: 999999, Action: 0, // no mob has this EntityID
	}))
	assert.Empty(t, f.conn.buf.Bytes(), "attack on an unknown target writes nothing")
	assert.Equal(t, int32(50), f.mob.HP, "unknown target is not wounded")
}

// TestCombatService_Attack_NoLiveSessionIsSilent asserts an attack from an
// account with no live player session (never entered the world, or already
// disconnected) is dropped silently rather than faulting.
func TestCombatService_Attack_NoLiveSessionIsSilent(t *testing.T) {
	t.Parallel()
	f := newCombatFixture(t, 1002, 50, 1, nil)

	require.NoError(t, f.svc.Attack(context.Background(), 888888, packet.CZActionRequestRequest{
		TargetGID: uint32(f.mob.EntityID), Action: 0,
	}))
	assert.Empty(t, f.conn.buf.Bytes(), "attack from a stranger writes nothing")
	assert.Equal(t, int32(50), f.mob.HP, "mob is not wounded by a stranger")
}

// TestMob_ApplyDamage_KillCreditAwardedOnce is the atomic kill-credit proof: when
// many attackers each deal lethal damage to a mob whose remaining HP cannot cover
// them all, exactly one call returns died=true — the death is awarded once, never
// double-counted. It also asserts a late hit against the now-dead mob is a no-op.
func TestMob_ApplyDamage_KillCreditAwardedOnce(t *testing.T) {
	t.Parallel()
	// HP 10; ten attackers each deal 5 (each individually lethal). At most one
	// of the ten can be the killer.
	const hp int32 = 10
	const damage int32 = 5
	const attackers = 10
	mob := &domain.Mob{HP: hp, MaxHP: hp}

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		kills    int
		noOps    int // calls against the already-dead mob
		finalHPs = make([]int32, 0, attackers)
	)
	wg.Add(attackers)
	for range attackers {
		go func() {
			defer wg.Done()
			_, died := mob.ApplyDamage(damage)
			mu.Lock()
			defer mu.Unlock()
			if died {
				kills++
			}
		}()
	}
	wg.Wait()

	// Exactly one kill credit, regardless of scheduling.
	assert.Equal(t, 1, kills, "exactly one attacker is credited the kill")

	// Every subsequent hit is a no-op: died=false, and HP stays at zero.
	for range 3 {
		newHP, died := mob.ApplyDamage(damage)
		assert.False(t, died, "a hit on a dead mob is never a kill")
		assert.Equal(t, int32(0), newHP, "a dead mob's HP stays zero")
		noOps++
	}
	finalHPs = append(finalHPs, mob.HP)
	assert.Equal(t, 3, noOps)
	assert.Equal(t, int32(0), finalHPs[0], "mob HP is zero after the contested kill")
}

// TestActionHandler_NoAuth asserts a CZ_ACTION_REQUEST on a connection that never
// completed the CZ_ENTER gate (no cached account) is rejected with an error rather
// than resolving an attack for account 0 — the impersonation guard shared with the
// movement handler.
func TestActionHandler_NoAuth(t *testing.T) {
	t.Parallel()
	f := newCombatFixture(t, 1002, 50, 1, nil)
	h := app.NewActionHandler(f.svc)

	var req bytes.Buffer
	require.NoError(t, (packet.CZActionRequestRequest{TargetGID: uint32(f.mob.EntityID), Action: 0}).Encode(&req))

	err := h.Handle(context.Background(), &captureConn{role: gwdomain.RoleMap}, gwdomain.Frame{
		Cmd: packet.HeaderCZACTIONREQUEST, Raw: req.Bytes(),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no verified account")
	assert.Equal(t, int32(50), f.mob.HP, "no attack resolved without auth")
}

// TestActionHandler_ParseError asserts a malformed CZ_ACTION_REQUEST frame is
// rejected with a parse error (so ProcessBytes logs it) rather than panicking or
// resolving a zero-target attack.
func TestActionHandler_ParseError(t *testing.T) {
	t.Parallel()
	f := newCombatFixture(t, 1002, 50, 1, nil)
	h := app.NewActionHandler(f.svc)

	err := h.Handle(context.Background(),
		&captureConn{role: gwdomain.RoleMap, auth: gwdomain.ConnAuth{AccountID: f.attacker.AccountID}},
		gwdomain.Frame{Cmd: packet.HeaderCZACTIONREQUEST, Raw: []byte{0x89, 0x00}}, // too short
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CZ_ACTION_REQUEST")
}

// TestActionHandler_DelegatesToService asserts the handler takes identity from the
// connection's verified auth (never the packet) and delegates a valid attack to
// the service: the mob is wounded and the attacker receives the NotifyAct frame.
func TestActionHandler_DelegatesToService(t *testing.T) {
	t.Parallel()
	f := newCombatFixture(t, 1002, 50, 1, nil)
	h := app.NewActionHandler(f.svc)

	var req bytes.Buffer
	require.NoError(t, (packet.CZActionRequestRequest{TargetGID: uint32(f.mob.EntityID), Action: 0}).Encode(&req))

	require.NoError(t, h.Handle(context.Background(),
		&captureConn{role: gwdomain.RoleMap, auth: gwdomain.ConnAuth{AccountID: f.attacker.AccountID}},
		gwdomain.Frame{Cmd: packet.HeaderCZACTIONREQUEST, Raw: req.Bytes()},
	))

	assert.Equal(t, int32(49), f.mob.HP, "handler drove the service's attack")
	frames := splitFixedFrames(t, f.conn.buf.Bytes())
	require.Len(t, frames, 1)
	assert.Equal(t,
		expectNotifyAct(t, f.attacker.AccountID, uint32(f.mob.EntityID), 0x11223344, 1),
		frames[0], "NotifyAct emitted through the handler path",
	)
}

// memProgressionStore is an in-memory domain.ProgressionStore for the M6c EXP/level
// tests. It holds one character (the test's attacker), keyed by (accountID,
// charID), and records the last Progression written so a test can assert the
// persisted EXP/level. GetByID returns a copy so awardKill's in-memory mutation
// does not alias the stored row.
type memProgressionStore struct {
	accountID uint32
	charID    uint32
	char      chardomain.Character
	saved     chardomain.Progression
	savedOK   bool
	saveErr   error // injected fault, returned from SaveProgression when non-nil
}

func (s *memProgressionStore) GetByID(_ context.Context, accountID, charID uint32) (*chardomain.Character, error) {
	if accountID != s.accountID || charID != s.charID {
		return nil, chardomain.ErrCharacterNotFound
	}
	cp := s.char
	return &cp, nil
}

func (s *memProgressionStore) SaveProgression(_ context.Context, accountID, charID uint32, p chardomain.Progression) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	if accountID != s.accountID || charID != s.charID {
		return chardomain.ErrCharacterNotFound
	}
	// Apply to the live char so a subsequent GetByID reflects the delta (mirrors the
	// GORM adapter writing through).
	s.char.BaseExp = p.BaseExp
	s.char.BaseLevel = p.BaseLevel
	s.char.StatusPoint = p.StatusPoint
	s.saved = p
	s.savedOK = true
	return nil
}

// newProgressionFixture builds a combat fixture wired with a memProgressionStore
// holding a fresh novice (base_level 1, base_exp 0, status_point 48) at the test's
// fixed (accountID, charID). It returns the store so the EXP/level test can assert
// the persisted state after the kill.
func newProgressionFixture(t *testing.T, mobID int32, mobHP int32) (*memProgressionStore, combatFixture) {
	t.Helper()
	const (
		aid    uint32 = 2000600
		charID uint32 = 150001
	)
	store := &memProgressionStore{
		accountID: aid,
		charID:    charID,
		char:      chardomain.Character{CharID: charID, AccountID: aid, BaseLevel: 1, StatusPoint: 48},
	}
	return store, newCombatFixture(t, mobID, mobHP, 1, store)
}

// TestCombatService_Kill_AwardsExpNoLevelUp kills a Poring (BaseExp 5) from a
// fresh novice (level 1, base_exp 0). The award (5) is below the level-2
// threshold (10), so the kill broadcasts exactly the EXP update (LongLongParChange)
// and no level/status-point frames. The persisted progression carries base_exp=5,
// base_level unchanged at 1.
func TestCombatService_Kill_AwardsExpNoLevelUp(t *testing.T) {
	t.Parallel()
	store, f := newProgressionFixture(t, 1002, 1) // HP 1 == one-blow kill (naked novice floors at 1)

	require.NoError(t, f.svc.Attack(context.Background(), f.attacker.AccountID, packet.CZActionRequestRequest{
		TargetGID: uint32(f.mob.EntityID), Action: 0,
	}))

	// Frames: NotifyAct, NotifyVanish, then the per-player EXP update. No level/
	// status-point frames (5 < level-2 threshold 10).
	frames := splitFixedFrames(t, f.conn.buf.Bytes())
	require.Len(t, frames, 3, "act + vanish + exp update")
	assert.Equal(t,
		expectLongLongParChange(t, packet.SPBaseExp, 5), frames[2],
		"base EXP update to the killer only",
	)

	require.True(t, store.savedOK, "progression was persisted")
	assert.Equal(t, uint64(5), store.saved.BaseExp, "persisted base_exp")
	assert.Equal(t, uint16(1), store.saved.BaseLevel, "no level-up below threshold")
	assert.Equal(t, uint32(48), store.saved.StatusPoint, "status points unchanged")
}

// TestCombatService_Kill_LevelUpsAndGrantsStatusPoints kills a high-EXP mob
// (BaseExp 100) from a fresh novice. The award pushes base_exp past the level-2
// (10), level-3 (30), and level-4 (70) thresholds — stopping at level 4 (100 <
// 150). The kill broadcasts the EXP update plus the level and status-point
// updates (+3/level × 3 levels = 9), and persists base_level=4, base_exp=100,
// status_point=57.
func TestCombatService_Kill_LevelUpsAndGrantsStatusPoints(t *testing.T) {
	t.Parallel()
	store, f := newProgressionFixture(t, 9001, 1) // Tank: BaseExp 100; HP 1 == one-blow kill (naked novice floors at 1)

	require.NoError(t, f.svc.Attack(context.Background(), f.attacker.AccountID, packet.CZActionRequestRequest{
		TargetGID: uint32(f.mob.EntityID), Action: 0,
	}))

	// 100 EXP → levels 1→2 (10), 2→3 (30), 3→4 (70); 100 < 150 so stops at L4.
	frames := splitFixedFrames(t, f.conn.buf.Bytes())
	require.Len(t, frames, 5, "act + vanish + exp + level + statuspoint")
	assert.Equal(t, expectLongLongParChange(t, packet.SPBaseExp, 100), frames[2], "EXP update")
	assert.Equal(t, expectParChange(t, packet.SPBaseLevel, 4), frames[3], "base level → 4")
	assert.Equal(t, expectParChange(t, packet.SPStatusPoint, 57), frames[4], "status points 48 + 3*3")

	require.True(t, store.savedOK)
	assert.Equal(t, uint64(100), store.saved.BaseExp)
	assert.Equal(t, uint16(4), store.saved.BaseLevel)
	assert.Equal(t, uint32(57), store.saved.StatusPoint)
}

// TestCombatService_Kill_ZeroExpMobAwardsNothing kills a mob whose mob_db entry
// grants no base EXP (Floored, BaseExp 0). The death path still tears the mob
// down (vanish + unregister), but awardKill is a no-op: no EXP/level frames and
// no persist write. This guards the entry.BaseExp<=0 early return.
func TestCombatService_Kill_ZeroExpMobAwardsNothing(t *testing.T) {
	t.Parallel()
	store, f := newProgressionFixture(t, 9002, 1) // Floored: BaseExp 0; dmg floored to 1 ⇒ HP 1 one-blow kill

	require.NoError(t, f.svc.Attack(context.Background(), f.attacker.AccountID, packet.CZActionRequestRequest{
		TargetGID: uint32(f.mob.EntityID), Action: 0,
	}))

	// Only act + vanish — no status frames, no persist.
	frames := splitFixedFrames(t, f.conn.buf.Bytes())
	require.Len(t, frames, 2, "act + vanish only; zero-EXP mob awards nothing")
	assert.False(t, store.savedOK, "zero-EXP kill does not persist")
	_, stillRegistered := f.mobs.ByEntity(f.mob.EntityID)
	assert.False(t, stillRegistered, "mob still torn down on a zero-EXP kill")
}

// TestCombatService_Kill_PersistFaultStillTearsDown injects a SaveProgression
// fault. The persist error is surfaced (returned, so ProcessBytes logs it), but
// the mob is still unregistered and removed from the AOI — a persistence fault
// never keeps a corpse alive. The status broadcasts have already gone out
// (best-effort), so the client saw the EXP even though the save failed.
func TestCombatService_Kill_PersistFaultStillTearsDown(t *testing.T) {
	t.Parallel()
	store, f := newProgressionFixture(t, 1002, 1)
	store.saveErr = errors.New("disk full")

	err := f.svc.Attack(context.Background(), f.attacker.AccountID, packet.CZActionRequestRequest{
		TargetGID: uint32(f.mob.EntityID), Action: 0,
	})

	require.Error(t, err, "persist fault surfaces as an error")
	assert.Contains(t, err.Error(), "persist killer progression")

	// EXP update was sent before the failed persist...
	frames := splitFixedFrames(t, f.conn.buf.Bytes())
	require.Len(t, frames, 3, "act + vanish + exp (sent before the save faulted)")

	// ...and the mob is torn down regardless.
	_, stillRegistered := f.mobs.ByEntity(f.mob.EntityID)
	assert.False(t, stillRegistered, "mob torn down even when persist faults")
	assert.Equal(t, 1, f.mp.AOI.EntityCount(), "mob removed from AOI even when persist faults")
}

// TestCombatService_Kill_AtCapAccruesExpNoLevel seeds a character already at the
// table's level cap (10). A kill still accrues and persists EXP, but the level-up
// loop's maxLevel bound keeps base_level pinned at the cap — no further level or
// status-point frames are emitted. Guards against the loop running off the end of
// noviceBaseExpThresholds.
func TestCombatService_Kill_AtCapAccruesExpNoLevel(t *testing.T) {
	t.Parallel()
	store, f := newProgressionFixture(t, 1002, 1) // Poring: BaseExp 5; HP 1 == one-blow kill
	// Place the character at the cap (level 10) with EXP already past the final
	// threshold, so the award accrues but cannot level further.
	store.char.BaseLevel = 10
	store.char.BaseExp = 6000
	store.char.StatusPoint = 90

	require.NoError(t, f.svc.Attack(context.Background(), f.attacker.AccountID, packet.CZActionRequestRequest{
		TargetGID: uint32(f.mob.EntityID), Action: 0,
	}))

	// 6000 + 5 = 6005 EXP; at the cap, only the EXP frame goes out.
	frames := splitFixedFrames(t, f.conn.buf.Bytes())
	require.Len(t, frames, 3, "act + vanish + exp; no level/status at cap")
	assert.Equal(t, expectLongLongParChange(t, packet.SPBaseExp, 6005), frames[2])

	require.True(t, store.savedOK)
	assert.Equal(t, uint16(10), store.saved.BaseLevel, "level pinned at the table cap")
	assert.Equal(t, uint64(6005), store.saved.BaseExp, "EXP accrues past the cap")
	assert.Equal(t, uint32(90), store.saved.StatusPoint, "no status-point grant at the cap")
}

// TestCombatService_Kill_ArmsRespawnThenReappears is the M6d happy path: a killed
// mob is torn down and a respawn is armed for its home cell. Before the respawn
// fires, the death path emits only act+vanish (the EXP frame is absent because
// chars is nil — awardKill no-ops, isolating the respawn assertion from the M6c
// progression path). Firing the armed closure spawns a fresh replacement — new
// EntityID (a new lifetime, never the dead mob's id), full HP, at SpawnX/SpawnY —
// re-registers it, re-inserts it into the AOI, and broadcasts one byte-exact
// ZC_SPAWN_UNIT to the adjacent attacker.
func TestCombatService_Kill_ArmsRespawnThenReappears(t *testing.T) {
	t.Parallel()
	f := newCombatFixture(t, 1002, 1, 1, nil) // Poring HP 1 == damage(1) ⇒ one-blow kill
	deadID := f.mob.EntityID

	require.NoError(t, f.svc.Attack(context.Background(), f.attacker.AccountID, packet.CZActionRequestRequest{
		TargetGID: uint32(deadID), Action: 0,
	}))

	// Death path: act + vanish only. The respawn is armed but deferred — no spawn
	// frame is emitted until the closure fires, so the synchronous kill is still
	// exactly two frames (chars nil ⇒ no EXP frame).
	frames := splitFixedFrames(t, f.conn.buf.Bytes())
	require.Len(t, frames, 2, "act + vanish; no spawn frame before the respawn fires")
	require.Equal(t, 1, f.respawn.Armed(), "exactly one respawn armed for the kill")
	assert.Equal(t, app.MobRespawnDelay, f.respawn.Delay(), "armed with the slice respawn delay")

	// The dead mob is fully torn down: gone from the registry and the AOI grid.
	_, gone := f.mobs.ByEntity(deadID)
	require.False(t, gone, "dead mob unregistered")
	assert.Equal(t, 1, f.mp.AOI.EntityCount(), "only the attacker remains in the AOI")

	// Fire the armed respawn (production: time.AfterFunc after MobRespawnDelay).
	beforeSpawn := f.conn.buf.Len()
	fired := f.respawn.Run()
	require.Equal(t, 1, fired, "one armed closure fired")

	// A fresh replacement is back on the map: new EntityID, same sprite, home cell,
	// full HP — the world refilled.
	live := f.mobs.OnMap("prontera")
	require.Len(t, live, 1, "one replacement mob registered")
	assert.NotEqual(t, deadID, live[0].EntityID, "replacement gets a fresh EntityID")
	assert.Equal(t, int32(1002), live[0].MobID, "same mob_db id (sprite preserved)")
	lx, ly, _ := live[0].Position()
	assert.Equal(t, int16(100), lx, "respawned at SpawnX")
	assert.Equal(t, int16(100), ly, "respawned at SpawnY")
	assert.Equal(t, live[0].MaxHP, live[0].HP, "full HP on respawn")
	assert.Equal(t, 2, f.mp.AOI.EntityCount(), "replacement mob + attacker back in the AOI")

	// Exactly one ZC_SPAWN_UNIT was broadcast to the attacker (the tail appended
	// after the kill frames). It must be byte-exact and addressed to the fresh mob.
	tail := f.conn.buf.Bytes()[beforeSpawn:]
	assert.Equal(t, expectMobSpawnUnit(t, live[0]), tail, "respawn emits one byte-exact SpawnUnit")
}

// TestCombatService_Kill_RespawnIsIndependentOfKillerSession guards the
// respawn-is-a-world-event contract: the respawn closure uses context.Background,
// not the killer's request ctx, so the mob comes back even when the killer has
// already disconnected. It cancels the request ctx before firing and confirms the
// replacement still spawns.
func TestCombatService_Kill_RespawnIsIndependentOfKillerSession(t *testing.T) {
	t.Parallel()
	f := newCombatFixture(t, 1002, 1, 1, nil)

	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, f.svc.Attack(ctx, f.attacker.AccountID, packet.CZActionRequestRequest{
		TargetGID: uint32(f.mob.EntityID), Action: 0,
	}))
	cancel() // killer "disconnects" before the respawn fires

	require.Equal(t, 1, f.respawn.Run(), "respawn fires despite the cancelled request ctx")
	live := f.mobs.OnMap("prontera")
	require.Len(t, live, 1, "replacement spawned even after the killer's ctx cancelled")
	assert.Equal(t, int32(1002), live[0].MobID)
}

// --- M14b: SM_BASH skill-cast slice --------------------------------------

// bashSkillLvl is the cast level every M14b unit test uses. BASH at lvl 10 =
// ratio 400% → naked-novice 1 dmg × 4 = 4 dmg/hit, killing a 50-HP Poring in 13
// casts (matches the e2e damage math: 13 × 4 = 52 ≥ 50).
const bashSkillLvl int16 = 10

// bashSkillID is rAthena's SM_BASH id (the only offensive weapon skill the M14b
// slice resolves).
const bashSkillID uint16 = 5

// TestCombatService_UseSkill_NilRegistryDropsSilently guards the nil-skills
// guard: a missing skill_db makes CZ_USE_SKILL2 a no-op so a misconfigured zone
// boots and plays (mirroring the nil-mob_db contract).
func TestCombatService_UseSkill_NilRegistryDropsSilently(t *testing.T) {
	t.Parallel()
	f := newCombatFixture(t, 1002, 50, 1, nil)
	// skills=nil (default). A cast must emit no frames and damage nothing.
	require.NoError(t, f.svc.UseSkill(context.Background(), f.attacker.AccountID, packet.CZUseSkill{
		SkillLv:  bashSkillLvl,
		SkillID:  bashSkillID,
		TargetID: uint32(f.mob.EntityID),
	}))
	frames := splitFixedFrames(t, f.conn.buf.Bytes())
	assert.Empty(t, frames, "UseSkill with nil skill_db emits no frames")
	assert.Equal(t, int32(50), f.mob.HP, "mob HP untouched when skills are nil")
}

// TestCombatService_UseSkill_KillsMobWithBash is the M14b kill proof at the
// unit level. A naked novice with SP=2000 casts SM_BASH lvl 10 (ratio 400%) on
// a 50-HP Poring. 1 dmg × 4 = 4 dmg/hit, 13 casts to kill. The captured stream
// must hold one ZC_PAR_CHANGE SPSP per cast (SP -= 15 at lvl 10), 13
// ZC_NOTIFY_SKILL + 13 ZC_NOTIFY_ACT pairs, and one ZC_NOTIFY_VANISH on the
// killing blow.
func TestCombatService_UseSkill_KillsMobWithBash(t *testing.T) {
	t.Parallel()
	f := newCombatFixture(t, 1002, 50, 1, nil, withSkillDB(testSkillDB(t)))
	// The fixture attacker is a naked novice (no SP set). BASH lvl 10 = 15 SP,
	// so seed enough SP for ~200 casts (margin over the 13 needed).
	f.attacker.SP = 2000
	f.attacker.MaxSP = 2000

	const castsNeeded = 13 // 50 HP / 4 dmg/hit, ceiling
	for i := 0; i < castsNeeded; i++ {
		require.NoError(t, f.svc.UseSkill(context.Background(), f.attacker.AccountID, packet.CZUseSkill{
			SkillLv:  bashSkillLvl,
			SkillID:  bashSkillID,
			TargetID: uint32(f.mob.EntityID),
		}))
	}

	frames := splitFixedFrames(t, f.conn.buf.Bytes())
	require.NotEmpty(t, frames, "BASH casts must emit frames")

	// Walk the captured stream and tally per-opcode occurrences. The exact
	// interleaving (parchange before/after skill+act) is dictated by UseSkill,
	// not the test: SP first, then NOTIFY_SKILL, then NOTIFY_ACT. We assert the
	// per-opcode count and the byte-exact match for each frame.
	var (
		parChanges   [][]byte
		skillHits    [][]byte
		actHits      [][]byte
		vanishFrames [][]byte
	)
	for _, fr := range frames {
		switch binary.LittleEndian.Uint16(fr[:2]) {
		case packet.HeaderZCPARCHANGE:
			parChanges = append(parChanges, fr)
		case packet.HeaderZCNOTIFYSKILL:
			skillHits = append(skillHits, fr)
		case packet.HeaderZCNOTIFYACT:
			actHits = append(actHits, fr)
		case packet.HeaderZCNOTIFYVANISH:
			vanishFrames = append(vanishFrames, fr)
		}
	}

	require.Equal(t, castsNeeded, len(parChanges),
		"one ZC_PAR_CHANGE SP_SP per cast; got %d", len(parChanges))
	require.Equal(t, castsNeeded, len(skillHits),
		"one ZC_NOTIFY_SKILL per cast; got %d", len(skillHits))
	require.Equal(t, castsNeeded, len(actHits),
		"one ZC_NOTIFY_ACT per cast; got %d", len(actHits))
	require.Equal(t, 1, len(vanishFrames), "exactly one ZC_NOTIFY_VANISH on the killing blow")

	// SP arithmetic: 2000 - 13×15 = 1805. The last par-change is the killing blow.
	wantSP := uint32(2000 - 13*15) //nolint:gosec // SP arithmetic, fits uint32
	lastPar := parChanges[len(parChanges)-1]
	assert.Equal(t, packet.SPSP, binary.LittleEndian.Uint16(lastPar[2:4]),
		"final par-change is SP_SP")
	assert.Equal(t, int32(wantSP), int32(binary.LittleEndian.Uint32(lastPar[4:8])), //nolint:gosec // G115: int32 wire slot
		"final SP = seed 2000 - 13×15 = 1805")

	// First skill-hit frame must be byte-exact: skillID=5, attacker AID, mob GID,
	// Damage=4, Level=10, Count=1, Action=DMGNormal, StartTime=fixedClock value.
	firstSkill := skillHits[0]
	assert.Equal(t, expectNotifySkill(t, bashSkillID, f.attacker.AccountID, uint32(f.mob.EntityID), 4, bashSkillLvl, 0x11223344), firstSkill,
		"first ZC_NOTIFY_SKILL is byte-exact (skillID=5, AID, GID, Damage=4, Level=10, Count=1, Action=0)")

	// ZC_NOTIFY_VANISH is the mob GID (death teardown).
	assert.Equal(t, expectNotifyVanish(t, uint32(f.mob.EntityID)), vanishFrames[0],
		"vanish frame addresses the dead Poring")

	// The mob is fully torn down: gone from the registry.
	_, gone := f.mobs.ByEntity(f.mob.EntityID)
	require.False(t, gone, "dead Poring unregistered from the mob registry")
}

// TestCombatService_UseSkill_SPInsufficientAcksAndDoesNotDamage guards the
// negative path: a naked novice with SP=0 casts SM_BASH (cost 8-15). The server
// replies ZC_ACK_TOUSESKILL with Cause=UseSkillFailSPInsufficient, the mob is
// not damaged, and the caster's SP stays at 0.
func TestCombatService_UseSkill_SPInsufficientAcksAndDoesNotDamage(t *testing.T) {
	t.Parallel()
	f := newCombatFixture(t, 1002, 50, 1, nil, withSkillDB(testSkillDB(t)))
	// SP=0 (zero-value) — BASH lvl 10 costs 15 SP, so the cast is rejected.

	require.NoError(t, f.svc.UseSkill(context.Background(), f.attacker.AccountID, packet.CZUseSkill{
		SkillLv:  bashSkillLvl,
		SkillID:  bashSkillID,
		TargetID: uint32(f.mob.EntityID),
	}))

	frames := splitFixedFrames(t, f.conn.buf.Bytes())
	require.Len(t, frames, 1, "exactly one ack frame on SP-insufficient reject")
	got := frames[0]
	assert.Equal(t, packet.HeaderZCACKTOUSESKILL, binary.LittleEndian.Uint16(got[:2]),
		"frame opcode is ZC_ACK_TOUSESKILL")
	assert.Equal(t, expectAckUseSkill(t, bashSkillID, packet.UseSkillFailSPInsufficient), got,
		"ack is byte-exact: SkillID=5, Cause=12 (SP insufficient)")

	assert.Equal(t, int32(50), f.mob.HP, "mob HP untouched on the rejected cast")
	_, sp := f.attacker.Vitals()
	assert.Equal(t, uint32(0), sp, "caster SP untouched on the rejected cast")
}

// TestCombatService_UseSkill_OutOfRangeDropsSilently guards the out-of-range
// branch: a cast from more than one cell away is a silent no-op (matching
// Attack's drop policy). The mob is not damaged, no frames are emitted.
func TestCombatService_UseSkill_OutOfRangeDropsSilently(t *testing.T) {
	t.Parallel()
	f := newCombatFixture(t, 1002, 50, 1, nil, withSkillDB(testSkillDB(t)))
	f.attacker.SP = 2000
	// Move the attacker 10 cells away from the mob (mob is at 100,100; the
	// fixture attacker is at 101,100). MoveEntity updates the AOI cell; the
	// player's own PosX/PosY must move in lockstep so the InMeleeRange check
	// sees the new (ax,ay) it derives from attacker.Position().
	require.NoError(t, f.mp.AOI.MoveEntity(aoi.EntityID(f.attacker.AccountID), 90, 90))
	f.attacker.PosX, f.attacker.PosY = 90, 90

	require.NoError(t, f.svc.UseSkill(context.Background(), f.attacker.AccountID, packet.CZUseSkill{
		SkillLv:  bashSkillLvl,
		SkillID:  bashSkillID,
		TargetID: uint32(f.mob.EntityID),
	}))

	assert.Empty(t, splitFixedFrames(t, f.conn.buf.Bytes()),
		"out-of-range cast emits no frames")
	assert.Equal(t, int32(50), f.mob.HP, "mob HP untouched when the cast is out of range")
	_, sp := f.attacker.Vitals()
	assert.Equal(t, uint32(2000), sp, "caster SP untouched when the cast is out of range")
}

// TestCombatService_UseSkill_UnknownTargetDropsSilently guards the unknown-target
// branch: a cast against a stale (already-removed) EntityID is a silent no-op,
// mirroring Attack's drop policy.
func TestCombatService_UseSkill_UnknownTargetDropsSilently(t *testing.T) {
	t.Parallel()
	f := newCombatFixture(t, 1002, 50, 1, nil, withSkillDB(testSkillDB(t)))
	f.attacker.SP = 2000
	// TargetID 999999 does not exist in the fixture's mob registry.
	require.NoError(t, f.svc.UseSkill(context.Background(), f.attacker.AccountID, packet.CZUseSkill{
		SkillLv:  bashSkillLvl,
		SkillID:  bashSkillID,
		TargetID: 999999,
	}))

	assert.Empty(t, splitFixedFrames(t, f.conn.buf.Bytes()),
		"unknown-target cast emits no frames")
	_, sp := f.attacker.Vitals()
	assert.Equal(t, uint32(2000), sp, "caster SP untouched when the target is unknown")
}

// TestCombatService_UseSkill_UnknownSkillDropsSilently guards the unknown-skill
// branch: a cast for a skill not in the skill_db (SkillID=99) is a silent no-op,
// mirroring Attack's drop policy.
func TestCombatService_UseSkill_UnknownSkillDropsSilently(t *testing.T) {
	t.Parallel()
	f := newCombatFixture(t, 1002, 50, 1, nil, withSkillDB(testSkillDB(t)))
	f.attacker.SP = 2000

	require.NoError(t, f.svc.UseSkill(context.Background(), f.attacker.AccountID, packet.CZUseSkill{
		SkillLv:  bashSkillLvl,
		SkillID:  99, // absent from testSkillDB
		TargetID: uint32(f.mob.EntityID),
	}))

	assert.Empty(t, splitFixedFrames(t, f.conn.buf.Bytes()),
		"unknown-skill cast emits no frames")
	_, sp := f.attacker.Vitals()
	assert.Equal(t, uint32(2000), sp, "caster SP untouched when the skill is unknown")
}
