// Package migrations embeds the SQL migration files so `goathena migrate` is a
// self-contained, idempotent schema applier (no external file path needed).
package migrations

import "embed"

// FS embeds all .sql up/down migration files in this directory so the
// `goathena migrate` subcommand is self-contained.
//
//go:embed *.sql
var FS embed.FS
