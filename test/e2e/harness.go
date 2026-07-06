//go:build e2e

// Package e2e contains end-to-end tests that exercise the full goAthena
// player journey across all three services: identity (login, character
// roster, warehouse lock), zone (map enter, movement), and the NATS-backed
// cross-zone transit handshake.
//
// These tests require the full cluster to be running (docker compose up
// or kustomize deploy) and are gated by the `e2e` build tag so plain
// `go test ./...` is a no-op for this package. NewE2EHarness performs a
// short reachability probe against every dependency and calls t.Skip()
// when one is missing, so the suite degrades gracefully on hosts without
// the cluster.
package e2e

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	// mysql driver registration — the database/sql package requires a
	// side-effect import to register the "mysql" driver name.
	_ "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	valkeygo "github.com/valkey-io/valkey-go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"

	identityv1 "github.com/bouroo/goAthena/api/pb/identity/v1"
	zonev1 "github.com/bouroo/goAthena/api/pb/zone/v1"
	natsinfra "github.com/bouroo/goAthena/internal/infrastructure/messaging/nats"
)

// Default service endpoints when running against the canonical
// docker-compose cluster. Override with the matching environment
// variables for staging or remote runs.
const (
	defaultGatewayTCPAddr = "localhost:6900"
	defaultGatewayWSAddr  = "localhost:6901"
	defaultIdentityHTTP   = "localhost:8080"
	defaultIdentityGRPC   = "localhost:50051"
	defaultZoneGRPC       = "localhost:7121"
	defaultNATSURL        = "nats://localhost:4222"
	defaultValkeyAddr     = "localhost:6379"
	defaultDBDSNTemplate  = "goathena:goathena@tcp(127.0.0.1:3306)/goathena?parseTime=true&charset=utf8mb4&loc=UTC&multiStatements=true"
	defaultProbeTimeout   = 3 * time.Second
	defaultConnectTimeout = 5 * time.Second
	defaultTestTimeout    = 30 * time.Second
	defaultCloseTimeout   = 5 * time.Second
)

// E2EConfig holds the connection info for the running cluster. All
// fields are populated by LoadE2EConfig and may be overridden by
// environment variables for non-default topologies. The stuttering
// name is intentional; it mirrors the public spec given to the e2e
// suite (D30).
//
//nolint:revive
type E2EConfig struct {
	GatewayTCPAddr   string
	GatewayWSAddr    string
	IdentityHTTPAddr string
	IdentityGRPCAddr string
	ZoneGRPCAddr     string
	NATSURL          string
	ValkeyAddr       string
	DBConnString     string
}

// LoadE2EConfig reads connection info from environment variables,
// falling back to the docker-compose defaults when unset. The function
// is pure — it never opens sockets — so it is safe to call outside of
// tests for configuration logging.
func LoadE2EConfig() E2EConfig {
	return E2EConfig{
		GatewayTCPAddr:   envOr("E2E_GATEWAY_TCP_ADDR", defaultGatewayTCPAddr),
		GatewayWSAddr:    envOr("E2E_GATEWAY_WS_ADDR", defaultGatewayWSAddr),
		IdentityHTTPAddr: envOr("E2E_IDENTITY_HTTP_ADDR", defaultIdentityHTTP),
		IdentityGRPCAddr: envOr("E2E_IDENTITY_GRPC_ADDR", defaultIdentityGRPC),
		ZoneGRPCAddr:     envOr("E2E_ZONE_GRPC_ADDR", defaultZoneGRPC),
		NATSURL:          envOr("E2E_NATS_URL", defaultNATSURL),
		ValkeyAddr:       envOr("E2E_VALKEY_ADDR", defaultValkeyAddr),
		DBConnString:     envOr("E2E_DB_DSN", defaultDBDSNTemplate),
	}
}

// E2EHarness provides connected clients to the running cluster. The
// zero value is not useful; obtain one via NewE2EHarness. The harness
// is safe for concurrent use by subtests in the same test function;
// cross-test sharing requires care because the underlying connections
// hold stateful resources. The stuttering name is intentional; it
// mirrors the public spec given to the e2e suite (D30).
//
//nolint:revive
type E2EHarness struct {
	Config         E2EConfig
	IdentityConn   *grpc.ClientConn
	ZoneConn       *grpc.ClientConn
	IdentityClient identityv1.IdentityServiceClient
	ZoneClient     zonev1.ZoneServiceClient
	NATSClient     *natsinfra.Client
	ValkeyClient   valkeygo.Client
	DB             *sql.DB
	Logger         *zerolog.Logger
}

