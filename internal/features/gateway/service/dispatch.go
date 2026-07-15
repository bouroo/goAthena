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
//   - CZ_ACK_SELECT_DEALTYPE (0x00c5) → ZC_PC_PURCHASE_ITEMLIST (Buy) / ZC_PC_SELL_ITEMLIST (Sell) / logged (Cancel)
//   - CZ_PC_PURCHASE_ITEMLIST (0x00c8) → ZC_PC_PURCHASE_RESULT (success/insufficient) + zeny LongParChange on OK
//   - CZ_PC_SELL_ITEMLIST (0x00c9)    → ZC_PC_SELL_RESULT (success/fail) + zeny LongParChange on OK
//   - everything else              → debug-logged, connection kept alive.

package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
	"google.golang.org/grpc/status"

	identityv1 "github.com/bouroo/goAthena/api/pb/identity/v1"
	zonev1 "github.com/bouroo/goAthena/api/pb/zone/v1"
	"github.com/bouroo/goAthena/internal/features/gateway/domain"
	"github.com/bouroo/goAthena/internal/features/script/vm"
	skilldomain "github.com/bouroo/goAthena/internal/features/skill/domain"
	statsdomain "github.com/bouroo/goAthena/internal/features/stats/domain"
	"github.com/bouroo/goAthena/pkg/ro/mobdb"
	"github.com/bouroo/goAthena/pkg/ro/packet"
	scriptpkg "github.com/bouroo/goAthena/pkg/ro/script"
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

	// damageRoll is the damage calculation RNG. Defaulted to a
	// uniform distribution in the range [low, high], overrideable by
	// tests for determinism.
	damageRoll func(low, high int32) int32

	// mobRegistry resolves a monster's mobdb.MobEntry by mob_db ID.
	// Used by lookupMobEntry to fetch authoritative Def/Vit values
	// (falling back to monsterSpawns when nil) and by appendMonsterDeath
	// to roll drop tables. May be nil; the handler treats a nil
	// registry as "mob_db not loaded" and degrades gracefully (no
	// drops, use struct Def/Vit).
	mobRegistry *mobdb.Registry

	// groundItemCounter is the per-handler monotonic counter used to
	// assign ground item object IDs in ZC_ITEM_FALL_ENTRY. rAthena
	// uses a global counter but a per-handler value is sufficient for
	// the single-player echo path (mobs and items do not cross
	// connections).
	groundItemCounter atomic.Uint32

	// dropRoll decides whether a single drop entry wins its roll.
	// Defaulted to rand.IntN(10000) < rate (rAthena mob.cpp item_drop
	// rate is on a 0..10000 scale). Overrideable for deterministic
	// tests.
	dropRoll func(rate int) bool

	// scriptSet is the snapshot of compiled NPC scripts supplied by
	// the gateway DI provider. The dispatcher resolves a clicked
	// NPC's ScriptName against scriptSet.Scripts to drive the
	// per-connection DialogSession VM. May be nil (test fixtures
	// that don't exercise the script path, or a gateway booted
	// without cfg.Zone.ScriptDir); handleCZContactNPC falls back to
	// the hardcoded M15 dialog when nil or when a name misses.
	scriptSet *scriptpkg.CompiledScriptSet

	// dialogSessions tracks the active per-connection dialog VM
	// sessions. Keyed by conn.ID. Stored on the handler (not on
	// ConnectionInfo) to avoid a domain→service import cycle:
	// domain cannot depend on *service.DialogSession, and the
	// dispatcher is the only consumer.
	dialogSessions sync.Map
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
	mobs *mobdb.Registry,
	scriptSet *scriptpkg.CompiledScriptSet,
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
		mobRegistry:  mobs,
		scriptSet:    scriptSet,
		respawnDelay: 5 * time.Second,
		damageRoll: func(low, high int32) int32 {
			if low >= high {
				return low
			}
			return low + rand.Int32N(high-low+1) //nolint:gosec // math/rand/v2 is sufficient for damage RNG
		},
		dropRoll: func(rate int) bool {
			if rate <= 0 {
				return false
			}
			if rate >= 10000 {
				return true
			}
			return rand.IntN(10000) < rate //nolint:gosec // math/rand/v2 is sufficient for drop RNG
		},
	}
}

// setDamageRoll installs a custom damage RNG for deterministic tests.
//
//nolint:unused // only used in tests
func (h *DispatchHandler) setDamageRoll(f func(low, high int32) int32) {
	h.damageRoll = f
}

// setDropRoll installs a custom drop-roll RNG for deterministic tests.
//
//nolint:unused // only used in tests
func (h *DispatchHandler) setDropRoll(f func(rate int) bool) {
	h.dropRoll = f
}

// lookupMobEntry resolves a GID → mobdb.MobEntry via the registry,
// falling back to nil when the registry is empty or the GID has no
// matching spawn. Callers should treat a nil return as "use struct
// Def/Vit" so the pre-mob_db combat path still works.
func (h *DispatchHandler) lookupMobEntry(gid uint32) *mobdb.MobEntry {
	if h.mobRegistry == nil {
		return nil
	}
	spawn, ok := spawnByGID(gid)
	if !ok || spawn.MobID == 0 {
		return nil
	}
	return h.mobRegistry.Get(spawn.MobID)
}

// defVitFor returns Def/Vit for a monster GID, preferring the mob_db
// entry when present and falling back to the hardcoded monsterSpawns
// struct otherwise. Returns 0,0 when the GID is unknown.
func (h *DispatchHandler) defVitFor(gid uint32) (int, int) {
	if mob := h.lookupMobEntry(gid); mob != nil {
		return int(mob.Defense), int(mob.Vit) //nolint:gosec // Defense/Vit are small positive values
	}
	def, vit, ok := LookupMonsterStats(gid)
	if !ok {
		return 0, 0
	}
	return def, vit
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
	case packet.HeaderCZACTIONREQUEST, packet.HeaderCZUSESKILL:
		return h.handleMapPacketCombat(ctx, conn, resp, cmd, frame)
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
	case packet.HeaderCZSTATUSCHANGE:
		return h.handleCZStatusChange(ctx, conn, resp, frame)
	default:
		return h.handleMapPacketNPC(ctx, conn, resp, cmd, frame)
	}
}

