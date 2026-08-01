// Package app: this file adds the M6 combat use case — a player attacks a mob,
// the server resolves melee damage, broadcasts the hit to AOI observers, and on
// the killing blow broadcasts the mob's vanish and tears it down. Combat is a
// world use case (not its own bounded context): it crosses the player and mob
// registries and the map AOI, exactly the collaborators the spawn and movement
// use cases already hold.
//
// Concurrency: unlike movement, combat needs no pathfinder, so it resolves
// synchronously on the connection's dispatch goroutine. Mob HP is mutated only
// through Mob.ApplyDamage (locked, atomic kill-credit); two players striking the
// same mob in the same tick cannot double-award a death. Each observer write
// goes to that player's own Conn (gnet AsyncWrite under TCP), so one slow
// socket never blocks another's broadcast.
package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	chardomain "github.com/bouroo/goAthena/internal/modules/character/domain"
	gwdomain "github.com/bouroo/goAthena/internal/modules/gateway/domain"
	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/aoi"
	"github.com/bouroo/goAthena/pkg/ro/combat"
	"github.com/bouroo/goAthena/pkg/ro/itemdb"
	"github.com/bouroo/goAthena/pkg/ro/mobdb"
	"github.com/bouroo/goAthena/pkg/ro/mode"
	"github.com/bouroo/goAthena/pkg/ro/packet"
	"github.com/bouroo/goAthena/pkg/ro/skilldb"
	"github.com/bouroo/goAthena/pkg/ro/statcalc"
)

// Motion timings written into ZC_NOTIFY_ACT's amotion slots, in milliseconds.
// rAthena derives these from status (src amotion = attacker's, dmg amotion =
// target's); the slice approximates them with fixed constants since the result
// of combat (HP/death) does not depend on them — they only pace the client's
// hit animation.
const (
	noviceAmotion  int32 = 432 // attacker attack-motion (rAthena NOVICE amotion)
	defaultDmotion int32 = 288 // target damage-motion delay
)

// statusPointsPerBaseLevel is the flat status-point grant per base level gained
// in the combat slice. rAthena derives status points from a per-level table
// (statpoint.Registry.Points, keyed by level alone — no class resolver needed),
// but a created novice starts with a fixed 48 status points that do not match
// that table's level-1 entry, so granting the table's cumulative delta would
// diverge. The flat +3 keeps the level-up math decoupled from the starting
// grant and directly assertable; the stance mirrors meleeDamage's slice
// approximation.
const statusPointsPerBaseLevel uint32 = 3

// noviceBaseExpThresholds[L-1] is the cumulative base EXP required to reach base
// level L for the combat slice's novice approximation; index 0 (level 1) is 0
// (a fresh novice starts there). The authoritative table lives in jobdb
// (BaseExpForLevel, which is cumulative and keyed by job name) but is not used
// here: resolving a char.class to a job name is a speculative feature outside
// the combat slice, and the table is only consulted to pace the level-up L3
// assertion. The slice length is the cap — at or beyond it, EXP still accrues
// and persists, but no further base level is granted.
var noviceBaseExpThresholds = [...]uint64{
	0,    // level 1
	10,   // level 2
	30,   // level 3
	70,   // level 4
	150,  // level 5
	310,  // level 6
	630,  // level 7
	1270, // level 8
	2550, // level 9
	5110, // level 10 (cap)
}

// MobRespawnDelay is how long a killed mob stays gone before a fresh replacement
// spawns at its home cell. rAthena derives this per spawn-group from the spawn
// DB's delay field; the combat slice approximates it with one flat constant since
// the result of combat (the kill) does not depend on the pacing — this only
// governs how soon the world refills. Short enough that an L3 e2e can observe a
// respawn without a long sleep.
const MobRespawnDelay = 5 * time.Second

// RespawnScheduler is the indirection over the respawn timer so unit tests can
// capture the armed closure and fire it deterministically (a real timer would
// make the respawn assertion timing-dependent). SystemRespawnScheduler is the
// production implementation backed by time.AfterFunc. Mirrors the Clock seam the
// movement and mob use cases already inject.
type RespawnScheduler interface {
	// After invokes fn exactly once after delay has elapsed, on its own goroutine.
	After(delay time.Duration, fn func())
}

// SystemRespawnScheduler is the production RespawnScheduler: time.AfterFunc fires
// the respawn closure off the connection's goroutine after the delay, so a kill
// never blocks the dispatch loop on the respawn.
type SystemRespawnScheduler struct{}

// After implements RespawnScheduler via time.AfterFunc.
func (SystemRespawnScheduler) After(delay time.Duration, fn func()) {
	time.AfterFunc(delay, fn)
}

