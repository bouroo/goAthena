//go:build unit

// WS-driven end-to-end char-list test. This file spawns a real
// *handler.WSHandler in-process, dials it with a coder/websocket
// client as a roBrowser-style peer, sends a real CH_ENTER frame, and
// asserts the dispatcher emits the rAthena headerless AID echo
// followed by an HC_ACCEPT_ENTER (cmd 0x006b) carrying one
// CHARACTER_INFO entry per fake identity row. It is the L3 evidence
// for the M2b char-list increment.
package handler

import (
	"bytes"
	"context"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/rs/zerolog"

	identityv1 "github.com/bouroo/goAthena/api/pb/identity/v1"
	"github.com/bouroo/goAthena/internal/features/gateway/domain"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// wsCharDispatchAdapter is a minimal test double that handles CH_ENTER
// the same way the production dispatch handler does: call identity
// GetCharacterList, send the 4-byte AID echo, then encode and send
// HC_ACCEPT_ENTER. It mirrors service.DispatchHandler for the WS path
// so this test exercises the real WSHandler → processBytes →
// domain.PacketHandler → identity client → HC_ACCEPT_ENTER → WS write
// round trip. If service/dispatch.go's wire mapping changes, mirror
// the change here.
type wsCharDispatchAdapter struct {
	identity   identityv1.IdentityServiceClient
	defaultMap string
	zoneHost   string
	zonePort   uint16
	logger     zerolog.Logger
}

func (a *wsCharDispatchAdapter) HandlePacket(ctx context.Context, _ domain.ConnectionInfo, resp domain.Responder, cmd uint16, frame []byte) error {
	switch cmd {
	case packet.HeaderCHENTER:
		req, parseErr := packet.ParseCHEnter(frame)
		if parseErr != nil {
			a.logger.Warn().Err(parseErr).Msg("malformed CH_ENTER; dropping")
			return nil
		}

		listResp, err := a.identity.GetCharacterList(ctx, &identityv1.GetCharacterListRequest{
			AccountId: req.AccountID,
			LoginId1:  uint64(req.LoginID1),
			Sex:       wsCharSexString(req.Sex),
		})
		if err != nil || listResp == nil {
			return wsCharSendRefuse(resp, 99)
		}

		// (1) Headerless AID echo (4 bytes LE).
		echo := make([]byte, 4)
		binary.LittleEndian.PutUint32(echo, req.AccountID)
		if err := resp.SendPacket(echo); err != nil {
			return err
		}

		// (2) HC_ACCEPT_ENTER.
		chars := listResp.GetCharacters()
		entries := make([]packet.CharacterInfo, 0, len(chars))
		for _, ch := range chars {
			if ch == nil {
				continue
			}
			entries = append(entries, packet.CharacterInfo{
				GID:      ch.GetCharId(),
				Job:      int16(ch.GetClassId()),
				Level:    int16(ch.GetBaseLevel()),
				JobLevel: int32(ch.GetJobLevel()),
				Name:     ch.GetName(),
				CharNum:  uint8(ch.GetSlot()),
				Sex:      req.Sex,
			})
		}
		accept := packet.AcceptEnterResponse{
			Total:      uint8(len(entries)),
			Characters: entries,
		}
		var buf bytes.Buffer
		if err := accept.Encode(&buf); err != nil {
			return err
		}
		return resp.SendPacket(buf.Bytes())
	default:
		return nil
	}
}

// wsCharSexString mirrors service.sexString — kept inline to keep
// this test self-contained.
func wsCharSexString(b uint8) string {
	switch b {
	case 0:
		return "F"
	case 1:
		return "M"
	default:
		return "S"
	}
}

// wsCharSendRefuse mirrors sendCharRefuse — minimal HC_REFUSE_ENTER.
func wsCharSendRefuse(resp domain.Responder, code uint8) error {
	refuse := packet.RefuseEnterResponse{Error: code}
	var buf bytes.Buffer
	if err := refuse.Encode(&buf); err != nil {
		return err
	}
	return resp.SendPacket(buf.Bytes())
}

// buildCHEnter crafts the 17-byte CH_ENTER frame the rAthena client
// sends right after the char-server connection opens (rathena/src/
// common/packets.hpp PACKET_CH_ENTER).
func buildCHEnter(accountID, loginID1, loginID2 uint32, sex uint8) []byte {
	const size = 17
	frame := make([]byte, size)
	binary.LittleEndian.PutUint16(frame[0:2], packet.HeaderCHENTER)
	binary.LittleEndian.PutUint32(frame[2:6], accountID)
	binary.LittleEndian.PutUint32(frame[6:10], loginID1)
	binary.LittleEndian.PutUint32(frame[10:14], loginID2)
	// [14:16] = reserved uint16 (zero in rAthena test fixtures).
	frame[16] = sex
	return frame
}

// TestWSHandler_CHEnter_RoundTrip_HCAcceptEnter is the M2b headline
// evidence. The fake identity client returns 2 characters; the
// dispatcher must write:
//
//   - a 4-byte AID echo (LE uint32) — first message
//   - an HC_ACCEPT_ENTER with header 0x6b 0x00, packetLength = 27 +
//     175*2 = 377, and two embedded CHARACTER_INFO entries whose GIDs
//     match the fake client — second message
//
// coder/websocket does not coalesce two writes on the same connection
// into one frame, so we read two consecutive binary messages.
func TestWSHandler_CHEnter_RoundTrip_HCAcceptEnter(t *testing.T) {
	fake := &loginFakeIdentityClient{
		charsFn: func(_ context.Context, _ *identityv1.GetCharacterListRequest) (*identityv1.GetCharacterListResponse, error) {
			return &identityv1.GetCharacterListResponse{
				Characters: []*identityv1.CharacterInfo{
					{CharId: 101, Slot: 0, Name: "Alpha", ClassId: 0, BaseLevel: 50, JobLevel: 1},
					{CharId: 102, Slot: 1, Name: "Beta", ClassId: 7, BaseLevel: 99, JobLevel: 50},
				},
				TotalSlots: 9,
			}, nil
		},
	}
	adapter := &wsCharDispatchAdapter{
		identity:   fake,
		defaultMap: "prontera",
		zoneHost:   "127.0.0.1",
		zonePort:   5121,
		logger:     zerolog.New(zerolog.NewTestWriter(t)).Level(zerolog.Disabled),
	}

	db := packet.NewLoginServerDB()
	db.Merge(packet.NewCharServerDB())
	h := NewWSHandler(db, adapter, "unused", "/ws/",
		zerolog.New(zerolog.NewTestWriter(t)).Level(zerolog.Disabled), nil)

	mux := http.NewServeMux()
	mux.HandleFunc(h.path, h.ServeHTTP)
	mux.HandleFunc("/", h.rejectNonUpgrade)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	wsURL := wsURLFromTestServer(t, srv.URL) + h.path

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer func() { _ = client.Close(websocket.StatusNormalClosure, "") }()

	const accountID uint32 = 4242
	frame := buildCHEnter(accountID, 0xdead, 0xbeef, 1 /* sex=M */)
	if err := client.Write(ctx, websocket.MessageBinary, frame); err != nil {
		t.Fatalf("ws write CH_ENTER: %v", err)
	}

	// (1) Read the headerless AID echo.
	readCtx, readCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer readCancel()
	if _, data, err := client.Read(readCtx); err != nil {
		t.Fatalf("ws read AID echo: %v", err)
	} else if len(data) != 4 {
		t.Fatalf("AID echo length = %d, want 4", len(data))
	} else if got := binary.LittleEndian.Uint32(data); got != accountID {
		t.Fatalf("AID echo = %d, want %d", got, accountID)
	}

	// (2) Read the HC_ACCEPT_ENTER frame.
	if _, data, err := client.Read(readCtx); err != nil {
		t.Fatalf("ws read HC_ACCEPT_ENTER: %v", err)
	} else {
		const wantLen = 27 + 2*packet.CharacterInfoSize
		if len(data) != wantLen {
			t.Fatalf("HC_ACCEPT_ENTER length = %d, want %d (27 + 2*175)", len(data), wantLen)
		}
		if data[0] != 0x6b || data[1] != 0x00 {
			t.Fatalf("HC_ACCEPT_ENTER header = %02x %02x, want 6b 00", data[0], data[1])
		}
		if pl := binary.LittleEndian.Uint16(data[2:4]); int(pl) != wantLen {
			t.Fatalf("packetLength = %d, want %d", pl, wantLen)
		}
		if total := data[4]; total != 2 {
			t.Fatalf("total byte at offset 4 = %d, want 2 (matches fake char count)", total)
		}
		// First embedded CHARACTER_INFO starts at offset 27; GID at [27:31] LE.
		if g := binary.LittleEndian.Uint32(data[27:31]); g != 101 {
			t.Fatalf("first embedded GID at [27:31] = %d, want 101", g)
		}
		// Second embedded CHARACTER_INFO starts at offset 27+175=202; GID at [202:206] LE.
		if g := binary.LittleEndian.Uint32(data[202:206]); g != 102 {
			t.Fatalf("second embedded GID at [202:206] = %d, want 102", g)
		}
	}
}
