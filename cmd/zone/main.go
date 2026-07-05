// Command zone is the entry point for the goAthena zone service (DEL-03).
// It is stateful (Agones GameServer) and runs map instances, AOI tower-grid,
// pathfinding, tick loop, and the embedded script engine.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/bouroo/goAthena/internal/app/common"
	"github.com/bouroo/goAthena/internal/app/zone"
	"github.com/bouroo/goAthena/internal/config"
)

var (
	Version   = "dev"
	CommitSHA = "unknown"
	BuildTime = "unknown"
)

func main() {
	os.Exit(run())
}

func run() (exitCode int) {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		return 1
	}

	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "invalid config: %v\n", err)
		return 1
	}

	common.Version = Version
	common.CommitSHA = CommitSHA
	common.BuildTime = BuildTime

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := zone.Run(ctx, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "zone stopped with error: %v\n", err)
		return 1
	}

	return 0
}
