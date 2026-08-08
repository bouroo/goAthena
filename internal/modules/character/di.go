// Package character is the character bounded-context module root. Register
// provisions the GORM character repo, the Valkey session store, and the
// CharService into the DI injector.
package character

import (
	"time"

	"github.com/samber/do/v2"
	"github.com/valkey-io/valkey-go"
	"gorm.io/gorm"

	"github.com/bouroo/goAthena/internal/modules/character/app"
	"github.com/bouroo/goAthena/internal/modules/character/domain"
	"github.com/bouroo/goAthena/internal/modules/character/infra"
)

// Register provisions the character module. The repo resolves the process-wide
// *gorm.DB; the session store resolves the process-wide valkey.Client.
func Register(inj do.Injector, maxChars int) {
	do.Provide(inj, func(i do.Injector) (*infra.GORMCharacterRepository, error) {
		gdb := do.MustInvoke[*gorm.DB](i)
		return infra.NewGORMCharacterRepository(gdb), nil
	})
	do.Provide(inj, func(i do.Injector) (*infra.ValkeySessionStore, error) {
		vc := do.MustInvoke[valkey.Client](i)
		return infra.NewValkeySessionStore(vc, 5*time.Minute), nil
	})
	do.Provide(inj, func(i do.Injector) (*app.CharService, error) {
		repo := do.MustInvoke[*infra.GORMCharacterRepository](i)
		sess := do.MustInvoke[*infra.ValkeySessionStore](i)
		return app.NewCharService(repo, sess, maxChars), nil
	})
	do.Provide(inj, func(i do.Injector) (domain.SessionStore, error) {
		return do.MustInvoke[*infra.ValkeySessionStore](i), nil
	})
	do.Provide(inj, func(i do.Injector) (domain.CharacterRepository, error) {
		return do.MustInvoke[*infra.GORMCharacterRepository](i), nil
	})
}