// CombatService resolves a player's attack against a mob: range check, melee
// damage, AOI damage broadcast, and the death path (vanish + ground drops +
// EXP/level/persist + teardown). It holds the live-session player index (attacker
// lookup + observer Conns), the mob index (target lookup + unregister), the map
// store (AOI broadcast), the immutable mob_db (target stats + base EXP + drop
// table), the immutable item_db (drop resolution AegisName→id/type), the floor
// item index (drop register + future pickup), the character progression store
// (EXP/level read-modify-write + persist on kill), and the clock (the
// ZC_NOTIFY_ACT server tick).
type CombatService struct {
	players    *domain.PlayerRegistry
	mobs       *domain.MobRegistry
	maps       domain.MapStore
	db         *mobdb.Registry
	chars      domain.ProgressionStore
	clock      Clock
	respawn    RespawnScheduler
	fs         statcalc.FormulaSet
	gm         mode.Mode
	items      *itemdb.Registry
	floorItems *domain.FloorItemRegistry
	rng        stepSource
	skills     *skilldb.Registry
	rates      Rates
}

// NewCombatService binds the combat collaborators. db may be nil (no mob_db):
// damage then resolves against a zero-stat defender and kills award no EXP and no
// drops. chars may be nil (no character store, e.g. the M6b fixtures): kills then
// tear down the mob with no EXP/level/persist step, keeping the two-frame
// (act+vanish) assertions valid. clock supplies the per-attack server tick.
// respawn arms the post-death respawn timer; a nil respawn (no scheduler) makes
// kills a final teardown — the mob stays gone — which keeps the M6b/c fixtures'
// "mob torn down, no reappearance" assertions valid. fs is the resolved game-mode
// formula set (BaseATK source for the attacker); gm selects the mode-keyed DEF
// reduction. Both come from cfg.Zone.Renewal via statcalc.Registry at composition
// time. items is the item_db (drop resolution); a nil items (no item_db) makes
// kills award no ground drops, mirroring the nil-mob_db contract. floorItems owns
// the dropped-item index; a nil floorItems disables drops the same way. rng is the
// shared stepSource RNG seam (mob-wander reuses it); the drop roller draws a per-
// myriad rate check from it. A nil rng defaults to randStep (the global source),
// matching NewMobService. skills is the optional skill_db; when nil, CZ_USE_SKILL2
// casts are no-ops (skill use disabled), mirroring the nil-mob_db contract. rates
// are the operator EXP/drop multipliers (percent; DefaultRates = 1x); a zero
// field genuinely disables that reward path, so callers that want baseline 1x
// pass DefaultRates (config does this unless the operator set a non-default).
func NewCombatService(players *domain.PlayerRegistry, mobs *domain.MobRegistry, maps domain.MapStore, db *mobdb.Registry, chars domain.ProgressionStore, clock Clock, respawn RespawnScheduler, fs statcalc.FormulaSet, gm mode.Mode, items *itemdb.Registry, floorItems *domain.FloorItemRegistry, rng stepSource, skills *skilldb.Registry, rates Rates) *CombatService {
	if rng == nil {
		rng = randStep{}
	}
	return &CombatService{players: players, mobs: mobs, maps: maps, db: db, chars: chars, clock: clock, respawn: respawn, fs: fs, gm: gm, items: items, floorItems: floorItems, rng: rng, skills: skills, rates: rates}
}

// Attack resolves one CZ_ACTION_REQUEST from accountID against req.TargetGID.
// Non-mob targets (a player, an NPC, or a stale id) and out-of-range attacks are
// silent no-ops — the slice has no PvP/NPC combat, and the client is expected to
// have moved adjacent first. A hit broadcasts ZC_NOTIFY_ACT to every player in
// the mob's AOI (the attacker included, since it stands adjacent), and on the
// killing blow drives the death path. It never returns an expected-outcome
// error (a dropped attack is not a session fault); only an infra fault (map
// load) is returned so ProcessBytes logs it.
func (s *CombatService) Attack(ctx context.Context, accountID uint32, req packet.CZActionRequestRequest) error {
	attacker, ok := s.players.ByAccount(accountID)
	if !ok {
		// No live session: the conn never entered the world or already
		// disconnected. Drop the attack silently.
		return nil
	}
	mob, ok := s.mobs.ByEntity(aoi.EntityID(req.TargetGID))
	if !ok {
		// Target is not a live mob. The slice attacks mobs only.
		return nil
	}
	ax, ay, _ := attacker.Position()
	mx, my, _ := mob.Position()
	if !combat.InMeleeRange(int(ax), int(ay), int(mx), int(my)) {
		return nil
	}
	mp, err := s.maps.Load(ctx, mob.MapName)
	if err != nil {
		return fmt.Errorf("combat: load map %q for mob %d: %w", mob.MapName, mob.EntityID, err)
	}
	if mp == nil {
		return fmt.Errorf("combat: map %q loaded nil for mob %d", mob.MapName, mob.EntityID)
	}

	damage := s.computeDamage(attacker, s.mobEntry(mob.MobID))
	s.broadcastDamage(mp, mx, my, attacker.AccountID, mob.EntityID, damage)

	if _, died := mob.ApplyDamage(damage); died {
		return s.onMobDeath(ctx, mp, mob, attacker)
	}
	return nil
}