// NewE2EHarness connects to every service declared in cfg, returning a
// ready-to-use harness. Each dependency is probed with a short timeout;
// if any probe fails the test is skipped (with a clear message) so the
// suite can run on hosts that have only a subset of services. All
// connections are released by t.Cleanup.
func NewE2EHarness(t *testing.T) *E2EHarness {
	t.Helper()

	cfg := LoadE2EConfig()
	logger := newTestLogger(t)

	h := &E2EHarness{Config: cfg, Logger: logger}

	probeCtx, cancel := context.WithTimeout(context.Background(), defaultProbeTimeout)
	defer cancel()

	var err error

	h.IdentityConn, err = grpc.NewClient(cfg.IdentityGRPCAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Skipf("e2e: dial identity gRPC at %s: %v", cfg.IdentityGRPCAddr, err)
	}
	t.Cleanup(func() { _ = closeGRPC(h.IdentityConn) })

	if err := healthProbe(probeCtx, h.IdentityConn); err != nil {
		t.Skipf("e2e: identity gRPC unreachable at %s: %v", cfg.IdentityGRPCAddr, err)
	}
	h.IdentityClient = identityv1.NewIdentityServiceClient(h.IdentityConn)

	h.ZoneConn, err = grpc.NewClient(cfg.ZoneGRPCAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Skipf("e2e: dial zone gRPC at %s: %v", cfg.ZoneGRPCAddr, err)
	}
	t.Cleanup(func() { _ = closeGRPC(h.ZoneConn) })

	if err := healthProbe(probeCtx, h.ZoneConn); err != nil {
		t.Skipf("e2e: zone gRPC unreachable at %s: %v", cfg.ZoneGRPCAddr, err)
	}
	h.ZoneClient = zonev1.NewZoneServiceClient(h.ZoneConn)

	h.DB, err = sql.Open("mysql", cfg.DBConnString)
	if err != nil {
		t.Skipf("e2e: open DB driver: %v", err)
	}
	t.Cleanup(func() { _ = h.DB.Close() })
	pingCtx, pingCancel := context.WithTimeout(context.Background(), defaultProbeTimeout)
	if err := h.DB.PingContext(pingCtx); err != nil {
		pingCancel()
		t.Skipf("e2e: DB unreachable at %s: %v", cfg.DBConnString, err)
	}
	pingCancel()

	h.ValkeyClient, err = valkeygo.NewClient(valkeygo.ClientOption{
		InitAddress: []string{cfg.ValkeyAddr},
	})
	if err != nil {
		t.Skipf("e2e: valkey client at %s: %v", cfg.ValkeyAddr, err)
	}
	t.Cleanup(func() { h.ValkeyClient.Close() })
	valkeyCtx, valkeyCancel := context.WithTimeout(context.Background(), defaultProbeTimeout)
	if err := h.ValkeyClient.Do(valkeyCtx, h.ValkeyClient.B().Ping().Build()).Error(); err != nil {
		valkeyCancel()
		t.Skipf("e2e: valkey unreachable at %s: %v", cfg.ValkeyAddr, err)
	}
	valkeyCancel()

	natsCtx, natsCancel := context.WithTimeout(context.Background(), defaultConnectTimeout)
	h.NATSClient, err = natsinfra.New(natsCtx, cfg.NATSURL, defaultConnectTimeout, logger)
	natsCancel()
	if err != nil {
		t.Skipf("e2e: NATS unreachable at %s: %v", cfg.NATSURL, err)
	}
	t.Cleanup(func() { _ = h.NATSClient.Close() })

	return h
}

// Cleanup is a no-op kept for forward compatibility — t.Cleanup
// registered in NewE2EHarness handles per-test teardown automatically.
// Tests that create persistent fixtures (DB rows, Valkey keys) should
// register their own cleanup via t.Cleanup at the call site.
func (h *E2EHarness) Cleanup(_ *testing.T) {}

// UniqueUserID returns a fresh 23-char-or-fewer userid suitable for the
// rAthena `login.userid` column (varchar(23)). The format is
// "e2e_<8 hex>" which is collision-resistant across parallel runs.
func UniqueUserID() string {
	return "e2e_" + uuid.NewString()[:8]
}

// UniqueCharName returns a fresh <=24-char character name suitable for
// the rAthena `char.name` column (varchar(24)). The format is
// "c<8 hex>" so it fits well under the cap.
func UniqueCharName() string {
	return "c" + uuid.NewString()[:8]
}

// TestContext returns a context bounded by defaultTestTimeout — the
// canonical per-test deadline for the E2E suite. Callers may apply
// tighter deadlines on top.
func TestContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	t.Cleanup(cancel)
	return ctx
}

// healthProbe calls the gRPC Health service on conn with ctx and
// returns nil only when the service reports SERVING. Other states
// (NOT_SERVING, UNKNOWN) are surfaced as errors so the harness can
// skip with a meaningful message.
func healthProbe(ctx context.Context, conn *grpc.ClientConn) error {
	client := grpc_health_v1.NewHealthClient(conn)
	resp, err := client.Check(ctx, &grpc_health_v1.HealthCheckRequest{}, grpc.WaitForReady(true))
	if err != nil {
		return fmt.Errorf("health probe: %w", err)
	}
	if resp.GetStatus() != grpc_health_v1.HealthCheckResponse_SERVING {
		return fmt.Errorf("health status=%s, want SERVING", resp.GetStatus())
	}
	return nil
}

// closeGRPC performs a bounded graceful close. Returns the error from
// Close so the caller can log it; the test harness always ignores the
// return value because the connection is being torn down at process end.
func closeGRPC(conn *grpc.ClientConn) error {
	ctx, cancel := context.WithTimeout(context.Background(), defaultCloseTimeout)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- conn.Close() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("grpc close: %w", ctx.Err())
	}
}

// envOr returns the value of key from the process environment, or def
// when the variable is unset or empty.
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// newTestLogger builds a zerolog logger that writes to stderr with the
// test name attached. The logger is shared with subtests via the
// returned pointer.
func newTestLogger(t *testing.T) *zerolog.Logger {
	t.Helper()
	writer := zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339}
	l := zerolog.New(writer).With().
		Timestamp().
		Str("test", t.Name()).
		Logger()
	return &l
}
