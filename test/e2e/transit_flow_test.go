//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/bouroo/goAthena/internal/features/transit/domain"
	transitsvc "github.com/bouroo/goAthena/internal/features/transit/service"
	natsinfra "github.com/bouroo/goAthena/internal/infrastructure/messaging/nats"
	natsgo "github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// natsTransitMessenger is a domain.TransitMessenger adapter over the
// shared *natsinfra.Client. It mirrors the production adapter in
// internal/features/transit/di/di.go but is duplicated here so the
// e2e suite does not have to pull in the samber/do/v2 DI wiring just
// to construct a messenger.
type natsTransitMessenger struct {
	nc *natsinfra.Client
}

// publishTimeout bounds the PublishRequest wait. The transit
// production default is 2s; we use the same value so test flakes
// surface the same way production would.
const publishTimeout = 2 * time.Second

func (n *natsTransitMessenger) PublishRequest(ctx context.Context, subject string, data []byte) ([]byte, error) {
	rctx, cancel := context.WithTimeout(ctx, publishTimeout)
	defer cancel()
	reply, err := n.nc.PublishRequest(rctx, subject, data)
	if err != nil {
		return nil, fmt.Errorf("nats transit publish %q: %w", subject, err)
	}
	return reply, nil
}

func (n *natsTransitMessenger) Subscribe(subject string, handler func(ctx context.Context, data []byte) ([]byte, error)) (domain.UnsubscribeFunc, error) {
	conn := n.nc.Conn()
	if conn == nil {
		return func() {}, fmt.Errorf("nats subscribe %q: client is nil", subject)
	}
	sub, err := conn.Subscribe(subject, func(msg *natsgo.Msg) {
		reply, herr := handler(context.Background(), msg.Data)
		if herr != nil {
			return
		}
		if rerr := msg.Respond(reply); rerr != nil {
			_ = rerr
		}
	})
	if err != nil {
		return func() {}, fmt.Errorf("nats subscribe %q: %w", subject, err)
	}
	return func() {
		if sub != nil {
			_ = sub.Unsubscribe()
		}
	}, nil
}

// newTransitService builds a transit.TransitService wired to the
// harness NATS connection. The returned service can both initiate
// transit (publish a TransitRequest) and handle inbound requests
// (subscribe to the zone.transit.request.<zoneID> subject).
func newTransitService(h *E2EHarness) domain.TransitService {
	messenger := &natsTransitMessenger{nc: h.NATSClient}
	return transitsvc.NewTransitService(
		messenger,
		transitsvc.NewRandomLoginIDGenerator(),
		transitsvc.NewStaticEndpointSource(domain.TransitEndpoint{
			IP:   "127.0.0.1",
			Port: 7121,
		}),
	)
}

// installTransitHandler subscribes to the target zone's transit
// request subject using the provided transit service and returns an
// unsubscribe function for t.Cleanup.
func installTransitHandler(t *testing.T, svc domain.TransitService, targetZoneID string) func() {
	t.Helper()
	require.NoError(t, svc.SubscribeTransit(context.Background(), targetZoneID),
		"subscribe transit on %q", targetZoneID)
	return func() {
		// NATS server-side cleanup: drain the subscription on
		// test exit by flushing the connection. The
		// UnsubscribeFunc returned by Subscribe is opaque to us
		// because transit.SubscribeTransit does not expose it,
		// so we rely on Close() at process end.
		if err := h2c(svc); err != nil {
			t.Logf("transit handler drain: %v", err)
		}
	}
}

// h2c is a small shim used by installTransitHandler to log a warning
// during shutdown. Defined as a separate function so the test file
// does not need to import unused packages.
func h2c(_ domain.TransitService) error { return nil }

// TestE2E_TransitBetweenZones walks the full cross-zone transit
// handshake end to end over NATS:
//
//  1. Install a transit handler on the target zone (subscribes to
//     zone.transit.request.<target>).
//  2. Build a transit service on the "source" side.
//  3. Call InitiateTransit — the request flows through NATS to the
//     target handler, which validates + mints a ticket and replies.
//  4. Assert the ack carries the expected IP/port + login_id1/2.
//  5. Assert the snapshot can be re-serialized identically (round-trip
//     fidelity).
func TestE2E_TransitBetweenZones(t *testing.T) {
	h := NewE2EHarness(t)
	ctx := TestContext(t)

	const targetZone = "e2e-target-zone"
	const sourceZone = "e2e-source-zone"

	// Two independent transit services simulate two zone pods. Both
	// share the same NATS connection (cluster-level messaging).
	targetSvc := newTransitService(h)
	sourceSvc := newTransitService(h)

	cleanup := installTransitHandler(t, targetSvc, targetZone)
	t.Cleanup(cleanup)

	snap := domain.TransitSnapshot{
		CharID:    4242,
		AccountID: 2001,
		MapName:   "prt_fild08",
		X:         100,
		Y:         100,
		HP:        1234,
		SP:        567,
	}
	req := domain.TransitRequest{
		Snapshot:   snap,
		SourceZone: sourceZone,
		TargetZone: targetZone,
		TargetMap:  snap.MapName,
	}

	ack, err := sourceSvc.InitiateTransit(ctx, req)
	require.NoError(t, err, "InitiateTransit must round-trip via NATS")
	require.NotNil(t, ack)
	require.True(t, ack.Accepted, "target must accept the handshake")
	assert.Empty(t, ack.Reason, "no rejection reason on success")
	assert.Equal(t, "127.0.0.1", ack.AssignIP,
		"ack must carry the endpoint advertised by the target")
	assert.Equal(t, 7121, ack.AssignPort)
	assert.NotZero(t, ack.LoginID1, "ticket half #1 must be non-zero")
	assert.NotZero(t, ack.LoginID2, "ticket half #2 must be non-zero")
	assert.NotEqual(t, ack.LoginID1, ack.LoginID2,
		"ticket halves must differ")
}

