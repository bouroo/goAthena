package app

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/bouroo/goAthena/pkg/ro/aoi"

	"github.com/panjf2000/gnet/v2"

	chardomain "github.com/bouroo/goAthena/internal/modules/character/domain"
	shopapp "github.com/bouroo/goAthena/internal/modules/commerce/shop/app"
	contentapp "github.com/bouroo/goAthena/internal/modules/content/app"
	contentdomain "github.com/bouroo/goAthena/internal/modules/content/domain"
	invapp "github.com/bouroo/goAthena/internal/modules/inventory/app"
	worldapp "github.com/bouroo/goAthena/internal/modules/world/app"
	worlddomain "github.com/bouroo/goAthena/internal/modules/world/domain"
	ropacket "github.com/bouroo/goAthena/pkg/ro/packet"
	"github.com/bouroo/goAthena/pkg/ro/skilldb"
)

// czEnterSize is the wire length of CZ_ENTER (0x0072): 2 cmd + 4 AID + 4 GID +
// 4 authCode + 4 clientTime + 1 sex = 19. Matches pkg/ro/packet sizeCZEnter.
const czEnterSize = 19

// mapRefuseRejected mirrors rAthena REFUSE_ENTER_REJECTED (0).
const mapRefuseRejected uint8 = 0

// playerRespawnDelay is how long a mob-killed PC stays vanished before respawning
// at its save point. rAthena pre-re auto-returns a dead PC to its save point after
// a short delay; goAthena uses a documented 5 s default — NOT derived from source.
const playerRespawnDelay = 5 * time.Second

// MapServer is the gnet TCP listener for the map protocol. It admits a fresh
// connection on CZ_ENTER (session verified via the SessionStore), registers the
// player in the world, and replies with the map-enter + self-spawn packets.
// Subsequent packets (LoadEndAck, movement) route through the dispatch table.
type MapServer struct {
	gnet.BuiltinEventEngine
	engine   gnet.Engine
	booted   bool
	handlers map[uint16]mapHandler
	// db is the wire-length oracle for opcodes that have no handler wired into
	// the dispatch table yet. OnTraffic consults it to skip a DB-registered
	// frame (drop/trade/skill) instead of disconnecting the client.
	db      *ropacket.DB
	world   *worldapp.WorldService
	spawn   *worldapp.SpawnService
	combat  *worldapp.CombatService
	mobAI   *worldapp.MobAIService
	equip   *worldapp.EquipService
	itemUse *worldapp.ItemUseService
	inv     *invapp.InventoryService
	content *contentapp.Engine
	skills  *worldapp.SkillService
	skillDB *skilldb.Registry
	shops   *shopapp.ShopService
	trade   *worldapp.TradeService
	// shopStore resolves an NPC GID to the shop name it sells (CZ_ACK_SELECT
	// DEALTYPE carries an NPC id, not a shop name).
	shopStore contentdomain.ShopStore
	sess      chardomain.SessionStore
	log       *slog.Logger
	// openedShops maps charID to the shop the player last opened via
	// CZ_ACK_SELECT_DEALTYPE, so the following CZ_PC_PURCHASE/SELL_ITEMLIST
	// (which carry item entries, not the NPC id) can resolve the shop. One entry
	// per char, overwritten on each open; pruned on disconnect by OnClose →
	// unregisterConn. Guarded by shopMu because shop handlers run off the
	// reactor goroutine.
	openedShops map[uint32]string
	shopMu      sync.RWMutex
	// conns maps charID to its live gnet connection so player-to-player trade
	// and AOI-broadcast packets reach peers (who are on DIFFERENT connections
	// than the sender). Registered on CZ_ENTER, pruned on disconnect by OnClose
	// → unregisterConn. Mutex-guarded (conn-context race lesson): broadcast
	// resolves via connFor + AsyncWrite only, never reading a peer's context.
	conns  map[uint32]gnet.Conn
	connMu sync.RWMutex
}

