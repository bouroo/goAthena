// Package testdb provisions throwaway database containers for GORM integration
// tests. It starts a MariaDB or PostgreSQL instance through testcontainers,
// applies goAthena's embedded rAthena schema migrations, and exports the live
// connection through the DB_* environment variables the existing integration
// tests already read (testDB()). That keeps the per-test connection code
// unchanged: the suite simply gains a real engine behind it.
//
// Lifecycle is owned by the test binary's TestMain so a single container backs
// the whole package:
//
//	func TestMain(m *testing.M) {
//	    if err := testdb.Setup("mariadb"); err != nil {
//	        fmt.Println(err)
//	        os.Exit(1)
//	    }
//	    code := m.Run()
//	    testdb.Terminate()
//	    os.Exit(code)
//	}
package testdb

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/bouroo/goAthena/internal/config"
	"github.com/bouroo/goAthena/internal/infrastructure/db"
)

const (
	// Single credentials/database for the throwaway container; nothing persists.
	dbName     = "goathena"
	dbUser     = "goathena"
	dbPassword = "goathena"

	startupTimeout = 90 * time.Second
)

// engineSpec describes how to launch and readiness-detect one DB engine.
type engineSpec struct {
	image   string
	env     map[string]string
	port    string // container port to publish ("<n>/tcp")
	waitLog string // log substring marking ready-for-connections
}

func specFor(driver string) (engineSpec, error) {
	switch normalize(driver) {
	case "mariadb":
		return engineSpec{
			image: "mariadb:11.8",
			env: map[string]string{
				"MARIADB_ROOT_PASSWORD": dbPassword,
				"MARIADB_DATABASE":      dbName,
				"MARIADB_USER":          dbUser,
				"MARIADB_PASSWORD":      dbPassword,
			},
			port:    "3306/tcp",
			waitLog: "ready for connections",
		}, nil
	case "postgres":
		return engineSpec{
			image: "postgres:18",
			env: map[string]string{
				"POSTGRES_PASSWORD": dbPassword,
				"POSTGRES_DB":       dbName,
				"POSTGRES_USER":     dbUser,
			},
			port:    "5432/tcp",
			waitLog: "database system is ready to accept connections",
		}, nil
	default:
		return engineSpec{}, fmt.Errorf("testdb: unsupported driver %q (want mariadb|postgres)", driver)
	}
}

var (
	once      sync.Once
	container testcontainers.Container
	cfg       config.DBConfig
	setupErr  error
)

// Setup starts the DB container for driver, applies the embedded migrations,
// and publishes the connection through the DB_* env vars. The first call does
// the work; later calls are no-ops returning the stored result. The caller must
// call Terminate once tests finish to release the container.
func Setup(driver string) (config.DBConfig, error) {
	once.Do(func() { cfg, container, setupErr = setup(driver) })
	return cfg, setupErr
}

// Terminate stops the container started by Setup. Safe to call when Setup was
// never invoked or failed (it is then a no-op).
func Terminate() {
	if container == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = container.Terminate(ctx) // best-effort: container is throwaway
}

func setup(driver string) (config.DBConfig, testcontainers.Container, error) {
	// Rootless podman cannot run the Ryuk reaper; disable it before the first
	// testcontainers call so container start doesn't wait on a reaper it can't
	// launch. The reaper is created lazily on first container start.
	_ = os.Setenv("TESTCONTAINERS_RYUK_DISABLED", "true")

	norm := normalize(driver)
	spec, err := specFor(norm)
	if err != nil {
		return config.DBConfig{}, nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        spec.image,
			Env:          spec.env,
			ExposedPorts: []string{spec.port},
			WaitingFor:   wait.ForLog(spec.waitLog).WithStartupTimeout(startupTimeout),
		},
		Started: true,
	})
	if err != nil {
		return config.DBConfig{}, nil, fmt.Errorf("testdb: start %s container: %w", norm, err)
	}

	host, err := ctr.Host(ctx)
	if err != nil {
		_ = ctr.Terminate(context.Background())
		return config.DBConfig{}, nil, fmt.Errorf("testdb: container host: %w", err)
	}
	mapped, err := ctr.MappedPort(ctx, spec.port)
	if err != nil {
		_ = ctr.Terminate(context.Background())
		return config.DBConfig{}, nil, fmt.Errorf("testdb: mapped port: %w", err)
	}

	dbCfg := config.DBConfig{
		Driver:   norm,
		Host:     host,
		Port:     int(mapped.Num()),
		Name:     dbName,
		User:     dbUser,
		Password: dbPassword,
		SSLMode:  "disable",
	}

	if err := migrate(ctx, dbCfg); err != nil {
		_ = ctr.Terminate(context.Background())
		return config.DBConfig{}, nil, err
	}

	// Export so the existing testDB() helpers (which read these vars) connect to
	// the container without any per-test wiring.
	setEnv(dbCfg)

	return dbCfg, ctr, nil
}

// migrate applies the embedded schema. A short retry tolerates a startup race
// where the readiness log precedes the engine truly accepting connections.
func migrate(ctx context.Context, dbCfg config.DBConfig) error {
	const attempts = 6
	var lastErr error
	for i := 0; i < attempts; i++ {
		lastErr = migrateOnce(dbCfg)
		if lastErr == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("testdb: migrate cancelled: %w", lastErr)
		case <-time.After(2 * time.Second):
		}
	}
	return fmt.Errorf("testdb: migrate after %d attempts: %w", attempts, lastErr)
}

// migrateOnce opens a dedicated migrator connection, applies pending schema,
// and closes it. Each retry gets a fresh connection.
func migrateOnce(dbCfg config.DBConfig) error {
	mg, err := db.NewMigrator(dbCfg)
	if err != nil {
		return fmt.Errorf("testdb: open migrator: %w", err)
	}
	defer func() { _ = mg.Close() }() // throwaway connection; close failure is harmless
	if err := mg.Up(); err != nil {
		return fmt.Errorf("testdb: migrate up: %w", err)
	}
	return nil
}

func setEnv(c config.DBConfig) {
	_ = os.Setenv("DB_DRIVER", c.Driver)
	_ = os.Setenv("DB_HOST", c.Host)
	_ = os.Setenv("DB_PORT", strconv.Itoa(c.Port))
	_ = os.Setenv("DB_NAME", c.Name)
	_ = os.Setenv("DB_USER", c.User)
	_ = os.Setenv("DB_PASSWORD", c.Password)
}

// normalize folds the historical aliases mysql→mariadb and postgresql→postgres
// onto the two engines goAthena ships migrations for.
func normalize(driver string) string {
	switch driver {
	case "", "mysql":
		return "mariadb"
	case "postgresql":
		return "postgres"
	default:
		return driver
	}
}
