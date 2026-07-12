package di

import (
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"github.com/samber/do/v2"

	"github.com/bouroo/goAthena/internal/features/chat/domain"
	chatrepository "github.com/bouroo/goAthena/internal/features/chat/repository"
	"github.com/bouroo/goAthena/internal/features/chat/service"
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

	locks, err := do.Invoke[domain.LockStore](c)
	if err != nil {
		return fmt.Errorf("invoke lock store: %w", err)
	}

	chatSvc := service.NewChatService(chatRepo, friendRepo, partyRepo, locks, 5*time.Second)
	do.ProvideValue(c, chatSvc)

	logger.Info().Msg("chat feature registered")
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
