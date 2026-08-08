// Command goathena is the single deployable binary for the modular-monolith
// Ragnarok Online server. It speaks three subcommands: serve (run the server),
// migrate (apply the schema — lands with the persistence phase), and version.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/bouroo/goAthena/internal/app"
	"github.com/bouroo/goAthena/internal/config"
	"github.com/bouroo/goAthena/internal/shared/log"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "goathena:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return fmt.Errorf("no subcommand")
	}
	cmd, rest := args[0], args[1:]

	switch cmd {
	case "serve":
		return serve(rest)
	case "migrate":
		// Migrations land with the persistence module (later phase). Fail loudly
		// rather than silently no-op so operators see this is not wired yet.
		return fmt.Errorf("migrate not implemented in this rebuild phase (lands with persistence)")
	case "version":
		fmt.Printf("goathena %s (commit %s, built %s)\n", app.Version, app.Commit, app.BuildTime)
		return nil
	case "-h", "--help", "help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown subcommand %q", cmd)
	}
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	configPath := fs.String("config", envOr("CONFIG_PATH", "config.yaml"), "path to config file")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	logger := log.New(cfg.Log)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	a, err := app.New(ctx, cfg, logger)
	if err != nil {
		return fmt.Errorf("build app: %w", err)
	}
	logger.Info("starting goathena", "name", cfg.App.Name, "env", cfg.App.Environment, "version", app.Version)
	if err := a.Run(ctx); err != nil {
		return fmt.Errorf("run: %w", err)
	}
	return nil
}

func usage() {
	fmt.Fprint(os.Stderr, `goathena — modular-monolith Ragnarok Online server

Usage:
  goathena serve   [-config config.yaml]   Run the server until SIGINT/SIGTERM
  goathena migrate                          Apply schema (lands with persistence)
  goathena version                          Print build metadata
`)
}

func envOr(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}
