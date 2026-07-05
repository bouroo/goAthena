//go:build unit

package db_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bouroo/goAthena/internal/config"
	"github.com/bouroo/goAthena/internal/infrastructure/db"
)

// driverForTest returns the driver selected by DB_DRIVER env, defaulting to
// "mariadb" since that is the project's primary DB.
func driverForTest(t *testing.T) string {
	t.Helper()
	d := os.Getenv("DB_DRIVER")
	if d == "" {
		return "mariadb"
	}
	return d
}

// TestNewDB_NilConfig verifies that NewDB rejects a nil config without
// attempting to parse a DSN or open a connection.
func TestNewDB_NilConfig(t *testing.T) {
	t.Parallel()

	nop := zerolog.Nop()
	gormDB, err := db.NewDB(context.Background(), nil, &nop)
	require.Error(t, err, "expected an error for a nil config")
	assert.Nil(t, gormDB, "gorm.DB must be nil when config is nil")
	assert.Contains(t, err.Error(), "config is nil", "error should mention nil config")
}

// TestNewDB_NilLogger verifies that NewDB rejects a nil logger.
func TestNewDB_NilLogger(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		DB: config.DBConfig{
			Driver:         driverForTest(t),
			Host:           "192.0.2.1", // TEST-NET-1, should not be reachable
			Port:           3306,
			Name:           "app",
			User:           "app",
			Password:       "app",
			SSLMode:        "false",
			MaxConns:       2,
			MaxIdleConns:   1,
			MaxConnIdle:    5 * time.Second,
			MaxConnLife:    10 * time.Second,
			ConnectTimeout: 1 * time.Second,
		},
	}

	gormDB, err := db.NewDB(context.Background(), cfg, nil)
	require.Error(t, err, "expected an error for a nil logger")
	assert.Nil(t, gormDB, "gorm.DB must be nil when logger is nil")
	assert.Contains(t, err.Error(), "logger is nil", "error should mention nil logger")
}

// TestNewDB_UnsupportedDriver verifies that NewDB rejects an unknown driver
// before touching the network.
func TestNewDB_UnsupportedDriver(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		DB: config.DBConfig{
			Driver: "sqlite",
			Host:   "127.0.0.1",
			Port:   3306,
			Name:   "app",
			User:   "u",
		},
	}
	nop := zerolog.Nop()

	gormDB, err := db.NewDB(context.Background(), cfg, &nop)
	require.Error(t, err)
	assert.Nil(t, gormDB)
	assert.Contains(t, err.Error(), "unsupported db driver")
}

// TestNewDB_UnreachableHost verifies that NewDB fails when the configured host
// is unreachable and that no usable *gorm.DB is returned. Each driver may fail
// at gorm.Open (eager dial) or at PingContext, depending on driver version and
// timeout interaction. Either failure path is acceptable — what matters is the
// caller sees a wrapped error and a nil *gorm.DB so the connection pool is not
// leaked.
func TestNewDB_UnreachableHost(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		DB: config.DBConfig{
			Driver:         driverForTest(t),
			Host:           "192.0.2.1", // TEST-NET-1, should not be reachable
			Port:           3306,
			Name:           "app",
			User:           "app",
			Password:       "app",
			SSLMode:        "false",
			MaxConns:       2,
			MaxIdleConns:   1,
			MaxConnIdle:    5 * time.Second,
			MaxConnLife:    10 * time.Second,
			ConnectTimeout: 1 * time.Second,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	nop := zerolog.Nop()
	gormDB, err := db.NewDB(ctx, cfg, &nop)
	require.Error(t, err, "expected an error when the host is unreachable")
	assert.Nil(t, gormDB, "gorm.DB must be nil on connection failure")
	msg := err.Error()
	if !assert.True(t,
		strings.Contains(msg, "ping db") || strings.Contains(msg, "open gorm"),
		"error should describe either ping or open failure; got: %s", msg,
	) {
		return
	}
	if gormDB != nil {
		assert.Fail(t, "gorm.DB should be nil on failure")
	}
}
