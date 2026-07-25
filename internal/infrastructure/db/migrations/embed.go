// Package migrations embeds SQL migration files so `goathena migrate` is
// self-contained (no external SQL directory at runtime).
package migrations

import "embed"

// FS holds all embedded .sql migration files.
//
//go:embed *.sql
var FS embed.FS
