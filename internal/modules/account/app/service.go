// Package app implements the account bounded context's use cases. The gateway
// CA_LOGIN handler depends on the domain.Authenticator port; AuthService is the
// concrete implementation, closing over the account repository and session
// store injected at construction.
package app

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/bouroo/goAthena/internal/modules/account/domain"
)

// Clock abstracts wall time so the expiration/ban checks and the login-record
// timestamp share one injectable instant (deterministic in tests).
type Clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// TokenMinter mints one session-token half (login_id1 / login_id2). rAthena uses
// a uniform random uint32 in [1, MAX] (login.cpp:413-414); production wires a
// crypto-random minter, tests a fixed one.
type TokenMinter func() uint32

// cryptoMinter draws a cryptographically random uint32. A failing system CSPRNG
// is unrecoverable for a token minter — falling back to a predictable value
// would mint guessable sessions — so it panics rather than degrade. crypto/rand
// on 4 bytes does not fail on a functioning host. Zero maps to 1 to honor the
// [1, MAX] range.
func cryptoMinter() uint32 {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("account auth: read crypto random: %v", err))
	}
	v := binary.LittleEndian.Uint32(b[:])
	if v == 0 {
		return 1
	}
	return v
}

// AuthService implements domain.Authenticator. It loads an account, applies the
// rAthena login_mmo_auth rules (unregistered / server-account / plaintext
// password / expiration / ban / state), and on success records the login and
// mints + persists a session.
type AuthService struct {
	accounts domain.AccountRepository
	sessions domain.SessionStore
	clock    Clock
	mint     TokenMinter
}

// NewAuthService builds an AuthService. A nil clock falls back to wall time and
// a nil minter to cryptoMinter, so production callers may pass nil for both;
// tests inject fakes for determinism.
func NewAuthService(accounts domain.AccountRepository, sessions domain.SessionStore, clock Clock, mint TokenMinter) *AuthService {
	if clock == nil {
		clock = systemClock{}
	}
	if mint == nil {
		mint = cryptoMinter
	}
	return &AuthService{accounts: accounts, sessions: sessions, clock: clock, mint: mint}
}

// Login runs the CA_LOGIN use case. A non-nil error means an infra/repository
// fault (the handler closes the connection); an expected refusal is returned as
// a non-accepted LoginResult with a nil error so the handler can send
// AC_REFUSE_LOGIN and keep the connection open for retry.
func (s *AuthService) Login(ctx context.Context, req domain.LoginRequest) (domain.LoginResult, error) {
	acc, err := s.accounts.LoadByUserID(ctx, req.UserID)
	if err != nil {
		if errors.Is(err, domain.ErrAccountNotFound) {
			// login.cpp:347 — unknown userid. A missing row is an expected auth
			// outcome, not an infra fault, so it surfaces as a refusal.
			return refuse(domain.AuthUnregistered), nil
		}
		return domain.LoginResult{}, fmt.Errorf("load account %q: %w", req.UserID, err)
	}

	// login.cpp:350-353 — server/system accounts cannot log in as players.
	if acc.Sex == domain.SexServer {
		return refuse(domain.AuthUnregistered), nil
	}

	// login.cpp:446 — CA_LOGIN (0x0064) carries no passwdenc, so the compare is
	// plaintext. Constant-time compare avoids leaking the stored password via
	// timing despite the plaintext transport.
	if subtle.ConstantTimeCompare([]byte(acc.UserPass), []byte(req.Password)) != 1 {
		return refuse(domain.AuthInvalidPassword), nil
	}

	now := s.clock.Now()

	// login.cpp:360-363 — a non-zero expiration in the past means expired.
	if acc.ExpirationTime != 0 && acc.ExpirationTime < now.Unix() {
		return refuse(domain.AuthExpired), nil
	}

	// login.cpp:365-370 — a non-zero unban time in the future means banned.
	if acc.UnbanTime != 0 && acc.UnbanTime > now.Unix() {
		return refuse(domain.AuthBanned), nil
	}

	// login.cpp:374 — a non-zero state yields the wire code state-1.
	if acc.State != 0 {
		return refuse(domain.AuthCode(acc.State - 1)), nil
	}

	// Success: record the login (logincount++, last_ip, lastlogin) before
	// minting the session, mirroring rAthena's save-then-send order. A failure
	// here is an infra fault — do not grant a session without a recorded login.
	if err := s.accounts.UpdateLoginInfo(ctx, acc.AccountID, req.IP, now); err != nil {
		return domain.LoginResult{}, fmt.Errorf("update login info %d: %w", acc.AccountID, err)
	}

	sess := &domain.Session{
		AccountID: acc.AccountID,
		LoginID1:  s.mint(),
		LoginID2:  s.mint(),
		Sex:       acc.Sex,
		CreatedAt: now,
	}
	if err := s.sessions.Put(ctx, sess); err != nil {
		return domain.LoginResult{}, fmt.Errorf("store session %d: %w", acc.AccountID, err)
	}

	return domain.LoginResult{
		Accepted: true,
		Account:  acc,
		LoginID1: sess.LoginID1,
		LoginID2: sess.LoginID2,
	}, nil
}

func refuse(code domain.AuthCode) domain.LoginResult {
	return domain.LoginResult{Accepted: false, Code: code}
}
