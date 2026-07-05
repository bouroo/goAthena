package domain

import "time"

// Session is the ephemeral auth node created on successful login. In
// rAthena this is stored in the in-memory auth_db (login.hpp:151-158) with
// a 30s AUTH_TIMEOUT; in goAthena it is persisted in Valkey with the same
// TTL so the gateway and zone service can resolve a login token from any
// pod.
type Session struct {
	// AccountID is the owning account; primary key of the session store.
	AccountID uint32
	// LoginID1 is the first random per-session token (login.cpp:413).
	LoginID1 uint32
	// LoginID2 is the second random per-session token (login.cpp:414).
	LoginID2 uint32
	// ClientType is the client_type byte from CA_LOGIN (loginclif.cpp:274).
	ClientType uint8
	// Sex is a snapshot of the account sex at session creation.
	Sex Sex
	// RemoteIP is the textual peer address that authenticated.
	RemoteIP string
	// CreatedAt is the wall-clock time the session was minted.
	CreatedAt time.Time
}

// SessionTTL is the auth node timeout. rAthena AUTH_TIMEOUT is 30000 ms
// (login.hpp:150); the client must complete the char-server handshake
// inside this window or the node is dropped and the player is bumped from
// online_db.
const SessionTTL = 30 * time.Second