// NewMapServer builds a map listener. shops and shopStore wire the NPC shop
// commerce verb (open/buy/sell); trade wires the player-to-player trade verb.
// shops/shopStore/trade may be nil in a reduced harness where their packets are
// never exercised, but production wiring resolves all three.
// skillDB is optional (may be nil) so existing callers are not broken.
func NewMapServer(world *worldapp.WorldService, spawn *worldapp.SpawnService, combat *worldapp.CombatService, mobAI *worldapp.MobAIService, equip *worldapp.EquipService, itemUse *worldapp.ItemUseService, inv *invapp.InventoryService, content *contentapp.Engine, skills *worldapp.SkillService, shops *shopapp.ShopService, shopStore contentdomain.ShopStore, trade *worldapp.TradeService, sess chardomain.SessionStore, skillDB *skilldb.Registry, log *slog.Logger) (*MapServer, error) {
	s := &MapServer{
		world:       world,
		spawn:       spawn,
		combat:      combat,
		mobAI:       mobAI,
		equip:       equip,
		itemUse:     itemUse,
		inv:         inv,
		content:     content,
		skills:      skills,
		skillDB:     skillDB,
		shops:       shops,
		shopStore:   shopStore,
		trade:       trade,
		sess:        sess,
		log:         log,
		handlers:    mapHandlers(),
		conns:       make(map[uint32]gnet.Conn),
		db:          ropacket.NewMapServerDB(),
		openedShops: make(map[uint32]string),
	}
	// Regen advances server-side on the world tick loop; this sink bridges the
	// changed vitals back to the player's client as ZC_PAR_CHANGE (mirrors the
	// script percentheal path). A char with no live conn is a no-op.
	world.OnStatChange = s.notifyStatChange
	// RespawnPlayer revives a dead PC off the reactor (ArmRespawn timer); this sink
	// bridges the revive back to the wire — ZC_SPAWN_UNIT to save-map neighbors +
	// ZC_ACCEPT_ENTER to relocate the player's own client. Mirrors handleEnter's
	// appear. A char whose conn dropped before the timer fires is a no-op.
	world.OnRespawn = s.notifyRespawn
	// GrantExp accrues mob-kill EXP to the killer; this sink bridges the new
	// totals back to the killer's client as two ZC_LONGLONGPAR_CHANGE frames
	// (SP_BASEEXP then SP_JOBEXP) so the EXP bar rises. A headless harness (no
	// EXP grants) is a no-op. Leveling (threshold crossing, stat recalc) is
	// deferred — this only relays the new EXP totals.
	world.OnExpChange = s.notifyExpChange
	// LevelingService converts accrued EXP to levels; this sink bridges a
	// level-up back to the client as one ZC_PAR_CHANGE burst (base level, the
	// recalculated maxima, and the full heal that rides a pre-re level-up) so
	// the client's level display and bars update together. A headless harness
	// (no leveling) is a no-op.
	world.OnLevelUp = s.notifyLevelUp
	// Mob AI runs on the same world tick; this sink bridges a mob's landed hit
	// back to the player as ZC_NOTIFY_ACT so the swing is visible and the target's
	// HP bar drops. A headless harness (no mob AI) leaves mobs passive.
	if mobAI != nil {
		mobAI.OnMobAttack = s.notifyMobAttack
		mobAI.OnMobMove = s.notifyMobMove
	}
	// A (re)spawned mob enters the world off any client request (initial seed,
	// respawn timer); this sink broadcasts the appear frame to the mob's AOI
	// neighbors so a respawn does not stay invisible until the mob first acts.
	// Players already nearby when the initial seed placed the mob learned about
	// it through their enter back-fill, so the broadcast is idempotent for them.
	if spawn != nil {
		spawn.OnMobSpawn = s.notifyMobSpawn
	}
	return s, nil
}

// setOpenedShop records the shop a player opened (threaded from
// CZ_ACK_SELECT_DEALTYPE to the subsequent CZ_PC_PURCHASE/SELL_ITEMLIST, which
// do not restate the NPC id). Safe to call from a shop handler goroutine.
func (s *MapServer) setOpenedShop(charID uint32, shopName string) {
	s.shopMu.Lock()
	defer s.shopMu.Unlock()
	s.openedShops[charID] = shopName
}

// openedShop returns the shop the player last opened, or false.
func (s *MapServer) openedShop(charID uint32) (string, bool) {
	s.shopMu.RLock()
	defer s.shopMu.RUnlock()
	name, ok := s.openedShops[charID]
	return name, ok
}

// registerConn records a charID's live connection so peer-to-peer packets
// (trade) can reach a partner on a different connection. Safe off the reactor.
func (s *MapServer) registerConn(charID uint32, c gnet.Conn) {
	s.connMu.Lock()
	s.conns[charID] = c
	s.connMu.Unlock()
}

// connFor returns the live connection for a charID, or false if none is
// registered (the char has not entered this map-server, or its shim entry was
// never created).
func (s *MapServer) connFor(charID uint32) (gnet.Conn, bool) {
	s.connMu.RLock()
	c, ok := s.conns[charID]
	s.connMu.RUnlock()
	return c, ok
}

// notifyStatChange emits ZC_PAR_CHANGE for HP then SP to the char's client. It is
// the world regen tick's notification sink (world.OnStatChange); a char with no
// live connection (offline, or not on this map-server) is skipped. Mirrors the
// ScriptHost.PercentHeal encoding so clients update vitals identically.
func (s *MapServer) notifyStatChange(charID uint32, hp, sp int32) {
	c, ok := s.connFor(charID)
	if !ok {
		return
	}
	var buf bytes.Buffer
	_ = ropacket.ParChangeResponse{VarID: ropacket.SPHP, Count: hp}.Encode(&buf)
	_ = ropacket.ParChangeResponse{VarID: ropacket.SPSP, Count: sp}.Encode(&buf)
	_ = c.AsyncWrite(buf.Bytes(), nil)
}

