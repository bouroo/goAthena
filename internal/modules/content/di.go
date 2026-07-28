package content

import (
	"fmt"
	"path/filepath"

	"github.com/bouroo/goAthena/internal/modules/content/app"
	"github.com/bouroo/goAthena/internal/modules/content/domain"
	"github.com/bouroo/goAthena/internal/modules/content/infra"
	worldapp "github.com/bouroo/goAthena/internal/modules/world/app"
	worlddomain "github.com/bouroo/goAthena/internal/modules/world/domain"
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
	// We need the NPC service to spawn them into the AOI
	npcSvc, err := do.Invoke[*worldapp.NPCService](c)
	if err != nil {
		return fmt.Errorf("content: failed to resolve NPCService: %w", err)
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

	// 4. Publish NPCs into the world
	for _, n := range scripts.NPCs {
		// Verify the map exists
		if _, ok := mapStore.Get(n.MapName); !ok {
			fmt.Printf("content: skipping NPC %q, map %q not in map index\n", n.Name, n.MapName)
			continue
		}

		// Register the NPC entity
		npcEntity := npcRegistry.Allocate(n.Name, n.MapName, n.X, n.Y, n.Facing, n.SpriteID)

		// Insert into the AOI grid so players see them immediately on enter
		if err := npcSvc.SpawnNPC(npcEntity); err != nil {
			fmt.Printf("content: failed to spawn NPC %q: %v\n", n.Name, err)
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
