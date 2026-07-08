package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"

	natsgo "github.com/nats-io/nats.go"
	"github.com/rs/zerolog"
	"google.golang.org/protobuf/proto"

	zonev1 "github.com/bouroo/goAthena/api/pb/zone/v1"
	"github.com/bouroo/goAthena/internal/features/gateway/domain"
	natsinfra "github.com/bouroo/goAthena/internal/infrastructure/messaging/nats"
)

// broadcastPacket is the encode contract the three observer packets
// (ZC_UNIT_WALKING 0x09fd, ZC_SPAWN_UNIT 0x09fe, ZC_NOTIFY_VANISH
// 0x0080) share. The fan-out encodes a packet once into a buffer and
// sends the same bytes to every observer on the target map, so the
// encoder must be deterministic and side-effect-free.
type broadcastPacket interface {
	Encode(io.Writer) error
	Size() int
}

// AreaSender spawns the entities already present on a map to a client
// that has just entered it (rAthena's clif_getareachar_unit direction).
// The dispatch handler calls this immediately after the entering
// client's CZ_ENTER handshake completes so the new player can see
// every other session that joined before them.
//
// AreaSender is satisfied by *BroadcastSubscriber; a nil
// *BroadcastSubscriber implements it as a no-op (the dispatch handler
// holds a nil pointer in tests where the broadcast path is not
// exercised).
type AreaSender interface {
	SendAreaEntities(mapName string, excludeAID uint32, resp domain.Responder)
}

// BroadcastSubscriber subscribes to zone events over NATS and fans
// them out to the observer clients connected through this gateway. It
// is the gateway side of the Phase-1 broadcast path: the zone
// publishes EntityMoved/EntitySpawned/EntityVanished per map; the
// gateway translates each into the matching ZC_ broadcast packet and
// pushes it to every session on that map except the moving/spawning/
// vanishing entity itself.
//
// It also tracks each entity's last-known cell (updated from the same
// events) so that SendAreaEntities can spawn already-present entities
// to a client that enters the map after them. Without the cache the
// second player to enter a map would never see the first until the
// first moved — because the EntitySpawned for player A fires before
// player B exists, and the gateway has no way to back-fill A's spawn
// at B's enter time unless it has been tracking A's cell from the
// prior events.
//
// A nil *BroadcastSubscriber is a safe no-op for SendAreaEntities;
// callers (the dispatch handler) may hold a nil pointer in tests where
// the broadcast path is not exercised.
type BroadcastSubscriber struct {
	nc       *natsinfra.Client
	registry SessionRegistry
	logger   zerolog.Logger

	// sub guards sub (the NATS subscription) against Start/Stop races.
	// The NATS callback is invoked on a NATS-managed goroutine and
	// itself never touches sub; it only reads the registry / position
	// cache, so sub is the only field that needs explicit locking.
	sub   *natsgo.Subscription
	subMu sync.Mutex

	// posMu guards pos. The map is read on every fan-out (on the NATS
	// callback goroutine) and on every on-enter SendAreaEntities call
	// (the dispatch goroutine), so reads use RLock; writes (trackPos
	// / untrackPos) use Lock.
	posMu sync.RWMutex
	pos   map[uint32]posCell
}

// posCell is the per-entity last-known cell carried by the broadcast
// position cache. It is keyed by AID — the same key the registry uses
// — so an entity's spawn / move / vanish events all land on the same
// cache row.
type posCell struct{ X, Y int16 }

// NewBroadcastSubscriber constructs a BroadcastSubscriber that will
// read zone events from nc, fan them out to the sessions in registry,
// and back its position cache with the same events. The subscriber is
// not yet attached to the NATS subject; call Start to subscribe.
func NewBroadcastSubscriber(nc *natsinfra.Client, registry SessionRegistry, logger zerolog.Logger) *BroadcastSubscriber {
	return &BroadcastSubscriber{
		nc:       nc,
		registry: registry,
		logger:   logger,
	}
}

// Start subscribes the broadcaster to zone.event.> and begins
// dispatching incoming events to handleMsg. The NATS Subscribe call
// has no context-aware variant that fits the Subscribe API, so ctx is
// accepted only for lifecycle symmetry with the rest of the gateway's
// startable services; cancellation is observed via Stop instead.
//
// Calling Start twice without an intervening Stop returns an error so
// the DI bootstrap cannot accidentally open a second NATS subscription
// on a single subscriber.
func (b *BroadcastSubscriber) Start(_ context.Context) error {
	if b == nil {
		return fmt.Errorf("gateway broadcast: subscriber is nil")
	}
	if b.nc == nil {
		return fmt.Errorf("gateway broadcast: nats client is nil")
	}
	subject := natsinfra.SubjectZoneEventPrefix + ".>"
	b.subMu.Lock()
	defer b.subMu.Unlock()
	if b.sub != nil {
		return fmt.Errorf("gateway broadcast: already started")
	}
	sub, err := b.nc.Subscribe(subject, b.handleMsg)
	if err != nil {
		return fmt.Errorf("gateway broadcast: subscribe %q: %w", subject, err)
	}
	b.sub = sub
	return nil
}