// notifyExpChange emits the two ZC_LONGLONGPAR_CHANGE frames — SP_BASEEXP then
// SP_JOBEXP — carrying the player's new EXP totals to its client so the EXP bar
// rises. It is the world.GrantExp notification sink (world.OnExpChange); a char
// with no live connection (offline, or not on this map-server) is skipped. EXP
// totals are uint64 but the PACKETVER-20250604 wire slot is int64; no real EXP
// total approaches math.MaxInt64, so the cast is lossless in practice. Mirrors
// notifyStatChange's coalesced two-frame AsyncWrite.
func (s *MapServer) notifyExpChange(charID uint32, baseExp, jobExp uint64) {
	c, ok := s.connFor(charID)
	if !ok {
		return
	}
	var buf bytes.Buffer
	_ = ropacket.LongLongParChangeResponse{VarID: ropacket.SPBaseExp, Amount: int64(baseExp)}.Encode(&buf) //nolint:gosec // G115: positive EXP fits int64.
	_ = ropacket.LongLongParChangeResponse{VarID: ropacket.SPJobExp, Amount: int64(jobExp)}.Encode(&buf)   //nolint:gosec // G115: positive EXP fits int64.
	_ = c.AsyncWrite(buf.Bytes(), nil)
}

// notifyLevelUp emits the level-up burst for charID as ZC_PAR_CHANGE frames:
// the new base level, the recalculated maxima, and the vitals healed to full
// (pre-re convention — a level-up restores HP/SP). It is the LevelingService's
// notification sink (world.OnLevelUp); a char with no live connection is a
// no-op. One buffered write so the client applies the frames atomically.
func (s *MapServer) notifyLevelUp(charID uint32, newLevel int16, maxHP, maxSP int32, statusPoint uint32) {
	c, ok := s.connFor(charID)
	if !ok {
		return
	}
	var buf bytes.Buffer
	_ = ropacket.ParChangeResponse{VarID: ropacket.SPBaseLevel, Count: int32(newLevel)}.Encode(&buf) //nolint:gosec // G115: level bounded by game values.
	_ = ropacket.ParChangeResponse{VarID: ropacket.SPMaxHP, Count: maxHP}.Encode(&buf)
	_ = ropacket.ParChangeResponse{VarID: ropacket.SPMaxSP, Count: maxSP}.Encode(&buf)
	_ = ropacket.ParChangeResponse{VarID: ropacket.SPHP, Count: maxHP}.Encode(&buf)
	_ = ropacket.ParChangeResponse{VarID: ropacket.SPSP, Count: maxSP}.Encode(&buf)
	_ = ropacket.ParChangeResponse{VarID: ropacket.SPStatusPoint, Count: int32(statusPoint)}.Encode(&buf) //nolint:gosec // G115: points bounded by level count, far below int32.
	_ = c.AsyncWrite(buf.Bytes(), nil)
}

// notifyMobAttack is the MobAIService.OnMobAttack sink (mirrors notifyStatChange
// for the inverse, mob→player direction). It emits ZC_NOTIFY_ACT — the damage /
// action broadcast rAthena's clif_damage sends — to the target player and their
// AOI neighbors so the mob's swing is visible, then refreshes the target's own
// HP/SP bar via notifyStatChange (applyDamage mutates HP directly without firing
// OnStatChange). dmg may be 0 (miss/block) and is still broadcast so the client
// renders the swing. On a killing blow (died==true) against a PC, handlePlayerDeath
// takes over: VanishDead at the death cell + an armed respawn timer. ServerTick/
// Speed are zero: mob_db carries no amotion, so the per-hit cadence
// (mobAttackInterval) is the only timing a first cut needs. A target that left the
// world is a no-op.
func (s *MapServer) notifyMobAttack(mobID, targetID worlddomain.EntityID, dmg int32, died bool) {
	target, err := s.world.Get(targetID)
	if err != nil {
		return // disconnected/despawned between swing and notify: nothing to show
	}
	resp := ropacket.NotifyActResponse{
		SrcID:    uint32(mobID),    //nolint:gosec // G115: EntityID wraps a uint32 GID
		TargetID: uint32(targetID), //nolint:gosec // G115: EntityID wraps a uint32 GID
		Damage:   dmg,
		Div:      1,
		Type:     ropacket.DMGNormal,
	}
	out := make([]byte, resp.Size())
	if err := resp.Encode(sliceWriter(out)); err != nil {
		s.log.Error("map: encode mob-attack notify", "err", err)
		return
	}
	// Exclude 0 (no charID is 0): the target player is in the neighbor set and
	// must see the hit land on them, alongside every AOI neighbor.
	s.broadcast(out, target.Map, target.Pos, 0)
	// Refresh the target's own vitals so their HP bar drops by the damage dealt.
	s.notifyStatChange(uint32(targetID), target.HP, target.SP) //nolint:gosec // G115: EntityID wraps a uint32 GID
	if died && target.Type == worlddomain.EntityTypePC {
		s.handlePlayerDeath(uint32(targetID), target.Map, target.Pos) //nolint:gosec // G115: EntityID wraps a uint32 GID
	}
}

