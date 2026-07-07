package assets

import (
	"fmt"

	"github.com/rs/zerolog"
	"github.com/samber/do/v2"

	"github.com/bouroo/goAthena/internal/config"
)

// Register wires the GRF asset server into the DI container if
// assets.enabled is true. When disabled, it is a no-op and the
// gateway composition root will not find an *AssetHandler in the
// container.
func Register(c do.Injector) error {
	cfg, err := do.Invoke[*config.Config](c)
	if err != nil {
		return fmt.Errorf("resolve config: %w", err)
	}
	if !cfg.Assets.Enabled {
		return nil
	}

	logger, err := do.Invoke[*zerolog.Logger](c)
	if err != nil {
		return fmt.Errorf("resolve logger: %w", err)
	}

	set, err := OpenGRFSet(cfg.Assets.GRFDir, cfg.Assets.MaxCacheMB*1024*1024)
	if err != nil {
		return fmt.Errorf("open grf set: %w", err)
	}

	handler := NewAssetHandler(set, *logger)
	do.ProvideValue(c, handler)

	logger.Info().
		Str("grf_dir", cfg.Assets.GRFDir).
		Int64("max_cache_mb", cfg.Assets.MaxCacheMB).
		Msg("asset server registered")

	return nil
}
