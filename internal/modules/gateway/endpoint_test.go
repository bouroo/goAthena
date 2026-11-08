//go:build unit

package gateway

import (
	"context"
	"errors"
	"testing"

	"github.com/samber/do/v2"

	"github.com/bouroo/goAthena/internal/config"
)

// stubResolver answers with a fixed endpoint or a fixed error.
type stubResolver struct {
	endpoint MapEndpoint
	err      error
}

func (s stubResolver) ResolveMapEndpoint(context.Context) (MapEndpoint, error) {
	return s.endpoint, s.err
}

// testConfig is a configuration with a known map host and port, so the
// assertions can tell the configured endpoint from the resolved one.
func testConfig() config.Config {
	var cfg config.Config
	cfg.Gateway.MapHost = "10.9.9.9"
	cfg.Gateway.MapPort = 5121
	return cfg
}

// TestResolveMapEndpointFallsBackToTheConfiguredEndpoint proves that a
// process with no resolver registered advertises the configured endpoint —
// what a local run wants.
func TestResolveMapEndpointFallsBackToTheConfiguredEndpoint(t *testing.T) {
	inj := do.New()
	endpoint, err := ResolveMapEndpoint(context.Background(), inj, testConfig())
	if err != nil {
		t.Fatalf("resolveMapEndpoint: %v", err)
	}
	if endpoint.Host != "10.9.9.9" {
		t.Errorf("host = %q, want 10.9.9.9 (the configured host)", endpoint.Host)
	}
	if endpoint.Port != 5121 {
		t.Errorf("port = %d, want 5121 (the configured port)", endpoint.Port)
	}
}

// TestResolveMapEndpointPrefersTheResolver proves the injected resolver
// wins over the configuration: inside a fleet the configured host is not
// the shard's address.
func TestResolveMapEndpointPrefersTheResolver(t *testing.T) {
	inj := do.New()
	RegisterMapEndpoint(inj, stubResolver{endpoint: MapEndpoint{Host: "10.0.0.7", Port: 31234}})

	endpoint, err := ResolveMapEndpoint(context.Background(), inj, testConfig())
	if err != nil {
		t.Fatalf("resolveMapEndpoint: %v", err)
	}
	if endpoint.Host != "10.0.0.7" {
		t.Errorf("host = %q, want 10.0.0.7 (the resolver's host)", endpoint.Host)
	}
	if endpoint.Port != 31234 {
		t.Errorf("port = %d, want 31234 (the resolver's port)", endpoint.Port)
	}
}

// TestResolveMapEndpointRejectsAnUnusableAnswer proves the resolver cannot
// hand back an endpoint that would drop every client: an empty host or a
// zero port is a failure, not an endpoint.
func TestResolveMapEndpointRejectsAnUnusableAnswer(t *testing.T) {
	cases := []struct {
		name string
		end  MapEndpoint
	}{
		{"empty host", MapEndpoint{Host: "", Port: 5121}},
		{"zero port", MapEndpoint{Host: "10.0.0.7", Port: 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inj := do.New()
			RegisterMapEndpoint(inj, stubResolver{endpoint: tc.end})
			if _, err := ResolveMapEndpoint(context.Background(), inj, testConfig()); err == nil {
				t.Fatal("resolveMapEndpoint = nil error, want a rejection")
			}
		})
	}
}

// TestResolveMapEndpointPropagatesTheResolverError proves a failing
// resolver is a failure, not a silent fall back to the configured address:
// a fleet shard that cannot say where it is must not advertise an address
// that is not its own.
func TestResolveMapEndpointPropagatesTheResolverError(t *testing.T) {
	inj := do.New()
	RegisterMapEndpoint(inj, stubResolver{err: errors.New("sidecar unreachable")})
	_, err := ResolveMapEndpoint(context.Background(), inj, testConfig())
	if err == nil {
		t.Fatal("resolveMapEndpoint = nil error, want the resolver's error")
	}
}

// TestStaticMapEndpointReturnsTheConfiguredEndpoint proves the static
// resolver is the configuration, unchanged.
func TestStaticMapEndpointReturnsTheConfiguredEndpoint(t *testing.T) {
	want := MapEndpoint{Host: "10.9.9.9", Port: 5121}
	got, err := StaticMapEndpoint{Endpoint: want}.ResolveMapEndpoint(context.Background())
	if err != nil {
		t.Fatalf("ResolveMapEndpoint: %v", err)
	}
	if got != want {
		t.Errorf("endpoint = %+v, want %+v", got, want)
	}
}