// notifyMobMove is the MobAIService.OnMobMove sink: after a mob takes a chase
// step (MoveEntity reseated it at `to`), broadcast ZC_UNIT_WALKING so every AOI
// neighbor near the destination cell sees the mob walk there. Mirrors the
// player-move observer broadcast but with ObjectType=MOB and exclude=0 — a mob
// has no own connection, so every nearby player is told (the player move excludes
// the mover, who receives a separate self-ack). mob_db WalkSpeed is already in
// ms per cell, the same unit as the packet Speed field (PC default 150), so it
// passes through unchanged. The mob's map/name resolve from the entity (Get runs
// off the world lock, so no mutex is held here); a mob that despawned between
// MoveEntity and the notify is a no-op.
func (s *MapServer) notifyMobMove(mobID worlddomain.EntityID, from, to worlddomain.Position, speed int16) {
	e, err := s.world.Get(mobID)
	if err != nil {
		return // despawned between step and notify: nothing to broadcast
	}
	resp := ropacket.UnitWalkingResponse{
		ObjectType: objectTypeMob,
		AID:        uint32(mobID), //nolint:gosec // G115: EntityID wraps a uint32 GID; AID=GID for mobs.
		GID:        uint32(mobID), //nolint:gosec // G115: EntityID wraps a uint32 GID.
		Speed:      speed,
		SrcX:       from.X,
		SrcY:       from.Y,
		DestX:      to.X,
		DestY:      to.Y,
		Name:       e.Name,
	}
	out, ok := encodeUnitWalk(s, resp)
	if !ok {
		return
	}
	// Anchor at the destination cell (where the mob lands), matching the player-
	// move observer broadcast; exclude 0 because a mob has no conn of its own.
	s.broadcast(out, e.Map, to, 0)
}

// notifyMobSpawn is the SpawnService.OnMobSpawn sink: after a mob (re)enters
// the world, broadcast its ZC_SPAWN_UNIT (ObjectType=MOB) to AOI neighbors at
// the spawn cell so bystanders see it appear. The mob has no conn of its own,
// so exclude is 0 and every nearby player is told. A mob that was removed again
// between the AddEntity and this notify is a no-op.
func (s *MapServer) notifyMobSpawn(mobID worlddomain.EntityID) {
	e, err := s.world.Get(mobID)
	if err != nil {
		return // removed again between spawn and notify: nothing to show
	}
	resp := spawnUnitAny(e, objectTypeMob)
	resp.AID = uint32(mobID) //nolint:gosec // G115: EntityID wraps a uint32 GID.
	if sbuf, ok := encodeSpawnUnit(s, resp); ok {
		s.broadcast(sbuf, e.Map, e.Pos, 0)
	}
}

// handlePlayerDeath performs the bounded PC death model: broadcast ZC_NOTIFY_VANISH
// (VanishDead) at the death cell — the dying player's own conn included so it sees
// its own death — then arm a respawn timer that revives the PC at its save point.
// Called from notifyMobAttack when a mob's killing blow (died==true) lands on a PC.
// The player's connection stays alive across the delay; the bounded goAthena model
// has no ghost/tomb/respawn-button — the PC simply vanishes then reappears.
func (s *MapServer) handlePlayerDeath(charID uint32, deathMap string, deathPos worlddomain.Position) {
	vanish := ropacket.NotifyVanishResponse{GID: charID, Type: ropacket.VanishDead}
	vbuf := make([]byte, vanish.Size())
	if err := vanish.Encode(sliceWriter(vbuf)); err != nil {
		s.log.Error("map: encode player vanish", "err", err)
	} else {
		// exclude 0 so the dying player's own conn receives its death frame.
		s.broadcast(vbuf, deathMap, deathPos, 0)
	}
	s.world.ArmRespawn(charID, playerRespawnDelay)
}

// notifyRespawn is the WorldService.OnRespawn sink: after a dead PC is revived at
// its save point, broadcast ZC_SPAWN_UNIT so save-map neighbors see it appear and
// deliver ZC_ACCEPT_ENTER so the player's own client relocates to the save cell.
// Mirrors handleEnter's appear (spawn-unit to others + accept-enter to self).
// RespawnPlayer settled state off the world mutex, so Get + broadcast here take no
// held mutex; a player whose conn dropped before the timer fired is a no-op.
func (s *MapServer) notifyRespawn(charID uint32) {
	e, err := s.world.Get(worlddomain.EntityID(charID)) //nolint:gosec // G115: charID is a char_id (uint32).
	if err != nil {
		return // player left between respawn and appear: nothing to broadcast
	}
	if sbuf, ok := encodeSpawnUnit(s, spawnUnitFromEntity(e)); ok {
		s.broadcast(sbuf, e.Map, e.Pos, charID)
	}
	if c, ok := s.connFor(charID); ok {
		s.writeAcceptEnter(c, e)
	}
}

// unregisterConn drops a charID's live connection and its last-opened shop from
// the registries. Called on connection close to prune the per-char entries that
// CZ_ENTER created, closing the M4b connection-registry leak. Safe off the
// reactor.
func (s *MapServer) unregisterConn(charID uint32) {
	s.connMu.Lock()
	delete(s.conns, charID)
	s.connMu.Unlock()
	s.shopMu.Lock()
	delete(s.openedShops, charID)
	s.shopMu.Unlock()
}

