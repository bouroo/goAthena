package infra

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/valkey-io/valkey-go"

	"github.com/bouroo/goAthena/internal/modules/character/domain"
)

// ValkeySessionStore persists login sessions in Valkey with a TTL, so the char
// server can validate a CH_ENTER without a DB round-trip. Keys are scoped by
// account id and expire a few minutes after login (enough for char-select).
type ValkeySessionStore struct {
	client valkey.Client
	ttl    time.Duration
}

// NewValkeySessionStore wraps a valkey-go client with a session TTL.
func NewValkeySessionStore(client valkey.Client, ttl time.Duration) *ValkeySessionStore {
	return &ValkeySessionStore{client: client, ttl: ttl}
}

func (s *ValkeySessionStore) key(accountID uint32) string {
	return fmt.Sprintf("goathena:session:%d", accountID)
}

// PutSession stores the session JSON with the configured TTL.
func (s *ValkeySessionStore) PutSession(ctx context.Context, sess domain.Session) error {
	raw, err := json.Marshal(sess)
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}
	if err := s.client.Do(ctx, s.client.B().
		Set().
		Key(s.key(sess.AccountID)).
		Value(string(raw)).
		ExSeconds(int64(s.ttl.Seconds())). //nolint:gosec // G115: ttl seconds bounded.
		Build()).Error(); err != nil {
		return fmt.Errorf("session put: %w", err)
	}
	return nil
}

// GetSession returns the stored session or domain.ErrSessionNotFound.
func (s *ValkeySessionStore) GetSession(ctx context.Context, accountID uint32) (domain.Session, error) {
	res := s.client.Do(ctx, s.client.B().Get().Key(s.key(accountID)).Build())
	if err := res.Error(); err != nil {
		// valkey-go returns a nil-value error when the key is absent.
		if errors.Is(err, valkey.Nil) {
			return domain.Session{}, domain.ErrSessionNotFound
		}
		return domain.Session{}, fmt.Errorf("session get: %w", err)
	}
	raw, err := res.ToString()
	if err != nil {
		return domain.Session{}, fmt.Errorf("session: %w", err)
	}
	var sess domain.Session
	if err := json.Unmarshal([]byte(raw), &sess); err != nil {
		return domain.Session{}, fmt.Errorf("unmarshal session: %w", err)
	}
	return sess, nil
}

// DeleteSession removes the session (called once char/map has consumed it).
func (s *ValkeySessionStore) DeleteSession(ctx context.Context, accountID uint32) error {
	if err := s.client.Do(ctx, s.client.B().Del().Key(s.key(accountID)).Build()).Error(); err != nil {
		return fmt.Errorf("session delete: %w", err)
	}
	return nil
}
