//go:build unit

package repository_test

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/bouroo/goAthena/internal/features/inventory/domain"
	"github.com/bouroo/goAthena/internal/features/inventory/repository"
)

// newInventoryMockGormDB wires a *gorm.DB onto a sqlmock-backed *sql.DB
// so the repository's queries can be exercised deterministically
// without a live database. The postgres dialector is used because
// sqlmock's placeholder/quoting semantics are stable across drivers;
// the repository code is dialect-agnostic for SELECTs/UPDATEs that
// don't touch driver-specific syntax.
func newInventoryMockGormDB(t *testing.T) (*gorm.DB, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err, "create sqlmock")
	t.Cleanup(func() { _ = sqlDB.Close() })

	gormDB, err := gorm.Open(
		postgres.New(postgres.Config{Conn: sqlDB}),
		&gorm.Config{SkipDefaultTransaction: true},
	)
	require.NoError(t, err, "open gorm against sqlmock")
	return gormDB, mock
}

// inventoryColumns mirrors the SELECT clause in inventory.go so
// AddRow(...) arguments map 1:1 to the InventoryModel fields that
// GORM scans into. Order MUST match inventorySelectColumns in
// repository/inventory.go.
var inventoryColumns = []string{
	"id", "char_id", "nameid", "amount", "equip", "identify",
	"refine", "attribute",
	"card0", "card1", "card2", "card3",
	"option_id0", "option_val0", "option_parm0",
	"option_id1", "option_val1", "option_parm1",
	"option_id2", "option_val2", "option_parm2",
	"option_id3", "option_val3", "option_parm3",
	"option_id4", "option_val4", "option_parm4",
	"expire_time", "favorite", "bound", "unique_id", "equip_switch",
	"enchantgrade",
}

// sampleInventoryRow builds a single deterministic inventory row for
// sqlmock. It exercises every column type (signed smallint, unsigned
// tinyint, uint32, uint64) at least once so the row→struct scan path
// is exercised end-to-end.
func sampleInventoryRow() *sqlmock.Rows {
	return sqlmock.NewRows(inventoryColumns).AddRow(
		uint32(42), uint32(150001), uint32(501), uint32(7), uint32(0), int16(1),
		uint8(5), uint8(0),
		uint32(4001), uint32(0), uint32(0), uint32(0),
		int16(1), int16(2), int8(0),
		int16(0), int16(0), int8(0),
		int16(0), int16(0), int8(0),
		int16(0), int16(0), int8(0),
		int16(0), int16(0), int8(0),
		uint32(0), uint8(1), uint8(0), uint64(1234567890123), uint32(0),
		uint8(3),
	)
}

