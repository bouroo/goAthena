// Package handler contains transport-layer adapters for the gateway
// feature (WS-A): the gnet TCP event handler and the WebSocket upgrade
// handler for the kRO / roBrowser client ingress.
package handler

import (
	"context"
	"time"

	"github.com/panjf2000/gnet/v2"
	"github.com/rs/zerolog"

	"github.com/bouroo/goAthena/internal/features/gateway/domain"
	netcodec "github.com/bouroo/goAthena/internal/infrastructure/net"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// TCPHandler implements gnet.EventHandler for the gateway's kRO TCP ingress.
//
// One TCPHandler owns the packet DB (shared, read-only after construction)
// and a single PacketHandler that owns the business dispatch. Per-connection
// state — a login-mode Decoder and a connection-id counter — lives in
// gnet.Conn.SetContext / Context, never in TCPHandler fields, so gnet's
// multicore event-loop model remains safe.
type TCPHandler struct {
	gnet.BuiltinEventEngine

	db      *packet.DB
	handler domain.PacketHandler
	logger  zerolog.Logger
	nextID  uint64 // monotonic connection id; incremented under no contention — see OnOpen
	engine  gnet.Engine
}

// NewTCPHandler constructs a gnet TCP event handler. The packet DB and
// PacketHandler must already be configured; TCPHandler does not own their
// lifecycle.
func NewTCPHandler(db *packet.DB, handler domain.PacketHandler, logger zerolog.Logger) *TCPHandler {
	return &TCPHandler{
		db:      db,
		handler: handler,
		logger:  logger.With().Str("component", "gateway.tcp").Logger(),
	}
}

// OnBoot logs the engine start and captures the engine handle for later
// graceful shutdown. gnet calls this once per process.
func (h *TCPHandler) OnBoot(eng gnet.Engine) gnet.Action {
	h.engine = eng
	h.logger.Info().Msg("gnet TCP engine booted")
	return gnet.None
}

// Engine returns the gnet.Engine captured at OnBoot. It is nil before the
// engine boots. Used by the composition root to invoke Engine.Stop for
// graceful shutdown (gnet.Stop is deprecated in v2).
func (h *TCPHandler) Engine() gnet.Engine {
	return h.engine
}

// OnShutdown logs the engine stop. gnet calls this when the listener
// exits cleanly (gnet.Stop or signal).
func (h *TCPHandler) OnShutdown(eng gnet.Engine) {
	h.logger.Info().Msg("gnet TCP engine shutting down")
}

// OnOpen creates a per-connection login-mode Decoder and stores it in
// gnet.Conn context. The connection id is monotonic; gnet does not
// guarantee uniqueness across restarts but does guarantee uniqueness
// within a process — which is what handlers need.
func (h *TCPHandler) OnOpen(c gnet.Conn) ([]byte, gnet.Action) {
	id := h.nextID
	h.nextID++

	decoder := netcodec.NewLoginDecoder(h.db)
	c.SetContext(&connState{
		info: domain.ConnectionInfo{
			ID:       id,
			RemoteIP: c.RemoteAddr().String(),
			OpenedAt: time.Now().UnixNano(),
		},
		decoder: decoder,
	})

	h.logger.Debug().
		Uint64("conn", id).
		Str("remote", c.RemoteAddr().String()).
		Msg("connection opened")
	return nil, gnet.None
}

// OnClose logs the disconnect and lets gnet drop the context.
func (h *TCPHandler) OnClose(c gnet.Conn, err error) gnet.Action {
	state, ok := c.Context().(*connState)
	if !ok || state == nil {
		return gnet.None
	}
	evt := h.logger.Debug().
		Uint64("conn", state.info.ID).
		Str("remote", state.info.RemoteIP).
		Int64("open_ns", state.info.OpenedAt)
	if err != nil {
		evt = evt.Err(err)
	}
	evt.Msg("connection closed")
	return gnet.None
}

// OnTraffic drains the inbound buffer into the connection's Decoder,
// extracts as many packets as are available, and dispatches each to the
// PacketHandler. Returns gnet.Close on any non-ErrIncomplete decoder
// error so a malformed or unknown packet tears the connection down
// (matches rathena/src/map/clif.cpp:25718-25744).
func (h *TCPHandler) OnTraffic(c gnet.Conn) gnet.Action {
	state, ok := c.Context().(*connState)
	if !ok || state == nil {
		h.logger.Error().Msg("OnTraffic on conn with no decoder context; closing")
		return gnet.Close
	}

	n := c.InboundBuffered()
	if n == 0 {
		return gnet.None
	}
	buf, err := c.Next(n)
	if err != nil {
		h.logger.Error().Err(err).Uint64("conn", state.info.ID).Msg("gnet Next failed; closing")
		return gnet.Close
	}

	if err := processBytes(context.Background(), state.decoder, buf, state.info, h.handler); err != nil {
		h.logger.Warn().
			Err(err).
			Uint64("conn", state.info.ID).
			Int("frame_len", len(buf)).
			Msg("decode failed; closing connection")
		return gnet.Close
	}
	return gnet.None
}

// connState is the per-connection payload stored on gnet.Conn via
// SetContext. It is not safe for concurrent use; gnet serializes
// EventHandler calls per connection.
type connState struct {
	info    domain.ConnectionInfo
	decoder *netcodec.Decoder
}
