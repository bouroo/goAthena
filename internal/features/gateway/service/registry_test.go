//go:build unit

package service

import (
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bouroo/goAthena/internal/features/gateway/domain"
)

// fakeResponder implements domain.Responder by recording every byte
// slice handed to SendPacket into an in-memory slice. It is safe to
// use concurrently — a mutex guards the slice — so the race test
// can hammer it without serialising SendPacket calls.
type fakeResponder struct {
	mu      sync.Mutex
	packets [][]byte
}

func (f *fakeResponder) SendPacket(p []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Copy so the caller's buffer (often the registry's snapshot
	// staging area) can be reused without disturbing recorded bytes.
	cp := make([]byte, len(p))
	copy(cp, p)
	f.packets = append(f.packets, cp)
	return nil
}

// newSampleSession returns a Session populated with non-zero
// character fields. Each call produces a fresh fake responder so
// individual tests do not share the recording slice.
func newSampleSession(accountID, charID uint32, mapName string) domain.Session {
	return domain.Session{
		Responder: &fakeResponder{},
		CharID:    charID,
		MapName:   mapName,
		View: domain.ViewData{
			ObjectType:  0, // PC
			AID:         accountID,
			GID:         charID,
			Speed:       150,
			Job:         0,
			Head:        1,
			Weapon:      100,
			Shield:      0,
			Accessory:   0,
			Accessory2:  0,
			Accessory3:  0,
			HeadPalette: 0,
			BodyPalette: 0,
			Robe:        0,
			Sex:         1,
			CLevel:      1,
			MaxHP:       100,
			HP:          100,
			Name:        "Test",
		},
	}
}

func TestSessionRegistry_RegisterAndGet(t *testing.T) {
	t.Parallel()
	r := NewSessionRegistry()

	want := newSampleSession(42, 7, "prt_fild08")
	r.Register(42, want)

	got, ok := r.Get(42)
	require.True(t, ok, "Get should find a registered session")
	assert.Equal(t, uint32(7), got.CharID, "snapshot CharID should match")
	assert.Equal(t, "prt_fild08", got.MapName, "snapshot MapName should match")
	assert.Equal(t, want.View, got.View, "snapshot View should match by value")
	assert.NotNil(t, got.Responder, "snapshot Responder should be preserved")
	assert.Equal(t, 1, r.Len(), "Len should reflect the one registered session")
}

func TestSessionRegistry_RegisterOverwrites(t *testing.T) {
	t.Parallel()
	r := NewSessionRegistry()

	first := newSampleSession(42, 7, "prt_fild08")
	second := newSampleSession(42, 99, "prontera")

	r.Register(42, first)
	r.Register(42, second)

	got, ok := r.Get(42)
	require.True(t, ok)
	assert.Equal(t, uint32(99), got.CharID, "second Register should overwrite CharID")
	assert.Equal(t, "prontera", got.MapName, "second Register should overwrite MapName")
	assert.Equal(t, 1, r.Len(), "overwrite should not grow Len")
}

func TestSessionRegistry_Unregister(t *testing.T) {
	t.Parallel()
	t.Run("present", func(t *testing.T) {
		t.Parallel()
		r := NewSessionRegistry()
		r.Register(42, newSampleSession(42, 7, "prt_fild08"))
		require.Equal(t, 1, r.Len())

		r.Unregister(42)

		_, ok := r.Get(42)
		assert.False(t, ok, "Get after Unregister should return ok=false")
		assert.Equal(t, 0, r.Len())
	})
	t.Run("absent", func(t *testing.T) {
		t.Parallel()
		r := NewSessionRegistry()
		// Should not panic, should not grow the map.
		r.Unregister(424242)
		assert.Equal(t, 0, r.Len())
	})
}

func TestSessionRegistry_SetMap(t *testing.T) {
	t.Parallel()
	t.Run("registered", func(t *testing.T) {
		t.Parallel()
		r := NewSessionRegistry()
		r.Register(42, newSampleSession(42, 7, ""))

		ok := r.SetMap(42, "prt_fild08")
		assert.True(t, ok, "SetMap on a registered key should return true")

		got, ok := r.Get(42)
		require.True(t, ok)
		assert.Equal(t, "prt_fild08", got.MapName)
	})
	t.Run("absent", func(t *testing.T) {
		t.Parallel()
		r := NewSessionRegistry()
		ok := r.SetMap(424242, "prt_fild08")
		assert.False(t, ok, "SetMap on an absent key should return false")
	})
}

