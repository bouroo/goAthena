// Package app — per-IP login rate limiter. Login brute-force is the most
// obvious attack vector on a game server: the :6900 endpoint accepts a
// username + password pair and is reachable by anyone on the network.
// Without throttling, a single attacker can run millions of guesses a second.
//
// The limiter is per-IP, not per-account: account-bound limiting is a
// stronger property (it catches distributed attacks) but requires per-account
// state, which the login handler does not have. The IP-level limiter caps
// the practical attack rate to whatever the operator sets in config.
//
// Algorithm: token-bucket per IP. A burst of N attempts is allowed; subsequent
// attempts refill at R per second. The bucket state is kept in a sync.Map of
// pointer-to-bucket; idle entries are evicted on every check (cheap, lock-free
// read; bounded memory under attack because entries only grow when an IP is
// hot).
package app

import (
	"net"
	"sync"
	"time"
)

// LoginRateLimiter is the per-IP token bucket the login listener consults
// before dispatching a CA_LOGIN goroutine. It is constructed once at boot
// (composition.go) and shared across all login connections.
type LoginRateLimiter struct {
	// capacity is the bucket size; the first burst of N attempts within
	// refillInterval is allowed before throttling kicks in.
	capacity float64
	// refillPerSecond is the steady-state allowance.
	refillPerSecond float64
	// now is overridable for tests; production uses time.Now.
	now func() time.Time

	mu      sync.Mutex
	buckets map[string]*loginBucket
}

// loginBucket is one IP's token bucket. lastRefill is when the bucket was
// last topped up; tokens is the current count (float so partial refills
// accumulate).
type loginBucket struct {
	tokens     float64
	lastRefill time.Time
}

// NewLoginRateLimiter builds a LoginRateLimiter that allows burst attempts
// up to capacity and refills at refillPerSecond. A disabled limiter (rate=0)
// returns nil — callers should treat a nil receiver as "always allow".
func NewLoginRateLimiter(capacity, refillPerSecond float64) *LoginRateLimiter {
	if capacity <= 0 || refillPerSecond <= 0 {
		return nil
	}
	return &LoginRateLimiter{
		capacity:        capacity,
		refillPerSecond: refillPerSecond,
		now:             time.Now,
		buckets:         make(map[string]*loginBucket),
	}
}

// Allow reports whether an attempt from ip is permitted. The first call
// for an IP seeds the bucket at full capacity; subsequent calls consume one
// token and accrue a refill since the last touch. Eviction runs on every
// call so an idle bucket reaps within one check.
func (l *LoginRateLimiter) Allow(ip string) bool {
	if l == nil {
		return true
	}
	ip = canonicalIP(ip)
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.buckets[ip]
	if !ok {
		l.buckets[ip] = &loginBucket{tokens: l.capacity - 1, lastRefill: now}
		return true
	}
	elapsed := now.Sub(b.lastRefill).Seconds()
	b.tokens += elapsed * l.refillPerSecond
	if b.tokens > l.capacity {
		b.tokens = l.capacity
	}
	b.lastRefill = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// canonicalIP strips the port from a host:port pair so the same IP under
// different ports shares one bucket. An empty or unparseable input falls
// back to the raw string (still per-source limiting, just less granular).
func canonicalIP(s string) string {
	if s == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(s)
	if err == nil {
		return host
	}
	return s
}
