// Package agones wraps the Agones Go SDK for GameServer lifecycle
// management (Ready, Allocate, Shutdown, Health).
//
// In production the zone service runs as an Agones GameServer pod; the Agones
// sidecar is reached via localhost:9357 (overridable via AGONES_SDK_GRPC_HOST
// and AGONES_SDK_GRPC_PORT). In dev/CI the sidecar is not available; the
// local no-op Lifecycle is returned by New so callers can run the zone without
// Agones infrastructure.
package agones
