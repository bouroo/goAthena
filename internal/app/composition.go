package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/samber/do/v2"
	vk "github.com/valkey-io/valkey-go"
	"gorm.io/gorm"

	"github.com/bouroo/goAthena/internal/config"
	"github.com/bouroo/goAthena/internal/infrastructure/db"
	"github.com/bouroo/goAthena/internal/infrastructure/messaging/valkey"
)

// deps are the best-effort infrastructure singletons the control plane probes
// for readiness. A nil field means the dependency could not be reached at boot;
// /readyz then reports 503 so load balancers stop sending traffic.
type deps struct {
	db     *gorm.DB
	valkey vk.Client
}

// compose opens the infrastructure singletons and registers them in the DI
// injector so bounded contexts can resolve them later. DB and Valkey connect
// best-effort: a boot failure is logged and the singleton left unregistered
// rather than crashing the process, so /healthz stays 200 and /readyz reflects
// the outage. The returned closer tears everything down in reverse order.
func compose(ctx context.Context, cfg *config.Config, log *slog.Logger) (do.Injector, *deps, func()) {
	inj := do.New()
	d := &deps{}
	var closers []func()

	// Register pure singletons every module needs.
	do.ProvideValue(inj, cfg)
	do.ProvideValue(inj, log)

	if gdb, err := db.New(cfg.DB); err != nil {
		log.Error("db connect failed; readiness will report down", "err", err)
	} else {
		d.db = gdb
		do.ProvideValue(inj, gdb)
		closers = append(closers, func() { _ = db.Close(gdb) })
	}

	if vc, err := valkey.New(ctx, cfg.Valkey); err != nil {
		log.Error("valkey connect failed; readiness will report down", "err", err)
	} else {
		d.valkey = vc
		do.ProvideValue(inj, vc)
		closers = append(closers, func() { vc.Close() })
	}

	closeAll := func() {
		for i := len(closers) - 1; i >= 0; i-- {
			closers[i]()
		}
	}
	return inj, d, closeAll
}

// ready reports nil only when every required dependency answers its probe.
func (d *deps) ready(ctx context.Context) error {
	if d.db == nil {
		return fmt.Errorf("db not connected")
	}
	if err := db.Ping(ctx, d.db); err != nil {
		return fmt.Errorf("db: %v", err)
	}
	if d.valkey == nil {
		return fmt.Errorf("valkey not connected")
	}
	if err := valkey.Ping(ctx, d.valkey); err != nil {
		return fmt.Errorf("valkey: %v", err)
	}
	return nil
}
