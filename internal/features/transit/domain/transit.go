// Package domain declares the inbound and outbound ports for the
// cross-zone transit handshake (D23). The transit service is the
// source/target for character moves between zone servers; it speaks
// NATS request/reply under the zone.transit.request.<zoneID> subject.
package domain

import (
	"context"
	"errors"
)

// Sentinel errors returned by the transit service. Callers compare
// with errors.Is so wrapping is preserved across the transport
// boundary.
var (
	// ErrTransitRejected is returned when the target zone declines the
	// handshake. The wrapped reason is included in the ack to the
	// source zone.
	ErrTransitRejected = errors.New("transit rejected by target zone")

	// ErrTransitTimeout is returned when the NATS request/reply to the
	// target zone does not respond within the configured window.
	ErrTransitTimeout = errors.New("transit request timed out")
)

// TransitSnapshot is the player state transferred between zones. It
// is the minimum the target zone needs to reconstruct a session
// without a database round-trip; richer state (inventory, status
// effects, quest flags) is layered on in later phases.
type TransitSnapshot struct {
	// CharID is the numeric primary key from the rAthena `char` table.
	CharID uint32
	// AccountID is the owning account.
	AccountID uint32
	// MapName is the destination map name (max 16 bytes).
	MapName string
	// X is the destination X coordinate in cells.
	X int
	// Y is the destination Y coordinate in cells.
	Y int
	// HP is the current HP at the moment of transit.
	HP int
	// SP is the current SP at the moment of transit.
	SP int
}

// TransitRequest initiates a character transfer between zones.
type TransitRequest struct {
	// Snapshot is the player state to hand to the target zone.
	Snapshot TransitSnapshot
	// SourceZone is the zone ID initiating the move.
	SourceZone string
	// TargetZone is the zone ID that should receive the character.
	TargetZone string
	// TargetMap is the destination map name (e.g. "prt_fild08").
	TargetMap string
}

// TransitAck is the response from the target zone back to the source.
// On success AssignIP/AssignPort carry the TCP endpoint the client
// should reconnect to (the rAthena 0x0ac7 packet at clif.cpp:2168).
type TransitAck struct {
	// Accepted is true when the target zone accepted the handshake.
	Accepted bool
	// Reason is empty on success; on rejection it carries a short
	// human-readable code (e.g. "lock_held", "no_slot").
	Reason string
	// AssignIP is the IP the client should dial to reconnect.
	AssignIP string
	// AssignPort is the port the client should dial.
	AssignPort int
	// LoginID1 is the first half of the auth ticket the target zone
	// generated; zero on rejection. Replaces rAthena's char-server
	// login_id1 (char_mapif.cpp:594-602).
	LoginID1 uint32
	// LoginID2 is the second half of the auth ticket; zero on rejection.
	LoginID2 uint32
}

// TransitService is the inbound port — the use case interface invoked
// by the zone service when a player crosses a map-server boundary.
type TransitService interface {
	// InitiateTransit starts a transfer from this zone to another.
	// Publishes a TransitRequest via NATS request/reply to the target
	// zone and returns the TransitAck. The caller is responsible for
	// unregistering the character from the local registry on success
	// and forwarding AssignIP/AssignPort to the client.
	InitiateTransit(ctx context.Context, req TransitRequest) (*TransitAck, error)

	// HandleTransitRequest processes an incoming transit request
	// received via NATS. It validates the request, generates the
	// auth ticket, and returns the ack. The target-zone gRPC handler
	// wires this to the inbound NATS subscription installed by
	// SubscribeTransit.
	HandleTransitRequest(ctx context.Context, req TransitRequest) (*TransitAck, error)

	// SubscribeTransit installs the inbound NATS subscription for
	// transit requests addressed to this zone. The subscription is
	// installed under the zone.transit.request.<zoneID> subject and
	// routes incoming messages to HandleTransitRequest.
	SubscribeTransit(ctx context.Context, zoneID string) error
}

// TransitMessenger is the narrow outbound port the transit service
// uses to talk to NATS. It is intentionally minimal so unit tests can
// mock the messaging layer without a running NATS broker.
type TransitMessenger interface {
	// PublishRequest publishes a request and waits for a reply on the
	// inbox subject embedded in the message by the broker. The reply
	// payload is returned verbatim.
	PublishRequest(ctx context.Context, subject string, data []byte) ([]byte, error)

	// Subscribe installs a handler on the given subject. The returned
	// UnsubscribeFunc removes the subscription.
	Subscribe(subject string, handler func(ctx context.Context, data []byte) ([]byte, error)) (UnsubscribeFunc, error)
}

// UnsubscribeFunc removes a previously installed subscription. It is
// safe to call multiple times.
type UnsubscribeFunc func()

// LoginIDGenerator mints the two halves of the auth ticket returned
// to the source zone. It is injected so tests can supply a
// deterministic sequence.
type LoginIDGenerator interface {
	// Next returns (login_id1, login_id2) for a transit handshake.
	Next() (uint32, uint32, error)
}

// TransitEndpoint describes the target zone's TCP endpoint, used to
// populate the TransitAck. The values come from the zone service's
// own configuration (which Agones annotates onto the pod).
type TransitEndpoint struct {
	// IP is the textual address (IPv4 or IPv6) the client should dial.
	IP string
	// Port is the TCP port the zone gRPC listener (and the legacy kRO
	// map-server listener) is bound to.
	Port int
}

// TransitConfigSource supplies the target-zone TCP endpoint for a
// given zone ID. In production this resolves to a per-pod config
// record (env-derived, Agones-annotated, or a small in-memory table).
// Tests inject a fake.
type TransitConfigSource interface {
	// EndpointFor returns the TCP endpoint for the given zone ID.
	// Returns (zero-value, false) when the zone ID is unknown.
	EndpointFor(zoneID string) (TransitEndpoint, bool)
}
