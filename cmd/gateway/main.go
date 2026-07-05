// Command gateway is the entry point for the goAthena gateway service (DEL-01).
// It is a stateless ingress layer: kRO TCP packet parse/decrypt, WebSocket
// for roBrowser, and gRPC routing to identity/zone.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/bouroo/goAthena/internal/app/common"
	"github.com/bouroo/goAthena/internal/app/gateway"
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

	if err := gateway.Run(ctx, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "gateway stopped with error: %v\n", err)
		return 1
	}

	return 0
}
