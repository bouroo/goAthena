package domain

import (
	"context"
	"errors"
	"time"
)

// Sentinel errors returned by repository implementations. Service-layer
// code should compare against these with errors.Is rather than string
// matching so wrapping is preserved.
var (
	// ErrAccountNotFound is returned when a lookup by userid or numeric
	// account_id yields no row.
	ErrAccountNotFound = errors.New("account not found")
	// ErrCharacterNotFound is returned when a character lookup by id
	// yields no row.
	ErrCharacterNotFound = errors.New("character not found")
	// ErrLockHeld is returned when a warehouse lock is already held by
	// another caller.
	ErrLockHeld = errors.New("warehouse lock already held")
)

// AccountRepository is the outbound port for account persistence. Concrete
// implementations live under internal/features/identity/repository and
// back onto MariaDB (GORM) or PostgreSQL via the configured driver.
type AccountRepository interface {
	// LoadByUserID fetches an account by its login userid. Returns
	// ErrAccountNotFound if no row matches.
	LoadByUserID(ctx context.Context, userID string) (*Account, error)
	// LoadByID fetches an account by its numeric account_id. Returns
	// ErrAccountNotFound if no row matches.
	LoadByID(ctx context.Context, accountID uint32) (*Account, error)
	// UpdateLoginInfo increments logincount, updates last_ip and
	// lastlogin atomically (login.cpp:421-424).
	UpdateLoginInfo(ctx context.Context, accountID uint32, ip string) error
}

// CharacterRepository is the outbound port for character persistence.
// Phase 2 ships the lobby roster; per-character inventory and equipment
// are loaded by the zone service on map enter.
type CharacterRepository interface {
	// ListByAccount returns characters for an account, ordered by slot
	// ascending. maxSlots caps the result to the effective slot count
	// (MIN_CHARS or account.character_slots, whichever is greater).
	ListByAccount(ctx context.Context, accountID uint32, maxSlots int) ([]CharacterSummary, error)
	// GetByID fetches a single character by (accountID, charID). Returns
	// ErrCharacterNotFound when no row matches. The (accountID, charID)
	// compound key is the canonical rAthena ownership check
	// (inter.cpp::char_clif_top) — a charID alone is not unique to an
	// account, so callers that omit accountID risk cross-account reads.
	GetByID(ctx context.Context, accountID, charID uint32) (*CharacterSummary, error)

	// ApplyLevelUp atomically sets base_level=toLevel, adds grantedStatusPoints to
	// status_point, and sets base_exp=0, WHERE account_id=? AND char_id=? AND
	// base_level=fromLevel (optimistic lock). Returns the post-update base_level
	// and status_point (re-read), and applied=true. applied=false when rows==0
	// (concurrent level-up — caller re-reads and skips).
	ApplyLevelUp(ctx context.Context, accountID, charID, fromLevel, toLevel, grantedStatusPoints uint32) (newLevel, newStatusPoint uint32, applied bool, err error)

	// AllocateStat atomically raises the named stat column by amount and deducts
	// cost from status_point via a conditional UPDATE whose WHERE clause verifies
	// ownership (account_id+char_id) AND status_point>=cost AND stat+amount<=99.
	// statColumn is one of "str"|"agi"|"vit"|"int"|"dex"|"luk". Returns post-update
	// (statValue, statusPoint) re-read. result: 0=applied, 1=insufficient points,
	// 2=stat would exceed MaxStat (rows==0 → re-read to distinguish).
	AllocateStat(ctx context.Context, accountID, charID uint32, statColumn string, amount uint8, cost uint32) (newValue, newStatusPoint uint32, result int, err error)
}

// SessionRepository is the outbound port for session persistence, backed
// by Valkey in production. It replaces rAthena's in-memory auth_db.
type SessionRepository interface {
	// Put stores a session with the given TTL.
	Put(ctx context.Context, sess *Session, ttl time.Duration) error
	// Get retrieves a session by account_id. Returns nil with no error
	// when the key is absent.
	Get(ctx context.Context, accountID uint32) (*Session, error)
	// Delete removes a session.
	Delete(ctx context.Context, accountID uint32) error
}

// WarehouseLock is the distributed lock for storage (warehouse) access.
// It is a goAthena improvement over rAthena, which had no row-level or
// table-level storage lock and relied on the single-online invariant
// (ipban.cpp / char.cpp comment at int_storage.cpp:38-86).
type WarehouseLock interface {
	// Acquire tries to get a storage lock for the account. Returns a
	// token on success; returns ErrLockHeld if another holder exists.
	Acquire(ctx context.Context, accountID uint32, ttl time.Duration) (token string, err error)
	// Release releases the lock if the token matches. The implementation
	// must use a Lua compare-and-delete to avoid releasing a lock that
	// has already expired and been re-acquired by another caller.
	Release(ctx context.Context, accountID uint32, token string) error
}
