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
	friendapp "github.com/bouroo/goAthena/internal/modules/social/friend/app"
	friendinfra "github.com/bouroo/goAthena/internal/modules/social/friend/infra"
	ropacket "github.com/bouroo/goAthena/pkg/ro/packet"
)

// The M11 friend L3 proofs. The unit suite covers the pair state machine and
// the repository round-trips; what only an end-to-end run can show is the wire
// conversation: a CZ_ADD_FRIENDS by name becomes a ZC_REQ_ADD_FRIENDS dialog
// on the target, an accept becomes ZC_ADD_FRIENDS(result 0) on BOTH sides
// (friend_auto_add), a delete becomes ZC_DELETE_FRIENDS on both sides with the
// id-swapped payloads, and a char entering the map while already befriended
// receives its ZC_FRIENDS_LIST during the LoadEndAck burst
// (clif_friendslist_send).

// buildFriendTestEnv is the two-player harness: two PCs on the same map and a
// FriendService over an in-memory friend repo with both chars registered.
func buildFriendTestEnv(t *testing.T, port int) (net.Conn, net.Conn, *friendinfra.MemoryFriendRepository) {
	t.Helper()
	sessions := charinfra.NewMemorySessionStore()
	_ = sessions.PutSession(t.Context(), chardomain.Session{
		AccountID: 2000001, LoginID1: 0x11111111, LoginID2: 0x22222222, Sex: 1,
	})
	_ = sessions.PutSession(t.Context(), chardomain.Session{
		AccountID: 2000002, LoginID1: 0x33333333, LoginID2: 0x44444444, Sex: 1,
	})
	ms, _ := buildTradeMapDeps(t, sessions) // two seeded PCs: Hero 150001 / Partner 150002
	repo := friendinfra.NewMemoryFriendRepository()
	repo.SetChar(150001, friendinfra.CharInfo{AccountID: 2000001, Name: "Hero", Online: true})
	repo.SetChar(150002, friendinfra.CharInfo{AccountID: 2000002, Name: "Partner", Online: true})
	ms.SetFriend(friendapp.NewFriendService(repo))

	conn1, conn2 := startAndDialTwo(t, ms, port)
	sendCZEnter(t, conn1, 2000001, 150001, 0x11111111)
	awaitAcceptEnter(t, conn1)
	sendCZEnter(t, conn2, 2000002, 150002, 0x33333333)
	awaitAcceptEnter(t, conn2)
	// Drain the mutual spawn frames (each enter broadcasts the newcomer).
	readTradeFrame(t, conn1, ropacket.HeaderZCSPAWNUNIT, 107)
	readTradeFrame(t, conn2, ropacket.HeaderZCSPAWNUNIT, 107)
	conn1.SetDeadline(time.Now().Add(3 * time.Second))
	conn2.SetDeadline(time.Now().Add(3 * time.Second))
	return conn1, conn2, repo
}

// readFriendFrame reads the next FRIEND frame, draining and skipping any
// init-burst frames in between (variable-length, declared at offset 2).
func readFriendFrame(t *testing.T, c net.Conn) (uint16, []byte) {
	t.Helper()
	for i := 0; i < 24; i++ {
		cmd, body, ok := readFrameSized(t, c)
		if !ok {
			continue // drained an init-burst frame; keep scanning.
		}
		return cmd, body
	}
	t.Fatal("friend frame never arrived (drained 24 frames)")
	return 0, nil
}

