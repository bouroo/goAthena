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
	"github.com/bouroo/goAthena/internal/modules/account"
	"github.com/bouroo/goAthena/internal/modules/gateway"
)

// loginServer is the lifecycle surface App.Run needs from a game-protocol
// listener. Keeping it an interface (rather than importing gateway/app, whose
// package name collides with this one) lets more listeners plug in later.
type loginServer interface {
	Start(addr string)
	Stop()
}

// deps are the best-effort infrastructure singletons the control plane probes
// for readiness. A nil field means the dependency could not be reached at boot;
// /readyz then reports 503 so load balancers stop sending traffic.
type deps struct {
	db     *gorm.DB
	valkey vk.Client
	login  loginServer
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

	// Feature modules register their providers; they resolve infra lazily, so a
	// down dependency surfaces as a resolution error at use time, not at boot.
	account.Register(inj, cfg.Identity.UseMD5Passwords)

	// The login listener resolves the account Authenticator from the injector.
	// Best-effort: a build failure is logged and login simply won't serve.
	if ls, err := gateway.NewLoginServer(inj, *cfg, log); err != nil {
		log.Error("login listener build failed; login will not start", "err", err)
	} else {
		d.login = ls
		closers = append(closers, ls.Stop)
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
