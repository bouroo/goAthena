// Package infra adapts the economy bounded context to its persistence layer:
// in-memory ledger for tests and a GORM-backed ledger for production. The
// ledger is append-only, so the in-memory store mirrors that: a sync.Mutex
// guards the slice, IDs are monotonic, and no path mutates a row after Append
// returns.
package infra

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/bouroo/goAthena/internal/modules/economy/domain"
)

// MemoryLedger is an in-memory LedgerRepository. Use in tests; it is not safe
// for cross-process use.
type MemoryLedger struct {
	mu      sync.Mutex
	nextID  int64
	entries []domain.ZenyTransaction
	// now, when non-nil, overrides time.Now so tests can stamp timestamps.
	now func() time.Time
}

// NewMemoryLedger builds an empty in-memory ledger.
func NewMemoryLedger() *MemoryLedger {
	return &MemoryLedger{nextID: 1, now: time.Now}
}

// Append writes the transaction and assigns a fresh ID + CreatedAt.
func (l *MemoryLedger) Append(_ context.Context, tx domain.ZenyTransaction) (domain.ZenyTransaction, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !tx.Valid() {
		// Valid() already rejects empty CreatedAt, but callers that set it
		// explicitly are honoured; otherwise stamp now.
		if tx.CreatedAt.IsZero() {
			tx.CreatedAt = l.now()
		}
		// Re-check after stamping so the validity contract is uniform.
		if !tx.Valid() {
			return domain.ZenyTransaction{}, errInvalid
		}
	}
	tx.ID = l.nextID
	l.nextID++
	l.entries = append(l.entries, tx)
	return tx, nil
}

// ListByChar returns up to limit transactions for charID, newest first.
func (l *MemoryLedger) ListByChar(_ context.Context, charID uint32, limit int) ([]domain.ZenyTransaction, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]domain.ZenyTransaction, 0)
	for i := len(l.entries) - 1; i >= 0 && len(out) < limit; i-- {
		if l.entries[i].CharID == charID {
			out = append(out, l.entries[i])
		}
	}
	return out, nil
}

// SumByChar returns the signed sum of every transaction for charID.
func (l *MemoryLedger) SumByChar(_ context.Context, charID uint32) (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var sum int64
	for _, e := range l.entries {
		if e.CharID == charID {
			sum += int64(e.Amount)
		}
	}
	return sum, nil
}

// All returns every transaction (test helper, not part of the port).
func (l *MemoryLedger) All() []domain.ZenyTransaction {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]domain.ZenyTransaction, len(l.entries))
	copy(out, l.entries)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// errInvalid is returned by Append when the supplied transaction fails Valid()
// even after timestamp stamping.
var errInvalid = &ledgerError{"invalid transaction"}

// ledgerError is a tiny sentinel type so tests can match errors.Is without
// exposing a public symbol that callers might latch onto.
type ledgerError struct{ msg string }

func (e *ledgerError) Error() string { return e.msg }
