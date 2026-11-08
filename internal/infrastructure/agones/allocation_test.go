package agones

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc"

	"agones.dev/agones/pkg/sdk"
)

// fakeSDK is a minimal SDKClient that answers GetGameServer with a
// canned GameServer. Every other method panics: the allocation path must
// not touch them.
type fakeSDK struct {
	sdk.SDKClient
	gs    *sdk.GameServer
	err   error
	calls int
}

func (f *fakeSDK) GetGameServer(context.Context, *sdk.Empty, ...grpc.CallOption) (*sdk.GameServer, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.gs, nil
}

// gameserver builds the GameServer the sidecar would report for a shard
// with the given address and ports.
func gameserver(name, address string, ports ...*sdk.GameServer_Status_Port) *sdk.GameServer {
	return &sdk.GameServer{
		ObjectMeta: &sdk.GameServer_ObjectMeta{Name: name},
		Status: &sdk.GameServer_Status{
			State:   "Ready",
			Address: address,
			Ports:   ports,
		},
	}
}

func gamePort(name string, port int32) *sdk.GameServer_Status_Port {
	return &sdk.GameServer_Status_Port{Name: name, Port: port}
}

func TestAllocateReadsTheAssignedAddressAndPort(t *testing.T) {
	// The port Agones assigned differs from the container port under a
	// Dynamic policy — that is the case this exists for.
	f := &fakeSDK{gs: gameserver("shard-1", "10.0.0.7", gamePort(GamePortName, 31234))}
	s := &Sidecar{client: f}

	allocation, err := s.Allocate(context.Background())
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if got := allocation.Address.String(); got != "10.0.0.7" {
		t.Errorf("address = %q, want 10.0.0.7", got)
	}
	if allocation.Port != 31234 {
		t.Errorf("port = %d, want 31234", allocation.Port)
	}
}

func TestAllocatePicksTheGamePortAmongOthers(t *testing.T) {
	// Agones reports every declared port; only "game" is the map listener.
	f := &fakeSDK{gs: gameserver("shard-1", "10.0.0.7",
		gamePort("http", 8080),
		gamePort(GamePortName, 31234),
	)}
	s := &Sidecar{client: f}

	allocation, err := s.Allocate(context.Background())
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if allocation.Port != 31234 {
		t.Errorf("port = %d, want 31234 (the game port, not the first)", allocation.Port)
	}
}

func TestAllocateRejectsAnUnusableAllocation(t *testing.T) {
	cases := []struct {
		name string
		gs   *sdk.GameServer
		want string
	}{
		{
			name: "no address",
			gs:   gameserver("shard-1", "", gamePort(GamePortName, 31234)),
			want: "address",
		},
		{
			name: "not an ip",
			gs:   gameserver("shard-1", "not-an-ip", gamePort(GamePortName, 31234)),
			want: "address",
		},
		{
			name: "ipv6 address",
			gs:   gameserver("shard-1", "::1", gamePort(GamePortName, 31234)),
			want: "not ipv4",
		},
		{
			name: "no game port",
			gs:   gameserver("shard-1", "10.0.0.7", gamePort("http", 8080)),
			want: "no \"game\" port",
		},
		{
			name: "port out of range",
			gs:   gameserver("shard-1", "10.0.0.7", gamePort(GamePortName, 70000)),
			want: "out of range",
		},
		{
			name: "no status",
			gs:   &sdk.GameServer{ObjectMeta: &sdk.GameServer_ObjectMeta{Name: "shard-1"}},
			want: "no status",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Sidecar{client: &fakeSDK{gs: tc.gs}}
			_, err := s.Allocate(context.Background())
			if err == nil {
				t.Fatal("Allocate = nil error, want a rejection")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestAllocatePropagatesTheSidecarError(t *testing.T) {
	s := &Sidecar{client: &fakeSDK{err: context.DeadlineExceeded}}
	_, err := s.Allocate(context.Background())
	if err == nil {
		t.Fatal("Allocate = nil error, want the sidecar's error")
	}
	if !strings.Contains(err.Error(), "get gameserver") {
		t.Errorf("error = %q, want it to mention the failing call", err)
	}
}

func TestNoopHasNoAllocation(t *testing.T) {
	// A process that does not run as a GameServer has nothing to advertise,
	// and the error says so rather than returning a zero address that a
	// client would dial and drop.
	var p Noop
	_, err := p.Allocate(context.Background())
	if err == nil {
		t.Fatal("Noop.Allocate = nil error, want ErrNoAllocation")
	}
	if err != ErrNoAllocation {
		t.Errorf("Noop.Allocate error = %v, want ErrNoAllocation", err)
	}
}
