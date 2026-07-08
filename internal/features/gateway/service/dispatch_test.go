//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"sync"
	"testing"

	"github.com/rs/zerolog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	identityv1 "github.com/bouroo/goAthena/api/pb/identity/v1"
	zonev1 "github.com/bouroo/goAthena/api/pb/zone/v1"
	"github.com/bouroo/goAthena/internal/features/gateway/domain"
	"github.com/bouroo/goAthena/pkg/ro/packet"
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
	h := NewDispatchHandler(fake, nil, 20250604, newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121)

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
	h := NewDispatchHandler(fake, nil, 20250604, newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121)
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
	h := NewDispatchHandler(fake, nil, 20250604, newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121)
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
	h := NewDispatchHandler(fake, nil, 20250604, newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121)
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
	h := NewDispatchHandler(fake, nil, 20250604, newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121)
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
	h := NewDispatchHandler(fake, nil, 20250604, newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121)
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
	h := NewDispatchHandler(fake, nil, 20250604, newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121)
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
	h := NewDispatchHandler(fake, nil, 20250604, newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121)
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
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121)

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
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121)

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
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121)

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
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121)

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
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121)

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
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121)

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
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121)

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
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121)

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
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121)

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
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121)

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
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121)

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

	// (2) The remainder must contain ZC_STATUS (0x00bd) somewhere in
	// the stream — scan for the header byte pair.
	if !bytes.Contains(out[8:], []byte{0xbd, 0x00}) {
		t.Fatalf("post-mapprop stream does not contain ZC_STATUS header [bd 00]; bytes=% x", out[8:])
	}

	// (3) Spot-check one ZC_PAR_CHANGE with SP_HP (varID=5, count=1234).
	// Layout of one ZC_PAR_CHANGE: cmd(2) varID(2) count(4) = 8 bytes.
	var hpOK bool
	for i := 8; i+8 <= len(out); i++ {
		if out[i] == 0xb0 && out[i+1] == 0x00 {
			vid := binary.LittleEndian.Uint16(out[i+2 : i+4])
			cnt := int32(binary.LittleEndian.Uint32(out[i+4 : i+8]))
			if vid == packet.SPHP && cnt == 1234 {
				hpOK = true
				break
			}
		}
	}
	if !hpOK {
		t.Errorf("no ZC_PAR_CHANGE with SP_HP=1234 found in burst; bytes=% x", out[8:])
	}

	// (4) Spot-check ZC_PAR_CHANGE SP_STATUSPOINT (varID=9, count=5).
	var spOK bool
	for i := 8; i+8 <= len(out); i++ {
		if out[i] == 0xb0 && out[i+1] == 0x00 {
			vid := binary.LittleEndian.Uint16(out[i+2 : i+4])
			cnt := int32(binary.LittleEndian.Uint32(out[i+4 : i+8]))
			if vid == packet.SPStatusPoint && cnt == 5 {
				spOK = true
				break
			}
		}
	}
	if !spOK {
		t.Errorf("no ZC_PAR_CHANGE with SP_STATUSPOINT=5 found in burst")
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
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121)

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
	if !bytes.Contains(out[8:], []byte{0xbd, 0x00}) {
		t.Fatalf("post-mapprop stream does not contain ZC_STATUS header; bytes=% x", out[8:])
	}

	// Verify HP was clamped to 1 (rAthena convention).
	var hpFound bool
	for i := 8; i+8 <= len(out); i++ {
		if out[i] == 0xb0 && out[i+1] == 0x00 {
			vid := binary.LittleEndian.Uint16(out[i+2 : i+4])
			if vid == packet.SPHP {
				cnt := int32(binary.LittleEndian.Uint32(out[i+4 : i+8]))
				if cnt != 1 {
					t.Errorf("fallback ZC_PAR_CHANGE SP_HP = %d, want 1 (rAthena clamps to min 1)", cnt)
				}
				hpFound = true
				break
			}
		}
	}
	if !hpFound {
		t.Errorf("no ZC_PAR_CHANGE with SP_HP found in fallback burst")
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
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121)

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
	if !bytes.Contains(out[8:], []byte{0xbd, 0x00}) {
		t.Fatalf("post-mapprop stream does not contain ZC_STATUS header; bytes=% x", out[8:])
	}
}

// CZ_REQUEST_TIME (0x007e, 6 bytes) dispatch tests.

func TestDispatchHandler_CZRequestTime_Success_EncodesZCNotifyTime(t *testing.T) {
	t.Parallel()

	h := NewDispatchHandler(&fakeIdentityClient{}, &fakeZoneClient{}, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121)

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
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121)

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
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121)

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
