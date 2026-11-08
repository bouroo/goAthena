//go:build unit

package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/bouroo/goAthena/internal/infrastructure/agones"
)

// recordingReporter captures the counters the loop publishes.
type recordingReporter struct {
	mu       sync.Mutex
	reports  []report
	err      error
	reported chan struct{}
}

type report struct {
	name     string
	count    int64
	capacity int64
}

func (r *recordingReporter) ReportCounter(_ context.Context, name string, count, capacity int64) error {
	r.mu.Lock()
	r.reports = append(r.reports, report{name: name, count: count, capacity: capacity})
	r.mu.Unlock()
	select {
	case r.reported <- struct{}{}:
	default:
	}
	return r.err
}

func (r *recordingReporter) last() (report, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.reports) == 0 {
		return report{}, false
	}
	return r.reports[len(r.reports)-1], true
}

// stubSource reports a fixed player count.
type stubSource struct{ count int }

func (s stubSource) OnlinePlayerCount() int { return s.count }

// TestRunCounterLoopPublishesThePlayerCount proves the loop reports the
// connected player count under the counter name the Fleet declares.
//
// The name and the capacity are the contract with the autoscaler: a
// report under any other name is a report the fleet never reads.
func TestRunCounterLoopPublishesThePlayerCount(t *testing.T) {
	reporter := &recordingReporter{reported: make(chan struct{}, 8)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go RunCounterLoop(ctx, discardLogger(), reporter, stubSource{count: 17}, 5*time.Millisecond)

	select {
	case <-reporter.reported:
	case <-time.After(2 * time.Second):
		t.Fatal("the loop published no counter within 2s")
	}
	got, ok := reporter.last()
	if !ok {
		t.Fatal("no report was recorded")
	}
	if got.name != agones.PlayersCounterName {
		t.Errorf("counter name = %q, want %q", got.name, agones.PlayersCounterName)
	}
	if got.count != 17 {
		t.Errorf("count = %d, want 17 (the connected players)", got.count)
	}
	if got.capacity != agones.PlayerCounterCapacity {
		t.Errorf("capacity = %d, want %d", got.capacity, agones.PlayerCounterCapacity)
	}
}

// TestRunCounterLoopCapsTheCountAtTheCapacity proves a count above the
// shard's capacity is reported as the capacity.
//
// The sidecar rejects a count above the capacity, so reporting the
// overflow would fail every time and leave the autoscaler scaling on a
// stale number.
func TestRunCounterLoopCapsTheCountAtTheCapacity(t *testing.T) {
	reporter := &recordingReporter{reported: make(chan struct{}, 8)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go RunCounterLoop(ctx, discardLogger(), reporter,
		stubSource{count: agones.PlayerCounterCapacity + 50}, 5*time.Millisecond)

	select {
	case <-reporter.reported:
	case <-time.After(2 * time.Second):
		t.Fatal("the loop published no counter within 2s")
	}
	got, _ := reporter.last()
	if got.count != agones.PlayerCounterCapacity {
		t.Errorf("count = %d, want %d (the capacity: the shard is full)",
			got.count, agones.PlayerCounterCapacity)
	}
}

// TestRunCounterLoopStopsWithTheContext proves the loop does not outlive
// the process: a reporting goroutine that keeps running after the drain
// would talk to a sidecar Agones is already tearing down.
func TestRunCounterLoopStopsWithTheContext(t *testing.T) {
	reporter := &recordingReporter{reported: make(chan struct{}, 8)}
	ctx, cancel := context.WithCancel(context.Background())

	go RunCounterLoop(ctx, discardLogger(), reporter, stubSource{count: 1}, 5*time.Millisecond)
	<-reporter.reported
	cancel()

	// The count must freeze after the cancellation.
	time.Sleep(50 * time.Millisecond)
	reporter.mu.Lock()
	frozen := len(reporter.reports)
	reporter.mu.Unlock()
	time.Sleep(60 * time.Millisecond)
	reporter.mu.Lock()
	after := len(reporter.reports)
	reporter.mu.Unlock()
	if after != frozen {
		t.Errorf("reports after cancel = %d, want frozen at %d", after, frozen)
	}
}

// TestRunCounterLoopIsInertWithoutAReporter proves a process outside a
// GameServer starts no reporting: there is no sidecar to report to.
func TestRunCounterLoopIsInertWithoutAReporter(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	// A nil reporter must not panic and must return promptly.
	RunCounterLoop(ctx, discardLogger(), nil, stubSource{count: 1}, 5*time.Millisecond)
}

// TestCounterReporterForIsNilWithoutASidecar proves a process that does
// not run as a GameServer has no counter reporter — the loop is not
// started at all.
func TestCounterReporterForIsNilWithoutASidecar(t *testing.T) {
	if got := counterReporterFor(nil); got != nil {
		t.Errorf("counterReporterFor(nil) = %v, want nil", got)
	}
}
