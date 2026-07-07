// Package service contains the use-case implementations for the identity
// feature (WS-B): login authentication, character roster retrieval. The
// service layer depends only on outbound ports declared in internal/features/
// identity/domain and is invoked by the gRPC handler in the same feature.
package service

import (
	"context"
	"crypto/md5" //nolint:gosec // G501: MD5 is mandated by the rAthena passwdenc=1 wire protocol.
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/rs/zerolog"

	"github.com/bouroo/goAthena/internal/features/identity/domain"
)

// LoginError carries the AC_REFUSE_LOGIN wire code produced by the auth
// flow. Handlers map Code onto the SC_NOTIFY_BAN / AC_REFUSE_LOGIN packet
// fields per loginclif.cpp:170-206.
type LoginError struct {
	Code domain.AuthError
	Msg  string
}

func (e *LoginError) Error() string {
	return fmt.Sprintf("auth error %d: %s", e.Code, e.Msg)
}

// identityService implements domain.IdentityService. It composes the
// account, character and session repositories into the rAthena login
// state machine (login.cpp:296-434).
type identityService struct {
	accounts   domain.AccountRepository
	characters domain.CharacterRepository
	sessions   domain.SessionRepository
	logger     *zerolog.Logger
	useMD5     bool
	maxChars   int
	now        func() time.Time
}

// Option mutates an identityService during construction. Used to inject
// non-default collaborators (clock, logger) without growing the
// constructor signature forever.
type Option func(*identityService)

// WithClock overrides the time source. Intended for deterministic tests.
func WithClock(now func() time.Time) Option {
	return func(s *identityService) { s.now = now }
}

