// Package guild is the guild bounded-context module root.
//
// Guilds group players under a master for chat, community, and (later) castle
// wars and skills. Membership is stored on the `char` row (rAthena shape), so
// this module reads and writes the char table through its own repository
// rather than through the character module.
package guild

import (
	"github.com/samber/do/v2"
	"gorm.io/gorm"

	"github.com/bouroo/goAthena/internal/modules/social/guild/app"
	"github.com/bouroo/goAthena/internal/modules/social/guild/domain"
	"github.com/bouroo/goAthena/internal/modules/social/guild/infra"
)

// Register provisions the GORM guild repo and the GuildService into the
// injector. The GORM handle is resolved lazily so a down database surfaces as a
// resolution error at use time rather than at boot.
func Register(inj do.Injector) {
	do.Provide(inj, func(i do.Injector) (*infra.GORMGuildRepository, error) {
		gdb := do.MustInvoke[*gorm.DB](i)
		return infra.NewGORMGuildRepository(gdb), nil
	})
	do.Provide(inj, func(i do.Injector) (domain.GuildRepository, error) {
		return do.MustInvoke[*infra.GORMGuildRepository](i), nil
	})
	do.Provide(inj, func(i do.Injector) (*app.GuildService, error) {
		repo := do.MustInvoke[*infra.GORMGuildRepository](i)
		return app.NewGuildService(repo), nil
	})
}
