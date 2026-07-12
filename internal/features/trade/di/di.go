package di

import (
	"github.com/rs/zerolog"
	"github.com/samber/do/v2"

	economydomain "github.com/bouroo/goAthena/internal/features/economy/domain"
	inventorydomain "github.com/bouroo/goAthena/internal/features/inventory/domain"
	"github.com/bouroo/goAthena/internal/features/trade/domain"
	"github.com/bouroo/goAthena/internal/features/trade/repository"
	"github.com/bouroo/goAthena/internal/features/trade/service"
)

// Register wires the trade feature into the DI container.
func Register(c do.Injector) error {
	logger := do.MustInvoke[*zerolog.Logger](c)

	repo := repository.NewMemoryTradeRepository()
	do.ProvideValue(c, repo)

	locks := repository.NewMemoryLockStore()
	do.ProvideValue(c, locks)

	invRepo, _ := do.Invoke[inventorydomain.InventoryRepository](c)
	zenyRepo, _ := do.Invoke[economydomain.CharacterZenyRepository](c)

	if invRepo == nil {
		logger.Warn().Msg("trade: inventory repo not registered; ownership validation disabled")
	}
	if zenyRepo == nil {
		logger.Warn().Msg("trade: zeny repo not registered; zeny validation disabled")
	}

	tradeSvc := service.NewTradeService(repo, locks, invRepo, zenyRepo, 0)
	do.ProvideValue(c, tradeSvc)

	logger.Info().Msg("trade feature registered")
	return nil
}

// ProvideTradeService resolves the trade service from the DI container.
func ProvideTradeService(c do.Injector) (domain.TradeService, error) {
	svc, err := do.Invoke[domain.TradeService](c)
	if err != nil {
		return nil, err
	}
	return svc, nil
}
