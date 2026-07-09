// Package service contains use-case implementations for the gateway
// feature (WS-A). DispatchHandler is the production handler that
// forwards the kRO packet stream to the identity and zone services
// over gRPC and encodes the reply back to the client via the supplied
// Responder.
//
// Wire surface (per M3b):
//   - CA_LOGIN (0x0064)            → identity.Authenticate → AC_ACCEPT_LOGIN / AC_REFUSE_LOGIN
//   - CH_ENTER (0x0065)            → identity.GetCharacterList → 4-byte AID echo + HC_ACCEPT_ENTER
//   - CH_SELECT_CHAR (0x0066)      → HC_NOTIFY_ZONESVR (zone redirect to DefaultMap)
//   - CZ_ENTER (0x0072)             → zone.EnterZone → ZC_ACCEPT_ENTER
//   - CZ_REQUEST_MOVE (0x0085)      → debug-logged (M4+ will forward to zone)
//   - CZ_NOTIFY_ACTORINIT (0x007d)  → ZC_MAPPROPERTY_R2 (MAPPROPERTY_NOTHING)
//   - CZ_REQUEST_TIME (0x007e)      → ZC_NOTIFY_TIME (unix millis low 32 bits)
//   - CZ_ACTION_REQUEST (0x0089)    → ZC_ACTION_RESPONSE (sit/stand echo; attacks ignored)
//   - CZ_GLOBAL_MESSAGE (0x008c)    → ZC_NOTIFY_CHAT (single-player echo; no AOI)
//   - CZ_CHANGE_DIRECTION (0x009b)  → ZC_CHANGE_DIRECTION (single-player echo; no AOI)
//   - CZ_REQ_EMOTION (0x00bf)       → ZC_EMOTION (single-player echo; no AOI)
//   - CZ_GETCHARNAMEREQUEST (0x0094) → ZC_ACK_REQNAME (name lookup by GID)
//   - CZ_RESTART (0x00b2)            → logged (state transition deferred)
//   - CZ_CONTACTNPC (0x0090)         → ZC_SAY_DIALOG2 + ZC_WAIT_DIALOG2 (dialog NPC)
//                                  or ZC_SELECT_DEALTYPE (shop NPC)
//   - CZ_REQNEXTSCRIPT (0x00b9)      → ZC_SAY_DIALOG2 + ZC_CLOSE_DIALOG (dialog continuation)
//   - CZ_CLOSE_DIALOG (0x0146)       → logged (client closes dialog locally)
//   - CZ_ACK_SELECT_DEALTYPE (0x00c5) → ZC_PC_PURCHASE_ITEMLIST (Buy) / logged (Sell / Cancel)
//   - CZ_PC_PURCHASE_ITEMLIST (0x00c8) → ZC_PC_PURCHASE_RESULT (always success; zeny / inventory deferred)
//   - everything else              → debug-logged, connection kept alive.

package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"time"

	"github.com/rs/zerolog"
	"google.golang.org/grpc/status"

	identityv1 "github.com/bouroo/goAthena/api/pb/identity/v1"
	zonev1 "github.com/bouroo/goAthena/api/pb/zone/v1"
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
// WebSocket ingress to the identity and zone gRPC services. It is the
// M3b handler: it covers the full CA_LOGIN → CH_ENTER → CH_SELECT_CHAR
// → CZ_ENTER path the rAthena clif handshake expects, plus the
// CZ_REQUEST_MOVE log-only stub that the zone Move RPC will consume in
// M4+.
type DispatchHandler struct {
	identity  identityv1.IdentityServiceClient
	zone      zonev1.ZoneServiceClient
	packetver uint32
	logger    zerolog.Logger
	// registry is the gateway-wide map-scoped session index. It is
	// populated by handleCZEnter on a successful map enter and read
	// by the future NATS broadcast subscriber (a later workstream
	// fans events out via ForEachOnMap). The interface is satisfied
	// by service.NewSessionRegistry and shared with TCPHandler /
	// WSHandler so the OnClose / disconnect paths can unregister
	// the same account.
	registry SessionRegistry

	// defaultMap is the initial map name advertised in HC_NOTIFY_ZONESVR
	// after CH_SELECT_CHAR. Sourced from zone.default_map.
	defaultMap string
	// zoneIP is the IPv4 host written into the HC_NOTIFY_ZONESVR IP slot
	// (network-byte-order uint32). Pre-resolved at construction time by
	// resolveZoneIPv4 so a config like "localhost:5121" produces a real
	// advertised IP rather than the 0.0.0.0 parseIPv4("localhost")
	// silently returned before the review fix. Sourced from
	// gateway.map_addr.
	zoneIP uint32
	// zonePort is the TCP port written into HC_NOTIFY_ZONESVR. Sourced
	// from gateway.map_addr.
	zonePort uint16
	// respawnDelay is the time to wait before respawning a killed monster.
	respawnDelay time.Duration

	// areaSender is the on-enter area-spawner that lets a newly-entering
	// player see every other session already on its map (rAthena's
	// clif_getareachar_unit direction). It is set by the DI composition
	// root after construction via SetAreaSender and remains nil in unit
	// tests that do not exercise the broadcast path; the call-site
	// (sendSelfSpawnAndUpdateRegistry) guards against a nil interface.
	// Declared as the AreaSender interface (not *BroadcastSubscriber) so
	// the dispatch handler does not depend on the NATS-backed
	// implementation — a future in-process test double or a different
	// transport can satisfy the same contract.
	areaSender AreaSender
}

// NewDispatchHandler constructs a dispatch-backed PacketHandler.
//
// defaultMap, zoneIP, and zonePort feed the HC_NOTIFY_ZONESVR frame
// emitted after CH_SELECT_CHAR. zoneIP is a pre-resolved network-byte-
// order IPv4 uint32 — see resolveZoneIPv4 for the literal/hostname
// resolution path. Sourced from config (zone.default_map /
// gateway.map_addr) via the DI provider.
//
// zone is the zone gRPC client used by the map-phase handlers
// (CZ_ENTER → zone.EnterZone). Passing nil for zone is allowed in
// tests that do not exercise the map path; the map-phase handlers
// check for nil and refuse the client with a generic ZC_REFUSE_ENTER
// error rather than panic.
func NewDispatchHandler(
	identity identityv1.IdentityServiceClient,
	zone zonev1.ZoneServiceClient,
	packetver int,
	logger zerolog.Logger,
	defaultMap string,
	zoneIP uint32,
	zonePort uint16,
	registry SessionRegistry,
) *DispatchHandler {
	return &DispatchHandler{
		identity:     identity,
		zone:         zone,
		packetver:    uint32(packetver), //nolint:gosec // validated upstream by config.min/max=20260000
		logger:       logger.With().Str("component", "gateway.dispatch").Logger(),
		defaultMap:   defaultMap,
		zoneIP:       zoneIP,
		zonePort:     zonePort,
		registry:     registry,
		respawnDelay: 5 * time.Second,
	}
}

// SetAreaSender installs the broadcast area-spawner used to show a
// newly-entering player the entities already present on its map. It is
// set by the DI composition root after construction; the field is nil
// in unit tests that do not exercise the broadcast path, and the
// call-site guards against a nil interface.
func (h *DispatchHandler) SetAreaSender(as AreaSender) {
	h.areaSender = as
}

// HandlePacket dispatches a single decoded kRO packet. Parse errors on
// any known command are logged and swallowed — rAthena tolerates a
// truncated or corrupt packet by dropping it without closing the
// connection, since the client will retry after re-reading the
// addressbook.
func (h *DispatchHandler) HandlePacket(ctx context.Context, conn *domain.ConnectionInfo, resp domain.Responder, cmd uint16, frame []byte) error {
	switch cmd {
	case packet.HeaderCALOGIN:
		return h.handleCALogin(ctx, conn, resp, frame)
	case packet.HeaderCHENTER:
		return h.handleCHEnter(ctx, conn, resp, frame)
	case packet.HeaderCHSELECTCHAR:
		return h.handleCHSelectChar(ctx, conn, resp, frame)
	default:
		return h.handleMapPacket(ctx, conn, resp, cmd, frame)
	}
}

// handleMapPacket dispatches map-phase (CZ_*) packets. Extracted from
// HandlePacket to keep the top-level switch under the gocyclo limit.
func (h *DispatchHandler) handleMapPacket(ctx context.Context, conn *domain.ConnectionInfo, resp domain.Responder, cmd uint16, frame []byte) error {
	switch cmd {
	case packet.HeaderCZENTER:
		return h.handleCZEnter(ctx, conn, resp, frame)
	case packet.HeaderCZREQUESTMOVE:
		return h.handleCZRequestMove(ctx, conn, resp, frame)
	case packet.HeaderCZNOTIFYACTORINIT:
		return h.handleCZNotifyActorInit(ctx, conn, resp)
	case packet.HeaderCZREQUESTTIME:
		return h.handleCZRequestTime(ctx, conn, resp, frame)
	case packet.HeaderCZGLOBALMESSAGE:
		return h.handleCZGlobalMessage(ctx, conn, resp, frame)
	case packet.HeaderCZACTIONREQUEST:
		return h.handleCZActionRequest(ctx, conn, resp, frame)
	case packet.HeaderCZCHANGEDIR:
		return h.handleCZChangeDir(ctx, conn, resp, frame)
	case packet.HeaderCZREQEMOTION:
		return h.handleCZReqEmotion(ctx, conn, resp, frame)
	case packet.HeaderCZGETCHARNAMEREQUEST:
		return h.handleCZGetCharNameRequest(ctx, conn, resp, frame)
	case packet.HeaderCZRESTART:
		return h.handleCZRestart(ctx, conn, resp, frame)
	case packet.HeaderCZUSEITEM2:
		return h.handleCZUseItem(ctx, conn, resp, frame)
	case packet.HeaderCZREQWEAREQUIPV5:
		return h.handleCZReqWearEquip(ctx, conn, resp, frame)
	case packet.HeaderCZREQTAKEOFFEQUIP:
		return h.handleCZReqTakeoffEquip(ctx, conn, resp, frame)
	default:
		return h.handleMapPacketNPC(ctx, conn, resp, cmd, frame)
	}
}

// handleMapPacketNPC dispatches the NPC-interaction (M15 dialog + M16
// shop) sub-group of map-phase packets. Extracted from
// handleMapPacket to keep that function's switch under the gocyclo
// limit (the table grew past 15 cases in M16).
func (h *DispatchHandler) handleMapPacketNPC(ctx context.Context, conn *domain.ConnectionInfo, resp domain.Responder, cmd uint16, frame []byte) error {
	switch cmd {
	case packet.HeaderCZCONTACTNPC:
		return h.handleCZContactNPC(ctx, conn, resp, frame)
	case packet.HeaderCZREQNEXTSCRIPT:
		return h.handleCZReqNextScript(ctx, conn, resp, frame)
	case packet.HeaderCZCLOSEDIALOG:
		return h.handleCZCloseDialog(ctx, conn, resp, frame)
	case packet.HeaderCZACKSELECTDEALTYPE:
		return h.handleCZAckSelectDealType(ctx, conn, resp, frame)
	case packet.HeaderCZPCPURCHASEITEMLIST:
		return h.handleCZPCPurchaseItemList(ctx, conn, resp, frame)
	default:
		h.logger.Debug().
			Uint64("conn", conn.ID).
			Uint16("cmd", cmd).
			Msg("unhandled map packet")
		return nil
	}
}

