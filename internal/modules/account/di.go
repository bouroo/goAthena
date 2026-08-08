// Package account is the account bounded-context module root. Its Register
// wires the GORM account repository and login AuthService into the DI injector
// so the gateway login listener (M1b) can resolve domain.Authenticator.
package account

import (
	"github.com/samber/do/v2"
	"gorm.io/gorm"

	"github.com/bouroo/goAthena/internal/modules/account/app"
	"github.com/bouroo/goAthena/internal/modules/account/domain"
	"github.com/bouroo/goAthena/internal/modules/account/infra"
)

// Register provisions the account module into the injector. The repository
// resolves the process-wide *gorm.DB; the AuthService is exposed as the
// domain.Authenticator port. useMD5 mirrors rAthena use_MD5_passwords.
func Register(inj do.Injector, useMD5 bool) {
	do.Provide(inj, func(i do.Injector) (*infra.GORMAccountRepository, error) {
		gdb := do.MustInvoke[*gorm.DB](i)
		return infra.NewGORMAccountRepository(gdb), nil
	})
	do.Provide(inj, func(i do.Injector) (domain.Authenticator, error) {
		repo := do.MustInvoke[*infra.GORMAccountRepository](i)
		return app.NewAuthService(repo, useMD5), nil
	})
}
