//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	identityv1 "github.com/bouroo/goAthena/api/pb/identity/v1"
	zonev1 "github.com/bouroo/goAthena/api/pb/zone/v1"
	"github.com/bouroo/goAthena/internal/features/gateway/domain"
	"github.com/bouroo/goAthena/internal/features/script/compiler"
	"github.com/bouroo/goAthena/internal/features/script/parser"
	skilldomain "github.com/bouroo/goAthena/internal/features/skill/domain"
	statsdomain "github.com/bouroo/goAthena/internal/features/stats/domain"
	"github.com/bouroo/goAthena/pkg/ro/mobdb"
	"github.com/bouroo/goAthena/pkg/ro/packet"
	"github.com/bouroo/goAthena/pkg/ro/script"
)

// fakeIdentityClient is a hand-written, in-process stand-in for
// identityv1.IdentityServiceClient. It records the most recent request
// and returns whatever the test installed via authenticateFn /
// characterListFn / getCharacterFn. We intentionally avoid mockgen
// here to keep the service tests self-contained and trivially diffable
// against the gRPC interface.
type fakeIdentityClient struct {
	mu              sync.Mutex
	authenticateFn  func(context.Context, *identityv1.AuthenticateRequest) (*identityv1.AuthenticateResponse, error)
	characterListFn func(context.Context, *identityv1.GetCharacterListRequest) (*identityv1.GetCharacterListResponse, error)
	getCharacterFn  func(context.Context, *identityv1.GetCharacterRequest) (*identityv1.GetCharacterResponse, error)
	getInventoryFn  func(context.Context, *identityv1.GetInventoryRequest) (*identityv1.GetInventoryResponse, error)
	equipItemFn     func(context.Context, *identityv1.EquipItemRequest) (*identityv1.EquipItemResponse, error)
	unequipItemFn   func(context.Context, *identityv1.UnequipItemRequest) (*identityv1.UnequipItemResponse, error)
	useItemFn       func(context.Context, *identityv1.UseItemRequest) (*identityv1.UseItemResponse, error)
	// P2B: shop economy. buyFromShopFn / sellToShopFn drive the
	// handleCZPCPurchaseItemList + handleCZPCSellItemList happy and
	// edge-case paths; buyReqs / sellReqs capture the (verifiable)
	// inputs the dispatcher forwards.
	buyFromShopFn    func(context.Context, *identityv1.BuyFromShopRequest) (*identityv1.BuyFromShopResponse, error)
	sellToShopFn     func(context.Context, *identityv1.SellToShopRequest) (*identityv1.SellToShopResponse, error)
	buyReqs          []*identityv1.BuyFromShopRequest
	sellReqs         []*identityv1.SellToShopRequest
	applyLevelUpFn   func(context.Context, *identityv1.ApplyLevelUpRequest) (*identityv1.ApplyLevelUpResponse, error)
	allocateStatFn   func(context.Context, *identityv1.AllocateStatRequest) (*identityv1.AllocateStatResponse, error)
	applyLevelUpReqs []*identityv1.ApplyLevelUpRequest
	allocateStatReqs []*identityv1.AllocateStatRequest
}

func (f *fakeIdentityClient) Authenticate(ctx context.Context, req *identityv1.AuthenticateRequest, _ ...grpc.CallOption) (*identityv1.AuthenticateResponse, error) {
	f.mu.Lock()
	fn := f.authenticateFn
	f.mu.Unlock()
	if fn == nil {
		return nil, status.Error(codes.Unimplemented, "no auth fn installed")
	}
	return fn(ctx, req)
}

func (f *fakeIdentityClient) GetCharacterList(ctx context.Context, req *identityv1.GetCharacterListRequest, _ ...grpc.CallOption) (*identityv1.GetCharacterListResponse, error) {
	f.mu.Lock()
	fn := f.characterListFn
	f.mu.Unlock()
	if fn == nil {
		return nil, status.Error(codes.Unimplemented, "no chars fn installed")
	}
	return fn(ctx, req)
}

func (f *fakeIdentityClient) GetCharacter(ctx context.Context, req *identityv1.GetCharacterRequest, _ ...grpc.CallOption) (*identityv1.GetCharacterResponse, error) {
	f.mu.Lock()
	fn := f.getCharacterFn
	f.mu.Unlock()
	if fn == nil {
		// A missing fn means the test did not set up the spawn
		// fallback. Return success=false so the gateway falls back
		// to a zero-filled spawn rather than aborting the handshake
		// with an Unimplemented error.
		return &identityv1.GetCharacterResponse{
			Success: false,
			Error:   "no getCharacter fn installed",
		}, nil
	}
	return fn(ctx, req)
}

// Inventory RPC stubs — dispatch tests that need a real response
// install getInventoryFn / equipItemFn / unequipItemFn / useItemFn;
// tests that don't install one get a `success=false` or empty
// response so the dispatcher falls back to the empty-list / fail
// ack paths without aborting the handshake.
func (f *fakeIdentityClient) GetInventory(ctx context.Context, req *identityv1.GetInventoryRequest, _ ...grpc.CallOption) (*identityv1.GetInventoryResponse, error) {
	f.mu.Lock()
	fn := f.getInventoryFn
	f.mu.Unlock()
	if fn == nil {
		// Empty list — the dispatcher falls back to two 4-byte empty
		// frames, which is the pre-P2A behaviour every other
		// TestDispatchHandler_CZNotifyActorInit_* test relies on.
		return &identityv1.GetInventoryResponse{Items: nil}, nil
	}
	return fn(ctx, req)
}

func (f *fakeIdentityClient) EquipItem(ctx context.Context, req *identityv1.EquipItemRequest, _ ...grpc.CallOption) (*identityv1.EquipItemResponse, error) {
	f.mu.Lock()
	fn := f.equipItemFn
	f.mu.Unlock()
	if fn == nil {
		return &identityv1.EquipItemResponse{Success: false, Error: "no equipItem fn installed"}, nil
	}
	return fn(ctx, req)
}

func (f *fakeIdentityClient) UnequipItem(ctx context.Context, req *identityv1.UnequipItemRequest, _ ...grpc.CallOption) (*identityv1.UnequipItemResponse, error) {
	f.mu.Lock()
	fn := f.unequipItemFn
	f.mu.Unlock()
	if fn == nil {
		return &identityv1.UnequipItemResponse{Success: false, Error: "no unequipItem fn installed"}, nil
	}
	return fn(ctx, req)
}

func (f *fakeIdentityClient) UseItem(ctx context.Context, req *identityv1.UseItemRequest, _ ...grpc.CallOption) (*identityv1.UseItemResponse, error) {
	f.mu.Lock()
	fn := f.useItemFn
	f.mu.Unlock()
	if fn == nil {
		return &identityv1.UseItemResponse{Success: false, Error: "no useItem fn installed"}, nil
	}
	return fn(ctx, req)
}

// P2B: shop economy RPCs. Tests that exercise the shop handlers
// install buyFromShopFn / sellToShopFn; tests that don't get a
// guard false-result so the dispatch path falls back to its fail
// encoding without aborting the handshake.
func (f *fakeIdentityClient) BuyFromShop(ctx context.Context, req *identityv1.BuyFromShopRequest, _ ...grpc.CallOption) (*identityv1.BuyFromShopResponse, error) {
	f.mu.Lock()
	f.buyReqs = append(f.buyReqs, req)
	fn := f.buyFromShopFn
	f.mu.Unlock()
	if fn == nil {
		return &identityv1.BuyFromShopResponse{Result: identityv1.BuyResult_BUY_RESULT_INSUFFICIENT_ZENY, NewZeny: 0}, nil
	}
	return fn(ctx, req)
}

func (f *fakeIdentityClient) SellToShop(ctx context.Context, req *identityv1.SellToShopRequest, _ ...grpc.CallOption) (*identityv1.SellToShopResponse, error) {
	f.mu.Lock()
	f.sellReqs = append(f.sellReqs, req)
	fn := f.sellToShopFn
	f.mu.Unlock()
	if fn == nil {
		return &identityv1.SellToShopResponse{Result: identityv1.SellResult_SELL_RESULT_INVALID_ITEM, NewZeny: 0}, nil
	}
	return fn(ctx, req)
}

func (f *fakeIdentityClient) ApplyLevelUp(ctx context.Context, req *identityv1.ApplyLevelUpRequest, _ ...grpc.CallOption) (*identityv1.ApplyLevelUpResponse, error) {
	f.mu.Lock()
	f.applyLevelUpReqs = append(f.applyLevelUpReqs, req)
	fn := f.applyLevelUpFn
	f.mu.Unlock()
	if fn == nil {
		return &identityv1.ApplyLevelUpResponse{Success: false}, nil
	}
	return fn(ctx, req)
}

func (f *fakeIdentityClient) AllocateStat(ctx context.Context, req *identityv1.AllocateStatRequest, _ ...grpc.CallOption) (*identityv1.AllocateStatResponse, error) {
	f.mu.Lock()
	f.allocateStatReqs = append(f.allocateStatReqs, req)
	fn := f.allocateStatFn
	f.mu.Unlock()
	if fn == nil {
		return &identityv1.AllocateStatResponse{Result: identityv1.StatResult_STAT_RESULT_INVALID_STAT}, nil
	}
	return fn(ctx, req)
}

// fakeZoneClient is the dispatch-test stand-in for
// zonev1.ZoneServiceClient. Tests install enterFn and moveFn to drive
// the per-RPC responses. Mirrors the hand-rolled fake pattern from
// internal/features/gateway/handler/map_ws_test.go so the dispatch
// tests stay trivially diffable against the gRPC interface.
type fakeZoneClient struct {
	mu       sync.Mutex
	enterFn  func(context.Context, *zonev1.EnterZoneRequest, ...grpc.CallOption) (*zonev1.EnterZoneResponse, error)
	moveFn   func(context.Context, *zonev1.MoveEntityRequest, ...grpc.CallOption) (*zonev1.MoveEntityResponse, error)
	moveReqs []*zonev1.MoveEntityRequest // captured MoveEntity calls (in arrival order)
}

func (f *fakeZoneClient) EnterZone(ctx context.Context, req *zonev1.EnterZoneRequest, opts ...grpc.CallOption) (*zonev1.EnterZoneResponse, error) {
	f.mu.Lock()
	fn := f.enterFn
	f.mu.Unlock()
	if fn == nil {
		return nil, status.Error(codes.Unimplemented, "no enter fn installed")
	}
	return fn(ctx, req, opts...)
}

func (f *fakeZoneClient) MoveEntity(ctx context.Context, req *zonev1.MoveEntityRequest, opts ...grpc.CallOption) (*zonev1.MoveEntityResponse, error) {
	f.mu.Lock()
	f.moveReqs = append(f.moveReqs, req)
	fn := f.moveFn
	f.mu.Unlock()
	if fn == nil {
		return nil, status.Error(codes.Unimplemented, "no move fn installed")
	}
	return fn(ctx, req, opts...)
}

func (f *fakeZoneClient) AttackEntity(ctx context.Context, req *zonev1.AttackEntityRequest, opts ...grpc.CallOption) (*zonev1.AttackEntityResponse, error) {
	return &zonev1.AttackEntityResponse{
		Success:       true,
		TargetDied:    false,
		DamageApplied: 10,
	}, nil
}

func (f *fakeZoneClient) PickupItem(ctx context.Context, req *zonev1.PickupItemRequest, opts ...grpc.CallOption) (*zonev1.PickupItemResponse, error) {
	return &zonev1.PickupItemResponse{
		Success: true,
		ItemId:  501,
		Amount:  1,
	}, nil
}

func (f *fakeZoneClient) RequestTrade(ctx context.Context, in *zonev1.RequestTradeRequest, opts ...grpc.CallOption) (*zonev1.RequestTradeResponse, error) {
	return &zonev1.RequestTradeResponse{Success: true, TradeId: "fake-trade-id"}, nil
}

func (f *fakeZoneClient) AddTradeItem(ctx context.Context, in *zonev1.AddTradeItemRequest, opts ...grpc.CallOption) (*zonev1.AddTradeItemResponse, error) {
	return &zonev1.AddTradeItemResponse{Success: true}, nil
}

func (f *fakeZoneClient) AddTradeZeny(ctx context.Context, in *zonev1.AddTradeZenyRequest, opts ...grpc.CallOption) (*zonev1.AddTradeZenyResponse, error) {
	return &zonev1.AddTradeZenyResponse{Success: true}, nil
}

func (f *fakeZoneClient) ConfirmTrade(ctx context.Context, in *zonev1.ConfirmTradeRequest, opts ...grpc.CallOption) (*zonev1.ConfirmTradeResponse, error) {
	return &zonev1.ConfirmTradeResponse{Success: true}, nil
}

func (f *fakeZoneClient) CompleteTrade(ctx context.Context, in *zonev1.CompleteTradeRequest, opts ...grpc.CallOption) (*zonev1.CompleteTradeResponse, error) {
	return &zonev1.CompleteTradeResponse{Success: true}, nil
}

func (f *fakeZoneClient) CancelTrade(ctx context.Context, in *zonev1.CancelTradeRequest, opts ...grpc.CallOption) (*zonev1.CancelTradeResponse, error) {
	return &zonev1.CancelTradeResponse{Success: true}, nil
}

func (f *fakeZoneClient) OpenStorage(ctx context.Context, in *zonev1.OpenStorageRequest, opts ...grpc.CallOption) (*zonev1.OpenStorageResponse, error) {
	return &zonev1.OpenStorageResponse{Success: true}, nil
}

func (f *fakeZoneClient) DepositItem(ctx context.Context, in *zonev1.DepositItemRequest, opts ...grpc.CallOption) (*zonev1.DepositItemResponse, error) {
	return &zonev1.DepositItemResponse{Success: true}, nil
}

func (f *fakeZoneClient) WithdrawItem(ctx context.Context, in *zonev1.WithdrawItemRequest, opts ...grpc.CallOption) (*zonev1.WithdrawItemResponse, error) {
	return &zonev1.WithdrawItemResponse{Success: true}, nil
}

func (f *fakeZoneClient) CloseStorage(ctx context.Context, in *zonev1.CloseStorageRequest, opts ...grpc.CallOption) (*zonev1.CloseStorageResponse, error) {
	return &zonev1.CloseStorageResponse{Success: true}, nil
}

// bufResponder captures every packet HandlePacket sends. Matched in
// parallel with the in-process dispatch under test.
type bufResponder struct {
	buf bytes.Buffer
}

func (b *bufResponder) SendPacket(p []byte) error {
	_, err := b.buf.Write(p)
	return err
}

// buildCALogin crafts a 55-byte CA_LOGIN frame.
func buildCALogin(t *testing.T, username, password string) []byte {
	t.Helper()
	const size = 55
	frame := make([]byte, size)
	binary.LittleEndian.PutUint16(frame[0:2], packet.HeaderCALOGIN)
	binary.LittleEndian.PutUint32(frame[2:6], 20250604)
	copy(frame[6:30], username)
	copy(frame[30:54], password)
	frame[54] = 0 // kRO client type
	return frame
}

func newDispatchTestLogger(t *testing.T) zerolog.Logger {
	t.Helper()
	return zerolog.New(zerolog.NewTestWriter(t)).Level(zerolog.Disabled)
}

// parChangeRecord captures the (varID, count) of a single ZC_PAR_CHANGE
// packet seen while sequentially walking a status-burst response.
type parChangeRecord struct {
	VarID uint16
	Count uint32
}

// parseStatusBurst walks a status-burst response buffer sequentially
// and returns the ZC_PAR_CHANGE records it contains, the ZC_STATUS
// payload, the ZC_LONGPAR_CHANGE count, the M10 empty list
// packet records, and the M14 NPC spawn (ZC_SET_UNIT_IDLE) records.
// This replaces the byte-by-byte header scan that could misfire when
// a payload value happened to match a packet header byte pair.
//
// Layout consumed: leading ZC_MAPPROPERTY_R2 (8 bytes) is skipped; then
// 0..N ZC_PAR_CHANGE / ZC_LONGPAR_CHANGE (8 bytes each), then exactly
// one ZC_STATUS (44 bytes), then the four M10 empty list packets
// (ZC_INVENTORY_ITEMLIST_NORMAL 0x00a3 / ZC_INVENTORY_ITEMLIST_EQUIP
// 0x00a4 / ZC_SKILLINFO_LIST 0x010f are 4 bytes each, and
// ZC_SHORTCUT_KEY_LIST 0x02b9 is 191 bytes), then 0..N M14 NPC spawn
// packets (ZC_SET_UNIT_IDLE 0x09ff, 107 bytes each). The buffer must
// be fully consumed.
func parseStatusBurst(t *testing.T, buf []byte) (
	parChanges []parChangeRecord,
	longParChanges int,
	statusPayload []byte,
	emptyListPackets []uint16,
	npcSpawnGIDs []uint32,
) {
	t.Helper()
	offset := 0
	for offset+2 <= len(buf) {
		cmd := binary.LittleEndian.Uint16(buf[offset:])
		switch cmd {
		case 0x099b: // ZC_MAPPROPERTY_R2
			if offset+8 > len(buf) {
				t.Fatalf("truncated ZC_MAPPROPERTY_R2 at offset %d (have %d bytes)", offset, len(buf)-offset)
			}
			offset += 8
		case 0x00b0: // ZC_PAR_CHANGE
			if offset+8 > len(buf) {
				t.Fatalf("truncated ZC_PAR_CHANGE at offset %d (have %d bytes)", offset, len(buf)-offset)
			}
			vid := binary.LittleEndian.Uint16(buf[offset+2 : offset+4])
			cnt := binary.LittleEndian.Uint32(buf[offset+4 : offset+8])
			parChanges = append(parChanges, parChangeRecord{VarID: vid, Count: cnt})
			offset += 8
		case 0x00b1: // ZC_LONGPAR_CHANGE
			if offset+8 > len(buf) {
				t.Fatalf("truncated ZC_LONGPAR_CHANGE at offset %d (have %d bytes)", offset, len(buf)-offset)
			}
			longParChanges++
			offset += 8
		case 0x00bd: // ZC_STATUS
			if offset+44 > len(buf) {
				t.Fatalf("truncated ZC_STATUS at offset %d (have %d bytes)", offset, len(buf)-offset)
			}
			statusPayload = buf[offset : offset+44]
			offset += 44
		case 0x00a3: // ZC_INVENTORY_ITEMLIST_NORMAL (M10, empty = 4 bytes)
			if offset+4 > len(buf) {
				t.Fatalf("truncated ZC_INVENTORY_ITEMLIST_NORMAL at offset %d (have %d bytes)", offset, len(buf)-offset)
			}
			plen := binary.LittleEndian.Uint16(buf[offset+2 : offset+4])
			if plen != 4 {
				t.Fatalf("ZC_INVENTORY_ITEMLIST_NORMAL packetLength = %d, want 4 (empty)", plen)
			}
			emptyListPackets = append(emptyListPackets, cmd)
			offset += int(plen)
		case 0x00a4: // ZC_INVENTORY_ITEMLIST_EQUIP (M10, empty = 4 bytes)
			if offset+4 > len(buf) {
				t.Fatalf("truncated ZC_INVENTORY_ITEMLIST_EQUIP at offset %d (have %d bytes)", offset, len(buf)-offset)
			}
			plen := binary.LittleEndian.Uint16(buf[offset+2 : offset+4])
			if plen != 4 {
				t.Fatalf("ZC_INVENTORY_ITEMLIST_EQUIP packetLength = %d, want 4 (empty)", plen)
			}
			emptyListPackets = append(emptyListPackets, cmd)
			offset += int(plen)
		case 0x010f: // ZC_SKILLINFO_LIST (M10/P3B, variable: 4-byte empty header + 37 bytes per skill)
			if offset+4 > len(buf) {
				t.Fatalf("truncated ZC_SKILLINFO_LIST at offset %d (have %d bytes)", offset, len(buf)-offset)
			}
			plen := binary.LittleEndian.Uint16(buf[offset+2 : offset+4])
			if plen != 4 && plen != 41 {
				t.Fatalf("ZC_SKILLINFO_LIST packetLength = %d, want 4 (empty) or 41 (NV_BASIC ×1)", plen)
			}
			emptyListPackets = append(emptyListPackets, cmd)
			offset += int(plen)
		case 0x02b9: // ZC_SHORTCUT_KEY_LIST (M10, fixed 191 bytes)
			const hotkeySize = 191
			if offset+hotkeySize > len(buf) {
				t.Fatalf("truncated ZC_SHORTCUT_KEY_LIST at offset %d (have %d bytes)", offset, len(buf)-offset)
			}
			emptyListPackets = append(emptyListPackets, cmd)
			offset += hotkeySize
		case 0x09ff: // ZC_SET_UNIT_IDLE (M14, fixed 107 bytes)
			const idleSize = 107
			if offset+idleSize > len(buf) {
				t.Fatalf("truncated ZC_SET_UNIT_IDLE at offset %d (have %d bytes)", offset, len(buf)-offset)
			}
			// AID at offset 5 (uint32 LE) is the NPC GID.
			gid := binary.LittleEndian.Uint32(buf[offset+5 : offset+9])
			npcSpawnGIDs = append(npcSpawnGIDs, gid)
			offset += idleSize
		default:
			t.Fatalf("unexpected packet header 0x%04x at offset %d (buf=% x)", cmd, offset, buf)
		}
	}
	if offset != len(buf) {
		t.Fatalf("trailing %d unparsed bytes at offset %d (buf=% x)", len(buf)-offset, offset, buf)
	}
	return parChanges, longParChanges, statusPayload, emptyListPackets, npcSpawnGIDs
}

// findParChange scans a (sequential) ZC_PAR_CHANGE list for the first
// record whose VarID matches; it returns the count and ok=true. Tests
// that only care about one specific value use this instead of indexing
// into the raw buffer.
func findParChange(records []parChangeRecord, varID uint16) (uint32, bool) {
	for _, r := range records {
		if r.VarID == varID {
			return r.Count, true
		}
	}
	return 0, false
}

func TestDispatchHandler_AcceptLogin_EncodesAccept(t *testing.T) {
	fake := &fakeIdentityClient{
		authenticateFn: func(_ context.Context, _ *identityv1.AuthenticateRequest) (*identityv1.AuthenticateResponse, error) {
			return &identityv1.AuthenticateResponse{
				Result:    identityv1.AuthResult_AUTH_RESULT_OK,
				AccountId: 42,
				LoginId1:  0x1111,
				LoginId2:  0x2222,
				LastIp:    "1.2.3.4",
				Sex:       "M",
				Token:     "abc",
				CharServers: []*identityv1.CharServerInfo{
					{Ip: "5.6.7.8", Port: 6121, Name: "RO-EP5", Users: 7, ServerType: 0},
				},
			}, nil
		},
	}
	h := NewDispatchHandler(fake, nil, 20250604, newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	conn := domain.ConnectionInfo{ID: 1, RemoteIP: "10.0.0.5:4321"}
	resp := &bufResponder{}
	frame := buildCALogin(t, "tester", "hunter2")

	if err := h.HandlePacket(context.Background(), &conn, resp, packet.HeaderCALOGIN, frame); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}

	out := resp.buf.Bytes()
	if len(out) == 0 {
		t.Fatal("nothing written to responder")
	}
	if out[0] != 0xc4 || out[1] != 0x0a {
		t.Fatalf("first two bytes = %x, want 0xc4 0x0a (AC_ACCEPT_LOGIN)", out[:2])
	}

	// PacketLength (little-endian uint16 at offset 2) must equal 224 for
	// one char_server entry: 64-byte header + 160-byte sub.
	if got := binary.LittleEndian.Uint16(out[2:4]); got != 224 {
		t.Fatalf("packetLength = %d, want 224 (64 + 160*1)", got)
	}

	// LoginID1 at offset 4 (uint32 LE) = 0x00001111.
	if got := binary.LittleEndian.Uint32(out[4:8]); got != 0x1111 {
		t.Fatalf("LoginID1 = 0x%x, want 0x1111", got)
	}
	// AID at offset 8 (uint32 LE) = 42.
	if got := binary.LittleEndian.Uint32(out[8:12]); got != 42 {
		t.Fatalf("AID = %d, want 42", got)
	}
	// LoginID2 at offset 12 (uint32 LE) = 0x00002222.
	if got := binary.LittleEndian.Uint32(out[12:16]); got != 0x2222 {
		t.Fatalf("LoginID2 = 0x%x, want 0x2222", got)
	}
	// acceptHeader layout from encode.go: cmd(2) + len(2) + login_id1(4) +
	// aid(4) + login_id2(4) + last_ip(4) + last_login(26) + sex(1) +
	// token(17) = 64 total. Sex byte is therefore at offset 46.
	if got := out[46]; got != 1 {
		t.Fatalf("sex byte at offset 46 = %d, want 1 (M)", got)
	}
}

func TestDispatchHandler_RefusedLogin_EncodesRefuse(t *testing.T) {
	fake := &fakeIdentityClient{
		authenticateFn: func(_ context.Context, _ *identityv1.AuthenticateRequest) (*identityv1.AuthenticateResponse, error) {
			return &identityv1.AuthenticateResponse{
				Result:    identityv1.AuthResult_AUTH_RESULT_REJECTED,
				ErrorCode: 1,
			}, nil
		},
	}
	h := NewDispatchHandler(fake, nil, 20250604, newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)
	resp := &bufResponder{}
	frame := buildCALogin(t, "tester", "wrongpw")

	if err := h.HandlePacket(context.Background(), &domain.ConnectionInfo{ID: 1}, resp, packet.HeaderCALOGIN, frame); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}

	out := resp.buf.Bytes()
	if len(out) != 26 {
		t.Fatalf("refuse length = %d, want 26", len(out))
	}
	if out[0] != 0x3e || out[1] != 0x08 {
		t.Fatalf("first two bytes = %x, want 0x3e 0x08 (AC_REFUSE_LOGIN)", out[:2])
	}
	if got := binary.LittleEndian.Uint32(out[2:6]); got != 1 {
		t.Fatalf("error code = %d, want 1", got)
	}
}

func TestDispatchHandler_IdentityDown_RefusesWithSentinel99(t *testing.T) {
	fake := &fakeIdentityClient{
		authenticateFn: func(_ context.Context, _ *identityv1.AuthenticateRequest) (*identityv1.AuthenticateResponse, error) {
			return nil, status.Error(codes.Unavailable, "identity service unreachable")
		},
	}
	h := NewDispatchHandler(fake, nil, 20250604, newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)
	resp := &bufResponder{}
	frame := buildCALogin(t, "tester", "pw")

	if err := h.HandlePacket(context.Background(), &domain.ConnectionInfo{ID: 1}, resp, packet.HeaderCALOGIN, frame); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}

	out := resp.buf.Bytes()
	if len(out) != 26 {
		t.Fatalf("refuse length = %d, want 26", len(out))
	}
	if got := binary.LittleEndian.Uint32(out[2:6]); got != ErrIdentityUnavailableRefuse {
		t.Fatalf("error code = %d, want %d (server-closed sentinel)", got, ErrIdentityUnavailableRefuse)
	}
}

func TestDispatchHandler_NilResponse_RefusesWithSentinel99(t *testing.T) {
	fake := &fakeIdentityClient{
		authenticateFn: func(_ context.Context, _ *identityv1.AuthenticateRequest) (*identityv1.AuthenticateResponse, error) {
			return nil, nil
		},
	}
	h := NewDispatchHandler(fake, nil, 20250604, newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)
	resp := &bufResponder{}
	frame := buildCALogin(t, "tester", "pw")

	if err := h.HandlePacket(context.Background(), &domain.ConnectionInfo{ID: 1}, resp, packet.HeaderCALOGIN, frame); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}

	out := resp.buf.Bytes()
	if len(out) != 26 {
		t.Fatalf("refuse length = %d, want 26", len(out))
	}
	if out[0] != 0x3e || out[1] != 0x08 {
		t.Fatalf("first two bytes = %x, want 0x3e 0x08 (AC_REFUSE_LOGIN)", out[:2])
	}
	if got := binary.LittleEndian.Uint32(out[2:6]); got != ErrIdentityUnavailableRefuse {
		t.Fatalf("error code = %d, want %d (server-closed sentinel)", got, ErrIdentityUnavailableRefuse)
	}
}

