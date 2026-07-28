package content

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/rs/zerolog"
	"github.com/samber/do/v2"

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

	// 5. Construct handlers
	dialogService := app.NewDialogService(scriptWorld, dialogRegistry, scripts)
	contactH := app.NewContactNPCHandler(dialogService)
	nextH := app.NewReqNextScriptHandler(dialogService)
	chooseH := app.NewChooseMenuHandler(dialogService)
	closeH := app.NewCloseDialogHandler(dialogService)

	// 6. Provide to the injector
	do.ProvideValue(c, dialogRegistry)
	do.ProvideValue(c, scriptWorld)
	do.ProvideValue(c, scripts)

	do.ProvideValue(c, contactH)
	do.ProvideValue(c, nextH)
	do.ProvideValue(c, chooseH)
	do.ProvideValue(c, closeH)

	return nil
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
