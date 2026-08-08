// Package migrations embeds the SQL migration files so `goathena migrate` is a
// self-contained, idempotent schema applier (no external file path needed).
//
// Migrations are split into per-engine subdirectories (mariadb/, postgres/)
// because the two databases have incompatible DDL dialects (AUTO_INCREMENT vs
// GENERATED IDENTITY, backtick vs double-quote, unsigned/enum, ENGINE clause).
// The Migrator selects the subdirectory matching the configured driver.
package migrations

import "embed"

// FS embeds all .sql up/down files in both engine subdirectories. The Migrator
// roots the iofs source at the driver-specific subdirectory (e.g. "mariadb").
//
//go:embed mariadb postgres
var FS embed.FS
