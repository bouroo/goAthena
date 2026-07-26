// Package app implements the world bounded context's use cases. M3 ships only
// the CZ_ENTER trust gate — the admission check that promotes a fresh map-server
// connection into an authed session. M4 layers entity spawn, AOI, and movement
// over the connections this gate admits.
package app

import (
	"context"
	"errors"
	"fmt"

	accountdomain "github.com/bouroo/goAthena/internal/modules/account/domain"
	gwdomain "github.com/bouroo/goAthena/internal/modules/gateway/domain"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// mapRefuseRejected is the ZC_REFUSE_ENTER error code rAthena sends for a
// rejected CZ_ENTER (REFUSE_ENTER_REJECTED = 0). The client drops back to the
// server-select screen; the connection stays open for retry.
const mapRefuseRejected uint8 = 0

// DefaultSpawn is the Prontera novice-start cell written into ZC_ACCEPT_ENTER at
// M3. It equals the last_x/last_y every combat-slice char is created with, so it
// is byte-correct without loading the char. M4 replaces it with the char's
// loaded last position once the world owns spawn.
var DefaultSpawn = SpawnPoint{PosX: 53, PosY: 111, Dir: 0}

// SpawnPoint is a map cell plus facing direction — the spawn view ZC_ACCEPT_ENTER
// carries so the client places the character before the world tick takes over.
type SpawnPoint struct {
	PosX int16
	PosY int16
	Dir  uint8
}

// MapEnterHandler serves CZ_ENTER (0x0072) on the map-role dispatch table. The
// map connection is a fresh reconnect (HC_NOTIFY_ZONESVR redirected the client
// here after CH_SELECT_CHAR), so unlike CH_ENTER it arrives with no
// in-connection auth cache: the only proof the CA_LOGIN that minted loginID1 was
// accepted is the stored session. VerifySession is the trust anchor.
//
// The gate follows rAthena's clif_accept_enter / clif_refuse_enter triad:
//   - gateRefuse — session missing or loginID1 mismatch: send ZC_REFUSE_ENTER
//     and keep the connection so the client can retry.
//   - gateDrop — the run context was cancelled mid-verify (server shutdown):
//     return nil without a refuse frame; the socket is closing anyway.
//   - gateAllow — session verified: cache the auth on the connection (sourced
//     from the session, never the client-controlled AccountID field) and send
//     ZC_ACCEPT_ENTER.
//
// Error policy mirrors CharEnterHandler: a parse/encode or infra fault is
// returned so ProcessBytes logs it; an expected refusal is a nil error.
type MapEnterHandler struct {
	auth    accountdomain.Authenticator
	spawn   SpawnPoint
	spawner *SpawnService
}

// NewMapEnterHandler builds a CZ_ENTER handler over the account Authenticator
// (for VerifySession), the spawn point written into ZC_ACCEPT_ENTER, and the
// spawn use case that drives the enter-world flow after the gate accepts. M3
// callers with no spawn-on-enter yet pass a nil spawner; the accept path then
// stops after ZC_ACCEPT_ENTER exactly as M3 did.
func NewMapEnterHandler(auth accountdomain.Authenticator, spawn SpawnPoint, spawner *SpawnService) *MapEnterHandler {
	return &MapEnterHandler{auth: auth, spawn: spawn, spawner: spawner}
}

// Handle implements gateway/domain.PacketHandler for CZ_ENTER.
func (h *MapEnterHandler) Handle(ctx context.Context, conn gwdomain.Conn, frame gwdomain.Frame) error {
	req, err := packet.ParseCZEnter(frame.Raw)
	if err != nil {
		return fmt.Errorf("parse CZ_ENTER: %w", err)
	}

	sess, err := h.auth.VerifySession(ctx, req.AccountID, req.AuthCode)
	switch {
	case err == nil:
		// gateAllow — fall through to the accept path.
	case errors.Is(err, accountdomain.ErrSessionNotFound):
		// gateRefuse: send ZC_REFUSE_ENTER and keep the connection open.
		if encErr := (packet.MapRefuseEnterResponse{Error: mapRefuseRejected}).Encode(connWriter{conn}); encErr != nil {
			return fmt.Errorf("encode ZC_REFUSE_ENTER: %w", encErr)
		}
		return nil
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		// gateDrop: shutdown in flight — drop silently, no refuse frame.
		return nil
	default:
		return fmt.Errorf("verify map session for account %d: %w", req.AccountID, err)
	}

	// gateAllow: cache the verified auth. AccountID comes from the session, not
	// req.AccountID — the packet field is client-controlled and must not be
	// trusted for identity (impersonation guard).
	conn.SetAuth(gwdomain.ConnAuth{
		AccountID: sess.AccountID,
		LoginID1:  sess.LoginID1,
		LoginID2:  sess.LoginID2,
		Sex:       sess.Sex.WireByte(),
	})

	resp := packet.MapAcceptEnterResponse{
		StartTime: 0, // M3 placeholder; M4 wires the real map-server tick.
		PosX:      h.spawn.PosX,
		PosY:      h.spawn.PosY,
		Dir:       h.spawn.Dir,
		XSize:     5,
		YSize:     5,
	}
	if err := resp.Encode(connWriter{conn}); err != nil {
		return fmt.Errorf("encode ZC_ACCEPT_ENTER: %w", err)
	}

	// M4b: spawn the player into the world. Runs only after the gate has
	// accepted and cached auth on the conn; the spawner reads the verified
	// accountID off the auth cache (sess.AccountID), never req.AccountID. A nil
	// spawner keeps the M3 behavior (accept then stop) for tests that exercise
	// only the gate.
	if h.spawner == nil {
		return nil
	}
	if err := h.spawner.EnterWorld(ctx, conn, sess.AccountID, req.CharID, h.spawn); err != nil {
		return fmt.Errorf("enter world account %d char %d: %w", sess.AccountID, req.CharID, err)
	}
	return nil
}

// connWriter adapts a gateway domain.Conn (Write returns only error) to the
// io.Writer the kernel packet encoders target. Duplicated from account/app and
// character/app: each module keeps its own copy so the gateway domain is the
// only cross-module dependency a feature module needs.
type connWriter struct{ conn gwdomain.Conn }

func (w connWriter) Write(p []byte) (int, error) {
	if err := w.conn.Write(p); err != nil {
		return 0, fmt.Errorf("write to conn: %w", err)
	}
	return len(p), nil
}
