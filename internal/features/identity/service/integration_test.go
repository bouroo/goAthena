//go:build integration

package service

import (
	"context"
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	valkeygo "github.com/valkey-io/valkey-go"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/bouroo/goAthena/internal/features/identity/domain"
	"github.com/bouroo/goAthena/internal/features/identity/repository"
)

// nopLogger returns a zerolog.Logger that discards everything.
func nopLogger() *zerolog.Logger {
	l := zerolog.Nop()
	return &l
}

// mariadbDSN returns the DSN used by integration tests against the local
// MariaDB declared in compose.yml. Override with GOATHENA_TEST_DSN if a
// different endpoint is required.
func mariadbDSN() string {
	if v := os.Getenv("GOATHENA_TEST_DSN"); v != "" {
		return v
	}
	user := envOr("DB_USER", "goathena")
	pass := envOr("DB_PASSWORD", "goathena")
	host := envOr("DB_HOST", "127.0.0.1")
	port := envOr("DB_PORT", "3306")
	name := envOr("DB_NAME", "goathena")
	return user + ":" + pass + "@tcp(" + host + ":" + port + ")/" + name +
		"?parseTime=true&multiStatements=true&charset=utf8mb4&loc=UTC"
}

// valkeyAddr returns the Valkey endpoint for integration tests.
func valkeyAddr() string {
	if v := os.Getenv("VALKEY_ADDR"); v != "" {
		return v
	}
	host := envOr("VALKEY_HOST", "127.0.0.1")
	port := envOr("VALKEY_PORT", "6379")
	return host + ":" + port
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// newIntegrationDB opens a *gorm.DB against the test MariaDB container.
// The underlying *sql.DB is closed via t.Cleanup.
func newIntegrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(mysql.Open(mariadbDSN()), &gorm.Config{
		SkipDefaultTransaction: true,
		Logger:                 logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err, "open mariadb at %s; is the container running?", mariadbDSN())

	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Ping(), "ping mariadb")

	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

// newIntegrationValkeyClient opens a valkey-go client against the
// integration Valkey container.
func newIntegrationValkeyClient(t *testing.T) valkeygo.Client {
	t.Helper()
	client, err := valkeygo.NewClient(valkeygo.ClientOption{
		InitAddress: []string{valkeyAddr()},
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, client.Do(ctx, client.B().Ping().Build()).Error(),
		"ping valkey at %s; is the container running?", valkeyAddr())

	t.Cleanup(func() { client.Close() })
	return client
}

// insertTestLogin inserts a row into the login table with the given
// credentials and returns the assigned account_id.
func insertTestLogin(t *testing.T, db *gorm.DB, userID, pass string, sex domain.Sex) uint32 {
	t.Helper()
	require.NoError(t, db.Exec(
		"INSERT INTO `login` (userid, user_pass, sex, email) VALUES (?, ?, ?, ?)",
		userID, pass, string(sex), userID+"@test.local",
	).Error)
	var id uint32
	require.NoError(t, db.Raw(
		"SELECT account_id FROM `login` WHERE userid = ?", userID,
	).Scan(&id).Error)
	return id
}

// deleteTestAccount removes a test account and any of its characters from
// the schema so each test run starts clean.
func deleteTestAccount(t *testing.T, db *gorm.DB, accountID uint32) {
	t.Helper()
	require.NoError(t, db.Exec("DELETE FROM `char` WHERE account_id = ?", accountID).Error)
	require.NoError(t, db.Exec("DELETE FROM `login` WHERE account_id = ?", accountID).Error)
}

// TestLogin_FullFlow_Integration runs the end-to-end login flow against
// real MariaDB + Valkey. The seed 'test' account (account_id 2000000,
// user_pass 'test', sex 'M') is asserted via a session round-trip.
func TestLogin_FullFlow_Integration(t *testing.T) {
	ctx := context.Background()
	db := newIntegrationDB(t)
	client := newIntegrationValkeyClient(t)

	accountRepo := repository.NewAccountRepository(db)
	charRepo := repository.NewCharacterRepository(db)
	sessRepo := repository.NewSessionRepository(client)

	t.Cleanup(func() {
		_ = sessRepo.Delete(ctx, 2000000)
	})

	svc := NewIdentityService(accountRepo, charRepo, sessRepo, nopLogger(), false, 15)

	ip := netip.MustParseAddr("203.0.113.55")
	resp, err := svc.Login(ctx, domain.LoginRequest{
		UserID:     "test",
		Password:   "test",
		Method:     domain.PassEncodingPlain,
		ClientType: 0,
		RemoteIP:   ip,
	})
	require.NoError(t, err, "test/test must authenticate against the seed row")
	require.NotNil(t, resp)
	require.NotNil(t, resp.Account)

	assert.Equal(t, uint32(2000000), resp.Account.AccountID,
		"account_id for the 'test' seed must match the migration")
	assert.Equal(t, domain.SexMale, resp.Account.Sex,
		"sex must be the value seeded in the migration")
	require.NotNil(t, resp.Session)
	assert.Equal(t, uint32(2000000), resp.Session.AccountID)
	assert.NotZero(t, resp.Session.LoginID1, "login_id1 must be non-zero")
	assert.NotZero(t, resp.Session.LoginID2, "login_id2 must be non-zero")
	assert.NotEqual(t, resp.Session.LoginID1, resp.Session.LoginID2,
		"tokens must differ")
	assert.Equal(t, domain.SexMale, resp.Session.Sex)
	assert.Equal(t, "203.0.113.55", resp.Session.RemoteIP)

	// Session must be readable from Valkey.
	stored, err := sessRepo.Get(ctx, resp.Account.AccountID)
	require.NoError(t, err)
	require.NotNil(t, stored, "session must be persisted in Valkey after Login")
	assert.Equal(t, resp.Session.LoginID1, stored.LoginID1)
	assert.Equal(t, resp.Session.LoginID2, stored.LoginID2)
	assert.Equal(t, resp.Session.AccountID, stored.AccountID)
}

// TestLogin_WrongPassword_Integration asserts that the AuthInvalidPassword
// wire code surfaces as a typed *LoginError.
func TestLogin_WrongPassword_Integration(t *testing.T) {
	ctx := context.Background()
	db := newIntegrationDB(t)
	client := newIntegrationValkeyClient(t)

	accountRepo := repository.NewAccountRepository(db)
	charRepo := repository.NewCharacterRepository(db)
	sessRepo := repository.NewSessionRepository(client)

	svc := NewIdentityService(accountRepo, charRepo, sessRepo, nopLogger(), false, 15)

	ip := netip.MustParseAddr("203.0.113.99")
	_, err := svc.Login(ctx, domain.LoginRequest{
		UserID:   "test",
		Password: "wrong-password",
		Method:   domain.PassEncodingPlain,
		RemoteIP: ip,
	})
	require.Error(t, err)
	var le *LoginError
	require.ErrorAs(t, err, &le)
	assert.Equal(t, domain.AuthInvalidPassword, le.Code)
}

// TestListCharacters_Integration inserts characters, calls ListCharacters,
// and verifies slot ordering and field round-trip. A row in slot 99
// (outside the default 15-slot cap) is included to prove the WHERE filter.
func TestListCharacters_Integration(t *testing.T) {
	ctx := context.Background()
	db := newIntegrationDB(t)
	client := newIntegrationValkeyClient(t)

	accountRepo := repository.NewAccountRepository(db)
	charRepo := repository.NewCharacterRepository(db)
	sessRepo := repository.NewSessionRepository(client)

	// Use a unique userid (max 23 chars per schema) so we do not collide
	// with seeded rows across runs. Format: "cl_<HHMMSS><micros-suffix>"
	// fits well under the varchar(23) cap.
	userID := "cl_" + time.Now().UTC().Format("150405") + "_x"
	acct := insertTestLogin(t, db, userID, "x", domain.SexMale)
	t.Cleanup(func() { deleteTestAccount(t, db, acct) })

	type seedChar struct {
		slot   int
		name   string
		level  uint32
		jlevel uint32
		class  uint16
	}
	seeds := []seedChar{
		{slot: 0, name: userID + "_alpha", level: 25, jlevel: 10, class: 0},
		{slot: 2, name: userID + "_gamma", level: 30, jlevel: 15, class: 1},
		{slot: 4, name: userID + "_eps", level: 40, jlevel: 20, class: 2},
	}
	for _, s := range seeds {
		require.NoError(t, db.Exec(
			"INSERT INTO `char` (account_id, char_num, name, class, base_level, job_level, hp, max_hp, sp, max_sp, sex) "+
				"VALUES (?, ?, ?, ?, ?, ?, 100, 100, 50, 50, 'M')",
			acct, s.slot, s.name, s.class, s.level, s.jlevel,
		).Error)
	}

	// Tombstoned out-of-range slot — must NOT show up in the roster.
	require.NoError(t, db.Exec(
		"INSERT INTO `char` (account_id, char_num, name, class, base_level, job_level, hp, max_hp, sp, max_sp, sex) "+
			"VALUES (?, 99, ?, 0, 1, 1, 10, 10, 0, 0, 'M')",
		acct, userID+"_outofrange",
	).Error)

	svc := NewIdentityService(accountRepo, charRepo, sessRepo, nopLogger(), false, 15)
	got, err := svc.ListCharacters(ctx, acct)
	require.NoError(t, err)
	require.Len(t, got, 3, "only the in-range seeds must be returned, slot-ordered")

	for i := range got {
		assert.Equal(t, seeds[i].slot, int(got[i].Slot))
		assert.Equal(t, seeds[i].name, got[i].Name)
		assert.Equal(t, seeds[i].level, got[i].BaseLevel)
		assert.Equal(t, seeds[i].jlevel, got[i].JobLevel)
		assert.Equal(t, seeds[i].class, got[i].Class)
		assert.Equal(t, acct, got[i].AccountID)
	}
}
