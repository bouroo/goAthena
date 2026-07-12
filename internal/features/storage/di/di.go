package di

import (
	"github.com/rs/zerolog"
	"github.com/samber/do/v2"

	inventorydomain "github.com/bouroo/goAthena/internal/features/inventory/domain"
	"github.com/bouroo/goAthena/internal/features/storage/domain"
	"github.com/bouroo/goAthena/internal/features/storage/repository"
	"github.com/bouroo/goAthena/internal/features/storage/service"
)

// Register wires the storage feature into the DI container.
func Register(c do.Injector) error {
	logger := do.MustInvoke[*zerolog.Logger](c)

	repo := repository.NewMemoryStorageRepository()
	do.ProvideValue(c, repo)

	locks := repository.NewMemoryLockStore()
	do.ProvideValue(c, locks)

	invRepo, _ := do.Invoke[inventorydomain.InventoryRepository](c)

	var storageInvRepo domain.InventoryRepository
	if invRepo != nil {
		storageInvRepo = newInventoryRepositoryAdapter(invRepo)
	} else {
		logger.Warn().Msg("storage: inventory repo not registered; deposit/withdraw validation disabled")
	}

	storageSvc := service.NewStorageService(repo, locks, storageInvRepo, 0)
	do.ProvideValue(c, storageSvc)

	logger.Info().Msg("storage feature registered")
	return nil
}

// ProvideStorageService resolves the storage service from the DI container.
func ProvideStorageService(c do.Injector) (domain.StorageService, error) {
	svc, err := do.Invoke[domain.StorageService](c)
	if err != nil {
		return nil, err
	}
	return svc, nil
}
