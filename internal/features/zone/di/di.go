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
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
	"github.com/samber/do/v2"
	"google.golang.org/grpc"

	zonev1 "github.com/bouroo/goAthena/api/pb/zone/v1"
	"github.com/bouroo/goAthena/internal/config"
	tradedi "github.com/bouroo/goAthena/internal/features/trade/di"
	"github.com/bouroo/goAthena/internal/features/zone/domain"
	"github.com/bouroo/goAthena/internal/features/zone/handler"
	"github.com/bouroo/goAthena/internal/features/zone/service"
	"github.com/bouroo/goAthena/internal/infrastructure/agones"
	natsinfra "github.com/bouroo/goAthena/internal/infrastructure/messaging/nats"
	"github.com/bouroo/goAthena/internal/shared/server"
	"github.com/bouroo/goAthena/pkg/ro/mobdb"
	"github.com/bouroo/goAthena/pkg/ro/romap"
)

// mobGIDCounter allocates zone-local GIDs for mob entities. Initialized to
// 110_000_001 so values stay in the standard rAthena mob GID band and remain
// distinct from player GIDs (which are allocated by the identity/gateway
// services).
var mobGIDCounter atomic.Uint32

func init() {
	mobGIDCounter.Store(110_000_001)
}

func nextMobGID() uint32 {
	return mobGIDCounter.Add(1) - 1
}

// Register wires the zone feature (map instances, AOI, tick loop,
// pathfinding, gRPC transport) into the DI container. It depends on
// *config.Config, *zerolog.Logger, agones.Lifecycle, and *natsinfra.Client
// being already registered.
func Register(c do.Injector) error {
	cfg := do.MustInvoke[*config.Config](c)
	logger := do.MustInvoke[*zerolog.Logger](c)
	ag := do.MustInvoke[agones.Lifecycle](c)
	nc := do.MustInvoke[*natsinfra.Client](c)

	if err := tradedi.Register(c); err != nil {
		return fmt.Errorf("register trade feature: %w", err)
	}

	tradeSvc, err := tradedi.ProvideTradeService(c)
	if err != nil {
		return fmt.Errorf("resolve trade service: %w", err)
	}

	md, err := loadMap(cfg.Zone.MapDir, cfg.Zone.DefaultMap)
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

	tickLoop := service.NewTickLoop(md, cfg.Zone.TickRate, logger, NewNATSPublisher(nc))

	zoneSvc := service.NewZoneService(
		tickLoop,
		ag,
		cfg.Zone.MoveSpeed,
		int(cfg.Zone.ShutdownGrace/time.Millisecond),
		logger,
	)

	tickLoop.SetDamageEntity(func(ctx context.Context, entityID domain.EntityID, damage int32, attackerID domain.EntityID) (*domain.DamageResponse, error) {
		return zoneSvc.DamageEntity(ctx, entityID, damage, attackerID, 0, 0)
	})

	spawnX, spawnY := findWalkableSpawn(md)

	grpcServer := server.NewGRPC(logger)
	zonev1.RegisterZoneServiceServer(
		grpcServer,
		handler.NewGRPCHandler(zoneSvc, tradeSvc, md.Name, spawnX, spawnY, logger),
	)

	do.ProvideValue(c, tickLoop)
	do.ProvideValue(c, zoneSvc)
	do.ProvideValue(c, domain.ZoneService(zoneSvc))
	do.ProvideValue(c, grpcServer)

	if spawned := spawnStartupMobs(cfg, logger, zoneSvc, md); spawned > 0 {
		logger.Info().
			Int("count", spawned).
			Str("map", md.Name).
			Msg("zone: spawned startup mobs")
	}

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

// loadMap reads .gat and .rsw files for the named map from mapDir and
// parses them via romap.LoadMap. The .rsw file is optional (soft-fail):
// if it is missing or unreadable, the map loads without water-level data.
// The .gat file is required: a missing or malformed .gat is a hard error.
func loadMap(mapDir, name string) (*romap.MapData, error) {
	gatPath := filepath.Join(mapDir, name+".gat")
	gat, err := os.ReadFile(gatPath) // #nosec G304 -- mapDir is server config, name is the configured default map
	if err != nil {
		return nil, fmt.Errorf("read %s.gat: %w", name, err)
	}

	var rsw []byte
	rswPath := filepath.Join(mapDir, name+".rsw")
	if rswData, rswErr := os.ReadFile(rswPath); rswErr == nil { // #nosec G304 -- see above
		rsw = rswData
	}

	md, err := romap.LoadMap(name, gat, rsw)
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", name, err)
	}
	return md, nil
}

