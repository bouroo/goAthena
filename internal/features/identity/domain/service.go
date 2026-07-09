package domain

import (
	"context"
	"net/netip"

	inventorydomain "github.com/bouroo/goAthena/internal/features/inventory/domain"
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
	// GetCharacter returns the full character detail (name, class, level,
	// HP, hair, equipment, sex) for a single character on an
	// authenticated account. Returns ErrCharacterNotFound (wrapped, with
	// a domain message) when the (accountID, charID) pair does not match
	// any row; the handler maps that onto success=false. accountID or
	// charID == 0 is rejected with ErrCharacterNotFound so callers can't
	// accidentally query the all-zeros key.
	GetCharacter(ctx context.Context, accountID, charID uint32) (*CharacterSummary, error)

	// GetInventory returns every item the given character owns. The
	// (accountID, charID) pair is treated defensively — both must be
	// non-zero and the row must belong to charID. Wraps
	// inventorydomain.ErrItemNotFound (a no-op for ListByChar, which
	// returns an empty slice instead) so callers can still distinguish
	// missing inventory from outright repo failure.
	GetInventory(ctx context.Context, accountID, charID uint32) ([]inventorydomain.InventoryItem, error)

	// EquipItem moves the item into the requested EQP_* bitmask. It
	// verifies ownership before mutating; a mismatch surfaces as a
	// wrapped ErrItemNotFound so the handler can map it onto
	// success=false. Weight recalculation is intentionally NOT applied
	// here — that is a separate concern (see TODO(P2A-WEIGHT) in the
	// service).
	EquipItem(ctx context.Context, accountID, charID, itemID, equipPos uint32) error

	// UnequipItem clears the EQP_* bitmask back to 0 (in-grid). Same
	// ownership check as EquipItem. Returns the equip-position bitmask
	// that was cleared (i.e. the one before setting to 0).
	UnequipItem(ctx context.Context, accountID, charID, itemID uint32) (priorEquipPos uint32, err error)

	// UseItem decrements the stack count by one; the row is deleted
	// when the resulting amount is zero. Returns the post-decrement
	// amount (0 when removed).
	UseItem(ctx context.Context, accountID, charID, itemID uint32) (remaining uint32, err error)

	// CheckWeight validates that acquiring addAmount units of addNameID
	// would not push charID's carried weight past MaxWeight (pre-re
	// base + str*300). Returns inventorydomain.ErrWeightExceeded when
	// the addition exceeds capacity. The STR stat is loaded from the
	// character row via characters.GetByID — pass the owning accountID
	// so a cross-account charID cannot drive a weight probe. Equipping
	// an already-owned item does NOT call this — weight is enforced on
	// acquisition only.
	CheckWeight(ctx context.Context, accountID, charID, addNameID, addAmount uint32) error
}
