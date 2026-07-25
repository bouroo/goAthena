package db

import (
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/mysql"    // registers the "mysql://" DSN scheme
	_ "github.com/golang-migrate/migrate/v4/database/postgres" // registers the "postgres://" DSN scheme
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/bouroo/goAthena/internal/config"
	"github.com/bouroo/goAthena/internal/infrastructure/db/migrations"
)

// RunMigrate applies the SQL migrations embedded in package migrations to the
// database named by cfg, using golang-migrate with the iofs source driver. The
// database engine is auto-detected from the DSN scheme: "mysql://" for MariaDB
// and "postgres://" for PostgreSQL. args selects the command. It returns a
// process exit code (0 success, 1 failure) so the caller can pass it straight
// to os.Exit.
func RunMigrate(cfg *config.Config, args []string) (exitCode int) {
	cmd, count, forceVersion, err := parseMigrateArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		printMigrateUsage()
		return 1
	}

	m, err := newMigrator(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create migrator: %v\n", err)
		return 1
	}
	defer func() {
		// Close returns separate source/database errors; both must be surfaced so
		// a half-closed migrator is never reported as clean. A failed close after
		// an otherwise-successful run is itself a failure, so it overrides a zero
		// exit code via the named return.
		srcErr, dbErr := m.Close()
		if srcErr != nil || dbErr != nil {
			fmt.Fprintf(os.Stderr, "failed to close migrator: source=%v db=%v\n", srcErr, dbErr)
			if exitCode == 0 {
				exitCode = 1
			}
		}
	}()

	switch cmd {
	case "up":
		return runMigrateUp(m)
	case "down":
		return runMigrateDown(m, count)
	case "force":
		return runMigrateForce(m, forceVersion)
	case "version":
		return runMigrateVersion(m)
	default:
		fmt.Fprintf(os.Stderr, "unknown migrate command: %s\n", cmd)
		printMigrateUsage()
		return 1
	}
}

// newMigrator builds a golang-migrate instance backed by the SQL files embedded
// in package migrations.
func newMigrator(cfg *config.Config) (*migrate.Migrate, error) {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return nil, fmt.Errorf("create migration source: %w", err)
	}

	m, err := migrate.NewWithSourceInstance("iofs", src, migratorDSN(cfg))
	if err != nil {
		return nil, fmt.Errorf("create migrator: %w", err)
	}

	return m, nil
}

// migratorDSN converts the application DSN to the form golang-migrate expects.
// The library auto-detects the database type from the URL scheme ("postgres://"
// for PostgreSQL, "mysql://" for MariaDB). The MariaDB application DSN is a
// go-sql-driver/mysql DSN, so it is wrapped with the "mysql://" scheme.
func migratorDSN(cfg *config.Config) string {
	if cfg.DB.Driver == DriverMariaDB {
		return "mysql://" + cfg.DBConnString()
	}
	return cfg.DBConnString()
}

func runMigrateUp(m *migrate.Migrate) int {
	// ErrNoChange means the schema is already at the latest version, which is
	// a successful no-op rather than an error.
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		fmt.Fprintf(os.Stderr, "migration up failed: %v\n", err)
		return 1
	}
	return printMigrateVersion(m)
}

func runMigrateDown(m *migrate.Migrate, count int) int {
	if err := m.Steps(-count); err != nil {
		fmt.Fprintf(os.Stderr, "migration down failed: %v\n", err)
		return 1
	}
	return printMigrateVersion(m)
}

func runMigrateForce(m *migrate.Migrate, version int) int {
	if err := m.Force(version); err != nil {
		fmt.Fprintf(os.Stderr, "force migration version failed: %v\n", err)
		return 1
	}
	return printMigrateVersion(m)
}

func runMigrateVersion(m *migrate.Migrate) int {
	version, dirty, err := m.Version()
	// ErrNilVersion means no migration has been applied yet (a fresh or fully
	// rolled-back database) — a legitimate state, not a failure. Reported the
	// same way as the post-migration version printer for consistency.
	if err != nil && !errors.Is(err, migrate.ErrNilVersion) {
		fmt.Fprintf(os.Stderr, "failed to read version: %v\n", err)
		return 1
	}
	if errors.Is(err, migrate.ErrNilVersion) {
		fmt.Println("no migrations applied")
		return 0
	}
	fmt.Printf("version %d dirty=%v\n", version, dirty)
	return 0
}

// printMigrateVersion reports the current version after a state-changing
// command. ErrNilVersion (a fresh database) is reported, not treated as failure.
func printMigrateVersion(m *migrate.Migrate) int {
	version, dirty, err := m.Version()
	if err != nil && !errors.Is(err, migrate.ErrNilVersion) {
		fmt.Fprintf(os.Stderr, "failed to read version after migration: %v\n", err)
		return 1
	}
	if errors.Is(err, migrate.ErrNilVersion) {
		fmt.Println("no migrations applied")
		return 0
	}
	fmt.Printf("migration complete: version %d dirty=%v\n", version, dirty)
	return 0
}

// parseMigrateArgs extracts the command and its optional numeric argument from
// the positional args. Defaults: command "up", down count 1. "force" requires a
// version integer.
func parseMigrateArgs(args []string) (cmd string, count int, forceVersion int, err error) {
	cmd = "up"
	count = 1
	if len(args) == 0 {
		return cmd, count, forceVersion, nil
	}

	cmd = args[0]
	switch cmd {
	case "down":
		if len(args) >= 2 {
			parsed, parseErr := strconv.Atoi(args[1])
			if parseErr != nil || parsed <= 0 {
				return cmd, count, forceVersion, fmt.Errorf("invalid down count %q: must be a positive integer", args[1])
			}
			count = parsed
		}
	case "force":
		if len(args) < 2 {
			return cmd, count, forceVersion, errors.New("force requires a version argument")
		}
		parsed, parseErr := strconv.Atoi(args[1])
		if parseErr != nil {
			return cmd, count, forceVersion, fmt.Errorf("invalid force version %q: must be an integer", args[1])
		}
		forceVersion = parsed
	}

	return cmd, count, forceVersion, nil
}

func printMigrateUsage() {
	fmt.Fprintln(os.Stderr, "usage: goathena migrate [up | down [N] | force VERSION | version]")
}
