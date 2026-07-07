// Package service contains use-case implementations for the gateway
// feature (WS-A). DispatchHandler is the production handler that
// forwards the kRO packet stream to the identity service over gRPC
// and encodes the reply back to the client via the supplied Responder.
//
// Wire surface (per M2b):
//   - CA_LOGIN (0x0064)            → identity.Authenticate → AC_ACCEPT_LOGIN / AC_REFUSE_LOGIN
//   - CH_ENTER (0x0065)            → identity.GetCharacterList → 4-byte AID echo + HC_ACCEPT_ENTER
//   - CH_SELECT_CHAR (0x0066)      → HC_NOTIFY_ZONESVR (zone redirect to DefaultMap)
//   - everything else              → debug-logged, connection kept alive.

package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"

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

// maxCharListCount caps the number of CHARACTER_INFO entries the
// gateway advertises in HC_ACCEPT_ENTER. The on-wire uint8 Total slot
// holds 0..255; values above that would silently truncate on the
// client and break the char-select screen.
const maxCharListCount = 255

// DispatchHandler is a domain.PacketHandler that bridges the kRO TCP /
// WebSocket ingress to the identity gRPC service. It is the M2b
// handler: it covers the full CA_LOGIN → CH_ENTER → CH_SELECT_CHAR
// path the rAthena char_clif handshake expects.
type DispatchHandler struct {
	identity  identityv1.IdentityServiceClient
	packetver uint32
	logger    zerolog.Logger

	// defaultMap is the initial map name advertised in HC_NOTIFY_ZONESVR
	// after CH_SELECT_CHAR. Sourced from zone.default_map.
	defaultMap string
	// zoneHost is the IPv4 host written into the HC_NOTIFY_ZONESVR IP
	// slot (network-byte-order uint32). Sourced from gateway.map_addr.
	zoneHost string
	// zonePort is the TCP port written into HC_NOTIFY_ZONESVR. Sourced
	// from gateway.map_addr.
	zonePort uint16
}

// NewDispatchHandler constructs a dispatch-backed PacketHandler.
//
// defaultMap, zoneHost, and zonePort feed the HC_NOTIFY_ZONESVR frame
// emitted after CH_SELECT_CHAR. They are sourced from config
// (zone.default_map / gateway.map_addr) and split by the DI provider.
func NewDispatchHandler(
	identity identityv1.IdentityServiceClient,
	packetver int,
	logger zerolog.Logger,
	defaultMap string,
	zoneHost string,
	zonePort uint16,
) *DispatchHandler {
	return &DispatchHandler{
		identity:   identity,
		packetver:  uint32(packetver), //nolint:gosec // validated upstream by config.min/max=20260000
		logger:     logger.With().Str("component", "gateway.dispatch").Logger(),
		defaultMap: defaultMap,
		zoneHost:   zoneHost,
		zonePort:   zonePort,
	}
}

// HandlePacket dispatches a single decoded kRO packet. Parse errors on
// any known command are logged and swallowed — rAthena tolerates a
// truncated or corrupt packet by dropping it without closing the
// connection, since the client will retry after re-reading the
// addressbook.
func (h *DispatchHandler) HandlePacket(ctx context.Context, conn domain.ConnectionInfo, resp domain.Responder, cmd uint16, frame []byte) error {
	switch cmd {
	case packet.HeaderCALOGIN:
		return h.handleCALogin(ctx, conn, resp, frame)
	case packet.HeaderCHENTER:
		return h.handleCHEnter(ctx, conn, resp, frame)
	case packet.HeaderCHSELECTCHAR:
		return h.handleCHSelectChar(ctx, conn, resp, frame)
	default:
		h.logger.Debug().
			Uint64("conn", conn.ID).
			Uint16("cmd", cmd).
			Msg("unhandled login/char packet")
		return nil
	}
}

