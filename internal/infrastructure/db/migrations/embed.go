// Package migrations embeds SQL migration files so cmd/migrate is self-contained.
package migrations

import "embed"

// FS holds all embedded .sql migration files.
//
//go:embed *.sql
var FS embed.FS
