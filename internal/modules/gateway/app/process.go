// Package app implements the gateway's table-driven dispatch and the
// transport-agnostic frame-processing loop that feeds the codec Decoder. The
// TCP (gnet) and WebSocket (coder/websocket) adapters supply bytes and a Conn;
// this package does the rest.
package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/rs/zerolog"

	netcodec "github.com/bouroo/goAthena/internal/infrastructure/net"
	"github.com/bouroo/goAthena/internal/modules/gateway/domain"
)

// ProcessBytes feeds a chunk of raw inbound bytes through the codec Decoder and
// dispatches each complete frame via d. It is the shared core of both ingress
// transports: the caller owns the per-connection Decoder and Conn, ProcessBytes
// owns framing and routing.
//
// Error policy (mirrors the rAthena clif.cpp parse loop):
//   - net.ErrIncomplete: the buffer holds a partial frame. Return nil so the
//     transport feeds the next chunk; the decoder retains the partial bytes.
//   - net.ErrUnknownPacket or a malformed wire length: the byte stream is
//     untrusted. Return the wrapped error so the transport closes the
//     connection (clif.cpp disconnects on unknown commands).
//   - ErrNoHandler or any handler error: logged with opcode and role context,
//     then the loop continues. One unhandled or failing packet must not kill
//     the session; handlers own their atomicity via Unit-of-Work.
func ProcessBytes(
	ctx context.Context,
	log *zerolog.Logger,
	conn domain.Conn,
	dec *netcodec.Decoder,
	buf []byte,
	d *domain.Dispatcher,
) error {
	dec.Feed(buf)
	for {
		cmd, frame, err := dec.Next()
		switch {
		case errors.Is(err, netcodec.ErrIncomplete):
			return nil
		case err != nil:
			return fmt.Errorf("decode frame: %w", err)
		}

		if err := d.Dispatch(ctx, conn, cmd, frame); err != nil {
			if errors.Is(err, domain.ErrNoHandler) {
				log.Warn().Uint16("cmd", cmd).Str("role", conn.Role().String()).Msg("no handler for opcode")
			} else {
				log.Error().Err(err).Uint16("cmd", cmd).Str("role", conn.Role().String()).Msg("handler error")
			}
			continue
		}
	}
}
