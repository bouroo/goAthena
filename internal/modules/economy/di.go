// Package economy is the economy bounded-context module root.
package economy

import (
	"github.com/samber/do/v2"

	chardomain "github.com/bouroo/goAthena/internal/modules/character/domain"
	"github.com/bouroo/goAthena/internal/modules/economy/app"
)

// Register provisions the EconomyService into the injector. It resolves the
// character repo from DI (economy writes zeny via the char table).
func Register(inj do.Injector) {
	do.Provide(inj, func(i do.Injector) (*app.EconomyService, error) {
		repo := do.MustInvoke[chardomain.CharacterRepository](i)
		return app.NewEconomyService(repo), nil
	})
}
