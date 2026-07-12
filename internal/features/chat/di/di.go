package di

import (
	"context"
	"time"

	"github.com/rs/zerolog"
	"github.com/samber/do/v2"

	"github.com/bouroo/goAthena/internal/features/chat/domain"
	chatrepository "github.com/bouroo/goAthena/internal/features/chat/repository"
	"github.com/bouroo/goAthena/internal/features/chat/service"
	storagedomain "github.com/bouroo/goAthena/internal/features/storage/domain"
)

// Register wires the chat feature into the DI container.
func Register(c do.Injector) error {
	logger := do.MustInvoke[*zerolog.Logger](c)

	chatRepo := chatrepository.NewMemoryChatRepository()
	do.ProvideValue(c, chatRepo)

	friendRepo := chatrepository.NewMemoryFriendRepository()
	do.ProvideValue(c, friendRepo)

	partyRepo := chatrepository.NewMemoryPartyRepository()
	do.ProvideValue(c, partyRepo)

	// Use the storage feature's LockStore implementation for friend/party locks.
	// If storage is not registered, fall back to a no-op implementation so the
	// chat feature can work independently in tests or standalone mode.
	var locks domain.LockStore
	if storageLocks, err := do.Invoke[storagedomain.LockStore](c); err == nil {
		locks = lockStoreAdapter{inner: storageLocks}
	} else {
		logger.Warn().Msg("chat: storage lock store not available, using no-op fallback")
		locks = noopLockStore{}
	}

	chatSvc := service.NewChatService(chatRepo, friendRepo, partyRepo, locks, 5*time.Second)
	do.ProvideValue(c, chatSvc)

	logger.Info().Msg("chat feature registered")
	return nil
}

// lockStoreAdapter wraps a storage.domain.LockStore as a chat.domain.LockStore.
// Both interfaces are structurally identical; the adapter exists because Go's
// nominal type system treats them as distinct types.
type lockStoreAdapter struct {
	inner storagedomain.LockStore
}

func (a lockStoreAdapter) Acquire(ctx context.Context, key string, ttl time.Duration) (string, error) {
	return a.inner.Acquire(ctx, key, ttl)
}

func (a lockStoreAdapter) Release(ctx context.Context, key, token string) error {
	return a.inner.Release(ctx, key, token)
}

// noopLockStore is a no-op implementation of LockStore that always succeeds.
// It's used as a fallback when the storage feature is not available.
type noopLockStore struct{}

func (noopLockStore) Acquire(_ context.Context, _ string, _ time.Duration) (string, error) {
	return "noop-token", nil
}

func (noopLockStore) Release(_ context.Context, _, _ string) error {
	return nil
}

	chatSvc := service.NewChatService(chatRepo, friendRepo, partyRepo, locks, 5*time.Second)
	do.ProvideValue(c, chatSvc)

	logger.Info().Msg("chat feature registered")
	return nil
}

// noopLockStore is a no-op implementation of LockStore that always succeeds.
// It's used as a fallback when the storage feature is not available.
type noopLockStore struct{}

func (noopLockStore) Acquire(_ context.Context, _ string, _ time.Duration) (string, error) {
	return "noop-token", nil
}

func (noopLockStore) Release(_ context.Context, _, _ string) error {
	return nil
}

// ProvideChatService resolves the chat service from the DI container.
func ProvideChatService(c do.Injector) (domain.ChatService, error) {
	svc, err := do.Invoke[domain.ChatService](c)
	if err != nil {
		return nil, err
	}
	return svc, nil
}
