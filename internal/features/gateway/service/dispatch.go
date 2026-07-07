// Package service contains use-case implementations for the gateway
// feature (WS-A). DispatchHandler is the M1b production handler: it
// forwards CA_LOGIN to the identity service over gRPC and encodes the
// reply (AC_ACCEPT_LOGIN or AC_REFUSE_LOGIN) back to the client via the
// supplied Responder.

package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"

	"github.com/rs/zerolog"
	"google.golang.org/grpc/status"

	identityv1 "github.com/bouroo/goAthena/api/pb/identity/v1"
	"github.com/bouroo/goAthena/internal/features/gateway/domain"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// ErrIdentityUnavailableRefuse is the AC_REFUSE_LOGIN error code the
// gateway sends when the identity gRPC call returns a transport-level
// failure (identity down, deadline exceeded, network partition). 99
// matches rAthena's mapLoginError sentinel for "server closed" so the
// client UI surfaces a recognizable "try again later" state.
const ErrIdentityUnavailableRefuse = uint32(99)

// DispatchHandler is a domain.PacketHandler that bridges the kRO TCP /
// WebSocket ingress to the identity gRPC service. It is the M1b
// replacement for LoggingHandler. It only handles CA_LOGIN (0x0064);
// other login variants (CA_LOGIN2/3/4, CA_LOGIN_PCBANG, CA_SSO_LOGIN_REQ)
// log at debug and continue.
type DispatchHandler struct {
	identity  identityv1.IdentityServiceClient
	packetver uint32
	logger    zerolog.Logger
}

// NewDispatchHandler constructs a dispatch-backed PacketHandler.
func NewDispatchHandler(identity identityv1.IdentityServiceClient, packetver int, logger zerolog.Logger) *DispatchHandler {
	return &DispatchHandler{
		identity:  identity,
		packetver: uint32(packetver), //nolint:gosec // validated upstream by config.min/max=20260000
		logger:    logger.With().Str("component", "gateway.dispatch").Logger(),
	}
}

// HandlePacket dispatches a single decoded login-server packet. Only
// CA_LOGIN is wired for M1b; all other commands are logged at debug and
// the connection is left alive. Parse errors on CA_LOGIN are likewise
// logged and swallowed — rAthena tolerates a truncated or corrupt login
// packet by dropping it without closing the connection, since the
// client will retry after re-reading the addressbook.
func (h *DispatchHandler) HandlePacket(ctx context.Context, conn domain.ConnectionInfo, resp domain.Responder, cmd uint16, frame []byte) error {
	if cmd != packet.HeaderCALOGIN {
		h.logger.Debug().
			Uint64("conn", conn.ID).
			Uint16("cmd", cmd).
			Msg("unhandled login packet")
		return nil
	}

	req, err := packet.ParseCALogin(frame)
	if err != nil {
		h.logger.Warn().
			Err(err).
			Uint64("conn", conn.ID).
			Int("frame_len", len(frame)).
			Msg("malformed CA_LOGIN; dropping packet")
		return nil
	}

	authReq := &identityv1.AuthenticateRequest{
		Username:   req.Username,
		Password:   []byte(req.Password),
		ClientType: uint32(req.ClientType),
		Packetver:  h.packetver,
		ClientIp:   splitHost(conn.RemoteIP),
		Method:     identityv1.AuthMethod_AUTH_METHOD_PASSWORD,
	}

	gResp, err := h.identity.Authenticate(ctx, authReq)
	if err != nil {
		// Transport-level failure (identity down, deadline, network).
		// Log with the gRPC status code so operators can correlate with
		// identity service logs, then refuse the login with sentinel 99.
		st, _ := status.FromError(err)
		h.logger.Error().
			Err(err).
			Uint64("conn", conn.ID).
			Str("user", req.Username).
			Str("grpc_code", st.Code().String()).
			Msg("identity Authenticate RPC failed; refusing login")
		return sendRefuse(resp, ErrIdentityUnavailableRefuse)
	}

	if gResp.GetResult() != identityv1.AuthResult_AUTH_RESULT_OK {
		h.logger.Info().
			Uint64("conn", conn.ID).
			Str("user", req.Username).
			Uint32("error_code", gResp.GetErrorCode()).
			Str("result", gResp.GetResult().String()).
			Msg("identity refused login")
		return sendRefuse(resp, gResp.GetErrorCode())
	}

	accept := packet.AcceptLoginResponse{
		LoginID1:    uint32(gResp.GetLoginId1() & 0xffffffff), //nolint:gosec // low 32 bits of session token
		AID:         gResp.GetAccountId(),
		LoginID2:    uint32(gResp.GetLoginId2() & 0xffffffff), //nolint:gosec // low 32 bits of session token
		LastIP:      parseIPv4(gResp.GetLastIp()),
		LastLogin:   gResp.GetLastLogin(),
		Sex:         sexToByte(gResp.GetSex()),
		Token:       gResp.GetToken(),
		CharServers: toCharServers(gResp.GetCharServers()),
	}

	var buf bytes.Buffer
	if err := accept.Encode(&buf); err != nil {
		// Encode errors only fire on string-overflow constraints (lastLogin,
		// token, char-server name). Identity must have surfaced a corrupt
		// row; surface as a server-closed refuse rather than crash the
		// connection.
		h.logger.Error().
			Err(err).
			Uint64("conn", conn.ID).
			Str("user", req.Username).
			Msg("encode AC_ACCEPT_LOGIN failed; refusing login")
		return sendRefuse(resp, ErrIdentityUnavailableRefuse)
	}

	h.logger.Info().
		Uint64("conn", conn.ID).
		Str("user", req.Username).
		Uint32("aid", accept.AID).
		Uint32("login_id1", accept.LoginID1).
		Uint32("login_id2", accept.LoginID2).
		Int("char_servers", len(accept.CharServers)).
		Msg("login accepted")

	if err := resp.SendPacket(buf.Bytes()); err != nil {
		return fmt.Errorf("send AC_ACCEPT_LOGIN: %w", err)
	}
	return nil
}

