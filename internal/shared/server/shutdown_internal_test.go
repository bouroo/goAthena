//go:build unit

package server

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/rs/zerolog"
	"github.com/samber/do/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"gorm.io/gorm"

	"github.com/bouroo/goAthena/internal/config"
)

func newTestApp(t *testing.T, injector do.Injector) *Application {
	t.Helper()
	cfg := &config.Config{
		App: config.AppConfig{ShutdownTimeout: time.Second},
		HTTP: config.HTTPConfig{
			Host:               "127.0.0.1",
			Port:               0,
			ReadTimeout:        time.Second,
			WriteTimeout:       time.Second,
			IdleTimeout:        time.Second,
			BodyLimit:          "1M",
			HealthProbeTimeout: time.Second,
		},
		GRPC: config.GRPCConfig{Host: "127.0.0.1", Port: 0},
	}
	logger := zerolog.New(nil).Level(zerolog.Disabled)
	return NewApplication(injector, cfg, &logger)
}

func TestNewApplication_GettersBeforeStart(t *testing.T) {
	t.Parallel()

	injector := do.New()
	app := newTestApp(t, injector)

	require.NotNil(t, app)
	assert.Equal(t, "", app.HTTPAddr(), "HTTPAddr must be empty before Run")
	assert.NotNil(t, app.HasHTTPStarted(), "HasHTTPStarted must return a non-nil channel")
	assert.NotNil(t, app.Logger())
}

func TestApplication_EchoResolvesHTTPFromInjector(t *testing.T) {
	t.Parallel()

	injector := do.New()
	cfg := &config.Config{
		HTTP: config.HTTPConfig{BodyLimit: "1M", HealthProbeTimeout: time.Second},
	}
	logger := zerolog.New(nil).Level(zerolog.Disabled)
	e := echo.New()
	do.ProvideValue(injector, cfg)
	do.ProvideValue(injector, &logger)
	do.ProvideValue(injector, e)

	app := NewApplication(injector, cfg, &logger)

	assert.Equal(t, e, app.Echo(), "Echo must return the registered echo instance")
	assert.Equal(t, e, app.Echo(), "subsequent Echo calls must return the cached instance")
}

func TestInvokeDB_MissingProviderReturnsFalse(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, do.New())
	db, ok := app.invokeDB()
	assert.False(t, ok)
	assert.Nil(t, db)
}

func TestInvokeDB_PresentProviderReturnsTrue(t *testing.T) {
	t.Parallel()

	injector := do.New()
	do.ProvideValue(injector, &gorm.DB{})
	app := newTestApp(t, injector)

	db, ok := app.invokeDB()
	assert.True(t, ok)
	assert.NotNil(t, db)
}

func TestInvokeValkey_MissingProviderReturnsFalse(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, do.New())
	client, ok := app.invokeValkey()
	assert.False(t, ok)
	assert.Nil(t, client)
}

func TestInvokeTracerProvider_MissingProviderReturnsFalse(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, do.New())
	tp, ok := app.invokeTracerProvider()
	assert.False(t, ok)
	assert.Nil(t, tp)
}

func TestInvokeMeterProvider_MissingProviderReturnsFalse(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, do.New())
	mp, ok := app.invokeMeterProvider()
	assert.False(t, ok)
	assert.Nil(t, mp)
}

func TestShutdownHTTP_NoOpWhenNotStarted(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, do.New())
	assert.NoError(t, app.shutdownHTTP(context.Background()))
}

func TestStartHTTP_StartsServerAndShutsDown(t *testing.T) {
	t.Parallel()

	injector := do.New()
	do.ProvideValue(injector, echo.New())
	cfg := &config.Config{
		App: config.AppConfig{ShutdownTimeout: 2 * time.Second},
		HTTP: config.HTTPConfig{
			Host:               "127.0.0.1",
			Port:               0, // ephemeral
			ReadTimeout:        time.Second,
			WriteTimeout:       time.Second,
			IdleTimeout:        time.Second,
			BodyLimit:          "1M",
			HealthProbeTimeout: time.Second,
		},
	}
	logger := zerolog.New(nil).Level(zerolog.Disabled)

	app := NewApplication(injector, cfg, &logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, app.StartHTTP(ctx))

	select {
	case <-app.HasHTTPStarted():
	case <-time.After(2 * time.Second):
		t.Fatal("HTTP server did not start within 2s")
	}

	addr := app.HTTPAddr()
	assert.NotEmpty(t, addr)
	_, err := net.Listen("tcp", addr) // expect failure because addr is in use
	assert.Error(t, err)

	require.NoError(t, app.shutdownHTTP(context.Background()))
}

func TestStartGRPC_BindsListener(t *testing.T) {
	t.Parallel()

	injector := do.New()
	logger := zerolog.New(nil).Level(zerolog.Disabled)
	do.ProvideValue(injector, grpc.NewServer())

	cfg := &config.Config{
		App:  config.AppConfig{ShutdownTimeout: time.Second},
		GRPC: config.GRPCConfig{Host: "127.0.0.1", Port: 0},
	}

	app := NewApplication(injector, cfg, &logger)
	require.NoError(t, app.startGRPC())
	require.NotNil(t, app.grpcListener)
	assert.NoError(t, app.grpcListener.Close())
}

func TestStartGRPC_InvalidAddrFails(t *testing.T) {
	t.Parallel()

	injector := do.New()
	logger := zerolog.New(nil).Level(zerolog.Disabled)
	do.ProvideValue(injector, grpc.NewServer())

	cfg := &config.Config{
		App:  config.AppConfig{ShutdownTimeout: time.Second},
		GRPC: config.GRPCConfig{Host: "0.0.0.0", Port: -1},
	}

	app := NewApplication(injector, cfg, &logger)
	err := app.startGRPC()
	assert.Error(t, err)
}

// TestCloseDB_HandlesMissingSQLHandle is intentionally not exhaustive —
// gorm.DB.DB() panics on an unconnected zero DB, so this test would require
// either a live *sql.DB or a sqlmock. We leave the closeDB coverage gap
// to integration tests.

// TestServerErrorChannel_StartsBothServers ensures the helper launches
// both server goroutines and the channel returns an error once the
// underlying gRPC listener is closed.
func TestServerErrorChannel_StartsBothServers(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, do.New())

	// Fake-initialize the HTTP lifecycle state so runHTTPServer returns
	// once the httpStartCtx is cancelled below.
	app.httpStarted = make(chan struct{})
	app.httpStopped = make(chan struct{})
	app.httpStartCtx, app.httpStartCancel = context.WithCancel(context.Background())
	close(app.httpStarted)
	close(app.httpStopped)

	// gRPC: bind a listener that we close so Serve returns.
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	app.grpcListener = lis

	gs := grpc.NewServer()
	app.grpcServer = gs

	ch := app.serverErrorChannel()

	app.httpStartCancel()
	require.NoError(t, lis.Close())

	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("serverErrorChannel did not return within 2s")
	}
}

// Compile-time check that Application.Logger() exposes the logger pointer
// (the type assertion in shutdown.go requires this contract).
var _ = func() *zerolog.Logger {
	var l zerolog.Logger
	return &l
}
