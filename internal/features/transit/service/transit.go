// Package service is the implementation of the cross-zone transit
// handshake (D23). The transit service is the source/target for
// character moves between zone servers; it speaks NATS request/reply
// under the zone.transit.request.<zoneID> subject and JSON-encodes
// the wire payload.
package service

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/bouroo/goAthena/internal/features/transit/domain"
	natsinfra "github.com/bouroo/goAthena/internal/infrastructure/messaging/nats"
)

// DefaultTransitTimeout is the per-request timeout used by
// InitiateTransit when the context does not carry a tighter deadline.
// Matches the Scout-findings §2.2 "2s" recommendation for transit
// handshakes.
const DefaultTransitTimeout = 2 * time.Second

// lockTokenGenerator returns the two 32-bit halves of the auth ticket.
// It uses crypto/rand so the values are unpredictable to clients
// trying to forge a CA_LOGIN on a sibling zone.
type lockTokenGenerator struct{}

func (lockTokenGenerator) Next() (uint32, uint32, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0, 0, fmt.Errorf("generate login id: %w", err)
	}
	return binary.LittleEndian.Uint32(b[:4]), binary.LittleEndian.Uint32(b[4:]), nil
}

// NewRandomLoginIDGenerator returns the production LoginIDGenerator.
func NewRandomLoginIDGenerator() domain.LoginIDGenerator {
	return lockTokenGenerator{}
}

// staticEndpointSource is the production TransitConfigSource: the
// zone service knows its own IP/Port from environment config and
// serves them for every "is this me?" lookup.
type staticEndpointSource struct {
	endpoint domain.TransitEndpoint
}

// NewStaticEndpointSource returns a TransitConfigSource that answers
// the same endpoint for every zone ID. The zone service uses this to
// hand out its own address when it is the target of an inbound
// transit request.
func NewStaticEndpointSource(endpoint domain.TransitEndpoint) domain.TransitConfigSource {
	return &staticEndpointSource{endpoint: endpoint}
}

func (s *staticEndpointSource) EndpointFor(zoneID string) (domain.TransitEndpoint, bool) {
	if s.endpoint.IP == "" || s.endpoint.Port <= 0 {
		return domain.TransitEndpoint{}, false
	}
	return s.endpoint, true
}

// transit is the service-layer implementation of TransitService. It
// is goroutine-safe: HandleTransitRequest is pure (no shared mutable
// state) and InitiateTransit delegates concurrency to the underlying
// TransitMessenger.
type transit struct {
	messenger domain.TransitMessenger
	loginIDs  domain.LoginIDGenerator
	endpoints domain.TransitConfigSource
	now       func() time.Time
}

// Option mutates a transit service during construction.
type Option func(*transit)

// WithClock overrides the time source used in logs / metrics. Not
// currently consumed by any public method but reserved for future
// telemetry hooks.
func WithClock(now func() time.Time) Option {
	return func(t *transit) { t.now = now }
}

// NewTransitService wires the transit use cases. The messenger is the
// narrow NATS port; loginIDs mints auth tickets; endpoints supplies
// the TCP endpoint advertised to clients on successful handshakes.
func NewTransitService(
	messenger domain.TransitMessenger,
	loginIDs domain.LoginIDGenerator,
	endpoints domain.TransitConfigSource,
	opts ...Option,
) domain.TransitService {
	t := &transit{
		messenger: messenger,
		loginIDs:  loginIDs,
		endpoints: endpoints,
		now:       time.Now,
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

// InitiateTransit serialises req to JSON and publishes it via NATS
// request/reply to zone.transit.request.<TargetZone>. The reply
// payload is decoded back into a TransitAck.
func (t *transit) InitiateTransit(ctx context.Context, req domain.TransitRequest) (*domain.TransitAck, error) {
	if req.TargetZone == "" {
		return nil, fmt.Errorf("transit: initiate: target_zone is empty")
	}
	if req.SourceZone == "" {
		return nil, fmt.Errorf("transit: initiate: source_zone is empty")
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("transit: marshal request for char %d: %w", req.Snapshot.CharID, err)
	}

	subject := natsinfra.TransitRequestSubject(req.TargetZone)
	reply, err := t.messenger.PublishRequest(ctx, subject, payload)
	if err != nil {
		return nil, fmt.Errorf("transit: request to %s for char %d: %w", req.TargetZone, req.Snapshot.CharID, err)
	}

	ack := &domain.TransitAck{}
	if err := json.Unmarshal(reply, ack); err != nil {
		return nil, fmt.Errorf("transit: decode ack from %s for char %d: %w", req.TargetZone, req.Snapshot.CharID, err)
	}
	if !ack.Accepted {
		return ack, fmt.Errorf("transit: target %s rejected char %d: %w: %s",
			req.TargetZone, req.Snapshot.CharID, domain.ErrTransitRejected, ack.Reason)
	}
	return ack, nil
}

// HandleTransitRequest validates the request, mints an auth ticket,
// and returns the TransitAck. It is the target-side counterpart to
// InitiateTransit and is invoked by the inbound NATS subscription
// installed by SubscribeTransit.
func (t *transit) HandleTransitRequest(ctx context.Context, req domain.TransitRequest) (*domain.TransitAck, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("transit: handle: %w", err)
	}
	if req.TargetZone == "" {
		return nil, fmt.Errorf("transit: handle: target_zone is empty")
	}
	if req.Snapshot.CharID == 0 {
		return nil, fmt.Errorf("transit: handle: snapshot.char_id is zero")
	}
	if strings.TrimSpace(req.TargetMap) == "" {
		return nil, fmt.Errorf("transit: handle: target_map is empty")
	}

	endpoint, ok := t.endpoints.EndpointFor(req.TargetZone)
	if !ok {
		return &domain.TransitAck{
			Accepted: false,
			Reason:   "endpoint_unknown",
		}, nil
	}

	login1, login2, err := t.loginIDs.Next()
	if err != nil {
		return nil, fmt.Errorf("transit: handle char %d: %w", req.Snapshot.CharID, err)
	}

	return &domain.TransitAck{
		Accepted:   true,
		AssignIP:   endpoint.IP,
		AssignPort: endpoint.Port,
		LoginID1:   login1,
		LoginID2:   login2,
	}, nil
}

// SubscribeTransit installs the inbound subscription on
// zone.transit.request.<zoneID>. The handler decodes the request,
// invokes HandleTransitRequest, and writes the JSON-encoded ack back
// as the reply. Unsubscribe is exposed for graceful shutdown.
func (t *transit) SubscribeTransit(ctx context.Context, zoneID string) error {
	if zoneID == "" {
		return fmt.Errorf("transit: subscribe: zone_id is empty")
	}
	subject := natsinfra.TransitRequestSubject(zoneID)

	_, err := t.messenger.Subscribe(subject, func(handlerCtx context.Context, data []byte) ([]byte, error) {
		var req domain.TransitRequest
		if err := json.Unmarshal(data, &req); err != nil {
			return nil, fmt.Errorf("transit: decode request on %s: %w", subject, err)
		}
		ack, err := t.HandleTransitRequest(handlerCtx, req)
		if err != nil {
			return nil, err
		}
		payload, err := json.Marshal(ack)
		if err != nil {
			return nil, fmt.Errorf("transit: marshal ack on %s: %w", subject, err)
		}
		return payload, nil
	})
	if err != nil {
		return fmt.Errorf("transit: subscribe %s: %w", subject, err)
	}

	return nil
}