// handleCALogin forwards CA_LOGIN to identity.Authenticate and encodes
// the reply. Extracted from the M1b switch to keep HandlePacket's
// dispatch table readable.
func (h *DispatchHandler) handleCALogin(ctx context.Context, conn domain.ConnectionInfo, resp domain.Responder, frame []byte) error {
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
		// Distinguish a client that disconnected (ctx cancelled) from a
		// real backend failure. The former is expected under load and
		// must not generate a refuse — the conn is gone and SendPacket
		// would either fail or panic on a closed writer.
		if clientGone := errors.Is(err, context.Canceled) || ctx.Err() != nil; clientGone {
			h.logger.Debug().
				Err(err).
				Uint64("conn", conn.ID).
				Str("user", req.Username).
				Msg("identity call cancelled (client gone)")
			_ = err
			return nil
		}
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

	if gResp == nil {
		// Buggy server returned (nil, nil). Treat as transport failure:
		// refuse with the server-closed sentinel so the client gets a
		// recognizable error rather than a hang.
		h.logger.Warn().
			Uint64("conn", conn.ID).
			Str("user", req.Username).
			Msg("identity returned nil response; refusing login")
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

// handleCHEnter forwards CH_ENTER to identity.GetCharacterList and
// emits the rAthena char_clif handshake reply:
//
//  1. Four raw little-endian bytes of the account ID (headerless echo
//     the client expects after CH_ENTER; rathena/src/char/char_clif.cpp:850-853).
//  2. HC_ACCEPT_ENTER (cmd 0x006b) carrying one CHARACTER_INFO entry
//     per character on the account.
//
// On identity failure the gateway falls back to HC_REFUSE_ENTER (cmd
// 0x006c) with the gRPC status code; on nil/transport errors the
// server-closed sentinel (99) is used.
func (h *DispatchHandler) handleCHEnter(ctx context.Context, conn domain.ConnectionInfo, resp domain.Responder, frame []byte) error {
	req, err := packet.ParseCHEnter(frame)
	if err != nil {
		h.logger.Warn().
			Err(err).
			Uint64("conn", conn.ID).
			Int("frame_len", len(frame)).
			Msg("malformed CH_ENTER; dropping packet")
		return nil
	}

	listReq := &identityv1.GetCharacterListRequest{
		AccountId: req.AccountID,
		LoginId1:  uint64(req.LoginID1),
		Sex:       sexString(req.Sex),
	}

	listResp, err := h.identity.GetCharacterList(ctx, listReq)
	if err != nil {
		if clientGone := errors.Is(err, context.Canceled) || ctx.Err() != nil; clientGone {
			h.logger.Debug().
				Err(err).
				Uint64("conn", conn.ID).
				Uint32("aid", req.AccountID).
				Msg("identity call cancelled (client gone)")
			_ = err
			return nil
		}
		st, _ := status.FromError(err)
		h.logger.Error().
			Err(err).
			Uint64("conn", conn.ID).
			Uint32("aid", req.AccountID).
			Str("grpc_code", st.Code().String()).
			Msg("identity GetCharacterList RPC failed; refusing char enter")
		return sendCharRefuse(resp, ErrIdentityUnavailableRefuse)
	}
	if listResp == nil {
		h.logger.Warn().
			Uint64("conn", conn.ID).
			Uint32("aid", req.AccountID).
			Msg("identity returned nil GetCharacterList response; refusing char enter")
		return sendCharRefuse(resp, ErrIdentityUnavailableRefuse)
	}

	chars := listResp.GetCharacters()

	// (1) Headerless AID echo. rAthena writes these 4 raw bytes
	// immediately before HC_ACCEPT_ENTER so the client can confirm it
	// received the right account's roster (rathena/src/char/
	// char_clif.cpp:850-853). Sending these bytes is what tells the
	// client "the roster you're about to receive is for AID=X", not
	// for a previously-cached account.
	echo := make([]byte, 4)
	binary.LittleEndian.PutUint32(echo, req.AccountID)
	if err := resp.SendPacket(echo); err != nil {
		return fmt.Errorf("send CH_ENTER AID echo: %w", err)
	}

	// (2) HC_ACCEPT_ENTER. Cap to 255 to fit the wire's uint8 Total
	// slot — values above would silently truncate on the client.
	limit := min(len(chars), maxCharListCount)
	entries := make([]packet.CharacterInfo, 0, limit)
	for _, ch := range chars[:limit] {
		if ch == nil {
			continue
		}
		entries = append(entries, packet.CharacterInfo{
			GID:      ch.GetCharId(),
			Job:      int16(clampUint16(ch.GetClassId())),   //nolint:gosec // wire slot is 16-bit
			Level:    int16(clampUint16(ch.GetBaseLevel())), //nolint:gosec // wire slot is 16-bit
			JobLevel: int32(clampUint16(ch.GetJobLevel())),  //nolint:gosec // wire slot is 32-bit
			Name:     ch.GetName(),
			CharNum:  uint8(ch.GetSlot()), //nolint:gosec // wire slot is 8-bit; values >255 silently saturate per rAthena
			Sex:      req.Sex,
		})
	}

	total := uint8(listResp.GetTotalSlots()) //nolint:gosec // wire slot is 8-bit
	if total == 0 {
		total = uint8(len(entries)) //nolint:gosec // capped to maxCharListCount above
	}

	accept := packet.AcceptEnterResponse{
		Total:      total,
		Characters: entries,
	}

	var buf bytes.Buffer
	if err := accept.Encode(&buf); err != nil {
		// Encode errors only fire on string-overflow constraints (name,
		// mapName). Identity must have surfaced a corrupt row; surface
		// as a server-closed refuse rather than crash the connection.
		h.logger.Error().
			Err(err).
			Uint64("conn", conn.ID).
			Uint32("aid", req.AccountID).
			Msg("encode HC_ACCEPT_ENTER failed; refusing char enter")
		return sendCharRefuse(resp, ErrIdentityUnavailableRefuse)
	}

	h.logger.Info().
		Uint64("conn", conn.ID).
		Uint32("aid", req.AccountID).
		Int("chars", len(entries)).
		Uint8("slots", total).
		Msg("char list delivered")

	if err := resp.SendPacket(buf.Bytes()); err != nil {
		return fmt.Errorf("send HC_ACCEPT_ENTER: %w", err)
	}
	return nil
}

// handleCHSelectChar emits HC_NOTIFY_ZONESVR pointing the client at
// the configured zone service. M2b does not retain per-connection
// character state, so the advertised CID is 0 (rAthena allows this
// when the zone will resolve the char from the AID + slot on its
// own). M3 will track the selected character explicitly and substitute
// the real CID + the char's last-saved map.
func (h *DispatchHandler) handleCHSelectChar(_ context.Context, conn domain.ConnectionInfo, resp domain.Responder, frame []byte) error {
	req, err := packet.ParseCHSelectChar(frame)
	if err != nil {
		h.logger.Warn().
			Err(err).
			Uint64("conn", conn.ID).
			Int("frame_len", len(frame)).
			Msg("malformed CH_SELECT_CHAR; dropping packet")
		return nil
	}

	notify := packet.NotifyZoneServerResponse{
		CID:     0, // M2b: no per-connection char state — see doc comment.
		MapName: h.defaultMap,
		IP:      parseIPv4(h.zoneHost),
		Port:    h.zonePort,
		Domain:  "",
	}

	var buf bytes.Buffer
	if err := notify.Encode(&buf); err != nil {
		h.logger.Error().
			Err(err).
			Uint64("conn", conn.ID).
			Uint8("slot", req.Slot).
			Msg("encode HC_NOTIFY_ZONESVR failed; dropping")
		return nil
	}

	h.logger.Info().
		Uint64("conn", conn.ID).
		Uint8("slot", req.Slot).
		Str("map", notify.MapName).
		Uint32("ip", notify.IP).
		Uint16("port", notify.Port).
		Msg("zone redirect")

	if err := resp.SendPacket(buf.Bytes()); err != nil {
		return fmt.Errorf("send HC_NOTIFY_ZONESVR: %w", err)
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

// sendCharRefuse encodes an HC_REFUSE_ENTER frame and sends it. Used
// when CH_ENTER cannot proceed (identity down, no chars available,
// etc.). Maps the uint32 identity error code into the 8-bit slot
// expected by rAthena — values above 255 are saturated to 0xff so a
// bogus error never silently truncates to a no-op success.
func sendCharRefuse(resp domain.Responder, code uint32) error {
	var refuseCode uint8
	if code > 0xff {
		refuseCode = 0xff
	} else {
		refuseCode = uint8(code)
	}
	refuse := packet.RefuseEnterResponse{Error: refuseCode}
	var buf bytes.Buffer
	if err := refuse.Encode(&buf); err != nil {
		return fmt.Errorf("encode HC_REFUSE_ENTER: %w", err)
	}
	if err := resp.SendPacket(buf.Bytes()); err != nil {
		return fmt.Errorf("send HC_REFUSE_ENTER: %w", err)
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
	if err != nil {
		return 0
	}
	addr = addr.Unmap()
	if !addr.Is4() {
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

// sexString is the inverse of sexToByte — used to translate the
// CH_ENTER uint8 sex byte into the string the identity gRPC contract
// expects (proto GetCharacterListRequest.sex is "F"|"M"|"S").
// Anything outside {0,1,2} (including the rAthena-reserved 0x03+
// values) defaults to "S" to avoid silently mis-classifying unknown
// sex bytes as a binary "F".
func sexString(b uint8) string {
	switch b {
	case 0:
		return "F"
	case 1:
		return "M"
	default:
		return "S"
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

// SplitMapAddr splits "host:port" into its parts. Exported so the DI
// provider can pull host/port out of cfg.Gateway.MapAddr without
// leaking the strings.Split / strconv.ParseUint boilerplate into
// di.go.
func SplitMapAddr(addr string) (string, uint16, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, fmt.Errorf("split map addr %q: %w", addr, err)
	}
	portInt, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return "", 0, fmt.Errorf("parse map addr port %q: %w", portStr, err)
	}
	return host, uint16(portInt), nil //nolint:gosec // ParseUint bitSize=16 caps at 0xffff
}
