// Package domain contains entities and port interfaces for the gateway
// feature (WS-A): packet codec, TCP/WS ingress, gRPC routing.
package domain

import "context"

// ConnectionInfo describes a single accepted TCP connection. It is built
// once at OnOpen time and threaded through the PacketHandler so handlers
// can log the peer and timestamp without re-querying gnet.Conn.
//
// AccountID and CharID are the only mutable fields: the dispatch handler
// sets them after a successful CZ_ENTER so subsequent CZ_REQUEST_MOVE
// packets can be attributed to the right zone entity without re-deriving
// the AID from the wire (the move packet carries no AID) and the
// post-actorinit status burst (M9) can fetch the character's stats via
// identity.GetCharacter. The handler chain takes the info by pointer so
// mutations persist across packets on the same connection.
type ConnectionInfo struct {
	ID        uint64
	RemoteIP  string
	OpenedAt  int64  // unix nanos
	AccountID uint32 // set by handleCZEnter on successful map enter
	CharID    uint32 // set by handleCZEnter on successful map enter
}

// Responder sends serialized packets back to the client. Each transport
// (gnet TCP, coder/websocket) supplies its own implementation; the port
// abstracts over async-write semantics so handlers stay transport-agnostic.
//
// SendPacket MUST be safe to call from the dispatch goroutine. For the TCP
// transport it delegates to gnet.Conn.AsyncWrite, which queues the buffer
// on the connection's outbound ring and returns immediately; for the WS
// transport it serializes over the active WebSocket read context.
type Responder interface {
	SendPacket(p []byte) error
}

// PacketHandler processes a decoded kRO packet. The gateway calls this for
// each packet extracted from the TCP stream by the codec.
//
// The handler uses resp to send replies (AC_ACCEPT_LOGIN / AC_REFUSE_LOGIN
// / …) back over the originating transport. Returning a non-nil error
// signals that the connection should be closed; the gnet layer treats
// handler errors as fatal and tears the connection down. Handlers that
// want the connection to stay open after a transient backend failure must
// log the cause and return nil.
//
// conn is passed by pointer so handlers can persist per-connection state
// (e.g. AccountID after CZ_ENTER) across successive packets on the same
// connection.
type PacketHandler interface {
	HandlePacket(ctx context.Context, conn *ConnectionInfo, resp Responder, cmd uint16, frame []byte) error
}
