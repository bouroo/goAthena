// Package domain defines the gateway ingress contract: the transport-agnostic
// connection abstraction, per-role packet routing, and the handler signature
// every inbound opcode resolves to. It is pure — no transport or persistence
// dependencies — so the TCP and WebSocket adapters, the handlers, and tests
// all depend on these types rather than on each other.
package domain

// Role identifies which packet table a connection dispatches against. There is
// no in-packet marker distinguishing a map packet from a login packet, so the
// role is carried in connection state, set as the handshake advances:
// RoleLogin → RoleChar → RoleMap.
type Role int

const (
	// RoleLogin is the login-server table (CA_LOGIN, CA_REQ_HASH, ...). Every
	// connection starts here.
	RoleLogin Role = iota
	// RoleChar is the char-server table (CH_ENTER, CH_SELECT_CHAR, ...).
	RoleChar
	// RoleMap is the zone/map table (CZ_ENTER, CZ_REQUEST_MOVE, ...). Map
	// traffic is obfuscated, so reaching this role also swaps the codec.
	RoleMap
)

// String reports the role name for logs.
func (r Role) String() string {
	switch r {
	case RoleLogin:
		return "login"
	case RoleChar:
		return "char"
	case RoleMap:
		return "map"
	default:
		return "unknown"
	}
}

// Frame is one decoded inbound packet. Raw is the full on-wire frame including
// the 2-byte command header, exactly as returned by the codec Decoder — so a
// handler hands it straight to a packet.Parse* function (which reads cmd from
// Raw[0:2]). Cmd is the decoded opcode, duplicated for routing convenience.
type Frame struct {
	Cmd uint16
	Raw []byte
}

// ConnAuth is the per-connection auth state. The login/char listener multiplexes
// login and char on one connection (the role advances RoleLogin → RoleChar
// in-connection), so the auth minted at CA_LOGIN is a valid trust anchor for
// CH_ENTER on that same connection. The map listener is different: a client
// reconnects to it after CH_SELECT_CHAR, so the fresh map connection carries no
// cache yet — CZ_ENTER re-verifies against the session store and only then
// populates this cache for the map packets that follow.
//
// Copy semantics: a single dispatch goroutine owns each connection's reads and
// writes, so Auth/SetAuth need no synchronization until M4 introduces
// cross-connection broadcast writes. A zero-value ConnAuth (AccountID == 0)
// means the connection has not completed login-accept (or, on the map listener,
// has not yet passed CZ_ENTER).
type ConnAuth struct {
	AccountID uint32
	LoginID1  uint32
	LoginID2  uint32
	Sex       uint8
}

// Conn is the gateway's transport-agnostic view of a game connection. The TCP
// (gnet) and WebSocket (coder/websocket) adapters implement it; handlers and
// the dispatcher program against this interface.
//
// Write must be safe to call from the dispatch goroutine (the single reader
// per connection). Broadcast-to-many writes arrive at M4 and bring their own
// synchronization; until then each connection is read and written by one
// goroutine.
type Conn interface {
	// Role reports the packet table this connection currently dispatches.
	Role() Role
	// SetRole advances the connection to a new table (login → char → map).
	SetRole(Role)
	// Auth returns the cached login credentials (zero-valued before they are
	// populated). On the login/char listener, the cache is set at login-accept
	// and CH_ENTER trusts it — the connection itself proves the login that
	// minted it was accepted. On the map listener the connection is a fresh
	// reconnect, so the cache is empty until CZ_ENTER verifies against the
	// session store; CZ_ENTER then sets it for the map packets that follow.
	Auth() ConnAuth
	// SetAuth caches the login credentials at accept time.
	SetAuth(ConnAuth)
	// RemoteAddr is the peer address, for logging only.
	RemoteAddr() string
	// Write sends raw response bytes to the client.
	Write(p []byte) error
	// Close terminates the connection.
	Close() error
}
