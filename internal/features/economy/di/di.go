package di

import (
	"fmt"

	"github.com/samber/do/v2"
	"gorm.io/gorm"

	"github.com/bouroo/goAthena/internal/features/economy/domain"
	"github.com/bouroo/goAthena/internal/features/economy/repository"
)

// Register wires the economy feature's persistence layer into the
// DI container.
func Register(c do.Injector) error {
	db := do.MustInvoke[*gorm.DB](c)

	repo := repository.NewCharacterZenyRepository(db)

	do.ProvideValue(c, repo)

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
