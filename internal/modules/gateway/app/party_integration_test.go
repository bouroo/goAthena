//go:build integration

package app_test

import (
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"

	chardomain "github.com/bouroo/goAthena/internal/modules/character/domain"
	charinfra "github.com/bouroo/goAthena/internal/modules/character/infra"
	partyapp "github.com/bouroo/goAthena/internal/modules/social/party/app"
	partyinfra "github.com/bouroo/goAthena/internal/modules/social/party/infra"
	ropacket "github.com/bouroo/goAthena/pkg/ro/packet"
)

// The M11 party L3 proofs. The unit suite covers the state machine and the
// repository round-trips; what only an end-to-end run can show is the wire
// behaviour: that a created party comes back as ZC_ACK_MAKE_GROUP followed by a
// ZC_GROUP_LIST naming the leader, and that a client entering the map while
// already grouped is handed its roster during the LoadEndAck burst
// (ZC_PARTY_CONFIG + ZC_GROUP_LIST), which is what rAthena's
// clif_parse_LoadEndAck → party_send_movemap path does.

// partyTestEnv is buildTestMapDeps' env plus the party repository the test seeds.
type partyTestEnv struct {
	mapTestEnv
	partyRepo *partyinfra.MemoryPartyRepository
}

// buildPartyTestEnv mirrors buildTestMapDeps but additionally wires a
// PartyService over an in-memory party repo with the test char registered, so
// the gateway's party verbs have a live backend.
func buildPartyTestEnv(t *testing.T, sessions *charinfra.MemorySessionStore, port int) (net.Conn, partyTestEnv) {
	t.Helper()
	ms, env := buildTestMapDeps(t, sessions)
	repo := partyinfra.NewMemoryPartyRepository()
	// The entering char (150001 / account 2000001) must exist as a party
	// candidate or Create rejects it. Seeded as the leader-to-be.
	repo.SetChar(150001, partyinfra.CharInfo{
		AccountID: 2000001, Name: "Hero", Map: "new_1-1", Class: 0, BaseLevel: 1, Online: true,
	})
	ms.SetParty(partyapp.NewPartyService(repo))

	conn := startAndDial(t, ms, port)
	sendCZEnter(t, conn, 2000001, 150001, 0x11111111)
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	awaitAcceptEnter(t, conn)
	return conn, partyTestEnv{mapTestEnv: env, partyRepo: repo}
}

// readFrame reads one frame's cmd and payload (everything after the 2-byte cmd).
func readFrame(t *testing.T, r io.Reader) (uint16, []byte) {
	t.Helper()
	var cmd [2]byte
	if _, err := io.ReadFull(r, cmd[:]); err != nil {
		t.Fatalf("read frame cmd: %v", err)
	}
	c := binary.LittleEndian.Uint16(cmd[:])
	// Every party reply here has a fixed size except ZC_GROUP_LIST, which
	// carries its length at offset 2 and is read by its own helper.
	switch c {
	case ropacket.HeaderZCACKMAKEGROUP:
		body := make([]byte, ropacket.AckMakeGroupResponse{}.Size()-2)
		if _, err := io.ReadFull(r, body); err != nil {
			t.Fatalf("read ZC_ACK_MAKE_GROUP body: %v", err)
		}
		return c, body
	case ropacket.HeaderZCPARTYCONFIG:
		body := make([]byte, ropacket.PartyConfigResponse{}.Size()-2)
		if _, err := io.ReadFull(r, body); err != nil {
			t.Fatalf("read ZC_PARTY_CONFIG body: %v", err)
		}
		return c, body
	case ropacket.HeaderZCGROUPLIST:
		lenBuf := make([]byte, 2)
		if _, err := io.ReadFull(r, lenBuf); err != nil {
			t.Fatalf("read ZC_GROUP_LIST len: %v", err)
		}
		total := int(binary.LittleEndian.Uint16(lenBuf))
		body := make([]byte, total-4)
		if _, err := io.ReadFull(r, body); err != nil {
			t.Fatalf("read ZC_GROUP_LIST body: %v", err)
		}
		return c, append(lenBuf, body...)
	default:
		t.Fatalf("unexpected frame cmd 0x%04x", c)
		return 0, nil
	}
}

// TestParty_CreateEmitsAckThenRoster drives CZ_MAKE_GROUP and asserts the two
// replies rAthena sends on a successful create: ZC_ACK_MAKE_GROUP result 0 and
// a ZC_GROUP_LIST whose single entry is the leader.
func TestParty_CreateEmitsAckThenRoster(t *testing.T) {
	port := freePort(t)
	sessions := charinfra.NewMemorySessionStore()
	_ = sessions.PutSession(t.Context(), chardomain.Session{
		AccountID: 2000001, LoginID1: 0x11111111, LoginID2: 0x22222222, Sex: 1,
	})
	conn, _ := buildPartyTestEnv(t, sessions, port)
	defer conn.Close()

	// CZ_MAKE_GROUP (0x00f9) is a fixed 26 bytes at this PACKETVER.
	frame := make([]byte, 26)
	binary.LittleEndian.PutUint16(frame[0:], ropacket.HeaderCZMAKEGROUP)
	copy(frame[2:26], "Athena")
	if _, err := conn.Write(frame); err != nil {
		t.Fatalf("send CZ_MAKE_GROUP: %v", err)
	}

	cmd, body := readFrame(t, conn)
	if cmd != ropacket.HeaderZCACKMAKEGROUP {
		t.Fatalf("first reply cmd = 0x%04x, want ZC_ACK_MAKE_GROUP", cmd)
	}
	if body[0] != ropacket.PartyCreateOK {
		t.Fatalf("create result = %d, want 0 (OK)", body[0])
	}

	cmd, body = readFrame(t, conn)
	if cmd != ropacket.HeaderZCGROUPLIST {
		t.Fatalf("second reply cmd = 0x%04x, want ZC_GROUP_LIST", cmd)
	}
	// body = [len(2)] + partyName(24) + N*54.
	if name := cstr(body[2:26]); name != "Athena" {
		t.Errorf("group list party name = %q, want %q", name, "Athena")
	}
	memberBytes := len(body) - 26
	if memberBytes != 54 {
		t.Fatalf("group list members = %d bytes, want one 54-byte entry", memberBytes)
	}
	member := body[26:]
	if aid := binary.LittleEndian.Uint32(member[0:4]); aid != 2000001 {
		t.Errorf("member AID = %d, want 2000001", aid)
	}
	if gid := binary.LittleEndian.Uint32(member[4:8]); gid != 150001 {
		t.Errorf("member GID = %d, want 150001", gid)
	}
	// leader byte is 0 for the leader (inverted on the rAthena wire).
	if member[44] != 0 {
		t.Errorf("leader byte = %d, want 0 (leader)", member[44])
	}
}

