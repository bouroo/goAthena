// Package content is the content bounded-context module root (script VM ↔ dialog).
package content

import (
	"log/slog"

	"github.com/samber/do/v2"

	"github.com/bouroo/goAthena/internal/modules/content/app"
	"github.com/bouroo/goAthena/internal/modules/content/domain"
	"github.com/bouroo/goAthena/internal/modules/content/infra"
	worldapp "github.com/bouroo/goAthena/internal/modules/world/app"
)

// Register provisions the NPC and shop stores and the script Engine. The Engine
// starts with no compiled scripts (NPC script files load once a data path is
// wired); clicks are a no-op until scripts are loaded. The WorldService is
// resolved lazily as the effect-builtins (warp/heal) world port; a resolve
// failure leaves world nil so the server still boots. The NPC store is exposed
// as the domain.NPCStore port and the shop store as domain.ShopStore so the
// world NPC-spawn path and commerce shop seed can populate them.
func Register(inj do.Injector) {
	do.Provide(inj, func(_ do.Injector) (*infra.MemoryNPCStore, error) {
		return infra.NewMemoryNPCStore(), nil
	})
	do.Provide(inj, func(_ do.Injector) (*infra.MemoryShopStore, error) {
		return infra.NewMemoryShopStore(), nil
	})
	do.Provide(inj, func(i do.Injector) (*app.Engine, error) {
		npcs := do.MustInvoke[*infra.MemoryNPCStore](i)
		log := do.MustInvoke[*slog.Logger](i)
		var world domain.ScriptWorld
		if ws, err := do.Invoke[*worldapp.WorldService](i); err == nil {
			world = ws
		}
		return app.NewEngine(nil, npcs, world, log), nil // scripts wired when the data loader lands
	})
	do.Provide(inj, func(i do.Injector) (domain.NPCStore, error) {
		return do.MustInvoke[*infra.MemoryNPCStore](i), nil
	})
	do.Provide(inj, func(i do.Injector) (domain.ShopStore, error) {
		return do.MustInvoke[*infra.MemoryShopStore](i), nil
	})
}
