package domain

import (
	"context"
	"net/netip"
)

// LoginRequest carries decoded CA_LOGIN fields from the gateway. The
// password is already decrypted/normalized by the codec — plaintext for
// passwdenc=0 or the client-computed MD5 hex otherwise. Method tells the
// service whether the supplied Password is a raw plaintext string or an
// MD5 hex digest so the verifier can pick the right comparison path.
type LoginRequest struct {
	// UserID is the account login name as supplied by the client.
	UserID string
	// Password is the post-decoding credential; raw plaintext when
	// Method == PassEncodingPlain, MD5-hex when Method == PassEncodingMD5.
	Password string
	// ClientType is the client_type byte from CA_LOGIN (loginclif.cpp:274).
	ClientType uint8
	// Method declares how Password is encoded.
	Method PasswordEncoding
	// RemoteIP is the peer address that opened the TCP session.
	RemoteIP netip.Addr
}

// LoginResponse is the result of a successful authentication. It bundles
// the persisted account, the minted session (already written to the
// SessionRepository), and the list of char-server endpoints the client
// may connect to. CharServers may be empty for single-server deployments.
type LoginResponse struct {
	// Account is the loaded account row.
	Account *Account
	// Session is the freshly minted auth node, already persisted.
	Session *Session
	// CharServers is the list of endpoints to advertise in
	// AC_ACCEPT_LOGIN (loginclif.cpp:130-159).
	CharServers []CharServerEndpoint
}

// CharServerEndpoint describes a character server the client can connect
// to. It maps 1:1 to the char_server entries serialized in
// AC_ACCEPT_LOGIN (loginclif.cpp:130-159).
type CharServerEndpoint struct {
	// IP is the textual address the client should dial.
	IP string
	// Port is the TCP port.
	Port uint16
	// Name is the human-readable server name shown in the server list.
	Name string
}

// IdentityService is the inbound port — the use case interface invoked by
// the gRPC handler. Concrete implementations live under
// internal/features/identity/service.
type IdentityService interface {
	// Login authenticates a user and creates a session. It returns the
	// non-nil AuthError equivalent as a typed error from the service
	// package on failure; the handler maps that error to the AC_REFUSE_LOGIN
	// wire code.
	Login(ctx context.Context, req LoginRequest) (*LoginResponse, error)
	// ListCharacters returns the character list for an authenticated
	// account, ordered by slot. The handler serializes this into
	// HC_ACCEPT_ENTER 0x6b (char_clif.cpp:408-430).
	ListCharacters(ctx context.Context, accountID uint32) ([]CharacterSummary, error)
}
