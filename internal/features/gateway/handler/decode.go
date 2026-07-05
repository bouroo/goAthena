package handler

import (
	"context"
	"errors"
	"fmt"

	"github.com/bouroo/goAthena/internal/features/gateway/domain"
	netcodec "github.com/bouroo/goAthena/internal/infrastructure/net"
)

// processBytes is the transport-agnostic decode + dispatch core. It feeds
// data into the Decoder, then drains every complete packet via Next,
// calling handler.HandlePacket for each one. ErrIncomplete is the normal
// "wait for more bytes" signal and returns nil; every other decoder error
// is wrapped and returned so the caller can decide to close the
// connection.
//
// Shared by TCPHandler (gnet, bytes drained from the inbound buffer on
// OnTraffic) and WSHandler (coder/websocket, one binary message per Read).
func processBytes(
	ctx context.Context,
	decoder *netcodec.Decoder,
	data []byte,
	info domain.ConnectionInfo,
	handler domain.PacketHandler,
) error {
	decoder.Feed(data)

	for {
		cmd, frame, err := decoder.Next()
		if err != nil {
			if errors.Is(err, netcodec.ErrIncomplete) {
				return nil
			}
			return fmt.Errorf("decode packet: %w", err)
		}
		if err := handler.HandlePacket(ctx, info, cmd, frame); err != nil {
			return fmt.Errorf("handle packet 0x%04x: %w", cmd, err)
		}
	}
}
