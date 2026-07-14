// Package di wires the identity feature into the DI container.
//
// Bootstrap order: config + db + valkey + grpc.Server (provided by
// upstream Register calls) → repositories → service → gRPC handler.
// The handler is registered onto the shared *grpc.Server so the identity
// gRPC listener also exposes Authenticate / GetCharacterList. The
// WarehouseLock is re-exposed as a DI value so the zone service can
// acquire per-account locks without depending on this feature's
// internals.
package di

import (
	"fmt"

	"github.com/rs/zerolog"
	"github.com/samber/do/v2"
	valkeygo "github.com/valkey-io/valkey-go"
	"google.golang.org/grpc"
	"gorm.io/gorm"

	identityv1 "github.com/bouroo/goAthena/api/pb/identity/v1"
	"github.com/bouroo/goAthena/internal/config"
	economydi "github.com/bouroo/goAthena/internal/features/economy/di"
	"github.com/bouroo/goAthena/internal/features/identity/domain"
	"github.com/bouroo/goAthena/internal/features/identity/handler"
	"github.com/bouroo/goAthena/internal/features/identity/repository"
	"github.com/bouroo/goAthena/internal/features/identity/service"
	inventorydomain "github.com/bouroo/goAthena/internal/features/inventory/domain"
	inventoryrepo "github.com/bouroo/goAthena/internal/features/inventory/repository"
	"github.com/bouroo/goAthena/pkg/ro/itemdb"
)

// Register wires the identity feature (login, char roster, warehouse
// lock) into the DI container. It depends on *config.Config, *gorm.DB,
// valkeygo.Client, *grpc.Server and *zerolog.Logger being already
// registered. It also resolves the economy ShopService from the
// container, so economydi.Register must run first (the identity
// composition root in internal/app/identity enforces this order).
func Register(c do.Injector) error {
	cfg := do.MustInvoke[*config.Config](c)
	db := do.MustInvoke[*gorm.DB](c)
	vk := do.MustInvoke[valkeygo.Client](c)
	grpcServer := do.MustInvoke[*grpc.Server](c)
	logger := do.MustInvoke[*zerolog.Logger](c)

	accountRepo := repository.NewAccountRepository(db)
	charRepo := repository.NewCharacterRepository(db)
	sessionRepo := repository.NewSessionRepository(vk)
	warehouseLock := repository.NewWarehouseLock(vk)
	inventoryRepo := inventoryrepo.NewInventoryRepository(db)
	itemWeight := resolveItemWeight(cfg.Identity.ItemDBPath, logger)

	identitySvc := service.NewIdentityService(
		accountRepo,
		charRepo,
		sessionRepo,
		logger,
		cfg.Identity.UseMD5Passwords,
		cfg.Identity.MaxChars,
		inventoryRepo,
		itemWeight,
	)

	shopSvc, err := economydi.ProvideShopService(c)
	if err != nil {
		return fmt.Errorf("resolve economy shop service: %w", err)
	}

	identityv1.RegisterIdentityServiceServer(grpcServer, handler.NewGRPCHandler(identitySvc, shopSvc))

	do.ProvideValue(c, warehouseLock)
	do.ProvideValue(c, identitySvc)

	logger.Info().
		Bool("use_md5_passwords", cfg.Identity.UseMD5Passwords).
		Int("max_chars", cfg.Identity.MaxChars).
		Msg("identity feature registered")

	return nil
}

func resolveItemWeight(path string, logger *zerolog.Logger) inventorydomain.ItemWeightLookup {
	if path == "" {
		logger.Warn().Msg("identity: item_db_path unset; item weights disabled")
		return inventorydomain.ZeroItemWeight{}
	}

	registry, err := itemdb.LoadFile(path)
	if err != nil {
		logger.Warn().Err(err).
			Str("path", path).
			Msg("identity: item_db load failed; item weights disabled")
		return inventorydomain.ZeroItemWeight{}
	}
	return registry
}

// ProvideIdentityService resolves the wired IdentityService use case.
// Other features (notably zone) call this to invoke login / character
// flows without depending on the handler or transport layer.
func ProvideIdentityService(c do.Injector) (domain.IdentityService, error) {
	svc, err := do.Invoke[domain.IdentityService](c)
	if err != nil {
		return nil, fmt.Errorf("resolve identity service: %w", err)
	}
	return svc, nil
}

// ProvideWarehouseLock resolves the shared warehouse lock used by both
// identity and zone features.
func ProvideWarehouseLock(c do.Injector) (domain.WarehouseLock, error) {
	lock, err := do.Invoke[domain.WarehouseLock](c)
	if err != nil {
		return nil, fmt.Errorf("resolve warehouse lock: %w", err)
	}
	return lock, nil
}
