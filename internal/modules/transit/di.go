// Package transit is the transit bounded-context module root (cross-map warp).
package transit

import (
	"fmt"
	"log/slog"

	"github.com/samber/do/v2"

	"github.com/bouroo/goAthena/internal/config"
	chardomain "github.com/bouroo/goAthena/internal/modules/character/domain"
	agonesdir "github.com/bouroo/goAthena/internal/modules/transit/agones"
	"github.com/bouroo/goAthena/internal/modules/transit/app"
	"github.com/bouroo/goAthena/internal/modules/transit/domain"
	staticdir "github.com/bouroo/goAthena/internal/modules/transit/static"
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

// RegisterMapDirectory provisions the MapDirectory per zone.directory.mode:
// "local" (default) answers local for every map; "static" resolves the
// deployment-level route table; "agones" resolves Agones GameServers over
// the Kubernetes API (in-cluster credentials, or kubeconfig — the Docker
// Swarm path). A failing non-local directory is a fatal boot error: a fleet
// pod that cannot resolve remote zones must not silently local-route.
func RegisterMapDirectory(inj do.Injector, cfg *config.Config, log *slog.Logger) error {
	mode := cfg.Zone.Directory.Mode
	switch mode {
	case "static":
		dir, err := staticdir.New(cfg.Zone.Directory.Routes)
		if err != nil {
			return fmt.Errorf("zone.directory.routes: %w", err)
		}
		do.ProvideValue(inj, domain.MapDirectory(dir))
	case "agones":
		dir, err := agonesdir.NewFleetDirectory(
			cfg.Zone.Directory.Namespace, cfg.Zone.Directory.Selector,
			cfg.Zone.Directory.SelfName, log)
		if err != nil {
			return fmt.Errorf("zone.directory.mode=agones: %w", err)
		}
		do.ProvideValue(inj, domain.MapDirectory(dir))
	default: // "local"
		do.ProvideValue(inj, domain.MapDirectory(domain.NewLocalDirectory()))
	}
	return nil
}