// handleCALogin forwards CA_LOGIN to identity.Authenticate and encodes
// the reply. Extracted from the M1b switch to keep HandlePacket's
// dispatch table readable.
func (h *DispatchHandler) handleCALogin(ctx context.Context, conn *domain.ConnectionInfo, resp domain.Responder, frame []byte) error {
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
func (h *DispatchHandler) handleCHEnter(ctx context.Context, conn *domain.ConnectionInfo, resp domain.Responder, frame []byte) error {
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

	totalSlots := min(listResp.GetTotalSlots(), maxCharListCount)
	total := uint8(totalSlots) //nolint:gosec // clamped to maxCharListCount=255 above
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
func (h *DispatchHandler) handleCHSelectChar(_ context.Context, conn *domain.ConnectionInfo, resp domain.Responder, frame []byte) error {
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
		IP:      h.zoneIP,
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

// handleCZEnter forwards CZ_ENTER to zone.EnterZone and encodes the
// reply. On success the client receives two packets in order:
//
//  1. ZC_ACCEPT_ENTER (cmd 0x02eb) carrying the map name + spawn
//     position from the zone.
//  2. ZC_SPAWN_UNIT (cmd 0x09fe) describing the player's own entity
//     — the client uses this to render its own character on the map.
//
// On failure the gateway sends ZC_REFUSE_ENTER with error code 0
// (rAthena's generic "server closed" refuse for map-server entry —
// there's no fine-grained client-visible code book on the map side
// the way HC_REFUSE_ENTER has on the char side).
//
// Reference: rathena/src/map/clif.cpp:10642 (clif_parse_WantToConnection)
// and the corresponding map_send_zip0088_accept_enter / clif_authfail
// emission sites; rathena/src/map/clif.cpp clif_spawn for the
// self-spawn ZC_SPAWN_UNIT emission that follows.
func (h *DispatchHandler) handleCZEnter(ctx context.Context, conn *domain.ConnectionInfo, resp domain.Responder, frame []byte) error {
	req, err := packet.ParseCZEnter(frame)
	if err != nil {
		h.logger.Warn().
			Err(err).
			Uint64("conn", conn.ID).
			Int("frame_len", len(frame)).
			Msg("malformed CZ_ENTER; dropping packet")
		return nil
	}

	if h.zone == nil {
		// DI misconfiguration or a test harness that did not wire the
		// zone client — surface a generic refuse rather than panic on
		// a nil-deref inside the gRPC stub.
		h.logger.Error().
			Uint64("conn", conn.ID).
			Uint32("aid", req.AccountID).
			Uint32("cid", req.CharID).
			Msg("zone client not configured; refusing map enter")
		return sendMapRefuse(resp)
	}

	zReq := &zonev1.EnterZoneRequest{
		AccountId:  req.AccountID,
		CharId:     req.CharID,
		LoginId1:   uint64(req.AuthCode),
		ClientTick: req.ClientTime,
		Sex:        sexString(req.Sex),
		Packetver:  h.packetver,
		ClientIp:   splitHost(conn.RemoteIP),
	}

	zResp, err := h.zone.EnterZone(ctx, zReq)
	if err != nil {
		if clientGone := errors.Is(err, context.Canceled) || ctx.Err() != nil; clientGone {
			h.logger.Debug().
				Err(err).
				Uint64("conn", conn.ID).
				Uint32("aid", req.AccountID).
				Msg("zone call cancelled (client gone)")
			_ = err
			return nil
		}
		st, _ := status.FromError(err)
		h.logger.Error().
			Err(err).
			Uint64("conn", conn.ID).
			Uint32("aid", req.AccountID).
			Str("grpc_code", st.Code().String()).
			Msg("zone EnterZone RPC failed; refusing map enter")
		return sendMapRefuse(resp)
	}
	if zResp == nil {
		h.logger.Warn().
			Uint64("conn", conn.ID).
			Uint32("aid", req.AccountID).
			Msg("zone returned nil EnterZone response; refusing map enter")
		return sendMapRefuse(resp)
	}
	if !zResp.GetSuccess() {
		h.logger.Warn().
			Uint64("conn", conn.ID).
			Uint32("aid", req.AccountID).
			Str("error", zResp.GetError()).
			Msg("zone refused map enter")
		return sendMapRefuse(resp)
	}

	// Cache the authenticated AID on the connection so the post-enter
	// packet stream (CZ_REQUEST_MOVE, chat, etc.) can attribute
	// packets to the zone entity without re-deriving the AID from the
	// wire (CZ_REQUEST_MOVE carries only dest x/y). Cleared on
	// connection close by gnet dropping connState.
	conn.AccountID = req.AccountID
	conn.CharID = req.CharID
	conn.MapName = zResp.GetMapName()

	// Install the session in the registry now that the map is known.
	// View is populated later (below) at the exact point the
	// identity.GetCharacter RPC result is available, avoiding a
	// second RPC at this site. The Responder is the per-connection
	// transport Responder; for TCP it wraps a stable gnet.Conn
	// (AsyncWrite is goroutine-safe) and for WS it wraps the active
	// read-context (lives until the WS serve loop returns), so
	// storing it as a fat pointer is correct.
	if conn.AccountID != 0 {
		h.registry.Register(conn.AccountID, domain.Session{
			Responder: resp,
			CharID:    conn.CharID,
			MapName:   conn.MapName,
		})
	}

	accept := packet.MapAcceptEnterResponse{
		StartTime: uint32(time.Now().Unix()), //nolint:gosec // low 32 bits of unix time per rAthena startTime convention
		PosX:      clampMapCoord(zResp.GetMapX()),
		PosY:      clampMapCoord(zResp.GetMapY()),
		Dir:       0,
		XSize:     5,
		YSize:     5,
		Font:      0,
	}

	var buf bytes.Buffer
	if err := accept.Encode(&buf); err != nil {
		// MapAcceptEnterResponse.Encode cannot fail in practice (no
		// variable-width fields), but we still bubble the error up
		// rather than silently swallow it — wrapcheck requires every
		// external error be wrapped.
		h.logger.Error().
			Err(err).
			Uint64("conn", conn.ID).
			Uint32("aid", req.AccountID).
			Msg("encode ZC_ACCEPT_ENTER failed; refusing map enter")
		return sendMapRefuse(resp)
	}

	h.logger.Info().
		Uint64("conn", conn.ID).
		Uint32("aid", req.AccountID).
		Uint32("cid", req.CharID).
		Str("map", zResp.GetMapName()).
		Uint32("pos_x", zResp.GetMapX()).
		Uint32("pos_y", zResp.GetMapY()).
		Msg("map enter accepted")

	if err := resp.SendPacket(buf.Bytes()); err != nil {
		return fmt.Errorf("send ZC_ACCEPT_ENTER: %w", err)
	}

	// Send the self-spawn packet so the client renders its own
	// character on the map. Character-specific fields (job, stats,
	// name) are populated from the identity service's GetCharacter
	// RPC. On any failure (gRPC error, success=false, or a nil
	// character) we fall back to a zero-filled spawn so the handshake
	// still completes — the player is already in the map, and a
	// missing spawn is preferable to a torn connection. A send
	// failure here does not tear the connection down either (the
	// client will surface the missing sprite as a visible glitch,
	// not a fatal protocol error).
	//
	// The same RPC result is also written back into the session
	// registry as the View snapshot — a single GetCharacter call
	// services both the self-spawn encode and the registry, so the
	// future fan-out produces the same wire shape as the per-conn
	// self-spawn.
	return h.sendSelfSpawnAndUpdateRegistry(conn, resp, req, zResp)
}

// sendSelfSpawnAndUpdateRegistry fetches the character for the
// self-spawn (reusing the same RPC result for the session registry's
// View snapshot) and sends the ZC_SPAWN_UNIT frame. Extracted from
// handleCZEnter to keep the parent's gocyclo budget under 15 after
// the registry wiring was added in Step 2c.
func (h *DispatchHandler) sendSelfSpawnAndUpdateRegistry(
	conn *domain.ConnectionInfo,
	resp domain.Responder,
	req packet.CZEnterRequest,
	zResp *zonev1.EnterZoneResponse,
) error {
	char, err := h.fetchCharacterForSpawn(conn, req)
	if err != nil {
		// Logged by fetchCharacterForSpawn; the fallback below gives
		// the client a usable, if unstyled, sprite.
		_ = err
	}
	if char != nil && conn.AccountID != 0 {
		// SetView silently no-ops if the session was Unregistered
		// between Register and this point (e.g. the client dropped
		// mid-handshake); the future ForEachOnMap will not see a
		// stale entry either way.
		h.registry.SetView(conn.AccountID, viewDataFromCharacter(conn.AccountID, char))
	}
	spawn := h.buildSelfSpawnFromCharacter(conn, req, char, zResp)

	var spawnBuf bytes.Buffer
	if err := spawn.Encode(&spawnBuf); err != nil {
		// SpawnUnitResponse.Encode cannot fail in practice (no
		// variable-width fields), but we still bubble the error up
		// rather than silently swallow it — wrapcheck requires every
		// external error be wrapped. Map-phase send failures are not
		// fatal at this stage (the handshake already succeeded), but
		// an encode failure indicates a programmer error and must
		// surface.
		h.logger.Error().
			Err(err).
			Uint64("conn", conn.ID).
			Uint32("aid", req.AccountID).
			Msg("encode ZC_SPAWN_UNIT failed; map enter partially delivered")
		return fmt.Errorf("encode ZC_SPAWN_UNIT: %w", err)
	}

	if err := resp.SendPacket(spawnBuf.Bytes()); err != nil {
		// Log and continue — ZC_ACCEPT_ENTER was already sent, the
		// connection is in a usable state, and a spawn send failure
		// (peer closed mid-stream, transport buffer full) is not a
		// reason to tear the conn down. The client will reconnect
		// and re-handshake.
		h.logger.Warn().
			Err(err).
			Uint64("conn", conn.ID).
			Uint32("aid", req.AccountID).
			Msg("send ZC_SPAWN_UNIT failed; map enter partially delivered")
	}

	// Tell the entering player about every other session already on this
	// map (rAthena's clif_getareachar_unit direction). Without this the
	// second player to enter would never see the first until the first
	// moved, because the first's EntitySpawned event fired before this
	// player existed. The area sender is nil in unit tests that bypass the
	// broadcast path; a nil interface is guarded here (a nil *BroadcastSubscriber
	// would be nil-receiver-safe, but the field is the interface type).
	if h.areaSender != nil {
		h.areaSender.SendAreaEntities(conn.MapName, conn.AccountID, resp)
	}
	return nil
}

// buildSelfSpawnFromCharacter assembles the ZC_SPAWN_UNIT response for
// the player's own entity from a pre-fetched character snapshot. On
// a nil character the function returns a zero-filled fallback so the
// map enter handshake always completes — the caller (handleCZEnter)
// decides how to surface the send.
//
// The character is fetched once in handleCZEnter and passed in here
// so the same GetCharacter RPC result can be mirrored into the
// session registry's View snapshot (via viewDataFromCharacter) without
// a second round-trip.
func (h *DispatchHandler) buildSelfSpawnFromCharacter(
	conn *domain.ConnectionInfo,
	req packet.CZEnterRequest,
	char *identityv1.CharacterDetail,
	zResp *zonev1.EnterZoneResponse,
) packet.SpawnUnitResponse {
	spawn := packet.SpawnUnitResponse{
		ObjectType: 0, // TYPE_PC — the only value the gateway emits today.
		AID:        conn.AccountID,
		// GID is the entity ID (rathena's `id`). For the PC self-spawn
		// this is the character's own char_id, not the account_id —
		// the client uses GID to attribute local input back to the
		// entity on the map and a mismatch would break per-entity
		// chat and move broadcasts.
		GID:   conn.CharID,
		Speed: 150,
		PosX:  clampMapCoord(zResp.GetMapX()),
		PosY:  clampMapCoord(zResp.GetMapY()),
		Dir:   0,
		XSize: 5,
		YSize: 5,
		// Initial Sex comes from the parsed CZ_ENTER request byte;
		// the non-nil-char path below overrides it with the identity
		// row (M7a). Kept as the fallback so the identity-failure
		// path still renders a sex-aware default sprite.
		Sex: req.Sex,
	}

	if char == nil {
		// Fallback values match the pre-M7a behaviour: a known-good
		// wire shape with zero-filled character-specific fields.
		// CLevel=1 / MaxHP=1 / HP=1 / Name="" render as a default
		// sprite on every client we care about; the player is still
		// in the map and can chat / move.
		spawn.CLevel = 1
		spawn.MaxHP = 1
		spawn.HP = 1
		return spawn
	}

	spawn.Job = int16(clampUint16(char.GetClassId())) //nolint:gosec // wire slot is 16-bit
	spawn.Head = clampUint16(char.GetHair())
	spawn.Weapon = char.GetWeapon()
	spawn.Shield = char.GetShield()
	// Accessory / Accessory2 / Accessory3 carry the head_bottom /
	// head_top / head_mid view sprites in rAthena's clif_spawn
	// packing order. clampUint16 saturates > 65535 values to 0
	// (sentinel) so a misconfigured row visibly fails rather than
	// silently wraps; the schema caps these columns at smallint so
	// the clamp never fires in practice.
	spawn.Accessory = clampUint16(char.GetHeadBottom())
	spawn.Accessory2 = clampUint16(char.GetHeadTop())
	spawn.Accessory3 = clampUint16(char.GetHeadMid())
	spawn.HeadPalette = int16(clampUint16(char.GetHairColor()))    //nolint:gosec // wire slot is 16-bit
	spawn.BodyPalette = int16(clampUint16(char.GetClothesColor())) //nolint:gosec // wire slot is 16-bit
	spawn.Robe = clampUint16(char.GetRobe())
	spawn.Sex = uint8(clampUint16(char.GetSex()))          //nolint:gosec // wire slot is 8-bit
	spawn.CLevel = int16(clampUint16(char.GetBaseLevel())) //nolint:gosec // wire slot is 16-bit
	// MaxHP / HP come in as uint32 from the proto and go out as
	// int32 on the wire; rAthena caps max_hp at int32 (2^31 - 1)
	// in pc.cpp::pc_setnewpc, so the conversion is safe in
	// practice — annotate so gosec does not flag it.
	spawn.MaxHP = int32(char.GetMaxHp()) //nolint:gosec // max_hp is int32 on the wire; values above 2^31-1 are impossible by rAthena's clamp
	spawn.HP = int32(char.GetHp())       //nolint:gosec // ditto
	spawn.Name = char.GetName()
	return spawn
}

// fetchCharacterByConn wraps fetchCharacterForSpawn for the post-actorinit
// status burst (M9). handleCZNotifyActorInit does not have a parsed
// CZEnterRequest frame — only the cached conn.AccountID + conn.CharID
// from the prior CZ_ENTER — so we rebuild the request here. Returns
// (nil, nil) on a zero key so the caller can fall back to a zero-filled
// status burst without an error log.
func (h *DispatchHandler) fetchCharacterByConn(conn *domain.ConnectionInfo) (*identityv1.CharacterDetail, error) {
	if conn.AccountID == 0 || conn.CharID == 0 {
		return nil, nil
	}
	return h.fetchCharacterForSpawn(conn, packet.CZEnterRequest{
		AccountID: conn.AccountID,
		CharID:    conn.CharID,
	})
}

// fetchCharacterForSpawn invokes identity.GetCharacter and returns
// the resulting CharacterDetail. Any failure — gRPC error, nil
// response, success=false, or an internal error from the handler — is
// logged and returns (nil, err) so buildSelfSpawn can fall back to
// the zero-filled shape. The handshake is never blocked on identity
// availability.
func (h *DispatchHandler) fetchCharacterForSpawn(
	conn *domain.ConnectionInfo,
	req packet.CZEnterRequest,
) (*identityv1.CharacterDetail, error) {
	charResp, err := h.identity.GetCharacter(context.Background(), &identityv1.GetCharacterRequest{
		AccountId: req.AccountID,
		CharId:    req.CharID,
	})
	if err != nil {
		if clientGone := errors.Is(err, context.Canceled); clientGone {
			h.logger.Debug().
				Err(err).
				Uint64("conn", conn.ID).
				Uint32("cid", req.CharID).
				Msg("identity GetCharacter cancelled (client gone)")
			return nil, fmt.Errorf("identity GetCharacter (client gone): %w", err)
		}
		st, _ := status.FromError(err)
		h.logger.Warn().
			Err(err).
			Uint64("conn", conn.ID).
			Uint32("aid", req.AccountID).
			Uint32("cid", req.CharID).
			Str("grpc_code", st.Code().String()).
			Msg("identity GetCharacter RPC failed; falling back to zero-filled spawn")
		return nil, fmt.Errorf("identity GetCharacter (cid=%d): %w", req.CharID, err)
	}
	if charResp == nil {
		h.logger.Warn().
			Uint64("conn", conn.ID).
			Uint32("aid", req.AccountID).
			Uint32("cid", req.CharID).
			Msg("identity returned nil GetCharacter response; falling back to zero-filled spawn")
		return nil, nil
	}
	if !charResp.GetSuccess() {
		h.logger.Warn().
			Uint64("conn", conn.ID).
			Uint32("aid", req.AccountID).
			Uint32("cid", req.CharID).
			Str("error", charResp.GetError()).
			Msg("identity GetCharacter returned success=false; falling back to zero-filled spawn")
		return nil, nil
	}
	char := charResp.GetCharacter()
	if char == nil {
		h.logger.Warn().
			Uint64("conn", conn.ID).
			Uint32("aid", req.AccountID).
			Uint32("cid", req.CharID).
			Msg("identity GetCharacter returned nil character; falling back to zero-filled spawn")
		return nil, nil
	}
	return char, nil
}

// handleCZRequestMove forwards CZ_REQUEST_MOVE to zone.MoveEntity and
// encodes the broadcast packet ZC_NOTIFY_PLAYERMOVE 0x0087 so the
// client can interpolate the sprite from the source cell to the
// destination cell. The source cell comes from the zone's
// GetEntity call (the entity's current X/Y before MoveEntity updates
// the path); the destination is the cell the path targets.
//
// Wire failures (identity/zone gRPC errors, missing account_id on a
// not-yet-entered connection, zone-side MoveEntity rejection) are
// logged and swallowed — rAthena treats a dropped move packet as a
// harmless transient the client will retry after the next tick, not a
// reason to tear the connection down.
func (h *DispatchHandler) handleCZRequestMove(ctx context.Context, conn *domain.ConnectionInfo, resp domain.Responder, frame []byte) error {
	req, err := packet.ParseCZRequestMove(frame)
	if err != nil {
		h.logger.Warn().
			Err(err).
			Uint64("conn", conn.ID).
			Int("frame_len", len(frame)).
			Msg("malformed CZ_REQUEST_MOVE; dropping packet")
		return nil
	}

	if conn.AccountID == 0 {
		// CZ_REQUEST_MOVE without a preceding CZ_ENTER: the client
		// has not authenticated against the zone yet, so we have no
		// entity to attribute the move to. Drop silently rather than
		// panic on a zero AID at the zone boundary.
		h.logger.Warn().
			Uint64("conn", conn.ID).
			Int16("dest_x", req.DestX).
			Int16("dest_y", req.DestY).
			Msg("CZ_REQUEST_MOVE without prior CZ_ENTER; dropping")
		return nil
	}

	if h.zone == nil {
		// DI misconfiguration or a test harness that did not wire the
		// zone client — surface a debug log rather than panic on a
		// nil-deref inside the gRPC stub. The next CZ_REQUEST_MOVE
		// will get the same treatment.
		h.logger.Error().
			Uint64("conn", conn.ID).
			Uint32("aid", conn.AccountID).
			Msg("zone client not configured; dropping CZ_REQUEST_MOVE")
		return nil
	}

	zResp, err := h.zone.MoveEntity(ctx, &zonev1.MoveEntityRequest{
		AccountId: conn.AccountID,
		DestX:     uint32(req.DestX), //nolint:gosec // map cell position (int16 → uint32)
		DestY:     uint32(req.DestY), //nolint:gosec // map cell position (int16 → uint32)
	})
	if err != nil {
		if clientGone := errors.Is(err, context.Canceled) || ctx.Err() != nil; clientGone {
			h.logger.Debug().
				Err(err).
				Uint64("conn", conn.ID).
				Uint32("aid", conn.AccountID).
				Msg("zone call cancelled (client gone)")
			_ = err
			return nil
		}
		st, _ := status.FromError(err)
		h.logger.Warn().
			Err(err).
			Uint64("conn", conn.ID).
			Uint32("aid", conn.AccountID).
			Str("grpc_code", st.Code().String()).
			Msg("zone MoveEntity RPC failed; dropping move")
		return nil
	}
	if zResp == nil {
		h.logger.Warn().
			Uint64("conn", conn.ID).
			Uint32("aid", conn.AccountID).
			Msg("zone returned nil MoveEntity response; dropping move")
		return nil
	}
	if !zResp.GetSuccess() {
		h.logger.Debug().
			Uint64("conn", conn.ID).
			Uint32("aid", conn.AccountID).
			Int16("dest_x", req.DestX).
			Int16("dest_y", req.DestY).
			Str("error", zResp.GetError()).
			Msg("zone rejected move")
		return nil
	}

	notify := packet.MapNotifyPlayerMoveResponse{
		MoveStartTime: uint32(time.Now().UnixMilli()), //nolint:gosec // low 32 bits of unix millis per rAthena moveStartTime convention
		SrcX:          clampMapCoord(zResp.GetSrcX()),
		SrcY:          clampMapCoord(zResp.GetSrcY()),
		DestX:         clampMapCoord(zResp.GetDestX()),
		DestY:         clampMapCoord(zResp.GetDestY()),
	}

	var buf bytes.Buffer
	if err := notify.Encode(&buf); err != nil {
		// MapNotifyPlayerMoveResponse.Encode cannot fail in practice
		// (no variable-width fields), but we still bubble the error
		// up rather than silently swallow it — wrapcheck requires
		// every external error be wrapped.
		h.logger.Error().
			Err(err).
			Uint64("conn", conn.ID).
			Uint32("aid", conn.AccountID).
			Msg("encode ZC_NOTIFY_PLAYERMOVE failed; dropping move")
		return nil
	}

	h.logger.Debug().
		Uint64("conn", conn.ID).
		Uint32("aid", conn.AccountID).
		Int16("src_x", notify.SrcX).
		Int16("src_y", notify.SrcY).
		Int16("dest_x", notify.DestX).
		Int16("dest_y", notify.DestY).
		Msg("move broadcast")

	if err := resp.SendPacket(buf.Bytes()); err != nil {
		return fmt.Errorf("send ZC_NOTIFY_PLAYERMOVE: %w", err)
	}
	return nil
}

// handleCZNotifyActorInit responds to CZ_NOTIFY_ACTORINIT (0x007d) —
// the client's signal that it has finished loading the map. rAthena
// responds with the map-property frame followed by a burst of status
// packets (clif_parse_LoadEndAck, rathena/src/map/clif.cpp:10791-10915)
// that populate the HP/SP bars, stats window, level display, and zeny
// counter. We send ZC_MAPPROPERTY_R2 (MAPPROPERTY_NOTHING) followed by
// the status burst. If the identity GetCharacter fetch fails, the
// burst is sent with zero values — the client tolerates a default
// stats window and the handshake still completes.
func (h *DispatchHandler) handleCZNotifyActorInit(ctx context.Context, conn *domain.ConnectionInfo, resp domain.Responder) error {
	prop := packet.MapPropertyResponse{
		PropertyType: 0, // MAPPROPERTY_NOTHING
		Flags:        0,
	}
	var propBuf bytes.Buffer
	if err := prop.Encode(&propBuf); err != nil {
		// MapPropertyResponse.Encode cannot fail in practice (no
		// variable-width fields), but we still bubble the error up
		// rather than silently swallow it — wrapcheck requires every
		// external error be wrapped. Drop the packet rather than
		// refuse the whole handshake: the client already entered the
		// map and the rest of the connection is unaffected.
		h.logger.Error().
			Err(err).
			Uint64("conn", conn.ID).
			Msg("encode ZC_MAPPROPERTY_R2 failed")
		return nil
	}
	h.logger.Info().Uint64("conn", conn.ID).Msg("map property sent")
	if err := resp.SendPacket(propBuf.Bytes()); err != nil {
		return fmt.Errorf("send ZC_MAPPROPERTY_R2: %w", err)
	}

	char, err := h.fetchCharacterByConn(conn)
	if err != nil {
		// Logged by fetchCharacterByConn; fall back to a zero-valued
		// status burst below. The client shows 0/0 stats instead of
		// the character's real values but the handshake still
		// completes — preferable to tearing the connection down over
		// a transient identity outage.
		_ = err
	}

	baseExp, jobExp := conn.ExpValues()

	// Default values for every parameter — zeny is not in the
	// proto today (M9) so it is always zero. Weight and max_weight
	// require inventory tracking (deferred). Manner/karma are sent as
	// zero (no system yet).
	var (
		hp, maxHP, sp, maxSP uint32
		baseLevel, jobLevel  uint32
		statusPoint          uint32
		skillPoint           uint32
		strV, agiV           uint8
		vitV, intV           uint8
		dexV, lukV           uint8
	)
	if char != nil {
		hp = char.GetHp()
		maxHP = char.GetMaxHp()
		sp = char.GetSp()
		maxSP = char.GetMaxSp()
		baseLevel = char.GetBaseLevel()
		jobLevel = char.GetJobLevel()
		statusPoint = char.GetStatusPoint()
		skillPoint = char.GetSkillPoint()
		strV = uint8(min(char.GetStr(), 255)) //nolint:gosec // clamp to max uint8 to prevent wrap-around
		agiV = uint8(min(char.GetAgi(), 255)) //nolint:gosec // ditto
		vitV = uint8(min(char.GetVit(), 255)) //nolint:gosec // ditto
		intV = uint8(min(char.GetInt(), 255)) //nolint:gosec // ditto
		dexV = uint8(min(char.GetDex(), 255)) //nolint:gosec // ditto
		lukV = uint8(min(char.GetLuk(), 255)) //nolint:gosec // ditto
	}

	// rAthena clamps HP to a minimum of 1 on LoadEndAck so the client
	// never sees "0 / 0" during the spawn frame.
	if hp == 0 {
		hp = 1
	}

	// Batch every status packet into a single send so the client
	// receives them as one coalesced write. The order matches
	// rAthena's clif_parse_LoadEndAck sequence
	// (rathena/src/map/clif.cpp:10791-10915).
	var burst bytes.Buffer
	packets := []interface {
		Encode(io.Writer) error
	}{
		// Weight / max weight — zero today (no inventory tracking).
		packet.ParChangeResponse{VarID: packet.SPWeight, Count: 0},
		packet.ParChangeResponse{VarID: packet.SPMaxWeight, Count: 0},
		// Speed — hardcoded 150 (rAthena's default PC amotion).
		packet.ParChangeResponse{VarID: packet.SPSpeed, Count: 150},
		// Base / job level.
		packet.ParChangeResponse{VarID: packet.SPBaseLevel, Count: int32(baseLevel)}, //nolint:gosec // base_level fits in int32 (≤ MAX_LEVEL)
		packet.ParChangeResponse{VarID: packet.SPJobLevel, Count: int32(jobLevel)},   //nolint:gosec // job_level fits in int32
		// Status / skill points.
		packet.ParChangeResponse{VarID: packet.SPStatusPoint, Count: int32(statusPoint)}, //nolint:gosec // status_point fits in int32
		packet.ParChangeResponse{VarID: packet.SPSkillPoint, Count: int32(skillPoint)},   //nolint:gosec // skill_point fits in int32
		// Max HP / SP first, then current HP / SP (rAthena order).
		packet.ParChangeResponse{VarID: packet.SPMaxHP, Count: int32(maxHP)}, //nolint:gosec // max_hp fits in int32
		packet.ParChangeResponse{VarID: packet.SPMaxSP, Count: int32(maxSP)}, //nolint:gosec // max_sp fits in int32
		packet.ParChangeResponse{VarID: packet.SPHP, Count: int32(hp)},       //nolint:gosec // hp fits in int32
		packet.ParChangeResponse{VarID: packet.SPSP, Count: int32(sp)},       //nolint:gosec // sp fits in int32
		// Zeny + base/job exp (32-bit; ZC_LONGLONGPAR_CHANGE upgrade deferred).
		packet.LongParChangeResponse{VarID: packet.SPZeny, Amount: 0},
		packet.LongParChangeResponse{VarID: packet.SPBaseExp, Amount: baseExp},
		packet.LongParChangeResponse{VarID: packet.SPJobExp, Amount: jobExp},
		// ZC_STATUS — base stats with their upgrade costs + derived combat values (zero).
		packet.StatusResponse{
			StatusPoint: uint16(statusPoint), //nolint:gosec // status_point fits in uint16 for pre-renewal
			Str:         strV,
			NeedStr:     packet.StatusPointCost(strV),
			Agi:         agiV,
			NeedAgi:     packet.StatusPointCost(agiV),
			Vit:         vitV,
			NeedVit:     packet.StatusPointCost(vitV),
			Int:         intV,
			NeedInt:     packet.StatusPointCost(intV),
			Dex:         dexV,
			NeedDex:     packet.StatusPointCost(dexV),
			Luk:         lukV,
			NeedLuk:     packet.StatusPointCost(lukV),
		},
		// Per-stat par-changes so the UI updates after the status block.
		packet.ParChangeResponse{VarID: packet.SPStr, Count: int32(strV)}, //nolint:gosec // str fits in int32
		packet.ParChangeResponse{VarID: packet.SPAgi, Count: int32(agiV)}, //nolint:gosec // agi fits in int32
		packet.ParChangeResponse{VarID: packet.SPVit, Count: int32(vitV)}, //nolint:gosec // vit fits in int32
		packet.ParChangeResponse{VarID: packet.SPInt, Count: int32(intV)}, //nolint:gosec // int fits in int32
		packet.ParChangeResponse{VarID: packet.SPDex, Count: int32(dexV)}, //nolint:gosec // dex fits in int32
		packet.ParChangeResponse{VarID: packet.SPLuk, Count: int32(lukV)}, //nolint:gosec // luk fits in int32
	}
	for _, pkt := range packets {
		if err := pkt.Encode(&burst); err != nil {
			// Encode errors on these fixed-layout packets are programmer
			// mistakes — log and bail rather than send a half-baked burst.
			h.logger.Error().
				Err(err).
				Uint64("conn", conn.ID).
				Msg("encode status burst packet failed")
			return nil
		}
	}

	// P2A: replace the M10 empty inventory list stubs with real items
	// sourced from identity.GetInventory. The order matches rAthena's
	// clif_parse_LoadEndAck sequence (rathena/src/map/clif.cpp:10791-10915
	// — the inventory normal list, then equip list, then skill list,
	// then hotkey list). On any identity failure (gRPC error, nil
	// response) we fall back to the empty list — the client initialises
	// its inventory grid with the 4-byte header, and the player is
	// already in the map. bytes.Buffer.Write never returns an error
	// so the results are discarded.
	_, _ = burst.Write(h.encodeInventoryLists(ctx, conn))
	_, _ = burst.Write(packet.EncodeEmptySkillList())
	_, _ = burst.Write(packet.EncodeEmptyHotkeyList())

	// M14: append NPC spawn packets (ZC_SET_UNIT_IDLE, 0x09ff) after
	// the empty list packets. rAthena's clif_parse_LoadEndAck spawns
	// NPCs via clif_spawnnpc after the status burst; we send them
	// inline in the same coalesced write. NPC GIDs start at
	// 110000000 (rAthena START_NPC_NUM).
	for _, npc := range npcSpawns {
		idle := packet.SetUnitIdleResponse{
			ObjectType: 0x06, // NPC_EVT_TYPE for standard NPC sprites
			AID:        npc.GID,
			GID:        0,
			Speed:      0,
			Job:        npc.SpriteID,
			MaxHP:      -1,
			HP:         -1,
			PosX:       npc.X,
			PosY:       npc.Y,
			Dir:        npc.Dir,
			Name:       npc.Name,
		}
		if err := idle.Encode(&burst); err != nil {
			h.logger.Error().
				Err(err).
				Uint64("conn", conn.ID).
				Str("npc_name", npc.Name).
				Msg("encode ZC_SET_UNIT_IDLE failed")
			return nil
		}
	}

	// M17: append monster spawn packets (ZC_SET_UNIT_IDLE, 0x09ff) after
	// the NPC spawns. Monsters use objectType=0x05 (NPC_MOB_TYPE) and
	// show their HP bar (positive maxHP/HP, unlike NPCs which use -1).
	// rAthena: clif_getareachar_mob → clif_set_unit_idle for mob spawns.
	for _, mob := range monsterSpawns {
		idle := packet.SetUnitIdleResponse{
			ObjectType: 0x05, // NPC_MOB_TYPE
			AID:        mob.GID,
			GID:        0,
			Speed:      mob.Speed,
			Job:        mob.SpriteID,
			MaxHP:      mob.MaxHP,
			HP:         mob.HP,
			PosX:       mob.X,
			PosY:       mob.Y,
			Dir:        mob.Dir,
			Name:       mob.Name,
			CLevel:     mob.Level,
		}
		if err := idle.Encode(&burst); err != nil {
			h.logger.Error().
				Err(err).
				Uint64("conn", conn.ID).
				Str("mob_name", mob.Name).
				Msg("encode monster ZC_SET_UNIT_IDLE failed")
			return nil
		}
	}

	// M18: initialize per-connection monster HP tracking. Each connection
	// gets its own copy so multiple clients can independently damage
	// monsters without interfering (single-player echo path; true multi-
	// player HP sync is zone-side work).
	spawns := make([]domain.MonsterSpawn, len(monsterSpawns))
	for i, mob := range monsterSpawns {
		spawns[i] = domain.MonsterSpawn{GID: mob.GID, MaxHP: mob.HP}
	}
	conn.InitMonsterHP(spawns)

	h.logger.Info().
		Uint64("conn", conn.ID).
		Uint32("aid", conn.AccountID).
		Uint32("cid", conn.CharID).
		Uint32("hp", hp).
		Uint32("max_hp", maxHP).
		Uint32("base_level", baseLevel).
		Uint32("job_level", jobLevel).
		Int("npc_count", len(npcSpawns)).
		Int("mob_count", len(monsterSpawns)).
		Msg("status burst sent")

	if err := resp.SendPacket(burst.Bytes()); err != nil {
		return fmt.Errorf("send status burst: %w", err)
	}
	return nil
}

// encodeInventoryLists returns the on-wire bytes for
// ZC_INVENTORY_ITEMLIST_NORMAL followed by ZC_INVENTORY_ITEMLIST_EQUIP,
// sourced from identity.GetInventory. On any identity failure
// (gRPC error, nil response) the two empty-list stubs are returned
// instead so the client always sees the 4-byte header that
// initialises its inventory grid — the player is already in the
// map and a missing list is preferable to a torn connection.
//
// The split between normal and equip items mirrors rAthena's
// `inventory.equip` column semantics: a non-zero EQP_* bitmask puts
// the row on the equip list; a zero bitmask puts it on the normal
// list. Today the proto InventoryItem has no `equip` field of its
// own — the inventory service is the single point that maps
// inventory.equip to the wire, and the dispatcher treats the
// presence of an `equip` field on the wire as the split key. The
// P2A proto models `equip` as InventoryItem.equip, so we use that
// directly. TODO(P2A-WEIGHT): the total weight, max-weight, and
// per-slot weight checks land in a later workstream; this path
// always writes weight=0 / max-weight=0 because the InventoryItem
// proto does not yet carry the per-item weight.
func (h *DispatchHandler) encodeInventoryLists(ctx context.Context, conn *domain.ConnectionInfo) []byte {
	if conn.AccountID == 0 || conn.CharID == 0 {
		// No authenticated character — emit empty lists. This is
		// defensive: handleCZNotifyActorInit is only expected to
		// fire post-CZ_ENTER, but a transport that sends actor-init
		// out of order must not crash the dispatch.
		return concat(packet.EncodeEmptyInventoryListNormal(), packet.EncodeEmptyInventoryListEquip())
	}

	resp, err := h.identity.GetInventory(ctx, &identityv1.GetInventoryRequest{
		AccountId: conn.AccountID,
		CharId:    conn.CharID,
	})
	if err != nil {
		// Cancellation is expected under load — the conn is gone and
		// SendPacket would either fail or panic on a closed writer.
		if clientGone := errors.Is(err, context.Canceled) || ctx.Err() != nil; clientGone {
			h.logger.Debug().
				Err(err).
				Uint64("conn", conn.ID).
				Msg("identity GetInventory cancelled (client gone)")
			return concat(packet.EncodeEmptyInventoryListNormal(), packet.EncodeEmptyInventoryListEquip())
		}
		st, _ := status.FromError(err)
		h.logger.Warn().
			Err(err).
			Uint64("conn", conn.ID).
			Uint32("aid", conn.AccountID).
			Uint32("cid", conn.CharID).
			Str("grpc_code", st.Code().String()).
			Msg("identity GetInventory RPC failed; emitting empty inventory lists")
		return concat(packet.EncodeEmptyInventoryListNormal(), packet.EncodeEmptyInventoryListEquip())
	}
	if resp == nil {
		h.logger.Warn().
			Uint64("conn", conn.ID).
			Uint32("aid", conn.AccountID).
			Uint32("cid", conn.CharID).
			Msg("identity returned nil GetInventory response; emitting empty inventory lists")
		return concat(packet.EncodeEmptyInventoryListNormal(), packet.EncodeEmptyInventoryListEquip())
	}

	items := resp.GetItems()
	normal := make([]packet.InventoryNormalItem, 0, len(items))
	equip := make([]packet.InventoryEquipItem, 0, len(items))
	for _, it := range items {
		if it == nil {
			continue
		}
		// Saturate uint32 → uint16 for the wire ITID slot; values
		// above 65535 are sentinel-only and don't need a real
		// truncation path — clamp to 0 so a malformed row visibly
		// fails rather than silently wraps.
		nameid := it.GetNameid()
		var nameidWire uint16
		if nameid <= 0xffff {
			nameidWire = uint16(nameid) //nolint:gosec // guarded by the > 0xffff check above
		}
		// TODO(P2A-WEIGHT): replace zero total weight with the sum
		// of per-item weights once the inventory table carries them.
		// TODO(P2A-WEIGHT): replace the hardcoded `weight=0` with the
		// character's current inventory weight, and surface a separate
		// max-weight computation (base 2000 + STR*30 in rAthena).
		if it.GetEquip() == 0 {
			normal = append(normal, packet.InventoryNormalItem{
				Index: uint16(it.GetId()), //nolint:gosec // inventory index fits in uint16 (MAX_INVENTORY=100)
				ITID:  nameidWire,
				Type:  uint8(it.GetAttribute() & 0xff), //nolint:gosec // IT_* type byte is 8-bit; values >255 saturate to 0
				Count: uint16(it.GetAmount()),          //nolint:gosec // stack count is 16-bit on the wire
				Flag:  boolToIdentifiedBit(it.GetIdentify()),
			})
		} else {
			equip = append(equip, packet.InventoryEquipItem{
				Index:            uint16(it.GetId()), //nolint:gosec // ditto
				ITID:             nameidWire,
				Type:             uint8(it.GetAttribute() & 0xff), //nolint:gosec // ditto
				Location:         it.GetEquip(),
				RefiningLevel:    uint8(it.GetRefine() & 0xff), //nolint:gosec // refine is 8-bit on the wire
				ItemSpriteNumber: 0,                            // TODO(P2A): item_db.view; surface via InventoryItem once available
				Flag:             boolToEquipIdentifiedBit(it.GetIdentify()),
			})
		}
	}

	var buf bytes.Buffer
	normalResp := packet.InventoryListNormalResponse{Items: normal}
	if err := normalResp.Encode(&buf); err != nil {
		// Encode errors only fire on >0xffff total length, which
		// cannot happen for a 26-byte per-item layout unless the
		// player has more than ~2500 items. Treat as a programming
		// error and fall back to empty lists so the handshake
		// completes.
		h.logger.Error().
			Err(err).
			Uint64("conn", conn.ID).
			Int("items", len(normal)).
			Msg("encode ZC_INVENTORY_ITEMLIST_NORMAL failed; emitting empty list")
		buf.Reset()
		_, _ = buf.Write(packet.EncodeEmptyInventoryListNormal())
	}
	equipResp := packet.InventoryListEquipResponse{Items: equip}
	if err := equipResp.Encode(&buf); err != nil {
		h.logger.Error().
			Err(err).
			Uint64("conn", conn.ID).
			Int("items", len(equip)).
			Msg("encode ZC_INVENTORY_ITEMLIST_EQUIP failed; emitting empty list")
		// Drop the trailing partial equip frame; the previous normal
		// frame is already in buf and the client tolerates a missing
		// equip list when the normal list is present.
		// TODO(P2A-WEIGHT): on rollback we lose the normal list too;
		// future work could keep the prefix and reset only after.
		buf.Reset()
		_, _ = buf.Write(packet.EncodeEmptyInventoryListNormal())
		_, _ = buf.Write(packet.EncodeEmptyInventoryListEquip())
	}

	h.logger.Info().
		Uint64("conn", conn.ID).
		Uint32("aid", conn.AccountID).
		Uint32("cid", conn.CharID).
		Int("normal_items", len(normal)).
		Int("equip_items", len(equip)).
		Msg("inventory lists sent")

	return buf.Bytes()
}

// concat is a tiny []byte concatenator. The two empty-list stubs are
// always 4 bytes each, so an explicit loop saves an import on
// bytes.NewBuffer for callers that don't need one.
func concat(parts ...[]byte) []byte {
	total := 0
	for _, p := range parts {
		total += len(p)
	}
	out := make([]byte, 0, total)
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// boolToIdentifiedBit maps an InventoryItem.identify (1=identified)
// to the NORMALITEM_INFO post-20120925 Flag byte (bit 0 = IsIdentified).
func boolToIdentifiedBit(identified uint32) uint8 {
	if identified == 0 {
		return 0
	}
	return 0x01
}

// boolToEquipIdentifiedBit maps an InventoryItem.identify to the
// EQUIPITEM_INFO post-20120925 Flag byte (bit 0 = IsIdentified,
// bit 1 = IsDamaged, bit 2 = PlaceETCTab).
func boolToEquipIdentifiedBit(identified uint32) uint8 {
	if identified == 0 {
		return 0
	}
	return 0x01
}

// handleCZUseItem responds to CZ_USE_ITEM2 (0x0439) — the client
// requesting to use a consumable item. The handler forwards the
// inventory index to identity.UseItem and emits the
// ZC_USE_ITEM_ACK2 (0x01c8) ack the client expects.
//
// Wire failures (identity gRPC error, missing account_id on a
// not-yet-entered connection, identity-side success=false) are
// logged and surfaced to the client via the ack's `result` byte
// — the client must see a ZC_USE_ITEM_ACK2 to update its UI; a
// silent drop leaves the item in the "being used" state.
func (h *DispatchHandler) handleCZUseItem(ctx context.Context, conn *domain.ConnectionInfo, resp domain.Responder, frame []byte) error {
	req, err := packet.ParseCZUseItem(frame)
	if err != nil {
		h.logger.Warn().
			Err(err).
			Uint64("conn", conn.ID).
			Int("frame_len", len(frame)).
			Msg("malformed CZ_USE_ITEM2; dropping packet")
		return nil
	}

	if conn.AccountID == 0 {
		h.logger.Warn().
			Uint64("conn", conn.ID).
			Uint16("inv_index", req.Index).
			Msg("CZ_USE_ITEM2 without prior CZ_ENTER; dropping")
		return nil
	}

	if h.identity == nil {
		// DI misconfiguration — surface a failure ack so the client
		// exits the "using item" state rather than hangs.
		h.logger.Error().
			Uint64("conn", conn.ID).
			Msg("identity client not configured; rejecting CZ_USE_ITEM2")
		return h.sendUseItemAck(resp, conn.AccountID, req.Index, 0, 0, false)
	}

	rpcCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	gResp, err := h.identity.UseItem(rpcCtx, &identityv1.UseItemRequest{
		AccountId: conn.AccountID,
		CharId:    conn.CharID,
		ItemId:    uint32(req.Index), //nolint:gosec // wire index is 16-bit; proto field is uint32
	})
	if err != nil {
		if clientGone := errors.Is(err, context.Canceled) || ctx.Err() != nil; clientGone {
			h.logger.Debug().
				Err(err).
				Uint64("conn", conn.ID).
				Uint16("inv_index", req.Index).
				Msg("identity call cancelled (client gone)")
			return nil
		}
		st, _ := status.FromError(err)
		h.logger.Warn().
			Err(err).
			Uint64("conn", conn.ID).
			Uint32("aid", conn.AccountID).
			Uint16("inv_index", req.Index).
			Str("grpc_code", st.Code().String()).
			Msg("identity UseItem RPC failed; sending fail ack")
		return h.sendUseItemAck(resp, conn.AccountID, req.Index, 0, 0, false)
	}
	if gResp == nil {
		h.logger.Warn().
			Uint64("conn", conn.ID).
			Uint32("aid", conn.AccountID).
			Uint16("inv_index", req.Index).
			Msg("identity returned nil UseItem response; sending fail ack")
		return h.sendUseItemAck(resp, conn.AccountID, req.Index, 0, 0, false)
	}

	// Surface success=false as a failure ack (result=0); the
	// identity service populates `error` with the reason.
	ok := gResp.GetSuccess()
	var itemID uint16
	if nameid := gResp.GetItemId(); nameid <= 0xffff {
		itemID = uint16(nameid) //nolint:gosec // ITID slot is 16-bit on the wire for PACKETVER 20250604
	}
	remaining := uint16(gResp.GetRemainingAmount()) //nolint:gosec // amount slot is 16-bit on the wire
	if !ok {
		h.logger.Info().
			Uint64("conn", conn.ID).
			Uint32("aid", conn.AccountID).
			Uint16("inv_index", req.Index).
			Str("error", gResp.GetError()).
			Msg("identity rejected UseItem")
	}
	return h.sendUseItemAck(resp, conn.AccountID, req.Index, itemID, remaining, ok)
}

// sendUseItemAck encodes ZC_USE_ITEM_ACK2 and writes it. Always
// returns nil for encode errors (the buffer is small and the wire
// layout is fixed — an encode failure indicates a programmer error
// we want surfaced via SendPacket).
func (h *DispatchHandler) sendUseItemAck(resp domain.Responder, aid uint32, invIndex uint16, itemID uint16, remaining uint16, ok bool) error {
	var resultByte uint8
	if ok {
		resultByte = 1
	}
	ack := packet.UseItemAck2Response{
		Index:  invIndex + 2, // clif.cpp:4482: server row index + 2 for the wire
		ItemID: itemID,
		AID:    aid,
		Amount: remaining,
		Result: resultByte,
	}
	var buf bytes.Buffer
	if err := ack.Encode(&buf); err != nil {
		h.logger.Error().
			Err(err).
			Uint32("aid", aid).
			Msg("encode ZC_USE_ITEM_ACK2 failed; dropping ack")
		return nil
	}
	if err := resp.SendPacket(buf.Bytes()); err != nil {
		return fmt.Errorf("send ZC_USE_ITEM_ACK2: %w", err)
	}
	return nil
}

// handleCZReqWearEquip responds to CZ_REQ_WEAR_EQUIP_V5 (0x0998) —
// the client requesting to equip an item. The handler forwards
// (inventory index, EQP_* position) to identity.EquipItem and
// emits the ZC_REQ_WEAR_EQUIP_ACK_V5 (0x0999) ack.
//
// Wire failures (identity gRPC error, missing account_id, identity
// success=false) are logged and surfaced to the client via the
// ack's `result` byte. The client must see a ZC_REQ_WEAR_EQUIP_ACK
// to update its inventory UI; a silent drop leaves the item in
// the "being equipped" state.
func (h *DispatchHandler) handleCZReqWearEquip(ctx context.Context, conn *domain.ConnectionInfo, resp domain.Responder, frame []byte) error {
	req, err := packet.ParseCZReqWearEquip(frame)
	if err != nil {
		h.logger.Warn().
			Err(err).
			Uint64("conn", conn.ID).
			Int("frame_len", len(frame)).
			Msg("malformed CZ_REQ_WEAR_EQUIP_V5; dropping packet")
		return nil
	}

	if conn.AccountID == 0 {
		h.logger.Warn().
			Uint64("conn", conn.ID).
			Uint16("inv_index", req.Index).
			Msg("CZ_REQ_WEAR_EQUIP_V5 without prior CZ_ENTER; dropping")
		return nil
	}

	if h.identity == nil {
		h.logger.Error().
			Uint64("conn", conn.ID).
			Msg("identity client not configured; rejecting CZ_REQ_WEAR_EQUIP_V5")
		return h.sendWearEquipAck(resp, req.Index, req.Position, 0, 0)
	}

	rpcCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	gResp, err := h.identity.EquipItem(rpcCtx, &identityv1.EquipItemRequest{
		AccountId:     conn.AccountID,
		CharId:        conn.CharID,
		ItemId:        uint32(req.Index), //nolint:gosec // wire index is 16-bit; proto field is uint32
		EquipPosition: req.Position,
	})
	if err != nil {
		if clientGone := errors.Is(err, context.Canceled) || ctx.Err() != nil; clientGone {
			h.logger.Debug().
				Err(err).
				Uint64("conn", conn.ID).
				Uint16("inv_index", req.Index).
				Msg("identity call cancelled (client gone)")
			return nil
		}
		st, _ := status.FromError(err)
		h.logger.Warn().
			Err(err).
			Uint64("conn", conn.ID).
			Uint32("aid", conn.AccountID).
			Uint16("inv_index", req.Index).
			Str("grpc_code", st.Code().String()).
			Msg("identity EquipItem RPC failed; sending fail ack")
		return h.sendWearEquipAck(resp, req.Index, req.Position, 0, 0)
	}
	if gResp == nil {
		h.logger.Warn().
			Uint64("conn", conn.ID).
			Uint32("aid", conn.AccountID).
			Uint16("inv_index", req.Index).
			Msg("identity returned nil EquipItem response; sending fail ack")
		return h.sendWearEquipAck(resp, req.Index, req.Position, 0, 0)
	}

	// Map identity success bool to the rAthena result byte: 0=fail,
	// 1=ok, 2=low-level fail (clif.cpp:4306-4309). The identity
	// service does not surface the "low-level" reason in the proto
	// today, so we collapse fail/success to {0, 1}.
	var resultByte uint8
	if gResp.GetSuccess() {
		resultByte = 1
	} else {
		resultByte = 0
		h.logger.Info().
			Uint64("conn", conn.ID).
			Uint32("aid", conn.AccountID).
			Uint16("inv_index", req.Index).
			Str("error", gResp.GetError()).
			Msg("identity rejected EquipItem")
	}
	return h.sendWearEquipAck(resp, req.Index, gResp.GetEquipPosition(), 0, resultByte)
}

// sendWearEquipAck encodes ZC_REQ_WEAR_EQUIP_ACK_V5 and writes it.
func (h *DispatchHandler) sendWearEquipAck(resp domain.Responder, invIndex uint16, wearLocation uint32, sprite uint16, result uint8) error {
	ack := packet.ReqWearEquipAckResponse{
		Index:            invIndex + 2, // client-side index = server row + 2
		WearLocation:     wearLocation,
		ItemSpriteNumber: sprite,
		Result:           result,
	}
	var buf bytes.Buffer
	if err := ack.Encode(&buf); err != nil {
		h.logger.Error().
			Err(err).
			Uint16("inv_index", invIndex).
			Msg("encode ZC_REQ_WEAR_EQUIP_ACK_V5 failed; dropping ack")
		return nil
	}
	if err := resp.SendPacket(buf.Bytes()); err != nil {
		return fmt.Errorf("send ZC_REQ_WEAR_EQUIP_ACK_V5: %w", err)
	}
	return nil
}

// handleCZReqTakeoffEquip responds to CZ_REQ_TAKEOFF_EQUIP (0x00ab) —
// the client requesting to unequip an item. The handler forwards
// the inventory index to identity.UnequipItem and emits the
// ZC_REQ_TAKEOFF_EQUIP_ACK (0x099a) ack.
//
// Wire failures (identity gRPC error, missing account_id, identity
// success=false) are logged and surfaced to the client via the
// ack's `flag` byte. For PACKETVER >= 20110824 the flag is
// inverted on the wire (clif.cpp:4338): flag=0 means success.
func (h *DispatchHandler) handleCZReqTakeoffEquip(ctx context.Context, conn *domain.ConnectionInfo, resp domain.Responder, frame []byte) error {
	req, err := packet.ParseCZReqTakeoffEquip(frame)
	if err != nil {
		h.logger.Warn().
			Err(err).
			Uint64("conn", conn.ID).
			Int("frame_len", len(frame)).
			Msg("malformed CZ_REQ_TAKEOFF_EQUIP; dropping packet")
		return nil
	}

	if conn.AccountID == 0 {
		h.logger.Warn().
			Uint64("conn", conn.ID).
			Uint16("inv_index", req.Index).
			Msg("CZ_REQ_TAKEOFF_EQUIP without prior CZ_ENTER; dropping")
		return nil
	}

	if h.identity == nil {
		h.logger.Error().
			Uint64("conn", conn.ID).
			Msg("identity client not configured; rejecting CZ_REQ_TAKEOFF_EQUIP")
		return h.sendTakeoffEquipAck(resp, req.Index, 0, 1) // flag=1 = failure (inverted)
	}

	rpcCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	gResp, err := h.identity.UnequipItem(rpcCtx, &identityv1.UnequipItemRequest{
		AccountId: conn.AccountID,
		CharId:    conn.CharID,
		ItemId:    uint32(req.Index), //nolint:gosec // wire index is 16-bit; proto field is uint32
	})
	if err != nil {
		if clientGone := errors.Is(err, context.Canceled) || ctx.Err() != nil; clientGone {
			h.logger.Debug().
				Err(err).
				Uint64("conn", conn.ID).
				Uint16("inv_index", req.Index).
				Msg("identity call cancelled (client gone)")
			return nil
		}
		st, _ := status.FromError(err)
		h.logger.Warn().
			Err(err).
			Uint64("conn", conn.ID).
			Uint32("aid", conn.AccountID).
			Uint16("inv_index", req.Index).
			Str("grpc_code", st.Code().String()).
			Msg("identity UnequipItem RPC failed; sending fail ack")
		return h.sendTakeoffEquipAck(resp, req.Index, 0, 1)
	}
	if gResp == nil {
		h.logger.Warn().
			Uint64("conn", conn.ID).
			Uint32("aid", conn.AccountID).
			Uint16("inv_index", req.Index).
			Msg("identity returned nil UnequipItem response; sending fail ack")
		return h.sendTakeoffEquipAck(resp, req.Index, 0, 1)
	}

	// flag is wire-inverted for PACKETVER >= 20110824: 0=success, 1=failure.
	var flag uint8
	if gResp.GetSuccess() {
		flag = 0
	} else {
		flag = 1
		h.logger.Info().
			Uint64("conn", conn.ID).
			Uint32("aid", conn.AccountID).
			Uint16("inv_index", req.Index).
			Str("error", gResp.GetError()).
			Msg("identity rejected UnequipItem")
	}
	// WearLocation: the unequip path derives the EQP_* position from
	// the row's previous `equip` column; the proto does not yet
	// return the prior location. Pass 0 — the client's UI does not
	// require a specific value on the success path (it just clears
	// the equip slot on its own).
	return h.sendTakeoffEquipAck(resp, req.Index, 0, flag)
}

// sendTakeoffEquipAck encodes ZC_REQ_TAKEOFF_EQUIP_ACK and writes it.
func (h *DispatchHandler) sendTakeoffEquipAck(resp domain.Responder, invIndex uint16, wearLocation uint32, flag uint8) error {
	ack := packet.ReqTakeoffEquipAckResponse{
		Index:        invIndex + 2, // client-side index = server row + 2
		WearLocation: wearLocation,
		Flag:         flag,
	}
	var buf bytes.Buffer
	if err := ack.Encode(&buf); err != nil {
		h.logger.Error().
			Err(err).
			Uint16("inv_index", invIndex).
			Msg("encode ZC_REQ_TAKEOFF_EQUIP_ACK failed; dropping ack")
		return nil
	}
	if err := resp.SendPacket(buf.Bytes()); err != nil {
		return fmt.Errorf("send ZC_REQ_TAKEOFF_EQUIP_ACK: %w", err)
	}
	return nil
}

// handleCZRequestTime responds to CZ_REQUEST_TIME (0x007e) — the
// client's periodic server-tick ping. rAthena responds with
// clif_notify_time(sd, gettick()); we return unix millis (low 32
// bits) as a stateless equivalent.
func (h *DispatchHandler) handleCZRequestTime(_ context.Context, conn *domain.ConnectionInfo, resp domain.Responder, frame []byte) error {
	req, err := packet.ParseCZRequestTime(frame)
	if err != nil {
		h.logger.Warn().
			Err(err).
			Uint64("conn", conn.ID).
			Int("frame_len", len(frame)).
			Msg("malformed CZ_REQUEST_TIME; dropping packet")
		return nil
	}
	notify := packet.NotifyTimeResponse{
		Time: uint32(time.Now().UnixMilli()), //nolint:gosec // low 32 bits of unix millis per rAthena time convention
	}
	var buf bytes.Buffer
	if err := notify.Encode(&buf); err != nil {
		// NotifyTimeResponse.Encode cannot fail in practice (no
		// variable-width fields), but we still bubble the error up
		// rather than silently swallow it — wrapcheck requires every
		// external error be wrapped. Drop the packet rather than
		// refuse the handshake.
		h.logger.Error().
			Err(err).
			Uint64("conn", conn.ID).
			Msg("encode ZC_NOTIFY_TIME failed")
		return nil
	}
	h.logger.Debug().
		Uint64("conn", conn.ID).
		Uint32("client_tick", req.ClientTick).
		Msg("time sync")
	if err := resp.SendPacket(buf.Bytes()); err != nil {
		return fmt.Errorf("send ZC_NOTIFY_TIME: %w", err)
	}
	return nil
}

// handleCZGlobalMessage responds to CZ_GLOBAL_MESSAGE (0x008c) — the
// client's public chat send. rAthena's clif_parse_GlobalMessage
// (rathena/src/map/clif.cpp:11509) prepends "<name> : " to the message
// and broadcasts ZC_NOTIFY_CHAT to nearby clients; the gateway has no
// AOI and no entity registry yet, so we echo the raw text back to the
// sender with the connection's authenticated AID substituted as the
// GID slot. The AID-as-GID stand-in is intentional and documented in
// decision-log.md — there is no zone-resident GID before the zone
// service returns one, and dropping chat for an in-progress map enter
// is a worse user experience than a stable but technically-wrong GID.
//
// Malformed frames are logged and dropped (rAthena treats a truncated
// chat packet the same way — the client retries after re-reading its
// addressbook).
func (h *DispatchHandler) handleCZGlobalMessage(_ context.Context, conn *domain.ConnectionInfo, resp domain.Responder, frame []byte) error {
	req, err := packet.ParseCZGlobalMessage(frame)
	if err != nil {
		h.logger.Warn().
			Err(err).
			Uint64("conn", conn.ID).
			Int("frame_len", len(frame)).
			Msg("malformed CZ_GLOBAL_MESSAGE; dropping packet")
		return nil
	}

	if conn.AccountID == 0 {
		// CZ_GLOBAL_MESSAGE without a preceding CZ_ENTER: the client
		// has not authenticated against the zone yet, so we have no
		// entity to attribute the chat to. Drop silently rather than
		// panic on a zero AID in the GID slot — see handleCZRequestMove
		// for the analogous guard.
		h.logger.Warn().
			Uint64("conn", conn.ID).
			Int("frame_len", len(frame)).
			Msg("CZ_GLOBAL_MESSAGE without prior CZ_ENTER; dropping")
		return nil
	}

	notify := packet.NotifyChatResponse{
		GID:     conn.AccountID,
		Message: req.Message,
	}

	var buf bytes.Buffer
	if err := notify.Encode(&buf); err != nil {
		// NotifyChatResponse.Encode cannot fail in practice (the
		// message is length-checked at parse time and the NUL
		// terminator is unconditionally appended), but wrapcheck
		// requires every external error be wrapped — log and drop.
		h.logger.Error().
			Err(err).
			Uint64("conn", conn.ID).
			Uint32("aid", conn.AccountID).
			Msg("encode ZC_NOTIFY_CHAT failed; dropping packet")
		return nil
	}

	h.logger.Debug().
		Uint64("conn", conn.ID).
		Uint32("aid", conn.AccountID).
		Str("message", req.Message).
		Msg("chat echo")

	if err := resp.SendPacket(buf.Bytes()); err != nil {
		return fmt.Errorf("send ZC_NOTIFY_CHAT: %w", err)
	}
	return nil
}

// handleCZActionRequest responds to CZ_ACTION_REQUEST (0x0089) — the
// client's sit/stand/attack selector. The on-wire action byte mapping
// follows rAthena's e_damage_type enum (rathena/src/map/clif.hpp:691-707):
//
//	0 → attack (DMG_NORMAL)   — combat: damage monster, send ZC_NOTIFY_ACT
//	1 → pickup item            — dropped (no item system yet)
//	2 → sit down (DMG_SIT_DOWN) — echo as ZC_NOTIFY_ACT with type=2
//	3 → stand up (DMG_STAND_UP) — echo as ZC_NOTIFY_ACT with type=3
//	7 → continuous attack      — same as action 0
//
// rAthena's clif_sitting / clif_standing (clif.cpp:5327-5358) broadcast
// ZC_NOTIFY_ACT (0x08c8) with type=DMG_SIT_DOWN / DMG_STAND_UP — NOT the
// compact 0x008b stub. M18 corrects the M11 mapping (which wrongly used
// 0/1 for sit/stand) and adds the attack path.
func (h *DispatchHandler) handleCZActionRequest(_ context.Context, conn *domain.ConnectionInfo, resp domain.Responder, frame []byte) error {
	req, err := packet.ParseCZActionRequest(frame)
	if err != nil {
		h.logger.Warn().
			Err(err).
			Uint64("conn", conn.ID).
			Int("frame_len", len(frame)).
			Msg("malformed CZ_ACTION_REQUEST; dropping packet")
		return nil
	}

	if conn.AccountID == 0 {
		h.logger.Warn().
			Uint64("conn", conn.ID).
			Uint8("action", req.Action).
			Msg("CZ_ACTION_REQUEST without prior CZ_ENTER; dropping")
		return nil
	}

	switch req.Action {
	case packet.DMGNormal, packet.DMGRepeat:
		return h.handleAttack(conn, resp, req.TargetGID)
	case packet.DMGSitDown, packet.DMGStandUp:
		return h.handleSitStand(conn, resp, req.Action)
	default:
		// action 1 (pickup item), 4-6, 8-14 — out of scope.
		h.logger.Debug().
			Uint64("conn", conn.ID).
			Uint32("aid", conn.AccountID).
			Uint8("action", req.Action).
			Msg("CZ_ACTION_REQUEST ignored (out of scope)")
		return nil
	}
}

// handleSitStand sends a ZC_NOTIFY_ACT packet for sit/stand actions.
// rAthena's clif_sitting / clif_standing use ZC_NOTIFY_ACT (0x08c8)
// with type=DMG_SIT_DOWN (2) or DMG_STAND_UP (3) and all other fields
// zeroed (no damage, no target).
func (h *DispatchHandler) handleSitStand(conn *domain.ConnectionInfo, resp domain.Responder, action uint8) error {
	act := packet.NotifyActResponse{
		SrcID: conn.AccountID,
		Type:  action,
	}
	var buf bytes.Buffer
	if err := act.Encode(&buf); err != nil {
		h.logger.Error().
			Err(err).
			Uint64("conn", conn.ID).
			Uint8("action", action).
			Msg("encode ZC_NOTIFY_ACT (sit/stand) failed")
		return nil
	}
	h.logger.Debug().
		Uint64("conn", conn.ID).
		Uint32("aid", conn.AccountID).
		Uint8("action", action).
		Msg("sit/stand echo")
	if err := resp.SendPacket(buf.Bytes()); err != nil {
		return fmt.Errorf("send ZC_NOTIFY_ACT (sit/stand): %w", err)
	}
	return nil
}

// handleAttack processes a melee attack on a monster. It applies a
// fixed damage value (10), sends ZC_NOTIFY_ACT with the damage, and
// if the monster's HP drops to 0 or below, sends ZC_NOTIFY_VANISH
// and removes the monster from the per-connection HP map.
func (h *DispatchHandler) handleAttack(conn *domain.ConnectionInfo, resp domain.Responder, targetGID uint32) error {
	// Fixed damage — no stat-based formula yet (deferred to zone service).
	const fixedDamage int32 = 10

	hp, ok := conn.ApplyDamage(targetGID, fixedDamage)
	if !ok {
		// Target is not a known monster (could be NPC, PC, or already
		// dead). Drop silently — no error, no reply.
		h.logger.Debug().
			Uint64("conn", conn.ID).
			Uint32("target_gid", targetGID).
			Msg("attack on unknown/dead target; dropping")
		return nil
	}

	dead := hp <= 0
	if dead {
		conn.RemoveMonster(targetGID)
	}

	tick := uint32(time.Now().UnixMilli()) //nolint:gosec // low 32 bits per rAthena time convention

	var burst bytes.Buffer

	// ZC_NOTIFY_ACT — damage notification.
	act := packet.NotifyActResponse{
		SrcID:      conn.AccountID,
		TargetID:   targetGID,
		ServerTick: tick,
		SrcSpeed:   0, // amotion — deferred
		DmgSpeed:   0, // dmotion — deferred
		Damage:     fixedDamage,
		Div:        1,
		Type:       packet.DMGNormal,
	}
	if err := act.Encode(&burst); err != nil {
		h.logger.Error().
			Err(err).
			Uint64("conn", conn.ID).
			Msg("encode ZC_NOTIFY_ACT (attack) failed")
		return nil
	}

	if dead {
		// Monster died — send ZC_NOTIFY_VANISH and remove from HP map.
		vanish := packet.NotifyVanishResponse{
			GID:  targetGID,
			Type: packet.VanishDead,
		}
		if err := vanish.Encode(&burst); err != nil {
			h.logger.Error().
				Err(err).
				Uint64("conn", conn.ID).
				Msg("encode ZC_NOTIFY_VANISH failed")
			return nil
		}

		// M19: apply EXP
		h.applyMonsterKillExp(conn, &burst, targetGID)

		// M20: schedule respawn
		h.scheduleMonsterRespawn(conn, resp, targetGID)

		h.logger.Info().
			Uint64("conn", conn.ID).
			Uint32("target_gid", targetGID).
			Int32("damage", fixedDamage).
			Msg("monster killed")
	} else {
		h.logger.Debug().
			Uint64("conn", conn.ID).
			Uint32("target_gid", targetGID).
			Int32("damage", fixedDamage).
			Int32("remaining_hp", hp).
			Msg("monster damaged")
	}

	if err := resp.SendPacket(burst.Bytes()); err != nil {
		return fmt.Errorf("send attack burst: %w", err)
	}
	return nil
}

// applyMonsterKillExp looks up the dead monster's EXP values, accumulates
// them in the connection state, and appends ZC_LONGPAR_CHANGE updates
// to the response burst.
func (h *DispatchHandler) applyMonsterKillExp(conn *domain.ConnectionInfo, burst *bytes.Buffer, targetGID uint32) {
	for _, m := range monsterSpawns {
		if m.GID == targetGID {
			conn.AddExp(m.BaseExp, m.JobExp)
			baseExp, jobExp := conn.ExpValues()

			baseExpUpdate := packet.LongParChangeResponse{
				VarID:  packet.SPBaseExp,
				Amount: baseExp,
			}
			if err := baseExpUpdate.Encode(burst); err != nil {
				h.logger.Error().Err(err).Msg("encode SPBaseExp failed")
			}

			jobExpUpdate := packet.LongParChangeResponse{
				VarID:  packet.SPJobExp,
				Amount: jobExp,
			}
			if err := jobExpUpdate.Encode(burst); err != nil {
				h.logger.Error().Err(err).Msg("encode SPJobExp failed")
			}
			break
		}
	}
}

// scheduleMonsterRespawn schedules the respawn of a killed monster.
func (h *DispatchHandler) scheduleMonsterRespawn(conn *domain.ConnectionInfo, resp domain.Responder, targetGID uint32) {
	// Pending timers hold conn/resp references until they fire; for this single-player echo path this is acceptable — full timer lifecycle is zone-service scope.
	time.AfterFunc(h.respawnDelay, func() {
		var mob *monsterSpawn
		for i := range monsterSpawns {
			if monsterSpawns[i].GID == targetGID {
				mob = &monsterSpawns[i]
				break
			}
		}
		if mob == nil {
			return
		}

		conn.RespawnMonster(targetGID, mob.MaxHP)

		idle := packet.SetUnitIdleResponse{
			ObjectType: 0x05, // NPC_MOB_TYPE
			AID:        mob.GID,
			GID:        0,
			Speed:      mob.Speed,
			Job:        mob.SpriteID,
			MaxHP:      mob.MaxHP,
			HP:         mob.HP,
			PosX:       mob.X,
			PosY:       mob.Y,
			Dir:        mob.Dir,
			Name:       mob.Name,
			CLevel:     mob.Level,
		}

		var buf bytes.Buffer
		if err := idle.Encode(&buf); err != nil {
			h.logger.Error().
				Err(err).
				Uint64("conn", conn.ID).
				Str("mob_name", mob.Name).
				Msg("encode monster ZC_SET_UNIT_IDLE failed on respawn")
			return
		}

		if err := resp.SendPacket(buf.Bytes()); err != nil {
			h.logger.Debug().
				Err(err).
				Uint64("conn", conn.ID).
				Uint32("target_gid", targetGID).
				Msg("send monster ZC_SET_UNIT_IDLE failed on respawn (client disconnected)")
		}
	})
}

// handleCZChangeDir responds to CZ_CHANGE_DIRECTION (0x009b) — the
// client notifying the server of its new body/head direction. rAthena
// calls pc_setdir + clif_changed_dir(*sd, AREA_WOS) at clif.cpp:11615-11617;
// the gateway has no AOI yet, so we echo the same values back to the
// sender with the AID stamped in the srcId slot. When the zone service
// takes over per-entity state (M14+) the dispatcher will forward this
// to a zone Direction RPC and broadcast the response on the AOI ring.
func (h *DispatchHandler) handleCZChangeDir(_ context.Context, conn *domain.ConnectionInfo, resp domain.Responder, frame []byte) error {
	req, err := packet.ParseCZChangeDir(frame)
	if err != nil {
		h.logger.Warn().
			Err(err).
			Uint64("conn", conn.ID).
			Int("frame_len", len(frame)).
			Msg("malformed CZ_CHANGE_DIRECTION; dropping packet")
		return nil
	}

	if conn.AccountID == 0 {
		// CZ_CHANGE_DIRECTION without a preceding CZ_ENTER: the
		// client has not authenticated against the zone yet, so we
		// have no entity to attribute the direction to. Drop
		// silently — see handleCZRequestMove for the analogous guard.
		h.logger.Warn().
			Uint64("conn", conn.ID).
			Uint16("head_dir", req.HeadDir).
			Uint8("dir", req.Dir).
			Msg("CZ_CHANGE_DIRECTION without prior CZ_ENTER; dropping")
		return nil
	}

	resp2 := packet.ChangeDirResponse{
		SrcID:   conn.AccountID, // AID-as-srcID stand-in — see handleCZGlobalMessage.
		HeadDir: req.HeadDir,
		Dir:     req.Dir,
	}

	var buf bytes.Buffer
	if err := resp2.Encode(&buf); err != nil {
		// ChangeDirResponse.Encode cannot fail in practice (no
		// variable-width fields), but wrapcheck requires every
		// external error be wrapped — log and drop.
		h.logger.Error().
			Err(err).
			Uint64("conn", conn.ID).
			Uint32("aid", conn.AccountID).
			Msg("encode ZC_CHANGE_DIRECTION failed; dropping packet")
		return nil
	}

	h.logger.Debug().
		Uint64("conn", conn.ID).
		Uint32("aid", conn.AccountID).
		Uint16("head_dir", resp2.HeadDir).
		Uint8("dir", resp2.Dir).
		Msg("direction echo")

	if err := resp.SendPacket(buf.Bytes()); err != nil {
		return fmt.Errorf("send ZC_CHANGE_DIRECTION: %w", err)
	}
	return nil
}

// handleCZReqEmotion responds to CZ_REQ_EMOTION (0x00bf) — the client
// requesting to display an emotion icon (smile, cry, sweat, …). rAthena
// runs a basic-skill check + flood throttle + ET_MAX guard at
// clif_parse_Emotion (clif.cpp:11636-11667) before calling
// clif_emotion(*sd, emoticon) which broadcasts ZC_EMOTION to AREA at
// clif.cpp:9417. The gateway's M12 echo path skips the rAthena-side
// gates (no basic-skill system, no per-connection emotion clock) and
// forwards the byte verbatim to the sender with the AID stamped in
// the GID slot. When the zone service takes over per-entity state
// (M14+) the dispatcher will forward this to a zone Emotion RPC and
// broadcast on the AOI ring.
func (h *DispatchHandler) handleCZReqEmotion(_ context.Context, conn *domain.ConnectionInfo, resp domain.Responder, frame []byte) error {
	req, err := packet.ParseCZReqEmotion(frame)
	if err != nil {
		h.logger.Warn().
			Err(err).
			Uint64("conn", conn.ID).
			Int("frame_len", len(frame)).
			Msg("malformed CZ_REQ_EMOTION; dropping packet")
		return nil
	}

	if conn.AccountID == 0 {
		// CZ_REQ_EMOTION without a preceding CZ_ENTER: the client
		// has not authenticated against the zone yet, so we have no
		// entity to attribute the emotion to. Drop silently — see
		// handleCZRequestMove for the analogous guard.
		h.logger.Warn().
			Uint64("conn", conn.ID).
			Uint8("emotion_type", req.EmotionType).
			Msg("CZ_REQ_EMOTION without prior CZ_ENTER; dropping")
		return nil
	}

	resp2 := packet.EmotionResponse{
		GID:  conn.AccountID, // AID-as-GID stand-in — see handleCZGlobalMessage.
		Type: req.EmotionType,
	}

	var buf bytes.Buffer
	if err := resp2.Encode(&buf); err != nil {
		// EmotionResponse.Encode cannot fail in practice (no
		// variable-width fields), but wrapcheck requires every
		// external error be wrapped — log and drop.
		h.logger.Error().
			Err(err).
			Uint64("conn", conn.ID).
			Uint32("aid", conn.AccountID).
			Msg("encode ZC_EMOTION failed; dropping packet")
		return nil
	}

	h.logger.Debug().
		Uint64("conn", conn.ID).
		Uint32("aid", conn.AccountID).
		Uint8("emotion_type", resp2.Type).
		Msg("emotion echo")

	if err := resp.SendPacket(buf.Bytes()); err != nil {
		return fmt.Errorf("send ZC_EMOTION: %w", err)
	}
	return nil
}

// handleCZGetCharNameRequest responds to CZ_GETCHARNAMEREQUEST (0x0094) —
// the client requesting a character name by GID. rAthena's
// clif_parse_GetCharNameRequest (rathena/src/map/clif.cpp:11469-11503)
// resolves the GID via map_id2bl and calls clif_name(sd, bl, SELF) to
// send the full ZC_ACK_REQNAMEALL response. The gateway has no entity
// registry yet, so we respond with the character name only when the
// requested GID matches the player's own CharID; for any other GID we
// respond with an empty name (the client handles this gracefully by
// showing "Unknown" or the GID itself).
//
// The response uses the compact ZC_ACK_REQNAME (0x0095) format:
// [2:cmd][4:GID int32][24:name char[24]] = 30 bytes. This is the
// pre-20180207 NPC name response shape that carries only the GID and
// name — no party/guild/position fields. The client accepts this for
// player name lookups on Thai Classic PACKETVER 20250604.
func (h *DispatchHandler) handleCZGetCharNameRequest(_ context.Context, conn *domain.ConnectionInfo, resp domain.Responder, frame []byte) error {
	req, err := packet.ParseCZGetCharNameRequest(frame)
	if err != nil {
		h.logger.Warn().
			Err(err).
			Uint64("conn", conn.ID).
			Int("frame_len", len(frame)).
			Msg("malformed CZ_GETCHARNAMEREQUEST; dropping packet")
		return nil
	}

	if conn.AccountID == 0 {
		h.logger.Warn().
			Uint64("conn", conn.ID).
			Uint32("req_gid", req.GID).
			Msg("CZ_GETCHARNAMEREQUEST without prior CZ_ENTER; dropping")
		return nil
	}

	name := ""
	if req.GID == conn.CharID {
		char, err := h.fetchCharacterByConn(conn)
		if err != nil {
			h.logger.Warn().
				Err(err).
				Uint64("conn", conn.ID).
				Uint32("char_id", conn.CharID).
				Msg("failed to fetch character for name request")
		} else if char != nil {
			name = char.GetName()
		}
	}

	ack := packet.AckReqNameResponse{
		GID:  req.GID,
		Name: name,
	}

	var buf bytes.Buffer
	if err := ack.Encode(&buf); err != nil {
		h.logger.Error().
			Err(err).
			Uint64("conn", conn.ID).
			Uint32("req_gid", req.GID).
			Msg("encode ZC_ACK_REQNAME failed; dropping packet")
		return nil
	}

	h.logger.Debug().
		Uint64("conn", conn.ID).
		Uint32("req_gid", req.GID).
		Uint32("char_id", conn.CharID).
		Str("name", name).
		Msg("name request")

	if err := resp.SendPacket(buf.Bytes()); err != nil {
		return fmt.Errorf("send ZC_ACK_REQNAME: %w", err)
	}
	return nil
}

// handleCZRestart responds to CZ_RESTART (0x00b2) — the client
// requesting either a respawn (type=0) or a return to the character
// select screen (type=1). rAthena's clif_parse_Restart
// (rathena/src/map/clif.cpp:11837-11854) branches on the type byte:
// 0x00 calls pc_respawn, 0x01 calls chrif_charselectreq (which sends
// the client back to the char server).
//
// The gateway does not yet implement the connection state machine
// required for a char-select transition (that requires tearing down
// the zone session and re-entering the char-select handshake), so both
// types are logged and dropped. The client will retry or the player
// can disconnect manually.
func (h *DispatchHandler) handleCZRestart(_ context.Context, conn *domain.ConnectionInfo, resp domain.Responder, frame []byte) error {
	req, err := packet.ParseCZRestart(frame)
	if err != nil {
		h.logger.Warn().
			Err(err).
			Uint64("conn", conn.ID).
			Int("frame_len", len(frame)).
			Msg("malformed CZ_RESTART; dropping packet")
		return nil
	}

	if conn.AccountID == 0 {
		h.logger.Warn().
			Uint64("conn", conn.ID).
			Uint8("type", req.Type).
			Msg("CZ_RESTART without prior CZ_ENTER; dropping")
		return nil
	}

	switch req.Type {
	case 0x00:
		h.logger.Info().
			Uint64("conn", conn.ID).
			Uint32("aid", conn.AccountID).
			Msg("CZ_RESTART respawn requested (deferred)")
	case 0x01:
		h.logger.Info().
			Uint64("conn", conn.ID).
			Uint32("aid", conn.AccountID).
			Msg("CZ_RESTART char select requested (deferred)")
	default:
		h.logger.Debug().
			Uint64("conn", conn.ID).
			Uint8("type", req.Type).
			Msg("CZ_RESTART unknown type; dropping")
	}
	return nil
}

// handleCZContactNPC responds to CZ_CONTACTNPC (0x0090) — the client
// clicking on an NPC. The handler looks up the NPC in the hardcoded
// npcSpawns slice by GID and branches on the NPC's ShopType:
//
//   - ShopType == 1 (shop NPC): send ZC_SELECT_DEALTYPE so the client
//     pops up the Buy / Sell / Cancel deal-type selector. Sell is
//     deferred (M16 only supports Buy); Cancel is a no-op response.
//   - ShopType == 0 (dialog NPC): send the M15 dialog sequence
//     (ZC_SAY_DIALOG2 + ZC_WAIT_DIALOG2).
//
// If the NPC GID is not found in npcSpawns, the handler logs a warning
// and returns nil (no response). The full script engine (DEL-04) is
// deferred to Phase 3; M15/M16 provide hardcoded dialog text and a
// hardcoded stock list for each NPC.
func (h *DispatchHandler) handleCZContactNPC(_ context.Context, conn *domain.ConnectionInfo, resp domain.Responder, frame []byte) error {
	req, err := packet.ParseCZContactNPC(frame)
	if err != nil {
		h.logger.Warn().
			Err(err).
			Uint64("conn", conn.ID).
			Int("frame_len", len(frame)).
			Msg("malformed CZ_CONTACTNPC; dropping packet")
		return nil
	}

	if conn.AccountID == 0 {
		h.logger.Warn().
			Uint64("conn", conn.ID).
			Uint32("npc_aid", req.AID).
			Msg("CZ_CONTACTNPC without prior CZ_ENTER; dropping")
		return nil
	}

	var npc *npcSpawn
	for i := range npcSpawns {
		if npcSpawns[i].GID == req.AID {
			npc = &npcSpawns[i]
			break
		}
	}
	if npc == nil {
		h.logger.Warn().
			Uint64("conn", conn.ID).
			Uint32("npc_aid", req.AID).
			Msg("CZ_CONTACTNPC for unknown NPC GID; dropping")
		return nil
	}

	if npc.ShopType == 1 {
		// Shop NPC — open the deal-type window.
		selectDt := packet.SelectDealtypeResponse{NpcID: req.AID}
		var buf bytes.Buffer
		if err := selectDt.Encode(&buf); err != nil {
			h.logger.Error().
				Err(err).
				Uint64("conn", conn.ID).
				Uint32("npc_aid", req.AID).
				Str("npc_name", npc.Name).
				Msg("encode ZC_SELECT_DEALTYPE failed; dropping packet")
			return nil
		}
		h.logger.Info().
			Uint64("conn", conn.ID).
			Uint32("npc_aid", req.AID).
			Str("npc_name", npc.Name).
			Int("shop_items", len(npc.ShopItems)).
			Msg("NPC shop opened")
		if err := resp.SendPacket(buf.Bytes()); err != nil {
			return fmt.Errorf("send ZC_SELECT_DEALTYPE: %w", err)
		}
		return nil
	}

	// Dialog NPC — M15 welcome sequence.
	say := packet.SayDialog2Response{
		NpcID:   req.AID,
		Type:    0,
		Message: "Welcome to goAthena! This is " + npc.Name + ".",
	}
	var sayBuf bytes.Buffer
	if err := say.Encode(&sayBuf); err != nil {
		h.logger.Error().
			Err(err).
			Uint64("conn", conn.ID).
			Uint32("npc_aid", req.AID).
			Msg("encode ZC_SAY_DIALOG2 failed; dropping packet")
		return nil
	}

	wait := packet.WaitDialog2Response{
		NpcID: req.AID,
		Type:  0,
	}
	var waitBuf bytes.Buffer
	if err := wait.Encode(&waitBuf); err != nil {
		h.logger.Error().
			Err(err).
			Uint64("conn", conn.ID).
			Uint32("npc_aid", req.AID).
			Msg("encode ZC_WAIT_DIALOG2 failed; dropping packet")
		return nil
	}

	h.logger.Info().
		Uint64("conn", conn.ID).
		Uint32("npc_aid", req.AID).
		Str("npc_name", npc.Name).
		Msg("NPC dialog started")

	if err := resp.SendPacket(sayBuf.Bytes()); err != nil {
		return fmt.Errorf("send ZC_SAY_DIALOG2: %w", err)
	}
	if err := resp.SendPacket(waitBuf.Bytes()); err != nil {
		return fmt.Errorf("send ZC_WAIT_DIALOG2: %w", err)
	}
	return nil
}

// handleCZAckSelectDealType responds to CZ_ACK_SELECT_DEALTYPE (0x00c5)
// — the client picking Buy / Sell / Cancel in the deal-type window
// opened by ZC_SELECT_DEALTYPE. The handler dispatches on the type
// byte:
//
//	0x00 = Buy  → ZC_PC_PURCHASE_ITEMLIST (the NPC's stock)
//	0x01 = Sell → logged, no response (sell flow deferred)
//	0x02 = Cancel → logged, no response
//
// Unknown type bytes are treated like Cancel (logged + dropped) so a
// client sending a malformed selector does not break the connection.
// NPCs that are not in npcSpawns (e.g. the client typed a bogus
// NpcID) are also dropped without response.
func (h *DispatchHandler) handleCZAckSelectDealType(_ context.Context, conn *domain.ConnectionInfo, resp domain.Responder, frame []byte) error {
	req, err := packet.ParseCZAckSelectDealType(frame)
	if err != nil {
		h.logger.Warn().
			Err(err).
			Uint64("conn", conn.ID).
			Int("frame_len", len(frame)).
			Msg("malformed CZ_ACK_SELECT_DEALTYPE; dropping packet")
		return nil
	}

	if conn.AccountID == 0 {
		h.logger.Warn().
			Uint64("conn", conn.ID).
			Uint32("npc_id", req.NpcID).
			Msg("CZ_ACK_SELECT_DEALTYPE without prior CZ_ENTER; dropping")
		return nil
	}

	var npc *npcSpawn
	for i := range npcSpawns {
		if npcSpawns[i].GID == req.NpcID {
			npc = &npcSpawns[i]
			break
		}
	}
	if npc == nil {
		h.logger.Warn().
			Uint64("conn", conn.ID).
			Uint32("npc_id", req.NpcID).
			Msg("CZ_ACK_SELECT_DEALTYPE for unknown NPC GID; dropping")
		return nil
	}

	switch req.Type {
	case 0x00:
		// Buy — send the NPC's stock list.
		items := make([]packet.ShopBuyItem, len(npc.ShopItems))
		for i, it := range npc.ShopItems {
			items[i] = packet.ShopBuyItem(it)
		}
		list := packet.PurchaseItemListResponse{Items: items}
		var buf bytes.Buffer
		if err := list.Encode(&buf); err != nil {
			h.logger.Error().
				Err(err).
				Uint64("conn", conn.ID).
				Uint32("npc_id", req.NpcID).
				Str("npc_name", npc.Name).
				Int("shop_items", len(items)).
				Msg("encode ZC_PC_PURCHASE_ITEMLIST failed; dropping packet")
			return nil
		}
		h.logger.Info().
			Uint64("conn", conn.ID).
			Uint32("npc_id", req.NpcID).
			Str("npc_name", npc.Name).
			Int("shop_items", len(items)).
			Msg("NPC shop buy list sent")
		if err := resp.SendPacket(buf.Bytes()); err != nil {
			return fmt.Errorf("send ZC_PC_PURCHASE_ITEMLIST: %w", err)
		}
		return nil
	case 0x01:
		// Sell — deferred until the inventory system lands.
		h.logger.Debug().
			Uint64("conn", conn.ID).
			Uint32("npc_id", req.NpcID).
			Str("npc_name", npc.Name).
			Msg("NPC shop sell requested (deferred)")
		return nil
	case 0x02:
		h.logger.Debug().
			Uint64("conn", conn.ID).
			Uint32("npc_id", req.NpcID).
			Str("npc_name", npc.Name).
			Msg("NPC shop deal cancelled by client")
		return nil
	default:
		h.logger.Debug().
			Uint64("conn", conn.ID).
			Uint32("npc_id", req.NpcID).
			Uint8("type", req.Type).
			Msg("CZ_ACK_SELECT_DEALTYPE unknown type; dropping")
		return nil
	}
}

// handleCZPCPurchaseItemList responds to CZ_PC_PURCHASE_ITEMLIST
// (0x00c8) — the player's purchase request. The handler logs the
// entries and replies with ZC_PC_PURCHASE_RESULT carrying result=0
// (success) so the client closes the buy window cleanly. Actual zeny
// deduction and inventory mutation are deferred until the inventory
// system lands; the current path is a no-op-on-success acknowledgement
// used to keep the M16 happy-path exercisable end-to-end.
func (h *DispatchHandler) handleCZPCPurchaseItemList(_ context.Context, conn *domain.ConnectionInfo, resp domain.Responder, frame []byte) error {
	req, err := packet.ParseCZPCPurchaseItemList(frame)
	if err != nil {
		h.logger.Warn().
			Err(err).
			Uint64("conn", conn.ID).
			Int("frame_len", len(frame)).
			Msg("malformed CZ_PC_PURCHASE_ITEMLIST; dropping packet")
		return nil
	}

	if conn.AccountID == 0 {
		h.logger.Warn().
			Uint64("conn", conn.ID).
			Int("entries", len(req.Entries)).
			Msg("CZ_PC_PURCHASE_ITEMLIST without prior CZ_ENTER; dropping")
		return nil
	}

	h.logger.Info().
		Uint64("conn", conn.ID).
		Uint32("aid", conn.AccountID).
		Int("entries", len(req.Entries)).
		Msg("NPC shop purchase requested (zeny/inventory deferred; result=success)")

	result := packet.PurchaseResultResponse{Result: 0}
	var buf bytes.Buffer
	if err := result.Encode(&buf); err != nil {
		// PurchaseResultResponse.Encode cannot fail in practice (no
		// variable-width fields), but wrapcheck requires every
		// external error be wrapped — log and drop.
		h.logger.Error().
			Err(err).
			Uint64("conn", conn.ID).
			Msg("encode ZC_PC_PURCHASE_RESULT failed; dropping packet")
		return nil
	}
	if err := resp.SendPacket(buf.Bytes()); err != nil {
		return fmt.Errorf("send ZC_PC_PURCHASE_RESULT: %w", err)
	}
	return nil
}

// handleCZReqNextScript responds to CZ_REQNEXTSCRIPT (0x00b9) — the
// client clicking "Next" in the NPC dialog. The handler sends the
// second part of the dialog:
//
//  1. ZC_SAY_DIALOG2 with a continuation message
//  2. ZC_CLOSE_DIALOG to show the "Close" button
//
// The full script engine (DEL-04) is deferred to Phase 3; M15 provides
// a single hardcoded continuation message for every NPC.
func (h *DispatchHandler) handleCZReqNextScript(_ context.Context, conn *domain.ConnectionInfo, resp domain.Responder, frame []byte) error {
	req, err := packet.ParseCZReqNextScript(frame)
	if err != nil {
		h.logger.Warn().
			Err(err).
			Uint64("conn", conn.ID).
			Int("frame_len", len(frame)).
			Msg("malformed CZ_REQNEXTSCRIPT; dropping packet")
		return nil
	}

	if conn.AccountID == 0 {
		h.logger.Warn().
			Uint64("conn", conn.ID).
			Uint32("npc_id", req.NpcID).
			Msg("CZ_REQNEXTSCRIPT without prior CZ_ENTER; dropping")
		return nil
	}

	say := packet.SayDialog2Response{
		NpcID:   req.NpcID,
		Type:    0,
		Message: "The server is under development. Enjoy exploring!",
	}
	var sayBuf bytes.Buffer
	if err := say.Encode(&sayBuf); err != nil {
		h.logger.Error().
			Err(err).
			Uint64("conn", conn.ID).
			Uint32("npc_id", req.NpcID).
			Msg("encode ZC_SAY_DIALOG2 failed; dropping packet")
		return nil
	}

	closeD := packet.CloseDialogResponse{NpcID: req.NpcID} //nolint:staticcheck // explicit struct init keeps the two wire structs decoupled; a Go conversion would silently break if CloseDialogResponse ever gains a field
	var closeBuf bytes.Buffer
	if err := closeD.Encode(&closeBuf); err != nil {
		h.logger.Error().
			Err(err).
			Uint64("conn", conn.ID).
			Uint32("npc_id", req.NpcID).
			Msg("encode ZC_CLOSE_DIALOG failed; dropping packet")
		return nil
	}

	h.logger.Info().
		Uint64("conn", conn.ID).
		Uint32("npc_id", req.NpcID).
		Msg("NPC dialog continued")

	if err := resp.SendPacket(sayBuf.Bytes()); err != nil {
		return fmt.Errorf("send ZC_SAY_DIALOG2: %w", err)
	}
	if err := resp.SendPacket(closeBuf.Bytes()); err != nil {
		return fmt.Errorf("send ZC_CLOSE_DIALOG: %w", err)
	}
	return nil
}

// handleCZCloseDialog responds to CZ_CLOSE_DIALOG (0x0146) — the
// client clicking "Close" in the NPC dialog. The client closes the
// dialog window locally; the server does not need to send a response.
// The handler logs the close event and returns nil.
func (h *DispatchHandler) handleCZCloseDialog(_ context.Context, conn *domain.ConnectionInfo, resp domain.Responder, frame []byte) error {
	req, err := packet.ParseCZCloseDialog(frame)
	if err != nil {
		h.logger.Warn().
			Err(err).
			Uint64("conn", conn.ID).
			Int("frame_len", len(frame)).
			Msg("malformed CZ_CLOSE_DIALOG; dropping packet")
		return nil
	}

	if conn.AccountID == 0 {
		h.logger.Warn().
			Uint64("conn", conn.ID).
			Uint32("npc_gid", req.GID).
			Msg("CZ_CLOSE_DIALOG without prior CZ_ENTER; dropping")
		return nil
	}

	h.logger.Info().
		Uint64("conn", conn.ID).
		Uint32("npc_gid", req.GID).
		Msg("NPC dialog closed")

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

// sendMapRefuse encodes a ZC_REFUSE_ENTER frame (cmd 0x0074, 3 bytes)
// and sends it. The map-server refuse code book is intentionally
// minimal in rAthena — most refusals land on error code 0
// (rAthena's map_authfail default) because the failure reasons
// (session expired, zone down, slot full) are surfaced through the
// client UI's reconnect prompt rather than a fine-grained code. We
// always refuse with 0; callers that want more diagnostic detail log
// the reason before invoking.
func sendMapRefuse(resp domain.Responder) error {
	refuse := packet.MapRefuseEnterResponse{Error: 0}
	var buf bytes.Buffer
	if err := refuse.Encode(&buf); err != nil {
		return fmt.Errorf("encode ZC_REFUSE_ENTER: %w", err)
	}
	if err := resp.SendPacket(buf.Bytes()); err != nil {
		return fmt.Errorf("send ZC_REFUSE_ENTER: %w", err)
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

// clampMapCoord saturates a uint32 map coordinate from the zone
// service into the int16 wire slot. RO maps are at most ~512 cells;
// values above 1000 indicate a zone-service bug and are clamped to
// 1000 rather than silently wrapping negative via an unchecked int16
// cast.
func clampMapCoord(v uint32) int16 {
	if v > 1000 {
		return 1000
	}
	return int16(v) //nolint:gosec // guarded by the > 1000 check above
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

// resolveZoneIPv4 converts the gateway.map_addr host portion into the
// network-byte-order uint32 the HC_NOTIFY_ZONESVR IP slot needs.
//
// Accepts both IP literals ("127.0.0.1") and hostnames ("localhost");
// the latter is resolved via the system DNS resolver and the first
// returned IPv4 wins. IPv4-mapped IPv6 addresses are normalized to the
// embedded IPv4 (same convention as parseIPv4). An unresolvable host —
// including the bare hostname "localhost" on a system that has no
// loopback entry — returns a wrapped error so the DI layer can fail
// fast at startup rather than silently advertising 0.0.0.0.
//
// Called once at construction time; not per-packet.
func resolveZoneIPv4(host string) (uint32, error) {
	if host == "" {
		return 0, fmt.Errorf("resolve zone IPv4: host is empty")
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		addr = addr.Unmap()
		if addr.Is4() {
			b := addr.As4()
			return binary.BigEndian.Uint32(b[:]), nil
		}
		return 0, fmt.Errorf("resolve zone IPv4 %q: not an IPv4 address", host)
	}
	addrs, err := net.LookupHost(host)
	if err != nil {
		return 0, fmt.Errorf("resolve zone IPv4 %q: %w", host, err)
	}
	for _, a := range addrs {
		addr, perr := netip.ParseAddr(a)
		if perr != nil {
			continue
		}
		addr = addr.Unmap()
		if addr.Is4() {
			b := addr.As4()
			return binary.BigEndian.Uint32(b[:]), nil
		}
	}
	return 0, fmt.Errorf("resolve zone IPv4 %q: no A records returned", host)
}

// ResolveZoneIPv4 is the exported alias of resolveZoneIPv4 for the DI
// layer; see resolveZoneIPv4 for the semantics.
func ResolveZoneIPv4(host string) (uint32, error) {
	return resolveZoneIPv4(host)
}
