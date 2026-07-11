//go:build unit

package domain

import (
	"sync"
	"testing"
)

func TestSpendSP_Success(t *testing.T) {
	t.Parallel()

	c := &ConnectionInfo{}
	c.SetSP(20, 100)

	remaining, ok := c.SpendSP(8)
	if !ok {
		t.Fatalf("SpendSP(8) ok = false, want true")
	}
	if remaining != 12 {
		t.Errorf("SpendSP(8) remaining = %d, want 12", remaining)
	}
	if got := c.SP(); got != 12 {
		t.Errorf("SP() = %d, want 12", got)
	}
}

func TestSpendSP_Insufficient(t *testing.T) {
	t.Parallel()

	c := &ConnectionInfo{}
	c.SetSP(5, 100)

	remaining, ok := c.SpendSP(8)
	if ok {
		t.Fatalf("SpendSP(8) ok = true, want false")
	}
	if remaining != 5 {
		t.Errorf("SpendSP(8) remaining = %d, want 5", remaining)
	}
	if got := c.SP(); got != 5 {
		t.Errorf("SP() = %d, want 5 (unchanged)", got)
	}
}

func TestSpendSP_ExactBoundary(t *testing.T) {
	t.Parallel()

	c := &ConnectionInfo{}
	c.SetSP(8, 100)

	remaining, ok := c.SpendSP(8)
	if !ok {
		t.Fatalf("SpendSP(8) ok = false, want true at boundary")
	}
	if remaining != 0 {
		t.Errorf("SpendSP(8) remaining = %d, want 0", remaining)
	}
}

func TestSetSP_RoundTrip(t *testing.T) {
	t.Parallel()

	c := &ConnectionInfo{}
	c.SetSP(42, 99)

	if got := c.SP(); got != 42 {
		t.Errorf("SP() = %d, want 42", got)
	}
	if got := c.MaxSP(); got != 99 {
		t.Errorf("MaxSP() = %d, want 99", got)
	}
}

func TestSpendSP_Concurrent(t *testing.T) {
	t.Parallel()

	const startSP uint32 = 10000
	c := &ConnectionInfo{}
	c.SetSP(startSP, startSP)

	const goroutines = 16
	const iters = 1000
	const cost uint32 = 1

	var wg sync.WaitGroup
	var successes int64
	var mu sync.Mutex

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			localOK := int64(0)
			for j := 0; j < iters; j++ {
				if _, ok := c.SpendSP(cost); ok {
					localOK++
				}
			}
			mu.Lock()
			successes += localOK
			mu.Unlock()
		}()
	}
	wg.Wait()

	if successes > int64(startSP) {
		t.Errorf("successes = %d exceeds starting SP %d (possible double-spend)", successes, startSP)
	}
	if got := c.SP(); got != startSP-uint32(successes) {
		t.Errorf("SP() = %d, want %d", got, startSP-uint32(successes))
	}
}
