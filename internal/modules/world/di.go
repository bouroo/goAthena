// Package world is the composition point for the world bounded context's DI. It
// resolves the account Authenticator (the CZ_ENTER trust anchor) and provides
// the map-enter handler for the composition root to thread into the gateway's
// map-role dispatch table.
//
// This file lives at the module root rather than under app/ or infra/ because it
// must import its own app layer and cross-module account/domain ports. The
// clean-architecture guard (internal/app/arch_test.go) forbids an app-layer file
// from importing another module, but a module-root file is the designated wiring
// seam and is exempt, and cross-module *domain* imports are permitted.
package world

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/rs/zerolog"
	"github.com/samber/do/v2"

	"github.com/bouroo/goAthena/internal/config"
	accountdomain "github.com/bouroo/goAthena/internal/modules/account/domain"
	chardomain "github.com/bouroo/goAthena/internal/modules/character/domain"
	invdomain "github.com/bouroo/goAthena/internal/modules/inventory/domain"
	"github.com/bouroo/goAthena/internal/modules/world/app"
	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/internal/modules/world/infra"
	"github.com/bouroo/goAthena/pkg/ro/itemdb"
	"github.com/bouroo/goAthena/pkg/ro/mobdb"
	"github.com/bouroo/goAthena/pkg/ro/mode"
	"github.com/bouroo/goAthena/pkg/ro/statcalc"
)

