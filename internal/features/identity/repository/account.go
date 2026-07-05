package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/bouroo/goAthena/internal/features/identity/domain"
)

// loginSelectColumns is the column whitelist for all auth-relevant reads.
// `pincode` and `pincode_change` are deliberately omitted: they are auth
// material and must not leak into the lobby / session-creation paths.
const loginSelectColumns = "account_id, userid, user_pass, sex, email, group_id, state, " +
	"unban_time, expiration_time, logincount, lastlogin, last_ip, birthdate, " +
	"character_slots, web_auth_token, web_auth_token_enabled, vip_time, old_group"

type accountRepo struct {
	db *gorm.DB
}

// NewAccountRepository wires the MariaDB/Postgres-backed AccountRepository.
func NewAccountRepository(db *gorm.DB) domain.AccountRepository {
	return &accountRepo{db: db}
}

// LoadByUserID fetches an account by its login userid. Wraps
// gorm.ErrRecordNotFound as domain.ErrAccountNotFound so service code can
// compare with errors.Is rather than string matching.
func (r *accountRepo) LoadByUserID(ctx context.Context, userID string) (*domain.Account, error) {
	if userID == "" {
		return nil, fmt.Errorf("load account by userid: %w: empty userid", domain.ErrAccountNotFound)
	}
	var m LoginModel
	err := r.db.WithContext(ctx).
		Select(loginSelectColumns).
		Where("userid = ?", userID).
		First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("%w: userid=%s", domain.ErrAccountNotFound, userID)
	}
	if err != nil {
		return nil, fmt.Errorf("load account by userid %q: %w", userID, err)
	}
	return loginModelToDomain(&m), nil
}

// LoadByID fetches an account by its numeric primary key.
func (r *accountRepo) LoadByID(ctx context.Context, accountID uint32) (*domain.Account, error) {
	var m LoginModel
	err := r.db.WithContext(ctx).
		Select(loginSelectColumns).
		Where("account_id = ?", accountID).
		First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("%w: account_id=%d", domain.ErrAccountNotFound, accountID)
	}
	if err != nil {
		return nil, fmt.Errorf("load account by id %d: %w", accountID, err)
	}
	return loginModelToDomain(&m), nil
}

// UpdateLoginInfo atomically increments logincount and overwrites last_ip
// and lastlogin. Mirrors login.cpp:421-424 where rAthena issues an UPDATE
// that hits all three columns in a single statement.
//
// We pass Go time.Now() directly rather than a literal expression so the
// timestamp is captured at the call site (matching the diagnostics the
// rAthena admin tools emit); the logincount increment is performed by
// MySQL/Postgres itself via gorm.Expr to avoid a read-modify-write race.
func (r *accountRepo) UpdateLoginInfo(ctx context.Context, accountID uint32, ip string) error {
	now := time.Now().UTC()
	res := r.db.WithContext(ctx).
		Model(&LoginModel{}).
		Where("account_id = ?", accountID).
		Updates(map[string]any{
			"logincount": gorm.Expr("logincount + 1"),
			"last_ip":    ip,
			"lastlogin":  now,
		})
	if res.Error != nil {
		return fmt.Errorf("update login info for account %d: %w", accountID, res.Error)
	}
	if res.RowsAffected == 0 {
		// Update on a missing row is a logic error (the caller just
		// authenticated the account), so we surface a typed-not-found
		// rather than a silent no-op.
		return fmt.Errorf("%w: account_id=%d", domain.ErrAccountNotFound, accountID)
	}
	return nil
}

// loginModelToDomain maps the GORM row to a domain entity. Unix-second
// columns collapse to Go zero values when 0, matching the SQL default.
// Nullable columns map to the zero value of the domain type when NULL.
func loginModelToDomain(m *LoginModel) *domain.Account {
	if m == nil {
		return nil
	}
	return &domain.Account{
		AccountID: m.AccountID,
		UserID:    m.UserID,
		UserPass:  m.UserPass,
		Email:     m.Email,
		Sex:       domain.Sex(m.Sex),
		//nolint:gosec // G115: schema is tinyint(3) NOT NULL DEFAULT 0; the domain expects unsigned semantics so we widen.
		GroupID:             uint8(m.GroupID),
		State:               m.State,
		UnbanTime:           unixOrZero(m.UnbanTime),
		ExpirationTime:      unixOrZero(m.ExpirationTime),
		LoginCount:          m.LoginCount,
		LastLogin:           timeOrZero(m.LastLogin),
		LastIP:              m.LastIP,
		Birthdate:           dateOrEmpty(m.Birthdate),
		CharacterSlots:      m.CharacterSlots,
		WebAuthToken:        stringOrEmpty(m.WebAuthToken),
		WebAuthTokenEnabled: m.WebAuthTokenEnabled != 0,
		VipTime:             unixOrZero(m.VipTime),
		//nolint:gosec // G115: schema is tinyint(3) NOT NULL DEFAULT 0; the domain expects unsigned semantics so we widen.
		OldGroup: uint8(m.OldGroup),
	}
}

func unixOrZero(ts int64) time.Time {
	if ts == 0 {
		return time.Time{}
	}
	return time.Unix(ts, 0).UTC()
}

func timeOrZero(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return t.UTC()
}

func dateOrEmpty(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format("2006-01-02")
}

func stringOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// LoginModelToDomainForTest exposes the in-memory conversion to white-box
// tests so they can verify NULL / zero-value handling without an actual
// DB round trip. It is the same function used by the repository methods
// at runtime; the underscore-suffixed name is the canonical
// repository-layer pattern for test-only exports.
func LoginModelToDomainForTest(m *LoginModel) *domain.Account {
	return loginModelToDomain(m)
}

// CharModelToDomainForTest mirrors the helper above for the character repo.
func CharModelToDomainForTest(m *CharModel) domain.CharacterSummary {
	return charModelToDomain(m)
}
