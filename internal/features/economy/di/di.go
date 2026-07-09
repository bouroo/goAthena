package di

import (
	"fmt"

	"github.com/samber/do/v2"
	valkeygo "github.com/valkey-io/valkey-go"
	"gorm.io/gorm"

	"github.com/bouroo/goAthena/internal/features/economy/domain"
	"github.com/bouroo/goAthena/internal/features/economy/repository"
	"github.com/bouroo/goAthena/internal/features/economy/service"
)

// Register wires the economy feature into the DI container. It resolves
// *gorm.DB (for CharacterZenyRepository) and valkeygo.Client (for the
// distributed economy lock, D-203) from upstream registrations, so it must
// be called after db.Register and valkey.Register.
func Register(c do.Injector) error {
	db := do.MustInvoke[*gorm.DB](c)

	zenyRepo := repository.NewCharacterZenyRepository(db)
	do.ProvideValue(c, zenyRepo)

	client := do.MustInvoke[valkeygo.Client](c)
	lockStore := repository.NewValkeyLockStore(client)
	do.ProvideValue(c, lockStore)

	// ShopService composes the lock + the atomic zeny/inventory
	// transaction. lockTTL of 0 lets NewShopService apply DefaultLockTTL.
	do.ProvideValue(c, service.NewShopService(zenyRepo, lockStore, 0))

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

// ProvideShopService resolves the economy ShopService (BuyFromShop /
// SellToShop use-cases). Identity handlers call this to back the gRPC
// economy RPCs without depending on the service package.
func ProvideShopService(c do.Injector) (domain.ShopService, error) {
	svc, err := do.Invoke[domain.ShopService](c)
	if err != nil {
		return nil, fmt.Errorf("resolve economy shop service: %w", err)
	}
	return svc, nil
}
