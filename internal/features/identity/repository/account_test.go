//go:build unit

package repository_test

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/bouroo/goAthena/internal/features/identity/domain"
	"github.com/bouroo/goAthena/internal/features/identity/repository"
)

// newMockGormDB wires a *gorm.DB onto a sqlmock-backed *sql.DB so the
// repository's queries can be exercised deterministically without a live
// database. The postgres dialector is used because sqlmock's
// placeholder/quoting semantics are stable across drivers; the
// repository code is dialect-agnostic for SELECTs/Updates that don't
// touch driver-specific syntax.
func newMockGormDB(t *testing.T) (*gorm.DB, sqlmock.Sqlmock) {
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

// loginColumns is the column whitelist used by the auth reads. The order
// here must mirror the repository's SELECT clause so AddRow(...)
// arguments line up with the scan target fields.
var loginColumns = []string{
	"account_id", "userid", "user_pass", "sex", "email",
	"group_id", "state", "unban_time", "expiration_time", "logincount",
	"lastlogin", "last_ip", "birthdate", "character_slots",
	"web_auth_token", "web_auth_token_enabled", "vip_time", "old_group",
}

func sampleLoginRows(userID string, accountID uint32) *sqlmock.Rows {
	enabled := int8(1)
	token := "tok_abcdef123456789"
	lastLogin := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	birth := time.Date(1990, 5, 12, 0, 0, 0, 0, time.UTC)
	return sqlmock.NewRows(loginColumns).AddRow(
		accountID, userID, "p1", "M", "test@test.com",
		int8(0), uint32(0), int64(0), int64(0), uint32(7),
		&lastLogin, "127.0.0.1", &birth, uint8(9),
		&token, enabled, int64(0), int8(0),
	)
}

func TestAccountRepository_LoadByUserID(t *testing.T) {
	t.Parallel()

	t.Run("happy path populates domain account from row", func(t *testing.T) {
		t.Parallel()
		gormDB, mock := newMockGormDB(t)
		repo := repository.NewAccountRepository(gormDB)

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT account_id, userid, user_pass, sex, email, group_id, state, unban_time, expiration_time, logincount, lastlogin, last_ip, birthdate, character_slots, web_auth_token, web_auth_token_enabled, vip_time, old_group FROM "login" WHERE userid = $1 ORDER BY "login"."account_id" LIMIT $2`,
		)).
			WithArgs("test", 1).
			WillReturnRows(sampleLoginRows("test", 2000000))

		acc, err := repo.LoadByUserID(context.Background(), "test")
		require.NoError(t, err)
		require.NotNil(t, acc)
		assert.Equal(t, uint32(2000000), acc.AccountID)
		assert.Equal(t, "test", acc.UserID)
		assert.Equal(t, domain.SexMale, acc.Sex)
		assert.Equal(t, "test@test.com", acc.Email)
		assert.Equal(t, uint8(0), acc.GroupID)
		assert.Equal(t, uint32(7), acc.LoginCount)
		assert.Equal(t, "127.0.0.1", acc.LastIP)
		assert.Equal(t, "1990-05-12", acc.Birthdate)
		assert.Equal(t, uint8(9), acc.CharacterSlots)
		assert.Equal(t, "tok_abcdef123456789", acc.WebAuthToken)
		assert.True(t, acc.WebAuthTokenEnabled, "non-zero tinyint must map to true")
		assert.Equal(t, 2026, acc.LastLogin.Year())
		assert.True(t, acc.UnbanTime.IsZero())
		assert.True(t, acc.ExpirationTime.IsZero())
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("empty userid returns ErrAccountNotFound without hitting DB", func(t *testing.T) {
		t.Parallel()
		gormDB, mock := newMockGormDB(t)
		repo := repository.NewAccountRepository(gormDB)
		// mock has no expectations — any query would fail this test.
		acc, err := repo.LoadByUserID(context.Background(), "")
		require.Error(t, err)
		assert.Nil(t, acc)
		assert.ErrorIs(t, err, domain.ErrAccountNotFound)
		assert.NoError(t, mock.ExpectationsWereMet(), "no SQL should have been issued")
	})

	t.Run("gorm ErrRecordNotFound maps to typed ErrAccountNotFound", func(t *testing.T) {
		t.Parallel()
		gormDB, mock := newMockGormDB(t)
		repo := repository.NewAccountRepository(gormDB)

		mock.ExpectQuery(`SELECT .* FROM "login" WHERE userid = \$1 ORDER BY .* LIMIT \$2`).
			WithArgs("missing", 1).
			WillReturnError(gorm.ErrRecordNotFound)

		acc, err := repo.LoadByUserID(context.Background(), "missing")
		require.Error(t, err)
		assert.Nil(t, acc)
		assert.ErrorIs(t, err, domain.ErrAccountNotFound)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("arbitrary DB errors wrap without losing the cause", func(t *testing.T) {
		t.Parallel()
		gormDB, mock := newMockGormDB(t)
		repo := repository.NewAccountRepository(gormDB)

		boom := assert.AnError
		mock.ExpectQuery(`SELECT .* FROM "login" WHERE userid = \$1 ORDER BY .* LIMIT \$2`).
			WithArgs("explode", 1).
			WillReturnError(boom)

		acc, err := repo.LoadByUserID(context.Background(), "explode")
		require.Error(t, err)
		assert.Nil(t, acc)
		assert.ErrorIs(t, err, boom)
		assert.Contains(t, err.Error(), "load account by userid")
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestAccountRepository_LoadByID(t *testing.T) {
	t.Parallel()

	t.Run("happy path", func(t *testing.T) {
		t.Parallel()
		gormDB, mock := newMockGormDB(t)
		repo := repository.NewAccountRepository(gormDB)

		mock.ExpectQuery(`SELECT .* FROM "login" WHERE account_id = \$1 ORDER BY .* LIMIT \$2`).
			WithArgs(uint32(2000000), 1).
			WillReturnRows(sampleLoginRows("test", 2000000))

		acc, err := repo.LoadByID(context.Background(), 2000000)
		require.NoError(t, err)
		assert.Equal(t, uint32(2000000), acc.AccountID)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		t.Parallel()
		gormDB, mock := newMockGormDB(t)
		repo := repository.NewAccountRepository(gormDB)

		mock.ExpectQuery(`SELECT .* FROM "login" WHERE account_id = \$1 ORDER BY .* LIMIT \$2`).
			WithArgs(uint32(404), 1).
			WillReturnError(gorm.ErrRecordNotFound)

		acc, err := repo.LoadByID(context.Background(), 404)
		require.Error(t, err)
		assert.Nil(t, acc)
		assert.ErrorIs(t, err, domain.ErrAccountNotFound)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("arbitrary DB errors wrap with account id context", func(t *testing.T) {
		t.Parallel()
		gormDB, mock := newMockGormDB(t)
		repo := repository.NewAccountRepository(gormDB)

		boom := assert.AnError
		mock.ExpectQuery(`SELECT .* FROM "login" WHERE account_id = \$1 ORDER BY .* LIMIT \$2`).
			WithArgs(uint32(500), 1).
			WillReturnError(boom)

		acc, err := repo.LoadByID(context.Background(), 500)
		require.Error(t, err)
		assert.Nil(t, acc)
		assert.ErrorIs(t, err, boom)
		assert.Contains(t, err.Error(), "500")
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestAccountRepository_UpdateLoginInfo(t *testing.T) {
	t.Parallel()

	t.Run("happy path issues an UPDATE with last_ip, lastlogin, logincount+1", func(t *testing.T) {
		t.Parallel()
		gormDB, mock := newMockGormDB(t)
		repo := repository.NewAccountRepository(gormDB)

		mock.ExpectExec(`UPDATE "login" SET .* WHERE account_id = \$3`).
			WithArgs("1.2.3.4", sqlmock.AnyArg(), uint32(2000000)).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.UpdateLoginInfo(context.Background(), 2000000, "1.2.3.4")
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("missing row surfaces ErrAccountNotFound instead of silent no-op", func(t *testing.T) {
		t.Parallel()
		gormDB, mock := newMockGormDB(t)
		repo := repository.NewAccountRepository(gormDB)

		mock.ExpectExec(`UPDATE "login" SET .* WHERE account_id = \$3`).
			WithArgs("1.2.3.4", sqlmock.AnyArg(), uint32(404)).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.UpdateLoginInfo(context.Background(), 404, "1.2.3.4")
		require.Error(t, err)
		assert.ErrorIs(t, err, domain.ErrAccountNotFound)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("DB error is wrapped with context", func(t *testing.T) {
		t.Parallel()
		gormDB, mock := newMockGormDB(t)
		repo := repository.NewAccountRepository(gormDB)

		boom := assert.AnError
		mock.ExpectExec(`UPDATE "login" SET .* WHERE account_id = \$3`).
			WithArgs("1.2.3.4", sqlmock.AnyArg(), uint32(2000000)).
			WillReturnError(boom)

		err := repo.UpdateLoginInfo(context.Background(), 2000000, "1.2.3.4")
		require.Error(t, err)
		assert.ErrorIs(t, err, boom)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// TestAccountRepository_LoginModelToDomain exercises the in-memory field
// mapping logic for nullable fields without involving the DB. This guards
// the boundary between "row has NULL" and "domain uses zero value" — the
// regression-prone case when schema drift introduces a new nullable
// column.
func TestAccountRepository_LoginModelToDomain(t *testing.T) {
	t.Parallel()

	t.Run("nil model returns nil", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, repository.LoginModelToDomainForTest(nil))
	})

	t.Run("nullable fields collapse to zero/empty values", func(t *testing.T) {
		t.Parallel()
		acc := repository.LoginModelToDomainForTest(&repository.LoginModel{
			AccountID:           2000000,
			UserID:              "u",
			Sex:                 "F",
			GroupID:             5,
			LastLogin:           nil,
			Birthdate:           nil,
			WebAuthToken:        nil,
			WebAuthTokenEnabled: 0,
			UnbanTime:           0,
			ExpirationTime:      0,
		})
		require.NotNil(t, acc)
		assert.Equal(t, domain.SexFemale, acc.Sex)
		assert.Equal(t, uint8(5), acc.GroupID)
		assert.True(t, acc.LastLogin.IsZero())
		assert.Equal(t, "", acc.Birthdate)
		assert.Equal(t, "", acc.WebAuthToken)
		assert.False(t, acc.WebAuthTokenEnabled)
		assert.True(t, acc.UnbanTime.IsZero())
		assert.True(t, acc.ExpirationTime.IsZero())
	})

	t.Run("non-zero unix timestamps are decoded to UTC", func(t *testing.T) {
		t.Parallel()
		acc := repository.LoginModelToDomainForTest(&repository.LoginModel{
			UnbanTime:      1_700_000_000,
			ExpirationTime: 1_800_000_000,
			VipTime:        1_900_000_000,
		})
		require.NotNil(t, acc)
		assert.Equal(t, int64(1700000000), acc.UnbanTime.Unix())
		assert.Equal(t, int64(1800000000), acc.ExpirationTime.Unix())
		assert.Equal(t, int64(1900000000), acc.VipTime.Unix())
	})
}
