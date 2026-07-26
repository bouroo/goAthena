// Package account is the composition point for the account bounded context's
// DI. It wires the GORM account repository and the Valkey session store into
// the AuthService and the CA_LOGIN gateway handler, then provides the
// domain.Authenticator port and the concrete *app.CALoginHandler for the
// composition root to thread into the gateway dispatch tables.
//
// This file lives at the module root rather than under app/ or infra/ because
// it must import both its own app and infra layers. The clean-architecture
// guard (internal/app/arch_test.go) forbids an app-layer file from importing
// infra, but a module-root file is the designated wiring seam and is exempt.
package account

import (
	"context"
	"fmt"

	"github.com/samber/do/v2"
	valkeygo "github.com/valkey-io/valkey-go"
	"gorm.io/gorm"

	"github.com/bouroo/goAthena/internal/modules/account/app"
	"github.com/bouroo/goAthena/internal/modules/account/domain"
	"github.com/bouroo/goAthena/internal/modules/account/infra"
)

// Register builds the account bounded context over real infrastructure: a GORM
// AccountRepository and a Valkey-backed SessionStore, wired into the AuthService
// and the CA_LOGIN handler. It provides domain.Authenticator (the inbound port)
// and *app.CALoginHandler (the gateway contribution) on the injector. ctx is
// accepted to match the samber/do v2 Register convention but is unused — the
// adapters derive a per-request context from the handler call.
func Register(_ context.Context, c do.Injector) error {
	db, err := do.Invoke[*gorm.DB](c)
	if err != nil {
		return fmt.Errorf("account: resolve gorm db: %w", err)
	}
	accounts := infra.NewGORMAccountRepository(db)

	client, err := do.Invoke[valkeygo.Client](c)
	if err != nil {
		return fmt.Errorf("account: resolve valkey client: %w", err)
	}
	sessions := infra.NewValkeySessionStore(client)

	// systemClock and cryptoMinter are the documented production defaults; pass
	// nil so NewAuthService selects them. Tests bypass Register and inject
	// fakes directly into NewAuthService.
	auth := app.NewAuthService(accounts, sessions, nil, nil)
	do.ProvideValue(c, domain.Authenticator(auth))

	// charServers is the trailing server list embedded in every AC_ACCEPT_LOGIN.
	// Login and char are multiplexed on one listener (the role advances
	// RoleLogin → RoleChar in-connection), so the client proceeds straight to
	// CH_ENTER on the same connection without consulting this list; an empty list
	// is correct. The login→char transition needs no advertisement. (The
	// char→map transition is different: it is a reconnect, and CH_SELECT_CHAR
	// advertises the map listener's address in HC_NOTIFY_ZONESVR, sourced from
	// cfg.Gateway.MapAddr in the character module.)
	login := app.NewCALoginHandler(auth, nil)
	do.ProvideValue(c, login)

	return nil
}
