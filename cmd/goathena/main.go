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
	"strconv"
	"syscall"

	"github.com/bouroo/goAthena/internal/app"
	"github.com/bouroo/goAthena/internal/config"
	"github.com/bouroo/goAthena/internal/infrastructure/db"
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
		return migrate(rest)
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

func migrate(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("migrate: need action up|down|version|force|steps")
	}
	action, rest := args[0], args[1:]
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	configPath := fs.String("config", envOr("CONFIG_PATH", "config.yaml"), "path to config file")
	if err := fs.Parse(rest); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	mg, err := db.NewMigrator(cfg.DB)
	if err != nil {
		return fmt.Errorf("migrator: %w", err)
	}
	defer func() { _ = mg.Close() }()
	return runMigrateAction(action, fs, mg)
}

// runMigrateAction dispatches a single migration verb against an open Migrator.
func runMigrateAction(action string, fs *flag.FlagSet, mg *db.Migrator) error {
	switch action {
	case "up":
		if err := mg.Up(); err != nil {
			return fmt.Errorf("up: %w", err)
		}
		v, _, _ := mg.Version()
		fmt.Printf("migrated up -> version %d\n", v)
	case "down":
		if err := mg.Down(); err != nil {
			return fmt.Errorf("down: %w", err)
		}
		fmt.Println("migrated down")
	case "version":
		v, dirty, err := mg.Version()
		if err != nil {
			return fmt.Errorf("version: %w", err)
		}
		fmt.Printf("version %d (dirty=%v)\n", v, dirty)
	case "force":
		return migrateForce(fs, mg)
	case "steps":
		return migrateSteps(fs, mg)
	default:
		return fmt.Errorf("migrate: unknown action %q", action)
	}
	return nil
}

func migrateForce(fs *flag.FlagSet, mg *db.Migrator) error {
	if fs.NArg() < 1 {
		return fmt.Errorf("migrate force: need version")
	}
	v, err := strconv.Atoi(fs.Arg(0))
	if err != nil {
		return fmt.Errorf("force version: %w", err)
	}
	if err := mg.Force(v); err != nil {
		return fmt.Errorf("force: %w", err)
	}
	fmt.Printf("forced version %d\n", v)
	return nil
}

func migrateSteps(fs *flag.FlagSet, mg *db.Migrator) error {
	if fs.NArg() < 1 {
		return fmt.Errorf("migrate steps: need n")
	}
	n, err := strconv.Atoi(fs.Arg(0))
	if err != nil {
		return fmt.Errorf("steps n: %w", err)
	}
	if err := mg.Steps(n); err != nil {
		return fmt.Errorf("steps: %w", err)
	}
	fmt.Printf("stepped %d\n", n)
	return nil
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
  goathena serve    [-config config.yaml]            Run the server until SIGINT/SIGTERM
  goathena migrate  up|down|version|force N|steps N  Apply the embedded SQL schema
  goathena version                                 Print build metadata
`)
}

func envOr(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}
