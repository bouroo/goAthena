package domain

import (
	"context"
	"time"
)

// Session is the per-account auth state minted at CA_LOGIN and verified later at
// char-select and map-enter (CZ_ENTER). It is the in-process replacement for
// rAthena's per-connection auth_db, keyed by AccountID.
type Session struct {
	AccountID uint32
	LoginID1  uint32
	LoginID2  uint32
	Sex       Sex
	CreatedAt time.Time
}

// AccountRepository is the outbound persistence port for accounts. The GORM
// and in-memory adapters implement it.
type AccountRepository interface {
	// LoadByUserID fetches an account by login userid. Returns ErrAccountNotFound
	// when no row matches (the service maps this to AuthUnregistered).
	LoadByUserID(ctx context.Context, userID string) (*Account, error)
	// UpdateLoginInfo increments logincount and records last_ip + lastlogin
	// (login.cpp:421-424). now is passed in so the auth decision and the login
	// record share one timestamp; returns ErrAccountNotFound if the row vanished.
	UpdateLoginInfo(ctx context.Context, accountID uint32, ip string, now time.Time) error
}

// SessionStore is the outbound port for auth sessions.
type SessionStore interface {
	Put(ctx context.Context, sess *Session) error
	// Get returns ErrSessionNotFound when no session exists for the account.
	Get(ctx context.Context, accountID uint32) (*Session, error)
	Delete(ctx context.Context, accountID uint32) error
}
