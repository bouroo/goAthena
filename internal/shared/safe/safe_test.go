//go:build unit

package safe_test

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bouroo/goAthena/internal/shared/safe"
)

// recorder is a slog.Handler that captures records and signals the first one,
// so a test can wait for the log line deterministically instead of sleeping
// while another goroutine writes it.
type recorder struct {
	mu      sync.Mutex
	records []slog.Record
	once    sync.Once
	done    chan struct{}
}

func newRecorder() *recorder { return &recorder{done: make(chan struct{})} }

func (r *recorder) Enabled(context.Context, slog.Level) bool { return true }

func (r *recorder) Handle(_ context.Context, rec slog.Record) error {
	r.mu.Lock()
	r.records = append(r.records, rec.Clone())
	r.mu.Unlock()
	r.once.Do(func() { close(r.done) })
	return nil
}

func (r *recorder) WithAttrs([]slog.Attr) slog.Handler { return r }
func (r *recorder) WithGroup(string) slog.Handler      { return r }

// attrs flattens the first captured record's attributes.
func (r *recorder) attrs(t *testing.T) map[string]string {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.records) == 0 {
		t.Fatal("no log record captured")
	}
	out := map[string]string{}
	r.records[0].Attrs(func(a slog.Attr) bool {
		out[a.Key] = a.Value.String()
		return true
	})
	return out
}

// TestReport_LogsScopeValueAndStack: the panic log line is the only evidence an
// operator gets, so it must name the scope, carry the panic value, and include
// the stack that says where it happened.
func TestReport_LogsScopeValueAndStack(t *testing.T) {
	rec := newRecorder()
	safe.Report(slog.New(rec), "map.dispatch", "bad frame")

	a := rec.attrs(t)
	if a["scope"] != "map.dispatch" {
		t.Errorf("scope = %q, want %q", a["scope"], "map.dispatch")
	}
	if !strings.Contains(a["panic"], "bad frame") {
		t.Errorf("panic = %q, want it to carry the panic value", a["panic"])
	}
	if !strings.Contains(a["stack"], "goroutine") {
		t.Errorf("stack = %q, want a goroutine stack", a["stack"])
	}
}

// TestGuard_RecoversPanicAndLogs is the core contract: a panic inside a
// directly-deferred Guard does not propagate.
func TestGuard_RecoversPanicAndLogs(t *testing.T) {
	rec := newRecorder()
	func() {
		defer safe.Guard(slog.New(rec), "world.tick")
		panic("tick blew up")
	}() // reaching the line below is the assertion that nothing propagated

	if got := rec.attrs(t)["panic"]; !strings.Contains(got, "tick blew up") {
		t.Errorf("panic = %q, want the panic value", got)
	}
}

// TestGuard_NoPanicIsNil pins the contract return-by-value callers branch on.
func TestGuard_NoPanicIsNil(t *testing.T) {
	if got := safe.Guard(slog.New(newRecorder()), "noop"); got != nil {
		t.Fatalf("Guard returned %v outside a panic, want nil", got)
	}
}

// TestGuard_CatchesPanicNil covers the edge a hand-rolled nil check would miss
// on older Go: panic(nil) arrives as *runtime.PanicNilError, so Guard still
// stops it and still reports.
//
// Guard is deferred directly here, as the contract requires — which is also why
// the test asserts through the log line rather than through Guard's return
// value: only the directly-deferred call can observe the panic at all, and a
// wrapper would silently let this panic keep unwinding.
func TestGuard_CatchesPanicNil(t *testing.T) {
	rec := newRecorder()
	func() {
		defer safe.Guard(slog.New(rec), "edge")
		panic(nil)
	}() // reaching the line below proves the panic was stopped

	if got := rec.attrs(t)["panic"]; !strings.Contains(got, "panic called with nil argument") {
		t.Errorf("panic = %q, want the runtime's nil-panic message", got)
	}
}

// TestGo_RunsFn is the happy path: the spawn still does its work.
func TestGo_RunsFn(t *testing.T) {
	rec := newRecorder()
	ran := make(chan struct{})
	safe.Go(slog.New(rec), "work", func() { close(ran) })

	select {
	case <-ran:
	case <-time.After(2 * time.Second):
		t.Fatal("fn never ran")
	}
}

// TestGo_ContainsPanicInSpawnedGoroutine is the availability proof: a panic in a
// spawned goroutine is reported and the spawn's own defers still run, while this
// test — standing in for the rest of the server — keeps running.
func TestGo_ContainsPanicInSpawnedGoroutine(t *testing.T) {
	rec := newRecorder()
	cleanedUp := make(chan struct{})

	safe.Go(slog.New(rec), "world.tick", func() {
		defer close(cleanedUp) // the goroutine's own defers run during the unwind
		panic("tick blew up")
	})

	select {
	case <-rec.done:
	case <-time.After(2 * time.Second):
		t.Fatal("a panic in a spawned goroutine was not recovered and reported within 2s")
	}
	if got := rec.attrs(t)["panic"]; !strings.Contains(got, "tick blew up") {
		t.Errorf("panic = %q, want the panic value", got)
	}

	select {
	case <-cleanedUp:
	case <-time.After(2 * time.Second):
		t.Error("the panicking goroutine's own defers did not run")
	}
}
