package app

import (
	"context"
	"errors"
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
	"github.com/bouroo/goAthena/internal/modules/content"
	"github.com/bouroo/goAthena/internal/modules/economy"
	"github.com/bouroo/goAthena/internal/modules/gateway"
	"github.com/bouroo/goAthena/internal/modules/inventory"
	"github.com/bouroo/goAthena/internal/modules/social"
	"github.com/bouroo/goAthena/internal/modules/transit"
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
	RegenTick(dt time.Duration)
	Stop()
}

// compose opens the infrastructure singletons and registers them in the DI
// injector so bounded contexts can resolve them later.
//
// Error classification: cfg arrives already validated, so any non-recoverable
// misconfiguration (malformed YAML, missing required field, unsupported value)
// was rejected by config.Load — wrapping config.ErrConfigFatal — before this
// function runs; the process exits at main rather than entering compose. The
// failures compose CAN still see are therefore transient infrastructure
// outages (DB/Valkey unreachable, listener bind refused). Those stay
// best-effort: a boot failure is logged and the singleton left unregistered
// rather than crashing the process, so /healthz stays 200 and /readyz reflects
// the outage for the readiness loop to keep probing. The returned closer tears
// everything down in reverse order.
func compose(ctx context.Context, cfg *config.Config, log *slog.Logger) (do.Injector, *deps, func()) {
	inj := do.New()
	d := &deps{}
	var closers []func()

	// Register pure singletons every module needs.
	do.ProvideValue(inj, cfg)
	do.ProvideValue(inj, log)

	// OTel (#10): initialize the trace SDK when exporter=otlp + an endpoint.
	// Best-effort — a refused exporter is logged and the server stays up.
	if shutdown, wired := initOTel(ctx, cfg.OTel, log); wired {
		closers = append([]func(){shutdown}, closers...) // shut down OTel first
	} else {
		log.Info("otel: trace export disabled (exporter=none or no endpoint)",
			"exporter", cfg.OTel.Exporter, "endpoint", cfg.OTel.Endpoint)
	}

	if gdb, err := db.New(cfg.DB); err != nil {
		log.Error("db connect failed; readiness will report down", "err", err)
	} else {
		d.db = gdb
		do.ProvideValue(inj, gdb)
		closers = append(closers, func() { _ = db.Close(gdb) })

		// Apply the embedded rAthena schema at boot so a fresh volume reaches
		// readiness without a manual `goathena migrate up`. Bounded retry
		// tolerates a slow-to-accept DB; a persistent failure leaves /readyz
		// reporting not-ready because probeSchema finds no schema_migrations row.
		// Reuses the existing golang-migrate infra — no new runner dependency.
		if err := applyMigrations(ctx, cfg, log); err != nil {
			log.Error("boot migrations failed; /readyz will report schema not applied", "err", err)
		}
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
	content.Register(inj)
	transit.Register(inj)
	world.Register(inj, cfg.Zone.TickRateHz, cfg.Zone.DBPath)

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

// ready reports nil only when every required dependency answers its probe AND
// the rAthena schema is present. A bare DB ping would report ready against a
// fresh/empty volume (no tables); the schema check closes that gap so a load
// balancer stops sending traffic until migrations have landed.
func (d *deps) ready(ctx context.Context) error {
	if d.db == nil {
		return errors.New("db not connected")
	}
	if err := db.Ping(ctx, d.db); err != nil {
		return fmt.Errorf("db: %v", err)
	}
	version, dirty, err := d.probeSchema(ctx)
	if verr := schemaVerdict(err, version, dirty); verr != nil {
		return verr
	}
	if d.valkey == nil {
		return errors.New("valkey not connected")
	}
	if err := valkey.Ping(ctx, d.valkey); err != nil {
		return fmt.Errorf("valkey: %v", err)
	}
	return nil
}

// probeSchema reads the golang-migrate version row over the existing GORM pool.
// On a fresh volume the schema_migrations table is absent, so the query errors
// — the honest "schema not applied" signal. One cheap query per probe; no
// dedicated connection is opened (migrator handles are confined to Up).
func (d *deps) probeSchema(ctx context.Context) (version uint, dirty bool, err error) {
	var row struct {
		Version uint
		Dirty   bool
	}
	res := d.db.WithContext(ctx).
		Raw("SELECT version, dirty FROM schema_migrations LIMIT 1").
		Scan(&row)
	if res.Error != nil {
		return 0, false, fmt.Errorf("read schema_migrations: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return 0, false, errors.New("no schema_migrations row (schema not applied)")
	}
	return row.Version, row.Dirty, nil
}

// schemaVerdict is the pure, testable core of the schema-aware readiness gate.
// It is split from probeSchema so the decision (applied? dirty? non-zero?) can
// be unit-tested with fakes — the live query cannot run without a database.
func schemaVerdict(err error, version uint, dirty bool) error {
	if err != nil {
		return fmt.Errorf("schema not applied: %w", err)
	}
	if dirty {
		return fmt.Errorf("schema dirty at version %d", version)
	}
	if version == 0 {
		return errors.New("schema not applied (version 0)")
	}
	return nil
}

// applyMigrations runs the embedded schema at boot with a bounded retry. It
// reuses db.NewMigrator (golang-migrate over the embedded SQL) — no new
// migration runner is introduced. Each attempt opens and closes its own
// migrator handles; the loop honours ctx cancellation between attempts.
func applyMigrations(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
	const maxAttempts = 3
	backoff := 500 * time.Millisecond
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("boot migrate cancelled: %w", err)
		}
		mg, err := db.NewMigrator(cfg.DB)
		if err != nil {
			lastErr = fmt.Errorf("migrator open (attempt %d): %w", attempt, err)
		} else if err := mg.Up(); err != nil {
			lastErr = fmt.Errorf("migrate up (attempt %d): %w", attempt, err)
			_ = mg.Close()
		} else {
			v, dirty, _ := mg.Version()
			log.Info("boot migrations applied", "version", v, "dirty", dirty, "attempt", attempt)
			_ = mg.Close()
			return nil
		}
		log.Warn("boot migrate retry", "attempt", attempt, "err", lastErr, "backoff", backoff)
		if !sleepCtx(ctx, backoff) {
			return fmt.Errorf("boot migrate cancelled: %w", ctx.Err())
		}
		backoff *= 2
	}
	return lastErr
}

// sleepCtx blocks for d or until ctx is cancelled. Returns false if cancelled.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// otelStatus removed: the trace SDK is now wired by initOTel (internal/app/otel.go).