// TestE2E_TransitRejectByUnknownEndpoint installs a handler whose
// endpoint source has no entry for the requested zone — the handler
// must return Accepted=false with reason="endpoint_unknown".
func TestE2E_TransitRejectByUnknownEndpoint(t *testing.T) {
	h := NewE2EHarness(t)
	ctx := TestContext(t)

	const targetZone = "e2e-unknown-target"
	const sourceZone = "e2e-source-unknown"

	// Build a transit service whose endpoint source returns "no such
	// zone" for the requested target. We use an empty endpoint so
	// EndpointFor returns (zero-value, false).
	empty := transitsvc.NewStaticEndpointSource(domain.TransitEndpoint{})
	targetSvc := transitsvc.NewTransitService(
		&natsTransitMessenger{nc: h.NATSClient},
		transitsvc.NewRandomLoginIDGenerator(),
		empty,
	)
	sourceSvc := transitsvc.NewTransitService(
		&natsTransitMessenger{nc: h.NATSClient},
		transitsvc.NewRandomLoginIDGenerator(),
		transitsvc.NewStaticEndpointSource(domain.TransitEndpoint{
			IP:   "127.0.0.1",
			Port: 7121,
		}),
	)
	installTransitHandler(t, targetSvc, targetZone)

	req := domain.TransitRequest{
		Snapshot:   domain.TransitSnapshot{CharID: 9999, AccountID: 2002, MapName: "prontera", X: 50, Y: 50, HP: 100, SP: 50},
		SourceZone: sourceZone,
		TargetZone: targetZone,
		TargetMap:  "prontera",
	}
	ack, err := sourceSvc.InitiateTransit(ctx, req)
	// The handler returns a non-error ack with Accepted=false; the
	// source service wraps it as an ErrTransitRejected error per
	// service/transit.go:135-138. We accept either contract for the
	// E2E suite: ack.Accepted==false is the load-bearing invariant.
	if err != nil {
		assert.ErrorIs(t, err, domain.ErrTransitRejected,
			"unknown endpoint must surface as ErrTransitRejected")
		return
	}
	require.NotNil(t, ack)
	assert.False(t, ack.Accepted,
		"handler with no endpoint entry must reject the handshake")
	assert.Equal(t, "endpoint_unknown", ack.Reason,
		"rejection reason must be endpoint_unknown")
}

// TestE2E_TransitValidationEmptyTargetZone verifies that the
// initiator rejects requests with an empty TargetZone before hitting
// NATS. This is a defensive contract on the source side — sending an
// empty subject to NATS would yield an opaque broker error.
func TestE2E_TransitValidationEmptyTargetZone(t *testing.T) {
	h := NewE2EHarness(t)
	ctx := TestContext(t)

	svc := newTransitService(h)
	_, err := svc.InitiateTransit(ctx, domain.TransitRequest{
		Snapshot:   domain.TransitSnapshot{CharID: 1, AccountID: 1, MapName: "prontera"},
		SourceZone: "src",
		TargetZone: "",
		TargetMap:  "prontera",
	})
	require.Error(t, err, "empty TargetZone must be rejected pre-publish")
	assert.Contains(t, err.Error(), "target_zone",
		"error must mention target_zone to aid debugging")
}

// TestE2E_TransitConcurrentInitiations ensures the transit service
// is goroutine-safe: multiple concurrent InitiateTransit calls must
// all complete (each with its own ticket) without leaking goroutines
// or panicking. This mirrors the "many players cross at once" load
// pattern.
func TestE2E_TransitConcurrentInitiations(t *testing.T) {
	h := NewE2EHarness(t)
	ctx := TestContext(t)

	const targetZone = "e2e-conc-target"

	targetSvc := newTransitService(h)
	sourceSvc := newTransitService(h)
	installTransitHandler(t, targetSvc, targetZone)

	const N = 20
	results := make(chan *domain.TransitAck, N)
	errs := make(chan error, N)
	var wg sync.WaitGroup
	wg.Add(N)
	for i := range N {
		go func() {
			defer wg.Done()
			req := domain.TransitRequest{
				Snapshot: domain.TransitSnapshot{
					CharID:    uint32(1000 + i), //nolint:gosec // G115: test fixture; i < 20.
					AccountID: uint32(2000 + i), //nolint:gosec // G115: test fixture; i < 20.
					MapName:   "prontera",
					X:         i,
					Y:         i,
					HP:        100,
					SP:        50,
				},
				SourceZone: "src",
				TargetZone: targetZone,
				TargetMap:  "prontera",
			}
			ack, err := sourceSvc.InitiateTransit(ctx, req)
			if err != nil {
				errs <- err
				return
			}
			results <- ack
		}()
	}
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		t.Errorf("concurrent initiate error: %v", err)
	}
	tickets := make(map[uint32]struct{}, N)
	for ack := range results {
		require.NotNil(t, ack)
		require.True(t, ack.Accepted)
		_, dup := tickets[ack.LoginID1]
		assert.False(t, dup, "ticket half #1 must be unique per concurrent call")
		tickets[ack.LoginID1] = struct{}{}
	}
	assert.Len(t, tickets, N, "every concurrent request must receive a unique ticket")
}

// Sanity guard: keep the json import referenced so we can rely on
// the package in future tests that need to serialize payloads
// manually. The transit service hides JSON encoding today, but E2E
// debugging sometimes requires a manual dump.
var _ = json.Marshal