// readFrameSized reads one frame sizing it from its own header. ok=false means
// the frame was an unrecognized (init-burst) frame and was fully drained.
func readFrameSized(t *testing.T, c net.Conn) (uint16, []byte, bool) {
	t.Helper()
	var cmdBuf [2]byte
	if _, err := io.ReadFull(c, cmdBuf[:]); err != nil {
		t.Fatalf("read frame cmd: %v", err)
	}
	cmd := binary.LittleEndian.Uint16(cmdBuf[:])
	total := 0
	switch cmd {
	case ropacket.HeaderZCFRIENDSLIST:
		var lenBuf [2]byte
		if _, err := io.ReadFull(c, lenBuf[:]); err != nil {
			t.Fatalf("read ZC_FRIENDS_LIST len: %v", err)
		}
		total = int(binary.LittleEndian.Uint16(lenBuf[:]))
		if total < 4 {
			t.Fatalf("ZC_FRIENDS_LIST declares bad length %d", total)
		}
		body := make([]byte, total-4)
		if _, err := io.ReadFull(c, body); err != nil {
			t.Fatalf("read ZC_FRIENDS_LIST body: %v", err)
		}
		return cmd, append(append([]byte{}, lenBuf[:]...), body...), true
	case ropacket.HeaderZCFRIENDSSTATE:
		total = ropacket.FriendsStateResponse{}.Size()
	case ropacket.HeaderZCREQADDFRIENDS:
		total = ropacket.FriendsReqAddResponse{}.Size()
	case ropacket.HeaderZCADDFRIENDS:
		total = ropacket.FriendsAddResponse{}.Size()
	case ropacket.HeaderZCDELETEFRIENDS:
		total = ropacket.FriendsDeleteResponse{}.Size()
	default:
		// Init-burst fixed frames: ZC_INVENTORY_START is 6 bytes (cmd + len
		// field + invType), ZC_INVENTORY_END is 4 bytes with NO length field
		// (mirrors drainUntilPartyConfig).
		if cmd == ropacket.HeaderZCINVENTORYSTART {
			if _, err := io.ReadFull(c, make([]byte, 4)); err != nil {
				t.Fatalf("drain ZC_INVENTORY_START: %v", err)
			}
			return 0, nil, false
		}
		if cmd == ropacket.HeaderZCINVENTORYEND {
			if _, err := io.ReadFull(c, make([]byte, 2)); err != nil {
				t.Fatalf("drain ZC_INVENTORY_END: %v", err)
			}
			return 0, nil, false
		}
		if cmd == ropacket.HeaderZCPARTYCONFIG {
			if _, err := io.ReadFull(c, make([]byte, 1)); err != nil {
				t.Fatalf("drain ZC_PARTY_CONFIG: %v", err)
			}
			return 0, nil, false
		}
		// Everything else declares its total length at offset 2; drain it.
		var lenBuf [2]byte
		if _, err := io.ReadFull(c, lenBuf[:]); err != nil {
			t.Fatalf("read len for 0x%04x: %v", cmd, err)
		}
		total = int(binary.LittleEndian.Uint16(lenBuf[:]))
		if total < 4 {
			t.Fatalf("frame 0x%04x declares bad length %d", cmd, total)
		}
		if _, err := io.ReadFull(c, make([]byte, total-4)); err != nil {
			t.Fatalf("read body for 0x%04x: %v", cmd, err)
		}
		return 0, nil, false
	}
	body := make([]byte, total-2)
	if _, err := io.ReadFull(c, body); err != nil {
		t.Fatalf("read body for 0x%04x: %v", cmd, err)
	}
	return cmd, body, true
}

// TestFriend_RequestAcceptRemove drives the whole friendship lifecycle over two
// real connections.
func TestFriend_RequestAcceptRemove(t *testing.T) {
	port := freePort(t)
	conn1, conn2, repo := buildFriendTestEnv(t, port)
	defer conn1.Close()
	defer conn2.Close()

	// 1. Hero adds Partner by name: Partner gets the dialog.
	addFrame := make([]byte, 26)
	binary.LittleEndian.PutUint16(addFrame[0:], ropacket.HeaderCZFRIENDSADD)
	copy(addFrame[2:26], "Partner")
	sendRaw(t, conn1, addFrame)
	cmd, body := readFriendFrame(t, conn2)
	if cmd != ropacket.HeaderZCREQADDFRIENDS {
		t.Fatalf("target reply cmd = 0x%04x, want ZC_REQ_ADD_FRIENDS", cmd)
	}
	if aid := binary.LittleEndian.Uint32(body[0:4]); aid != 2000001 {
		t.Errorf("dialog AID = %d, want 2000001", aid)
	}
	if cid := binary.LittleEndian.Uint32(body[4:8]); cid != 150001 {
		t.Errorf("dialog CID = %d, want 150001", cid)
	}

	// 2. Partner accepts: BOTH sides get ZC_ADD_FRIENDS result 0 naming the other.
	reply := make([]byte, 14)
	binary.LittleEndian.PutUint16(reply[0:], ropacket.HeaderCZFRIENDSREPLY)
	binary.LittleEndian.PutUint32(reply[2:], 2000001)
	binary.LittleEndian.PutUint32(reply[6:], 150001)
	binary.LittleEndian.PutUint32(reply[10:], uint32(ropacket.FriendReplyAccept))
	sendRaw(t, conn2, reply)

	cmd, body = readFriendFrame(t, conn1)
	if cmd != ropacket.HeaderZCADDFRIENDS {
		t.Fatalf("requester reply cmd = 0x%04x, want ZC_ADD_FRIENDS", cmd)
	}
	if result := int16(binary.LittleEndian.Uint16(body[0:2])); result != ropacket.FriendAddOK {
		t.Errorf("requester result = %d, want 0 (OK)", result)
	}
	if cid := binary.LittleEndian.Uint32(body[6:10]); cid != 150002 {
		t.Errorf("requester names CID = %d, want 150002", cid)
	}

	cmd, body = readFriendFrame(t, conn2)
	if cmd != ropacket.HeaderZCADDFRIENDS {
		t.Fatalf("acceptor echo cmd = 0x%04x, want ZC_ADD_FRIENDS", cmd)
	}
	if result := int16(binary.LittleEndian.Uint16(body[0:2])); result != ropacket.FriendAddOK {
		t.Errorf("acceptor result = %d, want 0 (OK)", result)
	}
	if cid := binary.LittleEndian.Uint32(body[6:10]); cid != 150001 {
		t.Errorf("acceptor names CID = %d, want 150001 (auto_add reverse)", cid)
	}
	if friends, err := repo.List(t.Context(), 150001); err != nil || len(friends) != 1 {
		t.Fatalf("repo after accept: %d friends (err %v), want 1", len(friends), err)
	}

	// 3. Hero removes Partner: Hero's client hears Partner's ids; Partner's
	// client hears Hero's ids (clif.cpp:15586-15613 id swap).
	del := make([]byte, 10)
	binary.LittleEndian.PutUint16(del[0:], ropacket.HeaderCZFRIENDSDELETE)
	binary.LittleEndian.PutUint32(del[2:], 2000002)
	binary.LittleEndian.PutUint32(del[6:], 150002)
	sendRaw(t, conn1, del)

	cmd, body = readFriendFrame(t, conn1)
	if cmd != ropacket.HeaderZCDELETEFRIENDS {
		t.Fatalf("remover reply cmd = 0x%04x, want ZC_DELETE_FRIENDS", cmd)
	}
	if cid := binary.LittleEndian.Uint32(body[4:8]); cid != 150002 {
		t.Errorf("remover frame CID = %d, want 150002 (the removed friend)", cid)
	}
	cmd, body = readFriendFrame(t, conn2)
	if cmd != ropacket.HeaderZCDELETEFRIENDS {
		t.Fatalf("removed-friend frame cmd = 0x%04x, want ZC_DELETE_FRIENDS", cmd)
	}
	if cid := binary.LittleEndian.Uint32(body[4:8]); cid != 150001 {
		t.Errorf("removed-friend frame CID = %d, want 150001 (the remover)", cid)
	}
	if friends, err := repo.List(t.Context(), 150002); err != nil || len(friends) != 0 {
		t.Fatalf("repo after remove: %d friends (err %v), want 0", len(friends), err)
	}
}

