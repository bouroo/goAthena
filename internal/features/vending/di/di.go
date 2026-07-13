package di

import (
	"github.com/rs/zerolog"
	"github.com/samber/do/v2"

	economydomain "github.com/bouroo/goAthena/internal/features/economy/domain"
	inventorydomain "github.com/bouroo/goAthena/internal/features/inventory/domain"
	"github.com/bouroo/goAthena/internal/features/vending/domain"
	"github.com/bouroo/goAthena/internal/features/vending/repository"
	"github.com/bouroo/goAthena/internal/features/vending/service"
)

// Register wires the vending feature into the DI container.
func Register(c do.Injector) error {
	logger := do.MustInvoke[*zerolog.Logger](c)

	repo := repository.NewMemoryVendingRepository()
	do.ProvideValue(c, repo)

	locks := repository.NewMemoryLockStore()
	do.ProvideValue(c, locks)

	invRepo, _ := do.Invoke[inventorydomain.InventoryRepository](c)
	zenyRepo, _ := do.Invoke[economydomain.CharacterZenyRepository](c)

	if invRepo == nil {
		logger.Warn().Msg("vending: inventory repo not registered; ownership validation disabled")
	}
	if zenyRepo == nil {
		logger.Warn().Msg("vending: zeny repo not registered; zeny validation disabled")
	}

	vendingSvc := service.NewVendingService(repo, locks, invRepo, zenyRepo, 0)
	do.ProvideValue(c, vendingSvc)

	logger.Info().Msg("vending feature registered")
	return nil
}

// ProvideVendingService resolves the vending service from the DI container.
func ProvideVendingService(c do.Injector) (domain.VendingService, error) {
	svc, err := do.Invoke[domain.VendingService](c)
	if err != nil {
		return nil, err
	}
	return svc, nil
}
