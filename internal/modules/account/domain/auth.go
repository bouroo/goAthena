package domain

import "context"

// AuthCode is the PACKET_AC_REFUSE_LOGIN result code. The numeric values are
// fixed by the wire protocol and match login_mmo_auth's return value
// (login.cpp:296-433):
//
//	0 unregistered   (login.cpp:347 — no row; also the sex=='S' refusal, :352)
//	1 invalid password (login.cpp:357)
//	2 expired        (login.cpp:360-363 — expiration_time in the past)
//	3 rejected       (login.cpp:315 — DNSBL / IP ban / passwdenc mismatch)
//	5 hash mismatch  (login.cpp:398 — client executable hash)
//	6 banned         (login.cpp:365-370 — unban_time in the future)
//
// Codes 4 (GM-blocked) and 7/8 are not raised by the modern AC_REFUSE_LOGIN
// path. A non-zero login.state produces the wire code state-1 (login.cpp:374),
// computed dynamically rather than named here.
//
// A successful login never produces an AuthCode — it returns an accepted
// LoginResult instead. Only the codes the auth logic explicitly emits are
// named; state-derived codes are computed as AuthCode(acc.State - 1).
type AuthCode uint32

// Only the codes the auth logic explicitly emits are named here. Codes 3 (DNSBL
// rejection) and 5 (client-hash mismatch) belong to features not yet built;
// state-derived codes are computed, not enumerated (see AuthCode above).
const (
	AuthUnregistered    AuthCode = 0
	AuthInvalidPassword AuthCode = 1
	AuthExpired         AuthCode = 2
	AuthBanned          AuthCode = 6
)

// LoginRequest is the CA_LOGIN use-case input assembled by the gateway handler.
type LoginRequest struct {
	UserID   string
	Password string
	IP       string // peer host, recorded on the login row and loginlog
}

// LoginResult is the outcome of a login attempt. Accepted distinguishes a
// successful auth from an expected refusal; repository or infra failures are
// returned as the service's error instead, so the handler can distinguish "send
// AC_REFUSE_LOGIN and keep the connection" (refusal) from "close the
// connection" (infra fault).
type LoginResult struct {
	Accepted bool
	Code     AuthCode // valid when !Accepted
	Account  *Account // valid when Accepted
	LoginID1 uint32   // valid when Accepted — minted session-token half
	LoginID2 uint32   // valid when Accepted — minted session-token half
}

// Authenticator is the inbound port for the CA_LOGIN use case and the map-enter
// trust gate. The gateway CA_LOGIN handler depends on the Login method; the map
// CZ_ENTER handler depends on VerifySession. account/app.AuthService implements
// both.
type Authenticator interface {
	Login(ctx context.Context, req LoginRequest) (LoginResult, error)

	// VerifySession confirms a CZ_ENTER's echoed credentials (accountID +
	// loginID1) against the session minted at CA_LOGIN. It exists because the
	// map-server connection is a fresh reconnect — unlike CH_ENTER, which rides
	// the in-connection auth cache, CZ_ENTER arrives on a connection the login
	// accept never touched, so the session store is the only trust anchor.
	//
	// A missing session or a loginID1 mismatch is an expected auth failure
	// returned as ErrSessionNotFound — deliberately the same sentinel for both
	// so the caller cannot distinguish them and leak whether a session exists
	// (no account-enumeration oracle). The loginID1 compare is constant-time.
	// A context cancellation (server shutdown mid-verify) is propagated as the
	// context error so the caller can drop silently; any other error is an infra
	// fault the caller surfaces for logging.
	VerifySession(ctx context.Context, accountID, loginID1 uint32) (*Session, error)
}