func TestInventoryRepository_ListByChar(t *testing.T) {
	t.Parallel()

	t.Run("happy path returns mapped items ordered by id", func(t *testing.T) {
		t.Parallel()
		gormDB, mock := newInventoryMockGormDB(t)
		repo := repository.NewInventoryRepository(gormDB)

		rows := sqlmock.NewRows(inventoryColumns).
			AddRow(
				uint32(1), uint32(150001), uint32(501), uint32(7), uint32(0), int16(1),
				uint8(0), uint8(0),
				uint32(0), uint32(0), uint32(0), uint32(0),
				int16(0), int16(0), int8(0),
				int16(0), int16(0), int8(0),
				int16(0), int16(0), int8(0),
				int16(0), int16(0), int8(0),
				int16(0), int16(0), int8(0),
				uint32(0), uint8(0), uint8(0), uint64(0), uint32(0),
				uint8(0),
			).
			AddRow(
				uint32(2), uint32(150001), uint32(502), uint32(1), uint32(1), int16(1),
				uint8(7), uint8(0),
				uint32(4001), uint32(0), uint32(0), uint32(0),
				int16(0), int16(0), int8(0),
				int16(0), int16(0), int8(0),
				int16(0), int16(0), int8(0),
				int16(0), int16(0), int8(0),
				int16(0), int16(0), int8(0),
				uint32(0), uint8(1), uint8(1), uint64(42), uint32(0),
				uint8(2),
			)

		mock.ExpectQuery(`SELECT .* FROM "inventory" WHERE char_id = \$1 ORDER BY id ASC`).
			WithArgs(uint32(150001)).
			WillReturnRows(rows)

		items, err := repo.ListByChar(context.Background(), 150001)
		require.NoError(t, err)
		require.Len(t, items, 2)

		// First row — a plain grid item.
		assert.Equal(t, uint32(1), items[0].ID)
		assert.Equal(t, uint32(150001), items[0].CharID)
		assert.Equal(t, uint32(501), items[0].NameID)
		assert.Equal(t, uint32(7), items[0].Amount)
		assert.Equal(t, domain.EquipSlot(0), items[0].Equip)
		assert.Equal(t, int16(1), items[0].Identify)
		assert.Equal(t, uint8(0), items[0].Refine)
		assert.Equal(t, uint8(0), items[0].Favorite)
		assert.Equal(t, uint64(0), items[0].UniqueID)

		// Second row — an equipped, starred, bound item with a card
		// slot and a non-zero enchantgrade.
		assert.Equal(t, uint32(2), items[1].ID)
		assert.Equal(t, domain.EquipSlot(1), items[1].Equip)
		assert.Equal(t, uint8(7), items[1].Refine)
		assert.Equal(t, uint32(4001), items[1].Card0)
		assert.Equal(t, uint8(1), items[1].Favorite)
		assert.Equal(t, uint8(1), items[1].Bound)
		assert.Equal(t, uint64(42), items[1].UniqueID)
		assert.Equal(t, uint8(2), items[1].EnchantGrade)

		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("zero charID is rejected without hitting DB", func(t *testing.T) {
		t.Parallel()
		gormDB, mock := newInventoryMockGormDB(t)
		repo := repository.NewInventoryRepository(gormDB)
		// mock has no expectations — any query would fail this test.
		items, err := repo.ListByChar(context.Background(), 0)
		require.Error(t, err)
		assert.Nil(t, items)
		assert.Contains(t, err.Error(), "charID must be > 0")
		assert.NoError(t, mock.ExpectationsWereMet(), "no SQL should have been issued")
	})

	t.Run("empty result returns an empty slice with nil error", func(t *testing.T) {
		t.Parallel()
		gormDB, mock := newInventoryMockGormDB(t)
		repo := repository.NewInventoryRepository(gormDB)

		mock.ExpectQuery(`SELECT .* FROM "inventory" WHERE char_id = \$1 ORDER BY id ASC`).
			WithArgs(uint32(999999)).
			WillReturnRows(sqlmock.NewRows(inventoryColumns))

		items, err := repo.ListByChar(context.Background(), 999999)
		require.NoError(t, err)
		assert.NotNil(t, items, "empty slice, not nil, to make for-range safe at the caller")
		assert.Empty(t, items)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("arbitrary DB errors wrap with char id context", func(t *testing.T) {
		t.Parallel()
		gormDB, mock := newInventoryMockGormDB(t)
		repo := repository.NewInventoryRepository(gormDB)

		boom := assert.AnError
		mock.ExpectQuery(`SELECT .* FROM "inventory" WHERE char_id = \$1 ORDER BY id ASC`).
			WithArgs(uint32(150001)).
			WillReturnError(boom)

		items, err := repo.ListByChar(context.Background(), 150001)
		require.Error(t, err)
		assert.Nil(t, items)
		assert.ErrorIs(t, err, boom)
		assert.Contains(t, err.Error(), "150001")
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestInventoryRepository_Add(t *testing.T) {
	t.Parallel()

	t.Run("happy path returns the autoincrement id", func(t *testing.T) {
		t.Parallel()
		gormDB, mock := newInventoryMockGormDB(t)
		repo := repository.NewInventoryRepository(gormDB)

		item := domain.InventoryItem{
			NameID: 501,
			Amount: 1,
			Options: [5]domain.ItemOption{
				{ID: 1, Value: 2, Parm: 0},
			},
		}

		mock.ExpectQuery(regexp.QuoteMeta(
			`INSERT INTO "inventory" ("char_id","nameid","amount","equip","identify","refine","attribute","card0","card1","card2","card3","option_id0","option_val0","option_parm0","option_id1","option_val1","option_parm1","option_id2","option_val2","option_parm2","option_id3","option_val3","option_parm3","option_id4","option_val4","option_parm4","expire_time","favorite","bound","unique_id","equip_switch","enchantgrade") VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32) RETURNING "id"`,
		)).
			WithArgs(
				uint32(150001), uint32(501), uint32(1), uint32(0), int16(0),
				uint8(0), uint8(0),
				uint32(0), uint32(0), uint32(0), uint32(0),
				int16(1), int16(2), int8(0),
				int16(0), int16(0), int8(0),
				int16(0), int16(0), int8(0),
				int16(0), int16(0), int8(0),
				int16(0), int16(0), int8(0),
				uint32(0), uint8(0), uint8(0), uint64(0), uint32(0),
				uint8(0),
			).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(uint32(42)))

		id, err := repo.Add(context.Background(), 150001, item)
		require.NoError(t, err)
		assert.Equal(t, uint32(42), id)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("zero charID is rejected without hitting DB", func(t *testing.T) {
		t.Parallel()
		gormDB, mock := newInventoryMockGormDB(t)
		repo := repository.NewInventoryRepository(gormDB)

		id, err := repo.Add(context.Background(), 0, domain.InventoryItem{NameID: 501})
		require.Error(t, err)
		assert.Equal(t, uint32(0), id)
		assert.Contains(t, err.Error(), "charID must be > 0")
		assert.NoError(t, mock.ExpectationsWereMet(), "no SQL should have been issued")
	})

	t.Run("arbitrary DB errors wrap with char/nameid context", func(t *testing.T) {
		t.Parallel()
		gormDB, mock := newInventoryMockGormDB(t)
		repo := repository.NewInventoryRepository(gormDB)

		boom := assert.AnError
		mock.ExpectQuery(`INSERT INTO "inventory"`).
			WillReturnError(boom)

		id, err := repo.Add(context.Background(), 150001, domain.InventoryItem{NameID: 501})
		require.Error(t, err)
		assert.Equal(t, uint32(0), id)
		assert.ErrorIs(t, err, boom)
		assert.Contains(t, err.Error(), "150001")
		assert.Contains(t, err.Error(), "501")
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestInventoryRepository_UpdateAmount(t *testing.T) {
	t.Parallel()

	t.Run("happy path issues an UPDATE with the new amount", func(t *testing.T) {
		t.Parallel()
		gormDB, mock := newInventoryMockGormDB(t)
		repo := repository.NewInventoryRepository(gormDB)

		mock.ExpectExec(`UPDATE "inventory" SET "amount"=\$1 WHERE id = \$2`).
			WithArgs(uint32(99), uint32(42)).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.UpdateAmount(context.Background(), 42, 99)
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("zero id returns ErrItemNotFound without hitting DB", func(t *testing.T) {
		t.Parallel()
		gormDB, mock := newInventoryMockGormDB(t)
		repo := repository.NewInventoryRepository(gormDB)

		err := repo.UpdateAmount(context.Background(), 0, 1)
		require.Error(t, err)
		assert.ErrorIs(t, err, domain.ErrItemNotFound)
		assert.NoError(t, mock.ExpectationsWereMet(), "no SQL should have been issued")
	})

	t.Run("missing row surfaces ErrItemNotFound instead of silent no-op", func(t *testing.T) {
		t.Parallel()
		gormDB, mock := newInventoryMockGormDB(t)
		repo := repository.NewInventoryRepository(gormDB)

		mock.ExpectExec(`UPDATE "inventory" SET "amount"=\$1 WHERE id = \$2`).
			WithArgs(uint32(99), uint32(404)).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.UpdateAmount(context.Background(), 404, 99)
		require.Error(t, err)
		assert.ErrorIs(t, err, domain.ErrItemNotFound)
		assert.Contains(t, err.Error(), "404")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("DB error is wrapped with id context", func(t *testing.T) {
		t.Parallel()
		gormDB, mock := newInventoryMockGormDB(t)
		repo := repository.NewInventoryRepository(gormDB)

		boom := assert.AnError
		mock.ExpectExec(`UPDATE "inventory" SET "amount"=\$1 WHERE id = \$2`).
			WithArgs(uint32(99), uint32(42)).
			WillReturnError(boom)

		err := repo.UpdateAmount(context.Background(), 42, 99)
		require.Error(t, err)
		assert.ErrorIs(t, err, boom)
		assert.Contains(t, err.Error(), "42")
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestInventoryRepository_Remove(t *testing.T) {
	t.Parallel()

	t.Run("happy path deletes the row", func(t *testing.T) {
		t.Parallel()
		gormDB, mock := newInventoryMockGormDB(t)
		repo := repository.NewInventoryRepository(gormDB)

		mock.ExpectExec(`DELETE FROM "inventory" WHERE id = \$1`).
			WithArgs(uint32(42)).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.Remove(context.Background(), 42)
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("zero id returns ErrItemNotFound without hitting DB", func(t *testing.T) {
		t.Parallel()
		gormDB, mock := newInventoryMockGormDB(t)
		repo := repository.NewInventoryRepository(gormDB)

		err := repo.Remove(context.Background(), 0)
		require.Error(t, err)
		assert.ErrorIs(t, err, domain.ErrItemNotFound)
		assert.NoError(t, mock.ExpectationsWereMet(), "no SQL should have been issued")
	})

	t.Run("missing row surfaces ErrItemNotFound", func(t *testing.T) {
		t.Parallel()
		gormDB, mock := newInventoryMockGormDB(t)
		repo := repository.NewInventoryRepository(gormDB)

		mock.ExpectExec(`DELETE FROM "inventory" WHERE id = \$1`).
			WithArgs(uint32(404)).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.Remove(context.Background(), 404)
		require.Error(t, err)
		assert.ErrorIs(t, err, domain.ErrItemNotFound)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("DB error is wrapped with id context", func(t *testing.T) {
		t.Parallel()
		gormDB, mock := newInventoryMockGormDB(t)
		repo := repository.NewInventoryRepository(gormDB)

		boom := assert.AnError
		mock.ExpectExec(`DELETE FROM "inventory" WHERE id = \$1`).
			WithArgs(uint32(42)).
			WillReturnError(boom)

		err := repo.Remove(context.Background(), 42)
		require.Error(t, err)
		assert.ErrorIs(t, err, boom)
		assert.Contains(t, err.Error(), "42")
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestInventoryRepository_SetEquip(t *testing.T) {
	t.Parallel()

	t.Run("happy path issues an UPDATE with the new equip mask", func(t *testing.T) {
		t.Parallel()
		gormDB, mock := newInventoryMockGormDB(t)
		repo := repository.NewInventoryRepository(gormDB)

		mock.ExpectExec(`UPDATE "inventory" SET "equip"=\$1 WHERE id = \$2`).
			WithArgs(uint32(0x40), uint32(42)).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.SetEquip(context.Background(), 42, 0x40)
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("zero id returns ErrItemNotFound without hitting DB", func(t *testing.T) {
		t.Parallel()
		gormDB, mock := newInventoryMockGormDB(t)
		repo := repository.NewInventoryRepository(gormDB)

		err := repo.SetEquip(context.Background(), 0, 1)
		require.Error(t, err)
		assert.ErrorIs(t, err, domain.ErrItemNotFound)
		assert.NoError(t, mock.ExpectationsWereMet(), "no SQL should have been issued")
	})

	t.Run("missing row surfaces ErrItemNotFound", func(t *testing.T) {
		t.Parallel()
		gormDB, mock := newInventoryMockGormDB(t)
		repo := repository.NewInventoryRepository(gormDB)

		mock.ExpectExec(`UPDATE "inventory" SET "equip"=\$1 WHERE id = \$2`).
			WithArgs(uint32(0x40), uint32(404)).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.SetEquip(context.Background(), 404, 0x40)
		require.Error(t, err)
		assert.ErrorIs(t, err, domain.ErrItemNotFound)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("DB error is wrapped with id context", func(t *testing.T) {
		t.Parallel()
		gormDB, mock := newInventoryMockGormDB(t)
		repo := repository.NewInventoryRepository(gormDB)

		boom := assert.AnError
		mock.ExpectExec(`UPDATE "inventory" SET "equip"=\$1 WHERE id = \$2`).
			WithArgs(uint32(0x40), uint32(42)).
			WillReturnError(boom)

		err := repo.SetEquip(context.Background(), 42, 0x40)
		require.Error(t, err)
		assert.ErrorIs(t, err, boom)
		assert.Contains(t, err.Error(), "42")
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// TestInventoryRepository_ModelMapping exercises the in-memory field
// mapping logic without involving the DB. This guards the boundary
// between "row has zero/default" and "domain uses zero value" — the
// regression-prone case when schema drift introduces a new column.
func TestInventoryRepository_ModelMapping(t *testing.T) {
	t.Parallel()

	t.Run("toDomain on nil model returns zero item", func(t *testing.T) {
		t.Parallel()
		item := repository.InventoryModelToDomainForTest(nil)
		assert.Equal(t, domain.InventoryItem{}, item)
	})

	t.Run("toDomain round-trips a fully populated model", func(t *testing.T) {
		t.Parallel()
		m := &repository.InventoryModel{
			ID:        42,
			CharID:    150001,
			NameID:    501,
			Amount:    7,
			Equip:     0x80,
			Identify:  1,
			Refine:    5,
			Attribute: 2,
			Card0:     4001,
			Card1:     4002,
			Card2:     4003,
			Card3:     4004,
			OptionID0: 1, OptionVal0: 10, OptionParm0: 0,
			OptionID1: 2, OptionVal1: 20, OptionParm1: 1,
			OptionID2: 3, OptionVal2: 30, OptionParm2: 0,
			OptionID3: 4, OptionVal3: 40, OptionParm3: 1,
			OptionID4: 5, OptionVal4: 50, OptionParm4: 0,
			ExpireTime:   1700000000,
			Favorite:     1,
			Bound:        1,
			UniqueID:     1234567890123,
			EquipSwitch:  0x100,
			EnchantGrade: 4,
		}
		item := repository.InventoryModelToDomainForTest(m)
		assert.Equal(t, uint32(42), item.ID)
		assert.Equal(t, domain.EquipSlot(0x80), item.Equip)
		assert.Equal(t, [5]domain.ItemOption{
			{ID: 1, Value: 10, Parm: 0},
			{ID: 2, Value: 20, Parm: 1},
			{ID: 3, Value: 30, Parm: 0},
			{ID: 4, Value: 40, Parm: 1},
			{ID: 5, Value: 50, Parm: 0},
		}, item.Options)
		assert.Equal(t, uint64(1234567890123), item.UniqueID)
		assert.Equal(t, uint8(4), item.EnchantGrade)
	})

	t.Run("fromDomainMaterialize clears the autoincrement id and pins char_id", func(t *testing.T) {
		t.Parallel()
		item := domain.InventoryItem{
			ID:     999, // should be ignored on insert
			NameID: 501,
			Amount: 1,
			Options: [5]domain.ItemOption{
				{ID: 7, Value: 8, Parm: 9},
			},
		}
		m := repository.FromDomainMaterializeForTest(150001, item)
		assert.Equal(t, uint32(0), m.ID, "id must be 0 so the DB autoincrement fills it")
		assert.Equal(t, uint32(150001), m.CharID, "char_id must be the one passed in, not item.CharID")
		assert.Equal(t, uint32(501), m.NameID)
		assert.Equal(t, uint32(1), m.Amount)
		assert.Equal(t, int16(7), m.OptionID0)
		assert.Equal(t, int16(8), m.OptionVal0)
		assert.Equal(t, int8(9), m.OptionParm0)
	})
}
