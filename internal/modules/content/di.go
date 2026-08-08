// Package content is the content bounded-context module root (script VM ↔ dialog).
package content

import (
	"log/slog"

	"github.com/samber/do/v2"

	"github.com/bouroo/goAthena/internal/modules/content/app"
	"github.com/bouroo/goAthena/internal/modules/content/domain"
	"github.com/bouroo/goAthena/internal/modules/content/infra"
)

// Register provisions the NPC store and the script Engine. The Engine starts
// with no compiled scripts (NPC script files load once a data path is wired);
// clicks are a no-op until scripts are loaded. The NPC store is exposed as the
// domain.NPCStore port so the world NPC-spawn path can populate it.
func Register(inj do.Injector) {
	do.Provide(inj, func(_ do.Injector) (*infra.MemoryNPCStore, error) {
		return infra.NewMemoryNPCStore(), nil
	})
	do.Provide(inj, func(i do.Injector) (*app.Engine, error) {
		npcs := do.MustInvoke[*infra.MemoryNPCStore](i)
		log := do.MustInvoke[*slog.Logger](i)
		return app.NewEngine(nil, npcs, log), nil // scripts wired when the data loader lands
	})
	do.Provide(inj, func(i do.Injector) (domain.NPCStore, error) {
		return do.MustInvoke[*infra.MemoryNPCStore](i), nil
	})
}
