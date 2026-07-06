//go:build unit

package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/bouroo/goAthena/internal/features/transit/domain"
	domainmock "github.com/bouroo/goAthena/internal/features/transit/domain/mock"
	"github.com/bouroo/goAthena/internal/features/transit/service"
	natsinfra "github.com/bouroo/goAthena/internal/infrastructure/messaging/nats"
)

// stubMessenger is a hand-rolled in-memory implementation of
// TransitMessenger. It supports a single installed subscription
// (Subscribe) and records PublishRequest payloads so tests can assert
// both the wire format and the subject the service targets.
type stubMessenger struct {
	mu sync.Mutex

	publishedSubjects []string
	publishedPayloads [][]byte
	publishReply      []byte
	publishErr        error

	handler func(ctx context.Context, data []byte) ([]byte, error)

	unsubscribed atomic.Bool
}

func (s *stubMessenger) PublishRequest(_ context.Context, subject string, data []byte) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.publishedSubjects = append(s.publishedSubjects, subject)
	s.publishedPayloads = append(s.publishedPayloads, append([]byte(nil), data...))
	if s.publishErr != nil {
		return nil, s.publishErr
	}
	return append([]byte(nil), s.publishReply...), nil
}

func (s *stubMessenger) Subscribe(_ string, handler func(ctx context.Context, data []byte) ([]byte, error)) (domain.UnsubscribeFunc, error) {
	s.mu.Lock()
	s.handler = handler
	s.mu.Unlock()
	return func() { s.unsubscribed.Store(true) }, nil
}

func (s *stubMessenger) invokeHandler(t *testing.T, data []byte) []byte {
	t.Helper()
	s.mu.Lock()
	h := s.handler
	s.mu.Unlock()
	require.NotNil(t, h, "subscribe was not called")
	reply, err := h(context.Background(), data)
	require.NoError(t, err)
	return reply
}

func stubEndpoints(ip string, port int) domain.TransitConfigSource {
	return service.NewStaticEndpointSource(domain.TransitEndpoint{IP: ip, Port: port})
}

func TestInitiateTransit_HappyPath(t *testing.T) {
	t.Parallel()
	messenger := &stubMessenger{}
	ctrl := gomock.NewController(t)
	loginIDs := domainmock.NewMockLoginIDGenerator(ctrl)
	loginIDs.EXPECT().Next().Return(uint32(0), uint32(0), nil).AnyTimes()

	tSvc := service.NewTransitService(messenger, loginIDs, stubEndpoints("10.0.0.5", 7121))

	messenger.mu.Lock()
	messenger.publishReply = mustJSON(t, domain.TransitAck{
		Accepted:   true,
		AssignIP:   "10.0.0.5",
		AssignPort: 7121,
		LoginID1:   0xdeadbeef,
		LoginID2:   0xcafebabe,
	})
	messenger.mu.Unlock()

	req := domain.TransitRequest{
		Snapshot:   domain.TransitSnapshot{CharID: 1001, AccountID: 42, MapName: "prt_fild08", X: 100, Y: 200, HP: 1000, SP: 500},
		SourceZone: "zone-a",
		TargetZone: "zone-b",
		TargetMap:  "prt_fild08",
	}

	ack, err := tSvc.InitiateTransit(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, ack)
	assert.True(t, ack.Accepted)
	assert.Equal(t, "10.0.0.5", ack.AssignIP)
	assert.Equal(t, 7121, ack.AssignPort)

	require.Len(t, messenger.publishedSubjects, 1)
	assert.Equal(t, natsinfra.TransitRequestSubject("zone-b"), messenger.publishedSubjects[0])

	require.Len(t, messenger.publishedPayloads, 1)
	var got domain.TransitRequest
	require.NoError(t, json.Unmarshal(messenger.publishedPayloads[0], &got))
	assert.Equal(t, req, got)
}

func TestInitiateTransit_RejectionPropagates(t *testing.T) {
	t.Parallel()
	messenger := &stubMessenger{}
	ctrl := gomock.NewController(t)
	loginIDs := domainmock.NewMockLoginIDGenerator(ctrl)
	loginIDs.EXPECT().Next().Return(uint32(0), uint32(0), nil).AnyTimes()

	tSvc := service.NewTransitService(messenger, loginIDs, stubEndpoints("10.0.0.5", 7121))
	messenger.publishReply = mustJSON(t, domain.TransitAck{Accepted: false, Reason: "lock_held"})

	_, err := tSvc.InitiateTransit(context.Background(), domain.TransitRequest{
		Snapshot:   domain.TransitSnapshot{CharID: 1},
		SourceZone: "a",
		TargetZone: "b",
		TargetMap:  "m",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrTransitRejected))
}

