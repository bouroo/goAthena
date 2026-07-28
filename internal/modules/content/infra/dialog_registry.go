package infra

import (
	"fmt"
	"sync"

	"github.com/bouroo/goAthena/internal/modules/content/domain"
)

type memoryDialogRegistry struct {
	mu      sync.RWMutex
	dialogs map[uint32]chan bool
}

// NewMemoryDialogRegistry constructs an in-memory dialog registry keyed by
// account ID. One registry instance is constructed per content module boot
// and provided to the injector as the dialog port.
func NewMemoryDialogRegistry() domain.DialogRegistry {
	return &memoryDialogRegistry{
		dialogs: make(map[uint32]chan bool),
	}
}

func (r *memoryDialogRegistry) Open(accountID uint32) (chan bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.dialogs[accountID]; exists {
		return nil, fmt.Errorf("dialog already active for account %d", accountID)
	}

	// Unbuffered channel so next/choose can block correctly
	ch := make(chan bool)
	r.dialogs[accountID] = ch
	return ch, nil
}

func (r *memoryDialogRegistry) Get(accountID uint32) chan bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.dialogs[accountID]
}

func (r *memoryDialogRegistry) Close(accountID uint32) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Delete-only, never close: every producer (next/choose/close handler) does a
	// non-blocking `select { case ch <- x: default: }`, so closing here would race
	// a concurrent send and panic (send on closed channel). A blocked VM goroutine
	// is instead reclaimed by the 30s Next() timeout, a bounded leak.
	delete(r.dialogs, accountID)
}
