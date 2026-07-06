// Package service contains use-case implementations for the gateway
// feature (WS-A). For Phase 1 the only service is LoggingHandler, which
// records decoded packets to the structured log; Phase 2+ will replace
// this with gRPC forwarding to identity/zone services.
package service

import (
	"context"

	"github.com/rs/zerolog"

	"github.com/bouroo/goAthena/internal/features/gateway/domain"
)

// LoggingHandler is a Phase 1 stub PacketHandler. It logs every decoded
// packet at info level with the connection id, command id, and frame
// length so operators can verify the gateway is alive and observing
// traffic without needing the gRPC forwarding path.
//
// It always returns nil — logging is best-effort and must not tear down
// a connection on its own. The dispatcher (handler.TCPHandler) will
// still close the connection if the underlying decoder returns a
// fatal error.
type LoggingHandler struct {
	logger zerolog.Logger
}

// NewLoggingHandler constructs a logging-backed PacketHandler.
func NewLoggingHandler(logger zerolog.Logger) *LoggingHandler {
	return &LoggingHandler{
		logger: logger.With().Str("component", "gateway.packet").Logger(),
	}
}

// HandlePacket records the packet at info level.
func (h *LoggingHandler) HandlePacket(_ context.Context, info domain.ConnectionInfo, cmd uint16, frame []byte) error {
	h.logger.Info().
		Uint64("conn", info.ID).
		Str("remote", info.RemoteIP).
		Uint16("cmd", cmd).
		Int("frame_len", len(frame)).
		Msg("packet received")
	return nil
}
