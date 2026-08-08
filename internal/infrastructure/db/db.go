// Package db opens the process-wide GORM connection backing all bounded
// contexts. The rAthena schema is applied by golang-migrate (never AutoMigrate),
// so this package only dials the engine and configures the pool.
package db

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/bouroo/goAthena/internal/config"
)

// New opens a GORM connection for the configured engine. mysql:// selects the
// MariaDB driver (rAthena schema compat); postgres:// selects PostgreSQL (prod).
func New(cfg config.DBConfig) (*gorm.DB, error) {
	gormCfg := &gorm.Config{
		Logger:                 logger.Default.LogMode(logger.Warn),
		SkipDefaultTransaction: true,
	}
	switch normalize(cfg.Driver) {
	case "mariadb":
		d, err := gorm.Open(mysql.Open(dsnMariaDB(cfg)), gormCfg)
		if err != nil {
			return nil, fmt.Errorf("open mariadb: %w", err)
		}
		return applyPool(d), nil
	case "postgres":
		d, err := gorm.Open(postgres.Open(dsnPostgres(cfg)), gormCfg)
		if err != nil {
			return nil, fmt.Errorf("open postgres: %w", err)
		}
		return applyPool(d), nil
	default:
		return nil, fmt.Errorf("unsupported db driver %q", cfg.Driver)
	}
}

// Ping verifies the connection is live within a short deadline.
func Ping(ctx context.Context, db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("raw db: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(pingCtx); err != nil {
		return fmt.Errorf("ping: %w", err)
	}
	return nil
}

// Close releases the underlying connection pool.
func Close(db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("raw db: %w", err)
	}
	if err := sqlDB.Close(); err != nil {
		return fmt.Errorf("close pool: %w", err)
	}
	return nil
}

// applyPool tunes the underlying connection pool. Values are conservative
// defaults suitable for a single zone process; tune per-deployment via env later.
func applyPool(d *gorm.DB) *gorm.DB {
	sqlDB, err := d.DB()
	if err != nil {
		return d
	}
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetMaxOpenConns(50)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	return d
}

func normalize(d string) string {
	switch d {
	case "mysql":
		return "mariadb"
	case "postgresql":
		return "postgres"
	default:
		return d
	}
}

func dsnMariaDB(c config.DBConfig) string {
	// go-sql-driver's tls param accepts false|true|skip-verify|preferred|<name>,
	// not the postgres-style "disable". Normalize the common no-TLS spellings.
	tls := "false"
	switch strings.ToLower(c.SSLMode) {
	case "true", "skip-verify", "preferred":
		tls = strings.ToLower(c.SSLMode)
	}
	return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?parseTime=true&tls=%s&charset=utf8mb4",
		c.User, c.Password, c.Host, c.Port, c.Name, tls)
}

func dsnPostgres(c config.DBConfig) string {
	ssl := c.SSLMode
	if ssl == "" {
		ssl = "disable"
	}
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s",
		c.User, c.Password, c.Host, c.Port, c.Name, ssl)
}