// Register builds the world bounded context. M3 provides the CZ_ENTER handler
// (the map-enter trust gate) over the account Authenticator; M4a adds the
// filesystem-backed MapStore that loads the per-map AOI grid and A* pathfinder.
// M4b layers the spawn-on-enter flow: the in-process PlayerRegistry and the
// SpawnService the gate calls after ZC_ACCEPT_ENTER. M5 adds the mob corpus:
// the MobRegistry + MobService that populate the world at boot and drive the
// idle-wander tick; SpawnAll runs eagerly here so mobs exist before the first
// enter-world spawn exchange.
func Register(ctx context.Context, c do.Injector) error {
	auth, err := do.Invoke[accountdomain.Authenticator](c)
	if err != nil {
		return fmt.Errorf("world: resolve account authenticator: %w", err)
	}

	cfg, err := do.Invoke[*config.Config](c)
	if err != nil {
		return fmt.Errorf("world: resolve config: %w", err)
	}
	// The MapStore is constructed now but loads lazily: Load only reads
	// .gat/.rsw when an entity enters a map (M4b), so a missing or relative
	// map_dir cannot fail server boot. The dedicated map-listener listeners
	// therefore still bind and the M3 e2e's app.Serve still comes up.
	maps := domain.MapStore(infra.NewFileMapStore(cfg.Zone.MapDir))
	do.ProvideValue(c, maps)

	// M4b: the character repository (provided by the character module as its
	// domain port) drives the spawn lookup; the PlayerRegistry is the live-PC
	// index the SpawnService and the future disconnect/movement paths share.
	// Provide it on the injector so M4c+ (movement, tick) resolve the same
	// instance rather than each building their own.
	chars, err := do.Invoke[chardomain.CharacterRepository](c)
	if err != nil {
		return fmt.Errorf("world: resolve character repository: %w", err)
	}
	registry := domain.NewPlayerRegistry()
	do.ProvideValue(c, registry)

	// M5: the mob registry + spawn corpus. mob_db is optional per ZoneConfig —
	// an empty path or an unreadable file logs a warning and the zone boots with
	// no mobs (db stays nil; SpawnAll/Run become no-ops), matching the documented
	// "misconfigured zone still boots" contract. The logger is resolved
	// defensively: telemetry.Register has already provided it by the time
	// world.Register runs, but a missing logger must not itself fail boot.
	mobs := domain.NewMobRegistry()
	do.ProvideValue(c, mobs)
	mobDB := loadMobDB(c, cfg.Zone)

	// M8: resolve the game-mode formula set once. zone.renewal selects Renewal vs
	// Pre-Renewal; every status/damage use case receives this FormulaSet instead
	// of branching on the mode itself, so no formula logic leaks into a bounded
	// context. The Registry dispatch keeps the two sets behind one seam.
	gm := mode.PreRenewal
	if cfg.Zone.Renewal {
		gm = mode.Renewal
	}
	fs := statcalc.NewRegistry().Get(gm)

	mobSvc := app.NewMobService(mobs, registry, maps, mobDB, cfg.Zone.MobSpawnsPath, app.SystemClock(), cfg.Zone.TickRate, nil)
	// Eagerly populate the world so mobs exist before the first player can
	// connect. A spawn failure is non-fatal (logged): the maps that did resolve
	// keep their mobs, the tick still runs, and the boot continues.
	if err := mobSvc.SpawnAll(ctx); err != nil {
		warnLogger(c).Warn().Err(err).Msg("world: mob spawn failed; some maps may be empty")
	}
	do.ProvideValue(c, mobSvc)

	spawner := app.NewSpawnService(chars, maps, registry, mobs, fs)
	do.ProvideValue(c, app.NewMapEnterHandler(auth, app.DefaultSpawn, spawner))

	// M4c: the movement worker. A single MoveService owns every map's
	// pathfinder — its Run goroutine is the sole caller of FindPath, so the
	// pathfinder's mutable scratch buffers are never raced (the single-goroutine
	// contract world/domain/map.go documents). queueSize 256 bounds the backlog;
	// a full queue drops the oldest pending move (backpressure, not OOM). The
	// worker is started as a Runnable from the composition root so SIGTERM
	// reaches it; here we only provide the service + handler for resolution.
	mover := app.NewMoveService(registry, maps, app.SystemClock(), 256)
	do.ProvideValue(c, mover)
	do.ProvideValue(c, app.NewMoveHandler(mover))

	// M6/M9c-1: the combat service. It resolves the attacker from the player
	// registry, the target from the mob registry, stats + base EXP + the drop
	// table from the mob_db loaded above (may be nil when mob_db is unconfigured —
	// combat then falls back to a flat hit and awards no EXP and no drops), and
	// broadcasts damage/vanish/floor-item-drop through the map AOI. The character
	// repository (chars, resolved above as the spawn lookup) is reused as the
	// progression store for the EXP/level read-modify-write and persist on kill —
	// CharacterRepository satisfies ProgressionStore structurally (GetByID +
	// SaveProgression). itemDB resolves drop AegisNames → item id/type (may be nil
	// when item_db is unconfigured — kills then award no ground drops); floorItems
	// is the dropped-item index the M9c-1 drop path registers into. The drop
	// roller draws from the shared stepSource RNG seam; a nil rng defaults to the
	// global source inside NewCombatService. Like movement it resolves
	// synchronously on the conn goroutine; the handler is provided for the
	// composition root to thread into the map-role dispatch table.
	itemDB := loadItemDB(c, cfg.Zone)
	floorItems := domain.NewFloorItemRegistry()
	combat := app.NewCombatService(registry, mobs, maps, mobDB, chars, app.SystemClock(), app.SystemRespawnScheduler{}, fs, gm, itemDB, floorItems, nil)
	do.ProvideValue(c, combat)
	do.ProvideValue(c, app.NewActionHandler(combat))

	// M10a: the pickup service. It shares the combat floor-item index + player
	// registry + map store + item_db, and adds the inventory port the bag
	// mutation goes through. The inventory module registers its repository under
	// this domain port (the interface-cast widen character/di.go uses), so the
	// world module resolves it without importing inventory/infra — the cross-
	// module license arch_test grants. inventory is a hard dependency (the loot
	// loop is a core feature and the table always exists once migrated), so a
	// missing registration fails boot with a clear error rather than silently
	// disabling pickups. The clock stamps the pickup animation's ZC_NOTIFY_ACT,
	// the same SystemClock movement stamps; the handler is provided for the
	// composition root to thread into the map-role dispatch table.
	invRepo, err := do.Invoke[invdomain.InventoryRepository](c)
	if err != nil {
		return fmt.Errorf("world: resolve inventory repository: %w", err)
	}
	pickup := app.NewPickupService(floorItems, registry, maps, invRepo, itemDB, app.SystemClock())
	do.ProvideValue(c, app.NewPickupHandler(pickup))

	// M7: player-expression handlers. The time handler echoes the shared server
	// tick (ZC_NOTIFY_TIME) against the same Clock movement stamps MoveStartTime
	// with. The social service owns the change-direction + emotion broadcasts;
	// it shares the movement/spawn collaborators (registry + map store) and adds
	// nothing else, so one AOI-walk path serves dir/emotion. All three resolve
	// synchronously on the conn goroutine and are provided here for the
	// composition root to thread into the map-role dispatch table.
	social := app.NewSocialService(registry, maps)
	do.ProvideValue(c, app.NewTimeHandler(app.SystemClock()))
	do.ProvideValue(c, app.NewChangeDirHandler(social))
	do.ProvideValue(c, app.NewEmotionHandler(social))

	// M7d: the char-select-return handler. CZ_RESTART type 1 sends
	// ZC_RESTART_ACK, unregisters the player (so a fresh CZ_ENTER re-registers
	// cleanly rather than hitting ErrPlayerAlreadyRegistered), removes its AOI
	// entity, and closes the map conn so the client reconnects to the char
	// listener. It shares the registry + map-store collaborators social uses.
	restart := app.NewRestartService(registry, maps)
	do.ProvideValue(c, app.NewRestartHandler(restart))
	return nil
}