// OnClose prunes the disconnecting connection from the char-indexed registries
// and persists the disconnect (offline flag + last position + hp/sp via
// LeaveMap) so the player's in-session state survives a restart, then removes
// the player's world entity so a disconnect is visible to others. It runs on
// the closing connection's own eventloop goroutine, so reading its cached
// mapAuth context is race-free here (unlike handler goroutines, which must not
// touch c.Context()).
func (s *MapServer) OnClose(c gnet.Conn, _ error) gnet.Action {
	a, ok := c.Context().(mapAuth)
	if !ok {
		return gnet.None
	}
	s.unregisterConn(a.charID)
	// Snapshot the player's map+pos BEFORE removing the entity: the vanish
	// broadcast anchors on the cell the player occupied.
	e, err := s.world.Get(worlddomain.EntityID(a.charID))
	if err != nil {
		s.log.Debug("map conn closed, entity already gone", "gid", a.charID)
		return gnet.None
	}
	// Persist the disconnect through LeaveMap — the single-source persist
	// primitive (warp + disconnect funnel here): it removes the entity from the
	// registry + AOI grid and writes the offline flag + last position + hp/sp, so
	// a disconnect neither leaves a stale-online row nor loses in-session vitals.
	// Best-effort: a failure is logged, not fatal — the vanish broadcast still
	// runs. The bounded ctx keeps a slow DB from stalling the reactor eventloop.
	leaveCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := s.world.LeaveMap(leaveCtx, a.charID); err != nil {
		s.log.Warn("map conn closed, leave world", "gid", a.charID, "err", err)
	}
	cancel()
	// Broadcast the departure to OTHER nearby players (ZC_NOTIFY_VANISH,
	// CLR_OUTSIGHT). The disconnecting conn is already closing, so excluding it
	// from its own goodbye is both correct and a no-op-in-practice.
	vanish := ropacket.NotifyVanishResponse{GID: a.charID, Type: ropacket.VanishOutsight}
	vbuf := make([]byte, vanish.Size())
	if err := vanish.Encode(sliceWriter(vbuf)); err != nil {
		s.log.Error("map: encode vanish on close", "err", err)
		return gnet.None
	}
	s.broadcast(vbuf, e.Map, e.Pos, a.charID)
	s.log.Debug("map conn closed, despawned + pruned", "gid", a.charID)
	return gnet.None
}

// broadcast delivers a pre-encoded packet to every OTHER player connection near
// an event's anchor cell (a mover's destination, a dying mob's cell, a drop
// site, a newly-entered player's spawn cell). excludeCharID is the actor who
// already received its own leg of the event (the mover/killer/dropper/enterer)
// and is skipped. Neighbors with no registered connection (offline, or not yet
// on this map-server) are skipped. Each delivery is a gnet.AsyncWrite on the
// partner connection only — thread-safe off the reactor — and never reads a
// partner connection's context. packet is treated as immutable; the same buffer
// is safe to fan out because gnet copies it into each connection's write buffer.
func (s *MapServer) broadcast(packet []byte, mapName string, pos worlddomain.Position, excludeCharID uint32) {
	for _, id := range s.world.PlayersNear(mapName, pos) {
		cid := uint32(id) //nolint:gosec // G115: EntityID wraps a char_id (uint32).
		if cid == excludeCharID {
			continue
		}
		if c, ok := s.connFor(cid); ok {
			_ = c.AsyncWrite(packet, nil)
		}
	}
}

// OnBoot captures the engine for shutdown.
func (s *MapServer) OnBoot(e gnet.Engine) gnet.Action {
	s.engine = e
	s.booted = true
	s.log.Info("map listener booted")
	return gnet.None
}

// OnTraffic is the table-driven dispatcher: peek the 2-byte opcode, look up the
// handler + frame size, read the full frame, and dispatch on a goroutine so the
// reactor never blocks. An opcode without a wired handler is not fatal: the
// packet DB supplies its on-wire length (or, for an opcode unknown to the DB
// too, the 2-byte header) so the frame is skipped and the connection stays
// alive — a client sending a not-yet-wired playable action (drop/trade/skill)
// must not be booted.
func (s *MapServer) OnTraffic(c gnet.Conn) gnet.Action {
	for {
		if c.InboundBuffered() < 2 {
			return gnet.None // need at least the 2-byte opcode header
		}
		hdr, err := c.Peek(2)
		if err != nil {
			return gnet.None
		}
		opcode := binary.LittleEndian.Uint16(hdr)
		if h, ok := s.handlers[opcode]; ok {
			n, ready := h.frameLen(c)
			if !ready {
				return gnet.None // wait for the full frame (fixed or variable) to arrive
			}
			frame, err := c.Next(n)
			if err != nil {
				return gnet.None
			}
			cp := append([]byte(nil), frame...) // detach from gnet's ring buffer
			// Resolve auth on the eventloop — the only goroutine that mutates the
			// conn's context — and pass it in. Handlers must not read c.Context()
			// off-loop, where gnet's conn.release() races it on close.
			auth := authFromConn(c)
			go h.fn(s, c, auth, cp)
			continue
		}
		// Unwired opcode: skip the frame using the DB's length so the client
		// stays connected. Never close on a registered (playable) opcode.
		skip, buffered := s.unhandledSkip(c, opcode)
		if !buffered {
			return gnet.None // variable-length frame not fully arrived yet
		}
		if _, err := c.Discard(skip); err != nil {
			return gnet.None
		}
		s.log.Debug("map: skipping unhandled opcode", "cmd", fmt.Sprintf("0x%04x", opcode), "skip", skip)
	}
}

