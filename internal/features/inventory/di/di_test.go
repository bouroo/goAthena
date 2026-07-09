//go:build unit

package di_test

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/samber/do/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/bouroo/goAthena/internal/features/inventory/di"
	"github.com/bouroo/goAthena/internal/features/inventory/domain"
)

// newStubGormDB wires a *gorm.DB backed by sqlmock. The DB is not
// exercised by the DI tests — Register only stores the pointer — but
// a real *gorm.DB is needed to satisfy the constructor.
func newStubGormDB(t *testing.T) *gorm.DB {
	t.Helper()
	sqlDB, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	gormDB, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{})
	require.NoError(t, err)
	return gormDB
}

func TestRegister_ProvidesInventoryRepository(t *testing.T) {
	t.Parallel()

	c := do.New()
	do.ProvideValue(c, newStubGormDB(t))

	require.NoError(t, di.Register(c))

	repo, err := di.ProvideInventoryRepository(c)
	require.NoError(t, err)
	require.NotNil(t, repo)
	assert.Implements(t, (*domain.InventoryRepository)(nil), repo)
}

func TestProvideInventoryRepository_NotRegistered(t *testing.T) {
	t.Parallel()

	c := do.New()
	repo, err := di.ProvideInventoryRepository(c)
	require.Error(t, err)
	assert.Nil(t, repo)
}
