package infra

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/bouroo/goAthena/internal/modules/account/domain"
)

// loginModel maps the rAthena `login` table (migration 000002_identity.up.sql).
// Only the columns the auth slice reads or writes are modeled; the table owns
// many more (email, birthdate, pincode, vip_time, character_slots, …) that
// CA_LOGIN does not touch, and GORM maps by column name so omitting them is
// safe — SELECTs name their columns and Updates use explicit maps.
type loginModel struct {
	AccountID      uint32     `gorm:"column:account_id;primaryKey"`
	UserID         string     `gorm:"column:userid"`
	UserPass       string     `gorm:"column:user_pass"`
	Sex            string     `gorm:"column:sex"`
	State          uint32     `gorm:"column:state"`
	UnbanTime      int64      `gorm:"column:unban_time"`
	ExpirationTime int64      `gorm:"column:expiration_time"`
	WebAuthToken   string     `gorm:"column:web_auth_token"`
	LoginCount     uint32     `gorm:"column:logincount"`
	LastIP         string     `gorm:"column:last_ip"`
	LastLogin      *time.Time `gorm:"column:lastlogin"`
}

// TableName fixes the rAthena table name regardless of GORM's pluralizer.
func (loginModel) TableName() string { return "login" }

func (m loginModel) toDomain() domain.Account {
	acc := domain.Account{
		AccountID:      m.AccountID,
		UserID:         m.UserID,
		UserPass:       m.UserPass,
		Sex:            domain.Sex(m.Sex),
		State:          m.State,
		UnbanTime:      m.UnbanTime,
		ExpirationTime: m.ExpirationTime,
		WebAuthToken:   m.WebAuthToken,
		LoginCount:     m.LoginCount,
		LastIP:         m.LastIP,
	}
	if m.LastLogin != nil {
		acc.LastLogin = *m.LastLogin
	}
	return acc
}

// GORMAccountRepository is the MariaDB/Postgres-backed domain.AccountRepository.
type GORMAccountRepository struct {
	db *gorm.DB
}

// NewGORMAccountRepository binds the adapter to a GORM session. Each query
// re-derives its connection from the request context (WithContext), so a
// surrounding Unit-of-Work can bind a transaction without the adapter knowing.
func NewGORMAccountRepository(db *gorm.DB) *GORMAccountRepository {
	return &GORMAccountRepository{db: db}
}

// LoadByUserID reads the auth-slice columns for one account. Take (not First)
// avoids the implicit ORDER BY on a unique-userid lookup.
func (r *GORMAccountRepository) LoadByUserID(ctx context.Context, userID string) (*domain.Account, error) {
	var m loginModel
	err := r.db.WithContext(ctx).
		Select("account_id, userid, user_pass, sex, state, unban_time, expiration_time, web_auth_token, logincount, last_ip, lastlogin").
		Where("userid = ?", userID).
		Take(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, domain.ErrAccountNotFound
		}
		return nil, fmt.Errorf("load account %q: %w", userID, err)
	}
	acc := m.toDomain()
	return &acc, nil
}

// UpdateLoginInfo atomically increments logincount in the database
// (gorm.Expr avoids a read-modify-write race) and sets last_ip/lastlogin.
func (r *GORMAccountRepository) UpdateLoginInfo(ctx context.Context, accountID uint32, ip string, now time.Time) error {
	res := r.db.WithContext(ctx).Model(&loginModel{}).
		Where("account_id = ?", accountID).
		Updates(map[string]any{
			"logincount": gorm.Expr("logincount + 1"),
			"last_ip":    ip,
			"lastlogin":  now,
		})
	if res.Error != nil {
		return fmt.Errorf("update login info %d: %w", accountID, res.Error)
	}
	if res.RowsAffected == 0 {
		return domain.ErrAccountNotFound
	}
	return nil
}
