package db

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// gormChecker reports database connectivity by pinging the underlying
// *sql.DB exposed by *gorm.DB. It works for both MariaDB and PostgreSQL
// drivers selected via config.DB.Driver.
type gormChecker struct {
	db *gorm.DB
}

// Name returns the dependency name reported in health output.
func (gormChecker) Name() string {
	return DriverPostgres
}

// Check verifies the database is reachable by pinging the connection pool.
func (c gormChecker) Check(ctx context.Context) error {
	if c.db == nil {
		return fmt.Errorf("gorm db is not initialized")
	}
	sqlDB, err := c.db.DB()
	if err != nil {
		return fmt.Errorf("get sql db: %w", err)
	}
	if err := sqlDB.PingContext(ctx); err != nil {
		return fmt.Errorf("ping db: %w", err)
	}
	return nil
}