func TestDispatchHandler_CancelledContext_NoRefuseSent(t *testing.T) {
	fake := &fakeIdentityClient{
		authenticateFn: func(_ context.Context, _ *identityv1.AuthenticateRequest) (*identityv1.AuthenticateResponse, error) {
			return nil, status.Error(codes.Canceled, "client gone")
		},
	}
	h := NewDispatchHandler(fake, nil, 20250604, newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)
	resp := &bufResponder{}
	frame := buildCALogin(t, "tester", "pw")

	// Pre-cancel the context to also cover the ctx.Err() != nil branch
	// the handler checks alongside errors.Is(err, context.Canceled).
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := h.HandlePacket(ctx, &domain.ConnectionInfo{ID: 1}, resp, packet.HeaderCALOGIN, frame); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
	if got := resp.buf.Len(); got != 0 {
		t.Fatalf("responder wrote %d bytes on cancelled ctx, want 0 (no refuse)", got)
	}
}

func TestDispatchHandler_MalformedFrame_NoReplyNoError(t *testing.T) {
	authCalls := 0
	fake := &fakeIdentityClient{
		authenticateFn: func(_ context.Context, _ *identityv1.AuthenticateRequest) (*identityv1.AuthenticateResponse, error) {
			authCalls++
			return &identityv1.AuthenticateResponse{}, nil
		},
	}
	h := NewDispatchHandler(fake, nil, 20250604, newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)
	resp := &bufResponder{}

	// 10 bytes — well short of the 55-byte CA_LOGIN; ParseCALogin must
	// reject this without touching the identity RPC.
	short := []byte{0x64, 0x00, 0, 0, 0, 0, 0, 0, 0, 0}
	if err := h.HandlePacket(context.Background(), &domain.ConnectionInfo{ID: 1}, resp, packet.HeaderCALOGIN, short); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil (parse error must not tear conn down)", err)
	}
	if got := resp.buf.Len(); got != 0 {
		t.Fatalf("responder wrote %d bytes, want 0 (malformed frame must not reply)", got)
	}
	if authCalls != 0 {
		t.Fatalf("Authenticate called %d times on malformed frame, want 0", authCalls)
	}
}

func TestDispatchHandler_PassesClientIPStrippedToAuthenticate(t *testing.T) {
	var captured *identityv1.AuthenticateRequest
	fake := &fakeIdentityClient{
		authenticateFn: func(_ context.Context, req *identityv1.AuthenticateRequest) (*identityv1.AuthenticateResponse, error) {
			captured = req
			return &identityv1.AuthenticateResponse{
				Result:    identityv1.AuthResult_AUTH_RESULT_OK,
				AccountId: 1,
				LoginId1:  1,
				LoginId2:  1,
			}, nil
		},
	}
	h := NewDispatchHandler(fake, nil, 20250604, newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)
	resp := &bufResponder{}
	frame := buildCALogin(t, "alice", "pw")

	conn := domain.ConnectionInfo{ID: 1, RemoteIP: "203.0.113.7:54321"}
	if err := h.HandlePacket(context.Background(), &conn, resp, packet.HeaderCALOGIN, frame); err != nil {
		t.Fatalf("HandlePacket err = %v", err)
	}

	if captured == nil {
		t.Fatal("Authenticate not called")
	}
	if captured.ClientIp != "203.0.113.7" {
		t.Fatalf("ClientIp = %q, want 203.0.113.7 (host without port)", captured.ClientIp)
	}
	if captured.Method != identityv1.AuthMethod_AUTH_METHOD_PASSWORD {
		t.Fatalf("Method = %v, want AUTH_METHOD_PASSWORD", captured.Method)
	}
	if captured.Packetver != 20250604 {
		t.Fatalf("Packetver = %d, want 20250604", captured.Packetver)
	}
}

func TestSplitHost(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"203.0.113.7:54321", "203.0.113.7"},
		{"127.0.0.1:6900", "127.0.0.1"},
		{"[::1]:1234", "::1"},
		// no port — passthrough
		{"localhost", "localhost"},
		// empty — passthrough
		{"", ""},
	}
	for _, tc := range cases {
		if got := splitHost(tc.in); got != tc.want {
			t.Errorf("splitHost(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestParseIPv4(t *testing.T) {
	wantMapped := binary.BigEndian.Uint32([]byte{1, 2, 3, 4})
	cases := []struct {
		in   string
		want uint32
	}{
		{"1.2.3.4", 0x01020304},
		{"127.0.0.1", 0x7f000001},
		{"", 0},
		{"not-an-ip", 0},
		{"::1", 0}, // plain IPv6 rejected for the IPv4 wire slot
		{"1.2.3.4.5", 0},
		// IPv4-mapped IPv6 (dual-stack) must normalize to the embedded IPv4.
		{"::ffff:1.2.3.4", wantMapped},
	}
	for _, tc := range cases {
		if got := parseIPv4(tc.in); got != tc.want {
			t.Errorf("parseIPv4(%q) = 0x%x, want 0x%x", tc.in, got, tc.want)
		}
	}
	// Equivalence assertion from the spec fix: the mapped and bare forms
	// must collapse to the same wire value.
	if got := parseIPv4("::ffff:1.2.3.4"); got != parseIPv4("1.2.3.4") {
		t.Errorf("mapped IPv6 should equal bare IPv4: 0x%x vs 0x%x", got, parseIPv4("1.2.3.4"))
	}
}

func TestSexToByte(t *testing.T) {
	cases := []struct {
		in   string
		want uint8
	}{
		{"F", 0},
		{"M", 1},
		{"S", 2},
		{"", 0},
		{"X", 0},
	}
	for _, tc := range cases {
		if got := sexToByte(tc.in); got != tc.want {
			t.Errorf("sexToByte(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestResolveZoneIPv4_Literal(t *testing.T) {
	t.Parallel()

	got, err := resolveZoneIPv4("127.0.0.1")
	if err != nil {
		t.Fatalf("resolveZoneIPv4(127.0.0.1) err = %v, want nil", err)
	}
	// 127.0.0.1 → big-endian uint32 = 0x7f000001 = 2130706433.
	if got != 0x7f000001 {
		t.Errorf("resolveZoneIPv4(127.0.0.1) = 0x%x, want 0x7f000001", got)
	}
}

func TestResolveZoneIPv4_LocalhostHostname(t *testing.T) {
	t.Parallel()

	got, err := resolveZoneIPv4("localhost")
	if err != nil {
		t.Fatalf("resolveZoneIPv4(localhost) err = %v, want nil "+
			"(every CI host must resolve localhost)", err)
	}
	// On every system we run CI on, localhost resolves to 127.0.0.1
	// via /etc/hosts — the test asserts that, not the precise uint32,
	// so a future DNS-only env where localhost returns ::1 (IPv6-only)
	// would fail with a wrapped error from the IPv4 filter below.
	if got == 0 {
		t.Errorf("resolveZoneIPv4(localhost) = 0, want a non-zero IPv4 (got the old parseIPv4('localhost') bug)")
	}
	wantLoopback := uint32(0x7f000001)
	if got != wantLoopback {
		t.Errorf("resolveZoneIPv4(localhost) = 0x%x, want 0x%x (127.0.0.1)", got, wantLoopback)
	}
}

func TestResolveZoneIPv4_Unresolvable(t *testing.T) {
	t.Parallel()

	// ".invalid" is reserved by RFC 6761 §6.4 as a guaranteed-unresolvable
	// TLD. No DNS server may return A records for it, so the lookup must
	// fail deterministically without depending on network state.
	_, err := resolveZoneIPv4("nonexistent.invalid")
	if err == nil {
		t.Fatal("resolveZoneIPv4(nonexistent.invalid) err = nil, want error")
	}
}

func TestResolveZoneIPv4_Empty(t *testing.T) {
	t.Parallel()

	if _, err := resolveZoneIPv4(""); err == nil {
		t.Fatal("resolveZoneIPv4(\"\") err = nil, want error")
	}
}

func TestDispatchHandler_CHEnter_ClampsTotalSlotsAboveMax(t *testing.T) {
	t.Parallel()

	// Identity returns TotalSlots=300 — values above 255 must be
	// clamped to maxCharListCount (255), NOT silently truncated via
	// uint8 overflow (which would produce 300 mod 256 = 44, an
	// under-reported character budget that breaks the char-select UI).
	fake := &fakeIdentityClient{
		characterListFn: func(_ context.Context, _ *identityv1.GetCharacterListRequest) (*identityv1.GetCharacterListResponse, error) {
			return &identityv1.GetCharacterListResponse{
				Characters: []*identityv1.CharacterInfo{}, // zero chars exercises len fallback path
				TotalSlots: 300,
			}, nil
		},
	}
	h := NewDispatchHandler(fake, nil, 20250604, newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)
	resp := &bufResponder{}

	if err := h.HandlePacket(context.Background(), &domain.ConnectionInfo{ID: 1}, resp, packet.HeaderCHENTER, chEnterFrame(1)); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}

	out := resp.buf.Bytes()
	if len(out) < 9 {
		t.Fatalf("output length = %d, want ≥ 9 (4-byte AID echo + HC_ACCEPT_ENTER header)", len(out))
	}
	// Layout: [0:4] headerless AID echo, [4:6] cmd 0x6b 0x00,
	// [6:8] packetLength (uint16 LE), [8] total. The old truncation
	// bug wrote `total := uint8(300)` here, which overflows to 44
	// (300 mod 256) — the test asserts the clamp-to-255 behaviour.
	total := out[8]
	if total != maxCharListCount {
		t.Errorf("HC_ACCEPT_ENTER total byte = %d, want %d (clamped from 300); got %d "+
			"would mean the old uint8 truncation bug regressed", total, maxCharListCount, total)
	}
	if total == 44 {
		t.Fatalf("total byte = 44 — the previous uint8(300) overflow truncation bug regressed")
	}
}

// chEnterFrame builds a minimal 17-byte CH_ENTER frame for tests that
// only care about the response, not the request parse.
func chEnterFrame(accountID uint32) []byte {
	frame := make([]byte, 17)
	binary.LittleEndian.PutUint16(frame[0:2], packet.HeaderCHENTER)
	binary.LittleEndian.PutUint32(frame[2:6], accountID)
	return frame
}

// buildCZRequestMove crafts the 5-byte CZ_REQUEST_MOVE frame the rAthena
// client sends on each cell move (rathena/src/map/clif.cpp:11374).
//
// Layout: int16 packetType + uint8 dest[3] = 5. The dest[3] slot uses
// rAthena's kRO 3-byte packed position (clif.cpp:173-178 WBUFPOS); we
// hand-compute the bits inline because encodePos is unexported and the
// format is small enough that the math is the test, not the helper.
//
// Layout (LSB first):
//
//	p[0] = x >> 2
//	p[1] = ((x & 0x03) << 6) | (y >> 4)
//	p[2] = ((y & 0x0f) << 4)
func buildCZRequestMove(destX, destY int16) []byte {
	frame := make([]byte, 5)
	binary.LittleEndian.PutUint16(frame[0:2], packet.HeaderCZREQUESTMOVE)
	ux := uint16(destX)                             //nolint:gosec // mirror rAthena's int16 → uint16 bit reinterpret
	uy := uint16(destY)                             //nolint:gosec // ditto
	frame[2] = byte(ux >> 2)                        //nolint:gosec // C truncates to uint8 by &0xff
	frame[3] = byte(((ux & 0x03) << 6) | (uy >> 4)) //nolint:gosec // ditto
	frame[4] = byte((uy & 0x0f) << 4)               //nolint:gosec // ditto
	return frame
}

func TestDispatchHandler_CZRequestMove_NoAccountID_DropsSilently(t *testing.T) {
	t.Parallel()

	zone := &fakeZoneClient{
		moveFn: func(_ context.Context, _ *zonev1.MoveEntityRequest, _ ...grpc.CallOption) (*zonev1.MoveEntityResponse, error) {
			t.Fatal("MoveEntity must not be called when conn.AccountID is 0")
			return nil, nil
		},
	}
	h := NewDispatchHandler(&fakeIdentityClient{}, zone, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1} // AccountID deliberately unset
	if err := h.HandlePacket(context.Background(), &conn, resp, packet.HeaderCZREQUESTMOVE,
		buildCZRequestMove(165, 210)); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
	if got := resp.buf.Len(); got != 0 {
		t.Fatalf("responder wrote %d bytes, want 0 (no AccountID → no RPC → no reply)", got)
	}
	if got := len(zone.moveReqs); got != 0 {
		t.Fatalf("MoveEntity called %d times, want 0", got)
	}
}

func TestDispatchHandler_CZRequestMove_Success_EncodesZCNotifyPlayerMove(t *testing.T) {
	t.Parallel()

	zone := &fakeZoneClient{
		moveFn: func(_ context.Context, req *zonev1.MoveEntityRequest, _ ...grpc.CallOption) (*zonev1.MoveEntityResponse, error) {
			if req.AccountId != 4242 {
				t.Errorf("forwarded account_id = %d, want 4242", req.AccountId)
			}
			if req.DestX != 165 || req.DestY != 210 {
				t.Errorf("forwarded dest = (%d, %d), want (165, 210)", req.DestX, req.DestY)
			}
			return &zonev1.MoveEntityResponse{
				Success: true,
				SrcX:    150,
				SrcY:    200,
				DestX:   165,
				DestY:   210,
			}, nil
		},
	}
	h := NewDispatchHandler(&fakeIdentityClient{}, zone, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 4242}
	if err := h.HandlePacket(context.Background(), &conn, resp, packet.HeaderCZREQUESTMOVE,
		buildCZRequestMove(165, 210)); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}

	out := resp.buf.Bytes()
	if len(out) != 12 {
		t.Fatalf("ZC_NOTIFY_PLAYERMOVE length = %d, want 12", len(out))
	}
	if out[0] != 0x87 || out[1] != 0x00 {
		t.Fatalf("opcode = %02x %02x, want 87 00 (LE 0x0087)", out[0], out[1])
	}
	// moveStartTime at [2:6] is unix millis low 32 bits — assert it was
	// written (non-zero).
	if startTime := binary.LittleEndian.Uint32(out[2:6]); startTime == 0 {
		t.Errorf("moveStartTime = 0, want non-zero (millis since epoch)")
	}
	// srcPos[3] at [6:9] — decode via ParseCZRequestMove on the bytes
	// is not available (it's a S→C packet), so verify by re-running
	// the same kRO unpack as the encoder tests do, but inlined here
	// to avoid touching packet internals.
	gotSrcX := int16(uint16(out[6])<<2 | uint16(out[7])>>6)
	gotSrcY := int16(uint16(out[7]&0x3f)<<4 | uint16(out[8])>>4)
	if gotSrcX != 150 || gotSrcY != 200 || (out[8]&0x0f) != 0 {
		t.Errorf("srcPos = (%d, %d, dir=%d), want (150, 200, 0); bytes = %x",
			gotSrcX, gotSrcY, out[8]&0x0f, out[6:9])
	}
	// destPos[3] at [9:12].
	gotDestX := int16(uint16(out[9])<<2 | uint16(out[10])>>6)
	gotDestY := int16(uint16(out[10]&0x3f)<<4 | uint16(out[11])>>4)
	if gotDestX != 165 || gotDestY != 210 || (out[11]&0x0f) != 0 {
		t.Errorf("destPos = (%d, %d, dir=%d), want (165, 210, 0); bytes = %x",
			gotDestX, gotDestY, out[11]&0x0f, out[9:12])
	}
}

func TestDispatchHandler_CZRequestMove_ZoneRejects_NoReply(t *testing.T) {
	t.Parallel()

	zone := &fakeZoneClient{
		moveFn: func(_ context.Context, _ *zonev1.MoveEntityRequest, _ ...grpc.CallOption) (*zonev1.MoveEntityResponse, error) {
			return &zonev1.MoveEntityResponse{
				Success: false,
				Error:   "no walkable path",
			}, nil
		},
	}
	h := NewDispatchHandler(&fakeIdentityClient{}, zone, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 4242}
	if err := h.HandlePacket(context.Background(), &conn, resp, packet.HeaderCZREQUESTMOVE,
		buildCZRequestMove(165, 210)); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
	if got := resp.buf.Len(); got != 0 {
		t.Fatalf("responder wrote %d bytes on zone-rejected move, want 0", got)
	}
}

func TestDispatchHandler_CZRequestMove_ZoneGRPCError_NoReply(t *testing.T) {
	t.Parallel()

	zone := &fakeZoneClient{
		moveFn: func(_ context.Context, _ *zonev1.MoveEntityRequest, _ ...grpc.CallOption) (*zonev1.MoveEntityResponse, error) {
			return nil, status.Error(codes.Unavailable, "zone down")
		},
	}
	h := NewDispatchHandler(&fakeIdentityClient{}, zone, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 4242}
	if err := h.HandlePacket(context.Background(), &conn, resp, packet.HeaderCZREQUESTMOVE,
		buildCZRequestMove(165, 210)); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
	if got := resp.buf.Len(); got != 0 {
		t.Fatalf("responder wrote %d bytes on gRPC error, want 0", got)
	}
}

func TestDispatchHandler_CZRequestMove_NilZone_DropsSilently(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, nil, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 4242}
	if err := h.HandlePacket(context.Background(), &conn, resp, packet.HeaderCZREQUESTMOVE,
		buildCZRequestMove(165, 210)); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
	if got := resp.buf.Len(); got != 0 {
		t.Fatalf("responder wrote %d bytes with nil zone client, want 0", got)
	}
}

func TestDispatchHandler_CZEnter_Success_CachesAccountID(t *testing.T) {
	t.Parallel()

	zone := &fakeZoneClient{
		enterFn: func(_ context.Context, req *zonev1.EnterZoneRequest, _ ...grpc.CallOption) (*zonev1.EnterZoneResponse, error) {
			return &zonev1.EnterZoneResponse{
				Success: true,
				MapName: "prontera",
				MapX:    150,
				MapY:    200,
			}, nil
		},
	}
	identity := &fakeIdentityClient{
		getCharacterFn: func(_ context.Context, req *identityv1.GetCharacterRequest) (*identityv1.GetCharacterResponse, error) {
			// M7a: verify the gateway forwards the parsed
			// (accountID, charID) from the CZ_ENTER frame to
			// identity.GetCharacter.
			if req.GetAccountId() != 4242 || req.GetCharId() != 9001 {
				t.Errorf("GetCharacter req = (aid=%d, cid=%d), want (4242, 9001)",
					req.GetAccountId(), req.GetCharId())
			}
			return &identityv1.GetCharacterResponse{
				Success: true,
				Character: &identityv1.CharacterDetail{
					CharId:       9001,
					Name:         "alpha",
					ClassId:      7, // swordsman
					BaseLevel:    50,
					JobLevel:     25,
					Hp:           1234,
					MaxHp:        2000,
					Sp:           100,
					MaxSp:        200,
					Hair:         5,
					HairColor:    3,
					ClothesColor: 1,
					Weapon:       1101,
					Shield:       0,
					HeadTop:      0,
					HeadMid:      0,
					HeadBottom:   0,
					Robe:         0,
					Sex:          1,
				},
			}, nil
		},
	}
	h := NewDispatchHandler(identity, zone, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1}
	if err := h.HandlePacket(context.Background(), &conn, resp, packet.HeaderCZENTER,
		buildCZEnter(4242, 9001, 0xdead0000, 0xbeef0000, 1)); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
	if conn.AccountID != 4242 {
		t.Fatalf("after successful CZ_ENTER, conn.AccountID = %d, want 4242", conn.AccountID)
	}

	// On success the gateway must emit TWO packets back-to-back:
	// 13-byte ZC_ACCEPT_ENTER (cmd 0x02eb) + 107-byte ZC_SPAWN_UNIT
	// (cmd 0x09fe) for the player's own entity. Both must be present
	// in the responder buffer in that order.
	out := resp.buf.Bytes()
	const wantAcceptLen = 13
	const wantSpawnLen = 107
	if len(out) != wantAcceptLen+wantSpawnLen {
		t.Fatalf("responder length = %d, want %d (ZC_ACCEPT_ENTER + ZC_SPAWN_UNIT)",
			len(out), wantAcceptLen+wantSpawnLen)
	}

	// (1) ZC_ACCEPT_ENTER layout (no packet-length field — this is one of
	// the few map-server packets with no length header): cmd (2) +
	// startTime (4) + posDir[3] (3) + xSize (1) + ySize (1) + font (2)
	// = 13 bytes.
	accept := out[:wantAcceptLen]
	if accept[0] != 0xeb || accept[1] != 0x02 {
		t.Errorf("ZC_ACCEPT_ENTER opcode = %02x %02x, want eb 02 (LE 0x02eb)", accept[0], accept[1])
	}
	if startTime := binary.LittleEndian.Uint32(accept[2:6]); startTime == 0 {
		t.Errorf("ZC_ACCEPT_ENTER startTime = 0, want non-zero (unix seconds)")
	}
	// posDir at [6:9] must unpack to the zone-reported (150, 200) with
	// dir=0 (the handler hardcodes Dir=0 on the accept).
	accX := int16(uint16(accept[6])<<2 | uint16(accept[7])>>6)
	accY := int16(uint16(accept[7]&0x3f)<<4 | uint16(accept[8])>>4)
	if accX != 150 || accY != 200 || (accept[8]&0x0f) != 0 {
		t.Errorf("ZC_ACCEPT_ENTER posDir = (%d, %d, dir=%d), want (150, 200, 0)",
			accX, accY, accept[8]&0x0f)
	}
	if accept[9] != 5 || accept[10] != 5 {
		t.Errorf("ZC_ACCEPT_ENTER xSize/ySize = %d/%d, want 5/5", accept[9], accept[10])
	}

	// (2) ZC_SPAWN_UNIT: cmd 0x09fe LE at [0:2] + packetLength=107 at
	// [2:4] + objectType=0 (PC) at [4] + AID=4242 at [5:9] + GID=9001
	// (M7a: charID, not AID) at [9:13] + speed/bodyState/healthState
	// at [13:19] + job=7 at [23:25] + head=5 (hair style) at [25:27] +
	// weapon=1101 at [27:31] + shield=0 at [31:35] + posDir at
	// [63:66] = (150, 200, 0) + clevel=50 at [68:70] + maxHP=2000 at
	// [72:76] + HP=1234 at [76:80] + name "alpha" at [83:88] (5 bytes,
	// null-padded to 24).
	spawn := out[wantAcceptLen:]
	if spawn[0] != 0xfe || spawn[1] != 0x09 {
		t.Errorf("ZC_SPAWN_UNIT opcode = %02x %02x, want fe 09 (LE 0x09fe)", spawn[0], spawn[1])
	}
	if plen := binary.LittleEndian.Uint16(spawn[2:4]); plen != wantSpawnLen {
		t.Errorf("ZC_SPAWN_UNIT packetLength = %d, want %d", plen, wantSpawnLen)
	}
	if spawn[4] != 0 {
		t.Errorf("ZC_SPAWN_UNIT objectType = %d, want 0 (PC)", spawn[4])
	}
	if aid := binary.LittleEndian.Uint32(spawn[5:9]); aid != 4242 {
		t.Errorf("ZC_SPAWN_UNIT AID = %d, want 4242 (conn.AccountID)", aid)
	}
	if gid := binary.LittleEndian.Uint32(spawn[9:13]); gid != 9001 {
		t.Errorf("ZC_SPAWN_UNIT GID = %d, want 9001 (M7a: charID, not AID)", gid)
	}
	// Job at [23:25] (int16 LE) = 7 (swordsman).
	if job := int16(binary.LittleEndian.Uint16(spawn[23:25])); job != 7 {
		t.Errorf("ZC_SPAWN_UNIT job = %d, want 7 (swordsman)", job)
	}
	// Head at [25:27] (uint16 LE) = 5 (hair style).
	if head := binary.LittleEndian.Uint16(spawn[25:27]); head != 5 {
		t.Errorf("ZC_SPAWN_UNIT head = %d, want 5 (hair style from identity)", head)
	}
	// Weapon at [27:31] (uint32 LE) = 1101.
	if weapon := binary.LittleEndian.Uint32(spawn[27:31]); weapon != 1101 {
		t.Errorf("ZC_SPAWN_UNIT weapon = %d, want 1101", weapon)
	}
	// Shield at [31:35] (uint32 LE) = 0.
	if shield := binary.LittleEndian.Uint32(spawn[31:35]); shield != 0 {
		t.Errorf("ZC_SPAWN_UNIT shield = %d, want 0", shield)
	}
	// Sex byte at [62] must echo the identity CharacterDetail sex
	// byte (1 = male) — this is the proto-mapped value, not the
	// CZ_ENTER request byte.
	if spawn[62] != 1 {
		t.Errorf("ZC_SPAWN_UNIT sex = %d, want 1 (from identity CharacterDetail)", spawn[62])
	}
	// posDir at [63:66] must unpack to the zone-reported (150, 200) with
	// dir=0 (the handler hardcodes Dir=0 on the spawn too).
	spX := int16(uint16(spawn[63])<<2 | uint16(spawn[64])>>6)
	spY := int16(uint16(spawn[64]&0x3f)<<4 | uint16(spawn[65])>>4)
	if spX != 150 || spY != 200 || (spawn[65]&0x0f) != 0 {
		t.Errorf("ZC_SPAWN_UNIT posDir = (%d, %d, dir=%d), want (150, 200, 0); bytes = %x",
			spX, spY, spawn[65]&0x0f, spawn[63:66])
	}
	if spawn[66] != 5 || spawn[67] != 5 {
		t.Errorf("ZC_SPAWN_UNIT xSize/ySize = %d/%d, want 5/5", spawn[66], spawn[67])
	}
	// CLevel at [68:70] (int16 LE) = 50.
	if clevel := int16(binary.LittleEndian.Uint16(spawn[68:70])); clevel != 50 {
		t.Errorf("ZC_SPAWN_UNIT clevel = %d, want 50 (identity base_level)", clevel)
	}
	// MaxHP at [72:76] (int32 LE) = 2000.
	if maxhp := int32(binary.LittleEndian.Uint32(spawn[72:76])); maxhp != 2000 {
		t.Errorf("ZC_SPAWN_UNIT maxHP = %d, want 2000 (identity max_hp)", maxhp)
	}
	// HP at [76:80] (int32 LE) = 1234.
	if hp := int32(binary.LittleEndian.Uint32(spawn[76:80])); hp != 1234 {
		t.Errorf("ZC_SPAWN_UNIT HP = %d, want 1234 (identity hp)", hp)
	}
	// Name at [83:107]: "alpha" (5 bytes) followed by 19 NULs.
	if got := string(spawn[83:88]); got != "alpha" {
		t.Errorf("ZC_SPAWN_UNIT name = %q, want %q", got, "alpha")
	}
	for i := 88; i < 107; i++ {
		if spawn[i] != 0 {
			t.Errorf("ZC_SPAWN_UNIT name tail byte at [%d] = 0x%02x, want 0x00",
				i, spawn[i])
		}
	}
}

func TestDispatchHandler_CZEnter_IdentityFails_FallsBackToZeroSpawn(t *testing.T) {
	t.Parallel()

	zone := &fakeZoneClient{
		enterFn: func(_ context.Context, _ *zonev1.EnterZoneRequest, _ ...grpc.CallOption) (*zonev1.EnterZoneResponse, error) {
			return &zonev1.EnterZoneResponse{
				Success: true,
				MapName: "prontera",
				MapX:    150,
				MapY:    200,
			}, nil
		},
	}
	identity := &fakeIdentityClient{
		getCharacterFn: func(_ context.Context, _ *identityv1.GetCharacterRequest) (*identityv1.GetCharacterResponse, error) {
			return nil, status.Error(codes.Unavailable, "identity down")
		},
	}
	h := NewDispatchHandler(identity, zone, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1}
	if err := h.HandlePacket(context.Background(), &conn, resp, packet.HeaderCZENTER,
		buildCZEnter(4242, 9001, 0xdead0000, 0xbeef0000, 1)); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil (identity failure must not abort map enter)", err)
	}

	// Both packets must still be emitted — ZC_ACCEPT_ENTER followed by
	// the zero-fallback ZC_SPAWN_UNIT. The player is already in the
	// map; a missing or default sprite is preferable to a torn
	// connection.
	out := resp.buf.Bytes()
	const wantAcceptLen = 13
	const wantSpawnLen = 107
	if len(out) != wantAcceptLen+wantSpawnLen {
		t.Fatalf("responder length = %d, want %d", len(out), wantAcceptLen+wantSpawnLen)
	}
	spawn := out[wantAcceptLen:]
	// AID/GID still populated from the (cancelled-or-not) CZ_ENTER
	// data; only the character-specific fields are zero-filled.
	if aid := binary.LittleEndian.Uint32(spawn[5:9]); aid != 4242 {
		t.Errorf("ZC_SPAWN_UNIT AID = %d, want 4242", aid)
	}
	if gid := binary.LittleEndian.Uint32(spawn[9:13]); gid != 9001 {
		t.Errorf("ZC_SPAWN_UNIT GID = %d, want 9001 (charID)", gid)
	}
	// CLevel / MaxHP / HP must fall back to the pre-M7a defaults
	// (1/1/1) so the client renders a usable sprite instead of a
	// (job=0, hp=0) blank.
	if clevel := int16(binary.LittleEndian.Uint16(spawn[68:70])); clevel != 1 {
		t.Errorf("ZC_SPAWN_UNIT fallback clevel = %d, want 1", clevel)
	}
	if maxhp := int32(binary.LittleEndian.Uint32(spawn[72:76])); maxhp != 1 {
		t.Errorf("ZC_SPAWN_UNIT fallback maxHP = %d, want 1", maxhp)
	}
	if hp := int32(binary.LittleEndian.Uint32(spawn[76:80])); hp != 1 {
		t.Errorf("ZC_SPAWN_UNIT fallback HP = %d, want 1", hp)
	}
	// Name must be the empty string (24 NUL bytes).
	for i := 83; i < 107; i++ {
		if spawn[i] != 0 {
			t.Errorf("ZC_SPAWN_UNIT fallback name byte at [%d] = 0x%02x, want 0x00",
				i, spawn[i])
		}
	}
}

func TestDispatchHandler_CZEnter_IdentitySuccessFalse_FallsBackToZeroSpawn(t *testing.T) {
	t.Parallel()

	zone := &fakeZoneClient{
		enterFn: func(_ context.Context, _ *zonev1.EnterZoneRequest, _ ...grpc.CallOption) (*zonev1.EnterZoneResponse, error) {
			return &zonev1.EnterZoneResponse{
				Success: true,
				MapName: "prontera",
				MapX:    150,
				MapY:    200,
			}, nil
		},
	}
	identity := &fakeIdentityClient{
		getCharacterFn: func(_ context.Context, _ *identityv1.GetCharacterRequest) (*identityv1.GetCharacterResponse, error) {
			return &identityv1.GetCharacterResponse{
				Success: false,
				Error:   "character not found",
			}, nil
		},
	}
	h := NewDispatchHandler(identity, zone, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1}
	if err := h.HandlePacket(context.Background(), &conn, resp, packet.HeaderCZENTER,
		buildCZEnter(4242, 9001, 0xdead0000, 0xbeef0000, 1)); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil (success=false must not abort map enter)", err)
	}

	out := resp.buf.Bytes()
	const wantAcceptLen = 13
	const wantSpawnLen = 107
	if len(out) != wantAcceptLen+wantSpawnLen {
		t.Fatalf("responder length = %d, want %d", len(out), wantAcceptLen+wantSpawnLen)
	}
	spawn := out[wantAcceptLen:]
	if hp := int32(binary.LittleEndian.Uint32(spawn[76:80])); hp != 1 {
		t.Errorf("ZC_SPAWN_UNIT success=false fallback HP = %d, want 1", hp)
	}
}

