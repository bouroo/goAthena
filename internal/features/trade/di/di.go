package di

import (
	"github.com/rs/zerolog"
	"github.com/samber/do/v2"

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

	tradeSvc := service.NewTradeService(repo, locks, nil, nil, 0)
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
