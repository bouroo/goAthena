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
	guildapp "github.com/bouroo/goAthena/internal/modules/social/guild/app"
	guildinfra "github.com/bouroo/goAthena/internal/modules/social/guild/infra"
	ropacket "github.com/bouroo/goAthena/pkg/ro/packet"
)

// The M11 guild L3 proofs. The unit suites cover the service state machine and
// the codecs; only an end-to-end run proves the wire behaviour: creating a
// guild answers ZC_RESULT_MAKE_GUILD then delivers the belong/info/roster
// burst, and a char entering while guilded receives that burst during the
// LoadEndAck tail (rAthena clif_parse_LoadEndAck →
// clif_guild_send_basicinfo/clif_guild_memberlist).

// guildTestEnv is buildTestMapDeps' env plus the guild repository the test seeds.
type guildTestEnv struct {
	mapTestEnv
	guildRepo *guildinfra.MemoryGuildRepository
}

// buildGuildTestEnv mirrors buildTestMapDeps but wires a GuildService over an
// in-memory guild repo with the entering char (150001 / account 2000001)
// registered, so the gateway's guild verbs have a live backend.
func buildGuildTestEnv(t *testing.T, sessions *charinfra.MemorySessionStore, port int) (net.Conn, guildTestEnv) {
	t.Helper()
	ms, env := buildTestMapDeps(t, sessions)
	repo := guildinfra.NewMemoryGuildRepository()
	repo.SetChar(150001, guildinfra.CharInfo{
		AccountID: 2000001, Name: "Hero", Map: "new_1-1", Class: 0, BaseLevel: 1, Online: true,
	})
	ms.SetGuild(guildapp.NewGuildService(repo))

	conn := startAndDial(t, ms, port)
	sendCZEnter(t, conn, 2000001, 150001, 0x11111111)
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	awaitAcceptEnter(t, conn)
	return conn, guildTestEnv{mapTestEnv: env, guildRepo: repo}
}

// readGuildFrame reads one guild reply's cmd and payload, honouring each
// frame's length discipline: fixed-size frames declare nothing; the member
// list carries its total at offset 2.
func readGuildFrame(t *testing.T, r io.Reader) (uint16, []byte) {
	t.Helper()
	var cmd [2]byte
	if _, err := io.ReadFull(r, cmd[:]); err != nil {
		t.Fatalf("read frame cmd: %v", err)
	}
	c := binary.LittleEndian.Uint16(cmd[:])
	var size int
	switch c {
	case ropacket.HeaderZCREQUESTMAKEGILD:
		size = ropacket.ResultMakeGuildResponse{}.Size()
	case ropacket.HeaderZCUPDATEGDID:
		size = ropacket.UpdateGDIDResponse{}.Size()
	case ropacket.HeaderZCGUILDINFO:
		size = ropacket.GuildInfoResponse{}.Size()
	case ropacket.HeaderZCMEMBERMGRINFO:
		var lenBuf [2]byte
		if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
			t.Fatalf("read ZC_MEMBERMGR_INFO len: %v", err)
		}
		body := make([]byte, int(binary.LittleEndian.Uint16(lenBuf[:]))-4)
		if _, err := io.ReadFull(r, body); err != nil {
			t.Fatalf("read ZC_MEMBERMGR_INFO body: %v", err)
		}
		return c, append(lenBuf[:], body...)
	default:
		t.Fatalf("unexpected frame cmd 0x%04x", c)
	}
	body := make([]byte, size-2)
	if _, err := io.ReadFull(r, body); err != nil {
		t.Fatalf("read body for 0x%04x: %v", c, err)
	}
	return c, body
}

