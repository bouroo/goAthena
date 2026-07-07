// Package service contains use-case implementations for the gateway
// feature (WS-A). For M1 the service exposes LoggingHandler (still useful
// for tests / debug) and DispatchHandler, which forwards CA_LOGIN to
// the identity service over gRPC and encodes the reply back to the
// client.
package service

import (
	"context"

	"github.com/rs/zerolog"

	"github.com/bouroo/goAthena/internal/features/gateway/domain"
)

// LoggingHandler is a passive PacketHandler sink. It logs every decoded
// packet at info level with the connection id, command id, and frame
// length, and never sends a reply. It always returns nil — logging is
// best-effort and must not tear down a connection on its own. The
// dispatcher (handler.TCPHandler) will still close the connection if the
// underlying decoder returns a fatal error.
//
// Responder is accepted so LoggingHandler remains a valid
// domain.PacketHandler; it is intentionally unused here.
type LoggingHandler struct {
	logger zerolog.Logger
}

// NewLoggingHandler constructs a logging-backed PacketHandler.
func NewLoggingHandler(logger zerolog.Logger) *LoggingHandler {
	return &LoggingHandler{
		logger: logger.With().Str("component", "gateway.packet").Logger(),
	}
}

// HandlePacket records the packet at info level. The Responder is
// ignored — LoggingHandler does not reply.
func (h *LoggingHandler) HandlePacket(_ context.Context, info *domain.ConnectionInfo, _ domain.Responder, cmd uint16, frame []byte) error {
	h.logger.Info().
		Uint64("conn", info.ID).
		Str("remote", info.RemoteIP).
		Uint16("cmd", cmd).
		Int("frame_len", len(frame)).
		Msg("packet received")
	return nil
}
