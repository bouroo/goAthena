//go:build unit

package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"testing"

	"github.com/bouroo/goAthena/internal/infrastructure/agones"
)

// stubAlloctor answers with a fixed allocation or a fixed error.
type stubAlloctor struct {
	allocation agones.Allocation
	err        error
}

func (s stubAlloctor) Allocate(context.Context) (agones.Allocation, error) {
	return s.allocation, s.err
}

// TestNewAgonesMapEndpointAdvertisesTheAllocation proves the adapter
// carries the allocation's address and port into the gateway's endpoint —
// the whole point of reading the allocation rather than the configuration.
func TestNewAgonesMapEndpointAdvertisesTheAllocation(t *testing.T) {
	addr := netip.MustParseAddr("10.0.0.7")
	resolver, err := NewAgonesMapEndpoint(context.Background(),
		stubAlloctor{allocation: agones.Allocation{Address: addr, Port: 31234}},
		discardLogger())
	if err != nil {
		t.Fatalf("NewAgonesMapEndpoint: %v", err)
	}
	endpoint, err := resolver.ResolveMapEndpoint(context.Background())
	if err != nil {
		t.Fatalf("ResolveMapEndpoint: %v", err)
	}
	if endpoint.Host != "10.0.0.7" {
		t.Errorf("host = %q, want 10.0.0.7 (the allocated address)", endpoint.Host)
	}
	if endpoint.Port != 31234 {
		t.Errorf("port = %d, want 31234 (the allocated port)", endpoint.Port)
	}
}

// TestNewAgonesMapEndpointReadsTheAllocationOnce proves the allocation is
// read at build time and cached: the node address and the bound port do not
// change over a GameServer's life, and asking the sidecar per connection
// would be a needless round trip.
func TestNewAgonesMapEndpointReadsTheAllocationOnce(t *testing.T) {
	addr := netip.MustParseAddr("10.0.0.7")
	alloctor := &countingAlloctor{allocation: agones.Allocation{Address: addr, Port: 31234}}
	resolver, err := NewAgonesMapEndpoint(context.Background(), alloctor, discardLogger())
	if err != nil {
		t.Fatalf("NewAgonesMapEndpoint: %v", err)
	}
	for range 3 {
		if _, err := resolver.ResolveMapEndpoint(context.Background()); err != nil {
			t.Fatalf("ResolveMapEndpoint: %v", err)
		}
	}
	if alloctor.calls != 1 {
		t.Errorf("allocation reads = %d, want 1 (read once, then cached)", alloctor.calls)
	}
}

// countingAlloctor counts the allocation reads.
type countingAlloctor struct {
	allocation agones.Allocation
	err        error
	calls      int
}

func (c *countingAlloctor) Allocate(context.Context) (agones.Allocation, error) {
	c.calls++
	return c.allocation, c.err
}

// TestNewAgonesMapEndpointFailsWithoutAnAllocation proves a shard that
// cannot say where it is does not start: every client it handed a
// character to would be sent to an unroutable address.
func TestNewAgonesMapEndpointFailsWithoutAnAllocation(t *testing.T) {
	_, err := NewAgonesMapEndpoint(context.Background(),
		stubAlloctor{err: errors.New("sidecar unreachable")}, discardLogger())
	if err == nil {
		t.Fatal("NewAgonesMapEndpoint = nil error, want the allocation's error")
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
