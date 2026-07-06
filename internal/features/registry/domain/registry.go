// Package domain declares the inbound and outbound ports for the
// cross-zone character registry (D22). The registry stores where each
// online character currently lives and arbitrates the dual-zone write
// lock that prevents a character from being simultaneously present on
// two zones during a transit handshake.
package domain

import (
	"context"
	"errors"
	"time"
)

// Sentinel errors returned by Registry implementations. Callers compare
// with errors.Is so wrapping is preserved across the transport boundary.
var (
	// ErrCharacterNotFound is returned when a lookup by charID yields no
	// entry in the registry. Distinct from a transport error so handlers
	// can render the rAthena-conventional fallback flow.
	ErrCharacterNotFound = errors.New("character not found in registry")

	// ErrLockHeld is returned by AcquireZoneLock when another zone
	// already owns the per-character write lock.
	ErrLockHeld = errors.New("character zone lock already held")
)

// CharacterLocation tracks where a character currently is. It is the
// canonical "where is charID playing right now" record and is consulted
// by every cross-zone decision (friend list, party invite, mail,
// whisper, GM locate).
type CharacterLocation struct {
	// CharID is the numeric primary key from the rAthena `char` table.
	CharID uint32
	// AccountID is the owning account's numeric primary key.
	AccountID uint32
	// ZoneID identifies the zone server currently hosting the
	// character. The format is a UUID truncated to 8 chars (see
	// Scout findings §1.3); zone IDs are opaque tokens to the registry.
	ZoneID string
	// MapName is the map the character is currently on (e.g. "prt_fild08").
	// Max MAP_NAME_LENGTH_EXT (16) bytes per the rAthena wire protocol.
	MapName string
	// LastSeen is the timestamp the registry entry was last refreshed.
	LastSeen time.Time
}

// Registry is the outbound port for the Valkey-backed character/account
// registry. It is implemented by the service-layer struct under
// internal/features/registry/service.
type Registry interface {
	// RegisterCharacter marks a character as present on this zone. The
	// hash entry TTL is implicit (no expiry) so a crashed zone leaves
	// a stale location until UnregisterCharacter or a re-register
	// overwrites it; the zone lock is the safety net that prevents
	// dual-zone presence.
	RegisterCharacter(ctx context.Context, loc CharacterLocation) error

	// UnregisterCharacter removes a character from the registry.
	// Missing entries are not an error (best-effort cleanup).
	UnregisterCharacter(ctx context.Context, charID uint32) error

	// GetCharacterLocation looks up which zone a character is on.
	// Returns ErrCharacterNotFound when the charID has no entry.
	GetCharacterLocation(ctx context.Context, charID uint32) (*CharacterLocation, error)

	// ListCharactersOnZone returns all characters currently registered
	// on the given zone. An empty slice is returned for a zone with no
	// members (not an error).
	ListCharactersOnZone(ctx context.Context, zoneID string) ([]CharacterLocation, error)

	// AcquireZoneLock acquires the write lock for a character. Returns
	// ErrLockHeld if another zone already owns the lock. The lock
	// auto-expires after ttl to recover from a holder crash; callers
	// must Release on the happy path to free the slot earlier.
	AcquireZoneLock(ctx context.Context, charID uint32, zoneID string, ttl time.Duration) error

	// ReleaseZoneLock releases the write lock if and only if zoneID
	// still owns it. A non-holder Release is a no-op (matches the
	// warehouse-lock semantics so a stale holder cannot free a fresh
	// acquisition).
	ReleaseZoneLock(ctx context.Context, charID uint32, zoneID string) error
}

// Store is the narrow outbound port the registry uses to talk to
// Valkey. It is intentionally minimal so tests can mock it without a
// running Valkey server. The valkey-backed implementation lives under
// internal/features/registry/service.
type Store interface {
	// HSet writes the given field/value pairs into the hash at key,
	// overwriting any existing values for the same fields. Missing key
	// is created.
	HSet(ctx context.Context, key string, fields map[string]string) error

	// HGetAll returns all field/value pairs of the hash at key. A
	// missing key is reported as (empty-map, nil) — callers can
	// distinguish "not present" from transport errors via the error.
	HGetAll(ctx context.Context, key string) (map[string]string, error)

	// Del removes the given keys. Missing keys are not an error.
	Del(ctx context.Context, keys ...string) error

	// SAdd adds the given members to the set at key.
	SAdd(ctx context.Context, key string, members ...string) error

	// SRem removes the given members from the set at key. Missing
	// members are not an error.
	SRem(ctx context.Context, key string, members ...string) error

	// SMembers returns all members of the set at key. A missing key is
	// reported as (empty-slice, nil).
	SMembers(ctx context.Context, key string) ([]string, error)

	// SetNX sets key=value only if key does not exist. Returns true on
	// successful set, false if the key already existed. The ttl bounds
	// how long the value lives; ttl <= 0 means no expiry.
	SetNX(ctx context.Context, key, value string, ttl time.Duration) (bool, error)

	// DelIfEqual atomically deletes key if its current value equals
	// expected. Used to implement compare-and-delete on lock release.
	// Returns nil regardless of whether a delete actually happened;
	// stale-holder Release is a no-op, not an error.
	DelIfEqual(ctx context.Context, key, expected string) error
}
