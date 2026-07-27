package app

import (
	"context"
	"fmt"
	"io"
	"net"

	"github.com/bouroo/goAthena/internal/modules/account/domain"
	gwdomain "github.com/bouroo/goAthena/internal/modules/gateway/domain"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// CALoginHandler is the CA_LOGIN (0x0064) gateway packet handler. It parses the
// inbound frame, delegates to the Authenticator use case, and encodes the
// response (AC_ACCEPT_LOGIN 0x0ac4 or AC_REFUSE_LOGIN 0x083e) onto the
// connection. CharServers is the list advertised on accept (loginclif.cpp appends
// the configured char servers); M1-6 fills it from config, M1-4 tests pass one.
//
// Error policy: a parse or encode error, or an infra fault returned by the use
// case, is returned so ProcessBytes logs it. An expected refusal is a nil error
// (the connection stays open for retry — rAthena does not disconnect on a
// refused login).
type CALoginHandler struct {
	auth        domain.Authenticator
	charServers []packet.CharServer
}

// NewCALoginHandler builds a handler over the given use case. charServers is the
// trailing server list embedded in every AC_ACCEPT_LOGIN.
func NewCALoginHandler(auth domain.Authenticator, charServers []packet.CharServer) *CALoginHandler {
	return &CALoginHandler{auth: auth, charServers: charServers}
}

// Handle implements gateway/domain.PacketHandler. It is a method value so it
// satisfies the function type directly at registration.
func (h *CALoginHandler) Handle(ctx context.Context, conn gwdomain.Conn, frame gwdomain.Frame) error {
	req, err := packet.ParseCALogin(frame.Raw)
	if err != nil {
		return fmt.Errorf("parse CA_LOGIN: %w", err)
	}

	res, err := h.auth.Login(ctx, domain.LoginRequest{
		UserID:   req.Username,
		Password: req.Password,
		IP:       peerHost(conn.RemoteAddr()),
	})
	if err != nil {
		return fmt.Errorf("login %q: %w", req.Username, err)
	}

	w := connWriter{conn: conn}
	if res.Accepted {
		// A successful login advances the connection to the char-server role so
		// the next inbound packet dispatches against the char table (e.g. the
		// server-list request) instead of login. rAthena performs this state
		// transition on AC_ACCEPT_LOGIN.
		conn.SetRole(gwdomain.RoleChar)
		// Cache the credentials the char/map enter flows will verify against. The
		// connection itself is proof the login that minted them was accepted, so
		// this cache is the trust anchor for CH_ENTER and CZ_ENTER — they compare
		// the packet's echoed credentials to it rather than re-querying the
		// session store.
		conn.SetAuth(gwdomain.ConnAuth{
			AccountID: res.Account.AccountID,
			LoginID1:  res.LoginID1,
			LoginID2:  res.LoginID2,
			Sex:       res.Account.Sex.WireByte(),
		})
		accept := packet.AcceptLoginResponse{
			LoginID1: res.LoginID1,
			AID:      res.Account.AccountID,
			LoginID2: res.LoginID2,
			// loginclif.cpp:116-128 hardcodes last_ip=0 and last_login="" on
			// the accept frame regardless of stored history.
			LastIP:      0,
			LastLogin:   "",
			Sex:         res.Account.Sex.WireByte(),
			Token:       res.Account.WebAuthToken,
			CharServers: h.charServers,
		}
		if err := accept.Encode(w); err != nil {
			return fmt.Errorf("encode AC_ACCEPT_LOGIN: %w", err)
		}
		return nil
	}
	refuse := packet.RefuseLoginResponse{Error: uint32(res.Code)}
	if err := refuse.Encode(w); err != nil {
		return fmt.Errorf("encode AC_REFUSE_LOGIN: %w", err)
	}
	return nil
}

// peerHost extracts the bare IP from a "host:port" remote address, falling back
// to the raw string when it is not host:port form (e.g. a unix path). This is
// what lands in login.last_ip, mirroring rAthena's sd->last_ip.
func peerHost(remote string) string {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		return remote
	}
	return host
}

// connWriter adapts a gateway domain.Conn (Write returns only error) to the
// io.Writer the kernel packet encoders target. The same bridge exists in the
// transport adapters; it is duplicated rather than shared to keep the gateway
// domain the only cross-module dependency the account app takes.
type connWriter struct{ conn gwdomain.Conn }

func (w connWriter) Write(p []byte) (int, error) {
	if err := w.conn.Write(p); err != nil {
		return 0, fmt.Errorf("write to conn: %w", err)
	}
	return len(p), nil
}

// compile-time: connWriter is an io.Writer.
var _ io.Writer = connWriter{}
