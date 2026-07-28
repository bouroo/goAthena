package content

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/bouroo/goAthena/internal/modules/content/app"
	"github.com/bouroo/goAthena/internal/modules/content/domain"
	"github.com/bouroo/goAthena/internal/modules/content/infra"
	worlddomain "github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/aoi"
	"github.com/samber/do/v2"
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

	// 3. Load and compile all scripts
	// The NPC data usually lives in data/npc/ (provided by top-level parameter).
	if npcDataRoot == "" { // fallback
		npcDataRoot = filepath.Join("data", "npc")
	}
	scripts, err := app.LoadScripts(npcDataRoot)
	if err != nil {
		return fmt.Errorf("content: failed to load scripts: %w", err)
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
			fmt.Printf("content: skipping NPC %q, map %q not in map index: %v\n", n.Name, n.MapName, err)
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
			fmt.Printf("content: failed to register NPC %q: %v\n", n.Name, err)
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
			fmt.Printf("content: failed to place NPC %q in AOI: %v\n", n.Name, err)
		}
	}

	// 5. Construct handlers
	dialogService := app.NewDialogService(scriptWorld, dialogRegistry, scripts)
	contactH := app.NewContactNPCHandler(dialogService)
	nextH := app.NewReqNextScriptHandler(dialogService)
	chooseH := app.NewChooseMenuHandler(dialogService)
	closeH := app.NewCloseDialogHandler(dialogService)

	// 6. Provide to the injector
	do.ProvideValue(c, domain.DialogRegistry(dialogRegistry))
	do.ProvideValue(c, domain.World(scriptWorld))
	do.ProvideValue(c, scripts)

	do.ProvideValue(c, contactH)
	do.ProvideValue(c, nextH)
	do.ProvideValue(c, chooseH)
	do.ProvideValue(c, closeH)

	return nil
}