// UseSkill resolves one CZ_USE_SKILL2 from accountID against req. The M14b slice
// honours a single offensive weapon skill (SM_BASH) cast on an adjacent mob: it
// validates the skill + melee range, pays the per-level SP cost, computes skill
// damage (base melee × the skill ratio), broadcasts ZC_NOTIFY_SKILL then
// ZC_NOTIFY_ACT, and on the killing blow drives the existing death path.
// Insufficient SP replies ZC_ACK_TOUSESKILL (cause = SP insufficient) and deals
// no damage. With no skill_db, an unknown skill, a non-weapon/attack skill, a
// non-mob target, or an out-of-range cast, the request is a silent no-op —
// matching Attack's drop policy; richer fail-acks are PENDING. Only an infra
// fault (map load) is returned so ProcessBytes logs it.
func (s *CombatService) UseSkill(ctx context.Context, accountID uint32, req packet.CZUseSkill) error {
	if s.skills == nil {
		return nil
	}
	attacker, ok := s.players.ByAccount(accountID)
	if !ok {
		return nil
	}
	skill := s.skills.Get(int32(req.SkillID)) //nolint:gosec // G115: skill DB ids fit int32
	if skill == nil || !isWeaponAttackSkill(skill) {
		return nil
	}
	mob, ok := s.mobs.ByEntity(aoi.EntityID(req.TargetID))
	if !ok {
		return nil
	}
	ax, ay, _ := attacker.Position()
	mx, my, _ := mob.Position()
	if !combat.InMeleeRange(int(ax), int(ay), int(mx), int(my)) {
		return nil
	}

	level := clampSkillLevel(req.SkillLv, skill.MaxLevel)
	cost := s.skills.SpCostAt(skill.ID, int(level))
	if _, curSP := attacker.Vitals(); int32(curSP) < cost { //nolint:gosec // G115: SP fits int32
		return s.sendSkillSPFail(attacker, req.SkillID)
	}

	mp, err := s.maps.Load(ctx, mob.MapName)
	if err != nil {
		return fmt.Errorf("skill: load map %q for mob %d: %w", mob.MapName, mob.EntityID, err)
	}
	if mp == nil {
		return fmt.Errorf("skill: map %q loaded nil for mob %d", mob.MapName, mob.EntityID)
	}

	// Pay SP after the map resolves so a load fault spends no resource; the new
	// SP is broadcast only to the caster (SP is private, unlike damage).
	newSP := attacker.SpendSP(cost)
	if err := (packet.ParChangeResponse{VarID: packet.SPSP, Count: int32(newSP)}).Encode(connWriter{attacker.Conn}); err != nil { //nolint:gosec // G115: SP fits int32
		return fmt.Errorf("skill: encode SP update account %d: %w", accountID, err)
	}

	damage := skillDamage(s.computeDamage(attacker, s.mobEntry(mob.MobID)), level)
	s.broadcastSkill(mp, mx, my, req.SkillID, attacker.AccountID, mob.EntityID, damage, level)
	s.broadcastDamage(mp, mx, my, attacker.AccountID, mob.EntityID, damage)

	if _, died := mob.ApplyDamage(damage); died {
		return s.onMobDeath(ctx, mp, mob, attacker)
	}
	return nil
}

// skillDamage scales a base melee hit by the cast skill's damage ratio. The M14b
// slice resolves SM_BASH only; its ratio is the base 100% + 30% per level
// (third_party/.../skills/swordman/bash.cpp:14, `base_skillratio += 30 *
// skill_lv`). int64 holds the product so a large melee hit × ratio cannot wrap.
func skillDamage(base int32, level int32) int32 {
	if base < 0 {
		base = 0
	}
	return int32(int64(base) * int64(bashSkillRatio(level)) / 100) //nolint:gosec // G115: bounded by base melee × small ratio
}

// bashSkillRatio is SM_BASH's per-level damage ratio as a percent.
func bashSkillRatio(level int32) int32 {
	return 100 + 30*level
}

