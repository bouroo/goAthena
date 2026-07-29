package content

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/rs/zerolog"
	"github.com/samber/do/v2"

	shopdomain "github.com/bouroo/goAthena/internal/modules/commerce/shop/domain"
	"github.com/bouroo/goAthena/internal/modules/content/app"
	"github.com/bouroo/goAthena/internal/modules/content/infra"
	worlddomain "github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/aoi"
)

// Register configures and loads the content module boundaries.
func Register(c do.Injector, npcDataRoot string) error {
	// 1. Resolve necessary cross-module dependencies
	// We depend on world's PlayerRegistry for domain.World implementation.
	players, err := do.Invoke[*worlddomain.PlayerRegistry](c)
	if err != nil {
		return fmt.Errorf("content: failed to resolve PlayerRegistry: %w", err)
	}

	// We depend on world's MapStore and NPCRegistry to publish NPCs.
	mapStore, err := do.Invoke[worlddomain.MapStore](c)
	if err != nil {
		return fmt.Errorf("content: failed to resolve MapStore: %w", err)
	}
	npcRegistry, err := do.Invoke[*worlddomain.NPCRegistry](c)
	if err != nil {
		return fmt.Errorf("content: failed to resolve NPCRegistry: %w", err)
	}

	// 2. Build our internal infrastructure (DialogRegistry, World impl)
	dialogRegistry := infra.NewMemoryDialogRegistry()
	scriptWorld := infra.NewScriptWorld(players)

	// 3. Load and compile all scripts.
	// The NPC data usually lives in data/npc/ (provided by top-level parameter).
	if npcDataRoot == "" { // fallback
		npcDataRoot = filepath.Join("data", "npc")
	}
	// Per the documented graceful-degradation contract (mirroring
	// world/di.go's loadMobDB/loadItemDB), a missing, unreadable, or
	// partially-unparseable script directory logs a warning and the zone
	// still boots with whatever partial ScriptStore LoadScripts returned
	// (possibly empty). One bad script file (e.g. an rAthena feature outside
	// the M11 dialog subset) must never abort server boot.
	lg := warnLogger(c)
	scripts, err := app.LoadScripts(npcDataRoot, lg)
	switch {
	case err != nil:
		lg.Warn().Err(err).Str("dir", npcDataRoot).
			Msg("content: script walk failed; booting with partial script store")
	case scripts == nil || (len(scripts.NPCs) == 0 && len(scripts.Set.Scripts) == 0 && len(scripts.Set.Funcs) == 0):
		lg.Warn().Str("dir", npcDataRoot).
			Msg("content: no scripts loaded; booting with empty script store")
	}

	// 4. Publish NPCs into the world: each placed NPC gets a unique EntityID
	// from the shared registry, is indexed (so the spawn exchange and dialog
	// handler resolve it), and is dropped into the map's AOI grid so players
	// see it on enter. This mirrors MobService.spawnOne's allocate → register
	// → AOI placement; boot is the sole writer, so no per-NPC lock is needed.
	for _, n := range scripts.NPCs {
		// Skip NPCs whose map the world never loaded (e.g. a script referencing
		// a map absent from the index) rather than aborting boot.
		mp, err := mapStore.Load(context.Background(), n.MapName)
		if err != nil {
			lg.Warn().Err(err).Str("npc", n.Name).Str("map", n.MapName).
				Msg("content: skipping NPC, map not in map index")
			continue
		}

		npc := &worlddomain.NPC{
			EntityID:   npcRegistry.NextEntityID(),
			Name:       n.Name,
			MapName:    n.MapName,
			PosX:       n.X,
			PosY:       n.Y,
			Dir:        n.Facing,
			Sprite:     int16(n.SpriteID), //nolint:gosec // G115: NPC view class is a small sprite number
			ScriptName: n.Name,
		}
		if err := npcRegistry.Register(npc); err != nil {
			lg.Warn().Err(err).Str("npc", n.Name).
				Msg("content: failed to register NPC")
			continue
		}
		if err := mp.AOI.AddEntity(&aoi.Entity{
			ID:   npc.EntityID,
			Type: aoi.EntityNPC,
			X:    int(npc.PosX),
			Y:    int(npc.PosY),
		}); err != nil {
			// Roll back the registry index so a half-placed NPC never resolves.
			npcRegistry.Unregister(npc.EntityID)
			lg.Warn().Err(err).Str("npc", n.Name).
				Msg("content: failed to place NPC in AOI")
		}
	}

	// 5. Load shop definitions and publish shop NPCs into the world.
	// Shop data lives in data/shop/ (sibling of the script data dir). The
	// same graceful-degradation contract applies: a missing, unreadable, or
	// partially-unparseable shop directory logs a warning and the zone boots
	// without any shop NPCs (the shopNPCs map stays empty; click-to-shop
	// routes fall through to the script path and harmlessly close).
	shopRoot := filepath.Join(filepath.Dir(npcDataRoot), "shop")
	shopDefs, shopErr := app.LoadShopDefs(shopRoot, lg)
	if shopErr != nil {
		lg.Warn().Err(shopErr).Str("dir", shopRoot).
			Msg("content: shop walk failed; booting with empty shop set")
		shopDefs = nil
	}
	shopEntityIDs, shopCatalog := publishShopNPCs(context.Background(), shopDefs, mapStore, npcRegistry, lg)

	// 6. Construct handlers
	dialogService := app.NewDialogService(scriptWorld, dialogRegistry, scripts, shopEntityIDs)
	contactH := app.NewContactNPCHandler(dialogService)
	nextH := app.NewReqNextScriptHandler(dialogService)
	chooseH := app.NewChooseMenuHandler(dialogService)
	closeH := app.NewCloseDialogHandler(dialogService)

	// 7. Provide to the injector
	do.ProvideValue(c, dialogRegistry)
	do.ProvideValue(c, scriptWorld)
	do.ProvideValue(c, scripts)

	do.ProvideValue(c, contactH)
	do.ProvideValue(c, nextH)
	do.ProvideValue(c, chooseH)
	do.ProvideValue(c, closeH)

	// Shop catalog is provided so the commerce module's future shop-buy /
	// shop-sell handlers (U4) can resolve a click's NPC EntityID to the
	// catalog without re-walking YAML. The catalog is not consumed by the
	// content module itself.
	do.ProvideValue[shopdomain.ShopCatalog](c, shopCatalog)

	return nil
}