// TestParty_LoadEndAckRestoresRoster proves the map-enter restore: a char who
// is already grouped when it enters the map receives ZC_PARTY_CONFIG and its
// ZC_GROUP_LIST during the LoadEndAck burst, matching rAthena's
// party_send_movemap. Without this the client shows an empty party window.
func TestParty_LoadEndAckRestoresRoster(t *testing.T) {
	port := freePort(t)
	sessions := charinfra.NewMemorySessionStore()
	_ = sessions.PutSession(t.Context(), chardomain.Session{
		AccountID: 2000001, LoginID1: 0x11111111, LoginID2: 0x22222222, Sex: 1,
	})
	conn, env := buildPartyTestEnv(t, sessions, port)
	defer conn.Close()

	// Pre-group the char (as if it grouped on a previous map).
	if _, err := env.partyRepo.Create(t.Context(), 2000001, 150001, "Athena"); err != nil {
		t.Fatalf("seed party: %v", err)
	}

	sendLoadEndAck(t, conn)
	// The init burst precedes the party frames. Drain it by frame rather than by
	// byte count so a change to the burst's contents cannot silently shift the
	// party frames out from under the reads below. The drain consumes through
	// ZC_PARTY_CONFIG, so the roster is the very next frame.
	drainUntilPartyConfig(t, conn)

	cmd, body := readFrame(t, conn)
	if cmd != ropacket.HeaderZCGROUPLIST {
		t.Fatalf("post-burst cmd = 0x%04x, want ZC_GROUP_LIST", cmd)
	}
	if name := cstr(body[2:26]); name != "Athena" {
		t.Errorf("restored party name = %q, want %q", name, "Athena")
	}
	if len(body)-26 != 54 {
		t.Fatalf("restored roster = %d member bytes, want one 54-byte entry", len(body)-26)
	}
}

// drainUntilPartyConfig consumes the LoadEndAck init burst — inventory start,
// both item lists, inventory end, and the skill list — up to and including
// ZC_PARTY_CONFIG. Each frame's length is read from its own header, so the
// drain stays correct if the burst gains or loses frames.
func drainUntilPartyConfig(t *testing.T, r io.Reader) {
	t.Helper()
	for i := 0; i < 16; i++ {
		var cmdBuf [2]byte
		if _, err := io.ReadFull(r, cmdBuf[:]); err != nil {
			t.Fatalf("drain burst: read cmd: %v", err)
		}
		cmd := binary.LittleEndian.Uint16(cmdBuf[:])
		switch cmd {
		case ropacket.HeaderZCPARTYCONFIG:
			// Consume its single payload byte and stop.
			if _, err := io.ReadFull(r, make([]byte, 1)); err != nil {
				t.Fatalf("drain burst: read ZC_PARTY_CONFIG body: %v", err)
			}
			return
		case ropacket.HeaderZCINVENTORYSTART:
			// 6 bytes total: cmd(2) + declared length(2) + invType(1) + name[0].
			if _, err := io.ReadFull(r, make([]byte, 4)); err != nil {
				t.Fatalf("drain burst: read ZC_INVENTORY_START body: %v", err)
			}
		case ropacket.HeaderZCINVENTORYEND:
			// 4 bytes total: cmd(2) + invType(1) + flag(1); no length field.
			if _, err := io.ReadFull(r, make([]byte, 2)); err != nil {
				t.Fatalf("drain burst: read ZC_INVENTORY_END body: %v", err)
			}
		default:
			// Variable-length frames (item lists, skill list) declare their
			// total length at offset 2.
			var lenBuf [2]byte
			if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
				t.Fatalf("drain burst: read len for 0x%04x: %v", cmd, err)
			}
			total := int(binary.LittleEndian.Uint16(lenBuf[:]))
			if total < 4 {
				t.Fatalf("drain burst: frame 0x%04x declares bad length %d", cmd, total)
			}
			if _, err := io.ReadFull(r, make([]byte, total-4)); err != nil {
				t.Fatalf("drain burst: read body for 0x%04x: %v", cmd, err)
			}
		}
	}
	t.Fatal("drain burst: ZC_PARTY_CONFIG never arrived")
}

// cstr reads a NUL-terminated fixed-width field.
func cstr(b []byte) string {
	if i := indexByte(b, 0); i >= 0 {
		return string(b[:i])
	}
	return string(b)
}

func indexByte(b []byte, c byte) int {
	for i, v := range b {
		if v == c {
			return i
		}
	}
	return -1
}