// TestFriend_LoadEndAckRestoresList proves the map-enter restore: a char who
// is already befriended when it enters receives ZC_FRIENDS_LIST during the
// LoadEndAck burst, followed by one online ZC_FRIENDS_STATE per connected
// friend (clif_friendslist_send).
func TestFriend_LoadEndAckRestoresList(t *testing.T) {
	port := freePort(t)
	conn1, _, repo := buildFriendTestEnv(t, port)
	defer conn1.Close()

	// Pre-friend the pair (as if befriended on a previous session).
	if err := repo.Add(t.Context(), 150001, 150002); err != nil {
		t.Fatalf("seed friendship: %v", err)
	}

	// Reload: disconnect semantics aside, a fresh CZ_ENTER + LoadEndAck is the
	// map-enter path the burst rides on.
	sendLoadEndAck(t, conn1)
	// Drain the init burst frame-by-frame until the friend list lands. The
	// burst is: inventory start/lists/end, skill list, ZC_PARTY_CONFIG, then
	// ZC_FRIENDS_LIST + ZC_FRIENDS_STATE per online friend.
	for i := 0; i < 24; i++ {
		cmd, body := readFriendFrame(t, conn1)
		switch cmd {
		case ropacket.HeaderZCFRIENDSLIST:
			// body = [len(2)] + N*8. One friend expected.
			if got := (len(body) - 2) / 8; got != 1 {
				t.Fatalf("friend list entries = %d, want 1", got)
			}
			if aid := binary.LittleEndian.Uint32(body[2:6]); aid != 2000002 {
				t.Errorf("friend AID = %d, want 2000002", aid)
			}
			if cid := binary.LittleEndian.Uint32(body[6:10]); cid != 150002 {
				t.Errorf("friend CID = %d, want 150002", cid)
			}
			// The online toggle for that friend follows immediately.
			cmd2, body2 := readFriendFrame(t, conn1)
			if cmd2 != ropacket.HeaderZCFRIENDSSTATE {
				t.Fatalf("after list cmd = 0x%04x, want ZC_FRIENDS_STATE", cmd2)
			}
			if offline := body2[8]; offline != 0 {
				t.Errorf("state offline byte = %d, want 0 (online)", offline)
			}
			if cid := binary.LittleEndian.Uint32(body2[4:8]); cid != 150002 {
				t.Errorf("state CID = %d, want 150002", cid)
			}
			return
		default:
			continue // init-burst frames (inventory, skills, party config).
		}
	}
	t.Fatal("ZC_FRIENDS_LIST never arrived in the LoadEndAck burst")
}
