package handler

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/rs/zerolog"

	"github.com/bouroo/goAthena/internal/features/gateway/domain"
	netcodec "github.com/bouroo/goAthena/internal/infrastructure/net"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// WSHandler accepts roBrowser WebSocket connections at the configured path,
// receives kRO packets as binary WebSocket messages, and decodes them
// through the same codec pipeline as TCPHandler.
//
// One WSHandler owns the packet DB (shared, read-only after construction)
// and a single PacketHandler that owns the business dispatch. Per-
// connection state — a login-mode Decoder and a connection-id counter —
// lives on the WSHandler. Read loops for each connection run in their own
// goroutines; WSHandler is safe to use with concurrent connections.
type WSHandler struct {
	db             *packet.DB
	handler        domain.PacketHandler
	logger         zerolog.Logger
	addr           string
	path           string
	allowedOrigins []string
	nextID         atomic.Uint64
	server         *http.Server
}

// NewWSHandler constructs an HTTP/WebSocket upgrade handler. The packet
// DB and PacketHandler must already be configured; WSHandler does not own
// their lifecycle. addr is the listen address (e.g. ":6901") and path is
// the URL path that triggers the upgrade (e.g. "/ws/"). allowedOrigins is
// the CSWSH origin allowlist applied to the upgrade; when empty, origin
// verification is disabled and a warning is logged per connection (dev
// default). Production deployments must pass a non-empty allowlist.
func NewWSHandler(db *packet.DB, handler domain.PacketHandler, addr, path string, logger zerolog.Logger, allowedOrigins []string) *WSHandler {
	return &WSHandler{
		db:             db,
		handler:        handler,
		addr:           addr,
		path:           path,
		allowedOrigins: allowedOrigins,
		logger:         logger.With().Str("component", "gateway.ws").Logger(),
	}
}

// Start builds the http.Server (mounting the upgrade handler at path and
// a 404 elsewhere) and begins serving on addr. ListenAndServe blocks
// until the server stops, so Start runs it in a goroutine and returns
// after the listener is bound.
func (h *WSHandler) Start(_ context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc(h.path, h.ServeHTTP)
	mux.HandleFunc("/", h.rejectNonUpgrade)

	h.server = &http.Server{
		Addr:              h.addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	listener, err := net.Listen("tcp", h.addr)
	if err != nil {
		return fmt.Errorf("ws listen %s: %w", h.addr, err)
	}

	go func() {
		if serveErr := h.server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			h.logger.Error().Err(serveErr).Msg("ws server stopped unexpectedly")
		}
	}()

	h.logger.Info().
		Str("addr", h.addr).
		Str("path", h.path).
		Msg("ws server listening")
	return nil
}

// Stop gracefully shuts down the HTTP server. Safe to call before Start
// and to call multiple times.
func (h *WSHandler) Stop(ctx context.Context) error {
	if h.server == nil {
		return nil
	}
	if err := h.server.Shutdown(ctx); err != nil {
		return fmt.Errorf("ws server shutdown: %w", err)
	}
	return nil
}

// rejectNonUpgrade responds to any non-upgrade HTTP request on the WS
// listener with 404. roBrowser only connects via the WS upgrade, but the
// listener must not respond to arbitrary HTTP traffic (e.g. probe
// requests) with anything else: returning 404 makes the WS port's role
// unambiguous.
func (h *WSHandler) rejectNonUpgrade(w http.ResponseWriter, _ *http.Request) {
	http.NotFound(w, nil)
}

// ServeHTTP upgrades the HTTP connection to WebSocket and runs the read
// loop until the peer closes or a decode error tears the connection down.
// roBrowser sends kRO packets as binary WS messages; each message may
// contain one or more complete packets, or a partial packet — the codec's
// internal buffer handles framing.
func (h *WSHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	opts := &websocket.AcceptOptions{}
	if len(h.allowedOrigins) > 0 {
		// Enforce origin allowlist — prevents CSWSH.
		opts.OriginPatterns = h.allowedOrigins
	} else {
		// No allowlist configured (dev/local default). Log a warning on
		// each connection so production misconfiguration is visible.
		// Production deployments MUST set gateway.ws.allowed_origins.
		h.logger.Warn().
			Str("remote", r.RemoteAddr).
			Msg("ws accepting connection with no origin allowlist configured; set gateway.ws.allowed_origins in production")
		opts.InsecureSkipVerify = true
	}
	conn, err := websocket.Accept(w, r, opts)
	if err != nil {
		// Accept already wrote an error response on the wire.
		h.logger.Debug().Err(err).Msg("ws upgrade failed")
		return
	}

	id := h.nextID.Add(1)
	decoder := netcodec.NewLoginDecoder(h.db)
	info := domain.ConnectionInfo{
		ID:       id,
		RemoteIP: r.RemoteAddr,
		OpenedAt: time.Now().UnixNano(),
	}

	h.logger.Debug().
		Uint64("conn", id).
		Str("remote", r.RemoteAddr).
		Msg("ws connection opened")

	defer func() {
		_ = conn.Close(websocket.StatusNormalClosure, "")
		h.logger.Debug().
			Uint64("conn", id).
			Str("remote", r.RemoteAddr).
			Int64("open_ns", info.OpenedAt).
			Msg("ws connection closed")
	}()

	// Lift the per-message read limit above the codec's MaxFrameSize (32
	// KiB) so a single WS message can carry multiple back-to-back kRO
	// packets — matches the codec's framing expectations.
	const readLimit = int64(netcodec.MaxFrameSize) * 4
	conn.SetReadLimit(readLimit)

	readCtx, cancel := context.WithCancel(r.Context())
	defer cancel()

	for {
		msgType, data, readErr := conn.Read(readCtx)
		if readErr != nil {
			h.logger.Debug().
				Err(readErr).
				Uint64("conn", id).
				Msg("ws read ended")
			return
		}
		if msgType != websocket.MessageBinary {
			h.logger.Warn().
				Uint64("conn", id).
				Str("msg_type", msgType.String()).
				Int("frame_len", len(data)).
				Msg("ws received non-binary message; closing")
			return
		}

		if err := processBytes(readCtx, decoder, data, info, h.handler); err != nil {
			h.logger.Warn().
				Err(err).
				Uint64("conn", id).
				Int("frame_len", len(data)).
				Msg("ws decode failed; closing connection")
			return
		}
	}
}
