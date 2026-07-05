// Package domain contains entities and port interfaces for the gateway
// feature (WS-A): packet codec, TCP/WS ingress, gRPC routing.
package domain

import "context"

// ConnectionInfo describes a single accepted TCP connection. It is built
// once at OnOpen time and threaded through the PacketHandler so handlers
// can log the peer and timestamp without re-querying gnet.Conn.
type ConnectionInfo struct {
	ID       uint64
	RemoteIP string
	OpenedAt int64 // unix nanos
}

// PacketHandler processes a decoded kRO packet. The gateway calls this for
// each packet extracted from the TCP stream by the codec.
//
// Returning a non-nil error signals that the connection should be closed;
// the gnet layer treats handler errors as fatal and tears the connection
// down. Phase 1 returns nil for every packet (LoggingHandler); Phase 2+
// will replace the logging handler with gRPC forwarding to identity/zone
// and may surface transient backend errors here.
type PacketHandler interface {
	HandlePacket(ctx context.Context, conn ConnectionInfo, cmd uint16, frame []byte) error
}