// loadMobDB reads mob_db.yml into a mobdb.Registry. Resolution is two-tier: an
// explicit zone.mob_db_path override wins (the committed ./data/mob_db slim set
// pins this for CI boots without the rAthena submodule); when the override is
// empty, the loader resolves <db_root>/mob_db.yml from the rAthena fork
// (db/re or db/pre-re by zone.renewal). An empty path with no fork file yields a
// nil registry (no mobs). A read or parse failure is logged but not returned —
// the zone boots with no mobs rather than aborting — so a corrupt or missing
// mob_db degrades gracefully instead of failing the whole process.
func loadMobDB(c do.Injector, zone config.ZoneConfig) *mobdb.Registry {
	path := zone.MobDBPath
	if path == "" {
		path = filepath.Join(zone.DBRoot(), "mob_db.yml")
	}
	reg, err := mobdb.LoadFile(path)
	if err != nil {
		warnLogger(c).Warn().Err(err).Str("path", path).
			Msg("world: mob_db load failed; mob spawning disabled")
		return nil
	}
	return reg
}

// loadItemDB reads the rAthena item_db sub-files into one itemdb.Registry.
// Resolution is two-tier: an explicit zone.item_db_path override wins (pointed
// at a single ITEM_DB v3 file); when the override is empty, the loader resolves
// the fork's item_db_{usable,equip,etc}.yml sub-files from <db_root> (db/re or
// db/pre-re by zone.renewal) — the fork ships item_db.yml as a router shell with
// no Body, so the sub-files are where the entries live. itemdb.LoadFiles merges
// the three and tolerates absent files (ENOENT) so a partial fork subtree still
// yields whatever items are present. An empty override with no fork files yields a
// non-nil empty registry (drops disabled, kills still playable). A read or parse
// failure is logged but not returned — the zone boots with no item drops rather
// than aborting — mirroring loadMobDB's graceful-degradation contract.
func loadItemDB(c do.Injector, zone config.ZoneConfig) *itemdb.Registry {
	paths := itemDBPaths(zone)
	reg, err := itemdb.LoadFiles(paths...)
	if err != nil {
		warnLogger(c).Warn().Err(err).Strs("paths", paths).
			Msg("world: item_db load failed; item drops disabled")
		return nil
	}
	return reg
}

// itemDBPaths resolves the item_db file list for a zone. An explicit override is
// used verbatim (a single-file deployment); otherwise the fork's three sub-files
// under <db_root> are returned in a stable order. DBRoot already folds in the
// re/pre-re branch, so no renewal suffix is added here.
func itemDBPaths(zone config.ZoneConfig) []string {
	if zone.ItemDBPath != "" {
		return []string{zone.ItemDBPath}
	}
	root := zone.DBRoot()
	return []string{
		filepath.Join(root, "item_db_usable.yml"),
		filepath.Join(root, "item_db_equip.yml"),
		filepath.Join(root, "item_db_etc.yml"),
	}
}

// warnLogger resolves the process logger for non-fatal warnings; if the logger
// is not on the injector it falls back to zerolog's default stderr logger so a
// warning is never dropped purely because telemetry misconfigured.
func warnLogger(c do.Injector) *zerolog.Logger {
	if lg, err := do.Invoke[*zerolog.Logger](c); err == nil {
		return lg
	}
	fallback := zerolog.Nop()
	return &fallback
}
