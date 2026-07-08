//go:build unit

package repository_test

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/bouroo/goAthena/internal/features/identity/domain"
	"github.com/bouroo/goAthena/internal/features/identity/repository"
)

// charColumns mirrors the SELECT clause in character.go so AddRow(...)
// arguments map 1:1 to the CharModel fields that GORM scans into.
var charColumns = []string{
	"char_id", "account_id", "char_num", "name", "class",
	"base_level", "job_level", "base_exp", "job_exp", "zeny",
	"max_hp", "hp", "max_sp", "sp", "hair", "hair_color",
	"clothes_color", "weapon", "shield", "head_top", "head_mid",
	"head_bottom", "robe", "last_map", "delete_date", "unban_time", "sex",
}

func sampleCharRows() *sqlmock.Rows {
	return sqlmock.NewRows(charColumns).AddRow(
		uint32(150001), uint32(2000000), int8(0), "alpha", uint16(0),
		uint32(99), uint32(50), uint64(123456), uint64(7890), uint32(1000),
		uint32(4000), uint32(3500), uint32(200), uint32(150), uint8(1), uint16(2),
		uint16(0), uint16(1), uint16(0), uint16(0), uint16(0),
		uint16(0), uint16(0), "prontera", int64(0), int64(0), "M",
	).AddRow(
		uint32(150002), uint32(2000000), int8(1), "beta", uint16(4001),
		uint32(70), uint32(45), uint64(500000), uint64(90000), uint32(500),
		uint32(3000), uint32(2500), uint32(150), uint32(100), uint8(5), uint16(3),
		uint16(1), uint16(2), uint16(1), uint16(0), uint16(0),
		uint16(0), uint16(0), "geffen", int64(1700000000), int64(0), "F",
	)
}

