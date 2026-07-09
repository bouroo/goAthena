//go:build unit

package repository

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"github.com/bouroo/goAthena/internal/features/economy/domain"
	inventorydomain "github.com/bouroo/goAthena/internal/features/inventory/domain"
)

func setupTestDB(t *testing.T) (*gorm.DB, sqlmock.Sqlmock) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)

	gdb, err := gorm.Open(mysql.New(mysql.Config{Conn: db, SkipInitializeWithVersion: true}), &gorm.Config{})
	require.NoError(t, err)

	return gdb, mock
}

func TestGetZeny(t *testing.T) {
	db, mock := setupTestDB(t)
	repo := NewCharacterZenyRepository(db)

	t.Run("success", func(t *testing.T) {
		mock.ExpectQuery("SELECT \\* FROM `char` WHERE char_id = \\? ORDER BY `char`.`char_id` LIMIT \\?").
			WithArgs(1, 1).
			WillReturnRows(sqlmock.NewRows([]string{"char_id", "zeny"}).AddRow(1, 100))

		zeny, err := repo.GetZeny(context.Background(), 1)
		assert.NoError(t, err)
		assert.Equal(t, uint32(100), zeny)
	})

	t.Run("not found", func(t *testing.T) {
		mock.ExpectQuery("SELECT \\* FROM `char` WHERE char_id = \\? ORDER BY `char`.`char_id` LIMIT \\?").
			WithArgs(2, 1).
			WillReturnError(gorm.ErrRecordNotFound)

		_, err := repo.GetZeny(context.Background(), 2)
		assert.ErrorIs(t, err, domain.ErrCharNotFound)
	})

	t.Run("invalid char id", func(t *testing.T) {
		_, err := repo.GetZeny(context.Background(), 0)
		assert.ErrorIs(t, err, domain.ErrCharNotFound)
	})

	t.Run("db error", func(t *testing.T) {
		mock.ExpectQuery("SELECT \\* FROM `char` WHERE char_id = \\? ORDER BY `char`.`char_id` LIMIT \\?").
			WithArgs(3, 1).
			WillReturnError(assert.AnError)

		_, err := repo.GetZeny(context.Background(), 3)
		assert.Error(t, err)
		assert.NotErrorIs(t, err, domain.ErrCharNotFound)
	})
}

func TestExecuteBuyTx(t *testing.T) {
	db, mock := setupTestDB(t)
	repo := NewCharacterZenyRepository(db)

	t.Run("success", func(t *testing.T) {
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT \\* FROM `char` WHERE char_id = \\? ORDER BY `char`.`char_id` LIMIT \\? FOR UPDATE").
			WithArgs(1, 1).
			WillReturnRows(sqlmock.NewRows([]string{"char_id", "zeny"}).AddRow(1, 1000))
		mock.ExpectExec("UPDATE `char` SET `zeny`=\\? WHERE `char_id` = \\?").
			WithArgs(500, 1).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec("INSERT INTO `inventory`").
			WillReturnResult(sqlmock.NewResult(10, 1))
		mock.ExpectCommit()

		newZeny, err := repo.ExecuteBuyTx(context.Background(), 1, 500, []domain.AcquiredItem{{ItemID: 501, Amount: 1}})
		assert.NoError(t, err)
		assert.Equal(t, uint32(500), newZeny)
	})

	t.Run("insufficient zeny", func(t *testing.T) {
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT \\* FROM `char` WHERE char_id = \\? ORDER BY `char`.`char_id` LIMIT \\? FOR UPDATE").
			WithArgs(1, 1).
			WillReturnRows(sqlmock.NewRows([]string{"char_id", "zeny"}).AddRow(1, 100))
		mock.ExpectRollback()

		_, err := repo.ExecuteBuyTx(context.Background(), 1, 500, nil)
		assert.ErrorIs(t, err, domain.ErrInsufficientZeny)
	})

	t.Run("invalid char id", func(t *testing.T) {
		_, err := repo.ExecuteBuyTx(context.Background(), 0, 500, nil)
		assert.ErrorIs(t, err, domain.ErrCharNotFound)
	})

	t.Run("char lock db error", func(t *testing.T) {
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT \\* FROM `char` WHERE char_id = \\? ORDER BY `char`.`char_id` LIMIT \\? FOR UPDATE").
			WithArgs(1, 1).
			WillReturnError(assert.AnError)
		mock.ExpectRollback()

		_, err := repo.ExecuteBuyTx(context.Background(), 1, 500, nil)
		assert.Error(t, err)
	})

	t.Run("create inv error", func(t *testing.T) {
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT \\* FROM `char` WHERE char_id = \\? ORDER BY `char`.`char_id` LIMIT \\? FOR UPDATE").
			WithArgs(1, 1).
			WillReturnRows(sqlmock.NewRows([]string{"char_id", "zeny"}).AddRow(1, 1000))
		mock.ExpectExec("UPDATE `char` SET `zeny`=\\? WHERE `char_id` = \\?").
			WithArgs(500, 1).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec("INSERT INTO `inventory`").
			WillReturnError(assert.AnError)
		mock.ExpectRollback()

		_, err := repo.ExecuteBuyTx(context.Background(), 1, 500, []domain.AcquiredItem{{ItemID: 501, Amount: 1}})
		assert.Error(t, err)
	})
}