// clampSkillLevel bounds a client-claimed level to [1, maxLv]; a level outside
// the skill's range resolves at the nearest valid level rather than being
// rejected (matching rAthena clamping the effective level server-side).
func clampSkillLevel(lv int16, maxLv int32) int32 {
	if maxLv > 0 && int32(lv) > maxLv { //nolint:gosec // G115: int16→int32 widening
		return maxLv
	}
	if lv < 1 {
		return 1
	}
	return int32(lv) //nolint:gosec // G115: int16→int32 widening
}

// isWeaponAttackSkill reports whether a skill is an offensive weapon skill — the
// only shape the M14b cast slice resolves. Buffs, magic, and passives are out of
// scope (the handler drops them; PENDING).
func isWeaponAttackSkill(s *skilldb.SkillEntry) bool {
	return s.Type == "Weapon" && s.TargetType == "Attack"
}

// sendSkillSPFail replies ZC_ACK_TOUSESKILL with the SP-insufficient cause to the
// caster only. The cast is rejected with no damage or SP spent.
func (s *CombatService) sendSkillSPFail(player *domain.Player, skillID uint16) error {
	ack := packet.AckUseSkillResponse{SkillID: skillID, Cause: packet.UseSkillFailSPInsufficient}
	if err := ack.Encode(connWriter{player.Conn}); err != nil {
		return fmt.Errorf("skill: encode SP-insufficient ack account %d skill %d: %w", player.AccountID, skillID, err)
	}
	return nil
}

// broadcastSkill sends ZC_NOTIFY_SKILL for one skill hit to every player within
// AOI of the mob's cell (the attacker included). Same observer-resolution and
// write-failure policy as broadcastDamage: a failed write is ignored, the
// observer's own dispatch goroutine owns its teardown.
func (s *CombatService) broadcastSkill(mp *domain.Map, mobX, mobY int16, skillID uint16, attackerAccountID uint32, mobEntityID aoi.EntityID, damage int32, level int32) {
	sk := packet.NotifySkillResponse{
		SKID:       skillID,
		AID:        attackerAccountID,
		TargetID:   uint32(mobEntityID),
		StartTime:  s.clock.MoveStart(),
		AttackMT:   noviceAmotion,
		AttackedMT: defaultDmotion,
		Damage:     damage,
		Level:      int16(level),           //nolint:gosec // G115: level is 1..MaxLevel (≤32)
		Count:      1,                      // BASH is a single hit
		Action:     int8(packet.DMGNormal), //nolint:gosec // int8 wire slot; DMG_NORMAL = regular offensive hit
	}
	for _, e := range mp.AOI.QueryVisible(int(mobX), int(mobY)) {
		if e.Type != aoi.EntityPlayer {
			continue
		}
		neighbor, ok := s.players.ByAccount(uint32(e.ID))
		if !ok {
			continue
		}
		_ = sk.Encode(connWriter{neighbor.Conn})
	}
}

// mobEntry resolves the mob_db stats for a mob id, returning nil when no mob_db
// is configured or the id is absent — callers treat nil as zero-stats.
func (s *CombatService) mobEntry(mobID int32) *mobdb.MobEntry {
	if s.db == nil {
		return nil
	}
	return s.db.Get(mobID)
}

// onMobDeath handles a mob's killing blow: broadcast ZC_NOTIFY_VANISH (death) to
// AOI observers, roll the drop table and spawn ground items (ZC_ITEM_FALL_ENTRY),
// award the killer EXP/level and persist it, arm a deferred respawn at the mob's
// home cell, then tear the mob out of the registry and grid. Teardown is
// unconditional — a dead mob is removed from the world even if EXP persistence
// faults, so a persist error never keeps a corpse alive; the persist error is
// returned only so ProcessBytes logs it. The respawn is armed before teardown
// because the dead mob's immutable spawn fields (id, map, SpawnX/Y, level, MaxHP,
// name, speed) are a complete template for the replacement, and Unregister only
// drops the registry index — it does not mutate the struct, so the closure's
// captured pointer stays valid. Unregister is idempotent and RemoveEntity on a
// missing entity is a no-op, so a concurrent path that already cleaned up (e.g.
// the wander tick tearing down an OOB mob) stays consistent.
func (s *CombatService) onMobDeath(ctx context.Context, mp *domain.Map, mob *domain.Mob, killer *domain.Player) error {
	mobX, mobY, _ := mob.Position()
	s.broadcastVanish(mp, mobX, mobY, mob.EntityID)
	entry := s.mobEntry(mob.MobID)
	s.dropLoot(mp, mob, entry, mobX, mobY)
	awardErr := s.awardKill(ctx, killer, entry)
	s.scheduleRespawn(mob)
	s.mobs.Unregister(mob.EntityID)
	_ = mp.AOI.RemoveEntity(mob.EntityID)
	return awardErr
}

