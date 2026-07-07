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
	"github.com/bouroo/goAthena/internal/features/gateway/domain"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// fakeIdentityClient is a hand-written, in-process stand-in for
// identityv1.IdentityServiceClient. It records the most recent request
// and returns whatever the test installed via authenticateFn /
// characterListFn. We intentionally avoid mockgen here to keep the
// service tests self-contained and trivially diffable against the gRPC
// interface.
type fakeIdentityClient struct {
	mu              sync.Mutex
	authenticateFn  func(context.Context, *identityv1.AuthenticateRequest) (*identityv1.AuthenticateResponse, error)
	characterListFn func(context.Context, *identityv1.GetCharacterListRequest) (*identityv1.GetCharacterListResponse, error)
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

	if err := h.HandlePacket(context.Background(), conn, resp, packet.HeaderCALOGIN, frame); err != nil {
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

	if err := h.HandlePacket(context.Background(), domain.ConnectionInfo{ID: 1}, resp, packet.HeaderCALOGIN, frame); err != nil {
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

	if err := h.HandlePacket(context.Background(), domain.ConnectionInfo{ID: 1}, resp, packet.HeaderCALOGIN, frame); err != nil {
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

	if err := h.HandlePacket(context.Background(), domain.ConnectionInfo{ID: 1}, resp, packet.HeaderCALOGIN, frame); err != nil {
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

	if err := h.HandlePacket(ctx, domain.ConnectionInfo{ID: 1}, resp, packet.HeaderCALOGIN, frame); err != nil {
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
	if err := h.HandlePacket(context.Background(), domain.ConnectionInfo{ID: 1}, resp, packet.HeaderCALOGIN, short); err != nil {
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
	if err := h.HandlePacket(context.Background(), conn, resp, packet.HeaderCALOGIN, frame); err != nil {
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

	if err := h.HandlePacket(context.Background(), domain.ConnectionInfo{ID: 1}, resp, packet.HeaderCHENTER, chEnterFrame(1)); err != nil {
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
