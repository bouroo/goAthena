//go:build unit

package app

import (
	"testing"
	"time"
)

// fixedClock returns a function that steps forward by step per call. Tests
// use it to drive the bucket's refill calculation deterministically.
func fixedClock(start time.Time, step time.Duration) func() time.Time {
	now := start
	return func() time.Time {
		t := now
		now = now.Add(step)
		return t
	}
}

// TestLoginRateLimiterBurstThenDeny: the first N attempts are allowed (the
// bucket starts full), then the (N+1)th is denied.
func TestLoginRateLimiterBurstThenDeny(t *testing.T) {
	l := NewLoginRateLimiter(3, 1) // 3 burst, 1/sec steady
	if l == nil {
		t.Fatal("limiter should not be nil for positive params")
	}
	for i := 0; i < 3; i++ {
		if !l.Allow("1.2.3.4") {
			t.Errorf("attempt %d: want allow (burst)", i)
		}
	}
	if l.Allow("1.2.3.4") {
		t.Error("attempt 4: want deny (bucket empty)")
	}
}

// TestLoginRateLimiterRefillsOverTime: after the bucket drains, waiting a
// full second earns one token (the refill rate).
func TestLoginRateLimiterRefillsOverTime(t *testing.T) {
	l := NewLoginRateLimiter(1, 1)
	l.now = fixedClock(time.Unix(0, 0), 0)

	if !l.Allow("5.6.7.8") {
		t.Fatal("first attempt: want allow")
	}
	if l.Allow("5.6.7.8") {
		t.Fatal("second immediate attempt: want deny (bucket empty)")
	}
	// Advance the clock one second: the refill should restore exactly one
	// token, allowing the next attempt.
	l.now = func() time.Time { return time.Unix(1, 0) }
	if !l.Allow("5.6.7.8") {
		t.Error("after 1s refill: want allow")
	}
}

// TestLoginRateLimiterIndependentIPs: a denial on one IP does not affect
// another. This is the property that lets the operator throttle a brute
// forcer without locking out legitimate users behind the same NAT.
func TestLoginRateLimiterIndependentIPs(t *testing.T) {
	l := NewLoginRateLimiter(1, 0.001)
	if !l.Allow("10.0.0.1") {
		t.Fatal("10.0.0.1 first: want allow")
	}
	if l.Allow("10.0.0.1") {
		t.Fatal("10.0.0.1 second immediate: want deny")
	}
	// Different IP: independent bucket, still allows.
	if !l.Allow("10.0.0.2") {
		t.Error("10.0.0.2 first: want allow (separate bucket)")
	}
}

// TestLoginRateLimiterNilAllowsAll: the disabled-rate guard — a nil receiver
// is "always allow". Composition.go returns nil when the operator disables
// rate limiting in config.
func TestLoginRateLimiterNilAllowsAll(t *testing.T) {
	var l *LoginRateLimiter // nil
	for i := 0; i < 1000; i++ {
		if !l.Allow("1.1.1.1") {
			t.Fatalf("nil limiter denied attempt %d", i)
		}
	}
}

// TestLoginRateLimiterZeroRateDisables: NewLoginRateLimiter(0, X) or
// NewLoginRateLimiter(X, 0) returns nil so a misconfigured operator does
// not lock everyone out.
func TestLoginRateLimiterZeroRateDisables(t *testing.T) {
	if NewLoginRateLimiter(0, 5) != nil {
		t.Error("capacity=0: want nil (disabled)")
	}
	if NewLoginRateLimiter(5, 0) != nil {
		t.Error("rate=0: want nil (disabled)")
	}
}

// TestLoginRateLimiterCanonicalIP: host:port inputs collapse to host so
// retries from different ephemeral ports share one bucket.
func TestLoginRateLimiterCanonicalIP(t *testing.T) {
	l := NewLoginRateLimiter(1, 0.001)
	if !l.Allow("1.2.3.4:5555") {
		t.Fatal("first attempt with port: want allow")
	}
	// Same host, different port — should share the bucket and be denied.
	if l.Allow("1.2.3.4:6666") {
		t.Error("same IP, different port: want deny (bucket shared)")
	}
}