// dropRateDenominator is the per-myriad (1/10000) base a mob_db drop Rate is
// expressed against. rAthena applies server drop-rate multipliers (battle_config
// item_rate_*) on top; M9c-1 rolls the raw Rate for a faithful baseline, and the
// multiplier knob is deferred to the balance pass.
const dropRateDenominator = 10000

// dropLoot rolls the dead mob's drop table and spawns one floor item per winning
// entry, broadcasting ZC_ITEM_FALL_ENTRY (0x0ADD) to observing players. It mirrors
// rAthena mob_dead → mob_item_drop → map_addflooritem → clif_dropflooritem: each
// drop entry rolls independently at Rate/Denom, and a win spawns a floor item at
// the mob's death cell. No item_db (items nil), no floor registry (floorItems
// nil), a mob with no drop table (entry nil / empty Drops), or a winning AegisName
// the item_db cannot resolve skips that entry — the world stays playable (no drop)
// rather than dropping an item the client cannot render. A Register fault on one
// drop does not abort the rest, matching rAthena's per-drop independence.
//
// rAthena scatters dropped items on the tile with a random sub-cell offset
// (fitem->subx = rnd_value(1,4)*3); M9c-1 drops at the cell center (subX=subY=0)
// for a deterministic slice, and the scatter is a deferred visual nicety.
func (s *CombatService) dropLoot(mp *domain.Map, mob *domain.Mob, entry *mobdb.MobEntry, x, y int16) {
	if entry == nil || s.items == nil || s.floorItems == nil || len(entry.Drops) == 0 {
		return
	}
	for _, d := range entry.Drops {
		if s.rng.Intn(dropRateDenominator) >= s.rates.dropThreshold(d.Rate) {
			continue
		}
		item := s.items.ByAegisName(d.Item)
		if item == nil {
			continue
		}
		fi := &domain.FloorItem{
			EntityID: s.floorItems.NextEntityID(),
			MapName:  mob.MapName,
			NameID:   uint32(item.Id), //nolint:gosec // G115: item_db Id is int32 but a valid item id is positive and well under uint32
			Type:     itemdb.WireType(item.Type),
			Amount:   1,
			PosX:     x,
			PosY:     y,
		}
		if err := s.floorItems.Register(fi); err != nil {
			continue // NextEntityID is allocator-unique; unreachable, but a drop is best-effort.
		}
		s.broadcastFloorItem(mp, fi)
	}
}

// broadcastFloorItem sends ZC_ITEM_FALL_ENTRY (0x0ADD) for a freshly dropped item
// to every player within AOI of its cell, so observers see it land. This is the
// sole drop-path packet in the rathenaThailand fork (map_addflooritem →
// clif_dropflooritem). Same observer-resolution and write-failure policy as
// broadcastDamage/broadcastVanish/broadcastSpawn: non-player entities are skipped,
// and a dead observer socket is ignored (its own dispatch goroutine owns
// teardown).
func (s *CombatService) broadcastFloorItem(mp *domain.Map, fi *domain.FloorItem) {
	msg := packet.ItemFallEntryResponse{
		ID:             uint32(fi.EntityID), //nolint:gosec // G115: EntityID is a uint32-derived aoi.EntityID
		NameID:         fi.NameID,
		Type:           fi.Type,
		Identified:     0,               // a mob drop is unidentified (fitem->item.identify == 0)
		X:              uint16(fi.PosX), //nolint:gosec // G115: map cell in int16 wire slot
		Y:              uint16(fi.PosY), //nolint:gosec // G115: map cell in int16 wire slot
		Amount:         fi.Amount,
		ShowDropEffect: 0, // no MVP/random-option drop effect for the basic slice
		DropEffectMode: 0,
	}
	for _, e := range mp.AOI.QueryVisible(int(fi.PosX), int(fi.PosY)) {
		if e.Type != aoi.EntityPlayer {
			continue
		}
		neighbor, ok := s.players.ByAccount(uint32(e.ID))
		if !ok {
			continue
		}
		_ = msg.Encode(connWriter{neighbor.Conn})
	}
}

// scheduleRespawn arms a deferred respawn of mob at its home cell via the
// scheduler, unless no scheduler is wired (respawn nil ⇒ kills are final, used by
// the M6b/c fixtures). The closure captures the dead mob pointer; every template
// field respawnMob reads is immutable, so the teardown that runs between arming
// and firing does not corrupt the read. Production fires the closure off the
// connection goroutine after MobRespawnDelay.
func (s *CombatService) scheduleRespawn(mob *domain.Mob) {
	if s.respawn == nil {
		return
	}
	s.respawn.After(MobRespawnDelay, func() { s.respawnMob(mob) })
}

