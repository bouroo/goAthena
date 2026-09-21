package domain

import "context"

// AccountRepository is the persistence port for the account aggregate. The app
// service depends on this interface, not the GORM repo, so unit tests use an
// in-memory implementation and a future extraction can swap the backend.
type AccountRepository interface {
	// FindByUserID loads the account for the given login name, or an error
	// (ErrAccountNotFound when absent).
	FindByUserID(ctx context.Context, userID string) (Account, error)
	// FindByAccountID loads the account by its account_id. Returns
	// ErrAccountNotFound when absent.
	FindByAccountID(ctx context.Context, id AccountID) (Account, error)
	// RecordLogin stamps logincount/lastlogin/last_ip for telemetry.
	RecordLogin(ctx context.Context, id AccountID, ip string) error
}

// Authenticator is the use-case port gateway/ingress calls to authenticate a
// client login. It returns the account plus two session tokens (loginID1,
// loginID2) rAthena carries through to the char/map handshake, or a sentinel
// error (ErrAccountNotFound/ErrInvalidPassword/ErrAccountBanned).
type Authenticator interface {
	Authenticate(ctx context.Context, userID, password, ip string) (Account, uint32, uint32, error)
}
