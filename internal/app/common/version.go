// Package common provides shared bootstrap utilities used by all service
// composition roots: version metadata and signal handling.
package common

// Version metadata is populated by each cmd/<svc>/main.go via these
// package-level variables before Run is called.
var (
	Version   = "dev"
	CommitSHA = "unknown"
	BuildTime = "unknown"
)