// respawnMob re-creates a killed mob at its home cell: allocate a fresh EntityID
// (a new lifetime — never the dead mob's id, so a client that cached the old GID
// is not confused by its reuse), restore full HP, register the mob, insert it
// into the map AOI, and broadcast ZC_SPAWN_UNIT to observing players so they see
// it reappear. It runs off the connection goroutine (production: time.AfterFunc);
// the registries and AOI are concurrency-safe (the MobRegistry doc comment
// anticipates mid-tick respawn), and each observer write goes to that player's
// own Conn. context.Background is used for the map load — respawn is a world
// event, not bound to any connection: the mob must come back even if the killer
// logged off, and the map is cached so the load is ctx-insensitive. Defensive
// early-returns mirror MobService.spawnOne: a cached-map load fault, an
// impossible double-register, or an AOI insertion failure simply leaves the mob
// absent this cycle rather than panicking — respawn is best-effort world refill,
// not a request whose error a caller can act on.
func (s *CombatService) respawnMob(dead *domain.Mob) {
	mp, err := s.maps.Load(context.Background(), dead.MapName)
	if err != nil || mp == nil {
		return
	}
	fresh := &domain.Mob{
		EntityID:  s.mobs.NextEntityID(),
		MobID:     dead.MobID,
		MapName:   dead.MapName,
		SpawnX:    dead.SpawnX,
		SpawnY:    dead.SpawnY,
		PosX:      dead.SpawnX,
		PosY:      dead.SpawnY,
		Dir:       0,
		Level:     dead.Level,
		MaxHP:     dead.MaxHP,
		HP:        dead.MaxHP,
		Name:      dead.Name,
		WalkSpeed: dead.WalkSpeed,
	}
	if err := s.mobs.Register(fresh); err != nil {
		return // impossible: NextEntityID is allocator-unique.
	}
	entity := &aoi.Entity{
		ID:   fresh.EntityID,
		Type: aoi.EntityMob,
		X:    int(dead.SpawnX),
		Y:    int(dead.SpawnY),
	}
	if err := mp.AOI.AddEntity(entity); err != nil {
		s.mobs.Unregister(fresh.EntityID) // roll back the registration.
		return
	}
	s.broadcastSpawn(mp, dead.SpawnX, dead.SpawnY, fresh)
}

// broadcastSpawn sends ZC_SPAWN_UNIT for a (re)spawned mob to every player within
// AOI of its cell so observers see it appear. Same observer-resolution and
// write-failure policy as broadcastDamage/broadcastVanish: non-player entities
// are skipped, and a dead observer socket is ignored (its own dispatch goroutine
// owns teardown).
func (s *CombatService) broadcastSpawn(mp *domain.Map, x, y int16, mob *domain.Mob) {
	spawn := mob.SpawnUnit()
	for _, e := range mp.AOI.QueryVisible(int(x), int(y)) {
		if e.Type != aoi.EntityPlayer {
			continue
		}
		neighbor, ok := s.players.ByAccount(uint32(e.ID))
		if !ok {
			continue
		}
		_ = spawn.Encode(connWriter{neighbor.Conn})
	}
}