// spawnStartupMobs loads the configured mob_db and spawn config and registers
// every spawned mob with the zone service. Both files are optional: an empty
// path or a read/parse failure is logged and the function returns 0 without
// failing zone boot. Each spawn entry's Count mobs are placed at deterministic
// coordinates (X/Y from the entry, no per-instance random offset yet — the
// XRange/YRange randomization lands in u3 alongside wander AI).
//
// The first mob GID comes from the package-level atomic counter starting at
// 110_000_001, matching the rAthena mob GID band so the gateway's GID→MobID
// routing in u4 stays unambiguous.
func spawnStartupMobs(cfg *config.Config, logger *zerolog.Logger, zoneSvc *service.ZoneService, md *romap.MapData) int {
	if cfg.Zone.MobDBPath == "" || cfg.Zone.MobSpawnsPath == "" {
		return 0
	}

	registry, err := mobdb.LoadFile(cfg.Zone.MobDBPath)
	if err != nil {
		logger.Warn().Err(err).
			Str("path", cfg.Zone.MobDBPath).
			Msg("zone: mob_db load failed; skipping mob spawn")
		return 0
	}

	spawnCfg, err := domain.LoadMobSpawnsFile(cfg.Zone.MobSpawnsPath)
	if err != nil {
		logger.Warn().Err(err).
			Str("path", cfg.Zone.MobSpawnsPath).
			Msg("zone: mob spawn config load failed; skipping mob spawn")
		return 0
	}

	ctx := context.Background()
	spawned := 0
	for _, entry := range spawnCfg.Spawns {
		mobID := int32(entry.MobID) //nolint:gosec // mob IDs are rAthena-allocated, bounded by DB lookup
		mob := registry.Get(mobID)
		if mob == nil {
			logger.Warn().
				Int32("mob_id", mobID).
				Msg("zone: spawn entry references unknown mob_id; skipping")
			continue
		}
		count := max(entry.Count, 1)
		respawn := time.Duration(entry.RespawnMs) * time.Millisecond
		if respawn <= 0 {
			respawn = 5 * time.Second
		}
		for range count {
			ent := &domain.Entity{
				ID:           domain.EntityID(nextMobGID()),
				Type:         domain.EntityMob,
				X:            entry.X,
				Y:            entry.Y,
				MoveSpeed:    int(mob.WalkSpeed),
				MobID:        mob.Id,
				HP:           mob.Hp,
				MaxHP:        mob.Hp,
				AI:           uint8(mob.Ai), //nolint:gosec // mob_db AI codes are 0-99; truncation is harmless
				SpawnOriginX: entry.X,
				SpawnOriginY: entry.Y,
				RespawnDelay: respawn,
				Name:         mob.Name,
			}
			if !md.IsWalkable(ent.X, ent.Y) {
				logger.Warn().
					Int32("mob_id", mob.Id).
					Int("x", ent.X).Int("y", ent.Y).
					Msg("zone: mob spawn cell is not walkable; skipping")
				continue
			}
			if err := zoneSvc.AddEntity(ctx, ent); err != nil {
				logger.Warn().Err(err).
					Int32("mob_id", mob.Id).
					Msg("zone: failed to register mob entity")
				continue
			}
			spawned++
		}
	}
	return spawned
}