func TestCharacterRepository_ListByAccount(t *testing.T) {
	t.Parallel()

	t.Run("happy path returns mapped summaries ordered by slot", func(t *testing.T) {
		t.Parallel()
		gormDB, mock := newMockGormDB(t)
		repo := repository.NewCharacterRepository(gormDB)

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT char_id, account_id, char_num, name, class, base_level, job_level, base_exp, job_exp, zeny, max_hp, hp, max_sp, sp, hair, hair_color, clothes_color, weapon, shield, head_top, head_mid, head_bottom, robe, last_map, delete_date, unban_time, sex, str, agi, vit, int, dex, luk, status_point, skill_point FROM "char" WHERE account_id = $1 AND char_num < $2 ORDER BY char_num ASC`,
		)).
			WithArgs(uint32(2000000), 9).
			WillReturnRows(sampleCharRows())

		got, err := repo.ListByAccount(context.Background(), 2000000, 9)
		require.NoError(t, err)
		require.Len(t, got, 2)

		assert.Equal(t, uint32(150001), got[0].CharID)
		assert.Equal(t, uint8(0), got[0].Slot)
		assert.Equal(t, "alpha", got[0].Name)
		assert.Equal(t, uint16(0), got[0].Class)
		assert.Equal(t, uint32(99), got[0].BaseLevel)
		assert.Equal(t, uint32(4000), got[0].MaxHP)
		assert.Equal(t, "prontera", got[0].LastMap)
		assert.Equal(t, domain.SexMale, got[0].Sex)
		assert.True(t, got[0].DeleteDate.IsZero(), "delete_date=0 must map to zero time")

		assert.Equal(t, "beta", got[1].Name)
		assert.Equal(t, uint8(1), got[1].Slot)
		assert.Equal(t, int64(1700000000), got[1].DeleteDate.Unix(), "non-zero delete_date decodes to unix seconds")
		assert.Equal(t, domain.SexFemale, got[1].Sex)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("empty result returns empty slice with no error", func(t *testing.T) {
		t.Parallel()
		gormDB, mock := newMockGormDB(t)
		repo := repository.NewCharacterRepository(gormDB)

		mock.ExpectQuery(`SELECT .* FROM "char" WHERE account_id = \$1 AND char_num < \$2`).
			WithArgs(uint32(2000000), 9).
			WillReturnRows(sqlmock.NewRows(charColumns))

		got, err := repo.ListByAccount(context.Background(), 2000000, 9)
		require.NoError(t, err)
		assert.NotNil(t, got, "empty slice must be non-nil to keep callers from nil-checking")
		assert.Empty(t, got)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("DB error is wrapped with the account id", func(t *testing.T) {
		t.Parallel()
		gormDB, mock := newMockGormDB(t)
		repo := repository.NewCharacterRepository(gormDB)

		boom := assert.AnError
		mock.ExpectQuery(`SELECT .* FROM "char" WHERE account_id = \$1 AND char_num < \$2`).
			WithArgs(uint32(2000000), 9).
			WillReturnError(boom)

		got, err := repo.ListByAccount(context.Background(), 2000000, 9)
		require.Error(t, err)
		assert.Nil(t, got)
		assert.ErrorIs(t, err, boom)
		assert.Contains(t, err.Error(), "2000000")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("non-positive maxSlots is rejected before issuing a query", func(t *testing.T) {
		t.Parallel()
		gormDB, mock := newMockGormDB(t)
		repo := repository.NewCharacterRepository(gormDB)
		// mock has no expectations — any query would fail this test.

		got, err := repo.ListByAccount(context.Background(), 2000000, 0)
		require.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "maxSlots")
		assert.NoError(t, mock.ExpectationsWereMet(), "no SQL should have been issued")
	})
}

func TestCharacterRepository_GetByID(t *testing.T) {
	t.Parallel()

	t.Run("happy path returns mapped summary", func(t *testing.T) {
		t.Parallel()
		gormDB, mock := newMockGormDB(t)
		repo := repository.NewCharacterRepository(gormDB)

		rows := sqlmock.NewRows(charColumns).AddRow(
			uint32(150001), uint32(2000000), int8(0), "alpha", uint16(7),
			uint32(99), uint32(50), uint64(123456), uint64(7890), uint32(1000),
			uint32(4000), uint32(3500), uint32(200), uint32(150), uint8(5), uint16(2),
			uint16(0), uint16(1101), uint16(0), uint16(0), uint16(0),
			uint16(0), uint16(0), "prontera", int64(0), int64(0), "M",
		)
		mock.ExpectQuery(`SELECT .* FROM "char" WHERE account_id = \$1 AND char_id = \$2`).
			WithArgs(uint32(2000000), uint32(150001), 1).
			WillReturnRows(rows)

		got, err := repo.GetByID(context.Background(), 2000000, 150001)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, uint32(150001), got.CharID)
		assert.Equal(t, uint32(2000000), got.AccountID)
		assert.Equal(t, "alpha", got.Name)
		assert.Equal(t, uint16(7), got.Class)
		assert.Equal(t, uint32(4000), got.MaxHP)
		assert.Equal(t, uint32(3500), got.HP)
		assert.Equal(t, uint16(1101), got.Weapon)
		assert.Equal(t, domain.SexMale, got.Sex)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found returns ErrCharacterNotFound without issuing extra queries", func(t *testing.T) {
		t.Parallel()
		gormDB, mock := newMockGormDB(t)
		repo := repository.NewCharacterRepository(gormDB)

		mock.ExpectQuery(`SELECT .* FROM "char" WHERE account_id = \$1 AND char_id = \$2`).
			WithArgs(uint32(2000000), uint32(150001), 1).
			WillReturnError(gorm.ErrRecordNotFound)

		got, err := repo.GetByID(context.Background(), 2000000, 150001)
		require.Error(t, err)
		assert.Nil(t, got)
		assert.ErrorIs(t, err, domain.ErrCharacterNotFound)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("other DB error is wrapped", func(t *testing.T) {
		t.Parallel()
		gormDB, mock := newMockGormDB(t)
		repo := repository.NewCharacterRepository(gormDB)

		boom := assert.AnError
		mock.ExpectQuery(`SELECT .* FROM "char" WHERE account_id = \$1 AND char_id = \$2`).
			WithArgs(uint32(2000000), uint32(150001), 1).
			WillReturnError(boom)

		got, err := repo.GetByID(context.Background(), 2000000, 150001)
		require.Error(t, err)
		assert.Nil(t, got)
		assert.ErrorIs(t, err, boom)
		assert.NotErrorIs(t, err, domain.ErrCharacterNotFound, "boom should not alias ErrCharacterNotFound")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("zero keys are rejected before any query", func(t *testing.T) {
		t.Parallel()
		gormDB, _ := newMockGormDB(t)
		repo := repository.NewCharacterRepository(gormDB)

		// No expectations: any SQL fired here would fail the test.
		got, err := repo.GetByID(context.Background(), 0, 150001)
		require.Error(t, err)
		assert.Nil(t, got)
		assert.ErrorIs(t, err, domain.ErrCharacterNotFound)

		got, err = repo.GetByID(context.Background(), 2000000, 0)
		require.Error(t, err)
		assert.Nil(t, got)
		assert.ErrorIs(t, err, domain.ErrCharacterNotFound)
	})
}

func TestCharacterRepository_CharModelToDomain(t *testing.T) {
	t.Parallel()

	t.Run("nil model returns zero value", func(t *testing.T) {
		t.Parallel()
		got := repository.CharModelToDomainForTest(nil)
		assert.Equal(t, domain.CharacterSummary{}, got)
	})

	t.Run("slot field is widened from int8 to uint8 without sign change", func(t *testing.T) {
		t.Parallel()
		got := repository.CharModelToDomainForTest(&repository.CharModel{
			CharID:    150001,
			AccountID: 2000000,
			CharNum:   5,
			Name:      "gamma",
		})
		assert.Equal(t, uint8(5), got.Slot)
		assert.Equal(t, "gamma", got.Name)
	})

	t.Run("zero-valued unix timestamps stay zero", func(t *testing.T) {
		t.Parallel()
		got := repository.CharModelToDomainForTest(&repository.CharModel{
			DeleteDate: 0,
			UnbanTime:  0,
		})
		assert.True(t, got.DeleteDate.IsZero())
		assert.True(t, got.UnbanTime.IsZero())
	})
}