// awardKill grants the dead mob's base EXP to the killer, resolves base
// level-ups against the in-code novice EXP table (+statusPointsPerBaseLevel per
// level), broadcasts the resulting status deltas to the killer's conn only
// (EXP/level are personal state — distinct from the AOI damage/vanish
// broadcasts), and persists the new progression. It is a no-op when no character
// store is wired (chars == nil), the mob has no mob_db entry, or the entry
// grants no base EXP — so nil-store fixtures and zero-EXP mobs produce no status
// frames. The in-memory character is mutated before the persist write so the
// live session reflects what the client was just told even if the save faults.
func (s *CombatService) awardKill(ctx context.Context, killer *domain.Player, entry *mobdb.MobEntry) error {
	if s.chars == nil || entry == nil || entry.BaseExp <= 0 {
		return nil
	}
	c, err := s.chars.GetByID(ctx, killer.AccountID, killer.CharID)
	if err != nil {
		return fmt.Errorf("combat: load killer char account %d char %d: %w", killer.AccountID, killer.CharID, err)
	}

	oldLevel := c.BaseLevel
	c.BaseExp += s.rates.baseExpAward(entry.BaseExp) // server base_exp_rate multiplier applied (battle.conf parity)
	maxLevel := uint16(len(noviceBaseExpThresholds))
	for c.BaseLevel < maxLevel && c.BaseExp >= baseExpForLevel(c.BaseLevel+1) {
		c.BaseLevel++
	}
	leveledUp := c.BaseLevel > oldLevel
	if leveledUp {
		c.StatusPoint += statusPointsPerBaseLevel * uint32(c.BaseLevel-oldLevel) //nolint:gosec // G115: level delta small
	}

	// Per-player status broadcast: base EXP always (every kill that grants EXP),
	// base level + status points only on a level-up. Sent to the killer's conn
	// alone — these are personal status params, not AOI-visible events. At
	// PACKETVER >= 20170830 clif_updatestatus routes SP_BASEEXP through the 64-bit
	// ZC_LONGLONGPAR_CHANGE (0x0acb), so EXP rides int64 here, never the 32-bit
	// variant the client no longer parses for exp.
	if err := (packet.LongLongParChangeResponse{VarID: packet.SPBaseExp, Amount: int64(c.BaseExp)}).Encode(connWriter{killer.Conn}); err != nil { //nolint:gosec // G115: uint64→int64 is value-preserving; EXP is the field's native width
		return fmt.Errorf("combat: send base-exp update to account %d: %w", killer.AccountID, err)
	}
	if leveledUp {
		if err := (packet.ParChangeResponse{VarID: packet.SPBaseLevel, Count: int32(c.BaseLevel)}).Encode(connWriter{killer.Conn}); err != nil { //nolint:gosec // G115: uint16 level→int32
			return fmt.Errorf("combat: send base-level update to account %d: %w", killer.AccountID, err)
		}
		if err := (packet.ParChangeResponse{VarID: packet.SPStatusPoint, Count: int32(c.StatusPoint)}).Encode(connWriter{killer.Conn}); err != nil { //nolint:gosec // G115: uint32→int32, status points are small
			return fmt.Errorf("combat: send status-point update to account %d: %w", killer.AccountID, err)
		}
	}

	if err := s.chars.SaveProgression(ctx, killer.AccountID, killer.CharID, chardomain.ProgressionOf(c)); err != nil {
		return fmt.Errorf("combat: persist killer progression account %d char %d: %w", killer.AccountID, killer.CharID, err)
	}
	return nil
}

// baseExpForLevel returns the cumulative base EXP required to reach the given
// base level. level must be in [1, len(noviceBaseExpThresholds)]; the level-up
// loop guarantees this by bounding on the table length. A corrupt level of 0 is
// treated as 1 (novice start), and a level past the table returns the final
// entry so the loop terminates rather than underflowing.
func baseExpForLevel(level uint16) uint64 {
	if level == 0 {
		level = 1
	}
	if idx := int(level) - 1; idx < len(noviceBaseExpThresholds) {
		return noviceBaseExpThresholds[idx]
	}
	return noviceBaseExpThresholds[len(noviceBaseExpThresholds)-1]
}

// computeDamage resolves one melee hit for attacker against a mob whose stats
// come from mob_db. A nil entry (no mob_db) yields a zero-stat defender, so the
// hit is the attacker's raw BaseATK (mode-keyed, clamped/floored) — the slice
// keeps resolving rather than fabricating a number. The pipeline is the
// pkg/ro/combat package: BaseATK from s.fs, mode-keyed DEF reduction from s.gm.
// This use case holds no formula logic of its own.
func (s *CombatService) computeDamage(attacker *domain.Player, entry *mobdb.MobEntry) int32 {
	base := statcalc.Base{
		Level: attacker.CLevel,
		Str:   attacker.Str, Agi: attacker.Agi, Vit: attacker.Vit,
		Int: attacker.Int, Dex: attacker.Dex, Luk: attacker.Luk,
	}
	var hardDEF, softDEF int32
	if entry != nil {
		hardDEF = entry.Defense
		softDEF = mobSoftDEF(entry, s.gm)
	}
	return combat.NormalMelee(
		combat.Attacker{Base: base},
		combat.Defender{HardDEF: hardDEF, SoftDEF: softDEF},
		s.fs, s.gm,
	).Damage
}

// mobSoftDEF reduces a mob's status def2 to the mode-specific soft-DEF the damage
// pipeline subtracts. rAthena stores the mob's def2 as Vit (pre-re,
// status.cpp:2714-2715) or floor((Level+Vit)/2) (renewal, status.cpp:2654,
// BL_MOB branch — the agi/5 term is BL_PC-only). The entry's Vit column is the
// canonical def2 input for both; the pre-re rnd term (def2/20)² is 0 for the
// low-Vit mobs the slice ships, so pre-re soft-DEF collapses to Vit.
func mobSoftDEF(entry *mobdb.MobEntry, m mode.Mode) int32 {
	if m == mode.Renewal {
		return (entry.Level + entry.Vit) / 2
	}
	return entry.Vit
}

