package infra

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	valkeygo "github.com/valkey-io/valkey-go"

	"github.com/bouroo/goAthena/internal/modules/account/domain"
)

// sessionTTL bounds how long a login session survives without further
// char/map confirmation. rAthena keeps auth_db entries until the map server
// removes them or the session times out; a bounded TTL prevents unbounded
// growth from clients that log in but never proceed. 24h covers a long play
// session; make it configurable only when operator-controlled lifetime is
// needed.
const sessionTTL = 24 * time.Hour

// sessionKey is the Valkey key for one account's auth session. rAthena's
// auth_db is keyed by account_id, not by token, so the char/map verify step
// looks up by the AID the client presents — we mirror that.
func sessionKey(accountID uint32) string {
	return "goathena:session:" + strconv.FormatUint(uint64(accountID), 10)
}

// ValkeySessionStore is the durable domain.SessionStore backed by Valkey. A
// session is JSON-encoded under goathena:session:<accountID> with a TTL; Put
// overwrites any prior entry on re-login, matching rAthena's auth_db
// overwrite-on-every-successful-CA_LOGIN semantics. The Valkey client is owned
// by the DI container (infrastructure/messaging/valkey), not here.
type ValkeySessionStore struct {
	client valkeygo.Client
}

// NewValkeySessionStore binds the session store to a connected Valkey client.
func NewValkeySessionStore(client valkeygo.Client) *ValkeySessionStore {
	return &ValkeySessionStore{client: client}
}

// Put serializes and stores the session, overwriting any prior entry. The TTL
// resets on each login so an active session never silently expires mid-play.
func (s *ValkeySessionStore) Put(ctx context.Context, sess *domain.Session) error {
	payload, err := json.Marshal(sess)
	if err != nil {
		return fmt.Errorf("encode session %d: %w", sess.AccountID, err)
	}
	cmd := s.client.B().Set().
		Key(sessionKey(sess.AccountID)).
		Value(string(payload)).
		ExSeconds(int64(sessionTTL / time.Second)).
		Build()
	if err := s.client.Do(ctx, cmd).Error(); err != nil {
		return fmt.Errorf("valkey put session %d: %w", sess.AccountID, err)
	}
	return nil
}

// Get loads the session. A missing key surfaces as domain.ErrSessionNotFound
// (mapped from valkey.Nil) so the service treats it as an expected outcome,
// not an infra fault.
func (s *ValkeySessionStore) Get(ctx context.Context, accountID uint32) (*domain.Session, error) {
	cmd := s.client.B().Get().Key(sessionKey(accountID)).Build()
	val, err := s.client.Do(ctx, cmd).ToString()
	if err != nil {
		if errors.Is(err, valkeygo.Nil) {
			return nil, domain.ErrSessionNotFound
		}
		return nil, fmt.Errorf("valkey get session %d: %w", accountID, err)
	}
	var sess domain.Session
	if err := json.Unmarshal([]byte(val), &sess); err != nil {
		return nil, fmt.Errorf("decode session %d: %w", accountID, err)
	}
	return &sess, nil
}

// Delete removes the session. Idempotent: a missing key is not an error,
// matching the in-memory store and rAthena's auth_db delete semantics.
func (s *ValkeySessionStore) Delete(ctx context.Context, accountID uint32) error {
	cmd := s.client.B().Del().Key(sessionKey(accountID)).Build()
	if err := s.client.Do(ctx, cmd).Error(); err != nil {
		return fmt.Errorf("valkey del session %d: %w", accountID, err)
	}
	return nil
}
