// Package app implements the account bounded context use cases. The login
// AuthService authenticates a client credential against the account repository
// and issues the two session tokens rAthena carries through the char/map
// handshake. It owns no game state.
package app

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/bouroo/goAthena/internal/modules/account/domain"
)

// AuthService authenticates accounts and issues login session tokens.
type AuthService struct {
	repos          domain.AccountRepository
	useMD5Password bool
}

// NewAuthService builds an Authenticator backed by repo. When useMD5Password is
// true (rAthena use_MD5_passwords), stored passwords are MD5 hex and the
// submitted plaintext is hashed before comparison; otherwise plaintext compare.
func NewAuthService(repo domain.AccountRepository, useMD5Password bool) *AuthService {
	return &AuthService{repos: repo, useMD5Password: useMD5Password}
}

// Authenticate resolves the account, verifies the password in constant time,
// rejects banned accounts, and returns the account plus two 32-bit session
// tokens (loginID1, loginID2) the char/map servers will echo back.
func (s *AuthService) Authenticate(ctx context.Context, userID, password, ip string) (domain.Account, uint32, uint32, error) {
	acc, err := s.repos.FindByUserID(ctx, userID)
	if err != nil {
		if errors.Is(err, domain.ErrAccountNotFound) {
			return domain.Account{}, 0, 0, domain.ErrAccountNotFound
		}
		return domain.Account{}, 0, 0, fmt.Errorf("account lookup: %w", err)
	}

	if !s.passwordOK(password, acc.UserPass) {
		return domain.Account{}, 0, 0, domain.ErrInvalidPassword
	}
	if acc.IsBanned() {
		return domain.Account{}, 0, 0, domain.ErrAccountBanned
	}

	id1, id2, err := newSessionTokens()
	if err != nil {
		return domain.Account{}, 0, 0, fmt.Errorf("session tokens: %w", err)
	}
	// Best-effort telemetry: a record-login failure must not fail auth.
	if rerr := s.repos.RecordLogin(ctx, acc.ID, ip); rerr != nil {
		_ = rerr
	}
	return acc, id1, id2, nil
}

// passwordOK compares the submitted password against the stored value in
// constant time. With MD5 mode the store holds the MD5 hex of the password.
func (s *AuthService) passwordOK(submitted, stored string) bool {
	var candidate string
	if s.useMD5Password {
		candidate = md5hex(submitted)
	} else {
		candidate = submitted
	}
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(stored)) == 1
}

// newSessionTokens returns two random 32-bit session keys read from crypto/rand.
func newSessionTokens() (uint32, uint32, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return 0, 0, fmt.Errorf("read rand: %w", err)
	}
	id1 := binary.LittleEndian.Uint32(buf[0:4])
	id2 := binary.LittleEndian.Uint32(buf[4:8])
	return id1, id2, nil
}

// md5hex returns the lowercase hex MD5 digest of s, matching rAthena's
// use_MD5_passwords storage format.
func md5hex(s string) string {
	h := md5sum([]byte(s))
	dst := make([]byte, 32)
	hex.Encode(dst, h[:])
	return string(dst)
}