// publishShopNPCs allocates a fresh EntityID per shop def, registers the NPC
// in both the world registry and the map's AOI grid, and accumulates a
// (1) set of shop EntityIDs used by ContactNPCHandler to short-circuit the
// dialog flow and a (2) commerce ShopCatalog keyed by EntityID. Per-shop
// failures (missing map, registry collision, AOI placement error) are
// logged as warnings and skipped, mirroring the script-NPC publish loop:
// the boot must never abort on a single malformed definition.
func publishShopNPCs(
	ctx context.Context,
	defs []app.ShopDef,
	mapStore worlddomain.MapStore,
	npcRegistry *worlddomain.NPCRegistry,
	lg *zerolog.Logger,
) (map[uint32]bool, shopdomain.ShopCatalog) {
	shopNPCs := map[uint32]bool{}
	catalog := shopdomain.NewMemoryShopCatalog()
	if len(defs) == 0 {
		return shopNPCs, catalog
	}

	for _, sd := range defs {
		mp, err := mapStore.Load(ctx, sd.Map)
		if err != nil {
			lg.Warn().Err(err).Str("shop", sd.Name).Str("map", sd.Map).
				Msg("content: skipping shop, map not in map index")
			continue
		}

		npc := &worlddomain.NPC{
			EntityID: npcRegistry.NextEntityID(),
			Name:     sd.Name,
			MapName:  sd.Map,
			PosX:     int16(sd.X),      //nolint:gosec // G115: X is a tile coord from YAML
			PosY:     int16(sd.Y),      //nolint:gosec // G115: Y is a tile coord from YAML
			Dir:      uint8(sd.Facing), //nolint:gosec // G115: Facing is a 0–7 direction
			Sprite:   int16(sd.Sprite), //nolint:gosec // G115: sprite ID is a small int
		}
		if err := npcRegistry.Register(npc); err != nil {
			lg.Warn().Err(err).Str("shop", sd.Name).
				Msg("content: failed to register shop NPC")
			continue
		}
		if err := mp.AOI.AddEntity(&aoi.Entity{
			ID:   npc.EntityID,
			Type: aoi.EntityNPC,
			X:    int(npc.PosX),
			Y:    int(npc.PosY),
		}); err != nil {
			npcRegistry.Unregister(npc.EntityID)
			lg.Warn().Err(err).Str("shop", sd.Name).
				Msg("content: failed to place shop NPC in AOI")
			continue
		}

		shopNPCs[uint32(npc.EntityID)] = true

		items := make([]shopdomain.ShopItem, 0, len(sd.Items))
		for _, it := range sd.Items {
			items = append(items, shopdomain.ShopItem{NameID: it.NameID, Price: it.Price})
		}
		catalog.Add(shopdomain.Shop{
			NPCID: uint32(npc.EntityID),
			Name:  sd.Name,
			Items: items,
		})
	}
	return shopNPCs, catalog
}

// warnLogger resolves the process logger for non-fatal warnings; mirrors
// internal/modules/world/di.go's warnLogger. If the logger is not on the
// injector it falls back to zerolog.Nop so a warning is never dropped purely
// because telemetry misconfigured, and so the content module never forces a
// hard dependency on the logger being registered first.
func warnLogger(c do.Injector) *zerolog.Logger {
	if lg, err := do.Invoke[*zerolog.Logger](c); err == nil {
		return lg
	}
	fallback := zerolog.Nop()
	return &fallback
}