// NewIdentityService wires the identity use cases. useMD5 is the
// deployment-wide `use_md5_passwds` bit and must match the encoding
// declared on every LoginRequest.Method; a mismatch is rejected with
// AuthRejected per login.cpp:233. maxChars caps the character roster
// (effective = max(account.character_slots, MIN_CHARS)); the default of 15
// matches PACKETVER >= 20100413.
func NewIdentityService(
	accounts domain.AccountRepository,
	characters domain.CharacterRepository,
	sessions domain.SessionRepository,
	logger *zerolog.Logger,
	useMD5 bool,
	maxChars int,
	opts ...Option,
) domain.IdentityService {
	if maxChars <= 0 {
		maxChars = 15
	}
	if logger == nil {
		nop := zerolog.Nop()
		logger = &nop
	}
	s := &identityService{
		accounts:   accounts,
		characters: characters,
		sessions:   sessions,
		logger:     logger,
		useMD5:     useMD5,
		maxChars:   maxChars,
		now:        time.Now,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Login runs the rAthena login_mmo_auth state machine.
//
// Check order (login.cpp:296-434):
//
//  1. encoding match (AuthRejected = 3)
//  2. account load   (AuthUnknownID = 0)
//  3. password       (AuthInvalidPassword = 1)
//  4. expiration     (AuthExpired = 2)
//  5. unban          (AuthBanned = 6)
//  6. state          (state - 1, capped at 254)
//
// On success a Session is persisted with the AUTH_TIMEOUT TTL and the
// account's last_ip / logincount are updated.
func (s *identityService) Login(ctx context.Context, req domain.LoginRequest) (*domain.LoginResponse, error) {
	if (req.Method == domain.PassEncodingMD5) != s.useMD5 {
		return nil, &LoginError{
			Code: domain.AuthRejected,
			Msg:  "passwdenc/use_md5_passwds mismatch",
		}
	}

	acc, err := s.accounts.LoadByUserID(ctx, req.UserID)
	if err != nil {
		if errors.Is(err, domain.ErrAccountNotFound) {
			return nil, &LoginError{
				Code: domain.AuthUnknownID,
				Msg:  "account not found",
			}
		}
		return nil, fmt.Errorf("load account %q: %w", req.UserID, err)
	}

	if !verifyPassword(acc.UserPass, req.Password, req.Method) {
		return nil, &LoginError{
			Code: domain.AuthInvalidPassword,
			Msg:  "password mismatch",
		}
	}

	now := s.now()
	if !acc.ExpirationTime.IsZero() && acc.ExpirationTime.Before(now) {
		return nil, &LoginError{
			Code: domain.AuthExpired,
			Msg:  fmt.Sprintf("account expired at %s", acc.ExpirationTime.Format(time.RFC3339)),
		}
	}

	if !acc.UnbanTime.IsZero() && acc.UnbanTime.After(now) {
		return nil, &LoginError{
			Code: domain.AuthBanned,
			Msg:  fmt.Sprintf("banned until %s", acc.UnbanTime.Format(time.RFC3339)),
		}
	}

	if acc.State != 0 {
		return nil, &LoginError{
			Code: authErrorFromState(acc.State),
			Msg:  fmt.Sprintf("account blocked (state=%d)", acc.State),
		}
	}

	loginID1, err := randomUint32()
	if err != nil {
		return nil, fmt.Errorf("generate login_id1: %w", err)
	}
	loginID2, err := randomUint32()
	if err != nil {
		return nil, fmt.Errorf("generate login_id2: %w", err)
	}

	remoteIP := req.RemoteIP.String()
	sess := &domain.Session{
		AccountID:  acc.AccountID,
		LoginID1:   loginID1,
		LoginID2:   loginID2,
		ClientType: req.ClientType,
		Sex:        acc.Sex,
		RemoteIP:   remoteIP,
		CreatedAt:  now,
	}

	if err := s.sessions.Put(ctx, sess, domain.SessionTTL); err != nil {
		return nil, fmt.Errorf("persist session: %w", err)
	}

	if err := s.accounts.UpdateLoginInfo(ctx, acc.AccountID, remoteIP); err != nil {
		s.logger.Warn().
			Err(err).
			Uint32("account_id", acc.AccountID).
			Msg("update login info failed; session persisted")
	}

	return &domain.LoginResponse{
		Account: acc,
		Session: sess,
	}, nil
}

// ListCharacters returns the character roster for an authenticated
// account. The handler that consumes this response serializes it into
// HC_ACCEPT_ENTER 0x6b.
func (s *identityService) ListCharacters(ctx context.Context, accountID uint32) ([]domain.CharacterSummary, error) {
	chrs, err := s.characters.ListByAccount(ctx, accountID, s.maxChars)
	if err != nil {
		return nil, fmt.Errorf("list characters for account %d: %w", accountID, err)
	}
	if chrs == nil {
		return []domain.CharacterSummary{}, nil
	}
	return chrs, nil
}

// GetCharacter returns the full character detail (name, class, level,
// HP, hair, equipment, sex) for a single character on an authenticated
// account. The gateway calls this on CZ_ENTER to populate the entity
// spawn packet (ZC_SPAWN_UNIT 0x09fe) with real character data.
//
// domain.ErrCharacterNotFound is preserved in the error chain so the
// handler can map it onto success=false via errors.Is; the wrap with
// the (accountID, charID) context gives log triage the call site
// without changing the sentinel's identity in the chain.
func (s *identityService) GetCharacter(ctx context.Context, accountID, charID uint32) (*domain.CharacterSummary, error) {
	char, err := s.characters.GetByID(ctx, accountID, charID)
	if err != nil {
		return nil, fmt.Errorf("get character (account=%d, char=%d): %w", accountID, charID, err)
	}
	return char, nil
}

// authErrorFromState maps acc.state (login.cpp:372-375) to the wire code.
// state=0 means OK and is handled by the caller; any non-zero state maps
// to state-1, clamped to the AuthError range to avoid wrap-around on
// absurdly large state values.
func authErrorFromState(state uint32) domain.AuthError {
	if state == 0 {
		return domain.AuthOK
	}
	code := min(state-1, 254)
	return domain.AuthError(code) //nolint:gosec // G115: code is clamped to ≤254, fits in uint8.
}

// verifyPassword compares the stored credential against the supplied one
// using a constant-time comparison. Encoding governs whether the supplied
// password is hashed before comparison.
//
// login.cpp:446 — strcmp against user_pass (plain).
// loginclif.cpp:279-281 — MD5(plaintext) hex before compare (MD5 mode).
func verifyPassword(stored, given string, encoding domain.PasswordEncoding) bool {
	var givenCred string
	switch encoding {
	case domain.PassEncodingMD5:
		hash := md5.Sum([]byte(given)) //nolint:gosec // G401: required by the rAthena passwdenc=1 protocol.
		givenCred = hex.EncodeToString(hash[:])
	case domain.PassEncodingPlain:
		givenCred = given
	default:
		return false
	}
	// Normalize both sides to a fixed 32-byte digest before constant-time
	// comparison. subtle.ConstantTimeCompare short-circuits on length
	// mismatch, which would leak the stored credential length via timing;
	// SHA-256 ensures both operands are always 32 bytes.
	storedDigest := sha256.Sum256([]byte(stored))
	givenDigest := sha256.Sum256([]byte(givenCred))
	return subtle.ConstantTimeCompare(storedDigest[:], givenDigest[:]) == 1
}

// randomUint32 draws a uniform random uint32 from crypto/rand, retrying
// when the raw bytes decode to zero. rAthena uses rnd_value(1, UINT32_MAX)
// (login.cpp:413-414), i.e. zero is excluded from the session-token range.
func randomUint32() (uint32, error) {
	for range 8 {
		var b [4]byte
		if _, err := rand.Read(b[:]); err != nil {
			return 0, fmt.Errorf("read random bytes: %w", err)
		}
		v := binary.BigEndian.Uint32(b[:])
		if v != 0 {
			return v, nil
		}
	}
	return 0, errors.New("randomUint32: failed to draw non-zero value after 8 attempts")
}
