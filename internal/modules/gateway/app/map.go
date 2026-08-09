package app

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/panjf2000/gnet/v2"

	chardomain "github.com/bouroo/goAthena/internal/modules/character/domain"
	contentapp "github.com/bouroo/goAthena/internal/modules/content/app"
	invapp "github.com/bouroo/goAthena/internal/modules/inventory/app"
	worldapp "github.com/bouroo/goAthena/internal/modules/world/app"
	worlddomain "github.com/bouroo/goAthena/internal/modules/world/domain"
	ropacket "github.com/bouroo/goAthena/pkg/ro/packet"
)

// czEnterSize is the wire length of CZ_ENTER (0x0072): 2 cmd + 4 AID + 4 GID +
// 4 authCode + 4 clientTime + 1 sex = 19. Matches pkg/ro/packet sizeCZEnter.
const czEnterSize = 19

// mapRefuseRejected mirrors rAthena REFUSE_ENTER_REJECTED (0).
const mapRefuseRejected uint8 = 0

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
	inv     *invapp.InventoryService
	content *contentapp.Engine
	skills  *worldapp.SkillService
	sess    chardomain.SessionStore
	log     *slog.Logger
}

// NewMapServer builds a map listener.
func NewMapServer(world *worldapp.WorldService, spawn *worldapp.SpawnService, combat *worldapp.CombatService, inv *invapp.InventoryService, content *contentapp.Engine, skills *worldapp.SkillService, sess chardomain.SessionStore, log *slog.Logger) (*MapServer, error) {
	return &MapServer{world: world, spawn: spawn, combat: combat, inv: inv, content: content, skills: skills, sess: sess, log: log, handlers: mapHandlers(), db: ropacket.NewMapServerDB()}, nil
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

	s.writeAcceptEnter(c, entity)
	s.log.Info("map entered", "aid", req.AccountID, "gid", req.CharID, "map", entity.Map)
}

// mapAuth is the per-connection auth cache set after a verified CZ_ENTER.
type mapAuth struct {
	accountID uint32
	charID    uint32
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
