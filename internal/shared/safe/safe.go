// Package safe isolates failures so that one panicking handler, one connection,
// or one background loop cannot take the whole process down.
//
// goAthena runs the login, char, and map listeners plus the control plane in a
// single process, and gnet recovers nothing: an unrecovered panic in a gnet
// callback or in any spawned goroutine terminates the server for every
// connected player. Every per-frame and background goroutine in the server is
// therefore guarded from here, so "no unguarded goroutine" is a single-point
// policy rather than a rule each call site has to remember.
//
// # Why these are not wrap-able
//
// recover stops a panic only when it is called directly by the deferred
// function — a helper called *by* that function sees recover() return nil and
// the panic keeps unwinding. Report/Guard/Go are therefore each designed to be
// the directly-deferred call (or, for Go, to defer Guard itself inside the new
// goroutine). A call site that needs a panic posture of its own must call
// recover() itself and hand the value to Report; it must not wrap Guard.
package safe

import (
	"log/slog"
	"runtime/debug"
)

// Report logs one recovered panic: the scope that panicked, the panic value,
// and the goroutine stack. It is the single place the panic log line's shape
// lives, shared by the guards below and by callers that need their own failure
// posture:
//
//	if r := recover(); r != nil {
//		safe.Report(log, "map.dispatch", r)
//		_ = c.Close()
//	}
func Report(log *slog.Logger, scope string, recovered any) {
	log.Error("panic recovered",
		"scope", scope,
		"panic", recovered,
		"stack", string(debug.Stack()))
}

// Guard recovers a panic, reports it, and returns the recovered value (nil when
// the goroutine did not panic). Defer it directly:
//
//	defer safe.Guard(log, "world.tick")
//
// It must be the deferred call itself — see the package comment.
//
// recover() reports a panic(nil) as a *runtime.PanicNilError rather than as a
// nil interface, so a nil check still catches every panic; there is no
// "recovered nothing but still unwinding" case to handle.
func Guard(log *slog.Logger, scope string) (recovered any) {
	r := recover()
	if r == nil {
		return nil
	}
	Report(log, scope, r)
	return r
}

// Go runs fn on a new goroutine under Guard, so a panic inside fn is logged
// under scope instead of crashing the process.
//
// The panic stays contained to that one goroutine: fn's own defers still run
// during the unwind (a session unregister, a lock release), and the rest of the
// server keeps serving.
func Go(log *slog.Logger, scope string, fn func()) {
	go func() {
		defer Guard(log, scope)
		fn()
	}()
}
