// Package world is the composition point for the world bounded context's DI. It
// resolves the account Authenticator (the CZ_ENTER trust anchor) and provides
// the map-enter handler for the composition root to thread into the gateway's
// map-role dispatch table.
//
// This file lives at the module root rather than under app/ or infra/ because it
// must import its own app layer and cross-module account/domain ports. The
// clean-architecture guard (internal/app/arch_test.go) forbids an app-layer file
// from importing another module, but a module-root file is the designated wiring
// seam and is exempt, and cross-module *domain* imports are permitted.
package world

import (
	"context"
	"fmt"

	"github.com/samber/do/v2"

	accountdomain "github.com/bouroo/goAthena/internal/modules/account/domain"
	"github.com/bouroo/goAthena/internal/modules/world/app"
)

// Register builds the world bounded context. M3 provides the CZ_ENTER handler
// (the map-enter trust gate) over the account Authenticator. M4 layers the
// entity registry, AOI, and tick on top. ctx is accepted to match the samber/do
// v2 Register convention but is unused.
func Register(_ context.Context, c do.Injector) error {
	auth, err := do.Invoke[accountdomain.Authenticator](c)
	if err != nil {
		return fmt.Errorf("world: resolve account authenticator: %w", err)
	}
	do.ProvideValue(c, app.NewMapEnterHandler(auth, app.DefaultSpawn))
	return nil
}