// drainUntilGuildBurst consumes the LoadEndAck init burst up to and including
// ZC_UPDATE_GDID), the first guild frame in the tail. Frame lengths
// are read from each frame's own header so burst changes upstream cannot
// shift the reads.
func drainUntilGuildBurst(t *testing.T, r io.Reader) []byte {
	t.Helper()
	for i := 0; i < 16; i++ {
		var cmdBuf [2]byte
		if _, err := io.ReadFull(r, cmdBuf[:]); err != nil {
			t.Fatalf("drain burst: read cmd: %v", err)
		}
		cmd := binary.LittleEndian.Uint16(cmdBuf[:])
		switch cmd {
		case ropacket.HeaderZCUPDATEGDID:
			body := make([]byte, ropacket.UpdateGDIDResponse{}.Size()-2)
			if _, err := io.ReadFull(r, body); err != nil {
				t.Fatalf("drain burst: read ZC_UPDATE_GDID body: %v", err)
			}
			return body
		case ropacket.HeaderZCINVENTORYSTART:
			if _, err := io.ReadFull(r, make([]byte, 4)); err != nil {
				t.Fatalf("drain burst: read ZC_INVENTORY_START body: %v", err)
			}
		case ropacket.HeaderZCINVENTORYEND:
			if _, err := io.ReadFull(r, make([]byte, 2)); err != nil {
				t.Fatalf("drain burst: read ZC_INVENTORY_END body: %v", err)
			}
		case ropacket.HeaderZCPARTYCONFIG:
			if _, err := io.ReadFull(r, make([]byte, 1)); err != nil {
				t.Fatalf("drain burst: read ZC_PARTY_CONFIG body: %v", err)
			}
		case ropacket.HeaderZCFRIENDSLIST, ropacket.HeaderZCGROUPLIST:
			var lenBuf [2]byte
			if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
				t.Fatalf("drain burst: read len for 0x%04x: %v", cmd, err)
			}
			total := int(binary.LittleEndian.Uint16(lenBuf[:]))
			if _, err := io.ReadFull(r, make([]byte, total-4)); err != nil {
				t.Fatalf("drain burst: read body for 0x%04x: %v", cmd, err)
			}
		default:
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
	t.Fatal("drain burst: ZC_UPDATE_GDID never arrived")
	return nil
}

// TestGuild_CreateEmitsAckThenBurst drives CZ_REQ_MAKE_GUILD and asserts the
// create reply plus the belong/info/roster burst that opens the guild window.
func TestGuild_CreateEmitsAckThenBurst(t *testing.T) {
	port := freePort(t)
	sessions := charinfra.NewMemorySessionStore()
	_ = sessions.PutSession(t.Context(), chardomain.Session{
		AccountID: 2000001, LoginID1: 0x11111111, LoginID2: 0x22222222, Sex: 1,
	})
	conn, _ := buildGuildTestEnv(t, sessions, port)
	defer conn.Close()

	frame := make([]byte, ropacket.SizeCZCreateGuild)
	binary.LittleEndian.PutUint16(frame[0:], ropacket.HeaderCZCREATEGUILD)
	binary.LittleEndian.PutUint32(frame[2:], 150001)
	copy(frame[6:30], "Athena")
	if _, err := conn.Write(frame); err != nil {
		t.Fatalf("send CZ_REQ_MAKE_GUILD: %v", err)
	}

	cmd, body := readGuildFrame(t, conn)
	if cmd != ropacket.HeaderZCREQUESTMAKEGILD {
		t.Fatalf("first reply cmd = 0x%04x, want ZC_RESULT_MAKE_GUILD", cmd)
	}
	if body[0] != ropacket.GuildCreateOK {
		t.Fatalf("create result = %d, want 0 (OK)", body[0])
	}

	cmd, body = readGuildFrame(t, conn)
	if cmd != ropacket.HeaderZCUPDATEGDID {
		t.Fatalf("second reply cmd = 0x%04x, want ZC_UPDATE_GDID", cmd)
	}
	// body starts after the cmd: guildId(4) at 0, isMaster(1) at 12,
	// guildName[24] at 17, masterGID(4) at 41.
	if gid := binary.LittleEndian.Uint32(body[0:4]); gid == 0 {
		t.Errorf("guild id = 0, want nonzero")
	}
	if body[12] != 1 {
		t.Errorf("isMaster = %d, want 1 (creator is master)", body[12])
	}
	if name := cstr(body[17:41]); name != "Athena" {
		t.Errorf("guild name = %q, want %q", name, "Athena")
	}
	if m := binary.LittleEndian.Uint32(body[41:45]); m != 150001 {
		t.Errorf("masterGID = %d, want 150001", m)
	}

	cmd, body = readGuildFrame(t, conn)
	if cmd != ropacket.HeaderZCGUILDINFO {
		t.Fatalf("third reply cmd = 0x%04x, want ZC_GUILD_INFO", cmd)
	}
	if name := cstr(body[44:68]); name != "Athena" {
		t.Errorf("guild info name = %q, want %q", name, "Athena")
	}
	if n := binary.LittleEndian.Uint32(body[8:12]); n != 1 {
		t.Errorf("guild info userNum = %d, want 1", n)
	}

	cmd, body = readGuildFrame(t, conn)
	if cmd != ropacket.HeaderZCMEMBERMGRINFO {
		t.Fatalf("fourth reply cmd = 0x%04x, want ZC_MEMBERMGR_INFO", cmd)
	}
	// body = [len(2)] + N*58; one member: AID(4) GID(4) ... name at 36.
	if members := (len(body) - 2) / 58; members != 1 {
		t.Fatalf("member list entries = %d, want 1", members)
	}
	member := body[2:]
	if aid := binary.LittleEndian.Uint32(member[0:4]); aid != 2000001 {
		t.Errorf("member AID = %d, want 2000001", aid)
	}
	if gid := binary.LittleEndian.Uint32(member[4:8]); gid != 150001 {
		t.Errorf("member GID = %d, want 150001", gid)
	}
	if name := cstr(member[34:58]); name != "Hero" {
		t.Errorf("member name = %q, want %q", name, "Hero")
	}
}

// TestGuild_LoadEndAckRestoresGuild proves the map-enter restore: a char
// already guilded when it enters receives belong-info, guild-info, and roster
// during the LoadEndAck tail. Without this the client's guild window is empty
// until the next membership change.
func TestGuild_LoadEndAckRestoresGuild(t *testing.T) {
	port := freePort(t)
	sessions := charinfra.NewMemorySessionStore()
	_ = sessions.PutSession(t.Context(), chardomain.Session{
		AccountID: 2000001, LoginID1: 0x11111111, LoginID2: 0x22222222, Sex: 1,
	})
	conn, env := buildGuildTestEnv(t, sessions, port)
	defer conn.Close()

	// Pre-guild the char (as if it joined on a previous map).
	g, err := env.guildRepo.Create(t.Context(), 2000001, 150001, "Athena")
	if err != nil {
		t.Fatalf("seed guild: %v", err)
	}

	sendLoadEndAck(t, conn)
	body := drainUntilGuildBurst(t, conn)
	if gid := binary.LittleEndian.Uint32(body[0:4]); gid != uint32(g.ID) { //nolint:gosec // G115: DB sequence ids.
		t.Errorf("restored guild id = %d, want %d", gid, g.ID)
	}
	if body[12] != 1 {
		t.Errorf("restored isMaster = %d, want 1", body[12])
	}
	if name := cstr(body[17:41]); name != "Athena" {
		t.Errorf("restored guild name = %q, want %q", name, "Athena")
	}

	cmd, mbody := readGuildFrame(t, conn)
	if cmd != ropacket.HeaderZCGUILDINFO {
		t.Fatalf("post-belong cmd = 0x%04x, want ZC_GUILD_INFO", cmd)
	}
	if name := cstr(mbody[44:68]); name != "Athena" {
		t.Errorf("restored info name = %q, want %q", name, "Athena")
	}

	cmd, mbody = readGuildFrame(t, conn)
	if cmd != ropacket.HeaderZCMEMBERMGRINFO {
		t.Fatalf("post-info cmd = 0x%04x, want ZC_MEMBERMGR_INFO", cmd)
	}
	if members := (len(mbody) - 2) / 58; members != 1 {
		t.Fatalf("restored roster entries = %d, want 1", members)
	}
}
