package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/samber/do/v2"
	vk "github.com/valkey-io/valkey-go"
	"gorm.io/gorm"

	"github.com/bouroo/goAthena/internal/config"
	"github.com/bouroo/goAthena/internal/infrastructure/db"
	"github.com/bouroo/goAthena/internal/infrastructure/messaging/valkey"
	"github.com/bouroo/goAthena/internal/modules/account"
	"github.com/bouroo/goAthena/internal/modules/character"
	shopmod "github.com/bouroo/goAthena/internal/modules/commerce/shop"
	"github.com/bouroo/goAthena/internal/modules/economy"
	"github.com/bouroo/goAthena/internal/modules/gateway"
	"github.com/bouroo/goAthena/internal/modules/inventory"
	"github.com/bouroo/goAthena/internal/modules/social"
	"github.com/bouroo/goAthena/internal/modules/world"
	worldapp "github.com/bouroo/goAthena/internal/modules/world/app"
)

// protoListener is the lifecycle surface App.Run needs from a game-protocol
// listener. Keeping it an interface (rather than importing gateway/app, whose
// package name collides with this one) lets more listeners plug in later.
type protoListener interface {
	Start(addr string)
	Stop()
}

// deps are the best-effort infrastructure singletons the control plane probes
// for readiness. A nil field means the dependency could not be reached at boot;
// /readyz then reports 503 so load balancers stop sending traffic.
type deps struct {
	db     *gorm.DB
	valkey vk.Client
	login  protoListener
	char   protoListener
	mapSrv protoListener
	tick   tickStarter
}

// tickStarter is the lifecycle surface for the world tick loop.
type tickStarter interface {
	StartTick(ctx context.Context, update func(ctx context.Context, dt time.Duration))
	Stop()
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
	character.Register(inj, cfg.Identity.MaxChars)
	inventory.Register(inj)
	economy.Register(inj)
	shopmod.Register(inj)
	social.Register(inj)
	world.Register(inj, cfg.Zone.TickRateHz)

	// Resolve the world service so App.Run can start/stop its tick loop.
	if ws, err := do.Invoke[*worldapp.WorldService](inj); err != nil {
		log.Error("world service resolve failed; tick loop will not run", "err", err)
	} else {
		d.tick = ws
	}

	// Listeners resolve their ports from the injector. Best-effort: a build
	// failure is logged and the listener simply won't serve.
	if ls, err := gateway.NewLoginServer(inj, *cfg, log); err != nil {
		log.Error("login listener build failed; login will not start", "err", err)
	} else {
		d.login = ls
		closers = append(closers, ls.Stop)
	}
	if cs, err := gateway.NewCharServer(inj, *cfg, log); err != nil {
		log.Error("char listener build failed; char will not start", "err", err)
	} else {
		d.char = cs
		closers = append(closers, cs.Stop)
	}
	if ms, err := gateway.NewMapServer(inj, log); err != nil {
		log.Error("map listener build failed; map will not start", "err", err)
	} else {
		d.mapSrv = ms
		closers = append(closers, ms.Stop)
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
