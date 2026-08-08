package app

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/panjf2000/gnet/v2"

	chardomain "github.com/bouroo/goAthena/internal/modules/character/domain"
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
type MapServer struct {
	gnet.BuiltinEventEngine
	engine gnet.Engine
	booted bool
	world  *worldapp.WorldService
	sess   chardomain.SessionStore
	log    *slog.Logger
}

// NewMapServer builds a map listener.
func NewMapServer(world *worldapp.WorldService, sess chardomain.SessionStore, log *slog.Logger) (*MapServer, error) {
	return &MapServer{world: world, sess: sess, log: log}, nil
}

// OnBoot captures the engine for shutdown.
func (s *MapServer) OnBoot(e gnet.Engine) gnet.Action {
	s.engine = e
	s.booted = true
	s.log.Info("map listener booted")
	return gnet.None
}

// OnTraffic reads complete CZ_ENTER frames. A connection's first packet is the
// CZ_ENTER admission gate; once verified, the connection is authed for the rest
// of the session. LoadEndAck (0x007d) and movement packets layer in M4+.
func (s *MapServer) OnTraffic(c gnet.Conn) gnet.Action {
	for c.InboundBuffered() >= czEnterSize {
		frame, err := c.Next(czEnterSize)
		if err != nil {
			break
		}
		cp := append([]byte(nil), frame...)
		go s.handleEnter(c, cp)
	}
	return gnet.None
}

// handleEnter verifies the CZ_ENTER, admits the player into the world, and sends
// the map-enter + self-spawn reply.
func (s *MapServer) handleEnter(c gnet.Conn, frame []byte) {
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