func TestDispatchHandler_CZEnter_ZoneRejects_DoesNotCacheAccountID(t *testing.T) {
	t.Parallel()

	zone := &fakeZoneClient{
		enterFn: func(_ context.Context, _ *zonev1.EnterZoneRequest, _ ...grpc.CallOption) (*zonev1.EnterZoneResponse, error) {
			return &zonev1.EnterZoneResponse{Success: false, Error: "aoi grid full"}, nil
		},
	}
	h := NewDispatchHandler(&fakeIdentityClient{}, zone, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1}
	if err := h.HandlePacket(context.Background(), &conn, resp, packet.HeaderCZENTER,
		buildCZEnter(4242, 9001, 0xdead0000, 0xbeef0000, 1)); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
	if conn.AccountID != 0 {
		t.Fatalf("after rejected CZ_ENTER, conn.AccountID = %d, want 0 (cache must not stick)", conn.AccountID)
	}
}

// buildCZEnter crafts a 19-byte CZ_ENTER frame for the dispatch tests.
func buildCZEnter(accountID, charID, authCode, clientTime uint32, sex uint8) []byte {
	frame := make([]byte, 19)
	binary.LittleEndian.PutUint16(frame[0:2], packet.HeaderCZENTER)
	binary.LittleEndian.PutUint32(frame[2:6], accountID)
	binary.LittleEndian.PutUint32(frame[6:10], charID)
	binary.LittleEndian.PutUint32(frame[10:14], authCode)
	binary.LittleEndian.PutUint32(frame[14:18], clientTime)
	frame[18] = sex
	return frame
}

// CZ_NOTIFY_ACTORINIT (0x007d, 2 bytes — cmd-only) dispatch tests.

func TestDispatchHandler_CZNotifyActorInit_EncodesZCMapPropertyR2(t *testing.T) {
	t.Parallel()

	// No zone/identity calls expected — the handler responds with a
	// fixed MAPPROPERTY_NOTHING frame followed by the M9 status burst
	// (zero-valued because conn has no CharID set in this test).
	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 4242}

	// CZ_NOTIFY_ACTORINIT is cmd-only (2 bytes).
	frame := make([]byte, 2)
	binary.LittleEndian.PutUint16(frame[0:], packet.HeaderCZNOTIFYACTORINIT)

	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZNOTIFYACTORINIT, frame); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}

	out := resp.buf.Bytes()
	// M9: response now contains ZC_MAPPROPERTY_R2 (8) + the status
	// burst (12*ZC_PAR_CHANGE/ZC_LONGPAR_CHANGE + 1*ZC_STATUS = 12*8+44 = 140).
	// Assert the leading 8 bytes are still the map property packet and
	// that the rest is non-empty.
	if len(out) <= 8 {
		t.Fatalf("response length = %d, want > 8 (mapprop + status burst)", len(out))
	}

	// Opcode at [0:2] = 0x099b LE.
	if out[0] != 0x9b || out[1] != 0x09 {
		t.Fatalf("opcode = %02x %02x, want 9b 09 (LE 0x099b)", out[0], out[1])
	}
	// propertyType at [2:4] = uint16 LE = 0 (MAPPROPERTY_NOTHING).
	if pt := binary.LittleEndian.Uint16(out[2:4]); pt != 0 {
		t.Errorf("propertyType = %d, want 0 (MAPPROPERTY_NOTHING)", pt)
	}
	// flags at [4:8] = uint32 LE = 0.
	if flags := binary.LittleEndian.Uint32(out[4:8]); flags != 0 {
		t.Errorf("flags = 0x%x, want 0", flags)
	}
}

func TestDispatchHandler_CZNotifyActorInit_StatusBurst(t *testing.T) {
	t.Parallel()

	// Identity returns a fully-populated character; the handler must
	// emit ZC_MAPPROPERTY_R2 + a status burst that carries the real
	// values through to the wire.
	identity := &fakeIdentityClient{
		getCharacterFn: func(_ context.Context, req *identityv1.GetCharacterRequest) (*identityv1.GetCharacterResponse, error) {
			if req.GetAccountId() != 4242 || req.GetCharId() != 9001 {
				t.Errorf("GetCharacter req = (aid=%d, cid=%d), want (4242, 9001)",
					req.GetAccountId(), req.GetCharId())
			}
			return &identityv1.GetCharacterResponse{
				Success: true,
				Character: &identityv1.CharacterDetail{
					CharId:      9001,
					Name:        "alpha",
					ClassId:     7,
					BaseLevel:   50,
					JobLevel:    25,
					Hp:          1234,
					MaxHp:       2000,
					Sp:          100,
					MaxSp:       200,
					Str:         30,
					Agi:         20,
					Vit:         25,
					Int:         15,
					Dex:         40,
					Luk:         10,
					StatusPoint: 5,
					SkillPoint:  3,
				},
			}, nil
		},
	}
	h := NewDispatchHandler(identity, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 4242, CharID: 9001}
	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZNOTIFYACTORINIT, make([]byte, 2)); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}

	out := resp.buf.Bytes()

	// (1) First 8 bytes must be ZC_MAPPROPERTY_R2.
	if len(out) < 8 {
		t.Fatalf("responder wrote %d bytes, want ≥ 8 (mapprop + burst)", len(out))
	}
	if out[0] != 0x9b || out[1] != 0x09 {
		t.Errorf("first packet opcode = %02x %02x, want 9b 09 (LE 0x099b ZC_MAPPROPERTY_R2)", out[0], out[1])
	}

	// (2) Walk the rest of the buffer sequentially — no byte-by-byte
	// scan, so a payload byte that happens to match a packet header
	// cannot cause a false positive.
	parChanges, longParChanges, statusPayload, emptyListPackets, npcGIDs := parseStatusBurst(t, out[8:])

	// (3) Status burst must include exactly one ZC_STATUS (44 bytes).
	if len(statusPayload) != 44 {
		t.Errorf("ZC_STATUS payload = %d bytes, want 44", len(statusPayload))
	}

	// (4) Spot-check SP_HP = 1234.
	if cnt, ok := findParChange(parChanges, packet.SPHP); !ok {
		t.Errorf("no ZC_PAR_CHANGE with SP_HP found in burst")
	} else if cnt != 1234 {
		t.Errorf("ZC_PAR_CHANGE SP_HP = %d, want 1234", cnt)
	}

	// (5) Spot-check SP_STATUSPOINT = 5.
	if cnt, ok := findParChange(parChanges, packet.SPStatusPoint); !ok {
		t.Errorf("no ZC_PAR_CHANGE with SP_STATUSPOINT found in burst")
	} else if cnt != 5 {
		t.Errorf("ZC_PAR_CHANGE SP_STATUSPOINT = %d, want 5", cnt)
	}

	// (6) M9 adds long-param broadcasts (Zeny, StatusPoint, etc.) via
	// ZC_LONGPAR_CHANGE — there must be at least one.
	if longParChanges == 0 {
		t.Errorf("expected at least one ZC_LONGPAR_CHANGE in burst, got 0")
	}

	// (7) M10: the four empty list packets (inventory normal, inventory
	// equip, skill, hotkey) must follow the status burst in the
	// documented rAthena LoadEndAck order
	// (rathena/src/map/clif.cpp:10791-10915).
	wantEmpty := []uint16{
		0x00a3, // ZC_INVENTORY_ITEMLIST_NORMAL
		0x00a4, // ZC_INVENTORY_ITEMLIST_EQUIP
		0x010f, // ZC_SKILLINFO_LIST
		0x02b9, // ZC_SHORTCUT_KEY_LIST
	}
	if len(emptyListPackets) != len(wantEmpty) {
		t.Fatalf("empty-list packets seen = %d, want %d (opcodes: % x)",
			len(emptyListPackets), len(wantEmpty), emptyListPackets)
	}
	for i, want := range wantEmpty {
		if emptyListPackets[i] != want {
			t.Errorf("empty-list packet[%d] = 0x%04x, want 0x%04x", i, emptyListPackets[i], want)
		}
	}

	// (8) M14 + M17: NPC and monster spawn packets (ZC_SET_UNIT_IDLE,
	// 0x09ff) must follow the empty list packets. Four NPCs (GIDs
	// 110000001-110000004) are defined first, then four monsters
	// (GIDs 110000005-110000008).
	wantSpawnGIDs := []uint32{110000001, 110000002, 110000003, 110000004, 110000005, 110000006, 110000007, 110000008}
	if len(npcGIDs) != len(wantSpawnGIDs) {
		t.Fatalf("spawn packets seen = %d, want %d (GIDs: %v)",
			len(npcGIDs), len(wantSpawnGIDs), npcGIDs)
	}
	for i, want := range wantSpawnGIDs {
		if npcGIDs[i] != want {
			t.Errorf("spawn[%d] GID = %d, want %d", i, npcGIDs[i], want)
		}
	}
}

func TestDispatchHandler_CZNotifyActorInit_NoCharacter_FallsBackToZeros(t *testing.T) {
	t.Parallel()

	// Identity returns success=false → handler must still emit the
	// full burst with zero values (and HP clamped to 1 per rAthena).
	identity := &fakeIdentityClient{
		getCharacterFn: func(_ context.Context, _ *identityv1.GetCharacterRequest) (*identityv1.GetCharacterResponse, error) {
			return &identityv1.GetCharacterResponse{Success: false, Error: "character not found"}, nil
		},
	}
	h := NewDispatchHandler(identity, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 4242, CharID: 9001}
	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZNOTIFYACTORINIT, make([]byte, 2)); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}

	out := resp.buf.Bytes()
	if len(out) < 8 {
		t.Fatalf("responder wrote %d bytes, want ≥ 8", len(out))
	}
	if out[0] != 0x9b || out[1] != 0x09 {
		t.Errorf("first packet opcode = %02x %02x, want 9b 09 (ZC_MAPPROPERTY_R2)", out[0], out[1])
	}

	// Walk the burst sequentially so a payload byte that happens to
	// look like a packet header can't cause a false positive.
	parChanges, _, statusPayload, _, _ := parseStatusBurst(t, out[8:])

	// ZC_STATUS must be present and 44 bytes.
	if len(statusPayload) != 44 {
		t.Errorf("ZC_STATUS payload = %d bytes, want 44", len(statusPayload))
	}

	// Verify HP was clamped to 1 (rAthena convention).
	cnt, ok := findParChange(parChanges, packet.SPHP)
	if !ok {
		t.Errorf("no ZC_PAR_CHANGE with SP_HP found in fallback burst")
	} else if cnt != 1 {
		t.Errorf("fallback ZC_PAR_CHANGE SP_HP = %d, want 1 (rAthena clamps to min 1)", cnt)
	}
}

func TestDispatchHandler_CZNotifyActorInit_NoConnID_StillBurstsZeros(t *testing.T) {
	t.Parallel()

	// No AccountID/CharID on conn — the handler must still send the
	// burst with zeros. fetchCharacterByConn returns (nil, nil) without
	// calling identity.
	identity := &fakeIdentityClient{
		getCharacterFn: func(_ context.Context, _ *identityv1.GetCharacterRequest) (*identityv1.GetCharacterResponse, error) {
			t.Fatal("GetCharacter must not be called when conn has no AccountID/CharID")
			return nil, nil
		},
	}
	h := NewDispatchHandler(identity, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1} // AccountID/CharID deliberately 0
	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZNOTIFYACTORINIT, make([]byte, 2)); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}

	out := resp.buf.Bytes()
	if len(out) < 8+44 {
		t.Fatalf("responder wrote %d bytes, want ≥ 52 (mapprop + at least one ZC_STATUS)", len(out))
	}
	if out[0] != 0x9b || out[1] != 0x09 {
		t.Errorf("first packet opcode = %02x %02x, want 9b 09", out[0], out[1])
	}

	// Walk the burst sequentially; this verifies ZC_STATUS is
	// present and the burst is well-formed. M10 also asserts the
	// four empty-list packets follow in the documented rAthena
	// LoadEndAck order.
	_, _, statusPayload, emptyListPackets, _ := parseStatusBurst(t, out[8:])
	if len(statusPayload) != 44 {
		t.Errorf("ZC_STATUS payload = %d bytes, want 44", len(statusPayload))
	}
	wantEmpty := []uint16{0x00a3, 0x00a4, 0x010f, 0x02b9}
	if len(emptyListPackets) != len(wantEmpty) {
		t.Errorf("empty-list packets seen = %d, want %d (opcodes: % x)",
			len(emptyListPackets), len(wantEmpty), emptyListPackets)
	}
}

// CZ_REQUEST_TIME (0x007e, 6 bytes) dispatch tests.

func TestDispatchHandler_CZRequestTime_Success_EncodesZCNotifyTime(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 4242}

	req := packet.CZRequestTimeRequest{ClientTick: 0x12345678}
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode CZ_REQUEST_TIME: %v", err)
	}

	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZREQUESTTIME, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}

	out := resp.buf.Bytes()
	const wantLen = 6
	if len(out) != wantLen {
		t.Fatalf("ZC_NOTIFY_TIME length = %d, want %d", len(out), wantLen)
	}

	// Opcode at [0:2] = 0x007f LE.
	if out[0] != 0x7f || out[1] != 0x00 {
		t.Fatalf("opcode = %02x %02x, want 7f 00 (LE 0x007f)", out[0], out[1])
	}
	// time at [2:6] = uint32 LE — assert non-zero (real unix millis).
	if t1 := binary.LittleEndian.Uint32(out[2:6]); t1 == 0 {
		t.Errorf("time = 0, want non-zero (millis since epoch)")
	}
}

func TestDispatchHandler_CZRequestTime_MalformedFrame_DropsSilently(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 4242}

	// 2-byte frame is too short for CZ_REQUEST_TIME (size = 6) — must
	// be dropped silently without writing any reply.
	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZREQUESTTIME, make([]byte, 2)); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
	if got := resp.buf.Len(); got != 0 {
		t.Fatalf("responder wrote %d bytes on malformed frame, want 0", got)
	}
}

func TestDispatchHandler_CZRequestTime_NoPriorEnter_StillReplies(t *testing.T) {
	t.Parallel()

	// CZ_REQUEST_TIME has no AID dependency — the handler replies
	// even if the client never CZ_ENTERed, because the server-tick
	// ping is independent of zone state.
	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1} // AccountID deliberately 0

	req := packet.CZRequestTimeRequest{ClientTick: 0}
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode CZ_REQUEST_TIME: %v", err)
	}

	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZREQUESTTIME, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}

	out := resp.buf.Bytes()
	if len(out) != 6 {
		t.Fatalf("ZC_NOTIFY_TIME length = %d, want 6 (no AccountID check)", len(out))
	}
	if out[0] != 0x7f || out[1] != 0x00 {
		t.Errorf("opcode = %02x %02x, want 7f 00", out[0], out[1])
	}
}

func TestClampMapCoord(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   uint32
		want int16
	}{
		{"origin", 0, 0},
		{"typical_prontera", 512, 512},
		{"boundary", 1000, 1000},
		{"just_over_int16_max", 32768, 1000},
		{"buggy_zone_response", 40000, 1000},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := clampMapCoord(tc.in); got != tc.want {
				t.Errorf("clampMapCoord(%d) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}

	// Regression guard: a value above int16 max must not silently
	// wrap to a negative coordinate — that is the exact bug this
	// clamp exists to prevent.
	if got := clampMapCoord(40000); got < 0 {
		t.Fatalf("clampMapCoord(40000) = %d, must not be negative; the int16 overflow regression has returned", got)
	}
}

// TestDispatchHandler_CZGlobalMessage_Success_EncodesZCNotifyChat
// covers the M11 chat echo happy path: the dispatcher must read the
// message text verbatim from the incoming frame, stamp the GID slot
// with the connection's authenticated AID, and emit a ZC_NOTIFY_CHAT
// reply of the expected byte-exact layout.
func TestDispatchHandler_CZGlobalMessage_Success_EncodesZCNotifyChat(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	const wantAID uint32 = 4242
	conn := domain.ConnectionInfo{ID: 1, AccountID: wantAID}

	const wantMessage = "hello world"
	req := packet.CZGlobalMessageRequest{Message: wantMessage}
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode CZ_GLOBAL_MESSAGE: %v", err)
	}
	if _, err := reqBuf.Write([]byte{0x00}); err != nil { // explicit NUL terminator
		t.Fatalf("append NUL: %v", err)
	}

	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZGLOBALMESSAGE, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}

	out := resp.buf.Bytes()
	// 4 (header) + 4 (GID) + len("hello world") + 1 (NUL) = 4 + 4 + 11 + 1 = 20.
	const wantLen = 20
	if len(out) != wantLen {
		t.Fatalf("ZC_NOTIFY_CHAT length = %d, want %d (buf=% x)", len(out), wantLen, out)
	}
	// Opcode at [0:2] = 0x008d LE.
	if out[0] != 0x8d || out[1] != 0x00 {
		t.Fatalf("opcode = %02x %02x, want 8d 00 (LE 0x008d)", out[0], out[1])
	}
	// packetLength at [2:4] = 20 LE.
	if plen := binary.LittleEndian.Uint16(out[2:4]); plen != wantLen {
		t.Errorf("packetLength = %d, want %d", plen, wantLen)
	}
	// GID at [4:8] = wantAID (AID-as-GID stand-in).
	if gid := binary.LittleEndian.Uint32(out[4:8]); gid != wantAID {
		t.Errorf("GID = %d, want %d (AID echoed)", gid, wantAID)
	}
	// Message bytes at [8:19] = "hello world".
	if !bytes.Equal(out[8:19], []byte(wantMessage)) {
		t.Errorf("message = %q, want %q", out[8:19], wantMessage)
	}
	// NUL terminator at [19].
	if out[19] != 0 {
		t.Errorf("NUL terminator at [19] = 0x%02x, want 0x00", out[19])
	}
}

// TestDispatchHandler_CZGlobalMessage_MalformedFrame_DropsSilently
// ensures a truncated or otherwise malformed chat frame is dropped
// without writing any reply and without tearing the connection down
// (rAthena treats the same shape — the client retries after
// re-reading its addressbook).
func TestDispatchHandler_CZGlobalMessage_MalformedFrame_DropsSilently(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 4242}

	// 2-byte frame is shorter than the 4-byte CZ_GLOBAL_MESSAGE
	// header — must be dropped silently.
	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZGLOBALMESSAGE, make([]byte, 2)); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
	if got := resp.buf.Len(); got != 0 {
		t.Fatalf("responder wrote %d bytes on malformed frame, want 0", got)
	}
}

// TestDispatchHandler_CZActionRequest_Sit_EncodesZCNotifyAct
// covers the M18 sit path (action=2, DMG_SIT_DOWN). The handler must
// reply with a 34-byte ZC_NOTIFY_ACT carrying the AID as srcID,
// type=2 (sit), and all other fields zeroed.
func TestDispatchHandler_CZActionRequest_Sit_EncodesZCNotifyAct(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	const wantAID uint32 = 9999
	conn := domain.ConnectionInfo{ID: 1, AccountID: wantAID}

	req := packet.CZActionRequestRequest{TargetGID: wantAID, Action: packet.DMGSitDown}
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode CZ_ACTION_REQUEST: %v", err)
	}

	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZACTIONREQUEST, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}

	out := resp.buf.Bytes()
	const wantLen = 34
	if len(out) != wantLen {
		t.Fatalf("ZC_NOTIFY_ACT length = %d, want %d (buf=% x)", len(out), wantLen, out)
	}
	// Opcode at [0:2] = 0x08c8 LE.
	if out[0] != 0xc8 || out[1] != 0x08 {
		t.Fatalf("opcode = %02x %02x, want c8 08 (LE 0x08c8)", out[0], out[1])
	}
	// srcID at [2:6] = wantAID.
	if gid := binary.LittleEndian.Uint32(out[2:6]); gid != wantAID {
		t.Errorf("srcID = %d, want %d", gid, wantAID)
	}
	// type at [29] = 2 (sit).
	if out[29] != packet.DMGSitDown {
		t.Errorf("type = 0x%02x, want 0x%02x (sit)", out[29], packet.DMGSitDown)
	}
}

// TestDispatchHandler_CZActionRequest_Stand_EncodesZCNotifyAct
// covers the M18 stand path (action=3, DMG_STAND_UP).
func TestDispatchHandler_CZActionRequest_Stand_EncodesZCNotifyAct(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 1}

	req := packet.CZActionRequestRequest{TargetGID: 1, Action: packet.DMGStandUp}
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode CZ_ACTION_REQUEST: %v", err)
	}

	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZACTIONREQUEST, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}

	out := resp.buf.Bytes()
	if len(out) != 34 {
		t.Fatalf("ZC_NOTIFY_ACT length = %d, want 34 (buf=% x)", len(out), out)
	}
	if out[29] != packet.DMGStandUp {
		t.Errorf("type = 0x%02x, want 0x%02x (stand)", out[29], packet.DMGStandUp)
	}
}

// TestDispatchHandler_CZActionRequest_OutOfScopeAction_NoReply covers
// action codes that are silently dropped: 1 (pickup item), 4-6, 8-14.
// Attack actions (0, 7) are handled by the attack path; sit (2) and
// stand (3) by the sit/stand path.
func TestDispatchHandler_CZActionRequest_OutOfScopeAction_NoReply(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	for _, action := range []uint8{1, 4, 5, 6, 8, 9, 10, 11, 12, 13, 14} {
		action := action
		t.Run(fmt.Sprintf("action_%d", action), func(t *testing.T) {
			t.Parallel()

			resp := &bufResponder{}
			conn := domain.ConnectionInfo{ID: 1, AccountID: 42}

			req := packet.CZActionRequestRequest{TargetGID: 42, Action: action}
			var reqBuf bytes.Buffer
			if err := req.Encode(&reqBuf); err != nil {
				t.Fatalf("Encode CZ_ACTION_REQUEST: %v", err)
			}

			if err := h.HandlePacket(context.Background(), &conn, resp,
				packet.HeaderCZACTIONREQUEST, reqBuf.Bytes()); err != nil {
				t.Fatalf("HandlePacket err = %v, want nil", err)
			}
			if got := resp.buf.Len(); got != 0 {
				t.Fatalf("responder wrote %d bytes for action=%d, want 0 (drop)",
					got, action)
			}
		})
	}
}

// TestDispatchHandler_CZActionRequest_AttackMonster_EncodesNotifyAct
// covers the M18 attack path: action=0 (DMG_NORMAL) targeting a known
// monster GID. The handler must reply with a 34-byte ZC_NOTIFY_ACT
// carrying the damage value, and decrement the monster's HP.
func TestDispatchHandler_CZActionRequest_AttackMonster_EncodesNotifyAct(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	const wantAID uint32 = 200001
	conn := domain.ConnectionInfo{
		ID:        1,
		AccountID: wantAID,
		MonsterHP: map[uint32]int32{110000005: 50}, // Poring with 50 HP
	}
	conn.SetCombatStats(10, 5, 0)
	h.setDamageRoll(func(min, max int32) int32 { return min })

	req := packet.CZActionRequestRequest{TargetGID: 110000005, Action: packet.DMGNormal}
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode CZ_ACTION_REQUEST: %v", err)
	}

	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZACTIONREQUEST, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}

	out := resp.buf.Bytes()
	// Only ZC_NOTIFY_ACT (34 bytes) — monster has 50 HP, damage is 10,
	// so it survives (40 HP remaining). No ZC_NOTIFY_VANISH.
	if len(out) != 34 {
		t.Fatalf("response length = %d, want 34 (ZC_NOTIFY_ACT only; buf=% x)", len(out), out)
	}
	// Opcode = 0x08c8.
	if out[0] != 0xc8 || out[1] != 0x08 {
		t.Fatalf("opcode = %02x %02x, want c8 08 (LE 0x08c8)", out[0], out[1])
	}
	// srcID = AID.
	if src := binary.LittleEndian.Uint32(out[2:6]); src != wantAID {
		t.Errorf("srcID = %d, want %d", src, wantAID)
	}
	// targetID = monster GID.
	if tgt := binary.LittleEndian.Uint32(out[6:10]); tgt != 110000005 {
		t.Errorf("targetID = %d, want 110000005", tgt)
	}
	// damage = 12.
	if dmg := int32(binary.LittleEndian.Uint32(out[22:26])); dmg != 12 {
		t.Errorf("damage = %d, want 12", dmg)
	}
	// type = DMG_NORMAL (0).
	if out[29] != packet.DMGNormal {
		t.Errorf("type = 0x%02x, want 0x%02x (DMG_NORMAL)", out[29], packet.DMGNormal)
	}
	// HP should be decremented.
	if hp := conn.MonsterHP[110000005]; hp != 38 {
		t.Errorf("monster HP after attack = %d, want 38", hp)
	}
}

