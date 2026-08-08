// Package rathenadb parses rAthena MySQL DDL — specifically
// `third_party/rathena/sql-files/main.sql` and goAthena's
// `internal/infrastructure/db/migrations/*.up.sql` files — into a typed
// Table/Column model and computes a column-level drift report between
// the two.
//
// The parser handles the rAthena DDL subset: `CREATE TABLE [IF NOT
// EXISTS] \`name\` ( ... ) ENGINE=...;` blocks with one column per line,
// backtick-quoted column names, and constraint lines (PRIMARY KEY, KEY,
// UNIQUE KEY, CONSTRAINT, INDEX, FOREIGN KEY) that are ignored for drift
// purposes. Comments (-- to end-of-line and /* ... */) and non-CREATE
// statements (INSERT, ALTER, DROP, etc.) are skipped.
//
// `ParseMainSQL` is intended for the rAthena canonical schema file;
// `ParseMigrationSQL` is intended for goAthena *.up.sql migrations and
// ignores ALTER/DROP/INSERT statements (goAthena migrations are
// additive-only per decision D-001, so drift is defined on the column
// set of CREATE TABLE blocks).
//
// The package has no I/O dependencies; callers feed it strings (read
// from `embed.FS`, the filesystem, or wherever). The D2+D8 R0 critical
// path gate (`internal/infrastructure/db/migrations/drift_test.go`,
// //go:build integration) wires this up against the real rAthena
// main.sql and the embedded migrations.FS.
package rathenadb
