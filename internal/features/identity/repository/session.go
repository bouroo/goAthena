package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	valkeygo "github.com/valkey-io/valkey-go"

	"github.com/bouroo/goAthena/internal/features/identity/domain"
)

// sessionKey returns the Valkey key for a session. One session per
// account: the gateway re-issues Put on every login, so a second login
// overwrites the prior node (matching rAthena's auth_db semantics where
// the second login bumps the first).
func sessionKey(accountID uint32) string {
	return fmt.Sprintf("session:account:%d", accountID)
}

type sessionRepo struct {
	client valkeygo.Client
}

// NewSessionRepository returns a Valkey-backed SessionRepository. The
// caller owns the valkey client and is responsible for closing it.
func NewSessionRepository(client valkeygo.Client) domain.SessionRepository {
	return &sessionRepo{client: client}
}

// Put stores a session as JSON with the given TTL. The key is owned by
// accountID; concurrent writes for the same account last-write-wins,
// matching rAthena's overwrite-on-relogin behavior.
func (r *sessionRepo) Put(ctx context.Context, sess *domain.Session, ttl time.Duration) error {
	if sess == nil {
		return fmt.Errorf("put session: session is nil")
	}
	data, err := json.Marshal(sess)
	if err != nil {
		return fmt.Errorf("marshal session for account %d: %w", sess.AccountID, err)
	}
	if err := r.client.Do(ctx, r.client.B().
		Set().
		Key(sessionKey(sess.AccountID)).
		Value(string(data)).
		Ex(ttl).
		Build()).Error(); err != nil {
		return fmt.Errorf("put session for account %d: %w", sess.AccountID, err)
	}
	return nil
}

// Get retrieves a session by account_id. Returns (nil, nil) when the key
// is absent — per the domain contract callers distinguish "no session"
// from transport errors via the nil check on the error.
func (r *sessionRepo) Get(ctx context.Context, accountID uint32) (*domain.Session, error) {
	resp := r.client.Do(ctx, r.client.B().Get().Key(sessionKey(accountID)).Build())
	if err := resp.Error(); err != nil {
		// A Valkey nil reply indicates the key is absent — translate to
		// the documented (nil, nil) contract.
		if valkeygo.IsValkeyNil(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("get session for account %d: %w", accountID, err)
	}
	data, err := resp.ToString()
	if err != nil {
		if valkeygo.IsValkeyNil(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("get session for account %d: %w", accountID, err)
	}
	var sess domain.Session
	if err := json.Unmarshal([]byte(data), &sess); err != nil {
		return nil, fmt.Errorf("unmarshal session for account %d: %w", accountID, err)
	}
	return &sess, nil
}

// Delete removes a session. Missing keys are not an error — callers use
// Delete as best-effort cleanup on logout.
func (r *sessionRepo) Delete(ctx context.Context, accountID uint32) error {
	if err := r.client.Do(ctx, r.client.B().Del().Key(sessionKey(accountID)).Build()).Error(); err != nil {
		return fmt.Errorf("delete session for account %d: %w", accountID, err)
	}
	return nil
}