// TestDispatchHandler_Attack_StatDerivedDamage ensures that different
// attacker stats produce different, formula-correct damage values.
func TestDispatchHandler_Attack_StatDerivedDamage(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	type testCase struct {
		name     string
		str      uint8
		expected int32
	}
	cases := []testCase{
		{"low stats", 1, 1}, // Poring is Vit0
		{"high stats", 50, 75},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := &bufResponder{}
			conn := domain.ConnectionInfo{
				ID:        1,
				AccountID: 200001,
				MonsterHP: map[uint32]int32{110000005: 100},
			}
			conn.SetCombatStats(tc.str, 0, 0)

			// Force min damage to get deterministic result
			h.setDamageRoll(func(min, max int32) int32 { return min })

			req := packet.CZActionRequestRequest{TargetGID: 110000005, Action: packet.DMGNormal}
			var reqBuf bytes.Buffer
			_ = req.Encode(&reqBuf)

			_ = h.HandlePacket(context.Background(), &conn, resp, packet.HeaderCZACTIONREQUEST, reqBuf.Bytes())

			out := resp.buf.Bytes()
			dmg := int32(binary.LittleEndian.Uint32(out[22:26]))
			if dmg != tc.expected {
				t.Errorf("damage = %d, want %d", dmg, tc.expected)
			}
		})
	}
}

// TestDispatchHandler_CZActionRequest_KillMonster_EncodesNotifyActAndVanish
// covers the M18/M19 kill path: attacking a monster whose HP drops to 0
// must produce ZC_NOTIFY_ACT (34 bytes), ZC_NOTIFY_VANISH (7 bytes), and
// ZC_LONGPAR_CHANGE (8 bytes) for BaseExp and JobExp (total 57 bytes).
func TestDispatchHandler_CZActionRequest_KillMonster_EncodesNotifyActAndVanish(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{
		ID:        1,
		AccountID: 200001,
		MonsterHP: map[uint32]int32{110000005: 5}, // Poring with only 5 HP — one hit kills
		BaseExp:   10,                             // Started with 10 BaseExp
		JobExp:    5,                              // Started with 5 JobExp
	}
	conn.SetCombatStats(10, 5, 0)
	h.setDamageRoll(func(min, max int32) int32 { return min })

	req := packet.CZActionRequestRequest{TargetGID: 110000005, Action: packet.DMGNormal}
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode CZ_ACTION_REQUEST: %v", err)
	}

	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZACTIONREQUEST, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}

	out := resp.buf.Bytes()
	// ZC_NOTIFY_ACT (34) + ZC_NOTIFY_VANISH (7) + ZC_LONGPAR_CHANGE (8) + ZC_LONGPAR_CHANGE (8) = 57 bytes.
	if len(out) != 57 {
		t.Fatalf("response length = %d, want 57 (ZC_NOTIFY_ACT + ZC_NOTIFY_VANISH + 2x ZC_LONGPAR_CHANGE; buf=% x)", len(out), out)
	}
	// First packet: ZC_NOTIFY_ACT.
	if out[0] != 0xc8 || out[1] != 0x08 {
		t.Fatalf("first opcode = %02x %02x, want c8 08 (LE 0x08c8)", out[0], out[1])
	}
	// Second packet: ZC_NOTIFY_VANISH at offset 34.
	if out[34] != 0x80 || out[35] != 0x00 {
		t.Fatalf("second opcode = %02x %02x, want 80 00 (LE 0x0080)", out[34], out[35])
	}
	// Vanish GID = monster GID.
	if gid := binary.LittleEndian.Uint32(out[36:40]); gid != 110000005 {
		t.Errorf("vanish GID = %d, want 110000005", gid)
	}
	// Verify HP was deleted.
	if _, ok := conn.MonsterHP[110000005]; ok {
		t.Errorf("MonsterHP still has entry for 110000005, want deleted")
	}

	// Verify EXP accumulated. Poring gives 2 Base, 1 Job.
	// We started with 10 Base, 5 Job.
	if conn.BaseExp != 12 {
		t.Errorf("BaseExp = %d, want 12", conn.BaseExp)
	}
	if conn.JobExp != 6 {
		t.Errorf("JobExp = %d, want 6", conn.JobExp)
	}

	// Third packet: ZC_LONGPAR_CHANGE (BaseExp) at offset 41.
	if out[41] != 0xb1 || out[42] != 0x00 {
		t.Fatalf("third opcode = %02x %02x, want b1 00 (LE 0x00b1)", out[41], out[42])
	}
	// Fourth packet: ZC_LONGPAR_CHANGE (JobExp) at offset 49.
	if out[49] != 0xb1 || out[50] != 0x00 {
		t.Fatalf("fourth opcode = %02x %02x, want b1 00 (LE 0x00b1)", out[49], out[50])
	}
}

func TestDispatchHandler_CZActionRequest_AttackUnknownTarget_NoReply(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{
		ID:        1,
		AccountID: 42,
		MonsterHP: map[uint32]int32{110000005: 50},
	}

	// Attack an NPC GID (not in MonsterHP).
	req := packet.CZActionRequestRequest{TargetGID: 110000001, Action: packet.DMGNormal}
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode CZ_ACTION_REQUEST: %v", err)
	}

	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZACTIONREQUEST, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
	if got := resp.buf.Len(); got != 0 {
		t.Fatalf("responder wrote %d bytes for unknown target, want 0 (drop)", got)
	}
}

// TestDispatchHandler_CZActionRequest_MalformedFrame_DropsSilently
// covers the truncated-frame path: the dispatcher must drop the
// packet silently (no reply, no connection tear-down).
func TestDispatchHandler_CZActionRequest_MalformedFrame_DropsSilently(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 4242}

	// 2-byte frame is shorter than the 7-byte CZ_ACTION_REQUEST
	// fixed header.
	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZACTIONREQUEST, make([]byte, 2)); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
	if got := resp.buf.Len(); got != 0 {
		t.Fatalf("responder wrote %d bytes on malformed frame, want 0", got)
	}
}

// TestDispatchHandler_CZGlobalMessage_PreAuthGuard_DropsSilently
// ensures the pre-auth guard rejects chat from a connection that has
// not yet completed CZ_ENTER (conn.AccountID == 0). The handler must
// drop the packet silently — no ZC_NOTIFY_CHAT reply, no connection
// tear-down — mirroring the pattern in handleCZRequestMove.
func TestDispatchHandler_CZGlobalMessage_PreAuthGuard_DropsSilently(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	// AccountID is zero — the pre-auth guard must trip.
	conn := domain.ConnectionInfo{ID: 1}

	req := packet.CZGlobalMessageRequest{Message: "hello"}
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode CZ_GLOBAL_MESSAGE: %v", err)
	}

	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZGLOBALMESSAGE, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
	if got := resp.buf.Len(); got != 0 {
		t.Fatalf("responder wrote %d bytes for pre-auth chat, want 0 (drop)", got)
	}
}

// TestDispatchHandler_CZActionRequest_PreAuthGuard_DropsSilently
// ensures the pre-auth guard rejects sit/stand from a connection that
// has not yet completed CZ_ENTER. Mirrors the chat pre-auth test.
func TestDispatchHandler_CZActionRequest_PreAuthGuard_DropsSilently(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1}

	req := packet.CZActionRequestRequest{TargetGID: 1, Action: packet.DMGSitDown}
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode CZ_ACTION_REQUEST: %v", err)
	}

	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZACTIONREQUEST, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
	if got := resp.buf.Len(); got != 0 {
		t.Fatalf("responder wrote %d bytes for pre-auth action, want 0 (drop)", got)
	}
}

// TestDispatchHandler_CZChangeDir_EncodesZCChangeDir covers the M12
// direction-change happy path: the dispatcher must echo the client's
// head_dir + dir bytes verbatim, stamp the AID in the srcId slot, and
// emit a fixed 9-byte ZC_CHANGE_DIRECTION reply.
func TestDispatchHandler_CZChangeDir_EncodesZCChangeDir(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	const wantAID uint32 = 7777
	conn := domain.ConnectionInfo{ID: 1, AccountID: wantAID}

	const wantHead uint16 = 0x0002 // CCW
	const wantDir uint8 = 0x05     // SE
	req := packet.CZChangeDirRequest{HeadDir: wantHead, Dir: wantDir}
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode CZ_CHANGE_DIRECTION: %v", err)
	}

	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZCHANGEDIR, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}

	out := resp.buf.Bytes()
	const wantLen = 9
	if len(out) != wantLen {
		t.Fatalf("ZC_CHANGE_DIRECTION length = %d, want %d (buf=% x)", len(out), wantLen, out)
	}
	// Opcode at [0:2] = 0x009c LE.
	if out[0] != 0x9c || out[1] != 0x00 {
		t.Fatalf("opcode = %02x %02x, want 9c 00 (LE 0x009c)", out[0], out[1])
	}
	// srcId at [2:6] = wantAID (AID-as-srcId stand-in).
	if src := binary.LittleEndian.Uint32(out[2:6]); src != wantAID {
		t.Errorf("srcId = %d, want %d (AID echoed)", src, wantAID)
	}
	// headDir at [6:8] = wantHead.
	if hd := binary.LittleEndian.Uint16(out[6:8]); hd != wantHead {
		t.Errorf("headDir = 0x%x, want 0x%x", hd, wantHead)
	}
	// dir at [8] = wantDir.
	if out[8] != wantDir {
		t.Errorf("dir = 0x%02x, want 0x%02x", out[8], wantDir)
	}
}

// TestDispatchHandler_CZChangeDir_MalformedFrame_DropsSilently
// ensures a truncated or otherwise malformed direction-change frame is
// dropped without writing any reply and without tearing the connection
// down.
func TestDispatchHandler_CZChangeDir_MalformedFrame_DropsSilently(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 4242}

	// 2-byte frame is shorter than the 5-byte CZ_CHANGE_DIRECTION
	// fixed header — must be dropped silently.
	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZCHANGEDIR, make([]byte, 2)); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
	if got := resp.buf.Len(); got != 0 {
		t.Fatalf("responder wrote %d bytes on malformed frame, want 0", got)
	}
}

// TestDispatchHandler_CZChangeDir_PreAuthGuard_DropsSilently ensures
// the pre-auth guard rejects direction changes from a connection that
// has not yet completed CZ_ENTER (conn.AccountID == 0). Mirrors the
// chat / sit-stand pre-auth tests.
func TestDispatchHandler_CZChangeDir_PreAuthGuard_DropsSilently(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1}

	req := packet.CZChangeDirRequest{HeadDir: 0x0001, Dir: 0x04}
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode CZ_CHANGE_DIRECTION: %v", err)
	}

	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZCHANGEDIR, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
	if got := resp.buf.Len(); got != 0 {
		t.Fatalf("responder wrote %d bytes for pre-auth direction, want 0 (drop)", got)
	}
}

// TestDispatchHandler_CZReqEmotion_EncodesZCEmotion covers the M12
// emotion happy path: the dispatcher must echo the client's
// emotion_type byte verbatim, stamp the AID in the GID slot, and emit
// a fixed 7-byte ZC_EMOTION reply.
func TestDispatchHandler_CZReqEmotion_EncodesZCEmotion(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	const wantAID uint32 = 8888
	conn := domain.ConnectionInfo{ID: 1, AccountID: wantAID}

	const wantEmotion uint8 = 0x07 // ET_OK
	req := packet.CZReqEmotionRequest{EmotionType: wantEmotion}
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode CZ_REQ_EMOTION: %v", err)
	}

	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZREQEMOTION, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}

	out := resp.buf.Bytes()
	const wantLen = 7
	if len(out) != wantLen {
		t.Fatalf("ZC_EMOTION length = %d, want %d (buf=% x)", len(out), wantLen, out)
	}
	// Opcode at [0:2] = 0x00c0 LE.
	if out[0] != 0xc0 || out[1] != 0x00 {
		t.Fatalf("opcode = %02x %02x, want c0 00 (LE 0x00c0)", out[0], out[1])
	}
	// GID at [2:6] = wantAID (AID-as-GID stand-in).
	if gid := binary.LittleEndian.Uint32(out[2:6]); gid != wantAID {
		t.Errorf("GID = %d, want %d (AID echoed)", gid, wantAID)
	}
	// type at [6] = wantEmotion.
	if out[6] != wantEmotion {
		t.Errorf("type = 0x%02x, want 0x%02x", out[6], wantEmotion)
	}
}

// TestDispatchHandler_CZReqEmotion_MalformedFrame_DropsSilently
// ensures a truncated or otherwise malformed emotion frame is dropped
// without writing any reply and without tearing the connection down.
func TestDispatchHandler_CZReqEmotion_MalformedFrame_DropsSilently(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 4242}

	// 2-byte frame is shorter than the 3-byte CZ_REQ_EMOTION fixed
	// header — must be dropped silently.
	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZREQEMOTION, make([]byte, 2)); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
	if got := resp.buf.Len(); got != 0 {
		t.Fatalf("responder wrote %d bytes on malformed frame, want 0", got)
	}
}

// TestDispatchHandler_CZReqEmotion_PreAuthGuard_DropsSilently ensures
// the pre-auth guard rejects emotion requests from a connection that
// has not yet completed CZ_ENTER (conn.AccountID == 0). Mirrors the
// chat / sit-stand pre-auth tests.
func TestDispatchHandler_CZReqEmotion_PreAuthGuard_DropsSilently(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1}

	req := packet.CZReqEmotionRequest{EmotionType: 0x02}
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode CZ_REQ_EMOTION: %v", err)
	}

	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZREQEMOTION, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
	if got := resp.buf.Len(); got != 0 {
		t.Fatalf("responder wrote %d bytes for pre-auth emotion, want 0 (drop)", got)
	}
}

// TestDispatchHandler_CZGetCharNameRequest_EncodesZCAckReqName covers the
// M13 name-request happy path: the dispatcher must respond with
// ZC_ACK_REQNAME carrying the character name when the requested GID
// matches the player's own CharID.
func TestDispatchHandler_CZGetCharNameRequest_EncodesZCAckReqName(t *testing.T) {
	t.Parallel()

	fake := &fakeIdentityClient{
		getCharacterFn: func(_ context.Context, _ *identityv1.GetCharacterRequest) (*identityv1.GetCharacterResponse, error) {
			return &identityv1.GetCharacterResponse{
				Success: true,
				Character: &identityv1.CharacterDetail{
					Name: "TestChar",
				},
			}, nil
		},
	}
	h := NewDispatchHandler(fake, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	const wantAID uint32 = 100
	const wantCID uint32 = 200
	conn := domain.ConnectionInfo{ID: 1, AccountID: wantAID, CharID: wantCID}

	req := packet.CZGetCharNameRequestRequest{GID: wantCID}
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode CZ_GETCHARNAMEREQUEST: %v", err)
	}

	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZGETCHARNAMEREQUEST, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}

	out := resp.buf.Bytes()
	const wantLen = 30
	if len(out) != wantLen {
		t.Fatalf("ZC_ACK_REQNAME length = %d, want %d (buf=% x)", len(out), wantLen, out)
	}
	// Opcode at [0:2] = 0x0095 LE.
	if out[0] != 0x95 || out[1] != 0x00 {
		t.Fatalf("opcode = %02x %02x, want 95 00 (LE 0x0095)", out[0], out[1])
	}
	// GID at [2:6] = wantCID.
	if gid := binary.LittleEndian.Uint32(out[2:6]); gid != wantCID {
		t.Errorf("GID = %d, want %d", gid, wantCID)
	}
	// Name at [6:30] = "TestChar" + null padding.
	nameSlot := out[6:30]
	gotName := cstrBytes(nameSlot)
	if gotName != "TestChar" {
		t.Errorf("name = %q, want %q", gotName, "TestChar")
	}
}

// TestDispatchHandler_CZGetCharNameRequest_UnknownGID_EmptyName ensures
// the dispatcher responds with an empty name when the requested GID
// does not match the player's own CharID.
func TestDispatchHandler_CZGetCharNameRequest_UnknownGID_EmptyName(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 100, CharID: 200}

	// Request a GID that does not match the player's CharID.
	req := packet.CZGetCharNameRequestRequest{GID: 999}
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode CZ_GETCHARNAMEREQUEST: %v", err)
	}

	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZGETCHARNAMEREQUEST, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}

	out := resp.buf.Bytes()
	if len(out) != 30 {
		t.Fatalf("ZC_ACK_REQNAME length = %d, want 30", len(out))
	}
	// GID must be the requested GID, not the player's CharID.
	if gid := binary.LittleEndian.Uint32(out[2:6]); gid != 999 {
		t.Errorf("GID = %d, want 999", gid)
	}
	// Name must be empty (all null bytes).
	for i := 6; i < 30; i++ {
		if out[i] != 0 {
			t.Errorf("name byte[%d] = 0x%02x, want 0x00 (empty name)", i, out[i])
		}
	}
}

// TestDispatchHandler_CZGetCharNameRequest_MalformedFrame_DropsSilently
// ensures a truncated name-request frame is dropped without writing any
// reply and without tearing the connection down.
func TestDispatchHandler_CZGetCharNameRequest_MalformedFrame_DropsSilently(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 4242}

	// 2-byte frame is shorter than the 6-byte CZ_GETCHARNAMEREQUEST.
	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZGETCHARNAMEREQUEST, make([]byte, 2)); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
	if got := resp.buf.Len(); got != 0 {
		t.Fatalf("responder wrote %d bytes on malformed frame, want 0", got)
	}
}

// TestDispatchHandler_CZGetCharNameRequest_PreAuthGuard_DropsSilently
// ensures the pre-auth guard rejects name requests from a connection
// that has not yet completed CZ_ENTER.
func TestDispatchHandler_CZGetCharNameRequest_PreAuthGuard_DropsSilently(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1}

	req := packet.CZGetCharNameRequestRequest{GID: 42}
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode CZ_GETCHARNAMEREQUEST: %v", err)
	}

	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZGETCHARNAMEREQUEST, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
	if got := resp.buf.Len(); got != 0 {
		t.Fatalf("responder wrote %d bytes for pre-auth name request, want 0 (drop)", got)
	}
}

// TestDispatchHandler_CZRestart_TypeCharSelect_LoggedOnly ensures the
// dispatcher logs the restart request but does not write any reply
// (state transition to char select is deferred).
func TestDispatchHandler_CZRestart_TypeCharSelect_LoggedOnly(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 100}

	req := packet.CZRestartRequest{Type: 0x01}
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode CZ_RESTART: %v", err)
	}

	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZRESTART, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
	// No reply expected — restart is logged only.
	if got := resp.buf.Len(); got != 0 {
		t.Fatalf("responder wrote %d bytes for CZ_RESTART, want 0 (log only)", got)
	}
}

// TestDispatchHandler_CZRestart_TypeRespawn_LoggedOnly ensures the
// dispatcher logs the respawn request but does not write any reply.
func TestDispatchHandler_CZRestart_TypeRespawn_LoggedOnly(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 100}

	req := packet.CZRestartRequest{Type: 0x00}
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode CZ_RESTART: %v", err)
	}

	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZRESTART, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
	if got := resp.buf.Len(); got != 0 {
		t.Fatalf("responder wrote %d bytes for CZ_RESTART respawn, want 0 (log only)", got)
	}
}

// TestDispatchHandler_CZRestart_MalformedFrame_DropsSilently ensures a
// truncated restart frame is dropped without writing any reply.
func TestDispatchHandler_CZRestart_MalformedFrame_DropsSilently(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 4242}

	// 2-byte frame is shorter than the 3-byte CZ_RESTART.
	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZRESTART, make([]byte, 2)); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
	if got := resp.buf.Len(); got != 0 {
		t.Fatalf("responder wrote %d bytes on malformed frame, want 0", got)
	}
}

// TestDispatchHandler_CZRestart_PreAuthGuard_DropsSilently ensures the
// pre-auth guard rejects restart requests from a connection that has
// not yet completed CZ_ENTER.
func TestDispatchHandler_CZRestart_PreAuthGuard_DropsSilently(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1}

	req := packet.CZRestartRequest{Type: 0x01}
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode CZ_RESTART: %v", err)
	}

	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZRESTART, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
	if got := resp.buf.Len(); got != 0 {
		t.Fatalf("responder wrote %d bytes for pre-auth restart, want 0 (drop)", got)
	}
}

// cstrBytes returns the NUL-terminated prefix of b as a string, or the
// full slice if no NUL byte is present.
func cstrBytes(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		return string(b[:i])
	}
	return string(b)
}

// M15: NPC dialog interaction tests.

// TestDispatchHandler_CZContactNPC_ValidNPC_SendsDialog ensures the
// dispatcher sends ZC_SAY_DIALOG2 + ZC_WAIT_DIALOG2 when a known NPC
// GID is clicked.
func TestDispatchHandler_CZContactNPC_ValidNPC_SendsDialog(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 100}

	// Click on Kafra Employee (GID 110000001).
	req := packet.CZContactNPCRequest{AID: 110000001, Type: 0x01}
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode CZ_CONTACTNPC: %v", err)
	}

	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZCONTACTNPC, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}

	out := resp.buf.Bytes()
	if len(out) == 0 {
		t.Fatal("responder wrote 0 bytes, want ZC_SAY_DIALOG2 + ZC_WAIT_DIALOG2")
	}

	// First packet: ZC_SAY_DIALOG2 (0x0972).
	if out[0] != 0x72 || out[1] != 0x09 {
		t.Fatalf("first packet header = %02x %02x, want 72 09 (ZC_SAY_DIALOG2)", out[0], out[1])
	}
	sayLen := int(binary.LittleEndian.Uint16(out[2:4]))
	if sayLen < 10 || sayLen > len(out) {
		t.Fatalf("ZC_SAY_DIALOG2 packetLength = %d, want >= 10", sayLen)
	}
	if nid := binary.LittleEndian.Uint32(out[4:8]); nid != 110000001 {
		t.Errorf("ZC_SAY_DIALOG2 NpcID = %d, want 110000001", nid)
	}
	if out[8] != 0 {
		t.Errorf("ZC_SAY_DIALOG2 type = %d, want 0", out[8])
	}
	msg := cstrBytes(out[9:sayLen])
	if msg != "Welcome to goAthena! This is Kafra Employee." {
		t.Errorf("ZC_SAY_DIALOG2 message = %q, want %q", msg, "Welcome to goAthena! This is Kafra Employee.")
	}

	// Second packet: ZC_WAIT_DIALOG2 (0x0973), fixed 7 bytes.
	const zcWaitDialog2Size = 7
	waitStart := sayLen
	if len(out) < waitStart+zcWaitDialog2Size {
		t.Fatalf("output too short for ZC_WAIT_DIALOG2: got %d bytes, need %d", len(out), waitStart+zcWaitDialog2Size)
	}
	waitFrame := out[waitStart : waitStart+zcWaitDialog2Size]
	if waitFrame[0] != 0x73 || waitFrame[1] != 0x09 {
		t.Fatalf("second packet header = %02x %02x, want 73 09 (ZC_WAIT_DIALOG2)", waitFrame[0], waitFrame[1])
	}
	if nid := binary.LittleEndian.Uint32(waitFrame[2:6]); nid != 110000001 {
		t.Errorf("ZC_WAIT_DIALOG2 NpcID = %d, want 110000001", nid)
	}
	if waitFrame[6] != 0 {
		t.Errorf("ZC_WAIT_DIALOG2 type = %d, want 0", waitFrame[6])
	}
}

// TestDispatchHandler_CZContactNPC_UnknownNPC_NoResponse ensures the
// dispatcher does not write any response when the NPC GID is not found
// in npcSpawns.
func TestDispatchHandler_CZContactNPC_UnknownNPC_NoResponse(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 100}

	// Click on an unknown NPC GID.
	req := packet.CZContactNPCRequest{AID: 999999999, Type: 0x01}
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode CZ_CONTACTNPC: %v", err)
	}

	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZCONTACTNPC, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
	if got := resp.buf.Len(); got != 0 {
		t.Fatalf("responder wrote %d bytes for unknown NPC, want 0", got)
	}
}

// TestDispatchHandler_CZContactNPC_MalformedFrame_DropsSilently ensures
// a truncated contact-NPC frame is dropped without writing any reply.
func TestDispatchHandler_CZContactNPC_MalformedFrame_DropsSilently(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 4242}

	// 2-byte frame is shorter than the 7-byte CZ_CONTACTNPC.
	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZCONTACTNPC, make([]byte, 2)); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
	if got := resp.buf.Len(); got != 0 {
		t.Fatalf("responder wrote %d bytes on malformed frame, want 0", got)
	}
}

// TestDispatchHandler_CZContactNPC_PreAuthGuard_DropsSilently ensures
// the pre-auth guard rejects contact-NPC requests from a connection
// that has not yet completed CZ_ENTER.
func TestDispatchHandler_CZContactNPC_PreAuthGuard_DropsSilently(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1}

	req := packet.CZContactNPCRequest{AID: 110000001, Type: 0x01}
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode CZ_CONTACTNPC: %v", err)
	}

	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZCONTACTNPC, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
	if got := resp.buf.Len(); got != 0 {
		t.Fatalf("responder wrote %d bytes for pre-auth contact NPC, want 0 (drop)", got)
	}
}

// TestDispatchHandler_CZReqNextScript_SendsContinuation ensures the
// dispatcher sends ZC_SAY_DIALOG2 + ZC_CLOSE_DIALOG when the client
// clicks "Next".
func TestDispatchHandler_CZReqNextScript_SendsContinuation(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 100}

	req := packet.CZReqNextScriptRequest{NpcID: 110000001}
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode CZ_REQNEXTSCRIPT: %v", err)
	}

	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZREQNEXTSCRIPT, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}

	out := resp.buf.Bytes()
	if len(out) == 0 {
		t.Fatal("responder wrote 0 bytes, want ZC_SAY_DIALOG2 + ZC_CLOSE_DIALOG")
	}

	// First packet: ZC_SAY_DIALOG2 (0x0972).
	if out[0] != 0x72 || out[1] != 0x09 {
		t.Fatalf("first packet header = %02x %02x, want 72 09 (ZC_SAY_DIALOG2)", out[0], out[1])
	}
	sayLen := int(binary.LittleEndian.Uint16(out[2:4]))
	if sayLen < 10 || sayLen > len(out) {
		t.Fatalf("ZC_SAY_DIALOG2 packetLength = %d, want >= 10", sayLen)
	}
	if nid := binary.LittleEndian.Uint32(out[4:8]); nid != 110000001 {
		t.Errorf("ZC_SAY_DIALOG2 NpcID = %d, want 110000001", nid)
	}
	msg := cstrBytes(out[9:sayLen])
	if msg != "The server is under development. Enjoy exploring!" {
		t.Errorf("ZC_SAY_DIALOG2 message = %q, want %q", msg, "The server is under development. Enjoy exploring!")
	}

	// Second packet: ZC_CLOSE_DIALOG (0x00b6), fixed 6 bytes.
	const zcCloseDialogSize = 6
	closeStart := sayLen
	if len(out) < closeStart+zcCloseDialogSize {
		t.Fatalf("output too short for ZC_CLOSE_DIALOG: got %d bytes, need %d", len(out), closeStart+zcCloseDialogSize)
	}
	closeFrame := out[closeStart : closeStart+zcCloseDialogSize]
	if closeFrame[0] != 0xb6 || closeFrame[1] != 0x00 {
		t.Fatalf("second packet header = %02x %02x, want b6 00 (ZC_CLOSE_DIALOG)", closeFrame[0], closeFrame[1])
	}
	if nid := binary.LittleEndian.Uint32(closeFrame[2:6]); nid != 110000001 {
		t.Errorf("ZC_CLOSE_DIALOG NpcID = %d, want 110000001", nid)
	}
}