// unhandledSkip returns how many leading bytes to discard for an opcode that has
// no wired handler, and whether that many bytes are already buffered. A
// DB-registered opcode skips its definition's fixed length; a variable-length
// packet reads its on-wire length from the uint16 at offset 2. An opcode absent
// from the DB skips only the 2-byte header — a truly unknown stream cannot be
// safely aligned, so we resync one header and keep the connection alive rather
// than booting the client.
func (s *MapServer) unhandledSkip(c gnet.Conn, opcode uint16) (skip int, buffered bool) {
	def, ok := s.db.Lookup(opcode)
	if !ok {
		return 2, true
	}
	if def.Length != ropacket.VariableLength {
		if c.InboundBuffered() < def.Length {
			return def.Length, false // wait for the full fixed frame
		}
		return def.Length, true
	}
	// Variable-length frame: [cmd:2][len:2][payload...]. The length prefix is
	// the uint16 at byte offset 2.
	if c.InboundBuffered() < 4 {
		return 0, false
	}
	prefix, err := c.Peek(4)
	if err != nil {
		return 0, false
	}
	n := int(binary.LittleEndian.Uint16(prefix[2:4]))
	if n < 4 {
		return 2, true // malformed length prefix: resync over the header
	}
	if c.InboundBuffered() < n {
		return n, false // wait for the rest of the frame
	}
	return n, true
}

// aoiSweepRadius bounds the enter floor-item sweep to the same viewport the
// AOI grid broadcasts into, so a player entering a map is told only about loot
// it could actually see.
const aoiSweepRadius = aoi.DefaultBroadcastRadius - 1

// handleEnter verifies the CZ_ENTER, admits the player into the world, and sends
// the map-enter + self-spawn reply.
func (s *MapServer) handleEnter(c gnet.Conn, _ *mapAuth, frame []byte) {
	req, err := ropacket.ParseCZEnter(frame)
	if err != nil {
		s.log.Warn("map: unparseable CZ_ENTER", "err", err)
		return
	}
	// Trust gate: verify the session exists and AuthCode matches loginID1.
	sess, err := s.sess.GetSession(context.Background(), req.AccountID)
	if err != nil {
		if errors.Is(err, chardomain.ErrSessionNotFound) {
			s.writeRefuseEnter(c)
			s.log.Info("map refused", "aid", req.AccountID, "reason", "no session")
			return
		}
		s.log.Error("map: session lookup", "err", err)
		s.writeRefuseEnter(c)
		return
	}
	if sess.LoginID1 != req.AuthCode {
		s.writeRefuseEnter(c)
		s.log.Info("map refused", "aid", req.AccountID, "reason", "authcode mismatch")
		return
	}

	// Admit: load the char's enter state and register it in the world.
	entity, err := s.world.EnterMap(context.Background(), req.CharID)
	if err != nil {
		s.log.Error("map: enter world", "aid", req.AccountID, "gid", req.CharID, "err", err)
		s.writeRefuseEnter(c)
		return
	}

	// Cache the authed identity on the connection (SetContext) so subsequent
	// packets (LoadEndAck, movement) know whose connection this is without
	// re-verifying. AID/GID are sourced from the verified session, never the
	// client-controlled packet fields.
	c.SetContext(mapAuth{accountID: req.AccountID, charID: req.CharID})
	// Index the connection by charID so peer-to-peer trade and AOI-broadcast
	// packets (whose target is on a different connection) can be delivered.
	// Pruned on disconnect by OnClose → unregisterConn.
	s.registerConn(req.CharID, c)

	s.writeAcceptEnter(c, entity)
	// Other players already on the map see the newcomer spawn in (ZC_SPAWN_UNIT).
	if sbuf, ok := encodeSpawnUnit(s, spawnUnitFromEntity(entity)); ok {
		s.broadcast(sbuf, entity.Map, entity.Pos, req.CharID)
	}
	// AOI back-fill: the newcomer also sees every existing nearby entity, one
	// ZC_SPAWN_UNIT per neighbor written to its own conn (not a broadcast). The
	// newcomer's own entity is already in the world, so the query may list it;
	// self is skipped by charID. NPCs get ObjectType=6 so the client renders
	// them as clickable static sprites.
	for _, nid := range s.world.QueryVisible(entity.Map, int(entity.Pos.X), int(entity.Pos.Y)) {
		if nid == worlddomain.EntityID(req.CharID) {
			continue
		}
		neighbor, gerr := s.world.Get(nid)
		if gerr != nil {
			s.log.Debug("map: AOI neighbor lookup skipped", "gid", nid, "err", gerr)
			continue
		}
		builder := spawnUnitFromEntity
		if neighbor.Type == worlddomain.EntityTypeNPC {
			builder = spawnUnitNPC
		}
		if nbuf, ok := encodeSpawnUnit(s, builder(neighbor)); ok {
			_ = c.AsyncWrite(nbuf, nil)
		}
	}
	// Floor items already on the ground inside the newcomer's AOI are sent as
	// ZC_ITEM_ENTRY (0x009d, clif_getareachar_item): the item becomes visible
	// AND pickable — a client that only saw a drop's 0x0ADD landing frame (sent
	// to whoever was near at drop time) cannot pick one it never learned about.
	// Fresh drops are excluded by proximity: only items within the AOI range of
	// the enter cell are swept.
	s.sweepFloorItems(c, entity)
	s.log.Info("map entered", "aid", req.AccountID, "gid", req.CharID, "map", entity.Map)
}

