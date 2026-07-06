// Package di wires the zone feature into the DI container.
//
// Bootstrap order: config + logger + agones lifecycle (provided by
// upstream Register calls) → map data (loaded from disk or synthetic
// fallback) → TickLoop → ZoneService.
//
// The TickLoop is constructed here but NOT started; the zone app
// composition root calls TickLoop.Start in a goroutine after Agones
// readiness is established.
package di

import (
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"github.com/samber/do/v2"

	"github.com/bouroo/goAthena/internal/config"
	"github.com/bouroo/goAthena/internal/features/zone/domain"
	"github.com/bouroo/goAthena/internal/features/zone/service"
	"github.com/bouroo/goAthena/internal/infrastructure/agones"
	"github.com/bouroo/goAthena/pkg/ro/romap"
)

// Register wires the zone feature (map instances, AOI, tick loop,
// pathfinding) into the DI container. It depends on *config.Config,
// *zerolog.Logger and agones.Lifecycle being already registered.
func Register(c do.Injector) error {
	cfg := do.MustInvoke[*config.Config](c)
	logger := do.MustInvoke[*zerolog.Logger](c)
	ag := do.MustInvoke[agones.Lifecycle](c)

	md, err := loadMap(cfg.Zone.DefaultMap)
	if err != nil {
		// Don't hard-fail: surface a clear log and substitute a synthetic
		// 100x100 all-walkable map so the zone can boot in dev/CI without
		// real .gat files on disk. Production deployments that ship map
		// files will never hit this branch.
		logger.Warn().Err(err).
			Str("map_dir", cfg.Zone.MapDir).
			Str("default_map", cfg.Zone.DefaultMap).
			Msg("zone: default map load failed, using synthetic fallback")
		md = syntheticMap(cfg.Zone.DefaultMap)
	}

	tickLoop := service.NewTickLoop(md, cfg.Zone.TickRate, logger)

	zoneSvc := service.NewZoneService(
		tickLoop,
		ag,
		cfg.Zone.MoveSpeed,
		int(cfg.Zone.ShutdownGrace/time.Millisecond),
		logger,
	)

	do.ProvideValue(c, tickLoop)
	do.ProvideValue(c, zoneSvc)
	do.ProvideValue(c, domain.ZoneService(zoneSvc))

	logger.Info().
		Str("map", md.Name).
		Int("width", md.Width).
		Int("height", md.Height).
		Dur("tick_rate", cfg.Zone.TickRate).
		Int("move_speed", cfg.Zone.MoveSpeed).
		Msg("zone feature registered")

	return nil
}

// ProvideZoneService resolves the wired ZoneService use case. Other
// features (notably the gateway transport layer) call this to invoke
// zone operations without depending on the service internals.
func ProvideZoneService(c do.Injector) (domain.ZoneService, error) {
	svc, err := do.Invoke[domain.ZoneService](c)
	if err != nil {
		return nil, fmt.Errorf("resolve zone service: %w", err)
	}
	return svc, nil
}

// ProvideTickLoop resolves the wired TickLoop. The zone app composition
// root calls TickLoop.Start after Agones is ready.
func ProvideTickLoop(c do.Injector) (*service.TickLoop, error) {
	tl, err := do.Invoke[*service.TickLoop](c)
	if err != nil {
		return nil, fmt.Errorf("resolve tick loop: %w", err)
	}
	return tl, nil
}

// syntheticMap builds an all-walkable 100x100 map used as a dev fallback
// when real .gat files are absent. MapData.IsWalkable treats
// out-of-bounds coordinates as walls, so this is safe to expose to
// pathfinding/AOI without any additional validation.
func syntheticMap(name string) *romap.MapData {
	const w, h = 100, 100
	md := &romap.MapData{
		Name:     name,
		Width:    w,
		Height:   h,
		Walkable: make([]bool, w*h),
		Heights:  make([]float32, w*h),
	}
	for i := range md.Walkable {
		md.Walkable[i] = true
	}
	return md
}

// loadMap attempts to load the named map. Real implementation will
// read .gat/.rsw from cfg.Zone.MapDir; for Phase 4 we return an error
// to trigger the synthetic fallback. The full disk loader will land in
// a follow-up unit.
func loadMap(_ string) (*romap.MapData, error) {
	return nil, fmt.Errorf("disk map loader not yet implemented (P4.6)")
}