// sendRefuse encodes an AC_REFUSE_LOGIN frame and sends it. Encode
// errors are not possible with the empty-string UnblockTime we send on
// every internal refusal path, so we treat any returned error as a fatal
// transport failure (the caller's Responder already returned a write
// error) and propagate it.
func sendRefuse(resp domain.Responder, code uint32) error {
	refuse := packet.RefuseLoginResponse{Error: code}
	var buf bytes.Buffer
	if err := refuse.Encode(&buf); err != nil {
		return fmt.Errorf("encode AC_REFUSE_LOGIN: %w", err)
	}
	if err := resp.SendPacket(buf.Bytes()); err != nil {
		return fmt.Errorf("send AC_REFUSE_LOGIN: %w", err)
	}
	return nil
}

// splitHost strips "host:port" to "host". It tolerates "host" alone
// (rare; some load balancers surface a bare IP via RemoteAddr). IPv6
// literals like "[::1]:1234" are reduced to "::1" via net.SplitHostPort.
func splitHost(remote string) string {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		return remote
	}
	return host
}

// parseIPv4 converts a dotted-quad IPv4 string to network byte order.
// Returns 0 for empty inputs and parse failures — both treated as "no
// recorded prior login", matching rAthena's behavior of writing the
// zero IPv4 into the AC_ACCEPT_LOGIN slot.
func parseIPv4(s string) uint32 {
	if s == "" {
		return 0
	}
	addr, err := netip.ParseAddr(s)
	if err != nil || !addr.Is4() {
		return 0
	}
	b := addr.As4()
	return binary.BigEndian.Uint32(b[:])
}

// sexToByte maps the identity service's sex string to the kRO wire byte:
// "F"→0, "M"→1, "S"→2. Anything unrecognized (including empty) defaults
// to 0 (female), which matches rAthena's `account->sex == 0` branch.
func sexToByte(s string) uint8 {
	switch s {
	case "F":
		return 0
	case "M":
		return 1
	case "S":
		return 2
	default:
		return 0
	}
}

// toCharServers projects the identity gRPC char-server list onto the
// packet.CharServer shape. Any per-entry that fails to parse an IPv4
// gets a zero IP — the client just won't be able to reach that server.
// Out-of-range port / users / server_type values are clamped to 0 so
// wire-shaped uint16 fields never overflow.
func toCharServers(list []*identityv1.CharServerInfo) []packet.CharServer {
	if len(list) == 0 {
		return nil
	}
	out := make([]packet.CharServer, 0, len(list))
	for _, cs := range list {
		if cs == nil {
			continue
		}
		out = append(out, packet.CharServer{
			IP:    parseIPv4(cs.GetIp()),
			Port:  clampUint16(cs.GetPort()),
			Name:  cs.GetName(),
			Users: clampUint16(cs.GetUsers()),
			Type:  clampUint16(cs.GetServerType()),
			New:   0,
		})
	}
	return out
}

// clampUint16 saturates a uint32 into a uint16. Identity contract is
// uint32; values above 65535 are sentinel-only and don't need a real
// truncation path — clamp to 0 so the malformed row visibly fails
// rather than silently wrapping.
func clampUint16(v uint32) uint16 {
	if v > 0xffff {
		return 0
	}
	return uint16(v) //nolint:gosec // guarded by the > 0xffff check above
}