// sweepFloorItems sends ZC_ITEM_ENTRY (0x009d, clif_getareachar_item) for every
// floor item already on the ground within the AOI radius of the entering
// player's cell. The enter back-fill covers entities; this covers loot — without
// it a newcomer cannot see or pick up items dropped before it arrived.
func (s *MapServer) sweepFloorItems(c gnet.Conn, entity worlddomain.Entity) {
	for _, fi := range s.spawn.FloorItems(entity.Map) {
		if abs(int(fi.PosX)-int(entity.Pos.X)) > aoiSweepRadius || abs(int(fi.PosY)-int(entity.Pos.Y)) > aoiSweepRadius {
			continue
		}
		entry := ropacket.ItemEntryResponse{
			AID:        fi.GroundID,
			NameID:     fi.NameID,
			Identified: 1,
			X:          uint16(fi.PosX),   //nolint:gosec // G115: map coords are non-negative int16.
			Y:          uint16(fi.PosY),   //nolint:gosec // G115: map coords are non-negative int16.
			Amount:     uint16(fi.Amount), //nolint:gosec // G115: amount bounded to small stack values.
		}
		ebuf := make([]byte, entry.Size())
		if err := entry.Encode(sliceWriter(ebuf)); err != nil {
			s.log.Error("map: encode item-entry", "err", err)
			continue
		}
		_ = c.AsyncWrite(ebuf, nil)
	}
}

// BindShop associates a seeded shop NPC's GID with a shop catalog name so
// CZ_CONTACT_NPC resolves the shop instead of starting a dialog script.
func (s *MapServer) BindShop(npcGID uint32, shopName string) {
	if s.shopStore == nil {
		return
	}
	s.shopStore.RegisterShop(npcGID, shopName)
}

// mapAuth is the per-connection auth cache set after a verified CZ_ENTER.
type mapAuth struct {
	accountID uint32
	charID    uint32
}

// handleStatusChange processes CZ_STATUS_CHANGE (0x00bb) — the client's request to
// spend status points on a base stat. It delegates to the world service and sends
// ZC_STATUS_CHANGE_ACK and ZC_PAR_CHANGE back to the client.
func (s *MapServer) handleStatusChange(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		s.log.Warn("map: CZ_STATUS_CHANGE from unauthed conn")
		return
	}
	czReq, err := ropacket.ParseCZStatusChange(frame)
	if err != nil {
		s.log.Debug("map: parse CZ_STATUS_CHANGE", "err", err)
		return
	}

	// Resolve the stat BEFORE spending so an unknown id is refused (ack result
	// 1) rather than silently dropped — the client knows the click failed.
	statName, ok := statusIDToStat[czReq.StatusID]
	if !ok {
		s.log.Debug("map: unknown StatusID in CZ_STATUS_CHANGE", "id", czReq.StatusID)
		s.writeStatusChangeAck(c, ropacket.ZCStatusChangeAck{StatusID: czReq.StatusID, Result: 1})
		return
	}

	newVal, _, _, err := s.world.AllocateStat(auth.charID, statName)
	if err != nil {
		// result 0x01 = insufficient points (also used for cap/unknown-stat —
		// rAthena's clif_status_change_ack result codes).
		s.writeStatusChangeAck(c, ropacket.ZCStatusChangeAck{StatusID: czReq.StatusID, Result: 1})
		return
	}
	s.writeStatusChangeAck(c, ropacket.ZCStatusChangeAck{StatusID: czReq.StatusID, Result: 0, Value: uint8(newVal)}) //nolint:gosec // G115: stat capped at 99.

	// ZC_PAR_CHANGE burst: the raised stat (so the client's stat window updates),
	// the remaining points, and current vitals — one buffered write.
	var buf bytes.Buffer
	_ = ropacket.ParChangeResponse{VarID: czReq.StatusID, Count: int32(newVal)}.Encode(&buf) //nolint:gosec // G115: stat capped at 99.
	e, err := s.world.Get(worlddomain.EntityID(auth.charID))
	if err != nil {
		_ = c.AsyncWrite(buf.Bytes(), nil)
		return
	}
	_ = ropacket.ParChangeResponse{VarID: ropacket.SPStatusPoint, Count: int32(e.StatusPoint)}.Encode(&buf) //nolint:gosec // G115: points bounded by game values.
	_ = ropacket.ParChangeResponse{VarID: ropacket.SPHP, Count: e.HP}.Encode(&buf)
	_ = ropacket.ParChangeResponse{VarID: ropacket.SPSP, Count: e.SP}.Encode(&buf)
	_ = c.AsyncWrite(buf.Bytes(), nil)
}

