// Package db wires database (MariaDB/MySQL or PostgreSQL) infrastructure into
// the DI container. The driver is selected at runtime from cfg.DB.Driver.
package db

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/bouroo/goAthena/internal/config"
)

// NewDB builds a configured *gorm.DB from the application config. The driver is
// chosen from cfg.DB.Driver (DriverMariaDB | DriverPostgres). The DSN is derived
// from cfg.DBConnString() and augmented with driver-appropriate transport
// timeouts before the GORM connection is opened. Pool tuning is applied via the
// underlying *sql.DB and the database is pinged before returning.
//
// The caller is responsible for closing the underlying *sql.DB obtained via
// (*gorm.DB).DB().
//
// Schema is owned by golang-migrate; AutoMigrate is never invoked here.
func NewDB(ctx context.Context, cfg *config.Config, log *zerolog.Logger) (*gorm.DB, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}

	if log == nil {
		return nil, fmt.Errorf("logger is nil")
	}

	dsn, err := buildDSN(cfg)
	if err != nil {
		return nil, fmt.Errorf("build dsn: %w", err)
	}

	var dialector gorm.Dialector
	switch cfg.DB.Driver {
	case DriverMariaDB:
		dialector = mysql.Open(dsn)
	case DriverPostgres:
		dialector = postgres.Open(dsn)
	default:
		return nil, fmt.Errorf("unsupported db driver: %s", cfg.DB.Driver)
	}

	gormDB, err := gorm.Open(dialector, &gorm.Config{
		Logger:                 newGORMLogger(log, cfg),
		SkipDefaultTransaction: true,
	})
	if err != nil {
		return nil, fmt.Errorf("open gorm: %w", err)
	}

	sqlDB, err := gormDB.DB()
	if err != nil {
		// (*gorm.DB).DB() is the only accessor for the underlying *sql.DB; on
		// failure there is no handle to close, so the partially-opened pool is
		// abandoned. Both mysql and postgres dialectors can return a *sql.DB.
		return nil, fmt.Errorf("get sql db: %w", err)
	}

	sqlDB.SetMaxOpenConns(int(cfg.DB.MaxConns))
	sqlDB.SetMaxIdleConns(int(cfg.DB.MaxIdleConns))
	sqlDB.SetConnMaxIdleTime(cfg.DB.MaxConnIdle)
	sqlDB.SetConnMaxLifetime(cfg.DB.MaxConnLife)

	pingCtx, cancel := context.WithTimeout(ctx, cfg.DB.ConnectTimeout)
	defer cancel()
	if err := sqlDB.PingContext(pingCtx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}

	return gormDB, nil
}

// buildDSN derives a transport-tuned DSN from cfg.DBConnString(). For MariaDB
// the DSN is already in MySQL format; we append connect/read/write timeout
// query parameters. For PostgreSQL we parse the URL and add the
// connect_timeout integer-seconds query parameter.
func buildDSN(cfg *config.Config) (string, error) {
	switch cfg.DB.Driver {
	case DriverPostgres:
		u, err := url.Parse(cfg.DBConnString())
		if err != nil {
			return "", fmt.Errorf("parse dsn: %w", err)
		}
		q := u.Query()
		seconds := int(cfg.DB.ConnectTimeout / time.Second)
		seconds = max(seconds, 1)
		q.Set("connect_timeout", strconv.Itoa(seconds))
		u.RawQuery = q.Encode()
		return u.String(), nil
	default: // DriverMariaDB
		seconds := int(cfg.DB.ConnectTimeout / time.Second)
		seconds = max(seconds, 1)
		sep := "?"
		if strings.Contains(cfg.DBConnString(), "?") {
			sep = "&"
		}
		return cfg.DBConnString() +
			sep + "timeout=" + strconv.Itoa(seconds) + "s" +
			"&readTimeout=30s" +
			"&writeTimeout=30s", nil
	}
}
