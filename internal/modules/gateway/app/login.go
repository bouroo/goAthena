// Package app is the gateway ingress: it owns the game-protocol TCP listeners
// and the codec→dispatch path. The login listener (M1b) accepts CA_LOGIN on
// :6900, authenticates via the account port, and replies AC_ACCEPT_LOGIN or
// AC_REFUSE_LOGIN. No stream crypto at PACKETVER 20250604 (identity codec).
package app

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/panjf2000/gnet/v2"

	"github.com/bouroo/goAthena/internal/modules/account/domain"
	ropacket "github.com/bouroo/goAthena/pkg/ro/packet"
)

// loginFrameSize is the wire length of a CA_LOGIN packet (0x0064): 2 cmd +
// 4 version + 24 userid + 24 pass + 1 clienttype. Matches pkg/ro/packet
// sizeCALogin (kept unexported there).
const loginFrameSize = 55

// rAthena AC_REFUSE_LOGIN result codes (loginclif.cpp comment block).
const (
	refuseUnregistered = 0 // Unregistered ID (account not found)
	refuseBadPassword  = 1 // Incorrect Password
	refuseProhibited   = 6 // prohibited to log in until %s (banned)
)

// LoginServer is the gnet TCP listener for the login protocol.
type LoginServer struct {
	gnet.BuiltinEventEngine
	engine   gnet.Engine
	booted   bool
	auth     domain.Authenticator
	log      *slog.Logger
	charIP   uint32 // advertised char-server IPv4 (wire uint32)
	charPort uint16 // advertised char-server port
	charName string // advertised char-server display name
}

// NewLoginServer builds a login listener. charHost/charPort/charName are the
// char-server endpoint advertised to the client inside AC_ACCEPT_LOGIN.
func NewLoginServer(auth domain.Authenticator, log *slog.Logger, charHost, charName string, charPort uint16) (*LoginServer, error) {
	ip, err := ipToWire(charHost)
	if err != nil {
		return nil, fmt.Errorf("char server host %q: %w", charHost, err)
	}
	return &LoginServer{auth: auth, log: log, charIP: ip, charPort: charPort, charName: charName}, nil
}

// OnBoot captures the running engine so Stop can shut the listener down.
func (s *LoginServer) OnBoot(e gnet.Engine) gnet.Action {
	s.engine = e
	s.booted = true
	s.log.Info("login listener booted")
	return gnet.None
}

// OnTraffic drains complete CA_LOGIN frames from each connection. Each frame is
// copied (gnet's Next buffer is invalid off the event loop) and dispatched to a
// goroutine so the blocking DB auth never stalls the reactor. The response is
// written back via the concurrency-safe AsyncWrite.
func (s *LoginServer) OnTraffic(c gnet.Conn) gnet.Action {
	for c.InboundBuffered() >= loginFrameSize {
		frame, err := c.Next(loginFrameSize)
		if err != nil {
			break // short read: wait for more bytes next OnTraffic
		}
		cp := append([]byte(nil), frame...) // detach from gnet's ring buffer
		ip := remoteIP(c.RemoteAddr())
		go s.handleLogin(c, cp, ip)
	}
	return gnet.None
}

// handleLogin parses a CA_LOGIN frame, authenticates, and writes the reply.
func (s *LoginServer) handleLogin(c gnet.Conn, frame []byte, ip string) {
	req, err := ropacket.ParseCALogin(frame)
	if err != nil {
		s.log.Warn("login: unparseable frame", "err", err, "ip", ip)
		return // malformed: drop silently rather than crash the loop
	}
	acc, id1, id2, err := s.auth.Authenticate(context.Background(), req.Username, req.Password, ip)
	if err != nil {
		s.writeRefuse(c, refuseCode(err))
		s.log.Info("login refused", "user", req.Username, "ip", ip, "err", err)
		return
	}
	resp := ropacket.AcceptLoginResponse{
		LoginID1: id1,
		AID:      uint32(acc.ID),
		LoginID2: id2,
		Sex:      sexByte(acc.Sex),
		CharServers: []ropacket.CharServer{{
			IP:   s.charIP,
			Port: s.charPort,
			Name: s.charName,
		}},
	}
	out := make([]byte, resp.Size())
	if err := resp.Encode(sliceWriter(out)); err != nil {
		s.log.Error("login: encode accept", "err", err)
		return
	}
	_ = c.AsyncWrite(out, nil)
	s.log.Info("login accepted", "user", req.Username, "aid", acc.ID, "ip", ip)
}

// writeRefuse encodes and sends an AC_REFUSE_LOGIN with the mapped code.
func (s *LoginServer) writeRefuse(c gnet.Conn, code uint32) {
	resp := ropacket.RefuseLoginResponse{Error: code}
	out := make([]byte, resp.Size())
	if err := resp.Encode(sliceWriter(out)); err != nil {
		s.log.Error("login: encode refuse", "err", err)
		return
	}
	_ = c.AsyncWrite(out, nil)
}

// refuseCode maps a domain auth sentinel to its rAthena AC_REFUSE_LOGIN code.
func refuseCode(err error) uint32 {
	switch {
	case errors.Is(err, domain.ErrAccountNotFound):
		return refuseUnregistered
	case errors.Is(err, domain.ErrInvalidPassword):
		return refuseBadPassword
	case errors.Is(err, domain.ErrAccountBanned):
		return refuseProhibited
	default:
		return refuseProhibited // unknown failure: safest deny
	}
}

// sexByte maps the domain Sex enum to the kRO wire byte (F=0, M=1, S=2).
func sexByte(s domain.Sex) uint8 {
	switch s {
	case domain.SexFemale:
		return 0
	case domain.SexServer:
		return 2
	default:
		return 1 // SexMale
	}
}

// ipToWire converts a dotted-quad host to the wire uint32 the client expects.
func ipToWire(host string) (uint32, error) {
	ip := net.ParseIP(host)
	if ip == nil {
		addrs, err := net.LookupHost(host)
		if err != nil || len(addrs) == 0 {
			return 0, fmt.Errorf("resolve: %w", err)
		}
		ip = net.ParseIP(addrs[0])
	}
	ip4 := ip.To4()
	if ip4 == nil {
		return 0, fmt.Errorf("not an ipv4: %s", host)
	}
	return binary.LittleEndian.Uint32(ip4), nil
}

// remoteIP extracts the remote IPv4 string from a gnet conn address.
func remoteIP(a net.Addr) string {
	if a == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(a.String())
	if err != nil {
		return a.String()
	}
	return host
}

// Start runs gnet.Run on addr (e.g. "tcp://0.0.0.0:6900") in a goroutine.
// Returns immediately; boot errors are logged (the control-plane /healthz
// stays the liveness signal).
func (s *LoginServer) Start(addr string) {
	go func() {
		if err := gnet.Run(s, addr, gnet.WithTicker(true)); err != nil {
			s.log.Error("login listener stopped", "addr", addr, "err", err)
		}
	}()
}

// Stop shuts the listener down, waiting up to 5s for a clean drain.
func (s *LoginServer) Stop() {
	if !s.booted {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.engine.Stop(ctx)
	s.booted = false
}

// sliceWriter adapts a pre-sized []byte to io.Writer for packet Encode.
type sliceWriter []byte

func (w sliceWriter) Write(p []byte) (int, error) { return copy(w, p), nil }