// TestDispatchHandler_CZReqNextScript_MalformedFrame_DropsSilently ensures
// a truncated next-script frame is dropped without writing any reply.
func TestDispatchHandler_CZReqNextScript_MalformedFrame_DropsSilently(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 4242}

	// 2-byte frame is shorter than the 6-byte CZ_REQNEXTSCRIPT.
	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZREQNEXTSCRIPT, make([]byte, 2)); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
	if got := resp.buf.Len(); got != 0 {
		t.Fatalf("responder wrote %d bytes on malformed frame, want 0", got)
	}
}

// TestDispatchHandler_CZReqNextScript_PreAuthGuard_DropsSilently ensures
// the pre-auth guard rejects next-script requests from a connection
// that has not yet completed CZ_ENTER.
func TestDispatchHandler_CZReqNextScript_PreAuthGuard_DropsSilently(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1}

	req := packet.CZReqNextScriptRequest{NpcID: 110000001}
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode CZ_REQNEXTSCRIPT: %v", err)
	}

	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZREQNEXTSCRIPT, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
	if got := resp.buf.Len(); got != 0 {
		t.Fatalf("responder wrote %d bytes for pre-auth next script, want 0 (drop)", got)
	}
}

// TestDispatchHandler_CZCloseDialog_NoResponse ensures the dispatcher
// does not write any response when the client closes the dialog.
func TestDispatchHandler_CZCloseDialog_NoResponse(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 100}

	req := packet.CZCloseDialogRequest{GID: 110000001}
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode CZ_CLOSE_DIALOG: %v", err)
	}

	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZCLOSEDIALOG, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
	if got := resp.buf.Len(); got != 0 {
		t.Fatalf("responder wrote %d bytes for close dialog, want 0 (no response)", got)
	}
}

// TestDispatchHandler_CZCloseDialog_MalformedFrame_DropsSilently ensures
// a truncated close-dialog frame is dropped without writing any reply.
func TestDispatchHandler_CZCloseDialog_MalformedFrame_DropsSilently(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 4242}

	// 2-byte frame is shorter than the 6-byte CZ_CLOSE_DIALOG.
	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZCLOSEDIALOG, make([]byte, 2)); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
	if got := resp.buf.Len(); got != 0 {
		t.Fatalf("responder wrote %d bytes on malformed frame, want 0", got)
	}
}

// TestDispatchHandler_CZCloseDialog_PreAuthGuard_DropsSilently ensures
// the pre-auth guard rejects close-dialog requests from a connection
// that has not yet completed CZ_ENTER.
func TestDispatchHandler_CZCloseDialog_PreAuthGuard_DropsSilently(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1}

	req := packet.CZCloseDialogRequest{GID: 110000001}
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode CZ_CLOSE_DIALOG: %v", err)
	}

	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZCLOSEDIALOG, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
	if got := resp.buf.Len(); got != 0 {
		t.Fatalf("responder wrote %d bytes for pre-auth close dialog, want 0 (drop)", got)
	}
}

// M16: NPC shop interaction tests.

// TestDispatchHandler_CZContactNPC_ShopNPC_SendsSelectDealtype ensures
// the dispatcher opens the deal-type window (ZC_SELECT_DEALTYPE) when a
// shop-type NPC is clicked. The Weapon Shop NPC (GID 110000002) is
// ShopType=1 with 4 stock items.
func TestDispatchHandler_CZContactNPC_ShopNPC_SendsSelectDealtype(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 100}

	req := packet.CZContactNPCRequest{AID: 110000002, Type: 0x01}
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode CZ_CONTACTNPC: %v", err)
	}

	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZCONTACTNPC, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}

	out := resp.buf.Bytes()
	const wantLen = 6 // sizeZCSelectDealtype
	if len(out) != wantLen {
		t.Fatalf("responder wrote %d bytes, want %d (ZC_SELECT_DEALTYPE)", len(out), wantLen)
	}

	// Header: 0x00c4 LE.
	if out[0] != 0xc4 || out[1] != 0x00 {
		t.Fatalf("header bytes = %02x %02x, want c4 00 (LE 0x00c4)", out[0], out[1])
	}
	if nid := binary.LittleEndian.Uint32(out[2:6]); nid != 110000002 {
		t.Errorf("NpcID = %d, want 110000002 (Weapon Shop)", nid)
	}
}

// TestDispatchHandler_CZAckSelectDealType_Buy_SendsPurchaseItemList
// ensures the dispatcher sends ZC_PC_PURCHASE_ITEMLIST with the NPC's
// stock list when the client picks "Buy" (type=0).
func TestDispatchHandler_CZAckSelectDealType_Buy_SendsPurchaseItemList(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 100}

	req := packet.CZAckSelectDealTypeRequest{NpcID: 110000002, Type: 0x00} // Buy
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode CZ_ACK_SELECT_DEALTYPE: %v", err)
	}

	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZACKSELECTDEALTYPE, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}

	out := resp.buf.Bytes()

	// Header: 0x0b77 LE.
	if out[0] != 0x77 || out[1] != 0x0b {
		t.Fatalf("header bytes = %02x %02x, want 77 0b (LE 0x0b77)", out[0], out[1])
	}
	plen := int(binary.LittleEndian.Uint16(out[2:4]))
	const wantItemCount = 4
	const shopBuyItemWireSize = 19 // per-item size in ZC_PC_PURCHASE_ITEMLIST
	wantLen := 4 + wantItemCount*shopBuyItemWireSize
	if plen != wantLen {
		t.Errorf("packetLength = %d, want %d (%d items × %d bytes + header)",
			plen, wantLen, wantItemCount, shopBuyItemWireSize)
	}
	if len(out) != wantLen {
		t.Fatalf("responder wrote %d bytes, want %d", len(out), wantLen)
	}

	// Spot-check the items match the Weapon Shop stock list.
	type wantItem struct {
		itemID uint32
		price  uint32
		itemTy uint8
		sprite uint16
		loc    uint32
	}
	wants := []wantItem{
		{501, 50, 0, 0, 0},
		{502, 200, 0, 0, 0},
		{1201, 500, 3, 1, 2},
		{1101, 1500, 3, 2, 2},
	}
	for i, w := range wants {
		off := 4 + i*shopBuyItemWireSize
		if id := binary.LittleEndian.Uint32(out[off : off+4]); id != w.itemID {
			t.Errorf("item[%d] itemId = %d, want %d", i, id, w.itemID)
		}
		if price := binary.LittleEndian.Uint32(out[off+4 : off+8]); price != w.price {
			t.Errorf("item[%d] price = %d, want %d", i, price, w.price)
		}
		if ty := out[off+12]; ty != w.itemTy {
			t.Errorf("item[%d] itemType = %d, want %d", i, ty, w.itemTy)
		}
		if sprite := binary.LittleEndian.Uint16(out[off+13 : off+15]); sprite != w.sprite {
			t.Errorf("item[%d] viewSprite = %d, want %d", i, sprite, w.sprite)
		}
		if loc := binary.LittleEndian.Uint32(out[off+15 : off+19]); loc != w.loc {
			t.Errorf("item[%d] location = %d, want %d", i, loc, w.loc)
		}
	}
}

// TestDispatchHandler_CZAckSelectDealType_Sell_NoResponse ensures the
// dispatcher does not write any response when the client picks "Sell"
// (type=1) — the sell flow is deferred until the inventory system
// lands.
func TestDispatchHandler_CZAckSelectDealType_Sell_NoResponse(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 100}

	req := packet.CZAckSelectDealTypeRequest{NpcID: 110000002, Type: 0x01} // Sell
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode CZ_ACK_SELECT_DEALTYPE: %v", err)
	}

	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZACKSELECTDEALTYPE, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
	if got := resp.buf.Len(); got != 0 {
		t.Fatalf("responder wrote %d bytes for Sell, want 0 (sell deferred)", got)
	}
}

// TestDispatchHandler_CZAckSelectDealType_Cancel_NoResponse ensures the
// dispatcher does not write any response when the client picks
// "Cancel" (type=2) — the client closes the deal window locally.
func TestDispatchHandler_CZAckSelectDealType_Cancel_NoResponse(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 100}

	req := packet.CZAckSelectDealTypeRequest{NpcID: 110000002, Type: 0x02} // Cancel
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode CZ_ACK_SELECT_DEALTYPE: %v", err)
	}

	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZACKSELECTDEALTYPE, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
	if got := resp.buf.Len(); got != 0 {
		t.Fatalf("responder wrote %d bytes for Cancel, want 0", got)
	}
}

// TestDispatchHandler_CZAckSelectDealType_UnknownNPC_NoResponse ensures
// the dispatcher does not write any response for a deal-type pick
// against an NPC GID that is not in npcSpawns.
func TestDispatchHandler_CZAckSelectDealType_UnknownNPC_NoResponse(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 100}

	req := packet.CZAckSelectDealTypeRequest{NpcID: 999999999, Type: 0x00}
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode CZ_ACK_SELECT_DEALTYPE: %v", err)
	}

	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZACKSELECTDEALTYPE, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
	if got := resp.buf.Len(); got != 0 {
		t.Fatalf("responder wrote %d bytes for unknown NPC, want 0", got)
	}
}

// TestDispatchHandler_CZAckSelectDealType_MalformedFrame_DropsSilently
// ensures a truncated deal-type frame is dropped without writing any
// reply.
func TestDispatchHandler_CZAckSelectDealType_MalformedFrame_DropsSilently(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 4242}

	// 2-byte frame is shorter than the 7-byte CZ_ACK_SELECT_DEALTYPE.
	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZACKSELECTDEALTYPE, make([]byte, 2)); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
	if got := resp.buf.Len(); got != 0 {
		t.Fatalf("responder wrote %d bytes on malformed frame, want 0", got)
	}
}

// TestDispatchHandler_CZAckSelectDealType_PreAuthGuard_DropsSilently
// ensures the pre-auth guard rejects deal-type picks from a connection
// that has not yet completed CZ_ENTER.
func TestDispatchHandler_CZAckSelectDealType_PreAuthGuard_DropsSilently(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1}

	req := packet.CZAckSelectDealTypeRequest{NpcID: 110000002, Type: 0x00}
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode CZ_ACK_SELECT_DEALTYPE: %v", err)
	}

	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZACKSELECTDEALTYPE, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
	if got := resp.buf.Len(); got != 0 {
		t.Fatalf("responder wrote %d bytes for pre-auth deal type, want 0 (drop)", got)
	}
}

// TestDispatchHandler_CZPCPurchaseItemList_EncodesZCPCPurchaseResult
// covers the P2B happy path: with the active shop NPC anchored via
// SetShopNPC, a valid catalog order returns result=0 + a zeny
// LongParChange. The previous "always result=0 stub" behaviour is
// obsolete; the contract is now that real zeny moves through the
// identity RPC and the helper result packet carries the outcome.
func TestDispatchHandler_CZPCPurchaseItemList_EncodesZCPCPurchaseResult(t *testing.T) {
	t.Parallel()

	identity := &fakeIdentityClient{
		buyFromShopFn: func(_ context.Context, _ *identityv1.BuyFromShopRequest) (*identityv1.BuyFromShopResponse, error) {
			return &identityv1.BuyFromShopResponse{Result: identityv1.BuyResult_BUY_RESULT_OK, NewZeny: 100}, nil
		},
	}
	h := NewDispatchHandler(identity, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 4242, CharID: 9001}
	conn.SetShopNPC(shopWeaponNpcGID)

	req := packet.CZPCPurchaseItemListRequest{
		Entries: []packet.CZPCPurchaseItemListEntry{
			{ItemID: 501, Amount: 1},
		},
	}
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode CZ_PC_PURCHASE_ITEMLIST: %v", err)
	}

	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZPCPURCHASEITEMLIST, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}

	out := resp.buf.Bytes()
	if len(out) < 3 {
		t.Fatalf("responder wrote %d bytes, want >= 3 (ZC_PC_PURCHASE_RESULT + ZC_LONGPAR_CHANGE)",
			len(out))
	}

	// Header: 0x00ca LE.
	if out[0] != 0xca || out[1] != 0x00 {
		t.Fatalf("header bytes = %02x %02x, want ca 00 (LE 0x00ca)", out[0], out[1])
	}
	// First packet result at [2] = 0 (success).
	if out[2] != 0x00 {
		t.Errorf("result byte = %d, want 0 (success)", out[2])
	}
	// Followed by a ZC_LONGPAR_CHANGE carrying the post-tx zeny
	// (NewZeny=100 from the fake identity client).
	cmd, payload := packetAt(t, out, 1)
	if cmd != packet.HeaderZCLONGPARCHANGE {
		t.Fatalf("packet[1] = 0x%04x, want 0x%04x (ZC_LONGPAR_CHANGE)",
			cmd, packet.HeaderZCLONGPARCHANGE)
	}
	if amt := int32(binary.LittleEndian.Uint32(payload[4:8])); amt != 100 {
		t.Errorf("LongParChange amount = %d, want 100", amt)
	}
}

// TestDispatchHandler_CZPCPurchaseItemList_MalformedFrame_DropsSilently
// ensures a truncated purchase-list frame is dropped without writing
// any reply. A 5-byte body (4-byte header + 1 stray byte) is
// misaligned and must be rejected by the parser.
func TestDispatchHandler_CZPCPurchaseItemList_MalformedFrame_DropsSilently(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 4242}

	// 4-byte header with 1 stray byte (5 total) — misaligned.
	frame := make([]byte, 5)
	binary.LittleEndian.PutUint16(frame[0:], packet.HeaderCZPCPURCHASEITEMLIST)
	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZPCPURCHASEITEMLIST, frame); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
	if got := resp.buf.Len(); got != 0 {
		t.Fatalf("responder wrote %d bytes on malformed frame, want 0", got)
	}
}

// TestDispatchHandler_CZPCPurchaseItemList_PreAuthGuard_DropsSilently
// ensures the pre-auth guard rejects purchase requests from a
// connection that has not yet completed CZ_ENTER.
func TestDispatchHandler_CZPCPurchaseItemList_PreAuthGuard_DropsSilently(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1}

	req := packet.CZPCPurchaseItemListRequest{
		Entries: []packet.CZPCPurchaseItemListEntry{
			{ItemID: 501, Amount: 1},
		},
	}
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode CZ_PC_PURCHASE_ITEMLIST: %v", err)
	}

	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZPCPURCHASEITEMLIST, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
	if got := resp.buf.Len(); got != 0 {
		t.Fatalf("responder wrote %d bytes for pre-auth purchase, want 0 (drop)", got)
	}
}

// TestDispatchHandler_CZNotifyActorInit_MonsterSpawn — M17: the
// CZ_NOTIFY_ACTORINIT response must include four monster spawn packets
// (ZC_SET_UNIT_IDLE 0x09ff, objectType=0x05) for Poring, Lunatic,
// Drops, and Spore with GIDs 110000005-110000008. The four NPC spawns
// (objectType=0x06) at GIDs 110000001-110000004 must still be present
// in the documented order (NPCs first, then monsters).
func TestDispatchHandler_CZNotifyActorInit_MonsterSpawn(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(
		&fakeIdentityClient{
			getCharacterFn: func(_ context.Context, _ *identityv1.GetCharacterRequest) (*identityv1.GetCharacterResponse, error) {
				return &identityv1.GetCharacterResponse{
					Success: true,
					Character: &identityv1.CharacterDetail{
						CharId: 9001, Name: "alpha", ClassId: 7, BaseLevel: 50, JobLevel: 25,
						Hp: 1234, MaxHp: 2000, Sp: 100, MaxSp: 200,
						Str: 30, Agi: 20, Vit: 25, Int: 15, Dex: 40, Luk: 10,
						StatusPoint: 5, SkillPoint: 3,
					},
				}, nil
			},
		},
		&fakeZoneClient{}, 20250604, newDispatchTestLogger(t),
		"prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil,
	)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 4242, CharID: 9001}
	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZNOTIFYACTORINIT, make([]byte, 2)); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
	out := resp.buf.Bytes()

	// Re-parse the burst to find the per-idle offsets. The
	// parseStatusBurst helper already knows the leading
	// non-idle layouts; we replay the same walk to capture the
	// starting offset of every ZC_SET_UNIT_IDLE frame.
	type idleRec struct {
		gid     uint32
		objType uint8
		hp      int32
		maxHP   int32
		job     int16
		speed   int16
		clevel  int16
		name    string
	}
	var idles []idleRec
	offset := 8 // skip leading 8-byte ZC_MAPPROPERTY_R2
	for offset+2 <= len(out) {
		cmd := binary.LittleEndian.Uint16(out[offset:])
		switch cmd {
		case 0x00b0, 0x00b1:
			offset += 8
		case 0x00bd:
			offset += 44
		case 0x00a3, 0x00a4, 0x010f:
			plen := int(binary.LittleEndian.Uint16(out[offset+2 : offset+4]))
			offset += plen
		case 0x02b9:
			offset += 191
		case 0x09ff:
			gid := binary.LittleEndian.Uint32(out[offset+5 : offset+9])
			objType := out[offset+4]
			speed := int16(binary.LittleEndian.Uint16(out[offset+13 : offset+15]))
			job := int16(binary.LittleEndian.Uint16(out[offset+23 : offset+25]))
			clevel := int16(binary.LittleEndian.Uint16(out[offset+68 : offset+70]))
			maxHP := int32(binary.LittleEndian.Uint32(out[offset+72 : offset+76]))
			hp := int32(binary.LittleEndian.Uint32(out[offset+76 : offset+80]))
			name := string(out[offset+83 : offset+83+24])
			if idx := strings.IndexByte(name, 0); idx >= 0 {
				name = name[:idx]
			}
			idles = append(idles, idleRec{
				gid: gid, objType: objType, hp: hp, maxHP: maxHP,
				job: job, speed: speed, clevel: clevel, name: name,
			})
			offset += 107
		default:
			t.Fatalf("unexpected packet header 0x%04x at offset %d (buf=% x)", cmd, offset, out)
		}
	}
	if offset != len(out) {
		t.Fatalf("trailing %d unparsed bytes at offset %d (buf=% x)", len(out)-offset, offset, out)
	}

	wantMonsters := []struct {
		gid     uint32
		name    string
		hp      int32
		maxHP   int32
		job     int16
		speed   int16
		clevel  int16
		objType uint8
	}{
		{gid: 110000005, name: "Poring", hp: 50, maxHP: 50, job: 1002, speed: 400, clevel: 1, objType: 0x05},
		{gid: 110000006, name: "Lunatic", hp: 60, maxHP: 60, job: 1063, speed: 400, clevel: 3, objType: 0x05},
		{gid: 110000007, name: "Drops", hp: 55, maxHP: 55, job: 1113, speed: 400, clevel: 3, objType: 0x05},
		{gid: 110000008, name: "Spore", hp: 510, maxHP: 510, job: 1014, speed: 400, clevel: 16, objType: 0x05},
	}
	if len(idles) != 8 {
		t.Fatalf("ZC_SET_UNIT_IDLE packet count = %d, want 8 (4 NPC + 4 monster)", len(idles))
	}
	// First 4 must be NPCs (objectType=0x06), next 4 must be monsters
	// (objectType=0x05).
	for i := 0; i < 4; i++ {
		if idles[i].objType != 0x06 {
			t.Errorf("idle[%d] objectType = 0x%02x, want 0x06 (NPC_EVT); gid=%d", i, idles[i].objType, idles[i].gid)
		}
	}
	for i, want := range wantMonsters {
		got := idles[4+i]
		if got.objType != want.objType {
			t.Errorf("monster[%d] objectType = 0x%02x, want 0x%02x", i, got.objType, want.objType)
		}
		if got.gid != want.gid {
			t.Errorf("monster[%d] GID = %d, want %d", i, got.gid, want.gid)
		}
		if got.name != want.name {
			t.Errorf("monster[%d] name = %q, want %q", i, got.name, want.name)
		}
		if got.hp != want.hp {
			t.Errorf("monster[%d] HP = %d, want %d", i, got.hp, want.hp)
		}
		if got.maxHP != want.maxHP {
			t.Errorf("monster[%d] MaxHP = %d, want %d", i, got.maxHP, want.maxHP)
		}
		if got.job != want.job {
			t.Errorf("monster[%d] job/spriteID = %d, want %d", i, got.job, want.job)
		}
		if got.speed != want.speed {
			t.Errorf("monster[%d] speed = %d, want %d", i, got.speed, want.speed)
		}
		if got.clevel != want.clevel {
			t.Errorf("monster[%d] clevel = %d, want %d", i, got.clevel, want.clevel)
		}
	}
}

// TestDispatchHandler_CZNotifyActorInit_MonsterCount — M17: the burst
// must contain exactly 8 ZC_SET_UNIT_IDLE packets (4 NPCs from M14
// plus 4 monsters). Simple count of opcodes 0x09ff in the response.
func TestDispatchHandler_CZNotifyActorInit_MonsterCount(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(
		&fakeIdentityClient{
			getCharacterFn: func(_ context.Context, _ *identityv1.GetCharacterRequest) (*identityv1.GetCharacterResponse, error) {
				return &identityv1.GetCharacterResponse{
					Success: true,
					Character: &identityv1.CharacterDetail{
						CharId: 9001, Name: "alpha", ClassId: 7, BaseLevel: 50, JobLevel: 25,
						Hp: 1234, MaxHp: 2000, Sp: 100, MaxSp: 200,
						Str: 30, Agi: 20, Vit: 25, Int: 15, Dex: 40, Luk: 10,
						StatusPoint: 5, SkillPoint: 3,
					},
				}, nil
			},
		},
		&fakeZoneClient{}, 20250604, newDispatchTestLogger(t),
		"prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil,
	)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 4242, CharID: 9001}
	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZNOTIFYACTORINIT, make([]byte, 2)); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
	out := resp.buf.Bytes()

	idleCount := 0
	for offset := 0; offset+2 <= len(out); {
		cmd := binary.LittleEndian.Uint16(out[offset:])
		if cmd == 0x09ff {
			idleCount++
			offset += 107
			continue
		}
		// Skip non-idle packets using their known lengths.
		switch cmd {
		case 0x099b:
			offset += 8
		case 0x00b0, 0x00b1:
			offset += 8
		case 0x00bd:
			offset += 44
		case 0x00a3, 0x00a4, 0x010f:
			if offset+4 > len(out) {
				t.Fatalf("truncated empty list packet 0x%04x at offset %d", cmd, offset)
			}
			plen := int(binary.LittleEndian.Uint16(out[offset+2 : offset+4]))
			offset += plen
		case 0x02b9:
			offset += 191
		default:
			t.Fatalf("unexpected packet 0x%04x at offset %d (buf=% x)", cmd, offset, out)
		}
	}
	if idleCount != 8 {
		t.Fatalf("ZC_SET_UNIT_IDLE packet count = %d, want 8 (4 NPC + 4 monster)", idleCount)
	}
}

type safeResponder struct {
	mu      sync.Mutex
	packets [][]byte
}

func (s *safeResponder) SendPacket(p []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.packets = append(s.packets, p)
	return nil
}

func (s *safeResponder) GetPackets() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([][]byte, len(s.packets))
	copy(out, s.packets)
	return out
}

func TestDispatchHandler_Attack_MonsterRespawns(t *testing.T) {
	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)
	h.respawnDelay = 20 * time.Millisecond

	resp := &safeResponder{}
	conn := domain.ConnectionInfo{
		ID:        1,
		AccountID: 200001,
	}
	conn.SetCombatStats(10, 5, 0)
	h.setDamageRoll(func(min, max int32) int32 { return min })
	conn.InitMonsterHP([]domain.MonsterSpawn{{GID: 110000005, MaxHP: 5}}) // Poring with only 5 HP — kills it

	req := packet.CZActionRequestRequest{TargetGID: 110000005, Action: packet.DMGNormal}
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode CZ_ACTION_REQUEST: %v", err)
	}

	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZACTIONREQUEST, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}

	// Verify the monster is dead and vanish packet is sent immediately
	// For checking HP without raw access, we can try to apply 0 damage and if it returns ok=false it means the monster was removed.
	if _, ok := conn.ApplyDamage(110000005, 0); ok {
		t.Fatal("monster should be dead (removed from MonsterHP)")
	}

	// Wait for respawn (up to 1s)
	var respawnPkt []byte
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		// If monster is respawned, ApplyDamage with 0 damage will succeed and return 50 (maxHP)
		if hp, ok := conn.ApplyDamage(110000005, 0); ok && hp == 50 {
			// Found respawned monster with full HP!
			// Check packets
			pkts := resp.GetPackets()
			for _, p := range pkts {
				if len(p) >= 2 && binary.LittleEndian.Uint16(p[0:2]) == 0x09ff {
					// Check if it is the correct AID
					if len(p) >= 9 && binary.LittleEndian.Uint32(p[5:9]) == 110000005 {
						respawnPkt = p
						break
					}
				}
			}
			if respawnPkt != nil {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
	}

	if respawnPkt == nil {
		t.Fatal("timed out waiting for monster respawn or respawn packet")
	}

	// Verify respawn packet fields:
	// objectType at offset 4 must be 0x05 (NPC_MOB_TYPE)
	if respawnPkt[4] != 0x05 {
		t.Errorf("respawn packet objectType = %d, want 0x05", respawnPkt[4])
	}
	// GID at [5:9] is 110000005
	if gid := binary.LittleEndian.Uint32(respawnPkt[5:9]); gid != 110000005 {
		t.Errorf("respawn packet GID = %d, want 110000005", gid)
	}
	// Job (sprite ID) at [23:25] must be 1002 (Poring)
	if sprite := int16(binary.LittleEndian.Uint16(respawnPkt[23:25])); sprite != 1002 {
		t.Errorf("respawn packet sprite ID = %d, want 1002", sprite)
	}
	// MaxHP at [72:76] must be 50
	if maxHP := int32(binary.LittleEndian.Uint32(respawnPkt[72:76])); maxHP != 50 {
		t.Errorf("respawn packet maxHP = %d, want 50", maxHP)
	}
	// HP at [76:80] must be 50
	if hp := int32(binary.LittleEndian.Uint32(respawnPkt[76:80])); hp != 50 {
		t.Errorf("respawn packet HP = %d, want 50", hp)
	}
}

func TestDispatchHandler_Attack_Concurrency(t *testing.T) {
	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &safeResponder{}
	conn := domain.ConnectionInfo{
		ID:        1,
		AccountID: 200001,
	}
	conn.SetCombatStats(10, 5, 0)
	h.setDamageRoll(func(min, max int32) int32 { return min })
	conn.InitMonsterHP([]domain.MonsterSpawn{{GID: 110000005, MaxHP: 10000}}) // Poring with large HP to prevent death in this test

	req := packet.CZActionRequestRequest{TargetGID: 110000005, Action: packet.DMGNormal}
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode CZ_ACTION_REQUEST: %v", err)
	}

	var wg sync.WaitGroup
	const workers = 10
	const iterations = 50
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				_ = h.HandlePacket(context.Background(), &conn, resp,
					packet.HeaderCZACTIONREQUEST, reqBuf.Bytes())
			}
		}()
	}
	wg.Wait()

	hp, ok := conn.ApplyDamage(110000005, 0)
	if !ok {
		t.Fatal("expected monster to exist")
	}

	expectedHP := int32(10000 - workers*iterations*12)
	if hp != expectedHP {
		t.Errorf("monster HP = %d, want %d", hp, expectedHP)
	}
}