func TestInitiateTransit_PublishError(t *testing.T) {
	t.Parallel()
	messenger := &stubMessenger{publishErr: errors.New("nats down")}
	ctrl := gomock.NewController(t)
	loginIDs := domainmock.NewMockLoginIDGenerator(ctrl)
	tSvc := service.NewTransitService(messenger, loginIDs, stubEndpoints("10.0.0.5", 7121))

	_, err := tSvc.InitiateTransit(context.Background(), domain.TransitRequest{
		Snapshot:   domain.TransitSnapshot{CharID: 1},
		SourceZone: "a",
		TargetZone: "b",
		TargetMap:  "m",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nats down")
}

func TestInitiateTransit_RejectsEmptyZones(t *testing.T) {
	t.Parallel()
	messenger := &stubMessenger{}
	ctrl := gomock.NewController(t)
	loginIDs := domainmock.NewMockLoginIDGenerator(ctrl)
	tSvc := service.NewTransitService(messenger, loginIDs, stubEndpoints("10.0.0.5", 7121))

	_, err := tSvc.InitiateTransit(context.Background(), domain.TransitRequest{
		Snapshot:  domain.TransitSnapshot{CharID: 1},
		TargetMap: "m",
	})
	require.Error(t, err)

	_, err = tSvc.InitiateTransit(context.Background(), domain.TransitRequest{
		Snapshot:   domain.TransitSnapshot{CharID: 1},
		SourceZone: "a",
		TargetMap:  "m",
	})
	require.Error(t, err)
}

func TestHandleTransitRequest_Accepts(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	messenger := domainmock.NewMockTransitMessenger(ctrl)
	loginIDs := domainmock.NewMockLoginIDGenerator(ctrl)
	loginIDs.EXPECT().Next().Return(uint32(0x11111111), uint32(0x22222222), nil)

	tSvc := service.NewTransitService(messenger, loginIDs, stubEndpoints("10.0.0.5", 7121))

	ack, err := tSvc.HandleTransitRequest(context.Background(), domain.TransitRequest{
		Snapshot:   domain.TransitSnapshot{CharID: 1001, AccountID: 42},
		SourceZone: "zone-a",
		TargetZone: "zone-b",
		TargetMap:  "prt_fild08",
	})
	require.NoError(t, err)
	require.NotNil(t, ack)
	assert.True(t, ack.Accepted)
	assert.Equal(t, "10.0.0.5", ack.AssignIP)
	assert.Equal(t, 7121, ack.AssignPort)
	assert.Equal(t, uint32(0x11111111), ack.LoginID1)
	assert.Equal(t, uint32(0x22222222), ack.LoginID2)
}

func TestHandleTransitRequest_RejectsWhenEndpointUnknown(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	messenger := domainmock.NewMockTransitMessenger(ctrl)
	loginIDs := domainmock.NewMockLoginIDGenerator(ctrl)

	endpoints := service.NewStaticEndpointSource(domain.TransitEndpoint{}) // zero value -> not configured
	tSvc := service.NewTransitService(messenger, loginIDs, endpoints)

	ack, err := tSvc.HandleTransitRequest(context.Background(), domain.TransitRequest{
		Snapshot:   domain.TransitSnapshot{CharID: 1},
		SourceZone: "a",
		TargetZone: "b",
		TargetMap:  "m",
	})
	require.NoError(t, err)
	require.NotNil(t, ack)
	assert.False(t, ack.Accepted)
	assert.Equal(t, "endpoint_unknown", ack.Reason)
}

func TestHandleTransitRequest_ValidationErrors(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	messenger := domainmock.NewMockTransitMessenger(ctrl)
	loginIDs := domainmock.NewMockLoginIDGenerator(ctrl)
	tSvc := service.NewTransitService(messenger, loginIDs, stubEndpoints("10.0.0.5", 7121))

	_, err := tSvc.HandleTransitRequest(context.Background(), domain.TransitRequest{
		SourceZone: "a", TargetZone: "b", TargetMap: "m",
	})
	require.Error(t, err)

	_, err = tSvc.HandleTransitRequest(context.Background(), domain.TransitRequest{
		Snapshot: domain.TransitSnapshot{CharID: 1}, SourceZone: "a", TargetMap: "m",
	})
	require.Error(t, err)

	_, err = tSvc.HandleTransitRequest(context.Background(), domain.TransitRequest{
		Snapshot: domain.TransitSnapshot{CharID: 1}, SourceZone: "a", TargetZone: "b",
	})
	require.Error(t, err)
}

func TestHandleTransitRequest_LoginIDGeneratorError(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	messenger := domainmock.NewMockTransitMessenger(ctrl)
	loginIDs := domainmock.NewMockLoginIDGenerator(ctrl)
	loginIDs.EXPECT().Next().Return(uint32(0), uint32(0), errors.New("rng down"))

	tSvc := service.NewTransitService(messenger, loginIDs, stubEndpoints("10.0.0.5", 7121))

	_, err := tSvc.HandleTransitRequest(context.Background(), domain.TransitRequest{
		Snapshot:   domain.TransitSnapshot{CharID: 1},
		SourceZone: "a", TargetZone: "b", TargetMap: "m",
	})
	require.Error(t, err)
}

func TestSubscribeTransit_InstallsHandlerAndReplies(t *testing.T) {
	t.Parallel()
	messenger := &stubMessenger{}
	ctrl := gomock.NewController(t)
	loginIDs := domainmock.NewMockLoginIDGenerator(ctrl)
	loginIDs.EXPECT().Next().Return(uint32(0xaaaa), uint32(0xbbbb), nil)

	tSvc := service.NewTransitService(messenger, loginIDs, stubEndpoints("10.0.0.5", 7121))

	require.NoError(t, tSvc.SubscribeTransit(context.Background(), "zone-b"))

	req := domain.TransitRequest{
		Snapshot:   domain.TransitSnapshot{CharID: 1, AccountID: 1},
		SourceZone: "zone-a",
		TargetZone: "zone-b",
		TargetMap:  "m",
	}
	raw, err := json.Marshal(req)
	require.NoError(t, err)

	reply := messenger.invokeHandler(t, raw)
	var ack domain.TransitAck
	require.NoError(t, json.Unmarshal(reply, &ack))
	assert.True(t, ack.Accepted)
	assert.Equal(t, "10.0.0.5", ack.AssignIP)
	assert.Equal(t, 7121, ack.AssignPort)
	assert.Equal(t, uint32(0xaaaa), ack.LoginID1)
}

func TestSubscribeTransit_RejectsEmptyZone(t *testing.T) {
	t.Parallel()
	messenger := &stubMessenger{}
	ctrl := gomock.NewController(t)
	loginIDs := domainmock.NewMockLoginIDGenerator(ctrl)
	tSvc := service.NewTransitService(messenger, loginIDs, stubEndpoints("10.0.0.5", 7121))
	require.Error(t, tSvc.SubscribeTransit(context.Background(), ""))
}

func TestSubscribeTransit_MessengerErrorPropagates(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	messenger := domainmock.NewMockTransitMessenger(ctrl)
	loginIDs := domainmock.NewMockLoginIDGenerator(ctrl)
	messenger.EXPECT().
		Subscribe(gomock.Any(), gomock.Any()).
		Return(func() {}, errors.New("subscribe boom"))

	tSvc := service.NewTransitService(messenger, loginIDs, stubEndpoints("10.0.0.5", 7121))
	err := tSvc.SubscribeTransit(context.Background(), "zone-b")
	require.Error(t, err)
}

func TestRandomLoginIDGenerator_ProducesDistinctValues(t *testing.T) {
	t.Parallel()
	gen := service.NewRandomLoginIDGenerator()
	a1, a2, err := gen.Next()
	require.NoError(t, err)
	b1, b2, err := gen.Next()
	require.NoError(t, err)
	assert.NotEqual(t, [2]uint32{a1, a2}, [2]uint32{b1, b2})
}

func TestStaticEndpointSource(t *testing.T) {
	t.Parallel()
	src := service.NewStaticEndpointSource(domain.TransitEndpoint{IP: "10.0.0.5", Port: 7121})
	ep, ok := src.EndpointFor("zone-b")
	assert.True(t, ok)
	assert.Equal(t, "10.0.0.5", ep.IP)
	assert.Equal(t, 7121, ep.Port)

	empty := service.NewStaticEndpointSource(domain.TransitEndpoint{})
	_, ok = empty.EndpointFor("zone-b")
	assert.False(t, ok)
}

func TestHandleTransitRequest_ContextCancellation(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	messenger := domainmock.NewMockTransitMessenger(ctrl)
	loginIDs := domainmock.NewMockLoginIDGenerator(ctrl)
	tSvc := service.NewTransitService(messenger, loginIDs, stubEndpoints("10.0.0.5", 7121))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := tSvc.HandleTransitRequest(ctx, domain.TransitRequest{
		Snapshot:   domain.TransitSnapshot{CharID: 1},
		SourceZone: "a", TargetZone: "b", TargetMap: "m",
	})
	require.Error(t, err)
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

// Ensure the subject construction lines up with the nats infra
// package (regression test against renames).
func TestTransitSubjectMatchesInfra(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "zone.transit.request.zone-b", natsinfra.TransitRequestSubject("zone-b"))
}

// Sanity: time.Duration default is 2s (per spec).
func TestDefaultTransitTimeoutIsTwoSeconds(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 2*time.Second, service.DefaultTransitTimeout)
}
