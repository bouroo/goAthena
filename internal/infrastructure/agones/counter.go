package agones

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"agones.dev/agones/pkg/sdk/beta"
)

// PlayersCounterName is the name of the Agones counter that carries the
// number of players connected to a shard.
//
// It is the key the Fleet's counter and the FleetAutoscaler's policy agree
// on: the Fleet declares a counter by this name, and the autoscaler reads
// the aggregate under the same name. A typo in either is a fleet that
// scales on nothing, so the manifest test asserts the two agree.
const PlayersCounterName = "players"

// PlayerCounterCapacity is the number of players a shard reports as its
// capacity.
//
// It is the number of players one shard can serve before another is
// needed. The value is the shard's design point, not a measurement — the
// autoscaler divides the fleet's aggregate capacity by it to decide how
// many shards the load needs.
const PlayerCounterCapacity = 100

// CounterReporter is the surface a process uses to publish a counter to
// Agones: the Fleet's aggregate counter is the sum over the fleet's
// shards, so each shard publishes its own count.
//
// It is separate from Port: a process that does not run as a GameServer
// has no counters to publish.
type CounterReporter interface {
	// ReportCounter sets a counter's count on this GameServer.
	ReportCounter(ctx context.Context, name string, count, capacity int64) error
}

// CounterTimeout bounds one counter update. The sidecar answers from
// local state, so a slow answer means the sidecar is unhealthy.
const CounterTimeout = 10 * time.Second

// betaClient is the beta SDK's client surface, narrowed to what the
// counter path uses. It is an interface so the test can supply a fake
// without a gRPC server.
type betaClient interface {
	UpdateCounter(ctx context.Context, in *beta.UpdateCounterRequest, opts ...grpc.CallOption) (*beta.Counter, error)
}

// newBeta dials the sidecar's beta service. The beta service shares the
// sidecar's gRPC port — it is the same server, a second service
// registered on it.
func newBeta(conn grpc.ClientConnInterface) betaClient {
	return beta.NewSDKClient(conn)
}

// ReportCounter sets a counter's count and capacity on this GameServer.
//
// Agones aggregates the counter across the fleet's shards, which is what
// the FleetAutoscaler's CounterPolicy reads.
//
// A count outside [0, capacity] is rejected by the sidecar, so the
// capacity is checked here too: reporting a count above the capacity
// would silently fail server-side and leave the autoscaler scaling on a
// stale number.
func (s *Sidecar) ReportCounter(ctx context.Context, name string, count, capacity int64) error {
	if s.beta == nil {
		return fmt.Errorf("agones: no beta client (the sidecar is not reachable)")
	}
	if count < 0 {
		return fmt.Errorf("agones: counter %q count %d is negative", name, count)
	}
	if capacity < 0 {
		return fmt.Errorf("agones: counter %q capacity %d is negative", name, capacity)
	}
	if count > capacity {
		return fmt.Errorf("agones: counter %q count %d exceeds capacity %d", name, count, capacity)
	}
	ctx, cancel := context.WithTimeout(ctx, CounterTimeout)
	defer cancel()
	_, err := s.beta.UpdateCounter(ctx, &beta.UpdateCounterRequest{
		CounterUpdateRequest: &beta.CounterUpdateRequest{
			Name:     name,
			Count:    wrapperspb.Int64(count),
			Capacity: wrapperspb.Int64(capacity),
		},
	})
	if err != nil {
		return fmt.Errorf("agones: update counter %q: %w", name, err)
	}
	return nil
}

// ReportCounter is the no-op: a process that does not run as a GameServer
// has no counters to publish.
func (Noop) ReportCounter(context.Context, string, int64, int64) error {
	return nil
}