// P2A: inventory list emission on CZ_NOTIFY_ACTORINIT. The handler
// must call identity.GetInventory and emit ZC_INVENTORY_ITEMLIST_NORMAL
// (0x00a3) followed by ZC_INVENTORY_ITEMLIST_EQUIP (0x00a4) in the
// documented rAthena LoadEndAck order
// (rathena/src/map/clif.cpp:10791-10915). Items with
// inventory.equip==0 go to the normal list, items with non-zero
// equip go to the equip list.
func TestDispatchHandler_CZNotifyActorInit_EmitsInventoryLists(t *testing.T) {
	t.Parallel()

	identity := &fakeIdentityClient{
		getCharacterFn: func(_ context.Context, _ *identityv1.GetCharacterRequest) (*identityv1.GetCharacterResponse, error) {
			return &identityv1.GetCharacterResponse{
				Success: true,
				Character: &identityv1.CharacterDetail{
					CharId: 9001, Name: "alpha", ClassId: 7, BaseLevel: 50, JobLevel: 25,
					Hp: 1234, MaxHp: 2000, Sp: 100, MaxSp: 200,
					Str: 30, Agi: 20, Vit: 25, Int: 15, Dex: 40, Luk: 10,
					StatusPoint: 5, SkillPoint: 3,
				},
			}, nil
		},
		getInventoryFn: func(_ context.Context, req *identityv1.GetInventoryRequest) (*identityv1.GetInventoryResponse, error) {
			if req.GetAccountId() != 4242 || req.GetCharId() != 9001 {
				t.Errorf("GetInventory req = (aid=%d, cid=%d), want (4242, 9001)",
					req.GetAccountId(), req.GetCharId())
			}
			return &identityv1.GetInventoryResponse{
				Items: []*identityv1.InventoryItem{
					{Id: 2, Nameid: 0xAB, Amount: 10, Identify: 1},                           // Red Potion (in grid)
					{Id: 3, Nameid: 0xC8, Amount: 5, Identify: 1},                            // Blue Potion (in grid)
					{Id: 9, Nameid: 0x538, Amount: 1, Identify: 1, Refine: 2, Equip: 0x0002}, // Knife (equipped, EQP_HAND_R)
				},
			}, nil
		},
	}
	h := NewDispatchHandler(identity, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 4242, CharID: 9001}
	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZNOTIFYACTORINIT, make([]byte, 2)); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}

	out := resp.buf.Bytes()

	// Walk the burst sequentially, recording the ZC_INVENTORY_ITEMLIST_NORMAL
	// and ZC_INVENTORY_ITEMLIST_EQUIP frames by their variable-length packetLength.
	const wantNormalItems = 2
	const wantEquipItems = 1
	const normalItemSize = 26 // sizeNormalItem
	const equipItemSize = 57  // sizeEquipItem
	var normalLen, equipLen int

	offset := 8 // skip leading 8-byte ZC_MAPPROPERTY_R2
	for offset+2 <= len(out) {
		cmd := binary.LittleEndian.Uint16(out[offset:])
		switch cmd {
		case 0x00b0, 0x00b1:
			offset += 8
		case 0x00bd:
			offset += 44
		case 0x00a3:
			plen := int(binary.LittleEndian.Uint16(out[offset+2 : offset+4]))
			normalLen = plen
			offset += plen
		case 0x00a4:
			plen := int(binary.LittleEndian.Uint16(out[offset+2 : offset+4]))
			equipLen = plen
			offset += plen
		case 0x010f:
			plen := int(binary.LittleEndian.Uint16(out[offset+2 : offset+4]))
			offset += plen
		case 0x02b9:
			offset += 191
		case 0x09ff:
			offset += 107
		default:
			t.Fatalf("unexpected packet 0x%04x at offset %d (buf=% x)", cmd, offset, out)
		}
	}

	wantNormalLen := 4 + wantNormalItems*normalItemSize
	if normalLen != wantNormalLen {
		t.Errorf("ZC_INVENTORY_ITEMLIST_NORMAL length = %d, want %d (2 normal items × 26 + 4 header)",
			normalLen, wantNormalLen)
	}
	wantEquipLen := 4 + wantEquipItems*equipItemSize
	if equipLen != wantEquipLen {
		t.Errorf("ZC_INVENTORY_ITEMLIST_EQUIP length = %d, want %d (1 equip item × 57 + 4 header)",
			equipLen, wantEquipLen)
	}
}

// P2A: when identity.GetInventory returns a gRPC error during the
// LoadEndAck burst, the dispatcher must fall back to the empty
// inventory list — the burst must still complete end-to-end so the
// client initialises its inventory grid.
func TestDispatchHandler_CZNotifyActorInit_InventoryRPCError_FallsBackToEmpty(t *testing.T) {
	t.Parallel()

	identity := &fakeIdentityClient{
		getCharacterFn: func(_ context.Context, _ *identityv1.GetCharacterRequest) (*identityv1.GetCharacterResponse, error) {
			return &identityv1.GetCharacterResponse{
				Success: true,
				Character: &identityv1.CharacterDetail{
					CharId: 9001, Name: "alpha", ClassId: 7, BaseLevel: 50, JobLevel: 25,
					Hp: 1234, MaxHp: 2000, Sp: 100, MaxSp: 200,
				},
			}, nil
		},
		getInventoryFn: func(_ context.Context, _ *identityv1.GetInventoryRequest) (*identityv1.GetInventoryResponse, error) {
			return nil, status.Error(codes.Unavailable, "identity down")
		},
	}
	h := NewDispatchHandler(identity, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 4242, CharID: 9001}
	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZNOTIFYACTORINIT, make([]byte, 2)); err != nil {
		t.Fatalf("HandlePacket err = %v", err)
	}
	// Walk the burst and assert the two inventory list frames are
	// the 4-byte empty stubs.
	normalEmpty := false
	equipEmpty := false
	offset := 8
	for offset+2 <= len(resp.buf.Bytes()) {
		cmd := binary.LittleEndian.Uint16(resp.buf.Bytes()[offset:])
		switch cmd {
		case 0x00b0, 0x00b1:
			offset += 8
		case 0x00bd:
			offset += 44
		case 0x00a3:
			plen := int(binary.LittleEndian.Uint16(resp.buf.Bytes()[offset+2 : offset+4]))
			if plen == 4 {
				normalEmpty = true
			}
			offset += plen
		case 0x00a4:
			plen := int(binary.LittleEndian.Uint16(resp.buf.Bytes()[offset+2 : offset+4]))
			if plen == 4 {
				equipEmpty = true
			}
			offset += plen
		case 0x010f:
			plen := int(binary.LittleEndian.Uint16(resp.buf.Bytes()[offset+2 : offset+4]))
			offset += plen
		case 0x02b9:
			offset += 191
		case 0x09ff:
			offset += 107
		}
	}
	if !normalEmpty {
		t.Errorf("ZC_INVENTORY_ITEMLIST_NORMAL not emitted as empty (4-byte) frame")
	}
	if !equipEmpty {
		t.Errorf("ZC_INVENTORY_ITEMLIST_EQUIP not emitted as empty (4-byte) frame")
	}
}

// P2A: CZ_USE_ITEM2 happy path — the dispatcher must call
// identity.UseItem and emit ZC_USE_ITEM_ACK2 (0x01c8) with the
// remaining stack count and result=1.
func TestDispatchHandler_CZUseItem_Success_EncodesAck(t *testing.T) {
	t.Parallel()

	var captured *identityv1.UseItemRequest
	identity := &fakeIdentityClient{
		useItemFn: func(_ context.Context, req *identityv1.UseItemRequest) (*identityv1.UseItemResponse, error) {
			captured = req
			if req.GetAccountId() != 4242 || req.GetCharId() != 9001 {
				t.Errorf("UseItem req = (aid=%d, cid=%d), want (4242, 9001)",
					req.GetAccountId(), req.GetCharId())
			}
			if req.GetItemId() != 2 {
				t.Errorf("UseItem req item_id = %d, want 2 (inventory index)", req.GetItemId())
			}
			return &identityv1.UseItemResponse{
				Success:         true,
				ItemId:          0xAB,
				RemainingAmount: 9,
			}, nil
		},
	}
	h := NewDispatchHandler(identity, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 4242, CharID: 9001}
	conn.SetInventoryIndex(map[uint16]uint32{0: 2})
	req := packet.CZUseItemRequest{Index: 2, AID: 4242}
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode CZ_USE_ITEM2: %v", err)
	}
	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZUSEITEM2, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
	if captured == nil {
		t.Fatal("UseItem RPC not called")
	}

	out := resp.buf.Bytes()
	const wantLen = 13
	if len(out) != wantLen {
		t.Fatalf("ZC_USE_ITEM_ACK2 length = %d, want %d", len(out), wantLen)
	}
	if out[0] != 0xc8 || out[1] != 0x01 {
		t.Errorf("opcode = %02x %02x, want c8 01 (LE 0x01c8)", out[0], out[1])
	}
	// index at [2:4] = 4 (server index 2 + 2, clif.cpp:4482)
	if v := binary.LittleEndian.Uint16(out[2:]); v != 4 {
		t.Errorf("index = %d, want 4 (server index 2 + 2)", v)
	}
	// itemId at [4:6] = 0xAB
	if v := binary.LittleEndian.Uint16(out[4:]); v != 0xAB {
		t.Errorf("itemId = 0x%x, want 0xAB", v)
	}
	// AID at [6:10] = 4242
	if v := binary.LittleEndian.Uint32(out[6:]); v != 4242 {
		t.Errorf("AID = %d, want 4242", v)
	}
	// amount at [10:12] = 9
	if v := binary.LittleEndian.Uint16(out[10:]); v != 9 {
		t.Errorf("amount = %d, want 9", v)
	}
	// result at [12] = 1 (success)
	if v := out[12]; v != 1 {
		t.Errorf("result = %d, want 1 (success)", v)
	}
}

// P2A: identity returns success=false — dispatcher must emit a
// failure ack (result=0) so the client exits the "using item" state.
func TestDispatchHandler_CZUseItem_IdentityRejects_EncodesFailAck(t *testing.T) {
	t.Parallel()

	identity := &fakeIdentityClient{
		useItemFn: func(_ context.Context, _ *identityv1.UseItemRequest) (*identityv1.UseItemResponse, error) {
			return &identityv1.UseItemResponse{Success: false, Error: "item not found"}, nil
		},
	}
	h := NewDispatchHandler(identity, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 4242, CharID: 9001}
	req := packet.CZUseItemRequest{Index: 2, AID: 4242}
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode CZ_USE_ITEM2: %v", err)
	}
	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZUSEITEM2, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}

	out := resp.buf.Bytes()
	if len(out) != 13 {
		t.Fatalf("ZC_USE_ITEM_ACK2 length = %d, want 13", len(out))
	}
	if out[12] != 0 {
		t.Errorf("result = %d, want 0 (failure)", out[12])
	}
}

// P2A: malformed frame and pre-auth guard for CZ_USE_ITEM2.
func TestDispatchHandler_CZUseItem_PreAuthGuard_DropsSilently(t *testing.T) {
	t.Parallel()

	identity := &fakeIdentityClient{
		useItemFn: func(_ context.Context, _ *identityv1.UseItemRequest) (*identityv1.UseItemResponse, error) {
			t.Fatal("UseItem must not be called without prior CZ_ENTER")
			return nil, nil
		},
	}
	h := NewDispatchHandler(identity, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1} // AccountID=0 — pre-auth
	req := packet.CZUseItemRequest{Index: 2, AID: 0}
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode CZ_USE_ITEM2: %v", err)
	}
	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZUSEITEM2, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
	if got := resp.buf.Len(); got != 0 {
		t.Fatalf("responder wrote %d bytes for pre-auth use item, want 0 (drop)", got)
	}
}

func TestDispatchHandler_CZUseItem_MalformedFrame_DropsSilently(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 4242, CharID: 9001}
	// 2-byte frame is shorter than the 8-byte CZ_USE_ITEM2.
	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZUSEITEM2, make([]byte, 2)); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
	if got := resp.buf.Len(); got != 0 {
		t.Fatalf("responder wrote %d bytes on malformed frame, want 0", got)
	}
}

// P2A: CZ_REQ_WEAR_EQUIP_V5 happy path.
func TestDispatchHandler_CZReqWearEquip_Success_EncodesAck(t *testing.T) {
	t.Parallel()

	var captured *identityv1.EquipItemRequest
	identity := &fakeIdentityClient{
		equipItemFn: func(_ context.Context, req *identityv1.EquipItemRequest) (*identityv1.EquipItemResponse, error) {
			captured = req
			if req.GetAccountId() != 4242 || req.GetCharId() != 9001 {
				t.Errorf("EquipItem req = (aid=%d, cid=%d), want (4242, 9001)",
					req.GetAccountId(), req.GetCharId())
			}
			if req.GetItemId() != 9 {
				t.Errorf("EquipItem req item_id = %d, want 9 (inventory index)", req.GetItemId())
			}
			if req.GetEquipPosition() != 0x0002 {
				t.Errorf("EquipItem req equip_position = 0x%x, want 0x2", req.GetEquipPosition())
			}
			return &identityv1.EquipItemResponse{
				Success:       true,
				ItemId:        9,
				EquipPosition: 0x0002,
			}, nil
		},
	}
	h := NewDispatchHandler(identity, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 4242, CharID: 9001}
	req := packet.CZReqWearEquipRequest{Index: 9, Position: 0x0002}
	conn.SetInventoryIndex(map[uint16]uint32{7: 9})
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode CZ_REQ_WEAR_EQUIP_V5: %v", err)
	}
	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZREQWEAREQUIPV5, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
	if captured == nil {
		t.Fatal("EquipItem RPC not called")
	}

	out := resp.buf.Bytes()
	const wantLen = 11
	if len(out) != wantLen {
		t.Fatalf("ZC_REQ_WEAR_EQUIP_ACK_V5 length = %d, want %d", len(out), wantLen)
	}
	if out[0] != 0x99 || out[1] != 0x09 {
		t.Errorf("opcode = %02x %02x, want 99 09 (LE 0x0999)", out[0], out[1])
	}
	// index at [2:4] = 11 (server index 9 + 2)
	if v := binary.LittleEndian.Uint16(out[2:]); v != 11 {
		t.Errorf("index = %d, want 11 (server index 9 + 2)", v)
	}
	// wearLocation at [4:8] = 0x0002
	if v := binary.LittleEndian.Uint32(out[4:]); v != 0x0002 {
		t.Errorf("wearLocation = 0x%x, want 0x2", v)
	}
	// result at [10] = 1 (success)
	if v := out[10]; v != 1 {
		t.Errorf("result = %d, want 1 (success)", v)
	}
}

func TestDispatchHandler_CZReqWearEquip_IdentityRejects_EncodesFailAck(t *testing.T) {
	t.Parallel()

	identity := &fakeIdentityClient{
		equipItemFn: func(_ context.Context, _ *identityv1.EquipItemRequest) (*identityv1.EquipItemResponse, error) {
			return &identityv1.EquipItemResponse{Success: false, Error: "class mismatch"}, nil
		},
	}
	h := NewDispatchHandler(identity, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 4242, CharID: 9001}
	req := packet.CZReqWearEquipRequest{Index: 9, Position: 0x0002}
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode CZ_REQ_WEAR_EQUIP_V5: %v", err)
	}
	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZREQWEAREQUIPV5, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}

	out := resp.buf.Bytes()
	if len(out) != 11 {
		t.Fatalf("ZC_REQ_WEAR_EQUIP_ACK_V5 length = %d, want 11", len(out))
	}
	if out[10] != 0 {
		t.Errorf("result = %d, want 0 (failure)", out[10])
	}
}

func TestDispatchHandler_CZReqWearEquip_PreAuthGuard_DropsSilently(t *testing.T) {
	t.Parallel()

	identity := &fakeIdentityClient{
		equipItemFn: func(_ context.Context, _ *identityv1.EquipItemRequest) (*identityv1.EquipItemResponse, error) {
			t.Fatal("EquipItem must not be called without prior CZ_ENTER")
			return nil, nil
		},
	}
	h := NewDispatchHandler(identity, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1} // AccountID=0
	req := packet.CZReqWearEquipRequest{Index: 9, Position: 0x0002}
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode CZ_REQ_WEAR_EQUIP_V5: %v", err)
	}
	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZREQWEAREQUIPV5, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
	if got := resp.buf.Len(); got != 0 {
		t.Fatalf("responder wrote %d bytes for pre-auth equip, want 0 (drop)", got)
	}
}

func TestDispatchHandler_CZReqWearEquip_MalformedFrame_DropsSilently(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 4242, CharID: 9001}
	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZREQWEAREQUIPV5, make([]byte, 2)); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
	if got := resp.buf.Len(); got != 0 {
		t.Fatalf("responder wrote %d bytes on malformed frame, want 0", got)
	}
}

// P2A: CZ_REQ_TAKEOFF_EQUIP happy path. flag is wire-inverted for
// PACKETVER >= 20110824: 0 = success.
func TestDispatchHandler_CZReqTakeoffEquip_Success_EncodesAck(t *testing.T) {
	t.Parallel()

	var captured *identityv1.UnequipItemRequest
	identity := &fakeIdentityClient{
		unequipItemFn: func(_ context.Context, req *identityv1.UnequipItemRequest) (*identityv1.UnequipItemResponse, error) {
			captured = req
			if req.GetAccountId() != 4242 || req.GetCharId() != 9001 {
				t.Errorf("UnequipItem req = (aid=%d, cid=%d), want (4242, 9001)",
					req.GetAccountId(), req.GetCharId())
			}
			if req.GetItemId() != 9 {
				t.Errorf("UnequipItem req item_id = %d, want 9", req.GetItemId())
			}
			return &identityv1.UnequipItemResponse{Success: true, ItemId: 9}, nil
		},
	}
	h := NewDispatchHandler(identity, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 4242, CharID: 9001}
	req := packet.CZReqTakeoffEquipRequest{Index: 9}
	conn.SetInventoryIndex(map[uint16]uint32{7: 9})
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode CZ_REQ_TAKEOFF_EQUIP: %v", err)
	}
	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZREQTAKEOFFEQUIP, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
	if captured == nil {
		t.Fatal("UnequipItem RPC not called")
	}

	out := resp.buf.Bytes()
	const wantLen = 9
	if len(out) != wantLen {
		t.Fatalf("ZC_REQ_TAKEOFF_EQUIP_ACK length = %d, want %d", len(out), wantLen)
	}
	if out[0] != 0x9a || out[1] != 0x09 {
		t.Errorf("opcode = %02x %02x, want 9a 09 (LE 0x099a)", out[0], out[1])
	}
	// index at [2:4] = 11 (server index 9 + 2)
	if v := binary.LittleEndian.Uint16(out[2:]); v != 11 {
		t.Errorf("index = %d, want 11 (server index 9 + 2)", v)
	}
	// flag at [8] = 0 (success on the wire — inverted for PACKETVER >= 20110824)
	if v := out[8]; v != 0 {
		t.Errorf("flag = %d, want 0 (success on the wire)", v)
	}
}

func TestDispatchHandler_CZReqTakeoffEquip_IdentityRejects_EncodesFailAck(t *testing.T) {
	t.Parallel()

	identity := &fakeIdentityClient{
		unequipItemFn: func(_ context.Context, _ *identityv1.UnequipItemRequest) (*identityv1.UnequipItemResponse, error) {
			return &identityv1.UnequipItemResponse{Success: false, Error: "not equipped"}, nil
		},
	}
	h := NewDispatchHandler(identity, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 4242, CharID: 9001}
	req := packet.CZReqTakeoffEquipRequest{Index: 9}
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode CZ_REQ_TAKEOFF_EQUIP: %v", err)
	}
	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZREQTAKEOFFEQUIP, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}

	out := resp.buf.Bytes()
	if len(out) != 9 {
		t.Fatalf("ZC_REQ_TAKEOFF_EQUIP_ACK length = %d, want 9", len(out))
	}
	// flag at [8] = 1 (failure on the wire)
	if v := out[8]; v != 1 {
		t.Errorf("flag = %d, want 1 (failure on the wire)", v)
	}
}

func TestDispatchHandler_CZReqTakeoffEquip_PreAuthGuard_DropsSilently(t *testing.T) {
	t.Parallel()

	identity := &fakeIdentityClient{
		unequipItemFn: func(_ context.Context, _ *identityv1.UnequipItemRequest) (*identityv1.UnequipItemResponse, error) {
			t.Fatal("UnequipItem must not be called without prior CZ_ENTER")
			return nil, nil
		},
	}
	h := NewDispatchHandler(identity, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1} // AccountID=0
	req := packet.CZReqTakeoffEquipRequest{Index: 9}
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode CZ_REQ_TAKEOFF_EQUIP: %v", err)
	}
	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZREQTAKEOFFEQUIP, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
	if got := resp.buf.Len(); got != 0 {
		t.Fatalf("responder wrote %d bytes for pre-auth unequip, want 0 (drop)", got)
	}
}

func TestDispatchHandler_CZReqTakeoffEquip_MalformedFrame_DropsSilently(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 4242, CharID: 9001}
	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZREQTAKEOFFEQUIP, make([]byte, 2)); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
	if got := resp.buf.Len(); got != 0 {
		t.Fatalf("responder wrote %d bytes on malformed frame, want 0", got)
	}
}

// P2B: NPC shop economy — buy / sell list emission, real zeny
// deduction / credit through identity RPCs, and zeny LongParChange
// update on success.

// shopWeaponNpcGID is the hardcoded GID of the Weapon Shop NPC from
// the gateway's npcSpawns catalog (npc_spawns.go).
const shopWeaponNpcGID uint32 = 110000002

// sizeZCPCSellItemListItem is re-declared here for the test (the
// production package's private constant is not exported).
const sizeZCPCSellItemListItem = 10

// packetAt returns the (cmd, payload) pair for the i'th packet in
// the responder buffer by sequential parse. The fixed / variable
// dispatch mirrors parseStatusBurst but applies to the shop-result /
// zeny-update frames that follow it.
func packetAt(t *testing.T, buf []byte, i int) (cmd uint16, payload []byte) {
	t.Helper()
	offset := 0
	idx := 0
	for offset+2 <= len(buf) {
		c := binary.LittleEndian.Uint16(buf[offset:])
		var plen int
		switch c {
		case packet.HeaderZCPCPURCHASERESULT, packet.HeaderZCPCSELLRESULT:
			plen = 3
		case packet.HeaderZCLONGPARCHANGE, packet.HeaderZCPARCHANGE:
			plen = 8
		default:
			if offset+4 > len(buf) {
				t.Fatalf("truncated packet at offset %d", offset)
			}
			plen = int(binary.LittleEndian.Uint16(buf[offset+2:]))
			if plen < 4 {
				t.Fatalf("packet 0x%04x at offset %d has plen=%d", c, offset, plen)
			}
		}
		if offset+plen > len(buf) {
			t.Fatalf("truncated packet 0x%04x at offset %d (want %d bytes, have %d)",
				c, offset, plen, len(buf)-offset)
		}
		if idx == i {
			return c, buf[offset : offset+plen]
		}
		offset += plen
		idx++
	}
	t.Fatalf("only %d packets in buffer (index %d requested); buf=% x", idx, i, buf)
	return 0, nil
}

// TestDispatchHandler_CZPCAckSelectDealType_Sell_SendsSellList
// covers the sell-list emission path: handleCZAckSelectDealType(type=Sell)
// must fetch inventory via identity.GetInventory, price each item at
// half the catalog buy price, and emit ZC_PC_SELL_ITEMLIST with the
// correct (index, price, overcharge) triple per row. slot 2's
// non-catalog item must also be listed with a 0 sell price so the
// client UI does not silently drop it.
func TestDispatchHandler_CZPCAckSelectDealType_Sell_SendsSellList(t *testing.T) {
	t.Parallel()

	identity := &fakeIdentityClient{
		getInventoryFn: func(_ context.Context, req *identityv1.GetInventoryRequest) (*identityv1.GetInventoryResponse, error) {
			if req.GetAccountId() != 4242 || req.GetCharId() != 9001 {
				t.Errorf("GetInventory = (%d, %d), want (4242, 9001)",
					req.GetAccountId(), req.GetCharId())
			}
			return &identityv1.GetInventoryResponse{Items: []*identityv1.InventoryItem{
				// Slot 0: Red Potion (501, catalog price 50 → sellPrice 25).
				{Id: 100, Nameid: 501, Amount: 5},
				// Slot 1: Knife (1201, catalog price 500 → sellPrice 250).
				{Id: 101, Nameid: 1201, Amount: 1},
				// Slot 2: catalog miss → sellPrice 0 (still listed).
				{Id: 102, Nameid: 9999, Amount: 1},
			}}, nil
		},
	}
	h := NewDispatchHandler(identity, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 4242, CharID: 9001}

	req := packet.CZAckSelectDealTypeRequest{NpcID: shopWeaponNpcGID, Type: 0x01}
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode CZAckSelectDealType: %v", err)
	}
	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZACKSELECTDEALTYPE, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v", err)
	}

	out := resp.buf.Bytes()
	if len(out) == 0 {
		t.Fatal("responder wrote nothing")
	}
	cmd, payload := packetAt(t, out, 0)
	if cmd != packet.HeaderZCPCSELLITEMLIST {
		t.Fatalf("packet[0] = 0x%04x, want 0x%04x (ZC_PC_SELL_ITEMLIST)", cmd, packet.HeaderZCPCSELLITEMLIST)
	}
	if plen := int(binary.LittleEndian.Uint16(payload[2:4])); plen != len(payload) {
		t.Fatalf("packetLength slot = %d, want %d (full frame length)", plen, len(payload))
	}
	// Slot 0: Red Potion.
	if idx := binary.LittleEndian.Uint16(payload[4:6]); idx != 0 {
		t.Errorf("items[0] index = %d, want 0", idx)
	}
	if p := binary.LittleEndian.Uint32(payload[6:10]); p != 25 {
		t.Errorf("items[0] price = %d, want 25 (50/2)", p)
	}
	if oc := binary.LittleEndian.Uint32(payload[10:14]); oc != 25 {
		t.Errorf("items[0] overcharge = %d, want 25", oc)
	}
	// Slot 1: Knife.
	off := 4 + sizeZCPCSellItemListItem
	if idx := binary.LittleEndian.Uint16(payload[off : off+2]); idx != 1 {
		t.Errorf("items[1] index = %d, want 1", idx)
	}
	if p := binary.LittleEndian.Uint32(payload[off+2 : off+6]); p != 250 {
		t.Errorf("items[1] price = %d, want 250 (500/2)", p)
	}
	// Slot 2: non-catalog item.
	off2 := 4 + 2*sizeZCPCSellItemListItem
	if p := binary.LittleEndian.Uint32(payload[off2+2 : off2+6]); p != 0 {
		t.Errorf("items[2] price = %d, want 0 (catalog miss)", p)
	}
	// shopNPCID must be set so the following sell request can
	// re-anchor to this NPC.
	if got := conn.ShopNPC(); got != shopWeaponNpcGID {
		t.Errorf("after Sell ACK, conn.ShopNPC() = %d, want %d", got, shopWeaponNpcGID)
	}
}

