package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	migratedb "github.com/golang-migrate/migrate/v4/database"
	"github.com/golang-migrate/migrate/v4/database/mysql"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/bouroo/goAthena/internal/config"
	"github.com/bouroo/goAthena/internal/infrastructure/db/migrations"
)

// Migrator applies the embedded schema to a live database. It is engine-aware:
// it selects the migration subdirectory matching the driver (mariadb/ or
// postgres/) so each engine gets DDL in its own dialect.
type Migrator struct {
	m *migrate.Migrate
}

// NewMigrator opens a dedicated *sql.DB from cfg and builds a Migrator. The
// migration source roots at the driver-specific subdirectory (mariadb/ or
// postgres/) inside the embedded FS.
func NewMigrator(cfg config.DBConfig) (*Migrator, error) {
	drv := normalize(cfg.Driver)
	database, raw, err := openMigrateDB(cfg)
	if err != nil {
		return nil, err
	}
	defer raw.Close()

	src, err := iofs.New(migrations.FS, drv)
	if err != nil {
		return nil, fmt.Errorf("migration source (%s): %w", drv, err)
	}
	m, err := migrate.NewWithInstance("iofs", src, drv, database)
	if err != nil {
		return nil, fmt.Errorf("migrate init: %w", err)
	}
	return &Migrator{m: m}, nil
}

// Up applies all pending migrations. migrate.ErrNoChange is not an error here.
func (mg *Migrator) Up() error {
	if err := mg.m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}

// Down rolls back all migrations. ErrNoChange is not an error here.
func (mg *Migrator) Down() error {
	if err := mg.m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate down: %w", err)
	}
	return nil
}

// Steps applies n migrations forward (n>0) or backward (n<0).
func (mg *Migrator) Steps(n int) error {
	if err := mg.m.Steps(n); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate steps %d: %w", n, err)
	}
	return nil
}

// Version reports the current schema version and dirty state.
func (mg *Migrator) Version() (uint, bool, error) {
	v, dirty, err := mg.m.Version()
	if err != nil {
		if errors.Is(err, migrate.ErrNilVersion) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("migrate version: %w", err)
	}
	return v, dirty, nil
}

// Close releases the migrate handles.
func (mg *Migrator) Close() error {
	srcErr, dbErr := mg.m.Close()
	return errors.Join(srcErr, dbErr)
}

// Force sets the schema version (unblocks a dirty state).
func (mg *Migrator) Force(v int) error {
	if err := mg.m.Force(v); err != nil {
		return fmt.Errorf("migrate force %d: %w", v, err)
	}
	return nil
}

// openMigrateDB dials a dedicated connection and wraps it in the engine's
// migrate driver. golang-migrate needs its own *sql.DB (not GORM's).
func openMigrateDB(cfg config.DBConfig) (migratedb.Driver, *sql.DB, error) {
	drv := normalize(cfg.Driver)
	dsn := ""
	if drv == "mariadb" {
		dsn = dsnMariaDB(cfg)
	} else {
		dsn = dsnPostgres(cfg)
	}

	raw, err := sql.Open(sqlDriverName(drv), dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("open migrate db: %w", err)
	}
	if err := raw.PingContext(context.Background()); err != nil {
		_ = raw.Close()
		return nil, nil, fmt.Errorf("ping migrate db: %w", err)
	}

	var d migratedb.Driver
	switch drv {
	case "mariadb":
		d, err = mysql.WithInstance(raw, &mysql.Config{})
	case "postgres":
		d, err = postgres.WithInstance(raw, &postgres.Config{})
	}
	if err != nil {
		_ = raw.Close()
		return nil, nil, fmt.Errorf("migrate driver: %w", err)
	}
	return d, raw, nil
}

func sqlDriverName(drv string) string {
	switch drv {
	case "postgres":
		return "postgres"
	default:
		return "mysql"
	}
}