// handleMapPacketCombat dispatches the combat sub-group (CZ_ACTION_REQUEST
// and CZ_USE_SKILL2). Extracted from handleMapPacket so the top-level
// switch stays under the gocyclo limit (the table grew past 15 cases in
// P3b-2 when CZ_USE_SKILL2 was added).
func (h *DispatchHandler) handleMapPacketCombat(ctx context.Context, conn *domain.ConnectionInfo, resp domain.Responder, cmd uint16, frame []byte) error {
	switch cmd {
	case packet.HeaderCZACTIONREQUEST:
		return h.handleCZActionRequest(ctx, conn, resp, frame)
	case packet.HeaderCZUSESKILL:
		return h.handleCZUseSkill(ctx, conn, resp, frame)
	default:
		h.logger.Debug().
			Uint64("conn", conn.ID).
			Uint16("cmd", cmd).
			Msg("unhandled combat packet")
		return nil
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
	case packet.HeaderCZCHOOSEMENU:
		return h.handleCZChooseMenu(ctx, conn, resp, frame)
	case packet.HeaderCZCLOSEDIALOG:
		return h.handleCZCloseDialog(ctx, conn, resp, frame)
	case packet.HeaderCZACKSELECTDEALTYPE:
		return h.handleCZAckSelectDealType(ctx, conn, resp, frame)
	case packet.HeaderCZPCPURCHASEITEMLIST:
		return h.handleCZPCPurchaseItemList(ctx, conn, resp, frame)
	case packet.HeaderCZPCSELLITEMLIST:
		return h.handleCZPCSellItemList(ctx, conn, resp, frame)
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
		conn.BaseLevel = baseLevel
		conn.SetCombatStats(strV, dexV, lukV)
		conn.SetSP(sp, maxSP)
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
	_, _ = burst.Write(h.encodeSkillList(conn))
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
	if h.identity == nil {
		h.logger.Error().
			Uint64("conn", conn.ID).
			Msg("identity client not configured; emitting empty inventory lists")
		return concat(packet.EncodeEmptyInventoryListNormal(), packet.EncodeEmptyInventoryListEquip())
	}
	if conn.AccountID == 0 || conn.CharID == 0 {
		return concat(packet.EncodeEmptyInventoryListNormal(), packet.EncodeEmptyInventoryListEquip())
	}

	resp, err := h.identity.GetInventory(ctx, &identityv1.GetInventoryRequest{
		AccountId: conn.AccountID,
		CharId:    conn.CharID,
	})
	if err != nil {
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
	invIndexMap := make(map[uint16]uint32, len(items))
	var pos uint16
	for _, it := range items {
		if it == nil {
			continue
		}
		invIndexMap[pos] = it.GetId()

		nameid := it.GetNameid()
		var nameidWire uint16
		if nameid <= 0xffff {
			nameidWire = uint16(nameid)
		}
		if it.GetEquip() == 0 {
			normal = append(normal, packet.InventoryNormalItem{
				Index: pos,
				ITID:  nameidWire,
				Type:  uint8(it.GetAttribute() & 0xff),
				Count: uint16(it.GetAmount() & 0xffff), //nolint:gosec // amount slot is 16-bit on the wire
				Flag:  boolToIdentifiedBit(it.GetIdentify()),
			})
		} else {
			equip = append(equip, packet.InventoryEquipItem{
				Index:            pos,
				ITID:             nameidWire,
				Type:             uint8(it.GetAttribute() & 0xff),
				Location:         it.GetEquip(),
				RefiningLevel:    uint8(it.GetRefine() & 0xff),
				ItemSpriteNumber: 0,
				Flag:             boolToEquipIdentifiedBit(it.GetIdentify()),
			})
		}
		pos++
	}
	conn.SetInventoryIndex(invIndexMap)

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

// encodeSkillList builds the ZC_SKILLINFO_LIST (0x010f) frame sent
// during the map-enter status burst (LoadEndAck). The character’s
// learned skill set is sourced from a deterministic novice default
// here — a persisted per-character `skill` table is deferred to
// sub-PR 3b-2, so we cannot query identity for it yet. The default
// (NV_BASIC L1) matches every fresh character in rAthena’s
// pre-re skill_db.
func (h *DispatchHandler) encodeSkillList(conn *domain.ConnectionInfo) []byte {
	learned := []skilldomain.LearnedSkill{{ID: 1, Level: 1}}
	entries := skilldomain.BuildSkillList(learned)
	if len(entries) == 0 {
		// NV_BASIC (id=1) is always registered; this is a
		// defensive fallback for an unlikely registry regression.
		h.logger.Error().
			Uint64("conn", conn.ID).
			Msg("skill list resolved empty; emitting empty ZC_SKILLINFO_LIST")
		return packet.EncodeEmptySkillList()
	}

	data := make([]packet.SkillData, len(entries))
	for i, e := range entries {
		data[i] = packet.SkillData{
			ID:     e.ID,
			Inf:    uint32(e.Inf),
			Level:  e.Level,
			SP:     e.SP,
			Range2: e.Range2,
			Name:   e.Name,
			UpFlag: e.UpFlag,
		}
	}

	resp := packet.SkillInfoListResponse{Skills: data}
	var buf bytes.Buffer
	if err := resp.Encode(&buf); err != nil {
		h.logger.Error().
			Err(err).
			Uint64("conn", conn.ID).
			Int("skills", len(data)).
			Msg("encode ZC_SKILLINFO_LIST failed; emitting empty list")
		return packet.EncodeEmptySkillList()
	}
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

	if req.Index < 2 {
		h.logger.Warn().Uint64("conn", conn.ID).Uint16("inv_index", req.Index).Msg("invalid inventory index")
		return h.sendUseItemAck(resp, conn.AccountID, req.Index, 0, 0, false)
	}
	pos := req.Index - 2
	itemID, ok := conn.ResolveInventoryID(pos)
	if !ok {
		h.logger.Warn().Uint64("conn", conn.ID).Uint16("inv_index", req.Index).Msg("unknown inventory position")
		return h.sendUseItemAck(resp, conn.AccountID, req.Index, 0, 0, false)
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
		ItemId:    itemID,
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
	ok = gResp.GetSuccess()
	var itemIDWire uint16
	if nameid := gResp.GetItemId(); nameid <= 0xffff {
		itemIDWire = uint16(nameid) //nolint:gosec // ITID slot is 16-bit on the wire for PACKETVER 20250604
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
	return h.sendUseItemAck(resp, conn.AccountID, req.Index, itemIDWire, remaining, ok)
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

	if req.Index < 2 {
		h.logger.Warn().Uint64("conn", conn.ID).Uint16("inv_index", req.Index).Msg("invalid inventory index")
		return h.sendWearEquipAck(resp, req.Index, req.Position, 0, 0)
	}
	pos := req.Index - 2
	itemID, ok := conn.ResolveInventoryID(pos)
	if !ok {
		h.logger.Warn().Uint64("conn", conn.ID).Uint16("inv_index", req.Index).Msg("unknown inventory position")
		return h.sendWearEquipAck(resp, req.Index, req.Position, 0, 0)
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
		ItemId:        itemID,
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

	if req.Index < 2 {
		h.logger.Warn().Uint64("conn", conn.ID).Uint16("inv_index", req.Index).Msg("invalid inventory index")
		return h.sendTakeoffEquipAck(resp, req.Index, 0, 1)
	}
	pos := req.Index - 2
	itemID, ok := conn.ResolveInventoryID(pos)
	if !ok {
		h.logger.Warn().Uint64("conn", conn.ID).Uint16("inv_index", req.Index).Msg("unknown inventory position")
		return h.sendTakeoffEquipAck(resp, req.Index, 0, 1)
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
		ItemId:    itemID,
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
	return h.sendTakeoffEquipAck(resp, req.Index, gResp.GetEquipPosition(), flag)
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

	msgUTF8, err := conn.Codepage.Decode([]byte(req.Message))
	if err != nil {
		h.logger.Warn().
			Err(err).
			Uint64("conn", conn.ID).
			Uint32("aid", conn.AccountID).
			Int("msg_len", len(req.Message)).
			Msg("transcode CZ_GLOBAL_MESSAGE inbound failed; dropping packet")
		return nil
	}

	// Echo uses the raw sender-codepage bytes from req.Message directly.
	// Re-encoding msgUTF8 to the same codepage is redundant and risks a
	// round-trip mismatch; the Decode above is kept solely to validate the
	// inbound payload is transcodable and to expose msgUTF8 for the Debug
	// log below. Encode will return in Phase 12b when broadcasting to
	// other clients that may sit on a different codepage.
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
		Str("message", msgUTF8).
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
func (h *DispatchHandler) handleCZActionRequest(ctx context.Context, conn *domain.ConnectionInfo, resp domain.Responder, frame []byte) error {
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
		return h.handleAttack(ctx, conn, resp, req.TargetGID)
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
func (h *DispatchHandler) handleAttack(ctx context.Context, conn *domain.ConnectionInfo, resp domain.Responder, targetGID uint32) error {
	def, vit := h.defVitFor(targetGID)

	str, dex, luk := conn.CombatStats()
	band := statsdomain.MeleeDamage(str, dex, luk, def, vit)
	damage := h.damageRoll(band.Min, band.Max)

	hp, ok := conn.ApplyDamage(targetGID, damage)
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
		Damage:     damage,
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
		if !h.appendMonsterDeath(ctx, conn, resp, &burst, targetGID) {
			return nil
		}
		h.logger.Info().
			Uint64("conn", conn.ID).
			Uint32("target_gid", targetGID).
			Int32("damage", damage).
			Msg("monster killed")
	} else {
		h.logger.Debug().
			Uint64("conn", conn.ID).
			Uint32("target_gid", targetGID).
			Int32("damage", damage).
			Int32("remaining_hp", hp).
			Msg("monster damaged")
	}

	if err := resp.SendPacket(burst.Bytes()); err != nil {
		return fmt.Errorf("send attack burst: %w", err)
	}
	return nil
}

// appendMonsterDeath encodes ZC_NOTIFY_VANISH into burst, forwards EXP via
// identity, and schedules the respawn timer. Extracted from handleAttack so
// handleCZUseSkill can reuse the byte-identical sequence (rathena clif
// clif_clearunit + clif_exp + respawn at clif.cpp:5358-5380 / mob.cpp).
//
// Returns false if the vanish encode failed (caller must abort the burst send).
func (h *DispatchHandler) appendMonsterDeath(ctx context.Context, conn *domain.ConnectionInfo, resp domain.Responder, burst *bytes.Buffer, targetGID uint32) bool {
	vanish := packet.NotifyVanishResponse{
		GID:  targetGID,
		Type: packet.VanishDead,
	}
	if err := vanish.Encode(burst); err != nil {
		h.logger.Error().
			Err(err).
			Uint64("conn", conn.ID).
			Msg("encode ZC_NOTIFY_VANISH failed")
		return false
	}

	// P3c u4: roll mob_db drop table and emit ZC_ITEM_FALL_ENTRY for
	// each winning entry. Item pick-up is deferred to a later phase;
	// the item NameID is resolved from the AegisName via a future item
	// DB — for now we emit 0 (IT_ETC placeholder) so the client renders
	// the drop tile. The spawn's (X, Y) is the drop origin (rAthena
	// drops at the mob's position when no party-split rule applies).
	h.appendMonsterDrops(burst, targetGID)

	// M19: apply EXP
	h.applyMonsterKillExp(ctx, conn, burst, targetGID)

	// M20: schedule respawn
	h.scheduleMonsterRespawn(conn, resp, targetGID)

	return true
}

// appendMonsterDrops rolls each entry in the mob's drop table and
// appends a ZC_ITEM_FALL_ENTRY (0x0ADD) frame to burst for every
// winning roll. The drop origin is the monster's spawn point (rAthena
// drops at the mob's last known cell). No-ops when mob_db is unloaded
// or the GID has no drop table.
func (h *DispatchHandler) appendMonsterDrops(burst *bytes.Buffer, targetGID uint32) {
	mob := h.lookupMobEntry(targetGID)
	if mob == nil || len(mob.Drops) == 0 {
		return
	}
	spawn, ok := spawnByGID(targetGID)
	if !ok {
		return
	}
	dropped := 0
	for _, d := range mob.Drops {
		if !h.dropRoll(d.Rate) {
			continue
		}
		id := h.groundItemCounter.Add(1)
		drop := packet.ItemFallEntryResponse{
			ID:     id,
			NameID: 0, // item DB deferred; client renders a default sprite
			Type:   3, // IT_ETC
			// Identified=1 lets the client display the item name slot
			// without the "?" overlay. rAthena drops items already
			// identified by default (mob.cpp item_drop).
			Identified: 1,
			X:          uint16(spawn.X), //nolint:gosec // map cell coords fit in uint16
			Y:          uint16(spawn.Y), //nolint:gosec // map cell coords fit in uint16
			SubX:       0,
			SubY:       0,
			Amount:     1,
		}
		if err := drop.Encode(burst); err != nil {
			h.logger.Error().
				Err(err).
				Uint64("conn", 0).
				Uint32("target_gid", targetGID).
				Str("item", d.Item).
				Msg("encode ZC_ITEM_FALL_ENTRY failed")
			continue
		}
		dropped++
	}
	if dropped > 0 {
		h.logger.Debug().
			Uint32("target_gid", targetGID).
			Int("dropped", dropped).
			Int("rolled", len(mob.Drops)).
			Msg("monster dropped items")
	}
}

// handleCZUseSkill processes a CZ_USE_SKILL2 (0x0438) single-target skill
// request. Currently restricted to SM_BASH — the pct=100+30*level damage
// formula is Bash-specific and must not silently apply to a future
// offensive skill (Pierce, Magnum Break, …). It validates the skill
// against the static registry, verifies the target monster is tracked
// before spending SP (so a failed cast does not desync the client's SP
// display), computes the pre-Renewal Bash damage band via
// statsdomain.BashDamage scaled by 100+30*level, applies the rolled
// damage to the target monster, and emits ZC_NOTIFY_SKILL (0x01de).
// On kill it reuses the shared monster-death path (vanish + EXP +
// respawn). On insufficient SP it emits ZC_ACK_TOUSESKILL (0x0110) with
// cause USESKILL_FAIL_SP_INSUFFICIENT (12) and returns without damaging
// the target. Unknown skills, non-Bash skills, or invalid/dead targets
// are dropped silently.
//
// Mirrors the shape of handleAttack (same parameter signature, same mob
// lookup, same damage RNG) so the two paths stay symmetric; the only
// outbound differences are the ZC_NOTIFY_SKILL / ZC_ACK_TOUSESKILL
// headers instead of ZC_NOTIFY_ACT.
func (h *DispatchHandler) handleCZUseSkill(ctx context.Context, conn *domain.ConnectionInfo, resp domain.Responder, frame []byte) error {
	req, err := packet.ParseCZUseSkill(frame)
	if err != nil {
		h.logger.Warn().
			Err(err).
			Uint64("conn", conn.ID).
			Int("frame_len", len(frame)).
			Msg("malformed CZ_USE_SKILL2; dropping packet")
		return nil
	}

	if conn.AccountID == 0 {
		h.logger.Warn().
			Uint64("conn", conn.ID).
			Uint16("skill_id", req.SkillID).
			Msg("CZ_USE_SKILL2 without prior CZ_ENTER; dropping")
		return nil
	}

	if req.SkillID != skilldomain.SM_BASH {
		// Non-Bash offensive skills are out of scope for this slice:
		// the pct=100+30*level formula below is Bash-specific and
		// must not silently apply to a future registry entry
		// (Pierce, Magnum Break, ...). Drop silently — rAthena
		// ignores unhandled use-skill requests the same way
		// (clif.cpp:13010 clif_parse_UseSkillToId).
		return nil
	}

	sk, ok := skilldomain.Lookup(req.SkillID)
	if !ok || sk.Inf != skilldomain.InfAttack {
		// Defensive lookup guard: SM_BASH above is the gate that
		// matters, but keep the registry check so a misconfigured
		// registry still fails closed.
		return nil
	}

	level := clampSkillLevel(int(req.SkillLv), int(sk.MaxLevel))

	// Validate the target BEFORE spending SP. Otherwise an invalid or
	// already-dead target would silently drain SP without producing a
	// ZC_PAR_CHANGE, leaving the client's SP display out of sync with
	// the server cache.
	if !conn.HasMonster(req.TargetID) {
		h.logger.Debug().
			Uint64("conn", conn.ID).
			Uint16("skill_id", req.SkillID).
			Uint32("target_gid", req.TargetID).
			Msg("CZ_USE_SKILL2 on unknown/dead target; dropping without spending SP")
		return nil
	}

	spCost := uint32(sk.SpAt(uint8(level))) //nolint:gosec // level ≤ MaxLevel (≤255)
	remaining, ok := conn.SpendSP(spCost)
	if !ok {
		return h.sendSkillAckInsufficientSP(conn, resp, req.SkillID)
	}

	def, vit := h.defVitFor(req.TargetID)

	str, dex, luk := conn.CombatStats()
	pct := int32(100 + 30*level)                                               //nolint:gosec // level ≤ MaxLevel (≤255)
	band := statsdomain.BashDamage(str, dex, luk, int32(def), int32(vit), pct) //nolint:gosec // mob_db Def/Vit are small positive values
	dmg := h.damageRoll(band.Min, band.Max)

	hp, ok := conn.ApplyDamage(req.TargetID, dmg)
	if !ok {
		h.logger.Debug().
			Uint64("conn", conn.ID).
			Uint32("target_gid", req.TargetID).
			Msg("CZ_USE_SKILL2 on unknown/dead target; dropping")
		return nil
	}

	dead := hp <= 0
	if dead {
		conn.RemoveMonster(req.TargetID)
	}

	var burst bytes.Buffer
	if !h.appendSkillHitBurst(conn, &burst, req, dmg, level, remaining) {
		return nil
	}

	if dead {
		if !h.appendMonsterDeath(ctx, conn, resp, &burst, req.TargetID) {
			return nil
		}
		h.logger.Info().
			Uint64("conn", conn.ID).
			Uint16("skill_id", req.SkillID).
			Uint32("target_gid", req.TargetID).
			Int32("damage", dmg).
			Uint32("sp_cost", spCost).
			Uint32("sp_remaining", remaining).
			Msg("monster killed by skill")
	} else {
		h.logger.Debug().
			Uint64("conn", conn.ID).
			Uint16("skill_id", req.SkillID).
			Uint32("target_gid", req.TargetID).
			Int32("damage", dmg).
			Int32("remaining_hp", hp).
			Uint32("sp_remaining", remaining).
			Msg("monster damaged by skill")
	}

	if err := resp.SendPacket(burst.Bytes()); err != nil {
		return fmt.Errorf("send skill burst: %w", err)
	}
	return nil
}

// clampSkillLevel bounds a CZ_USE_SKILL2.SkillLv to [1, maxLevel].
// Out-of-range levels silently clamp rather than fail — matches rAthena's
// pc->skill_lv handling at clif_parse_UseSkillToId (clif.cpp:13014).
func clampSkillLevel(reqLevel, maxLevel int) int {
	if reqLevel < 1 {
		return 1
	}
	if reqLevel > maxLevel {
		return maxLevel
	}
	return reqLevel
}

// sendSkillAckInsufficientSP emits a 14-byte ZC_ACK_TOUSESKILL with
// Cause=USESKILL_FAIL_SP_INSUFFICIENT. Extracted from handleCZUseSkill
// to keep that function under the gocyclo limit.
func (h *DispatchHandler) sendSkillAckInsufficientSP(conn *domain.ConnectionInfo, resp domain.Responder, skillID uint16) error {
	ack := packet.AckUseSkillResponse{
		SkillID: skillID,
		BType:   0,
		ItemID:  0,
		Flag:    0,
		Cause:   packet.UseSkillFailSPInsufficient,
	}
	var ackBuf bytes.Buffer
	if err := ack.Encode(&ackBuf); err != nil {
		h.logger.Error().
			Err(err).
			Uint64("conn", conn.ID).
			Uint16("skill_id", skillID).
			Msg("encode ZC_ACK_TOUSESKILL failed")
		return nil
	}
	if err := resp.SendPacket(ackBuf.Bytes()); err != nil {
		return fmt.Errorf("send ZC_ACK_TOUSESKILL: %w", err)
	}
	return nil
}

// appendSkillHitBurst encodes ZC_NOTIFY_SKILL + ZC_PAR_CHANGE (SP) into
// burst. StartTime is the same low-32-bit server tick handleAttack uses
// for ZC_NOTIFY_ACT (rathena clif_parse_UseSkillToId → clif_skill_damage).
// Extracted from handleCZUseSkill to keep that function under the gocyclo
// limit. Returns true on success, false if either encode failed (caller
// must abort the burst send — matches the rest of the dispatcher's
// "log + drop" convention).
func (h *DispatchHandler) appendSkillHitBurst(conn *domain.ConnectionInfo, burst *bytes.Buffer, req packet.CZUseSkill, dmg int32, level int, spRemaining uint32) bool {
	skill := packet.NotifySkillResponse{
		SKID:       req.SkillID,
		AID:        conn.AccountID,
		TargetID:   req.TargetID,
		StartTime:  uint32(time.Now().UnixMilli()), //nolint:gosec // low 32 bits per rAthena time convention
		AttackMT:   0,
		AttackedMT: 0,
		Damage:     dmg,
		Level:      int16(level), //nolint:gosec // level ≤ MaxLevel (≤255)
		Count:      1,
		Action:     0,
	}
	if err := skill.Encode(burst); err != nil {
		h.logger.Error().
			Err(err).
			Uint64("conn", conn.ID).
			Uint16("skill_id", req.SkillID).
			Msg("encode ZC_NOTIFY_SKILL failed")
		return false
	}

	par := packet.ParChangeResponse{
		VarID: packet.SPSP,
		Count: int32(spRemaining), //nolint:gosec // remaining ≤ MaxSP (≤ int32)
	}
	if err := par.Encode(burst); err != nil {
		h.logger.Error().
			Err(err).
			Uint64("conn", conn.ID).
			Uint16("skill_id", req.SkillID).
			Msg("encode ZC_PAR_CHANGE (SP) failed")
		return false
	}
	return true
}

// applyBaseLevelUp detects whether the most recent EXP gain triggers a
// base level-up, persists it via identity.ApplyLevelUp, and emits the
// ZC_NOTIFY_EFFECT + ZC_PAR_CHANGE burst. Extracted from
// applyMonsterKillExp to keep that function's nesting under the gocyclo
// limit (D-213).
func (h *DispatchHandler) applyBaseLevelUp(ctx context.Context, conn *domain.ConnectionInfo, burst *bytes.Buffer, m *monsterSpawn, baseExp int32) {
	if m.BaseExp <= 0 || conn.BaseLevel == 0 {
		return
	}
	gain := statsdomain.ApplyBaseExpGain(conn.BaseLevel, uint64(baseExp-m.BaseExp), uint64(m.BaseExp)) //nolint:gosec // G115: EXP values are non-negative int32
	if !gain.LeveledUp {
		return
	}
	levelUpResp, lerr := h.identity.ApplyLevelUp(ctx, &identityv1.ApplyLevelUpRequest{
		AccountId:           conn.AccountID,
		CharId:              conn.CharID,
		FromBaseLevel:       conn.BaseLevel,
		ToBaseLevel:         gain.NewLevel,
		GrantedStatusPoints: gain.GrantedStatusPoints,
	})
	if lerr != nil {
		h.logger.Error().Err(lerr).Uint64("conn", conn.ID).Msg("ApplyLevelUp RPC failed")
		return
	}
	if !levelUpResp.GetSuccess() {
		return
	}
	conn.BaseLevel = gain.NewLevel
	conn.SetBaseExp(int32(gain.NewExp)) //nolint:gosec // G115: NewExp is within [0, nextThreshold), fits int32
	if err := (packet.ZCNotifyEffect{EffectID: packet.EffectBaseLevelUp}).Encode(burst); err != nil {
		h.logger.Error().Err(err).Msg("encode ZC_NOTIFY_EFFECT failed")
	}
	if err := (packet.ParChangeResponse{VarID: packet.SPBaseLevel, Count: int32(gain.NewLevel)}).Encode(burst); err != nil { //nolint:gosec // base_level <= 99
		h.logger.Error().Err(err).Msg("encode SPBaseLevel failed")
	}
	if err := (packet.ParChangeResponse{VarID: packet.SPStatusPoint, Count: int32(levelUpResp.GetNewStatusPoint())}).Encode(burst); err != nil { //nolint:gosec // status_point <= 1273
		h.logger.Error().Err(err).Msg("encode SPStatusPoint failed")
	}
}

// them in the connection state, and appends ZC_LONGPAR_CHANGE updates
// to the response burst.
func (h *DispatchHandler) applyMonsterKillExp(ctx context.Context, conn *domain.ConnectionInfo, burst *bytes.Buffer, targetGID uint32) {
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

			h.applyBaseLevelUp(ctx, conn, burst, &m, baseExp)
			break
		}
	}
}

// handleCZStatusChange processes a CZ_STATUS_CHANGE (0x00bb) request:
// the client asks to raise one base stat (SP_STR..SP_LUK) by amount
// (usually 1). The handler forwards to identity.AllocateStat which
// computes the pre-re cost, validates, and runs the atomic conditional
// UPDATE. The ack carries the result + new value; on success a
// ZC_PAR_CHANGE burst updates the stat and status_point on the client
// (rathena/src/map/clif.cpp:4283 clif_statusupack + clif_updatestatus).
func (h *DispatchHandler) handleCZStatusChange(ctx context.Context, conn *domain.ConnectionInfo, resp domain.Responder, frame []byte) error {
	req, err := packet.ParseCZStatusChange(frame)
	if err != nil {
		h.logger.Warn().Err(err).Uint64("conn", conn.ID).Msg("parse CZ_STATUS_CHANGE failed")
		return nil
	}
	if conn.AccountID == 0 || conn.CharID == 0 {
		h.logger.Warn().Uint64("conn", conn.ID).Msg("CZ_STATUS_CHANGE without prior CZ_ENTER; dropping")
		return nil
	}

	allocResp, err := h.identity.AllocateStat(ctx, &identityv1.AllocateStatRequest{
		AccountId: conn.AccountID,
		CharId:    conn.CharID,
		StatId:    uint32(req.StatusID),
		Amount:    uint32(req.Amount),
	})
	if err != nil {
		h.logger.Error().Err(err).Uint64("conn", conn.ID).Msg("AllocateStat RPC failed")
		return nil
	}

	var burst bytes.Buffer
	ack := packet.ZCStatusChangeAck{
		StatusID: req.StatusID,
		Result:   statResultToAckByte(allocResp.GetResult()),
		Value:    uint8(allocResp.GetNewValue()), //nolint:gosec // stats cap at 99
	}
	if err := ack.Encode(&burst); err != nil {
		return fmt.Errorf("encode ZC_STATUS_CHANGE_ACK: %w", err)
	}

	if allocResp.GetResult() == identityv1.StatResult_STAT_RESULT_OK {
		if err := (packet.ParChangeResponse{VarID: req.StatusID, Count: int32(allocResp.GetNewValue())}).Encode(&burst); err != nil { //nolint:gosec // stat <= 99
			h.logger.Error().Err(err).Msg("encode ZC_PAR_CHANGE (stat) failed")
		}
		if err := (packet.ParChangeResponse{VarID: packet.SPStatusPoint, Count: int32(allocResp.GetNewStatusPoint())}).Encode(&burst); err != nil { //nolint:gosec // status_point <= 1273
			h.logger.Error().Err(err).Msg("encode ZC_PAR_CHANGE (status_point) failed")
		}
	}

	if err := resp.SendPacket(burst.Bytes()); err != nil {
		return fmt.Errorf("send CZ_STATUS_CHANGE response: %w", err)
	}
	return nil
}

// statResultToAckByte maps the proto StatResult onto the rAthena
// ZC_STATUS_CHANGE_ACK result byte (0=success, 1=insufficient, 2=max).
func statResultToAckByte(r identityv1.StatResult) uint8 {
	switch r {
	case identityv1.StatResult_STAT_RESULT_OK:
		return 0
	case identityv1.StatResult_STAT_RESULT_INSUFFICIENT_POINTS:
		return 1
	case identityv1.StatResult_STAT_RESULT_MAX_STAT:
		return 2
	default:
		return 3
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
//   - ShopType == 0 (dialog NPC): if the NPC has a ScriptName that
//     resolves in h.scriptSet, build a DialogSession and run the VM
//     to drive the dialog via script. Otherwise fall back to the M15
//     hardcoded ZC_SAY_DIALOG2 + ZC_WAIT_DIALOG2 flow.
//
// If the NPC GID is not found in npcSpawns, the handler logs a warning
// and returns nil (no response).
func (h *DispatchHandler) handleCZContactNPC(ctx context.Context, conn *domain.ConnectionInfo, resp domain.Responder, frame []byte) error {
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

	// Dialog NPC — script-driven when ScriptName resolves in the
	// script set; otherwise fall back to the M15 hardcoded welcome.
	if h.startScriptDialog(ctx, conn, resp, npc) {
		return nil
	}
	return h.sendHardcodedWelcome(conn, resp, npc)
}

// startScriptDialog attempts to start a script-driven dialog for the
// NPC. Returns true when the session was created (and the script
// executed or paused for input); false when the NPC has no
// ScriptName or the script isn't compiled in the current snapshot
// and the caller should fall back to the hardcoded dialog.
//
// A new dialog for this connection always replaces any prior one
// (rAthena semantics: clicking a second NPC closes the first dialog
// on the client).
func (h *DispatchHandler) startScriptDialog(ctx context.Context, conn *domain.ConnectionInfo, resp domain.Responder, npc *npcSpawn) bool {
	if h.scriptSet == nil || npc.ScriptName == "" {
		return false
	}
	cs, ok := h.scriptSet.Scripts[npc.ScriptName]
	if !ok || cs == nil {
		return false
	}

	session := NewDialogSessionForResponder(cs, npc.GID, npc.Name, resp)
	h.dialogSessions.Store(conn.ID, session)

	if _, err := session.VM.Run(ctx); err != nil {
		h.logger.Error().
			Err(err).
			Uint64("conn", conn.ID).
			Uint32("npc_gid", npc.GID).
			Str("npc_name", npc.Name).
			Str("script", npc.ScriptName).
			Msg("script dialog VM run failed; cleaning up session")
		h.dialogSessions.Delete(conn.ID)
		return true
	}
	if session.IsDone() {
		h.dialogSessions.Delete(conn.ID)
	}
	h.logger.Info().
		Uint64("conn", conn.ID).
		Uint32("npc_gid", npc.GID).
		Str("npc_name", npc.Name).
		Str("script", npc.ScriptName).
		Msg("NPC script dialog started")
	return true
}

// sendHardcodedWelcome emits the M15 ZC_SAY_DIALOG2 + ZC_WAIT_DIALOG2
// fallback for dialog NPCs without a compiled script. Used when
// scriptSet is nil, the NPC has no ScriptName, or the named script
// isn't in the current snapshot.
func (h *DispatchHandler) sendHardcodedWelcome(conn *domain.ConnectionInfo, resp domain.Responder, npc *npcSpawn) error {
	say := packet.SayDialog2Response{
		NpcID:   npc.GID,
		Type:    0,
		Message: "Welcome to goAthena! This is " + npc.Name + ".",
	}
	var sayBuf bytes.Buffer
	if err := say.Encode(&sayBuf); err != nil {
		h.logger.Error().
			Err(err).
			Uint64("conn", conn.ID).
			Uint32("npc_aid", npc.GID).
			Msg("encode ZC_SAY_DIALOG2 failed; dropping packet")
		return nil
	}

	wait := packet.WaitDialog2Response{
		NpcID: npc.GID,
		Type:  0,
	}
	var waitBuf bytes.Buffer
	if err := wait.Encode(&waitBuf); err != nil {
		h.logger.Error().
			Err(err).
			Uint64("conn", conn.ID).
			Uint32("npc_aid", npc.GID).
			Msg("encode ZC_WAIT_DIALOG2 failed; dropping packet")
		return nil
	}

	h.logger.Info().
		Uint64("conn", conn.ID).
		Uint32("npc_aid", npc.GID).
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
func (h *DispatchHandler) handleCZAckSelectDealType(ctx context.Context, conn *domain.ConnectionInfo, resp domain.Responder, frame []byte) error {
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
		// Buy — send the NPC's stock list and remember the NPC for
		// the follow-up CZ_PC_PURCHASE_ITEMLIST price-authority
		// check.
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
		conn.SetShopNPC(npc.GID)
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
		// Sell — fetch the player's inventory, build the priced
		// sell list (D-213), and send ZC_PC_SELL_ITEMLIST. The
		// GetInventory call is the price-authority reference: each
		// inventory item's nameid is resolved to the NPC's catalog
		// buy price, then halved for the sell price. Items not in
		// any shop catalog sell for 0.
		conn.SetShopNPC(npc.GID)
		h.handleCZAckSelectDealTypeSell(ctx, conn, resp, npc)
		return nil
	case 0x02:
		conn.SetShopNPC(0)
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

// sellCatalog holds the (itemid → buyPrice) catalog rAthena uses to
// compute the sell-price floor for the sell window. The gateway uses
// the union of every NPC's ShopItems (D-213) so an item that's not
// stocked by the active shop NPC still gets a sensible sell price
// based on a different shop's offer. Computed once via sellCatalogOnce
// because npcSpawns is static (loaded at startup); rebuilding the map
// on every sell request would iterate every NPC's ShopItems needlessly.
//
// The map is SHARED READ-ONLY across goroutines and across every
// sellPriceCatalog() call. Callers MUST NOT mutate it — mutation would
// race with concurrent reads and would persist into every subsequent
// sell response.
var (
	sellCatalog     map[uint32]uint32
	sellCatalogOnce sync.Once
)

// sellPriceCatalog returns the cached (itemid → buyPrice) catalog.
// The catalog is built lazily on the first call via sync.Once and
// then shared read-only with every subsequent caller; because
// npcSpawns is static (loaded at startup), rebuilding it would be
// wasteful. First-write-wins preserves the catalog order: a later
// NPC selling the same item at a different price does not override
// the first entry, matching rAthena's "shop loads vendor prices at
// startup" monotonicity.
//
// The returned map is SHARED READ-ONLY — callers MUST NOT mutate it.
func sellPriceCatalog() map[uint32]uint32 {
	sellCatalogOnce.Do(func() {
		cat := make(map[uint32]uint32)
		for _, npc := range npcSpawns {
			for _, it := range npc.ShopItems {
				if _, ok := cat[it.ItemID]; !ok {
					cat[it.ItemID] = it.Price
				}
			}
		}
		sellCatalog = cat
	})
	return sellCatalog
}

// handleCZAckSelectDealTypeSell handles the Sell branch of
// handleCZAckSelectDealType. The player just clicked "Sell" against a
// known shop NPC; the handler fetches the player's inventory from
// identity, prices every sellable item at half its catalog buy price,
// and sends ZC_PC_SELL_ITEMLIST. No gRPC errors are returned to the
// client — rAthena's clif_purchaseitems_for_sell path simply emits
// nothing if the inventory fetch fails, and we mirror that so a
// transient identity failure keeps the connection alive for retry.
func (h *DispatchHandler) handleCZAckSelectDealTypeSell(ctx context.Context, conn *domain.ConnectionInfo, resp domain.Responder, npc *npcSpawn) {
	if conn.AccountID == 0 || conn.CharID == 0 {
		h.logger.Warn().
			Uint64("conn", conn.ID).
			Uint32("npc_id", npc.GID).
			Msg("CZ_ACK_SELECT_DEALTYPE Sell without prior CZ_ENTER; dropping")
		return
	}

	invReq := &identityv1.GetInventoryRequest{
		AccountId: conn.AccountID,
		CharId:    conn.CharID,
	}
	invResp, err := h.identity.GetInventory(ctx, invReq)
	if err != nil {
		if clientGone := errors.Is(err, context.Canceled) || ctx.Err() != nil; clientGone {
			h.logger.Debug().
				Uint64("conn", conn.ID).
				Uint32("npc_id", npc.GID).
				Msg("identity GetInventory cancelled (client gone)")
			return
		}
		h.logger.Warn().
			Err(err).
			Uint64("conn", conn.ID).
			Uint32("npc_id", npc.GID).
			Str("npc_name", npc.Name).
			Msg("identity GetInventory failed for shop sell list; dropping")
		return
	}
	if invResp == nil {
		h.logger.Warn().
			Uint64("conn", conn.ID).
			Uint32("npc_id", npc.GID).
			Msg("identity returned nil GetInventory response for shop sell list; dropping")
		return
	}

	catalog := sellPriceCatalog()
	items := invResp.GetItems()
	lines := make([]packet.ShopSellItem, 0, len(items))
	for idx, it := range items {
		if it == nil {
			continue
		}
		nameid := it.GetNameid()
		// Sell price = buyPrice / 2 (rAthena's
		// pc_shopprice / itemdb value / 2). Items not in any
		// shop catalog list at 0.
		var sellPrice uint32
		if buy, ok := catalog[nameid]; ok {
			sellPrice = buy / 2
		}
		lines = append(lines, packet.ShopSellItem{
			Index:      uint16(idx), //nolint:gosec // client slot position is 16-bit on the wire
			Price:      sellPrice,
			Overcharge: sellPrice,
		})
	}

	list := packet.SellItemListResponse{Items: lines}
	var buf bytes.Buffer
	if err := list.Encode(&buf); err != nil {
		h.logger.Error().
			Err(err).
			Uint64("conn", conn.ID).
			Uint32("npc_id", npc.GID).
			Str("npc_name", npc.Name).
			Int("sell_items", len(lines)).
			Msg("encode ZC_PC_SELL_ITEMLIST failed; dropping packet")
		return
	}
	h.logger.Info().
		Uint64("conn", conn.ID).
		Uint32("npc_id", npc.GID).
		Str("npc_name", npc.Name).
		Int("sell_items", len(lines)).
		Msg("NPC shop sell list sent")
	if err := resp.SendPacket(buf.Bytes()); err != nil {
		h.logger.Error().
			Err(err).
			Uint64("conn", conn.ID).
			Uint32("npc_id", npc.GID).
			Msg("send ZC_PC_SELL_ITEMLIST failed; dropping packet")
	}
}

// handleCZPCPurchaseItemList responds to CZ_PC_PURCHASE_ITEMLIST
// (0x00c8) — the player's purchase request. The handler validates
// the order against the active NPC's price catalog (D-204) so a
// client cannot dictate unit prices, then commits the order via
// identity.BuyFromShop. On OK it sends ZC_PC_PURCHASE_RESULT (0) +
// a ZC_LONGPAR_CHANGE for the post-transaction zeny balance; on
// non-OK it sends ZC_PC_PURCHASE_RESULT (1) with no zeny update.
func (h *DispatchHandler) handleCZPCPurchaseItemList(ctx context.Context, conn *domain.ConnectionInfo, resp domain.Responder, frame []byte) error {
	req, err := packet.ParseCZPCPurchaseItemList(frame)
	if err != nil {
		h.logger.Warn().
			Err(err).
			Uint64("conn", conn.ID).
			Int("frame_len", len(frame)).
			Msg("malformed CZ_PC_PURCHASE_ITEMLIST; dropping packet")
		return nil
	}
	if conn.AccountID == 0 || conn.CharID == 0 {
		h.shopDropNoAuth(conn, "CZ_PC_PURCHASE_ITEMLIST", len(req.Entries))
		return nil
	}
	npc, npcGID := h.resolveActiveShopNPC(conn)
	if npc == nil {
		h.logger.Warn().
			Uint64("conn", conn.ID).
			Uint32("aid", conn.AccountID).
			Msg("CZ_PC_PURCHASE_ITEMLIST without prior CZ_ACK_SELECT_DEALTYPE; failing")
		return h.sendPurchaseResultFail(resp)
	}
	orders, ok := h.buildPurchaseOrders(conn, npc, npcGID, req.Entries)
	if !ok {
		return h.sendPurchaseResultFail(resp)
	}
	h.logger.Info().
		Uint64("conn", conn.ID).
		Uint32("aid", conn.AccountID).
		Uint32("cid", conn.CharID).
		Uint32("npc_id", npcGID).
		Int("entries", len(orders)).
		Msg("NPC shop purchase requested")
	return h.commitBuyAndReply(ctx, conn, resp, orders)
}

// commitBuyAndReply calls identity.BuyFromShop, writes
// ZC_PC_PURCHASE_RESULT, and on OK emits a zeny LongParChange.
// Extracted from handleCZPCPurchaseItemList to keep the main handler
// under the gocyclo limit.
func (h *DispatchHandler) commitBuyAndReply(ctx context.Context, conn *domain.ConnectionInfo, resp domain.Responder, orders []*identityv1.ShopOrder) error {
	buyResp, err := h.identity.BuyFromShop(ctx, &identityv1.BuyFromShopRequest{
		AccountId: conn.AccountID,
		CharId:    conn.CharID,
		Orders:    orders,
	})
	if err != nil {
		return h.handleShopRPCErr(ctx, conn, err, "BuyFromShop", "purchase", h.sendPurchaseResultFail(resp))
	}
	if buyResp == nil || buyResp.GetResult() != identityv1.BuyResult_BUY_RESULT_OK {
		return h.sendPurchaseResultFail(resp)
	}
	return h.replyShopOK(
		resp, conn, buyResp.GetNewZeny(),
		func(w io.Writer) error { return packet.PurchaseResultResponse{Result: 0}.Encode(w) },
		"ZC_PC_PURCHASE_RESULT",
	)
}

// handleCZPCSellItemList responds to CZ_PC_SELL_ITEMLIST (0x00c9) —
// the player's sell request. The handler resolves each entry's
// client-side slot index to an inventory row id, looks up the sell
// price (catalog buyPrice / 2), commits the sale via
// identity.SellToShop, and on OK sends ZC_PC_SELL_RESULT (0) + a
// zeny LongParChange for the post-transaction balance.
func (h *DispatchHandler) handleCZPCSellItemList(ctx context.Context, conn *domain.ConnectionInfo, resp domain.Responder, frame []byte) error {
	req, err := packet.ParseCZPCSellItemList(frame)
	if err != nil {
		h.logger.Warn().
			Err(err).
			Uint64("conn", conn.ID).
			Int("frame_len", len(frame)).
			Msg("malformed CZ_PC_SELL_ITEMLIST; dropping packet")
		return nil
	}
	if conn.AccountID == 0 || conn.CharID == 0 {
		h.shopDropNoAuth(conn, "CZ_PC_SELL_ITEMLIST", len(req.Entries))
		return nil
	}
	npcGID := conn.ShopNPC()
	if npcGID == 0 {
		h.logger.Warn().
			Uint64("conn", conn.ID).
			Uint32("aid", conn.AccountID).
			Msg("CZ_PC_SELL_ITEMLIST without prior CZ_ACK_SELECT_DEALTYPE; failing")
		return h.sendSellResultFail(resp)
	}
	sales, ok := h.buildSellLines(ctx, conn, req.Entries)
	if !ok {
		return h.sendSellResultFail(resp)
	}
	h.logger.Info().
		Uint64("conn", conn.ID).
		Uint32("aid", conn.AccountID).
		Uint32("cid", conn.CharID).
		Uint32("npc_id", npcGID).
		Int("entries", len(sales)).
		Msg("NPC shop sell requested")
	return h.commitSellAndReply(ctx, conn, resp, sales)
}

// shopDropNoAuth logs the standard "missing AID/CharID" precondition
// drop for the shop packet family. Extracted from handleCZPC*
// to keep the per-handler cyclomatic complexity under the gocyclo
// limit.
func (h *DispatchHandler) shopDropNoAuth(conn *domain.ConnectionInfo, opName string, entryCount int) {
	if conn.AccountID == 0 {
		h.logger.Warn().
			Uint64("conn", conn.ID).
			Int("entries", entryCount).
			Str("op", opName).
			Msg("shop request without prior CZ_ENTER; dropping")
		return
	}
	h.logger.Warn().
		Uint64("conn", conn.ID).
		Int("entries", entryCount).
		Str("op", opName).
		Msg("shop request without prior CharID; dropping")
}

// resolveActiveShopNPC returns the npcSpawn whose GID the connection
// has anchored via SetShopNPC, or nil if no NPC is active. Extracted
// from handleCZPCPurchaseItemList for clarity.
func (h *DispatchHandler) resolveActiveShopNPC(conn *domain.ConnectionInfo) (*npcSpawn, uint32) {
	npcGID := conn.ShopNPC()
	if npcGID == 0 {
		return nil, 0
	}
	for i := range npcSpawns {
		if npcSpawns[i].GID == npcGID {
			return &npcSpawns[i], npcGID
		}
	}
	h.logger.Warn().
		Uint64("conn", conn.ID).
		Uint32("npc_id", npcGID).
		Msg("active shop NPC no longer in catalog")
	return nil, 0
}

// buildPurchaseOrders translates the (itemId, amount) entries into
// *identityv1.ShopOrder rows using the NPC's catalog as the price
// authority (D-204). Returns false if any itemId is unknown — the
// caller must fail the whole transaction in that case.
func (h *DispatchHandler) buildPurchaseOrders(
	conn *domain.ConnectionInfo,
	npc *npcSpawn,
	npcGID uint32,
	entries []packet.CZPCPurchaseItemListEntry,
) ([]*identityv1.ShopOrder, bool) {
	priceByItemID := make(map[uint32]uint32, len(npc.ShopItems))
	for _, it := range npc.ShopItems {
		priceByItemID[it.ItemID] = it.Price
	}
	orders := make([]*identityv1.ShopOrder, 0, len(entries))
	for _, e := range entries {
		price, ok := priceByItemID[e.ItemID]
		if !ok {
			h.logger.Warn().
				Uint64("conn", conn.ID).
				Uint32("npc_id", npcGID).
				Uint32("item_id", e.ItemID).
				Uint16("amount", e.Amount).
				Msg("CZ_PC_PURCHASE_ITEMLIST item not in shop catalog; failing")
			return nil, false
		}
		orders = append(orders, &identityv1.ShopOrder{
			ItemId:    e.ItemID,
			Amount:    uint32(e.Amount), //nolint:gosec // wire amount is 16-bit
			UnitPrice: price,
		})
	}
	return orders, true
}

// buildSellLines resolves each (slot, amount) entry to an inventory
// DB id, looks up the sell price (catalog buyPrice / 2), and returns
// the *identityv1.SellLine rows ready for identity.SellToShop.
// Returns false on any slot-resolution failure so the caller can
// fail the whole transaction.
func (h *DispatchHandler) buildSellLines(
	ctx context.Context,
	conn *domain.ConnectionInfo,
	entries []packet.CZPCSellItemListEntry,
) ([]*identityv1.SellLine, bool) {
	invResp, err := h.identity.GetInventory(ctx, &identityv1.GetInventoryRequest{
		AccountId: conn.AccountID,
		CharId:    conn.CharID,
	})
	if err != nil {
		h.shopClientGoneOrWarn(ctx, conn, err, "GetInventory", "sell")
		return nil, false
	}
	if invResp == nil {
		h.logger.Warn().
			Uint64("conn", conn.ID).
			Msg("identity returned nil GetInventory for sell request; failing")
		return nil, false
	}
	items := invResp.GetItems()
	nameidBySlot := make(map[uint16]uint32, len(items))
	for idx, it := range items {
		if it == nil {
			continue
		}
		nameidBySlot[uint16(idx)] = it.GetNameid() //nolint:gosec // slot position is 16-bit on the wire
	}
	catalog := sellPriceCatalog()
	sales := make([]*identityv1.SellLine, 0, len(entries))
	for _, e := range entries {
		nameid, ok := nameidBySlot[e.Index]
		if !ok {
			h.logger.Warn().
				Uint64("conn", conn.ID).
				Uint16("slot", e.Index).
				Msg("CZ_PC_SELL_ITEMLIST slot not in inventory snapshot; failing")
			return nil, false
		}
		invID, ok := conn.ResolveInventoryID(e.Index)
		if !ok {
			h.logger.Warn().
				Uint64("conn", conn.ID).
				Uint16("slot", e.Index).
				Msg("CZ_PC_SELL_ITEMLIST slot missing from invIndex; failing")
			return nil, false
		}
		sales = append(sales, &identityv1.SellLine{
			InvId:     invID,
			Amount:    uint32(e.Amount), //nolint:gosec // wire amount is 16-bit
			UnitPrice: catalog[nameid] / 2,
		})
	}
	return sales, true
}

// shopClientGoneOrWarn logs context.Canceled as Debug and any other
// transport error as Warn. Returns true if the caller should drop
// silently (client gone / ctx cancelled); false if it should fail
// the transaction.
func (h *DispatchHandler) shopClientGoneOrWarn(ctx context.Context, conn *domain.ConnectionInfo, err error, rpcName, op string) bool {
	if clientGone := errors.Is(err, context.Canceled) || ctx.Err() != nil; clientGone {
		h.logger.Debug().
			Uint64("conn", conn.ID).
			Str("rpc", rpcName).
			Msg("identity RPC cancelled (client gone)")
		return true
	}
	h.logger.Warn().
		Err(err).
		Uint64("conn", conn.ID).
		Uint32("aid", conn.AccountID).
		Str("rpc", rpcName).
		Str("op", op).
		Msg("identity RPC failed")
	return false
}

// handleShopRPCErr is the shop-equivalent wrapper around
// shopClientGoneOrWarn: if the client is gone the connection is
// preserved (return nil); otherwise the caller invokes its fail
// callback and forwards the error.
func (h *DispatchHandler) handleShopRPCErr(
	ctx context.Context, conn *domain.ConnectionInfo,
	err error, rpcName, op string, failOnError error,
) error {
	if h.shopClientGoneOrWarn(ctx, conn, err, rpcName, op) {
		return nil
	}
	return failOnError
}

// replyShopOK emits the success result packet and the post-tx zeny
// LongParChange. Used by both commitBuyAndReply and
// commitSellAndReply to avoid duplicating the encode + send + log
// + zeny-cleanup sequence (D-205).
func (h *DispatchHandler) replyShopOK(
	resp domain.Responder, conn *domain.ConnectionInfo,
	newZeny uint32,
	encodeResult func(io.Writer) error,
	resultName string,
) error {
	var buf bytes.Buffer
	if err := encodeResult(&buf); err != nil {
		h.logger.Error().
			Err(err).
			Uint64("conn", conn.ID).
			Str("result", resultName).
			Msg("encode success result failed; dropping packet")
		return nil
	}
	if err := resp.SendPacket(buf.Bytes()); err != nil {
		return fmt.Errorf("send %s: %w", resultName, err)
	}
	if err := h.sendZenyParChange(resp, newZeny); err != nil {
		return err
	}
	conn.SetShopNPC(0)
	return nil
}

// commitSellAndReply calls identity.SellToShop and on OK writes
// ZC_PC_SELL_RESULT + zeny update. Extracted from
// handleCZPCSellItemList to keep the main handler under the gocyclo
// limit.
func (h *DispatchHandler) commitSellAndReply(ctx context.Context, conn *domain.ConnectionInfo, resp domain.Responder, sales []*identityv1.SellLine) error {
	sellResp, err := h.identity.SellToShop(ctx, &identityv1.SellToShopRequest{
		AccountId: conn.AccountID,
		CharId:    conn.CharID,
		Sales:     sales,
	})
	if err != nil {
		return h.handleShopRPCErr(ctx, conn, err, "SellToShop", "sell", h.sendSellResultFail(resp))
	}
	if sellResp == nil || sellResp.GetResult() != identityv1.SellResult_SELL_RESULT_OK {
		return h.sendSellResultFail(resp)
	}
	return h.replyShopOK(
		resp, conn, sellResp.GetNewZeny(),
		func(w io.Writer) error { return packet.SellResultResponse{Result: 0}.Encode(w) },
		"ZC_PC_SELL_RESULT",
	)
}

// sendPurchaseResultFail writes ZC_PC_PURCHASE_RESULT(result=1)
// on the wire. Returns the wrapped SendPacket error so callers can
// surface transport failures to the dispatch loop just like the
// success path.
func (h *DispatchHandler) sendPurchaseResultFail(resp domain.Responder) error {
	var buf bytes.Buffer
	pr := packet.PurchaseResultResponse{Result: 1}
	if err := pr.Encode(&buf); err != nil {
		// Fixed-layout encoders cannot fail; log and drop so the
		// dispatch loop does not surface a phantom error.
		h.logger.Error().
			Err(err).
			Uint64("conn", 0).
			Msg("encode ZC_PC_PURCHASE_RESULT(fail) failed; dropping")
		return nil
	}
	if err := resp.SendPacket(buf.Bytes()); err != nil {
		return fmt.Errorf("send ZC_PC_PURCHASE_RESULT(fail): %w", err)
	}
	return nil
}

// sendSellResultFail writes ZC_PC_SELL_RESULT(result=1) on the wire
// with the same wrapcheck + transport semantics as
// sendPurchaseResultFail.
func (h *DispatchHandler) sendSellResultFail(resp domain.Responder) error {
	var buf bytes.Buffer
	sr := packet.SellResultResponse{Result: 1}
	if err := sr.Encode(&buf); err != nil {
		h.logger.Error().
			Err(err).
			Uint64("conn", 0).
			Msg("encode ZC_PC_SELL_RESULT(fail) failed; dropping")
		return nil
	}
	if err := resp.SendPacket(buf.Bytes()); err != nil {
		return fmt.Errorf("send ZC_PC_SELL_RESULT(fail): %w", err)
	}
	return nil
}

// sendZenyParChange encodes and writes a ZC_LONGPAR_CHANGE with
// varID=SP_ZENY (20). Used by both buy and sell handlers to keep
// the client's zeny counter in sync with the identity-side balance
// after a successful transaction (D-205).
func (h *DispatchHandler) sendZenyParChange(resp domain.Responder, newZeny uint32) error {
	// Defensive clamp: the wire slot is int32 but zeny is uint32, so a
	// future MaxZeny above MaxInt32 would otherwise produce a negative
	// Amount. Clamp to MaxInt32 (the wire can't represent more anyway)
	// and log once so the cap can be re-evaluated.
	amount := newZeny
	if amount > math.MaxInt32 {
		h.logger.Warn().
			Uint64("conn", 0).
			Uint32("new_zeny", newZeny).
			Uint32("clamped_to", math.MaxInt32).
			Msg("zeny exceeds MaxInt32 wire slot; clamping ZC_LONGPAR_CHANGE")
		amount = math.MaxInt32
	}
	var buf bytes.Buffer
	pp := packet.LongParChangeResponse{
		VarID:  packet.SPZeny,
		Amount: int32(amount), //nolint:gosec // clamp above guarantees value fits in int32
	}
	if err := pp.Encode(&buf); err != nil {
		// Fixed-layout encoder cannot fail; log + drop.
		h.logger.Error().
			Err(err).
			Uint64("conn", 0).
			Uint32("new_zeny", newZeny).
			Msg("encode ZC_LONGPAR_CHANGE(zeny) failed; dropping")
		return nil
	}
	if err := resp.SendPacket(buf.Bytes()); err != nil {
		return fmt.Errorf("send ZC_LONGPAR_CHANGE(zeny): %w", err)
	}
	return nil
}

// handleCZReqNextScript responds to CZ_REQNEXTSCRIPT (0x00b9) — the
// client clicking "Next" in the NPC dialog. If the connection has an
// active script-driven DialogSession, the handler swaps in the
// current Responder and resumes the VM; builtins emit the next
// dialog page (ZC_SAY_DIALOG2, ZC_WAIT_DIALOG2, ZC_MENU_LIST, or
// ZC_CLOSE_DIALOG) as the script runs. Otherwise the handler falls
// back to a single hardcoded continuation + close, matching the M15
// behavior that pre-dates the script engine.
func (h *DispatchHandler) handleCZReqNextScript(ctx context.Context, conn *domain.ConnectionInfo, resp domain.Responder, frame []byte) error {
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

	if v, ok := h.dialogSessions.Load(conn.ID); ok {
		session, ok := v.(*DialogSession)
		if !ok || session == nil {
			h.logger.Error().
				Uint64("conn", conn.ID).
				Uint32("npc_id", req.NpcID).
				Msg("dialogSessions map held non-*DialogSession or nil value; cleaning up")
			h.dialogSessions.Delete(conn.ID)
			return nil
		}
		session.SetResponder(resp)
		if _, runErr := session.VM.Resume(ctx); runErr != nil {
			h.logger.Error().
				Err(runErr).
				Uint64("conn", conn.ID).
				Uint32("npc_id", req.NpcID).
				Msg("script dialog VM resume failed; cleaning up session")
			h.dialogSessions.Delete(conn.ID)
			return nil
		}
		if session.IsDone() {
			h.dialogSessions.Delete(conn.ID)
		}
		h.logger.Info().
			Uint64("conn", conn.ID).
			Uint32("npc_id", req.NpcID).
			Msg("NPC script dialog resumed")
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

// handleCZChooseMenu responds to CZ_CHOOSE_MENU (0x00b8) — the client
// selecting an option from a ZC_MENU_LIST. The 1-based menu index is
// written into the active DialogSession's scope as ".@menu" and the
// VM is resumed; builtins emit the next packet as the script runs.
// A selection of -1 (0xff on the wire) means the player cancelled
// the dialog: the handler closes the dialog window and drops the
// session without resuming. If no session is active, the packet is
// dropped silently (matches the existing handling for stray menu
// replies from the pre-script-engine M15 flow).
func (h *DispatchHandler) handleCZChooseMenu(ctx context.Context, conn *domain.ConnectionInfo, resp domain.Responder, frame []byte) error {
	req, err := packet.ParseCZChooseMenu(frame)
	if err != nil {
		h.logger.Warn().
			Err(err).
			Uint64("conn", conn.ID).
			Int("frame_len", len(frame)).
			Msg("malformed CZ_CHOOSE_MENU; dropping packet")
		return nil
	}

	if conn.AccountID == 0 {
		h.logger.Warn().
			Uint64("conn", conn.ID).
			Uint32("npc_id", req.NpcID).
			Msg("CZ_CHOOSE_MENU without prior CZ_ENTER; dropping")
		return nil
	}

	v, ok := h.dialogSessions.Load(conn.ID)
	if !ok {
		h.logger.Warn().
			Uint64("conn", conn.ID).
			Uint32("npc_id", req.NpcID).
			Int8("selected", req.Selected).
			Msg("CZ_CHOOSE_MENU with no active dialog session; dropping")
		return nil
	}
	session, ok := v.(*DialogSession)
	if !ok || session == nil {
		h.logger.Error().
			Uint64("conn", conn.ID).
			Uint32("npc_id", req.NpcID).
			Msg("dialogSessions map held non-*DialogSession or nil value; cleaning up")
		h.dialogSessions.Delete(conn.ID)
		return nil
	}

	if req.Selected < 0 {
		// 0xff on the wire (rAthena: "cancel") → close the dialog
		// window and drop the session. No resume; the player
		// explicitly chose to leave.
		closeD := packet.CloseDialogResponse{NpcID: req.NpcID} //nolint:staticcheck // explicit struct init keeps the wire struct decoupled from SayDialog2Response
		var closeBuf bytes.Buffer
		if err := closeD.Encode(&closeBuf); err != nil {
			return fmt.Errorf("encode ZC_CLOSE_DIALOG on menu cancel: %w", err)
		}
		if err := resp.SendPacket(closeBuf.Bytes()); err != nil {
			return fmt.Errorf("send ZC_CLOSE_DIALOG on menu cancel: %w", err)
		}
		h.dialogSessions.Delete(conn.ID)
		h.logger.Info().
			Uint64("conn", conn.ID).
			Uint32("npc_id", req.NpcID).
			Msg("NPC menu cancelled by client")
		return nil
	}

	session.SetResponder(resp)
	session.Scopes.Set(".@menu", vm.IntValue(int64(req.Selected)))
	if _, runErr := session.VM.Resume(ctx); runErr != nil {
		h.logger.Error().
			Err(runErr).
			Uint64("conn", conn.ID).
			Uint32("npc_id", req.NpcID).
			Int8("selected", req.Selected).
			Msg("script dialog VM resume failed on menu; cleaning up session")
		h.dialogSessions.Delete(conn.ID)
		return nil
	}
	if session.IsDone() {
		h.dialogSessions.Delete(conn.ID)
	}
	h.logger.Info().
		Uint64("conn", conn.ID).
		Uint32("npc_id", req.NpcID).
		Int8("selected", req.Selected).
		Msg("NPC script menu resumed")
	return nil
}

// handleCZCloseDialog responds to CZ_CLOSE_DIALOG (0x0146) — the
// client clicking "Close" in the NPC dialog. The client closes the
// dialog window locally; the server does not need to send a response.
// The handler also drops any active DialogSession for this
// connection so a subsequent CZ_CONTACTNPC starts fresh.
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

	h.dialogSessions.Delete(conn.ID)

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
