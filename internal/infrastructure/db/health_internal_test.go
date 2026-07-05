//go:build unit

package db

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestGormChecker_Name(t *testing.T) {
	t.Parallel()

	t.Run("nil db returns fallback", func(t *testing.T) {
		t.Parallel()
		c := gormChecker{}
		assert.Equal(t, "database", c.Name())
	})

	t.Run("nil dialector returns fallback", func(t *testing.T) {
		t.Parallel()
		c := gormChecker{db: &gorm.DB{Config: &gorm.Config{}}}
		assert.Equal(t, "database", c.Name())
	})

	t.Run("mysql dialector returns mysql", func(t *testing.T) {
		t.Parallel()
		c := gormChecker{db: &gorm.DB{Config: &gorm.Config{Dialector: mysql.Dialector{}}}}
		assert.Equal(t, "mysql", c.Name())
	})

	t.Run("postgres dialector returns postgres", func(t *testing.T) {
		t.Parallel()
		c := gormChecker{db: &gorm.DB{Config: &gorm.Config{Dialector: postgres.Dialector{}}}}
		assert.Equal(t, "postgres", c.Name())
	})
}

func TestGormChecker_Check_NilDB(t *testing.T) {
	t.Parallel()

	c := gormChecker{}
	err := c.Check(t.Context())

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not initialized")
}