// Stop unsubscribes from the NATS subject. It is safe to call on a
// never-started subscriber (returns nil) and to call repeatedly
// (subsequent calls return nil). Stop does NOT close the NATS
// connection — the nats Shutdowner owns that lifecycle.
func (b *BroadcastSubscriber) Stop() error {
	b.subMu.Lock()
	sub := b.sub
	b.sub = nil
	b.subMu.Unlock()
	if sub == nil {
		return nil
	}
	if err := sub.Unsubscribe(); err != nil {
		return fmt.Errorf("gateway broadcast: unsubscribe: %w", err)
	}
	return nil
}

// handleMsg is the NATS callback for every incoming zone event. It is
// recover-guarded so a panic in a per-session SendPacket or encoder
// cannot take down the whole subscription — the surviving sessions
// still get the broadcast.
//
// The if-chain (GetMoved / GetSpawned / GetVanished) intentionally
// avoids a `switch` on the oneof: protobuf-generated oneof switches
// trip the `exhaustive` linter and force a no-op default arm, while
// the if-chain expresses the same dispatch with nil-checks against
// the pointer variants.
func (b *BroadcastSubscriber) handleMsg(msg *natsgo.Msg) {
	if msg == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			b.logger.Error().
				Interface("panic", r).
				Str("subject", msg.Subject).
				Msg("gateway broadcast: handler panic recovered")
		}
	}()

	mapName, ok := parseMapFromSubject(msg.Subject)
	if !ok {
		b.logger.Debug().
			Str("subject", msg.Subject).
			Msg("gateway broadcast: ignoring non-map subject")
		return
	}

	var evt zonev1.ZoneEvent
	if err := proto.Unmarshal(msg.Data, &evt); err != nil {
		b.logger.Warn().
			Err(err).
			Str("subject", msg.Subject).
			Str("map", mapName).
			Msg("gateway broadcast: unmarshal zone event")
		return
	}

	if m := evt.GetMoved(); m != nil {
		b.onMoved(mapName, m)
		return
	}
	if s := evt.GetSpawned(); s != nil {
		b.onSpawned(mapName, s)
		return
	}
	if v := evt.GetVanished(); v != nil {
		b.onVanished(mapName, v)
		return
	}
}

// onMoved updates the position cache before fanning out, so a
// SendAreaEntities call that races a move still sees the latest cell.
// The mover's own session is excluded by fanout; the move event is
// also recorded before lookup so the cache is current even when the
// mover has no ViewData registered yet (an unregistered entity
// produces a debug log but still updates its cached position —
// which is the right behaviour for an AID the gateway has not yet
// attributed a view to).
func (b *BroadcastSubscriber) onMoved(mapName string, m *zonev1.EntityMoved) {
	if m == nil {
		return
	}
	moverAID := m.GetEntityId()
	b.trackPos(moverAID, int16(m.GetDestX()), int16(m.GetDestY())) //nolint:gosec // map coords fit int16 wire slot
	if view, ok := b.lookupView(moverAID); ok {
		b.fanout(mapName, moverAID, unitWalkingFromEvent(view, m))
		return
	}
	b.logger.Debug().
		Uint32("aid", moverAID).
		Str("map", mapName).
		Msg("gateway broadcast: moved event for unregistered entity")
}

// onSpawned mirrors onMoved: track before fan-out, exclude the
// spawned entity from its own broadcast, and log a debug line for
// unregistered AIDs (monsters and NPCs enter the broadcast stream
// too, and the gateway only registers PC sessions — so a non-PC
// spawn is expected and must not be treated as an error).
func (b *BroadcastSubscriber) onSpawned(mapName string, s *zonev1.EntitySpawned) {
	if s == nil {
		return
	}
	aid := s.GetEntityId()
	b.trackPos(aid, int16(s.GetX()), int16(s.GetY())) //nolint:gosec // map coords fit int16 wire slot
	if view, ok := b.lookupView(aid); ok {
		b.fanout(mapName, aid, spawnFromView(view, int16(s.GetX()), int16(s.GetY()))) //nolint:gosec // map coords fit int16 wire slot
		return
	}
	b.logger.Debug().
		Uint32("aid", aid).
		Str("map", mapName).
		Msg("gateway broadcast: spawned event for unregistered entity (non-PC or view not cached)")
}

// onVanished always fans out the vanish broadcast — clients need to
// remove the entity from their local view regardless of the reason —
// and only drops the cached position on a true LOGOUT. OUT_OF_SIGHT
// (the entity walked out of every observer's view range) and
// TELEPORT (the entity moved to another map) leave the cache
// intact: a re-appearing PC must re-spawn, but a momentary
// out-of-sight or a cross-map teleport does not invalidate the
// cell we have for them should they re-enter the map.
func (b *BroadcastSubscriber) onVanished(mapName string, v *zonev1.EntityVanished) {
	if v == nil {
		return
	}
	aid := v.GetEntityId()
	b.fanout(mapName, aid, vanishFromEvent(v))
	if v.GetType() == 1 {
		b.untrackPos(aid)
	}
}

