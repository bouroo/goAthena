// Command goathena is the single binary entry point for the goAthena modular
// monolith. It dispatches to the `serve` (long-running login/char/map server
// plus HTTP health/gRPC) and `migrate` (database schema migration) subcommands.
//
// serve blocks until SIGINT/SIGTERM (handled inside the application orchestrator)
// or a fatal server error. migrate runs to completion and exits with a status
// code reflecting whether the schema change applied.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/bouroo/goAthena/internal/app"
	"github.com/bouroo/goAthena/internal/config"
	"github.com/bouroo/goAthena/internal/infrastructure/db"
)

// Build metadata, set via -ldflags at release build time
// (-X main.Version=... etc.). Defaults describe an untagged local build.
var (
	Version   = "dev"
	CommitSHA = "unknown"
	BuildTime = "unknown"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

// run dispatches to a subcommand and returns the process exit code.
func run(args []string) int {
	if len(args) == 0 {
		printUsage()
		return 1
	}

	switch args[0] {
	case "serve":
		return runServe(args[1:])
	case "migrate":
		return runMigrate(args[1:])
	case "version":
		return runVersion()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", args[0])
		printUsage()
		return 1
	}
}

// runVersion prints the build metadata injected at release time. It lets an
// operator confirm which revision a deployed binary was built from without
// inspecting the image labels.
func runVersion() int {
	fmt.Printf("goathena %s (commit %s, built %s)\n", Version, CommitSHA, BuildTime)
	return 0
}

// runServe loads config and runs the server until shutdown. The context is the
// plain background context: Application.Run installs its own SIGINT/SIGTERM
// handler internally, so a second signal context here would race it.
func runServe(_ []string) int {
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	if err := app.Serve(context.Background(), cfg); err != nil {
		fmt.Fprintf(os.Stderr, "goathena serve exited with error: %v\n", err)
		return 1
	}
	return 0
}

// runMigrate loads config and runs the requested schema command. It passes the
// remaining args straight to the runner so `goathena migrate up|down|force|
// version` maps 1:1 to the migration subcommands.
func runMigrate(args []string) int {
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return db.RunMigrate(cfg, args)
}

// loadConfig reads and validates the application config, returning any failure
// as an error ready to print to stderr.
func loadConfig() (*config.Config, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	return cfg, nil
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "usage: goathena <command> [args]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "commands:")
	fmt.Fprintln(os.Stderr, "  serve                run the goAthena modular monolith")
	fmt.Fprintln(os.Stderr, "  migrate              apply database schema migrations")
	fmt.Fprintln(os.Stderr, "    up                 apply all pending migrations (default)")
	fmt.Fprintln(os.Stderr, "    down [N]           roll back N migrations (default 1)")
	fmt.Fprintln(os.Stderr, "    force VERSION      set the migration version, ignoring state")
	fmt.Fprintln(os.Stderr, "    version            print the current migration version")
	fmt.Fprintln(os.Stderr, "  version              print the goathena build version")
}
