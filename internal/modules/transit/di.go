// Package transit is the transit bounded-context module root (cross-map warp).
package transit

import (
	"github.com/samber/do/v2"

	chardomain "github.com/bouroo/goAthena/internal/modules/character/domain"
	"github.com/bouroo/goAthena/internal/modules/transit/app"
	"github.com/bouroo/goAthena/internal/modules/transit/domain"
	worldapp "github.com/bouroo/goAthena/internal/modules/world/app"
)

// Register provisions the TransitService into the injector.
func Register(inj do.Injector) {
	do.Provide(inj, func(i do.Injector) (*app.TransitService, error) {
		world := do.MustInvoke[*worldapp.WorldService](i)
		repos := do.MustInvoke[chardomain.CharacterRepository](i)
		return app.NewTransitService(world, repos), nil
	})
}

// RegisterMapDirectory provisions the v0 single-zone MapDirectory (every map
// resolves local). M13's fleet directory replaces the provider, not the
// gateway seam.
func RegisterMapDirectory(inj do.Injector) {
	do.Provide(inj, func(_ do.Injector) (domain.MapDirectory, error) {
		return domain.NewLocalDirectory(), nil
	})
}