func TestExecuteSellTx(t *testing.T) {
	db, mock := setupTestDB(t)
	repo := NewCharacterZenyRepository(db)

	t.Run("success decrement", func(t *testing.T) {
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT \\* FROM `char` WHERE char_id = \\? ORDER BY `char`.`char_id` LIMIT \\? FOR UPDATE").
			WithArgs(1, 1).
			WillReturnRows(sqlmock.NewRows([]string{"char_id", "zeny"}).AddRow(1, 100))
		mock.ExpectExec("UPDATE `char` SET `zeny`=\\? WHERE `char_id` = \\?").
			WithArgs(600, 1).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectQuery("SELECT \\* FROM `inventory` WHERE id = \\? ORDER BY `inventory`.`id` LIMIT \\? FOR UPDATE").
			WithArgs(99, 1).
			WillReturnRows(sqlmock.NewRows([]string{"id", "amount"}).AddRow(99, 10))
		mock.ExpectExec("UPDATE `inventory` SET `amount`=\\? WHERE `id` = \\?").
			WithArgs(5, 99).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()

		newZeny, err := repo.ExecuteSellTx(context.Background(), 1, 500, []domain.SellLine{{InvID: 99, Amount: 5}})
		assert.NoError(t, err)
		assert.Equal(t, uint32(600), newZeny)
	})

	t.Run("success delete", func(t *testing.T) {
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT \\* FROM `char` WHERE char_id = \\? ORDER BY `char`.`char_id` LIMIT \\? FOR UPDATE").
			WithArgs(1, 1).
			WillReturnRows(sqlmock.NewRows([]string{"char_id", "zeny"}).AddRow(1, 100))
		mock.ExpectExec("UPDATE `char` SET `zeny`=\\? WHERE `char_id` = \\?").
			WithArgs(600, 1).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectQuery("SELECT \\* FROM `inventory` WHERE id = \\? ORDER BY `inventory`.`id` LIMIT \\? FOR UPDATE").
			WithArgs(99, 1).
			WillReturnRows(sqlmock.NewRows([]string{"id", "amount"}).AddRow(99, 5))
		mock.ExpectExec("DELETE FROM `inventory` WHERE `inventory`.`id` = \\?").
			WithArgs(99).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()

		newZeny, err := repo.ExecuteSellTx(context.Background(), 1, 500, []domain.SellLine{{InvID: 99, Amount: 5}})
		assert.NoError(t, err)
		assert.Equal(t, uint32(600), newZeny)
	})

	t.Run("overflow", func(t *testing.T) {
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT \\* FROM `char` WHERE char_id = \\? ORDER BY `char`.`char_id` LIMIT \\? FOR UPDATE").
			WithArgs(1, 1).
			WillReturnRows(sqlmock.NewRows([]string{"char_id", "zeny"}).AddRow(1, 1999999999))
		mock.ExpectRollback()

		_, err := repo.ExecuteSellTx(context.Background(), 1, 2, nil)
		assert.ErrorIs(t, err, domain.ErrZenyOverflow)
	})

	t.Run("invalid char id", func(t *testing.T) {
		_, err := repo.ExecuteSellTx(context.Background(), 0, 500, nil)
		assert.ErrorIs(t, err, domain.ErrCharNotFound)
	})

	t.Run("char lock db error", func(t *testing.T) {
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT \\* FROM `char` WHERE char_id = \\? ORDER BY `char`.`char_id` LIMIT \\? FOR UPDATE").
			WithArgs(1, 1).
			WillReturnError(assert.AnError)
		mock.ExpectRollback()

		_, err := repo.ExecuteSellTx(context.Background(), 1, 500, nil)
		assert.Error(t, err)
	})

	t.Run("inv not found", func(t *testing.T) {
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT \\* FROM `char` WHERE char_id = \\? ORDER BY `char`.`char_id` LIMIT \\? FOR UPDATE").
			WithArgs(1, 1).
			WillReturnRows(sqlmock.NewRows([]string{"char_id", "zeny"}).AddRow(1, 100))
		mock.ExpectExec("UPDATE `char` SET `zeny`=\\? WHERE `char_id` = \\?").
			WithArgs(600, 1).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectQuery("SELECT \\* FROM `inventory` WHERE id = \\? ORDER BY `inventory`.`id` LIMIT \\? FOR UPDATE").
			WithArgs(99, 1).
			WillReturnError(gorm.ErrRecordNotFound)
		mock.ExpectRollback()

		_, err := repo.ExecuteSellTx(context.Background(), 1, 500, []domain.SellLine{{InvID: 99, Amount: 5}})
		assert.ErrorIs(t, err, inventorydomain.ErrItemNotFound)
	})
}
