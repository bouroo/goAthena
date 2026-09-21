//go:build unit

package app

import (
	"io"
	"log/slog"
	"testing"

	"github.com/panjf2000/gnet/v2"
)

// closeRecorder is a gnet.Conn whose only implemented method is Close. The panic
// path reaches nothing else, and embedding the nil interface keeps the fake
// honest about that: if a helper ever calls another Conn method on the way out,
// the test panics instead of quietly passing.
type closeRecorder struct {
	gnet.Conn
	closed bool
}

func (c *closeRecorder) Close() error {
	c.closed = true
	return nil
}

// quietLogger discards the guard's log line; these tests assert the failure
// posture, and the logging itself is covered in internal/shared/safe.
func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// TestCloseOnPanic_ClosesTheOffendingConnection: a panicking per-frame handler
// must not leave the client connected to a path that just broke.
func TestCloseOnPanic_ClosesTheOffendingConnection(t *testing.T) {
	c := &closeRecorder{}
	func() {
		defer closeOnPanic(quietLogger(), "map.dispatch", c)
		panic("bad frame")
	}()

	if !c.closed {
		t.Fatal("connection left open after a panicking handler, want closed")
	}
}

// TestCloseOnPanic_LeavesAHealthyConnectionOpen: the guard is a defer on every
// dispatch, so it must not close a connection whose handler succeeded.
func TestCloseOnPanic_LeavesAHealthyConnectionOpen(t *testing.T) {
	c := &closeRecorder{}
	func() {
		defer closeOnPanic(quietLogger(), "map.dispatch", c)
	}()

	if c.closed {
		t.Fatal("connection closed after a handler that did not panic")
	}
}

// TestCloseOnPanicAction_TearsDownViaGnetClose: on the event loop the failure
// posture is the reactor's own teardown, not a direct close.
func TestCloseOnPanicAction_TearsDownViaGnetClose(t *testing.T) {
	action := gnet.None
	func() {
		defer closeOnPanicAction(quietLogger(), "map.OnTraffic", &action)
		panic("event loop")
	}()

	if action != gnet.Close {
		t.Fatalf("action = %d, want gnet.Close (%d)", action, gnet.Close)
	}
}

// TestCloseOnPanicAction_KeepsTheReturnedAction: a clean callback's action is
// whatever the callback returned, untouched by the guard.
func TestCloseOnPanicAction_KeepsTheReturnedAction(t *testing.T) {
	action := gnet.None
	func() {
		defer closeOnPanicAction(quietLogger(), "map.OnTraffic", &action)
	}()

	if action != gnet.None {
		t.Fatalf("action = %d, want gnet.None (%d)", action, gnet.None)
	}
}
