package service

import (
	"sync"

	"github.com/bouroo/goAthena/internal/features/gateway/domain"
)

// SessionRegistry is the gateway's map-scoped session index. It is
// keyed by account ID (the only globally unique key the gateway carries
// on the wire; char ID alone is not unique because a single account
// can have multiple characters, only one of which is "in the map").
//
// Implementations MUST be safe for concurrent use by the gnet / WS
// dispatch goroutines (Register / Unregister / SetMap / Get) and the
// future NATS broadcast subscriber goroutine (ForEachOnMap) at once.
//
// ForEachOnMap holds the read lock for the entire iteration; the
// callback MUST NOT block on slow I/O. It will be invoked from the
// broadcast hot path, where every millisecond of head-of-line blocking
// delays every other session on the same map.
//
// design note: The interface lives in this package on purpose. The
// registry has exactly one production implementation and is tightly
// coupled to the broadcast fan-out semantics — keeping the port next
// to the implementation avoids importing `service` from `domain`
// (which would invert the clean-architecture dependency direction) and
// keeps future wiring code free to swap the implementation in tests
// without reaching into the gateway's domain types.
type SessionRegistry interface {
	// Register installs s as the session for accountID, overwriting any
	// previous session for that key. The intent is that on a
	// re-connection the fresh session replaces the stale one
	// unconditionally; callers that need to detect the overwrite can
	// pre-check with Get.
	Register(accountID uint32, s domain.Session)

	// Unregister removes the session for accountID. A no-op if the
	// key is absent — Unregister is always safe to call from cleanup
	// paths that may run more than once.
	Unregister(accountID uint32)

	// SetMap updates the map name on the session for accountID. It
	// returns false if no session is registered for that account; the
	// caller is expected to treat a false return as a "session
	// vanished" condition.
	SetMap(accountID uint32, mapName string) bool

	// Get returns a snapshot copy of the session for accountID. The
	// second return is false when no session is registered. The copy
	// means callers can read Responder / View without racing against
	// concurrent Register / Unregister / SetMap calls.
	Get(accountID uint32) (domain.Session, bool)

	// ForEachOnMap invokes fn for every session whose MapName equals
	// mapName, passing the registered account ID and a snapshot copy
	// of the session. Sessions with an empty MapName are skipped
	// unconditionally — so a half-registered session cannot leak into
	// a map-scoped broadcast even when the caller asks for the ""
	// map.
	//
	// fn MUST NOT block on slow I/O (resolvers, remote RPCs,
	// channel sends without buffering). It is invoked with the
	// registry's read lock held; long-blocking fn implementations
	// will stall concurrent Register / Unregister calls and starve
	// every other reader. If the broadcast payload needs a remote
	// call, copy what you need out of the snapshot inside fn and
	// dispatch the slow work after ForEachOnMap returns.
	ForEachOnMap(mapName string, fn func(accountID uint32, s domain.Session))

	// Len returns the number of sessions currently registered. It is
	// provided for tests and operational diagnostics (debug endpoints,
	// /metrics gauges); do not rely on it being consistent with any
	// other returned value — the registry is concurrent.
	Len() int
}

// sessionRegistry is the production SessionRegistry. It is keyed by
// account ID and stores a value copy of the session so callers can
// read the snapshot without holding any registry lock. The mutex
// guards the map structure only; the snapshot copy hands out copies
// of the per-account state under the read lock.
type sessionRegistry struct {
	mu    sync.RWMutex
	rooms map[uint32]domain.Session
}

// NewSessionRegistry returns a fresh, empty SessionRegistry backed
// by an in-memory map.
//
// The interface (rather than the concrete type) is returned because
// Go's naming rule cannot satisfy both "an exported interface named
// SessionRegistry" and "an unexported concrete implementation" in the
// same return type without tripping revive's unexported-return check.
// Callers that need DI providers can still write
// `do.Provide(c, func(i do.Injector) (SessionRegistry, error) { ... })`
// — the interface is the natural assign target.
func NewSessionRegistry() SessionRegistry {
	return &sessionRegistry{
		rooms: make(map[uint32]domain.Session),
	}
}

// Register installs s as the session for accountID, overwriting any
// previous value. Overwriting is silent — the registry does not log
// because the only legitimate source of overwrites today is a
// reconnect after gnet dropped the previous conn and the cleanup
// path did not see the drop in time.
func (r *sessionRegistry) Register(accountID uint32, s domain.Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rooms[accountID] = s
}

// Unregister removes the session for accountID. No-op if the key is
// absent — safe to call from double-cleanup paths.
func (r *sessionRegistry) Unregister(accountID uint32) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.rooms, accountID)
}

// SetMap updates the MapName field on the session for accountID.
// Returns false when no session is registered for accountID — callers
// treat a false return as "the session vanished mid-call".
func (r *sessionRegistry) SetMap(accountID uint32, mapName string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.rooms[accountID]
	if !ok {
		return false
	}
	s.MapName = mapName
	r.rooms[accountID] = s
	return true
}

// Get returns a snapshot copy of the session for accountID; the
// second return is false when the key is absent. The snapshot is a
// value copy of the Session struct, including its Responder interface
// value — callers should treat the returned Session as read-only.
func (r *sessionRegistry) Get(accountID uint32) (domain.Session, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.rooms[accountID]
	return s, ok
}

// ForEachOnMap invokes fn for every session whose MapName equals
// mapName, holding the read lock for the full iteration. See the
// SessionRegistry.ForEachOnMap contract for the non-blocking
// discipline fn must follow.
func (r *sessionRegistry) ForEachOnMap(mapName string, fn func(accountID uint32, s domain.Session)) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for accountID, s := range r.rooms {
		// Empty-MapName sessions are skipped unconditionally — a
		// half-registered session (post-CZ_ENTER, pre-actor-init) must
		// never leak into a map-scoped broadcast even if the caller
		// asks for the "" map.
		if s.MapName == "" {
			continue
		}
		if s.MapName != mapName {
			continue
		}
		fn(accountID, s)
	}
}

// Len returns the count of registered sessions. It is a diagnostic
// helper — the value can be stale by the time the caller inspects it.
func (r *sessionRegistry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.rooms)
}