// TestDispatchHandler_CZPCAckSelectDealType_Cancel_ClearsShopNPC
// verifies the Cancel branch resets shopNPCID so a stale shop
// context can't leak into a subsequent sell request.
func TestDispatchHandler_CZPCAckSelectDealType_Cancel_ClearsShopNPC(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 4242, CharID: 9001}
	conn.SetShopNPC(shopWeaponNpcGID)

	req := packet.CZAckSelectDealTypeRequest{NpcID: shopWeaponNpcGID, Type: 0x02}
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZACKSELECTDEALTYPE, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v", err)
	}
	if got := conn.ShopNPC(); got != 0 {
		t.Errorf("after Cancel, conn.ShopNPC() = %d, want 0", got)
	}
}

// TestDispatchHandler_CZPCPurchaseItemList_HappyPath covers the
// full buy path: parse the (itemId, amount) request, look up the
// unit price from the active NPC's catalog, call
// identity.BuyFromShop, and emit ZC_PC_PURCHASE_RESULT (0) +
// ZC_LONGPAR_CHANGE for the post-transaction zeny.
func TestDispatchHandler_CZPCPurchaseItemList_HappyPath(t *testing.T) {
	t.Parallel()

	const wantNewZeny uint32 = 5000
	identity := &fakeIdentityClient{
		buyFromShopFn: func(_ context.Context, req *identityv1.BuyFromShopRequest) (*identityv1.BuyFromShopResponse, error) {
			if req.GetAccountId() != 4242 || req.GetCharId() != 9001 {
				t.Errorf("BuyFromShop = (aid=%d, cid=%d), want (4242, 9001)",
					req.GetAccountId(), req.GetCharId())
			}
			if len(req.GetOrders()) != 1 {
				t.Fatalf("len(Orders) = %d, want 1", len(req.GetOrders()))
			}
			ord := req.GetOrders()[0]
			if ord.GetItemId() != 501 || ord.GetAmount() != 3 || ord.GetUnitPrice() != 50 {
				t.Errorf("Orders[0] = (item=%d, amount=%d, price=%d), want (501, 3, 50)",
					ord.GetItemId(), ord.GetAmount(), ord.GetUnitPrice())
			}
			return &identityv1.BuyFromShopResponse{
				Result:  identityv1.BuyResult_BUY_RESULT_OK,
				NewZeny: wantNewZeny,
			}, nil
		},
	}
	h := NewDispatchHandler(identity, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 4242, CharID: 9001}
	conn.SetShopNPC(shopWeaponNpcGID)

	req := packet.CZPCPurchaseItemListRequest{Entries: []packet.CZPCPurchaseItemListEntry{
		{ItemID: 501, Amount: 3},
	}}
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode CZ_PC_PURCHASE_ITEMLIST: %v", err)
	}
	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZPCPURCHASEITEMLIST, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v", err)
	}

	out := resp.buf.Bytes()

	// Packet 0: ZC_PC_PURCHASE_RESULT result=0.
	cmd0, p0 := packetAt(t, out, 0)
	if cmd0 != packet.HeaderZCPCPURCHASERESULT {
		t.Fatalf("packet[0] = 0x%04x, want 0x%04x (ZC_PC_PURCHASE_RESULT)",
			cmd0, packet.HeaderZCPCPURCHASERESULT)
	}
	if p0[2] != 0 {
		t.Errorf("ZC_PC_PURCHASE_RESULT result byte = %d, want 0", p0[2])
	}

	// Packet 1: ZC_LONGPAR_CHANGE SP_ZENY=5000.
	cmd1, p1 := packetAt(t, out, 1)
	if cmd1 != packet.HeaderZCLONGPARCHANGE {
		t.Fatalf("packet[1] = 0x%04x, want 0x%04x (ZC_LONGPAR_CHANGE)",
			cmd1, packet.HeaderZCLONGPARCHANGE)
	}
	if vid := binary.LittleEndian.Uint16(p1[2:4]); vid != packet.SPZeny {
		t.Errorf("LongParChange varID = %d, want %d (SP_ZENY)", vid, packet.SPZeny)
	}
	if amt := int32(binary.LittleEndian.Uint32(p1[4:8])); amt != int32(wantNewZeny) {
		t.Errorf("LongParChange amount = %d, want %d", amt, wantNewZeny)
	}

	// shopNPCID cleared after the transaction.
	if got := conn.ShopNPC(); got != 0 {
		t.Errorf("after successful purchase, conn.ShopNPC() = %d, want 0", got)
	}
}

// TestDispatchHandler_CZPCPurchaseItemList_InsufficientZeny covers
// the non-OK identity-result path: the response must carry
// result=1 and no zeny update.
func TestDispatchHandler_CZPCPurchaseItemList_InsufficientZeny(t *testing.T) {
	t.Parallel()

	identity := &fakeIdentityClient{
		buyFromShopFn: func(_ context.Context, _ *identityv1.BuyFromShopRequest) (*identityv1.BuyFromShopResponse, error) {
			return &identityv1.BuyFromShopResponse{
				Result:  identityv1.BuyResult_BUY_RESULT_INSUFFICIENT_ZENY,
				NewZeny: 0,
			}, nil
		},
	}
	h := NewDispatchHandler(identity, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 4242, CharID: 9001}
	conn.SetShopNPC(shopWeaponNpcGID)

	req := packet.CZPCPurchaseItemListRequest{Entries: []packet.CZPCPurchaseItemListEntry{
		// 1101 is Short Sword in catalog (1500 zeny), but the player is broke.
		{ItemID: 1101, Amount: 1000},
	}}
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZPCPURCHASEITEMLIST, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v", err)
	}

	out := resp.buf.Bytes()
	if len(out) != 3 {
		t.Fatalf("responder wrote %d bytes, want exactly 3 (ZC_PC_PURCHASE_RESULT(fail))",
			len(out))
	}
	if out[0] != 0xca || out[1] != 0x00 {
		t.Errorf("opcode = %02x %02x, want ca 00 (ZC_PC_PURCHASE_RESULT)",
			out[0], out[1])
	}
	if out[2] != 1 {
		t.Errorf("result = %d, want 1 (fail)", out[2])
	}
}

// TestDispatchHandler_CZPCPurchaseItemList_ItemNotInCatalog covers
// D-204 price authority: an order referencing an itemId the active
// NPC does not sell must fail without calling identity.
func TestDispatchHandler_CZPCPurchaseItemList_ItemNotInCatalog(t *testing.T) {
	t.Parallel()

	identity := &fakeIdentityClient{
		buyFromShopFn: func(_ context.Context, _ *identityv1.BuyFromShopRequest) (*identityv1.BuyFromShopResponse, error) {
			t.Fatal("BuyFromShop must NOT be called when the ordered item is not in the NPC catalog")
			return nil, nil
		},
	}
	h := NewDispatchHandler(identity, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 4242, CharID: 9001}
	conn.SetShopNPC(shopWeaponNpcGID)

	req := packet.CZPCPurchaseItemListRequest{Entries: []packet.CZPCPurchaseItemListEntry{
		{ItemID: 9999, Amount: 1}, // not in catalog
	}}
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZPCPURCHASEITEMLIST, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v", err)
	}

	out := resp.buf.Bytes()
	if len(out) != 3 || out[2] != 1 {
		t.Fatalf("responder out = %v, want 3 bytes with result=1", out)
	}
	if len(identity.buyReqs) != 0 {
		t.Fatalf("BuyFromShop called %d times, want 0", len(identity.buyReqs))
	}
}

// TestDispatchHandler_CZPCPurchaseItemList_NoShopNPC verifies the
// no-prior-ACK guard: without a CZ_ACK_SELECT_DEALTYPE, the
// handler must fail the purchase without calling identity.
func TestDispatchHandler_CZPCPurchaseItemList_NoShopNPC(t *testing.T) {
	t.Parallel()

	identity := &fakeIdentityClient{
		buyFromShopFn: func(_ context.Context, _ *identityv1.BuyFromShopRequest) (*identityv1.BuyFromShopResponse, error) {
			t.Fatal("BuyFromShop must NOT be called without a prior shop dialog")
			return nil, nil
		},
	}
	h := NewDispatchHandler(identity, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 4242, CharID: 9001}

	req := packet.CZPCPurchaseItemListRequest{Entries: []packet.CZPCPurchaseItemListEntry{
		{ItemID: 501, Amount: 1},
	}}
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZPCPURCHASEITEMLIST, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v", err)
	}

	if len(identity.buyReqs) != 0 {
		t.Fatalf("BuyFromShop called %d times, want 0 (no prior shop dialog)",
			len(identity.buyReqs))
	}
	out := resp.buf.Bytes()
	if len(out) != 3 || out[2] != 1 {
		t.Fatalf("responder out = %v, want 3 bytes result=1", out)
	}
}

// TestDispatchHandler_CZPCSellItemList_HappyPath covers the
// complete sell path: parse (index, amount), look up unit price
// from the catalog, call identity.SellToShop, emit
// ZC_PC_SELL_RESULT (0) + ZC_LONGPAR_CHANGE for the new zeny.
func TestDispatchHandler_CZPCSellItemList_HappyPath(t *testing.T) {
	t.Parallel()

	const wantNewZeny uint32 = 75
	identity := &fakeIdentityClient{
		getInventoryFn: func(_ context.Context, _ *identityv1.GetInventoryRequest) (*identityv1.GetInventoryResponse, error) {
			return &identityv1.GetInventoryResponse{Items: []*identityv1.InventoryItem{
				{Id: 100, Nameid: 501, Amount: 5}, // slot 0, Red Potion
			}}, nil
		},
		sellToShopFn: func(_ context.Context, req *identityv1.SellToShopRequest) (*identityv1.SellToShopResponse, error) {
			if len(req.GetSales()) != 1 {
				t.Fatalf("len(Sales) = %d, want 1", len(req.GetSales()))
			}
			line := req.GetSales()[0]
			if line.GetInvId() != 100 || line.GetAmount() != 3 || line.GetUnitPrice() != 25 {
				t.Errorf("Sales[0] = (inv=%d, amount=%d, price=%d), want (100, 3, 25)",
					line.GetInvId(), line.GetAmount(), line.GetUnitPrice())
			}
			return &identityv1.SellToShopResponse{
				Result:  identityv1.SellResult_SELL_RESULT_OK,
				NewZeny: wantNewZeny,
			}, nil
		},
	}
	h := NewDispatchHandler(identity, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 4242, CharID: 9001}
	conn.SetInventoryIndex(map[uint16]uint32{0: 100})
	conn.SetShopNPC(shopWeaponNpcGID)

	req := packet.CZPCSellItemListRequest{Entries: []packet.CZPCSellItemListEntry{
		{Index: 0, Amount: 3},
	}}
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode CZ_PC_SELL_ITEMLIST: %v", err)
	}
	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZPCSELLITEMLIST, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v", err)
	}

	out := resp.buf.Bytes()
	// Packet 0: ZC_PC_SELL_RESULT result=0.
	cmd0, p0 := packetAt(t, out, 0)
	if cmd0 != packet.HeaderZCPCSELLRESULT {
		t.Fatalf("packet[0] = 0x%04x, want 0x%04x (ZC_PC_SELL_RESULT)",
			cmd0, packet.HeaderZCPCSELLRESULT)
	}
	if p0[2] != 0 {
		t.Errorf("ZC_PC_SELL_RESULT result byte = %d, want 0", p0[2])
	}
	// Packet 1: ZC_LONGPAR_CHANGE SP_ZENY=75.
	cmd1, p1 := packetAt(t, out, 1)
	if cmd1 != packet.HeaderZCLONGPARCHANGE {
		t.Fatalf("packet[1] = 0x%04x, want 0x%04x (ZC_LONGPAR_CHANGE)",
			cmd1, packet.HeaderZCLONGPARCHANGE)
	}
	if amt := int32(binary.LittleEndian.Uint32(p1[4:8])); amt != int32(wantNewZeny) {
		t.Errorf("LongParChange amount = %d, want %d", amt, wantNewZeny)
	}
	// shopNPCID cleared after the transaction.
	if got := conn.ShopNPC(); got != 0 {
		t.Errorf("after successful sell, conn.ShopNPC() = %d, want 0", got)
	}
}

// TestDispatchHandler_CZPCSellItemList_NoShopNPC verifies the
// no-prior-ACK guard on the sell path.
func TestDispatchHandler_CZPCSellItemList_NoShopNPC(t *testing.T) {
	t.Parallel()

	identity := &fakeIdentityClient{
		sellToShopFn: func(_ context.Context, _ *identityv1.SellToShopRequest) (*identityv1.SellToShopResponse, error) {
			t.Fatal("SellToShop must NOT be called without a prior shop dialog")
			return nil, nil
		},
	}
	h := NewDispatchHandler(identity, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 4242, CharID: 9001}

	req := packet.CZPCSellItemListRequest{Entries: []packet.CZPCSellItemListEntry{
		{Index: 0, Amount: 1},
	}}
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZPCSELLITEMLIST, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v", err)
	}

	if len(identity.sellReqs) != 0 {
		t.Fatalf("SellToShop called %d times, want 0", len(identity.sellReqs))
	}
	out := resp.buf.Bytes()
	if len(out) != 3 || out[2] != 1 {
		t.Fatalf("responder out = %v, want 3 bytes result=1", out)
	}
}

// P3B: the map-enter status burst must carry a real ZC_SKILLINFO_LIST
// (0x010f) frame for the character's learned skills, not the M10 4-byte
// empty stub. This drives the full LoadEndAck path through
// handleCZNotifyActorInit and locates the skill-list frame inside the
// coalesced burst. The deterministic novice default yields exactly one
// NV_BASIC L1 entry → packetLength = 4 + 37 = 41.
func TestDispatchHandler_CZNotifyActorInit_EmitsSkillInfoList(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 4242, CharID: 9001}
	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZNOTIFYACTORINIT, make([]byte, 2)); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}

	out := resp.buf.Bytes()

	// Locate the 0x010f frame inside the burst. Walk the packet
	// stream by opcode so a payload byte that happens to match the
	// header does not give a false positive.
	offset := 8 // skip leading 8-byte ZC_MAPPROPERTY_R2
	var skillStart, skillPlen int = -1, -1
	for offset+2 <= len(out) {
		cmd := binary.LittleEndian.Uint16(out[offset:])
		switch cmd {
		case 0x099b:
			offset += 8
		case 0x00b0, 0x00b1:
			offset += 8
		case 0x00bd:
			offset += 44
		case 0x00a3, 0x00a4, 0x010f:
			plen := int(binary.LittleEndian.Uint16(out[offset+2 : offset+4]))
			if cmd == 0x010f {
				skillStart = offset
				skillPlen = plen
			}
			offset += plen
		case 0x02b9:
			offset += 191
		case 0x09ff:
			offset += 107
		default:
			t.Fatalf("unexpected packet 0x%04x at offset %d (buf=% x)", cmd, offset, out)
		}
	}
	if skillStart < 0 {
		t.Fatalf("ZC_SKILLINFO_LIST (0x010f) not found in burst")
	}
	if skillStart+skillPlen > len(out) {
		t.Fatalf("ZC_SKILLINFO_LIST frame truncated: start=%d len=%d buf=%d",
			skillStart, skillPlen, len(out))
	}

	frame := out[skillStart : skillStart+skillPlen]

	// Opcode: 0x0f 0x01 (LE 0x010f).
	if frame[0] != 0x0f || frame[1] != 0x01 {
		t.Fatalf("ZC_SKILLINFO_LIST opcode = %02x %02x, want 0f 01 (LE 0x010f)",
			frame[0], frame[1])
	}

	// Packet length: 4-byte header + 37 bytes per skill entry. NV_BASIC ×1 = 41.
	const wantPlen = 41
	if skillPlen != wantPlen {
		t.Fatalf("ZC_SKILLINFO_LIST packetLength = %d, want %d (NV_BASIC ×1)", skillPlen, wantPlen)
	}

	// Total frame size matches packetLength.
	if len(frame) != wantPlen {
		t.Fatalf("ZC_SKILLINFO_LIST frame = %d bytes, want %d", len(frame), wantPlen)
	}

	// Name slot is at off 16 (4-byte header + 12 bytes of id/inf/level/sp/range2).
	// writeFixedName NUL-pads after the string, so it must appear
	// verbatim followed by zero bytes up to off 40.
	nameStart := skillStart + 4 + 12
	nameEnd := nameStart + len("NV_BASIC")
	for i, b := range []byte("NV_BASIC") {
		if out[nameStart+i] != b {
			t.Fatalf("ZC_SKILLINFO_LIST name slot byte %d = %q, want %q (full: % x)",
				i, out[nameStart+i], b, out[nameStart:nameStart+24])
		}
	}
	for i := nameEnd; i < nameStart+24; i++ {
		if out[i] != 0 {
			t.Fatalf("ZC_SKILLINFO_LIST name slot not NUL-terminated at byte %d (got %02x)",
				i-nameStart, out[i])
		}
	}

	// Level field at SkillData+6 LE uint16 (after id uint16 + inf uint32).
	// 4-byte packet header + 6-byte SkillData prefix.
	level := binary.LittleEndian.Uint16(out[skillStart+4+6 : skillStart+4+8])
	if level != 1 {
		t.Errorf("NV_BASIC level = %d, want 1", level)
	}

	// Skill ID at SkillData+0 LE uint16 (right after the 4-byte packet header).
	id := binary.LittleEndian.Uint16(out[skillStart+4 : skillStart+6])
	if id != 1 {
		t.Errorf("skill id = %d, want 1 (NV_BASIC)", id)
	}
}

// --- Phase 3b-2 L3 acceptance: CZ_USE_SKILL2 → SM_BASH end-to-end path ---
//
// Wire sizes (see pkg/ro/packet/map_skill_usage.go godoc):
//   CZ_USE_SKILL2   : 10 bytes  (cmd 0x0438 + SkillLv + SkillID + TargetID)
//   ZC_NOTIFY_SKILL : 33 bytes  (cmd 0x01de + SKID + AID + TargetID + StartTime
//                                 + AttackMT + AttackedMT + Damage + Level
//                                 + Count + Action)
//   ZC_PAR_CHANGE   : 8 bytes   (cmd 0x00b0 + varID u16 + value u32)
//   ZC_ACK_TOUSESKILL: 14 bytes (cmd 0x0110 + SkillID + BType i32 + ItemID u32
//                                 + Flag u8 + Cause u8)

const (
	// poringGID is the GID used by every CZ_USE_SKILL2 test below.
	// Matches monster_spawns.go: Poring has Def=0, Vit=0.
	poringGID uint32 = 110000005
)

// buildCZUseSkill encodes a CZ_USE_SKILL2 frame for a single-target skill.
func buildCZUseSkill(t *testing.T, skillID uint16, level int16, targetID uint32) []byte {
	t.Helper()
	req := packet.CZUseSkill{SkillID: skillID, SkillLv: level, TargetID: targetID}
	var buf bytes.Buffer
	if err := req.Encode(&buf); err != nil {
		t.Fatalf("encode CZ_USE_SKILL2: %v", err)
	}
	return buf.Bytes()
}

// TestDispatchHandler_CZUseSkill_BashHit_DeductsSPAndEmitsScaledDamage
// is the L3 success path for the P3b-2 SM_BASH slice: SP=20, Bash L1
// (spCost=8, pct=130) on a Poring with 50 HP.
//
//	(1) SP drops to 12 and a ZC_PAR_CHANGE carries the new value.
//	(2) ZC_NOTIFY_SKILL is emitted with SKID=5, Level=1, Count=1, Action=0,
//	    Damage equal to statsdomain.BashDamage(...,130) (scaled, not the
//	    flat-10 melee damage).
//	(3) Monster HP is decremented by that damage.
func TestDispatchHandler_CZUseSkill_BashHit_DeductsSPAndEmitsScaledDamage(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	const wantAID uint32 = 200001
	conn := domain.ConnectionInfo{
		ID:        1,
		AccountID: wantAID,
		MonsterHP: map[uint32]int32{poringGID: 50},
	}
	conn.SetCombatStats(10, 5, 0) // matches melee-test baseline
	conn.SetSP(20, 100)
	// Force min damage so the assertion is deterministic.
	h.setDamageRoll(func(min, max int32) int32 { return min })

	reqBuf := buildCZUseSkill(t, skilldomain.SM_BASH, 1, poringGID)

	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZUSESKILL, reqBuf); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}

	out := resp.buf.Bytes()
	// ZC_NOTIFY_SKILL (33) + ZC_PAR_CHANGE (8) = 41 bytes.
	if len(out) != 41 {
		t.Fatalf("response length = %d, want 41 (ZC_NOTIFY_SKILL 33 + ZC_PAR_CHANGE 8); buf=% x", len(out), out)
	}

	// --- ZC_NOTIFY_SKILL @ offset 0 ---
	if out[0] != 0xde || out[1] != 0x01 {
		t.Fatalf("first opcode = %02x %02x, want de 01 (LE 0x01de ZC_NOTIFY_SKILL)", out[0], out[1])
	}
	// SKID @ [2:4]
	if skid := binary.LittleEndian.Uint16(out[2:4]); skid != skilldomain.SM_BASH {
		t.Errorf("SKID = %d, want %d (SM_BASH)", skid, skilldomain.SM_BASH)
	}
	// AID @ [4:8]
	if aid := binary.LittleEndian.Uint32(out[4:8]); aid != wantAID {
		t.Errorf("AID = %d, want %d", aid, wantAID)
	}
	// TargetID @ [8:12]
	if tgt := binary.LittleEndian.Uint32(out[8:12]); tgt != poringGID {
		t.Errorf("TargetID = %d, want %d", tgt, poringGID)
	}
	// Damage @ [24:28] — must equal BashDamage(str=10, dex=5, luk=0,
	// def=0, vit=0, pct=130) (deterministic because damageRoll→min).
	band := statsdomain.BashDamage(10, 5, 0, 0, 0, 130)
	wantDmg := band.Min
	if dmg := int32(binary.LittleEndian.Uint32(out[24:28])); dmg != wantDmg {
		t.Errorf("ZC_NOTIFY_SKILL Damage = %d, want %d (BashDamage scaled, NOT flat 10)", dmg, wantDmg)
	}
	// Belt-and-suspenders: prove the damage is NOT the legacy flat-10.
	if dmg := int32(binary.LittleEndian.Uint32(out[24:28])); dmg == 10 {
		t.Errorf("Damage = 10 — this is the legacy flat-melee value; Bash must scale")
	}
	// Level @ [28:30]
	if level := int16(binary.LittleEndian.Uint16(out[28:30])); level != 1 {
		t.Errorf("Level = %d, want 1", level)
	}
	// Count @ [30:32]
	if count := int16(binary.LittleEndian.Uint16(out[30:32])); count != 1 {
		t.Errorf("Count = %d, want 1", count)
	}
	// Action @ [32]
	if action := int8(out[32]); action != 0 {
		t.Errorf("Action = %d, want 0 (DMG_NORMAL)", action)
	}

	// --- ZC_PAR_CHANGE @ offset 33 ---
	if out[33] != 0xb0 || out[34] != 0x00 {
		t.Fatalf("second opcode = %02x %02x, want b0 00 (LE 0x00b0 ZC_PAR_CHANGE)", out[33], out[34])
	}
	// varID @ [35:37] must be SPSP=7
	if varID := binary.LittleEndian.Uint16(out[35:37]); varID != packet.SPSP {
		t.Errorf("ZC_PAR_CHANGE varID = %d, want %d (SPSP)", varID, packet.SPSP)
	}
	// value @ [37:41] must be the post-spend SP (20 - SpAt(1)=8 = 12).
	if sp := int32(binary.LittleEndian.Uint32(out[37:41])); sp != 12 {
		t.Errorf("ZC_PAR_CHANGE SP value = %d, want 12 (20 - 8)", sp)
	}

	// --- Cache side effects ---
	if got := conn.SP(); got != 12 {
		t.Errorf("conn.SP() = %d, want 12 after Bash L1 (cost 8)", got)
	}
	if hp := conn.MonsterHP[poringGID]; hp != 50-wantDmg {
		t.Errorf("monster HP = %d, want %d (50 - BashDamage scaled)", hp, 50-wantDmg)
	}
}

// TestDispatchHandler_CZUseSkill_BashKill_FiresDeathPath covers the kill
// path: Bash L1 with Poring HP set so a single hit kills it. The death
// path (vanish + EXP + respawn) must fire exactly as it does for the
// melee path — same helper, byte-identical byte stream after the
// ZC_NOTIFY_SKILL + ZC_PAR_CHANGE prefix.
func TestDispatchHandler_CZUseSkill_BashKill_FiresDeathPath(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)
	h.respawnDelay = 50 * time.Millisecond

	resp := &safeResponder{}
	conn := domain.ConnectionInfo{
		ID:        1,
		AccountID: 200001,
		MonsterHP: map[uint32]int32{poringGID: 5}, // 5 HP — any Bash hit kills
		BaseExp:   10,
		JobExp:    5,
	}
	conn.SetCombatStats(10, 5, 0)
	conn.SetSP(50, 100)
	h.setDamageRoll(func(min, max int32) int32 { return min })

	reqBuf := buildCZUseSkill(t, skilldomain.SM_BASH, 1, poringGID)

	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZUSESKILL, reqBuf); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}

	// Monster should have been removed from the HP map (death).
	if _, ok := conn.ApplyDamage(poringGID, 0); ok {
		t.Fatal("monster should be dead (removed from MonsterHP)")
	}

	// EXP must have been applied (Poring gives Base=2, Job=1).
	if conn.BaseExp != 12 {
		t.Errorf("BaseExp = %d, want 12", conn.BaseExp)
	}
	if conn.JobExp != 6 {
		t.Errorf("JobExp = %d, want 6", conn.JobExp)
	}

	// The handler ships the burst as ONE SendPacket call, so
	// safeResponder has a single packet containing:
	//   ZC_NOTIFY_SKILL (33) + ZC_PAR_CHANGE (8) + ZC_NOTIFY_VANISH (7)
	//   + ZC_LONGPAR_CHANGE BaseExp (8) + ZC_LONGPAR_CHANGE JobExp (8)
	// = 64 bytes. Walk that one packet to assert each header.
	pkts := resp.GetPackets()
	if len(pkts) != 1 {
		// The respawn SET_UNIT_IDLE arrives asynchronously and may
		// not yet be present at the moment we assert; we only need
		// ≥1 (the burst).
		if len(pkts) < 1 {
			t.Fatalf("got %d packets, want ≥1 (the burst); pkts=% x", len(pkts), pkts)
		}
	}
	burst := pkts[0]
	wantLen := 33 + 8 + 7 + 8 + 8
	if len(burst) != wantLen {
		t.Fatalf("burst length = %d, want %d (skill 33 + par 8 + vanish 7 + 2x longpar 8); buf=% x", len(burst), wantLen, burst)
	}
	// ZC_NOTIFY_SKILL @ [0:33]
	if burst[0] != 0xde || burst[1] != 0x01 {
		t.Fatalf("burst[0:2] = %02x %02x, want de 01 (ZC_NOTIFY_SKILL)", burst[0], burst[1])
	}
	// ZC_PAR_CHANGE @ [33:41]
	if burst[33] != 0xb0 || burst[34] != 0x00 {
		t.Fatalf("burst[33:35] = %02x %02x, want b0 00 (ZC_PAR_CHANGE)", burst[33], burst[34])
	}
	// ZC_NOTIFY_VANISH @ [41:48] — 7 bytes, opcode 0x0080.
	if burst[41] != 0x80 || burst[42] != 0x00 {
		t.Fatalf("burst[41:43] = %02x %02x, want 80 00 (ZC_NOTIFY_VANISH)", burst[41], burst[42])
	}
	if gid := binary.LittleEndian.Uint32(burst[43:47]); gid != poringGID {
		t.Errorf("vanish GID = %d, want %d", gid, poringGID)
	}

	// Wait for the scheduled respawn (mirrors TestDispatchHandler_Attack_MonsterRespawns).
	var respawnPkt []byte
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		for _, p := range resp.GetPackets() {
			if len(p) >= 9 && binary.LittleEndian.Uint16(p[0:2]) == 0x09ff &&
				binary.LittleEndian.Uint32(p[5:9]) == poringGID {
				respawnPkt = p
				break
			}
		}
		if respawnPkt != nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if respawnPkt == nil {
		t.Fatal("timed out waiting for monster respawn or respawn packet")
	}
}

