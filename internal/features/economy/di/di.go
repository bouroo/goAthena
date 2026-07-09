package di

import (
	"fmt"

	"github.com/samber/do/v2"
	valkeygo "github.com/valkey-io/valkey-go"
	"gorm.io/gorm"

	"github.com/bouroo/goAthena/internal/features/economy/domain"
	"github.com/bouroo/goAthena/internal/features/economy/repository"
)

// Register wires the economy feature's persistence layer into the
// DI container. It resolves *gorm.DB (for CharacterZenyRepository) and
// valkeygo.Client (for the distributed economy lock, D-203) from upstream
// registrations, so it must be called after db.Register and valkey.Register.
func Register(c do.Injector) error {
	db := do.MustInvoke[*gorm.DB](c)

	zenyRepo := repository.NewCharacterZenyRepository(db)
	do.ProvideValue(c, zenyRepo)

	client := do.MustInvoke[valkeygo.Client](c)
	do.ProvideValue(c, repository.NewValkeyLockStore(client))

	return nil
}

// ProvideCharacterZenyRepository resolves the CharacterZenyRepository.
func ProvideCharacterZenyRepository(c do.Injector) (domain.CharacterZenyRepository, error) {
	repo, err := do.Invoke[domain.CharacterZenyRepository](c)
	if err != nil {
		return nil, fmt.Errorf("resolve character zeny repository: %w", err)
	}
	return repo, nil
}

// ProvideLockStore resolves the wired distributed LockStore.
func ProvideLockStore(c do.Injector) (domain.LockStore, error) {
	store, err := do.Invoke[domain.LockStore](c)
	if err != nil {
		return nil, fmt.Errorf("resolve economy lock store: %w", err)
	}
	return store, nil
}
