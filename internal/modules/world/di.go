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

	"github.com/bouroo/goAthena/internal/config"
	accountdomain "github.com/bouroo/goAthena/internal/modules/account/domain"
	"github.com/bouroo/goAthena/internal/modules/world/app"
	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/internal/modules/world/infra"
)

// Register builds the world bounded context. M3 provides the CZ_ENTER handler
// (the map-enter trust gate) over the account Authenticator; M4a adds the
// filesystem-backed MapStore that loads the per-map AOI grid and A* pathfinder.
// M4b+ layer entity spawn, AOI broadcast, and the tick on top. ctx is accepted
// to match the samber/do v2 Register convention but is unused — the map is
// loaded lazily on first demand (spawn-on-enter), not eagerly at Register.
func Register(_ context.Context, c do.Injector) error {
	auth, err := do.Invoke[accountdomain.Authenticator](c)
	if err != nil {
		return fmt.Errorf("world: resolve account authenticator: %w", err)
	}
	do.ProvideValue(c, app.NewMapEnterHandler(auth, app.DefaultSpawn))

	cfg, err := do.Invoke[*config.Config](c)
	if err != nil {
		return fmt.Errorf("world: resolve config: %w", err)
	}
	// The MapStore is constructed now but loads lazily: Load only reads
	// .gat/.rsw when an entity enters a map (M4b), so a missing or relative
	// map_dir cannot fail server boot. The dedicated map-listener listeners
	// therefore still bind and the M3 e2e's app.Serve still comes up.
	do.ProvideValue(c, domain.MapStore(infra.NewFileMapStore(cfg.Zone.MapDir)))
	return nil
}