// TestDispatchHandler_CZUseSkill_BashInsufficientSP_AcksAndNoOps covers
// the negative path: SP=5, Bash L1 (cost=8) →
//
//	(1) ZC_ACK_TOUSESKILL (14 bytes) with Cause=UseSkillFailSPInsufficient
//	(2) SP unchanged (still 5)
//	(3) No ZC_NOTIFY_SKILL, no damage applied.
func TestDispatchHandler_CZUseSkill_BashInsufficientSP_AcksAndNoOps(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{
		ID:        1,
		AccountID: 200001,
		MonsterHP: map[uint32]int32{poringGID: 50},
	}
	conn.SetCombatStats(10, 5, 0)
	conn.SetSP(5, 100) // SpAt(1)=8 > 5 → must fail.

	reqBuf := buildCZUseSkill(t, skilldomain.SM_BASH, 1, poringGID)

	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZUSESKILL, reqBuf); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}

	out := resp.buf.Bytes()
	// Exactly one packet: ZC_ACK_TOUSESKILL (14 bytes).
	if len(out) != 14 {
		t.Fatalf("response length = %d, want 14 (ZC_ACK_TOUSESKILL only); buf=% x", len(out), out)
	}
	// Opcode = 0x0110.
	if out[0] != 0x10 || out[1] != 0x01 {
		t.Fatalf("opcode = %02x %02x, want 10 01 (LE 0x0110)", out[0], out[1])
	}
	// SkillID @ [2:4]
	if skillID := binary.LittleEndian.Uint16(out[2:4]); skillID != skilldomain.SM_BASH {
		t.Errorf("SkillID = %d, want %d (SM_BASH)", skillID, skilldomain.SM_BASH)
	}
	// Cause @ [13] = UseSkillFailSPInsufficient (12).
	if cause := out[13]; cause != packet.UseSkillFailSPInsufficient {
		t.Errorf("Cause = %d, want %d (UseSkillFailSPInsufficient)", cause, packet.UseSkillFailSPInsufficient)
	}

	// SP unchanged.
	if got := conn.SP(); got != 5 {
		t.Errorf("conn.SP() = %d, want 5 (no spend on insufficient-SP path)", got)
	}
	// Monster HP unchanged.
	if hp := conn.MonsterHP[poringGID]; hp != 50 {
		t.Errorf("monster HP = %d, want 50 (no damage on insufficient-SP path)", hp)
	}
}

// TestDispatchHandler_CZUseSkill_UnknownTarget_NoSPSpend is the
// regression for the HIGH-priority Gemini finding: a Bash cast against
// a monster that is not tracked (already dead or never spawned) must
// NOT deduct SP and must NOT emit a ZC_NOTIFY_SKILL / ZC_PAR_CHANGE
// burst. Pre-fix the handler called SpendSP before the ApplyDamage
// lookup, so a failed cast drained the SP cache while sending nothing
// to the client — leaving the client's SP display desynced from the
// server.
func TestDispatchHandler_CZUseSkill_UnknownTarget_NoSPSpend(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{
		ID:        1,
		AccountID: 200001,
		// Intentionally do NOT seed the target GID in MonsterHP.
		MonsterHP: map[uint32]int32{},
	}
	conn.SetCombatStats(10, 5, 0)
	conn.SetSP(20, 100)

	const unknownGID uint32 = 0xDEADBEEF
	reqBuf := buildCZUseSkill(t, skilldomain.SM_BASH, 1, unknownGID)

	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZUSESKILL, reqBuf); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}

	// (a) SP unchanged — SpendSP must not have been called.
	if got := conn.SP(); got != 20 {
		t.Errorf("conn.SP() = %d, want 20 (no spend on unknown-target path)", got)
	}
	// (b) No outbound burst.
	if n := resp.buf.Len(); n != 0 {
		t.Errorf("response length = %d, want 0 (no ZC_NOTIFY_SKILL, no ZC_PAR_CHANGE); buf=% x", n, resp.buf.Bytes())
	}
	// (c) MonsterHP untouched — the unknown GID should not have been
	// inserted as a side effect.
	if _, ok := conn.MonsterHP[unknownGID]; ok {
		t.Errorf("unknown GID %d was inserted into MonsterHP", unknownGID)
	}
}

// TestDispatchHandler_CZUseSkill_NonBashSkill_DropsSilently is the
// regression for FINDING 4: the pct=100+30*level formula and the
// statsdomain.BashDamage scaling are Bash-specific. A future offensive
// skill (Pierce, Magnum Break, …) must NOT silently inherit them; the
// handler must drop non-Bash use-skill requests without spending SP or
// emitting any burst.
//
// The registry currently only contains SM_BASH as an offensive skill,
// so we drive a non-Bash numeric ID (6 — no SM_PROVOKE in this slice's
// registry). The test asserts the observable behavior: no SP drain,
// no burst, monster HP untouched.
func TestDispatchHandler_CZUseSkill_NonBashSkill_DropsSilently(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{
		ID:        1,
		AccountID: 200001,
		MonsterHP: map[uint32]int32{poringGID: 50},
	}
	conn.SetCombatStats(10, 5, 0)
	conn.SetSP(20, 100)

	const nonBashSkillID uint16 = 6
	reqBuf := buildCZUseSkill(t, nonBashSkillID, 1, poringGID)

	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZUSESKILL, reqBuf); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}

	if got := conn.SP(); got != 20 {
		t.Errorf("conn.SP() = %d, want 20 (non-Bash must not spend SP)", got)
	}
	if n := resp.buf.Len(); n != 0 {
		t.Errorf("response length = %d, want 0 (non-Bash must not emit a burst); buf=% x", n, resp.buf.Bytes())
	}
	if hp := conn.MonsterHP[poringGID]; hp != 50 {
		t.Errorf("monster HP = %d, want 50 (non-Bash must not apply damage)", hp)
	}
}

// TestAppendMonsterDeath_DropsRolled verifies that a successful kill
// rolls the mob_db drop table and appends one ZC_ITEM_FALL_ENTRY
// (0x0ADD) frame per winning drop into the response burst. The test
// stubs dropRoll to always return true so every entry wins, then walks
// the burst to find the 22-byte drop frames and checks their (X, Y)
// match the Poring spawn point.
func TestAppendMonsterDeath_DropsRolled(t *testing.T) {
	t.Parallel()

	// Build a minimal mob_db with one drop entry on Poring (id=1002).
	const mobID int32 = 1002
	mobs := &mobdb.Registry{}
	// The Registry zero-value has no public insert path; use Load on a
	// tiny in-memory YAML fixture instead so we exercise the real
	// loader and match how DI wires the registry.
	yamlBody := `Header:
  Type: MOB_DB
  Version: 5
Body:
  - Id: 1002
    AegisName: PORING
    Name: Poring
    Level: 1
    Hp: 50
    BaseExp: 2
    JobExp: 1
    Attack: 7
    Attack2: 10
    Defense: 0
    MagicDefense: 5
    Str: 1
    Agi: 1
    Vit: 0
    Int: 0
    Dex: 1
    Luk: 1
    WalkSpeed: 400
    AttackRange: 1
    ChaseRange: 12
    Size: Medium
    Race: Plant
    Element: Water
    ElementLevel: 1
    Ai: "02"
    Drops:
      - Item: Jellopy
        Rate: 10000
      - Item: Sticky_Mucus
        Rate: 10000
`
	mobs, err := mobdb.Load(bytes.NewBufferString(yamlBody))
	if err != nil {
		t.Fatalf("mobdb.Load: %v", err)
	}
	if got := mobs.Get(mobID); got == nil {
		t.Fatalf("mobs.Get(%d) = nil, want entry", mobID)
	}

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), mobs, nil)
	h.respawnDelay = time.Hour // respawn irrelevant for this assertion

	// Force every drop to win.
	h.setDropRoll(func(_ int) bool { return true })
	// Force max damage so the Poring dies on the first hit.
	h.setDamageRoll(func(min, max int32) int32 { return max })

	resp := &safeResponder{}
	conn := domain.ConnectionInfo{
		ID:        1,
		AccountID: 200001,
		BaseExp:   0,
		JobExp:    0,
	}
	conn.InitMonsterHP([]domain.MonsterSpawn{{GID: poringGID, MaxHP: 5}})
	conn.SetCombatStats(10, 5, 0)

	req := packet.CZActionRequestRequest{TargetGID: poringGID, Action: packet.DMGNormal}
	var reqBuf bytes.Buffer
	if err := req.Encode(&reqBuf); err != nil {
		t.Fatalf("Encode CZ_ACTION_REQUEST: %v", err)
	}

	if err := h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZACTIONREQUEST, reqBuf.Bytes()); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}

	pkts := resp.GetPackets()
	if len(pkts) < 1 {
		t.Fatalf("got %d packets, want ≥1 (the burst)", len(pkts))
	}
	burst := pkts[0]

	// Walk the burst for ZC_ITEM_FALL_ENTRY (opcode 0x0ADD) frames.
	// The pre-drop prefix is ZC_NOTIFY_ACT (34) — opcode 0x08c8, type
	// = DMGNormal at offset 29. Drop frames follow ZC_NOTIFY_VANISH
	// (7) and the two ZC_LONGPAR_CHANGE EXP packets (8 + 8).
	const (
		sizeNotifyAct    = 34
		sizeNotifyVanish = 7
		sizeLongPar      = 8
	)
	sizeZCItemFallEntry := (&packet.ItemFallEntryResponse{}).Size()
	// Find every 22-byte window whose first two bytes are DD 0A
	// (little-endian 0x0ADD).
	var dropFrames [][]byte
	for i := 0; i+sizeZCItemFallEntry <= len(burst); i++ {
		if burst[i] == 0xDD && burst[i+1] == 0x0A {
			dropFrames = append(dropFrames, burst[i:i+sizeZCItemFallEntry])
			i += sizeZCItemFallEntry - 1
		}
	}
	if len(dropFrames) != 2 {
		t.Fatalf("got %d ZC_ITEM_FALL_ENTRY frames in burst, want 2; burst=% x", len(dropFrames), burst)
	}

	// Both drops should land at the Poring spawn (155, 165).
	for i, frame := range dropFrames {
		if got := binary.LittleEndian.Uint16(frame[11:13]); got != 155 {
			t.Errorf("drop[%d] X = %d, want 155", i, got)
		}
		if got := binary.LittleEndian.Uint16(frame[13:15]); got != 165 {
			t.Errorf("drop[%d] Y = %d, want 165", i, got)
		}
		if got := binary.LittleEndian.Uint16(frame[6:8]); got != 0 {
			t.Errorf("drop[%d] NameID = %d, want 0 (item DB deferred)", i, got)
		}
		if frame[10] != 1 {
			t.Errorf("drop[%d] Identified = %d, want 1", i, frame[10])
		}
	}

	// Ground item IDs must be unique and start at 1.
	id0 := binary.LittleEndian.Uint32(dropFrames[0][2:6])
	id1 := binary.LittleEndian.Uint32(dropFrames[1][2:6])
	if id0 == 0 || id1 == 0 || id0 == id1 {
		t.Errorf("ground IDs = (%d, %d), want distinct non-zero", id0, id1)
	}

	// Sanity-check the pre-drop layout to confirm the drops really
	// trail the vanish frame, not the act frame.
	if burst[0] != 0xc8 || burst[1] != 0x08 {
		t.Fatalf("burst[0:2] = % x, want c8 08 (ZC_NOTIFY_ACT)", burst[0:2])
	}
	vanishOff := sizeNotifyAct
	if burst[vanishOff] != 0x80 || burst[vanishOff+1] != 0x00 {
		t.Fatalf("burst[%d:%d] = % x, want 80 00 (ZC_NOTIFY_VANISH)",
			vanishOff, vanishOff+2, burst[vanishOff:vanishOff+2])
	}
	if gid := binary.LittleEndian.Uint32(burst[vanishOff+2 : vanishOff+6]); gid != poringGID {
		t.Errorf("vanish GID = %d, want %d", gid, poringGID)
	}
	// The first drop should follow vanish; EXP longpar packets come
	// after the drops (appendMonsterDeath appends drops before EXP so
	// the client's "loot appears then EXP toast" UX matches rAthena's
	// order).
	firstDropOff := sizeNotifyAct + sizeNotifyVanish
	if burst[firstDropOff] != 0xDD || burst[firstDropOff+1] != 0x0A {
		t.Fatalf("burst[%d:%d] = % x, want DD 0A (ZC_ITEM_FALL_ENTRY)",
			firstDropOff, firstDropOff+2, burst[firstDropOff:firstDropOff+2])
	}
}

// compileForTest parses and compiles a script source string via the
// real parser/compiler pipeline so dialog tests exercise the same
// bytecode production dialog scripts use in production. Mirrors the
// helper in dialog_test.go but kept locally because dispatch tests
// pull in only the gateway service package's import graph.
func compileForTest(t *testing.T, src string) *script.CompiledScript {
	t.Helper()
	tokens, err := script.Lex([]byte(src))
	require.NoError(t, err)
	stmts, err := parser.New(tokens).ParseStmts()
	require.NoError(t, err)
	cs, err := compiler.New().Compile("dialog_test", stmts)
	require.NoError(t, err)
	return cs
}

// scriptSetWith builds a single-script CompiledScriptSet keyed by
// name. Used by u2 dispatch tests to inject a deterministic script
// snapshot without going through the script engine loader.
func scriptSetWith(name string, cs *script.CompiledScript) *script.CompiledScriptSet {
	set := script.NewCompiledScriptSet()
	set.Scripts[name] = cs
	return set
}

// buildCZChooseMenu crafts a 7-byte CZ_CHOOSE_MENU frame. The packet
// struct has no Encode method (it's a decoded form only), so tests
// write the bytes directly.
func buildCZChooseMenu(npcID uint32, selected int8) []byte {
	frame := make([]byte, 7)
	binary.LittleEndian.PutUint16(frame[0:2], packet.HeaderCZCHOOSEMENU)
	binary.LittleEndian.PutUint32(frame[2:6], npcID)
	frame[6] = byte(selected) //nolint:gosec // int8→byte is the wire convention; 0xff means "cancel"
	return frame
}

// TestDispatchHandler_CZContactNPC_ScriptNPC_RunsVM confirms that
// clicking an NPC with a non-empty ScriptName resolves in the
// configured scriptSet, drives the DialogSession VM, and emits
// ZC_SAY_DIALOG2 + ZC_WAIT_DIALOG2 from the dialog builtins (the
// same wire output as the u1 tests, but now reached through the
// dispatch handler rather than NewDialogSession directly).
func TestDispatchHandler_CZContactNPC_ScriptNPC_RunsVM(t *testing.T) {
	t.Parallel()

	const scriptName = "SampleNPC"
	cs := compileForTest(t, `mes "Hello!"; next;`)
	scripts := scriptSetWith(scriptName, cs)

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, scripts)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 42, AccountID: 100}

	// Guide NPC (GID 110000004) has ScriptName="SampleNPC".
	req := packet.CZContactNPCRequest{AID: 110000004, Type: 0x01}
	var reqBuf bytes.Buffer
	require.NoError(t, req.Encode(&reqBuf))

	require.NoError(t, h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZCONTACTNPC, reqBuf.Bytes()))

	out := resp.buf.Bytes()
	if len(out) == 0 {
		t.Fatal("responder wrote 0 bytes, want ZC_SAY_DIALOG2 + ZC_WAIT_DIALOG2 from script")
	}

	// First packet: ZC_SAY_DIALOG2 with the script text.
	if out[0] != 0x72 || out[1] != 0x09 {
		t.Fatalf("first packet header = %02x %02x, want 72 09 (ZC_SAY_DIALOG2)", out[0], out[1])
	}
	sayLen := int(binary.LittleEndian.Uint16(out[2:4]))
	if nid := binary.LittleEndian.Uint32(out[4:8]); nid != 110000004 {
		t.Errorf("ZC_SAY_DIALOG2 NpcID = %d, want 110000004", nid)
	}
	if msg := cstrBytes(out[9:sayLen]); msg != "Hello!" {
		t.Errorf("ZC_SAY_DIALOG2 message = %q, want %q", msg, "Hello!")
	}

	// Second packet: ZC_WAIT_DIALOG2 (built by next()).
	const zcWaitDialog2Size = 7
	if len(out) < sayLen+zcWaitDialog2Size {
		t.Fatalf("output too short for ZC_WAIT_DIALOG2: %d bytes", len(out))
	}
	waitFrame := out[sayLen : sayLen+zcWaitDialog2Size]
	if waitFrame[0] != 0x73 || waitFrame[1] != 0x09 {
		t.Fatalf("second packet = %02x %02x, want 73 09 (ZC_WAIT_DIALOG2)", waitFrame[0], waitFrame[1])
	}

	// The session must be parked in the dialogSessions map (paused
	// after next()) so a follow-up CZ_REQNEXTSCRIPT can resume it.
	if _, ok := h.dialogSessions.Load(conn.ID); !ok {
		t.Fatal("expected active dialog session stored under conn.ID after script-driven contact")
	}
}

// TestDispatchHandler_CZReqNextScript_ScriptNPC_ResumesVM exercises
// the resume path: a CZ_CONTACTNPC opens a session paused at next();
// a subsequent CZ_REQNEXTSCRIPT should drive the next script page
// and emit the second ZC_SAY_DIALOG2 + ZC_CLOSE_DIALOG.
func TestDispatchHandler_CZReqNextScript_ScriptNPC_ResumesVM(t *testing.T) {
	t.Parallel()

	const scriptName = "SampleNPC"
	cs := compileForTest(t, `mes "First"; next; mes "Second"; close;`)
	scripts := scriptSetWith(scriptName, cs)

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, scripts)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 7, AccountID: 100}

	contact := packet.CZContactNPCRequest{AID: 110000004, Type: 0x01}
	var contactBuf bytes.Buffer
	require.NoError(t, contact.Encode(&contactBuf))
	require.NoError(t, h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZCONTACTNPC, contactBuf.Bytes()))

	firstOut := append([]byte(nil), resp.buf.Bytes()...)
	if len(firstOut) == 0 {
		t.Fatal("contact produced no output")
	}

	// Second packet on the same responder: CZ_REQNEXTSCRIPT.
	next := packet.CZReqNextScriptRequest{NpcID: 110000004}
	var nextBuf bytes.Buffer
	require.NoError(t, next.Encode(&nextBuf))
	require.NoError(t, h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZREQNEXTSCRIPT, nextBuf.Bytes()))

	// After the second packet, the VM has run to completion
	// (close -> StateEnd), so the session should be evicted.
	if _, ok := h.dialogSessions.Load(conn.ID); ok {
		t.Fatal("expected dialog session to be cleared after close()")
	}

	// Decode the appended bytes (after firstOut) — should be a
	// second ZC_SAY_DIALOG2 ("Second") followed by ZC_CLOSE_DIALOG.
	rest := resp.buf.Bytes()[len(firstOut):]
	if len(rest) == 0 {
		t.Fatal("CZ_REQNEXTSCRIPT produced no output")
	}
	if rest[0] != 0x72 || rest[1] != 0x09 {
		t.Fatalf("first appended packet = %02x %02x, want 72 09 (ZC_SAY_DIALOG2)", rest[0], rest[1])
	}
	sayLen := int(binary.LittleEndian.Uint16(rest[2:4]))
	if msg := cstrBytes(rest[9:sayLen]); msg != "Second" {
		t.Errorf("resumed ZC_SAY_DIALOG2 message = %q, want %q", msg, "Second")
	}

	closeStart := sayLen
	const zcCloseDialogSize = 6
	if len(rest) < closeStart+zcCloseDialogSize {
		t.Fatalf("resumed output too short for ZC_CLOSE_DIALOG: %d bytes", len(rest))
	}
	closeFrame := rest[closeStart : closeStart+zcCloseDialogSize]
	if closeFrame[0] != 0xb6 || closeFrame[1] != 0x00 {
		t.Fatalf("appended packet = %02x %02x, want b6 00 (ZC_CLOSE_DIALOG)", closeFrame[0], closeFrame[1])
	}
}

// TestDispatchHandler_CZChooseMenu_ScriptNPC_SetsMenuAndResumes
// exercises the menu path: contact drives mes+next+menu; the menu
// emits ZC_MENU_LIST and pauses the VM; CZ_CHOOSE_MENU sets
// ".@menu" and resumes. The subsequent page picks a label based on
// the chosen index — here L_First (index 1).
func TestDispatchHandler_CZChooseMenu_ScriptNPC_SetsMenuAndResumes(t *testing.T) {
	t.Parallel()

	const scriptName = "SampleNPC"
	cs := compileForTest(t, `mes "Pick:"; menu "First", L_First, "Second", L_Second;
L_First:
	mes "You picked first"; close;
L_Second:
	mes "You picked second"; close;`)
	scripts := scriptSetWith(scriptName, cs)

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, scripts)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 11, AccountID: 100}

	contact := packet.CZContactNPCRequest{AID: 110000004, Type: 0x01}
	var contactBuf bytes.Buffer
	require.NoError(t, contact.Encode(&contactBuf))
	require.NoError(t, h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZCONTACTNPC, contactBuf.Bytes()))

	out := resp.buf.Bytes()
	if len(out) == 0 {
		t.Fatal("contact produced no output")
	}

	// Parse the menu packet off the front (should be SayDialog2 +
	// MenuList; say "Pick:" then ZC_MENU_LIST with "First:Second").
	// We don't slice frame-by-frame; instead grab the trailing
	// ZC_MENU_LIST by searching for its header.
	menuOff := -1
	for i := 0; i+2 <= len(out); i++ {
		if out[i] == 0xb7 && out[i+1] == 0x00 {
			menuOff = i
			break
		}
	}
	if menuOff < 0 {
		t.Fatalf("no ZC_MENU_LIST (00 b7) found in initial output % x", out)
	}

	// The session must be parked after menu.
	if _, ok := h.dialogSessions.Load(conn.ID); !ok {
		t.Fatal("expected dialog session parked after menu()")
	}

	// Player picks "First" (index 1, 1-based per rAthena).
	require.NoError(t, h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZCHOOSEMENU, buildCZChooseMenu(110000004, 1)))

	// Session should be cleared: the script went mes + close.
	if _, ok := h.dialogSessions.Load(conn.ID); ok {
		t.Fatal("expected dialog session cleared after menu path closed")
	}

	// The script also reads .@menu — the VM's ScopeStore holds the
	// value. Verify by inspecting the session via a fresh re-store
	// trick: the session was just deleted, so re-resolve by running
	// the same menu path with an injected session is overkill. The
	// wire assertion below (resumed output references the picked
	// label) is sufficient: if the resume ignored the selected
	// index, it would fall into L_Second instead of L_First and the
	// final text would differ.
	rest := resp.buf.Bytes()[len(out):]
	if len(rest) == 0 {
		t.Fatal("CZ_CHOOSE_MENU produced no output")
	}
	// First appended packet must be ZC_SAY_DIALOG2 flushing the
	// accumulated "Pick:" text plus a newline + the picked-label
	// "You picked first" (the menu() builtin pauses the VM
	// *without* emitting SayDialog2, so the mes "Pick:" text
	// accumulated up to menu is flushed on the next page).
	if rest[0] != 0x72 || rest[1] != 0x09 {
		t.Fatalf("first appended packet = %02x %02x, want 72 09 (ZC_SAY_DIALOG2)", rest[0], rest[1])
	}
	sayLen := int(binary.LittleEndian.Uint16(rest[2:4]))
	if msg := cstrBytes(rest[9:sayLen]); msg != "Pick:\nYou picked first" {
		t.Errorf("resumed message = %q, want %q (wrong branch taken)", msg, "Pick:\nYou picked first")
	}
}

// TestDispatchHandler_CZChooseMenu_NoActiveSession_DropsSilently
// confirms that a stray CZ_CHOOSE_MENU without a parked session is
// logged-and-dropped rather than crashing or sending a fallback
// reply.
func TestDispatchHandler_CZChooseMenu_NoActiveSession_DropsSilently(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 100}

	require.NoError(t, h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZCHOOSEMENU, buildCZChooseMenu(110000004, 1)))

	if got := resp.buf.Len(); got != 0 {
		t.Fatalf("responder wrote %d bytes for stray CZ_CHOOSE_MENU, want 0", got)
	}
}

// TestDispatchHandler_CZCloseDialog_DropsSession confirms that
// CZ_CLOSE_DIALOG evicts the active dialog session so a subsequent
// CZ_CONTACTNPC starts fresh.
func TestDispatchHandler_CZCloseDialog_DropsSession(t *testing.T) {
	t.Parallel()

	cs := compileForTest(t, `mes "Hi"; close;`)
	scripts := scriptSetWith("SampleNPC", cs)

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, scripts)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 99, AccountID: 100}

	contact := packet.CZContactNPCRequest{AID: 110000004, Type: 0x01}
	var contactBuf bytes.Buffer
	require.NoError(t, contact.Encode(&contactBuf))
	require.NoError(t, h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZCONTACTNPC, contactBuf.Bytes()))

	// close() already evicted the session for a mes+close script,
	// so seed a parked session manually for the assertion below.
	h.dialogSessions.Store(conn.ID, NewDialogSession(compileForTest(t, `mes "Hi"; next;`), 1, "X", &bytes.Buffer{}))
	if _, ok := h.dialogSessions.Load(conn.ID); !ok {
		t.Fatal("setup: failed to seed dialog session")
	}

	closeReq := packet.CZCloseDialogRequest{GID: 110000004}
	var closeBuf bytes.Buffer
	require.NoError(t, closeReq.Encode(&closeBuf))
	require.NoError(t, h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZCLOSEDIALOG, closeBuf.Bytes()))

	if _, ok := h.dialogSessions.Load(conn.ID); ok {
		t.Fatal("expected dialog session dropped after CZ_CLOSE_DIALOG")
	}
}

// TestDispatchHandler_CZContactNPC_DialogNPC_NoScript_FallsBack
// confirms the hardcoded M15 welcome is still emitted when the
// scriptSet is nil OR the NPC's ScriptName doesn't resolve.
func TestDispatchHandler_CZContactNPC_DialogNPC_NoScript_FallsBack(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1, AccountID: 100}

	// Kafra Employee has no ScriptName → hardcoded fallback.
	req := packet.CZContactNPCRequest{AID: 110000001, Type: 0x01}
	var reqBuf bytes.Buffer
	require.NoError(t, req.Encode(&reqBuf))
	require.NoError(t, h.HandlePacket(context.Background(), &conn, resp,
		packet.HeaderCZCONTACTNPC, reqBuf.Bytes()))

	out := resp.buf.Bytes()
	if len(out) == 0 {
		t.Fatal("responder wrote 0 bytes, want fallback ZC_SAY_DIALOG2 + ZC_WAIT_DIALOG2")
	}
	// Hardcoded message references the NPC's Name field.
	if out[0] != 0x72 || out[1] != 0x09 {
		t.Fatalf("first packet header = %02x %02x, want 72 09 (ZC_SAY_DIALOG2)", out[0], out[1])
	}
	sayLen := int(binary.LittleEndian.Uint16(out[2:4]))
	if msg := cstrBytes(out[9:sayLen]); msg != "Welcome to goAthena! This is Kafra Employee." {
		t.Errorf("fallback message = %q, want %q", msg, "Welcome to goAthena! This is Kafra Employee.")
	}

	if _, ok := h.dialogSessions.Load(conn.ID); ok {
		t.Fatal("fallback path must not seed a dialog session")
	}
}