// writeStatusChangeAck sends ZC_STATUS_CHANGE_ACK to the client.
func (s *MapServer) writeStatusChangeAck(c gnet.Conn, ack ropacket.ZCStatusChangeAck) {
	out := make([]byte, 6) // ZC_STATUS_CHANGE_ACK = 2(cmd) + 2(statusID) + 1(result) + 1(value)
	if err := ack.Encode(sliceWriter(out)); err != nil {
		s.log.Error("map: encode ZC_STATUS_CHANGE_ACK", "err", err)
		return
	}
	_ = c.AsyncWrite(out, nil)
}

// statusIDToStat maps the ropacket SP_* constants to world stat field names.
var statusIDToStat = map[uint16]string{
	ropacket.SPStr: "Str",
	ropacket.SPAgi: "Agi",
	ropacket.SPVit: "Vit",
	ropacket.SPInt: "Int",
	ropacket.SPDex: "Dex",
	ropacket.SPLuk: "Luk",
}

// writeAcceptEnter sends ZC_ACCEPT_ENTER (the self spawn follows in M4 via the
// AOI visibility refresh; for M3b the accept-enter places the client on-map).
func (s *MapServer) writeAcceptEnter(c gnet.Conn, e worlddomain.Entity) {
	resp := ropacket.MapAcceptEnterResponse{
		StartTime: 0, // map-server monotone tick; 0 acceptable for a local clock
		PosX:      e.Pos.X,
		PosY:      e.Pos.Y,
		Dir:       e.Dir,
		XSize:     5, // rAthena hardcodes 5
		YSize:     5,
	}
	out := make([]byte, resp.Size())
	if err := resp.Encode(sliceWriter(out)); err != nil {
		s.log.Error("map: encode accept-enter", "err", err)
		return
	}
	_ = c.AsyncWrite(out, nil)
}

// writeSkillInfoList appends ZC_SKILLINFO_LIST (0x010f) — every learned skill
// from the entity's LearnedSkills map — to the LoadEndAck init burst, matching
// rAthena's clif.cpp skill-list-on-loadend placement. For each skill, it
// resolves SP cost and range from the skill_db registry (if available); unknown
// skills are listed with their id/level only. An empty LearnedSkills map
// appends nothing (the client treats a missing list as no skills).
// skillDataFor builds one SKILLDATA row. skill ids and levels come from the
// persisted skill table and skill_db, both bounded well under uint16.
func (s *MapServer) skillDataFor(skillID int32, level int16) ropacket.SkillData {
	sd := ropacket.SkillData{
		ID:    uint16(skillID), //nolint:gosec // skill ids fit uint16 (max ~5k)
		Level: uint16(level),   //nolint:gosec // skill levels cap at 10
	}
	if s.skillDB == nil {
		return sd
	}
	entry := s.skillDB.Get(skillID)
	if entry == nil {
		return sd
	}
	sd.SP = uint16(s.skillDB.SpCostAt(skillID, int(level))) //nolint:gosec // SP costs are small
	if entry.Range.IsScalar {
		sd.Range2 = uint16(entry.Range.Value) //nolint:gosec // ranges fit uint16
		return sd
	}
	if int(level) <= len(entry.Range.Levels) {
		sd.Range2 = uint16(entry.Range.Levels[int(level)-1].Size) //nolint:gosec // ranges fit uint16
	}
	return sd
}

func (s *MapServer) writeSkillInfoList(burst []byte, e worlddomain.Entity) []byte {
	if len(e.LearnedSkills) == 0 {
		return burst
	}
	skills := make([]ropacket.SkillData, 0, len(e.LearnedSkills))
	for skillID, level := range e.LearnedSkills {
		skills = append(skills, s.skillDataFor(skillID, level))
	}
	resp := ropacket.SkillInfoListResponse{Skills: skills}
	var buf bytes.Buffer
	if err := resp.Encode(&buf); err != nil {
		s.log.Error("map: encode skill info list", "err", err)
		return burst
	}
	return append(burst, buf.Bytes()...)
}

// writeRefuseEnter sends ZC_REFUSE_ENTER.
func (s *MapServer) writeRefuseEnter(c gnet.Conn) {
	resp := ropacket.MapRefuseEnterResponse{Error: mapRefuseRejected}
	out := make([]byte, resp.Size())
	if err := resp.Encode(sliceWriter(out)); err != nil {
		s.log.Error("map: encode refuse-enter", "err", err)
		return
	}
	_ = c.AsyncWrite(out, nil)
}

// Start runs the map listener in a goroutine.
func (s *MapServer) Start(addr string) {
	go func() {
		if err := gnet.Run(s, addr, gnet.WithTicker(true)); err != nil {
			s.log.Error("map listener stopped", "addr", addr, "err", err)
		}
	}()
}

// Stop shuts the listener down.
func (s *MapServer) Stop() {
	if !s.booted {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.engine.Stop(ctx)
	s.booted = false
}
