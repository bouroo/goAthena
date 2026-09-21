package remote

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bouroo/goAthena/internal/modules/economy/app"
)

// Server is the host side of the extraction: it subscribes the economy
// subjects and serves them from the in-process service. `goathena
// serve-economy` runs one; multiple replicas behind QueueGroup load-share.
//
// The server owns no lifecycle beyond its subscriptions — draining the
// connection (nats.Drain) retires them, which is how the host shuts down.
type Server struct {
	nc   *nats.Conn
	svc  *app.EconomyService
	log  *slog.Logger
	subs []*nats.Subscription
}

// NewServer subscribes the economy subjects. A subscribe failure aborts the
// build (the host cannot serve partially); subscriptions already opened are
// drained before returning.
func NewServer(nc *nats.Conn, svc *app.EconomyService, log *slog.Logger) (*Server, error) {
	if log == nil {
		log = slog.Default()
	}
	s := &Server{nc: nc, svc: svc, log: log}
	for subject, handler := range map[string]nats.MsgHandler{
		SubjectZenyGet:    s.onGet,
		SubjectZenyCredit: s.onMove(s.svc.CreditZenyFor),
		SubjectZenyDeduct: s.onMove(s.svc.DeductZenyFor),
	} {
		sub, err := nc.QueueSubscribe(subject, QueueGroup, handler)
		if err != nil {
			s.drain()
			return nil, fmt.Errorf("nats subscribe %s: %w", subject, err)
		}
		s.subs = append(s.subs, sub)
	}
	// Subscribe is asynchronous — the broker learns about the SUB after this
	// round-trip. Flushing pins the contract "when NewServer returns, a
	// caller's request is servable"; without it the first request can race
	// the SUB and die with no-responders.
	if err := nc.Flush(); err != nil {
		s.drain()
		return nil, fmt.Errorf("nats flush subscriptions: %w", err)
	}
	return s, nil
}

// Subscriptions reports how many subjects this server answers (observability:
// the host logs it at boot).
func (s *Server) Subscriptions() int { return len(s.subs) }

// drain unwinds subscriptions opened before a failed build.
func (s *Server) drain() {
	for _, sub := range s.subs {
		_ = sub.Drain()
	}
}

// boundedCtx bounds the service call so a wedged DB cannot pile up handler
// goroutines; the caller's own deadline bounds what it waits for.
func (s *Server) boundedCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), HandlerTimeout)
}

// onGet answers zeny.get.
func (s *Server) onGet(msg *nats.Msg) {
	req, err := decodeRequest[zenyGetReq](msg.Data)
	if err != nil {
		s.respond(msg, reply{OK: false, ErrCode: codeInternal, ErrMsg: err.Error()})
		return
	}
	ctx, cancel := s.boundedCtx()
	defer cancel()
	amount, err := s.svc.GetZeny(ctx, req.CharID)
	if err != nil {
		s.fail(msg, "get zeny", err)
		return
	}
	s.respond(msg, reply{OK: true, Amount: amount})
}

// onMove answers zeny.credit / zeny.deduct. The service method is injected so
// both subjects share one decode/reply path and the credit and deduct legs
// cannot drift.
func (s *Server) onMove(move func(context.Context, uint32, int32, app.LedgerEntry) error) nats.MsgHandler {
	return func(msg *nats.Msg) {
		req, err := decodeRequest[zenyMoveReq](msg.Data)
		if err != nil {
			s.respond(msg, reply{OK: false, ErrCode: codeInternal, ErrMsg: err.Error()})
			return
		}
		ctx, cancel := s.boundedCtx()
		defer cancel()
		if err := move(ctx, req.CharID, req.Amount, req.Entry.toEntry()); err != nil {
			s.fail(msg, "move zeny", err)
			return
		}
		s.respond(msg, reply{OK: true})
	}
}

// fail answers with the sentinel-preserving error envelope. The detail stays
// in the host log; the wire carries the code plus a safe message only — a
// wrapped driver error must not cross the bus.
func (s *Server) fail(msg *nats.Msg, verb string, err error) {
	code := codeFor(err)
	s.log.Warn("economy host: request failed", "verb", verb, "code", code, "err", err)
	text := "request failed"
	if code == codeInternal {
		text = "internal error"
	}
	s.respond(msg, reply{OK: false, ErrCode: code, ErrMsg: text})
}

// respond publishes the envelope, tolerating a vanished reply subject (the
// caller gave up — its context deadline already surfaced the timeout).
func (s *Server) respond(msg *nats.Msg, r reply) {
	if msg.Reply == "" {
		return
	}
	if err := msg.Respond(encodeReply(r)); err != nil {
		s.log.Warn("economy host: reply publish failed", "subject", msg.Subject, "err", err)
	}
}

// HandlerTimeout bounds one server-side service call; see boundedCtx.
const HandlerTimeout = 30 * time.Second