// broadcastDamage sends ZC_NOTIFY_ACT for one hit to every player within AOI of
// the mob's cell — the attacker included, since it stands adjacent, so the
// client renders its own attack animation and damage. Non-player entities are
// skipped (no Conn). A write failure means the observer's socket is dead; the
// observer's own dispatch goroutine owns teardown, so the failed write is
// ignored here.
func (s *CombatService) broadcastDamage(mp *domain.Map, mobX, mobY int16, attackerAccountID uint32, mobEntityID aoi.EntityID, damage int32) {
	act := packet.NotifyActResponse{
		SrcID:      attackerAccountID,
		TargetID:   uint32(mobEntityID),
		ServerTick: s.clock.MoveStart(),
		SrcSpeed:   noviceAmotion,
		DmgSpeed:   defaultDmotion,
		Damage:     damage,
		IsSPDamage: 0, // HP damage
		Div:        1, // single hit
		Type:       packet.DMGNormal,
		Damage2:    0, // no dual-wield second hand
	}
	for _, e := range mp.AOI.QueryVisible(int(mobX), int(mobY)) {
		if e.Type != aoi.EntityPlayer {
			continue
		}
		neighbor, ok := s.players.ByAccount(uint32(e.ID))
		if !ok {
			continue
		}
		_ = act.Encode(connWriter{neighbor.Conn})
	}
}

// broadcastVanish sends ZC_NOTIFY_VANISH (death) for a mob to every player in
// its AOI so observers see it disappear. Same observer-resolution and
// write-failure policy as broadcastDamage.
func (s *CombatService) broadcastVanish(mp *domain.Map, mobX, mobY int16, mobEntityID aoi.EntityID) {
	vanish := packet.NotifyVanishResponse{
		GID:  uint32(mobEntityID),
		Type: packet.VanishDead,
	}
	for _, e := range mp.AOI.QueryVisible(int(mobX), int(mobY)) {
		if e.Type != aoi.EntityPlayer {
			continue
		}
		neighbor, ok := s.players.ByAccount(uint32(e.ID))
		if !ok {
			continue
		}
		_ = vanish.Encode(connWriter{neighbor.Conn})
	}
}

// ActionHandler serves CZ_ACTION_REQUEST (0x0089) on the map-role dispatch
// table. The packet carries only a target GID and an action byte; identity is
// the connection's verified auth (set by the CZ_ENTER gate), never a client
// field — the impersonation guard shared with the movement handler.
type ActionHandler struct {
	svc *CombatService
}

// NewActionHandler binds the handler to its combat service.
func NewActionHandler(svc *CombatService) *ActionHandler {
	return &ActionHandler{svc: svc}
}

// Handle implements gateway/domain.PacketHandler for CZ_ACTION_REQUEST.
func (h *ActionHandler) Handle(ctx context.Context, conn gwdomain.Conn, frame gwdomain.Frame) error {
	req, err := packet.ParseCZActionRequest(frame.Raw)
	if err != nil {
		return fmt.Errorf("parse CZ_ACTION_REQUEST: %w", err)
	}
	accountID := conn.Auth().AccountID
	if accountID == 0 {
		// No cached auth ⇒ the connection never passed the CZ_ENTER gate.
		// Tolerated by the gateway (the conn stays open) but the attack is
		// dropped.
		return errors.New("action: connection has no verified account (CZ_ENTER not completed)")
	}
	return h.svc.Attack(ctx, accountID, req)
}

// Compile-time check that ActionHandler satisfies the gateway handler shape.
var _ gwdomain.PacketHandler = (*ActionHandler)(nil).Handle

// SkillHandler serves CZ_USE_SKILL2 (0x0438) on the map-role dispatch table.
// The packet carries the skill id, claimed level, and target id; identity is the
// connection's verified auth (the CZ_ENTER impersonation guard shared with
// ActionHandler) — the client's target id is trusted only to select a mob, never
// to identify the caster.
type SkillHandler struct {
	svc *CombatService
}

// NewSkillHandler binds the handler to its combat service.
func NewSkillHandler(svc *CombatService) *SkillHandler {
	return &SkillHandler{svc: svc}
}

// Handle implements gateway/domain.PacketHandler for CZ_USE_SKILL2.
func (h *SkillHandler) Handle(ctx context.Context, conn gwdomain.Conn, frame gwdomain.Frame) error {
	req, err := packet.ParseCZUseSkill(frame.Raw)
	if err != nil {
		return fmt.Errorf("parse CZ_USE_SKILL2: %w", err)
	}
	accountID := conn.Auth().AccountID
	if accountID == 0 {
		// No cached auth ⇒ the connection never passed the CZ_ENTER gate.
		// Tolerated by the gateway (the conn stays open) but the cast is dropped.
		return errors.New("skill: connection has no verified account (CZ_ENTER not completed)")
	}
	return h.svc.UseSkill(ctx, accountID, req)
}

// Compile-time check that SkillHandler satisfies the gateway handler shape.
var _ gwdomain.PacketHandler = (*SkillHandler)(nil).Handle