func TestSessionRegistry_ForEachOnMap(t *testing.T) {
	t.Parallel()
	r := NewSessionRegistry()

	// Three sessions across two maps; one session deliberately has
	// an empty MapName to verify the skip-empty contract.
	r.Register(10, newSampleSession(10, 100, "prt_fild08"))
	r.Register(11, newSampleSession(11, 101, "prt_fild08"))
	r.Register(20, newSampleSession(20, 200, "prontera"))
	r.Register(30, newSampleSession(30, 300, ""))

	t.Run("matching map", func(t *testing.T) {
		var seen []uint32
		r.ForEachOnMap("prt_fild08", func(accountID uint32, _ domain.Session) {
			seen = append(seen, accountID)
		})
		slices.Sort(seen)
		assert.Equal(t, []uint32{10, 11}, seen, "ForEachOnMap should visit every matching session once")
	})

	t.Run("empty MapName sessions skipped", func(t *testing.T) {
		// Even when the requested map is "" the empty-MapName session
		// must be skipped — a half-registered session cannot leak
		// into any broadcast.
		var seen []uint32
		r.ForEachOnMap("", func(accountID uint32, _ domain.Session) {
			seen = append(seen, accountID)
		})
		assert.Empty(t, seen, "empty-MapName sessions must not fire any callback")
	})

	t.Run("unknown map", func(t *testing.T) {
		var calls int32
		r.ForEachOnMap("nowhere", func(_ uint32, _ domain.Session) {
			atomic.AddInt32(&calls, 1)
		})
		assert.Equal(t, int32(0), atomic.LoadInt32(&calls), "unknown map should invoke fn zero times")
	})
}

// TestSessionRegistry_Concurrency exercises the registry under
// concurrent Register / Unregister / Get / ForEachOnMap load. The
// primary acceptance criterion is that the test does not panic or
// trip -race; the secondary criterion is that Len after the storm is
// in [0, maxWorkers*2], which is non-deterministic but bounded.
func TestSessionRegistry_Concurrency(t *testing.T) {
	t.Parallel()
	r := NewSessionRegistry()

	const (
		workers    = 100
		opsPerWork = 100
		maxKeys    = 50
	)
	var wg sync.WaitGroup
	wg.Add(workers * 3)

	// Registers + Unregisters cycling over maxKeys distinct accounts,
	// so the registry never grows past the worker upper bound.
	for i := 0; i < workers; i++ {
		go func(worker int) {
			defer wg.Done()
			for j := 0; j < opsPerWork; j++ {
				accountID := uint32((worker*opsPerWork+j)%maxKeys + 1) //nolint:gosec // bounded by maxKeys
				switch j % 3 {
				case 0:
					r.Register(accountID, newSampleSession(accountID, accountID, "prt_fild08"))
				case 1:
					r.Unregister(accountID)
				case 2:
					_, _ = r.Get(accountID)
				}
			}
		}(i)
	}

	// Parallel readers hammering Get + ForEachOnMap. They share the
	// registry with the writers above; -race must remain silent.
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < opsPerWork; j++ {
				_, _ = r.Get(uint32(j%maxKeys + 1)) //nolint:gosec // bounded
				r.ForEachOnMap("prt_fild08", func(_ uint32, _ domain.Session) {})
			}
		}()
	}

	// A handful of goroutines flipping SetMap back and forth — the
	// only mutation that leaves the registry size unchanged but still
	// requires the write lock.
	for i := 0; i < workers; i++ {
		go func(worker int) {
			defer wg.Done()
			for j := 0; j < opsPerWork; j++ {
				accountID := uint32((worker*7 + j) % maxKeys) //nolint:gosec // bounded
				r.SetMap(accountID, "prt_fild08")
			}
		}(i)
	}

	wg.Wait()

	final := r.Len()
	require.GreaterOrEqual(t, final, 0, "Len must be non-negative")
	require.LessOrEqual(t, final, maxKeys, "Len must not exceed the cycling key set")
}

// TestSessionRegistry_InterfaceContract is satisfied at compile time:
// NewSessionRegistry is declared to return SessionRegistry, so this
// assignment is implicit. The body is empty on purpose; if the
// signature drifts the build breaks instead of failing a test.
func TestSessionRegistry_InterfaceContract(t *testing.T) {
	t.Parallel()
	_ = NewSessionRegistry()
}
