// Package party is the party bounded-context module root.
//
// Party groups players for EXP sharing and loot rules. Membership is stored on
// the `char` row (rAthena shape), so this module reads and writes the char table
// through its own repository rather than through the character module.
package party

import (
	"github.com/samber/do/v2"
	"gorm.io/gorm"

	"github.com/bouroo/goAthena/internal/modules/social/party/app"
	"github.com/bouroo/goAthena/internal/modules/social/party/domain"
	"github.com/bouroo/goAthena/internal/modules/social/party/infra"
)

// Register provisions the GORM party repo and the PartyService into the
// injector. The GORM handle is resolved lazily so a down database surfaces as a
// resolution error at use time rather than at boot.
func Register(inj do.Injector) {
	do.Provide(inj, func(i do.Injector) (*infra.GORMPartyRepository, error) {
		gdb := do.MustInvoke[*gorm.DB](i)
		return infra.NewGORMPartyRepository(gdb), nil
	})
	do.Provide(inj, func(i do.Injector) (domain.PartyRepository, error) {
		return do.MustInvoke[*infra.GORMPartyRepository](i), nil
	})
	do.Provide(inj, func(i do.Injector) (*app.PartyService, error) {
		repo := do.MustInvoke[*infra.GORMPartyRepository](i)
		return app.NewPartyService(repo), nil
	})
}