// lookupView returns the ViewData snapshot for aid if the session is
// currently registered. A false return means the entity is either a
// non-PC (NPC / monster) or a PC that has not yet completed
// CZ_ENTER; in both cases the broadcaster should not emit a unit
// packet because the observer client cannot attribute the unit.
func (b *BroadcastSubscriber) lookupView(aid uint32) (domain.ViewData, bool) {
	s, ok := b.registry.Get(aid)
	if !ok {
		return domain.ViewData{}, false
	}
	return s.View, true
}

// trackPos stores the most recent cell for aid, allocating the map
// lazily. It is safe for concurrent use; the cache is only ever
// written from the NATS callback goroutine, but the lazy allocation
// keeps the zero value of BroadcastSubscriber usable.
func (b *BroadcastSubscriber) trackPos(aid uint32, x, y int16) {
	b.posMu.Lock()
	defer b.posMu.Unlock()
	if b.pos == nil {
		b.pos = make(map[uint32]posCell)
	}
	b.pos[aid] = posCell{X: x, Y: y}
}

// untrackPos removes aid from the position cache. Called only on
// LOGOUT vanish events; see onVanished for the rationale.
func (b *BroadcastSubscriber) untrackPos(aid uint32) {
	b.posMu.Lock()
	defer b.posMu.Unlock()
	delete(b.pos, aid)
}

// posOf returns the most recent cell for aid, or false if the cache
// has not seen a Spawned / Moved event for that AID. Callers (the
// on-enter helper) treat a false return as "no spawn cell known;
// skip the entity" rather than a fatal error — the entity may be a
// PC that entered the map before the gateway received any events
// for them.
func (b *BroadcastSubscriber) posOf(aid uint32) (posCell, bool) {
	b.posMu.RLock()
	defer b.posMu.RUnlock()
	p, ok := b.pos[aid]
	return p, ok
}

// fanout encodes pkt once and pushes the resulting bytes to every
// session on mapName except excludeAID. Encoding once is the entire
// point of this helper — the alternative (encode per session) would
// multiply the encoder work by the observer count and would make
// per-byte timings visible at the wire level.
//
// A failed encode is logged and the fan-out is aborted: every
// observer would otherwise receive a partial / corrupted packet.
func (b *BroadcastSubscriber) fanout(mapName string, excludeAID uint32, pkt broadcastPacket) {
	if pkt == nil {
		return
	}
	buf := make([]byte, 0, pkt.Size())
	w := bytes.NewBuffer(buf)
	if err := pkt.Encode(w); err != nil {
		b.logger.Warn().
			Err(err).
			Str("map", mapName).
			Msg("gateway broadcast: encode observer packet")
		return
	}
	encoded := w.Bytes()
	b.registry.ForEachOnMap(mapName, func(aid uint32, s domain.Session) {
		if aid == excludeAID {
			return
		}
		b.sendSafe(s.Responder, encoded, aid)
	})
}

// sendSafe invokes resp.SendPacket with a recover guard so a single
// dead client (closed channel, nil interface, panic in a buggy
// transport) cannot stall the broadcast — the surviving sessions
// must still receive the packet. SendPacket itself is contractually
// non-blocking; the recover is a belt-and-braces against
// implementation bugs.
func (b *BroadcastSubscriber) sendSafe(resp domain.Responder, p []byte, aid uint32) {
	if resp == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			b.logger.Warn().
				Interface("panic", r).
				Uint32("aid", aid).
				Msg("gateway broadcast: send panic recovered")
		}
	}()
	if err := resp.SendPacket(p); err != nil {
		b.logger.Warn().
			Err(err).
			Uint32("aid", aid).
			Msg("gateway broadcast: send failed")
	}
}

// SendAreaEntities spawns every other session on mapName to resp
// (the entering client), using the cached position to populate the
// spawn cell. The dispatch handler calls this after the entering
// client's CZ_ENTER completes.
//
// excludeAID is the entering client's own AID — the entering client
// already receives a self-spawn from buildSelfSpawn, so re-emitting
// its own spawn here would double-spawn the local player.
//
// SendAreaEntities is nil-receiver safe: a nil *BroadcastSubscriber
// is a no-op, which lets the dispatch handler hold a nil pointer
// in unit tests where the broadcast path is not exercised.
func (b *BroadcastSubscriber) SendAreaEntities(mapName string, excludeAID uint32, resp domain.Responder) {
	if b == nil || resp == nil {
		return
	}
	b.registry.ForEachOnMap(mapName, func(aid uint32, s domain.Session) {
		if aid == excludeAID {
			return
		}
		pos, ok := b.posOf(aid)
		if !ok {
			b.logger.Debug().
				Uint32("aid", aid).
				Str("map", mapName).
				Msg("gateway broadcast: no cached position for area entity; skipping")
			return
		}
		pkt := spawnFromView(s.View, pos.X, pos.Y)
		var buf bytes.Buffer
		if err := pkt.Encode(&buf); err != nil {
			b.logger.Warn().
				Err(err).
				Uint32("aid", aid).
				Str("map", mapName).
				Msg("gateway broadcast: encode area spawn")
			return
		}
		b.sendSafe(resp, buf.Bytes(), aid)
	})
}
