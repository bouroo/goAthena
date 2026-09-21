package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/bouroo/goAthena/internal/config"
	"github.com/bouroo/goAthena/internal/infrastructure/db"
	natsinfra "github.com/bouroo/goAthena/internal/infrastructure/messaging/nats"
	charinfra "github.com/bouroo/goAthena/internal/modules/character/infra"
	economyapp "github.com/bouroo/goAthena/internal/modules/economy/app"
	economyinfra "github.com/bouroo/goAthena/internal/modules/economy/infra"
	"github.com/bouroo/goAthena/internal/modules/economy/remote"
)

// RunEconomyHost serves the economy module over the NATS bus: DB + ledger +
// in-process EconomyService + the remote request/reply subscriptions, and
// nothing else — no game listeners, no Valkey, no world. `goathena
// serve-economy` runs it; a zone process with nats.economy=remote calls it
// (M13).
//
// Unlike compose, every dependency here is REQUIRED, not best-effort: a host
// that cannot reach its DB or its bus serves nothing, and a half-up economy
// host is worse than a absent one (callers would time out instead of failing
// fast). The dial tolerates a broker that is merely slow to appear
// (retry-on-failed-connect); a malformed URL or a refused DB aborts the boot.
func RunEconomyHost(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
	gdb, err := db.New(cfg.DB)
	if err != nil {
		return fmt.Errorf("db: %w", err)
	}
	defer func() { _ = db.Close(gdb) }()

	if err := applyMigrations(ctx, cfg, log); err != nil {
		return fmt.Errorf("migrations: %w", err)
	}

	svc := economyapp.NewEconomyService(
		charinfra.NewGORMCharacterRepository(gdb),
		economyinfra.NewGORMLedger(gdb),
	)

	nc, err := natsinfra.New(cfg.NATS)
	if err != nil {
		return fmt.Errorf("nats: %w", err)
	}
	defer func() { _ = natsinfra.Close(nc) }()

	srv, err := remote.NewServer(nc, svc, log)
	if err != nil {
		return fmt.Errorf("economy server: %w", err)
	}
	log.Info("economy host serving",
		"url", cfg.NATS.URL, "subjects", srv.Subscriptions(), "version", Version)

	<-ctx.Done()
	log.Info("economy host draining")
	return nil
}
