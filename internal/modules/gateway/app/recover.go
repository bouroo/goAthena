package app

import (
	"log/slog"

	"github.com/panjf2000/gnet/v2"

	"github.com/bouroo/goAthena/internal/shared/safe"
)

// The gateway is where attacker-controlled bytes first reach server code, so it
// is also where an unguarded panic is most likely. gnet recovers nothing, and
// its reactor callbacks run on the event loop itself, so a panic in either a
// per-frame handler or a callback ends the process — every listener and the
// control plane with it.
//
// The two helpers below are the only failure postures the gateway needs: a
// per-frame dispatch goroutine drops its own connection, and a reactor callback
// asks gnet to. Both call recover() directly because only a direct call stops
// the panic (see the safe package comment); the log line comes from
// safe.Report, so the shape stays in one place.

// closeOnPanic is deferred directly by a per-frame dispatch goroutine:
//
//	go func() {
//		defer closeOnPanic(s.log, "map.dispatch", c)
//		h.fn(s, c, auth, cp)
//	}()
//
// The offending connection is closed rather than left open: the client's frame
// reached a bug, and dropping that one player is a far better outcome than
// losing the server for everyone. Close is safe off the event loop — gnet
// queues it onto the owning poller.
func closeOnPanic(log *slog.Logger, scope string, c gnet.Conn) {
	if r := recover(); r != nil {
		safe.Report(log, scope, r)
		_ = c.Close()
	}
}

// closeOnPanicAction is the reactor-callback counterpart, deferred directly by
// OnTraffic/OnClose. On the event loop the failure posture is gnet's own
// teardown: returning gnet.Close drops that one connection instead of unwinding
// the reactor.
func closeOnPanicAction(log *slog.Logger, scope string, action *gnet.Action) {
	if r := recover(); r != nil {
		safe.Report(log, scope, r)
		*action = gnet.Close
	}
}
