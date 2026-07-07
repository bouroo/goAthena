// Package di wires the zone feature into the DI container.
//
// Bootstrap order: config + logger + agones lifecycle (provided by
// upstream Register calls) → map data (loaded from disk or synthetic
// fallback) → TickLoop → ZoneService → gRPC handler + *grpc.Server.
//
// The TickLoop and gRPC server are constructed here but NOT started; the
// zone app composition root starts both after Agones readiness is
// established.
package di

import (
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"github.com/samber/do/v2"
	"google.golang.org/grpc"

	zonev1 "github.com/bouroo/goAthena/api/pb/zone/v1"
	"github.com/bouroo/goAthena/internal/config"
	"github.com/bouroo/goAthena/internal/features/zone/domain"
	"github.com/bouroo/goAthena/internal/features/zone/handler"
	"github.com/bouroo/goAthena/internal/features/zone/service"
	"github.com/bouroo/goAthena/internal/infrastructure/agones"
	"github.com/bouroo/goAthena/internal/shared/server"
	"github.com/bouroo/goAthena/pkg/ro/romap"
)

// Register wires the zone feature (map instances, AOI, tick loop,
// pathfinding, gRPC transport) into the DI container. It depends on
// *config.Config, *zerolog.Logger and agones.Lifecycle being already
// registered.
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

	spawnX, spawnY := findWalkableSpawn(md)

	grpcServer := server.NewGRPC(logger)
	zonev1.RegisterZoneServiceServer(
		grpcServer,
		handler.NewGRPCHandler(zoneSvc, md.Name, spawnX, spawnY, logger),
	)

	do.ProvideValue(c, tickLoop)
	do.ProvideValue(c, zoneSvc)
	do.ProvideValue(c, domain.ZoneService(zoneSvc))
	do.ProvideValue(c, grpcServer)

	logger.Info().
		Str("map", md.Name).
		Int("width", md.Width).
		Int("height", md.Height).
		Int("spawn_x", spawnX).
		Int("spawn_y", spawnY).
		Dur("tick_rate", cfg.Zone.TickRate).
		Int("move_speed", cfg.Zone.MoveSpeed).
		Str("grpc_addr", cfg.GRPCAddr()).
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

// ProvideGRPCServer resolves the wired *grpc.Server that carries the
// ZoneService handler. The zone app composition root binds it to a TCP
// listener and serves it after Agones Ready.
func ProvideGRPCServer(c do.Injector) (*grpc.Server, error) {
	s, err := do.Invoke[*grpc.Server](c)
	if err != nil {
		return nil, fmt.Errorf("resolve zone grpc server: %w", err)
	}
	return s, nil
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

// findWalkableSpawn returns the nearest walkable cell to the map
// center. It scans outward in an expanding square from
// (centerX, centerY). If no walkable cell exists (degenerate all-wall
// map), it returns (0, 0).
func findWalkableSpawn(md *romap.MapData) (int, int) {
	cx, cy := md.Width/2, md.Height/2
	if md.IsWalkable(cx, cy) {
		return cx, cy
	}
	maxR := max(md.Width, md.Height)
	for r := 1; r <= maxR; r++ {
		for dy := -r; dy <= r; dy++ {
			for dx := -r; dx <= r; dx++ {
				if abs(dx) < r && abs(dy) < r {
					continue // inner ring already checked
				}
				x, y := cx+dx, cy+dy
				if md.IsWalkable(x, y) {
					return x, y
				}
			}
		}
	}
	return 0, 0
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// loadMap attempts to load the named map. Real implementation will
// read .gat/.rsw from cfg.Zone.MapDir; for Phase 4 we return an error
// to trigger the synthetic fallback. The full disk loader will land in
// a follow-up unit.
func loadMap(_ string) (*romap.MapData, error) {
	return nil, fmt.Errorf("disk map loader not yet implemented (P4.6)")
}
