//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/bouroo/goAthena/internal/features/identity/domain"
	netcodec "github.com/bouroo/goAthena/internal/infrastructure/net"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// On-wire byte lengths for the small fixed map-server responses. The
// packet package exposes these as unexported constants; the e2e tests
// pin them as named constants to avoid importing internal helpers
// just to assert "frame is the right size".
const (
	// zcAcceptEnterSize is the fixed length of ZC_ACCEPT_ENTER
	// (rathena/src/map/packets.hpp:562-571).
	zcAcceptEnterSize = 13
	// zcSpawnUnitSize is the fixed length of ZC_SPAWN_UNIT
	// (rathena/src/map/packets.hpp, PACKETVER >= 20150513 branch).
	zcSpawnUnitSize = 107
	// zcNotifyPlayerMoveSize is the fixed length of ZC_NOTIFY_PLAYERMOVE
	// (rathena/src/map/packets.hpp).
	zcNotifyPlayerMoveSize = 12
	// zcMapPropertyR2Size is the fixed length of ZC_MAPPROPERTY_R2
	// (rathena/src/map/clif.cpp:6869-6902).
	zcMapPropertyR2Size = 8
	// zcNotifyTimeSize is the fixed length of ZC_NOTIFY_TIME
	// (rathena/src/map/clif.cpp:11186-11193).
	zcNotifyTimeSize = 6
	// zcShortcutKeyListSize is the fixed length of ZC_SHORTCUT_KEY_LIST
	// (rathena/src/map/packets_struct.hpp:1613-1619 — the PACKETVER
	// < 20090603 branch that gives opcode 0x02b9 with
	// MAX_HOTKEYS_PACKET=27). Total = 2 (opcode) + 27 * 7
	// (hotkey_data) = 191 bytes.
	zcShortcutKeyListSize = 191
	// zcActionResponseSize is the fixed length of ZC_ACTION_RESPONSE
	// (M11): [2:cmd][4:GID][1:action][4:targetGID] = 11 bytes.
	zcActionResponseSize = 11
	// zcChangeDirSize is the fixed length of ZC_CHANGE_DIRECTION
	// (M12): [2:cmd][4:srcId][2:headDir uint16][1:dir uint8] = 9 bytes.
	zcChangeDirSize = 9
	// zcEmotionSize is the fixed length of ZC_EMOTION (M12):
	// [2:cmd][4:GID][1:type] = 7 bytes.
	zcEmotionSize = 7
	// zcAckReqNameSize is the fixed length of ZC_ACK_REQNAME (M13):
	// [2:cmd][4:GID int32][24:name char[24]] = 30 bytes.
	zcAckReqNameSize = 30
	// zcSetUnitIdleSize is the fixed length of ZC_SET_UNIT_IDLE (M14):
	// same struct layout as ZC_SPAWN_UNIT (0x09fe) but opcode 0x09ff.
	// [2:cmd][2:packetLength][103:payload] = 107 bytes.
	zcSetUnitIdleSize = 107
	// zcWaitDialog2Size is the fixed length of ZC_WAIT_DIALOG2 (M15):
	// [2:cmd=0x0973][4:NpcID][1:type] = 7 bytes.
	zcWaitDialog2Size = 7
	// zcCloseDialogSize is the fixed length of ZC_CLOSE_DIALOG (M15):
	// [2:cmd=0x00b6][4:NpcID] = 6 bytes.
	zcCloseDialogSize = 6
	// M16: NPC shop interaction.
	//
	// zcSelectDealtypeSize is the fixed length of ZC_SELECT_DEALTYPE:
	// [2:cmd=0x00c4][4:NpcID] = 6 bytes.
	zcSelectDealtypeSize = 6
	// shopBuyItemWireSize is the per-item size in
	// ZC_PC_PURCHASE_ITEMLIST (rathena/src/map/packets_struct.hpp
	// ITEM_INFO, PACKETVER >= 20210203): uint32 itemId + uint32 price
	// + uint32 discountPrice + uint8 itemType + uint16 viewSprite +
	// uint32 location = 4+4+4+1+2+4 = 19 bytes.
	shopBuyItemWireSize = 19
	// zcPCPurchaseResultSize is the fixed length of
	// ZC_PC_PURCHASE_RESULT: [2:cmd=0x00ca][1:result] = 3 bytes.
	zcPCPurchaseResultSize = 3
)

// acceptEnterHeaderSize is the fixed prefix length of HC_ACCEPT_ENTER
// preceding the trailing CHARACTER_INFO[] flexible array:
// 2 (cmd) + 2 (packetLength) + 1 (total) + 1 (premiumStart) +
// 1 (premiumEnd) + 20 (extension) = 27 bytes.
const acceptEnterHeaderSize = 27

// acceptEnterCharNameOffset is the byte offset inside one
// CHARACTER_INFO entry where the name[24] slot begins
// (rathena/src/common/packets.hpp:31-105 — name at offset 108).
const acceptEnterCharNameOffset = 108

// feedAndNext drains bytes available from conn under the supplied
// deadline, feeds them into dec, and returns the next fully framed
// packet (cmd, frame). It returns an error if the deadline elapses or
// the decoder reports anything other than "need more data".
//
// The read uses SetReadDeadline once before each chunk; TCP coalescing
// / splitting means a single Next() may require multiple Feed cycles —
// this helper loops until the decoder produces a frame or the deadline
// fires. The deadline is reused for every chunk so the overall
// wait-time is bounded by `deadline` even when bytes trickle in.
func feedAndNext(t *testing.T, conn net.Conn, dec *netcodec.Decoder, deadline time.Duration) (uint16, []byte, error) {
	t.Helper()

	if err := conn.SetReadDeadline(time.Now().Add(deadline)); err != nil {
		return 0, nil, fmt.Errorf("set read deadline: %w", err)
	}

	chunk := make([]byte, 4096)
	for {
		cmd, frame, err := dec.Next()
		if err == nil {
			return cmd, frame, nil
		}
		if err != netcodec.ErrIncomplete {
			return 0, nil, fmt.Errorf("decode: %w", err)
		}
		n, readErr := conn.Read(chunk)
		if n > 0 {
			dec.Feed(chunk[:n])
			continue
		}
		if readErr == nil {
			return 0, nil, io.EOF
		}
		if netErr, ok := readErr.(net.Error); ok && netErr.Timeout() {
			return 0, nil, fmt.Errorf("decode timeout after %s", deadline)
		}
		return 0, nil, fmt.Errorf("read: %w", readErr)
	}
}

// dialGatewayAndDecode returns a fresh TCP connection to the gateway
// (skipping the test if unreachable) plus a login-mode decoder backed
// by the merged login+char+map packet DB.
func dialGatewayAndDecode(t *testing.T, addr string) (net.Conn, *netcodec.Decoder) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Skipf("e2e: gateway TCP unreachable at %s: %v", addr, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	db := packet.NewLoginServerDB()
	db.Merge(packet.NewCharServerDB())
	db.Merge(packet.NewMapServerDB())
	return conn, netcodec.NewLoginDecoder(db)
}

// stageCALogin sends CA_LOGIN and returns (AID, loginID1, sex).
func stageCALogin(t *testing.T, conn net.Conn, dec *netcodec.Decoder, deadline time.Duration, userID, password string) (uint32, uint32, uint8) {
	t.Helper()
	if err := (packet.CALoginRequest{
		Version:    20250604,
		Username:   userID,
		Password:   password,
		ClientType: 0,
	}).Encode(conn); err != nil {
		t.Fatalf("encode CA_LOGIN: %v", err)
	}

	cmd, frame, err := feedAndNext(t, conn, dec, deadline)
	if err != nil {
		t.Fatalf("read AC_ACCEPT_LOGIN: %v", err)
	}
	if cmd != packet.HeaderACACCEPTLOGIN {
		t.Fatalf("CA_LOGIN response cmd = 0x%04x, want 0x%04x (AC_ACCEPT_LOGIN); frame=% x",
			cmd, packet.HeaderACACCEPTLOGIN, frame)
	}
	if len(frame) < 47 {
		t.Fatalf("AC_ACCEPT_LOGIN frame too short: got %d bytes, want at least 47", len(frame))
	}
	// AC_ACCEPT_LOGIN layout (modern, PACKETVER >= 20170315):
	//   [0:2]   cmd 0x0ac4
	//   [2:4]   packetLength (uint16 LE)
	//   [4:8]   loginID1 (uint32 LE)
	//   [8:12]  AID       (uint32 LE)
	//   ...
	//   [46]    sex
	loginID1 := binary.LittleEndian.Uint32(frame[4:8])
	aid := binary.LittleEndian.Uint32(frame[8:12])
	sexByte := frame[46]
	t.Logf("CA_LOGIN ok: AID=%d loginID1=0x%x sex=%d", aid, loginID1, sexByte)
	return aid, loginID1, sexByte
}

// stageCHEnter sends CH_ENTER, drains the 4-byte headerless AID echo,
// then reads HC_ACCEPT_ENTER and asserts slot-0 carries the expected
// character. Returns slot-0's GID.
func stageCHEnter(t *testing.T, conn net.Conn, dec *netcodec.Decoder, deadline time.Duration, aid uint32, loginID1 uint32, sexByte uint8, wantCharID uint32, wantCharName string) uint32 {
	t.Helper()
	if err := (packet.CHEnterRequest{
		AccountID: aid,
		LoginID1:  loginID1,
		LoginID2:  0,
		Sex:       sexByte,
	}).Encode(conn); err != nil {
		t.Fatalf("encode CH_ENTER: %v", err)
	}

	echo := make([]byte, 4)
	if _, err := io.ReadFull(conn, echo); err != nil {
		t.Fatalf("read CH_ENTER AID echo: %v", err)
	}
	if got := binary.LittleEndian.Uint32(echo); got != aid {
		t.Fatalf("CH_ENTER AID echo = %d, want %d (bytes=% x)", got, aid, echo)
	}

	cmd, frame, err := feedAndNext(t, conn, dec, deadline)
	if err != nil {
		t.Fatalf("read HC_ACCEPT_ENTER: %v", err)
	}
	if cmd != packet.HeaderHCACCEPTENTER {
		t.Fatalf("CH_ENTER response cmd = 0x%04x, want 0x%04x (HC_ACCEPT_ENTER); frame=% x",
			cmd, packet.HeaderHCACCEPTENTER, frame)
	}
	if len(frame) < acceptEnterHeaderSize+packet.CharacterInfoSize {
		t.Fatalf("HC_ACCEPT_ENTER too short: %d bytes (want ≥ %d); frame=% x",
			len(frame), acceptEnterHeaderSize+packet.CharacterInfoSize, frame)
	}
	// Slot-0 GID lives at bytes [27:31] of the first CHARACTER_INFO.
	slot0GID := binary.LittleEndian.Uint32(frame[acceptEnterHeaderSize : acceptEnterHeaderSize+4])
	nameBytes := frame[acceptEnterHeaderSize+acceptEnterCharNameOffset : acceptEnterHeaderSize+acceptEnterCharNameOffset+24]
	gotName := cstrBytes(nameBytes)
	if gotName != wantCharName {
		t.Fatalf("slot-0 character name = %q, want %q", gotName, wantCharName)
	}
	if slot0GID != wantCharID {
		t.Fatalf("slot-0 GID = %d, want %d", slot0GID, wantCharID)
	}
	t.Logf("CH_ENTER ok: slot-0 charID=%d name=%q", slot0GID, gotName)
	return slot0GID
}

// stageCHSelectChar sends CH_SELECT_CHAR and asserts HC_NOTIFY_ZONESVR
// carries a non-empty map name. Returns the map name.
func stageCHSelectChar(t *testing.T, conn net.Conn, dec *netcodec.Decoder, deadline time.Duration) string {
	t.Helper()
	if err := (packet.CHSelectCharRequest{Slot: 0}).Encode(conn); err != nil {
		t.Fatalf("encode CH_SELECT_CHAR: %v", err)
	}

	cmd, frame, err := feedAndNext(t, conn, dec, deadline)
	if err != nil {
		t.Fatalf("read HC_NOTIFY_ZONESVR: %v", err)
	}
	if cmd != packet.HeaderHCNOTIFYZONESVR {
		t.Fatalf("CH_SELECT_CHAR response cmd = 0x%04x, want 0x%04x (HC_NOTIFY_ZONESVR); frame=% x",
			cmd, packet.HeaderHCNOTIFYZONESVR, frame)
	}
	if len(frame) < 28 {
		t.Fatalf("HC_NOTIFY_ZONESVR frame too short: got %d bytes, want at least 28", len(frame))
	}
	// HC_NOTIFY_ZONESVR layout (PACKETVER >= 20170315):
	//   [0:2]    cmd 0x0ac5
	//   [2:6]    CID (uint32 LE)
	//   [6:22]   mapname[16]
	//   [22:26]  ip
	//   [26:28]  port
	//   [28:156] domain[128]
	gotMap := cstrBytes(frame[6:22])
	if gotMap == "" {
		t.Fatalf("HC_NOTIFY_ZONESVR map name is empty (frame=% x)", frame)
	}
	t.Logf("CH_SELECT_CHAR ok: map=%q ip=%d port=%d",
		gotMap,
		binary.LittleEndian.Uint32(frame[22:26]),
		binary.LittleEndian.Uint16(frame[26:28]),
	)
	return gotMap
}

// stageCZEnter sends CZ_ENTER and asserts the gateway emits
// ZC_ACCEPT_ENTER followed by ZC_SPAWN_UNIT (with the expected char
// name in the spawn tail). Returns the spawn cell coords.
func stageCZEnter(t *testing.T, conn net.Conn, dec *netcodec.Decoder, deadline time.Duration, aid, charID, loginID1 uint32, sexByte uint8, wantName string) (int16, int16) {
	t.Helper()
	if err := (packet.CZEnterRequest{
		AccountID:  aid,
		CharID:     charID,
		AuthCode:   loginID1,
		ClientTime: 0,
		Sex:        sexByte,
	}).Encode(conn); err != nil {
		t.Fatalf("encode CZ_ENTER: %v", err)
	}

	cmd, acceptFrame, err := feedAndNext(t, conn, dec, deadline)
	if err != nil {
		t.Fatalf("read ZC_ACCEPT_ENTER: %v", err)
	}
	if cmd != packet.HeaderZCACCEPTENTER {
		t.Fatalf("CZ_ENTER first response cmd = 0x%04x, want 0x%04x (ZC_ACCEPT_ENTER); frame=% x",
			cmd, packet.HeaderZCACCEPTENTER, acceptFrame)
	}
	if len(acceptFrame) != zcAcceptEnterSize {
		t.Fatalf("ZC_ACCEPT_ENTER length = %d, want %d",
			len(acceptFrame), zcAcceptEnterSize)
	}
	// posDir[3] at [6:9] — unpack to recover the spawn cell.
	spawnX, spawnY, _ := decodePosBytes(acceptFrame[6:9])

	cmd, spawnFrame, err := feedAndNext(t, conn, dec, deadline)
	if err != nil {
		t.Fatalf("read ZC_SPAWN_UNIT: %v", err)
	}
	if cmd != packet.HeaderZCSPAWNUNIT {
		t.Fatalf("CZ_ENTER second response cmd = 0x%04x, want 0x%04x (ZC_SPAWN_UNIT); frame=% x",
			cmd, packet.HeaderZCSPAWNUNIT, spawnFrame)
	}
	if len(spawnFrame) != zcSpawnUnitSize {
		t.Fatalf("ZC_SPAWN_UNIT length = %d, want %d",
			len(spawnFrame), zcSpawnUnitSize)
	}
	// Spawn name lives at the tail 24 bytes — see
	// pkg/ro/packet/map_encode.go SpawnUnitResponse layout (offset 83).
	nameSlot := spawnFrame[len(spawnFrame)-24:]
	gotSpawnName := cstrBytes(nameSlot)
	if gotSpawnName != wantName {
		t.Fatalf("ZC_SPAWN_UNIT name = %q, want %q (tail=% x)",
			gotSpawnName, wantName, nameSlot)
	}
	t.Logf("CZ_ENTER ok: spawned at (%d,%d) as %q", spawnX, spawnY, gotSpawnName)
	return spawnX, spawnY
}

// stageCZRequestMove sends CZ_REQUEST_MOVE one step east+south of the
// spawn cell and asserts ZC_NOTIFY_PLAYERMOVE carries the same
// destination coords.
func stageCZRequestMove(t *testing.T, conn net.Conn, dec *netcodec.Decoder, deadline time.Duration, spawnX, spawnY int16) {
	t.Helper()
	const step = int16(5)
	destX := spawnX + step
	destY := spawnY + step

	if err := (packet.CZRequestMoveRequest{
		DestX: destX,
		DestY: destY,
	}).Encode(conn); err != nil {
		t.Fatalf("encode CZ_REQUEST_MOVE: %v", err)
	}

	cmd, moveFrame, err := feedAndNext(t, conn, dec, deadline)
	if err != nil {
		t.Fatalf("read ZC_NOTIFY_PLAYERMOVE: %v", err)
	}
	if cmd != packet.HeaderZCNOTIFYPLAYERMOVE {
		t.Fatalf("CZ_REQUEST_MOVE response cmd = 0x%04x, want 0x%04x (ZC_NOTIFY_PLAYERMOVE); frame=% x",
			cmd, packet.HeaderZCNOTIFYPLAYERMOVE, moveFrame)
	}
	if len(moveFrame) != zcNotifyPlayerMoveSize {
		t.Fatalf("ZC_NOTIFY_PLAYERMOVE length = %d, want %d",
			len(moveFrame), zcNotifyPlayerMoveSize)
	}
	// srcPos at [6:9], destPos at [9:12].
	gotDestX, gotDestY, _ := decodePosBytes(moveFrame[9:12])
	if gotDestX != destX || gotDestY != destY {
		t.Fatalf("ZC_NOTIFY_PLAYERMOVE dest = (%d,%d), want (%d,%d); frame=% x",
			gotDestX, gotDestY, destX, destY, moveFrame)
	}
	t.Logf("CZ_REQUEST_MOVE ok: moved to (%d,%d)", gotDestX, gotDestY)
}

// stageCZNotifyActorInit sends CZ_NOTIFY_ACTORINIT (cmd-only, 2 bytes)
// and asserts ZC_MAPPROPERTY_R2 carries MAPPROPERTY_NOTHING (type=0,
// flags=0) followed by the M9 status burst (ZC_PAR_CHANGE /
// ZC_LONGPAR_CHANGE / ZC_STATUS). The client sends this once after the
// map finishes loading; without the response some client forks hang
// on a black screen. The status burst is coalesced into a single
// stream read; we assert the first frame is ZC_MAPPROPERTY_R2 and
// scan the remainder for the ZC_STATUS header (0x00bd) to confirm
// the burst was delivered.
func stageCZNotifyActorInit(t *testing.T, conn net.Conn, dec *netcodec.Decoder, deadline time.Duration) {
	t.Helper()
	// CZ_NOTIFY_ACTORINIT is cmd-only (2 bytes, no payload).
	buf := make([]byte, 2)
	binary.LittleEndian.PutUint16(buf, packet.HeaderCZNOTIFYACTORINIT)
	if _, err := conn.Write(buf); err != nil {
		t.Fatalf("write CZ_NOTIFY_ACTORINIT: %v", err)
	}

	cmd, propFrame, err := feedAndNext(t, conn, dec, deadline)
	if err != nil {
		t.Fatalf("read ZC_MAPPROPERTY_R2: %v", err)
	}
	if cmd != packet.HeaderZCMAPPROPERTYR2 {
		t.Fatalf("CZ_NOTIFY_ACTORINIT response cmd = 0x%04x, want 0x%04x (ZC_MAPPROPERTY_R2); frame=% x",
			cmd, packet.HeaderZCMAPPROPERTYR2, propFrame)
	}
	if len(propFrame) != zcMapPropertyR2Size {
		t.Fatalf("ZC_MAPPROPERTY_R2 length = %d, want %d",
			len(propFrame), zcMapPropertyR2Size)
	}
	// propertyType at [2:4] = 0 (MAPPROPERTY_NOTHING), flags at
	// [4:8] = 0 — no maps carry PVP/GVG flags yet.
	if pt := binary.LittleEndian.Uint16(propFrame[2:4]); pt != 0 {
		t.Errorf("ZC_MAPPROPERTY_R2 propertyType = %d, want 0 (MAPPROPERTY_NOTHING)", pt)
	}
	if flags := binary.LittleEndian.Uint32(propFrame[4:8]); flags != 0 {
		t.Errorf("ZC_MAPPROPERTY_R2 flags = 0x%x, want 0", flags)
	}

	// M9+M10+M14: the response stream after MAPPROPERTY_R2 contains the
	// status burst (ZC_PAR_CHANGE / ZC_LONGPAR_CHANGE / ZC_STATUS) and
	// the four M10 empty list packets (ZC_INVENTORY_ITEMLIST_NORMAL /
	// ZC_INVENTORY_ITEMLIST_EQUIP / ZC_SKILLINFO_LIST /
	// ZC_SHORTCUT_KEY_LIST) followed by M14 NPC spawn packets
	// (ZC_SET_UNIT_IDLE 0x09ff, 107 bytes each). Drain framed packets
	// from the decoder until either we have observed all the required
	// headers in the rAthena LoadEndAck order or the decoder reports
	// ErrIncomplete / a timeout.
	if err := conn.SetReadDeadline(time.Now().Add(deadline)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	chunk := make([]byte, 4096)
	sawStatus := false
	var emptyListCmds []uint16
	var npcSpawnGIDs []uint32
	packets := 0
	wantEmpty := []uint16{
		packet.HeaderZCINVENTORYITEMLISTNORMAL,
		packet.HeaderZCINVENTORYITEMLISTEQUIP,
		packet.HeaderZCSKILLINFOLIST,
		packet.HeaderZCSHORTCUTKEYLIST,
	}
	wantNPCGIDs := []uint32{110000001, 110000002, 110000003, 110000004}
	for packets < 128 { // generous upper bound on status-burst + list + NPC packet count
		fcmd, frame, derr := dec.Next()
		if derr == nil {
			packets++
			switch fcmd {
			case packet.HeaderZCSTATUS:
				if len(frame) != 44 {
					t.Errorf("ZC_STATUS frame length = %d, want 44", len(frame))
				}
				sawStatus = true
			case packet.HeaderZCINVENTORYITEMLISTNORMAL,
				packet.HeaderZCINVENTORYITEMLISTEQUIP,
				packet.HeaderZCSKILLINFOLIST:
				// Variable-length list packets with an empty payload
				// must be exactly 4 bytes (cmd + packetLength=4, no
				// entries).
				if len(frame) != 4 {
					t.Errorf("empty list packet 0x%04x frame length = %d, want 4", fcmd, len(frame))
				}
				emptyListCmds = append(emptyListCmds, fcmd)
			case packet.HeaderZCSHORTCUTKEYLIST:
				// Fixed 191-byte payload: 2 (opcode) + 27 * 7
				// (zero-filled hotkey slots).
				if len(frame) != zcShortcutKeyListSize {
					t.Errorf("ZC_SHORTCUT_KEY_LIST frame length = %d, want %d", len(frame), zcShortcutKeyListSize)
				}
				emptyListCmds = append(emptyListCmds, fcmd)
			case packet.HeaderZCSETUNITIDLE:
				// M14: NPC spawn packet (107 bytes fixed).
				if len(frame) != zcSetUnitIdleSize {
					t.Errorf("ZC_SET_UNIT_IDLE frame length = %d, want %d", len(frame), zcSetUnitIdleSize)
				}
				// AID at offset 5 (uint32 LE) is the NPC GID.
				gid := binary.LittleEndian.Uint32(frame[5:9])
				npcSpawnGIDs = append(npcSpawnGIDs, gid)
			}
			if sawStatus && len(emptyListCmds) == len(wantEmpty) && len(npcSpawnGIDs) == len(wantNPCGIDs) {
				break
			}
			continue
		}
		if derr != netcodec.ErrIncomplete {
			break
		}
		n, rerr := conn.Read(chunk)
		if n > 0 {
			dec.Feed(chunk[:n])
			continue
		}
		if rerr == nil {
			break
		}
		if netErr, ok := rerr.(net.Error); ok && netErr.Timeout() {
			break
		}
		t.Fatalf("read status burst: %v", rerr)
	}
	if !sawStatus {
		t.Fatalf("status burst did not deliver ZC_STATUS (0x%04x) after %d framed packets", packet.HeaderZCSTATUS, packets)
	}
	if len(emptyListCmds) != len(wantEmpty) {
		t.Fatalf("empty-list packets seen = %d, want %d (cmds: % x)",
			len(emptyListCmds), len(wantEmpty), emptyListCmds)
	}
	for i, want := range wantEmpty {
		if emptyListCmds[i] != want {
			t.Errorf("empty-list packet[%d] = 0x%04x, want 0x%04x (rAthena LoadEndAck order)",
				i, emptyListCmds[i], want)
		}
	}
	if len(npcSpawnGIDs) != len(wantNPCGIDs) {
		t.Fatalf("NPC spawn packets seen = %d, want %d (GIDs: %v)",
			len(npcSpawnGIDs), len(wantNPCGIDs), npcSpawnGIDs)
	}
	for i, want := range wantNPCGIDs {
		if npcSpawnGIDs[i] != want {
			t.Errorf("NPC spawn[%d] GID = %d, want %d", i, npcSpawnGIDs[i], want)
		}
	}
	t.Logf("CZ_NOTIFY_ACTORINIT ok: propertyType=0 flags=0 burst_packets=%d empty_list_packets=%d npc_spawns=%d", packets, len(emptyListCmds), len(npcSpawnGIDs))
}

// stageCZRequestTime sends CZ_REQUEST_TIME and asserts ZC_NOTIFY_TIME
// arrives with a non-zero server tick.
func stageCZRequestTime(t *testing.T, conn net.Conn, dec *netcodec.Decoder, deadline time.Duration) {
	t.Helper()
	const clientTick uint32 = 12345
	if err := (packet.CZRequestTimeRequest{ClientTick: clientTick}).Encode(conn); err != nil {
		t.Fatalf("encode CZ_REQUEST_TIME: %v", err)
	}

	cmd, timeFrame, err := feedAndNext(t, conn, dec, deadline)
	if err != nil {
		t.Fatalf("read ZC_NOTIFY_TIME: %v", err)
	}
	if cmd != packet.HeaderZCNOTIFYTIME {
		t.Fatalf("CZ_REQUEST_TIME response cmd = 0x%04x, want 0x%04x (ZC_NOTIFY_TIME); frame=% x",
			cmd, packet.HeaderZCNOTIFYTIME, timeFrame)
	}
	if len(timeFrame) != zcNotifyTimeSize {
		t.Fatalf("ZC_NOTIFY_TIME length = %d, want %d",
			len(timeFrame), zcNotifyTimeSize)
	}
	if srvTick := binary.LittleEndian.Uint32(timeFrame[2:6]); srvTick == 0 {
		t.Errorf("ZC_NOTIFY_TIME server tick = 0, want non-zero (unix millis)")
	}
	t.Logf("CZ_REQUEST_TIME ok: client_tick=%d", clientTick)
}

// stageCZGlobalMessage sends CZ_GLOBAL_MESSAGE and asserts ZC_NOTIFY_CHAT
// arrives carrying the same message text and the AID as the GID (M11
// echo path — no AOI yet). The wire layout is
//
//	[0:2]   cmd 0x008d
//	[2:4]   packetLength
//	[4:8]   GID
//	[8:n+8] message bytes
//	[n+8]   NUL terminator
func stageCZGlobalMessage(t *testing.T, conn net.Conn, dec *netcodec.Decoder, deadline time.Duration, aid uint32) {
	t.Helper()
	const message = "hello e2e"
	if err := (packet.CZGlobalMessageRequest{Message: message}).Encode(conn); err != nil {
		t.Fatalf("encode CZ_GLOBAL_MESSAGE: %v", err)
	}

	cmd, chatFrame, err := feedAndNext(t, conn, dec, deadline)
	if err != nil {
		t.Fatalf("read ZC_NOTIFY_CHAT: %v", err)
	}
	if cmd != packet.HeaderZCNOTIFYCHAT {
		t.Fatalf("CZ_GLOBAL_MESSAGE response cmd = 0x%04x, want 0x%04x (ZC_NOTIFY_CHAT); frame=% x",
			cmd, packet.HeaderZCNOTIFYCHAT, chatFrame)
	}
	const wantLen = 4 + 4 + len(message) + 1 // header + GID + msg + NUL
	if len(chatFrame) != wantLen {
		t.Fatalf("ZC_NOTIFY_CHAT length = %d, want %d (frame=% x)",
			len(chatFrame), wantLen, chatFrame)
	}
	if plen := binary.LittleEndian.Uint16(chatFrame[2:4]); int(plen) != wantLen {
		t.Errorf("ZC_NOTIFY_CHAT packetLength = %d, want %d", plen, wantLen)
	}
	if gid := binary.LittleEndian.Uint32(chatFrame[4:8]); gid != aid {
		t.Errorf("ZC_NOTIFY_CHAT GID = %d, want %d (AID-as-GID echo)", gid, aid)
	}
	if got := cstrBytes(chatFrame[8:]); got != message {
		t.Errorf("ZC_NOTIFY_CHAT message = %q, want %q", got, message)
	}
	t.Logf("CZ_GLOBAL_MESSAGE ok: message=%q", message)
}

// stageCZActionRequestSit sends CZ_ACTION_REQUEST with action=1 (sit)
// and asserts ZC_ACTION_RESPONSE echoes the same action byte back with
// the AID stamped in the GID slot and targetGID=0.
func stageCZActionRequestSit(t *testing.T, conn net.Conn, dec *netcodec.Decoder, deadline time.Duration, aid uint32) {
	t.Helper()
	if err := (packet.CZActionRequestRequest{TargetGID: aid, Action: 1}).Encode(conn); err != nil {
		t.Fatalf("encode CZ_ACTION_REQUEST (sit): %v", err)
	}

	cmd, actFrame, err := feedAndNext(t, conn, dec, deadline)
	if err != nil {
		t.Fatalf("read ZC_ACTION_RESPONSE: %v", err)
	}
	if cmd != packet.HeaderZCACTIONRESPONSE {
		t.Fatalf("CZ_ACTION_REQUEST response cmd = 0x%04x, want 0x%04x (ZC_ACTION_RESPONSE); frame=% x",
			cmd, packet.HeaderZCACTIONRESPONSE, actFrame)
	}
	if len(actFrame) != zcActionResponseSize {
		t.Fatalf("ZC_ACTION_RESPONSE length = %d, want %d",
			len(actFrame), zcActionResponseSize)
	}
	if gid := binary.LittleEndian.Uint32(actFrame[2:6]); gid != aid {
		t.Errorf("ZC_ACTION_RESPONSE GID = %d, want %d (AID-as-GID echo)", gid, aid)
	}
	if actFrame[6] != 0x01 {
		t.Errorf("ZC_ACTION_RESPONSE action = %d, want 1 (sit)", actFrame[6])
	}
	if tgt := binary.LittleEndian.Uint32(actFrame[7:11]); tgt != 0 {
		t.Errorf("ZC_ACTION_RESPONSE targetGID = %d, want 0", tgt)
	}
	t.Logf("CZ_ACTION_REQUEST ok: action=sit gid=%d", aid)
}

// stageCZChangeDir sends CZ_CHANGE_DIRECTION with headDir=CCW + dir=SE
// and asserts ZC_CHANGE_DIRECTION echoes the same head/dir bytes with
// the AID stamped in the srcId slot. Wire layout is
//
//	[0:2]   cmd 0x009c
//	[2:6]   srcId (uint32 LE)
//	[6:8]   headDir (uint16 LE)
//	[8]     dir (uint8)
func stageCZChangeDir(t *testing.T, conn net.Conn, dec *netcodec.Decoder, deadline time.Duration, aid uint32) {
	t.Helper()
	const (
		wantHead uint16 = 0x0002 // CCW (clif.cpp:11569)
		wantDir  uint8  = 0x05   // SE (clif.cpp:11576)
	)
	if err := (packet.CZChangeDirRequest{HeadDir: wantHead, Dir: wantDir}).Encode(conn); err != nil {
		t.Fatalf("encode CZ_CHANGE_DIRECTION: %v", err)
	}

	cmd, dirFrame, err := feedAndNext(t, conn, dec, deadline)
	if err != nil {
		t.Fatalf("read ZC_CHANGE_DIRECTION: %v", err)
	}
	if cmd != packet.HeaderZCCHANGEDIR {
		t.Fatalf("CZ_CHANGE_DIRECTION response cmd = 0x%04x, want 0x%04x (ZC_CHANGE_DIRECTION); frame=% x",
			cmd, packet.HeaderZCCHANGEDIR, dirFrame)
	}
	if len(dirFrame) != zcChangeDirSize {
		t.Fatalf("ZC_CHANGE_DIRECTION length = %d, want %d (frame=% x)",
			len(dirFrame), zcChangeDirSize, dirFrame)
	}
	if src := binary.LittleEndian.Uint32(dirFrame[2:6]); src != aid {
		t.Errorf("ZC_CHANGE_DIRECTION srcId = %d, want %d (AID-as-srcId echo)", src, aid)
	}
	if hd := binary.LittleEndian.Uint16(dirFrame[6:8]); hd != wantHead {
		t.Errorf("ZC_CHANGE_DIRECTION headDir = 0x%x, want 0x%x", hd, wantHead)
	}
	if d := dirFrame[8]; d != wantDir {
		t.Errorf("ZC_CHANGE_DIRECTION dir = 0x%02x, want 0x%02x", d, wantDir)
	}
	t.Logf("CZ_CHANGE_DIRECTION ok: head=CCW dir=SE gid=%d", aid)
}

// stageCZReqEmotion sends CZ_REQ_EMOTION with emotion_type=ET_OK and
// asserts ZC_EMOTION echoes the same type byte with the AID stamped in
// the GID slot. Wire layout is
//
//	[0:2]   cmd 0x00c0
//	[2:6]   GID (uint32 LE)
//	[6]     type (uint8)
func stageCZReqEmotion(t *testing.T, conn net.Conn, dec *netcodec.Decoder, deadline time.Duration, aid uint32) {
	t.Helper()
	const wantType uint8 = 0x07 // ET_OK
	if err := (packet.CZReqEmotionRequest{EmotionType: wantType}).Encode(conn); err != nil {
		t.Fatalf("encode CZ_REQ_EMOTION: %v", err)
	}

	cmd, emoFrame, err := feedAndNext(t, conn, dec, deadline)
	if err != nil {
		t.Fatalf("read ZC_EMOTION: %v", err)
	}
	if cmd != packet.HeaderZCEMOTION {
		t.Fatalf("CZ_REQ_EMOTION response cmd = 0x%04x, want 0x%04x (ZC_EMOTION); frame=% x",
			cmd, packet.HeaderZCEMOTION, emoFrame)
	}
	if len(emoFrame) != zcEmotionSize {
		t.Fatalf("ZC_EMOTION length = %d, want %d (frame=% x)",
			len(emoFrame), zcEmotionSize, emoFrame)
	}
	if gid := binary.LittleEndian.Uint32(emoFrame[2:6]); gid != aid {
		t.Errorf("ZC_EMOTION GID = %d, want %d (AID-as-GID echo)", gid, aid)
	}
	if typ := emoFrame[6]; typ != wantType {
		t.Errorf("ZC_EMOTION type = 0x%02x, want 0x%02x", typ, wantType)
	}
	t.Logf("CZ_REQ_EMOTION ok: type=ET_OK gid=%d", aid)
}

// stageCZGetCharNameRequest sends CZ_GETCHARNAMEREQUEST for the
// player's own CharID and asserts ZC_ACK_REQNAME carries the expected
// character name. Wire layout is
//
//	[0:2]   cmd 0x0095
//	[2:6]   GID (uint32 LE)
//	[6:30]  name[24] (NUL-padded)
func stageCZGetCharNameRequest(t *testing.T, conn net.Conn, dec *netcodec.Decoder, deadline time.Duration, charID uint32, wantName string) {
	t.Helper()
	if err := (packet.CZGetCharNameRequestRequest{GID: charID}).Encode(conn); err != nil {
		t.Fatalf("encode CZ_GETCHARNAMEREQUEST: %v", err)
	}

	cmd, nameFrame, err := feedAndNext(t, conn, dec, deadline)
	if err != nil {
		t.Fatalf("read ZC_ACK_REQNAME: %v", err)
	}
	if cmd != packet.HeaderZCACKREQNAME {
		t.Fatalf("CZ_GETCHARNAMEREQUEST response cmd = 0x%04x, want 0x%04x (ZC_ACK_REQNAME); frame=% x",
			cmd, packet.HeaderZCACKREQNAME, nameFrame)
	}
	if len(nameFrame) != zcAckReqNameSize {
		t.Fatalf("ZC_ACK_REQNAME length = %d, want %d (frame=% x)",
			len(nameFrame), zcAckReqNameSize, nameFrame)
	}
	if gid := binary.LittleEndian.Uint32(nameFrame[2:6]); gid != charID {
		t.Errorf("ZC_ACK_REQNAME GID = %d, want %d", gid, charID)
	}
	gotName := cstrBytes(nameFrame[6:30])
	if gotName != wantName {
		t.Errorf("ZC_ACK_REQNAME name = %q, want %q", gotName, wantName)
	}
	t.Logf("CZ_GETCHARNAMEREQUEST ok: name=%q gid=%d", gotName, charID)
}

// stageCZRestart sends CZ_RESTART with type=1 (return to char select)
// and asserts no response is written (the gateway logs the request but
// does not implement the state transition yet).
func stageCZRestart(t *testing.T, conn net.Conn, dec *netcodec.Decoder, deadline time.Duration) {
	t.Helper()
	if err := (packet.CZRestartRequest{Type: 0x01}).Encode(conn); err != nil {
		t.Fatalf("encode CZ_RESTART: %v", err)
	}

	// CZ_RESTART is logged only — no response is expected. Verify that
	// the next read times out (no packet was written back).
	if err := conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	chunk := make([]byte, 4096)
	for {
		cmd, _, derr := dec.Next()
		if derr == nil {
			t.Fatalf("unexpected response packet 0x%04x after CZ_RESTART", cmd)
		}
		if derr != netcodec.ErrIncomplete {
			break
		}
		n, rerr := conn.Read(chunk)
		if n > 0 {
			dec.Feed(chunk[:n])
			continue
		}
		if rerr == nil {
			break
		}
		if netErr, ok := rerr.(net.Error); ok && netErr.Timeout() {
			break
		}
		t.Fatalf("read after CZ_RESTART: %v", rerr)
	}
	t.Logf("CZ_RESTART ok: no response (logged only)")
}

// stageCZContactNPC sends CZ_CONTACTNPC with a known NPC GID and
// asserts the server responds with ZC_SAY_DIALOG2 + ZC_WAIT_DIALOG2.
// Wire layout for ZC_SAY_DIALOG2 (0x0972):
//
//	[0:2]   cmd 0x0972
//	[2:4]   packetLength (uint16 LE)
//	[4:8]   NpcID (uint32 LE)
//	[8]     type (uint8)
//	[9:]    message (NUL-terminated)
//
// Wire layout for ZC_WAIT_DIALOG2 (0x0973):
//
//	[0:2]   cmd 0x0973
//	[2:6]   NpcID (uint32 LE)
//	[6]     type (uint8)
func stageCZContactNPC(t *testing.T, conn net.Conn, dec *netcodec.Decoder, deadline time.Duration, npcGID uint32, wantName string) {
	t.Helper()
	if err := (packet.CZContactNPCRequest{AID: npcGID, Type: 0x01}).Encode(conn); err != nil {
		t.Fatalf("encode CZ_CONTACTNPC: %v", err)
	}

	// First response: ZC_SAY_DIALOG2.
	cmd, sayFrame, err := feedAndNext(t, conn, dec, deadline)
	if err != nil {
		t.Fatalf("read ZC_SAY_DIALOG2: %v", err)
	}
	if cmd != packet.HeaderZCSAYDIALOG2 {
		t.Fatalf("CZ_CONTACTNPC response cmd = 0x%04x, want 0x%04x (ZC_SAY_DIALOG2); frame=% x",
			cmd, packet.HeaderZCSAYDIALOG2, sayFrame)
	}
	sayLen := int(binary.LittleEndian.Uint16(sayFrame[2:4]))
	if sayLen < 10 || sayLen > len(sayFrame) {
		t.Fatalf("ZC_SAY_DIALOG2 packetLength = %d, want >= 10 (frame len=%d)", sayLen, len(sayFrame))
	}
	if nid := binary.LittleEndian.Uint32(sayFrame[4:8]); nid != npcGID {
		t.Errorf("ZC_SAY_DIALOG2 NpcID = %d, want %d", nid, npcGID)
	}
	if sayFrame[8] != 0 {
		t.Errorf("ZC_SAY_DIALOG2 type = %d, want 0", sayFrame[8])
	}
	gotMsg := cstrBytes(sayFrame[9:sayLen])
	wantMsg := "Welcome to goAthena! This is " + wantName + "."
	if gotMsg != wantMsg {
		t.Errorf("ZC_SAY_DIALOG2 message = %q, want %q", gotMsg, wantMsg)
	}

	// Second response: ZC_WAIT_DIALOG2.
	cmd, waitFrame, err := feedAndNext(t, conn, dec, deadline)
	if err != nil {
		t.Fatalf("read ZC_WAIT_DIALOG2: %v", err)
	}
	if cmd != packet.HeaderZCWAITDIALOG2 {
		t.Fatalf("CZ_CONTACTNPC second response cmd = 0x%04x, want 0x%04x (ZC_WAIT_DIALOG2); frame=% x",
			cmd, packet.HeaderZCWAITDIALOG2, waitFrame)
	}
	if len(waitFrame) != zcWaitDialog2Size {
		t.Fatalf("ZC_WAIT_DIALOG2 length = %d, want %d (frame=% x)",
			len(waitFrame), zcWaitDialog2Size, waitFrame)
	}
	if nid := binary.LittleEndian.Uint32(waitFrame[2:6]); nid != npcGID {
		t.Errorf("ZC_WAIT_DIALOG2 NpcID = %d, want %d", nid, npcGID)
	}
	if waitFrame[6] != 0 {
		t.Errorf("ZC_WAIT_DIALOG2 type = %d, want 0", waitFrame[6])
	}
	t.Logf("CZ_CONTACTNPC ok: npc=%q gid=%d", wantName, npcGID)
}

// stageCZReqNextScript sends CZ_REQNEXTSCRIPT and asserts the server
// responds with ZC_SAY_DIALOG2 + ZC_CLOSE_DIALOG.
// Wire layout for ZC_CLOSE_DIALOG (0x00b6):
//
//	[0:2]   cmd 0x00b6
//	[2:6]   NpcID (uint32 LE)
func stageCZReqNextScript(t *testing.T, conn net.Conn, dec *netcodec.Decoder, deadline time.Duration, npcID uint32) {
	t.Helper()
	if err := (packet.CZReqNextScriptRequest{NpcID: npcID}).Encode(conn); err != nil {
		t.Fatalf("encode CZ_REQNEXTSCRIPT: %v", err)
	}

	// First response: ZC_SAY_DIALOG2.
	cmd, sayFrame, err := feedAndNext(t, conn, dec, deadline)
	if err != nil {
		t.Fatalf("read ZC_SAY_DIALOG2: %v", err)
	}
	if cmd != packet.HeaderZCSAYDIALOG2 {
		t.Fatalf("CZ_REQNEXTSCRIPT response cmd = 0x%04x, want 0x%04x (ZC_SAY_DIALOG2); frame=% x",
			cmd, packet.HeaderZCSAYDIALOG2, sayFrame)
	}
	sayLen := int(binary.LittleEndian.Uint16(sayFrame[2:4]))
	if sayLen < 10 || sayLen > len(sayFrame) {
		t.Fatalf("ZC_SAY_DIALOG2 packetLength = %d, want >= 10 (frame len=%d)", sayLen, len(sayFrame))
	}
	if nid := binary.LittleEndian.Uint32(sayFrame[4:8]); nid != npcID {
		t.Errorf("ZC_SAY_DIALOG2 NpcID = %d, want %d", nid, npcID)
	}
	gotMsg := cstrBytes(sayFrame[9:sayLen])
	const wantMsg = "The server is under development. Enjoy exploring!"
	if gotMsg != wantMsg {
		t.Errorf("ZC_SAY_DIALOG2 message = %q, want %q", gotMsg, wantMsg)
	}

	// Second response: ZC_CLOSE_DIALOG.
	cmd, closeFrame, err := feedAndNext(t, conn, dec, deadline)
	if err != nil {
		t.Fatalf("read ZC_CLOSE_DIALOG: %v", err)
	}
	if cmd != packet.HeaderZCCLOSEDIALOG {
		t.Fatalf("CZ_REQNEXTSCRIPT second response cmd = 0x%04x, want 0x%04x (ZC_CLOSE_DIALOG); frame=% x",
			cmd, packet.HeaderZCCLOSEDIALOG, closeFrame)
	}
	if len(closeFrame) != zcCloseDialogSize {
		t.Fatalf("ZC_CLOSE_DIALOG length = %d, want %d (frame=% x)",
			len(closeFrame), zcCloseDialogSize, closeFrame)
	}
	if nid := binary.LittleEndian.Uint32(closeFrame[2:6]); nid != npcID {
		t.Errorf("ZC_CLOSE_DIALOG NpcID = %d, want %d", nid, npcID)
	}
	t.Logf("CZ_REQNEXTSCRIPT ok: npc_id=%d", npcID)
}

// stageCZCloseDialog sends CZ_CLOSE_DIALOG and asserts no response is
// written (the client closes the dialog locally).
func stageCZCloseDialog(t *testing.T, conn net.Conn, dec *netcodec.Decoder, deadline time.Duration, npcGID uint32) {
	t.Helper()
	if err := (packet.CZCloseDialogRequest{GID: npcGID}).Encode(conn); err != nil {
		t.Fatalf("encode CZ_CLOSE_DIALOG: %v", err)
	}

	// CZ_CLOSE_DIALOG is logged only — no response is expected.
	if err := conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	chunk := make([]byte, 4096)
	for {
		cmd, _, derr := dec.Next()
		if derr == nil {
			t.Fatalf("unexpected response packet 0x%04x after CZ_CLOSE_DIALOG", cmd)
		}
		if derr != netcodec.ErrIncomplete {
			break
		}
		n, rerr := conn.Read(chunk)
		if n > 0 {
			dec.Feed(chunk[:n])
			continue
		}
		if rerr == nil {
			break
		}
		if netErr, ok := rerr.(net.Error); ok && netErr.Timeout() {
			break
		}
		t.Fatalf("read after CZ_CLOSE_DIALOG: %v", rerr)
	}
	t.Logf("CZ_CLOSE_DIALOG ok: no response (logged only)")
}

// M16: NPC shop interaction stages.
//
// stageCZContactNPCShop sends CZ_CONTACTNPC for the Weapon Shop NPC
// (GID 110000002) and asserts the server responds with ZC_SELECT_DEALTYPE
// (0x00c4) rather than the M15 dialog sequence. Wire layout:
//
//	[0:2]   cmd 0x00c4
//	[2:6]   NpcID (uint32 LE)
func stageCZContactNPCShop(t *testing.T, conn net.Conn, dec *netcodec.Decoder, deadline time.Duration, npcGID uint32) {
	t.Helper()
	if err := (packet.CZContactNPCRequest{AID: npcGID, Type: 0x01}).Encode(conn); err != nil {
		t.Fatalf("encode CZ_CONTACTNPC: %v", err)
	}

	cmd, frame, err := feedAndNext(t, conn, dec, deadline)
	if err != nil {
		t.Fatalf("read ZC_SELECT_DEALTYPE: %v", err)
	}
	if cmd != packet.HeaderZCSELECTDEALTYPE {
		t.Fatalf("CZ_CONTACTNPC shop response cmd = 0x%04x, want 0x%04x (ZC_SELECT_DEALTYPE); frame=% x",
			cmd, packet.HeaderZCSELECTDEALTYPE, frame)
	}
	if len(frame) != zcSelectDealtypeSize {
		t.Fatalf("ZC_SELECT_DEALTYPE length = %d, want %d (frame=% x)",
			len(frame), zcSelectDealtypeSize, frame)
	}
	if nid := binary.LittleEndian.Uint32(frame[2:6]); nid != npcGID {
		t.Errorf("ZC_SELECT_DEALTYPE NpcID = %d, want %d", nid, npcGID)
	}
	t.Logf("CZ_CONTACTNPC shop ok: npc_gid=%d", npcGID)
}

// stageCZAckSelectDealTypeBuy sends CZ_ACK_SELECT_DEALTYPE with type=0
// (Buy) and asserts ZC_PC_PURCHASE_ITEMLIST carries the Weapon Shop
// stock list (4 items: 501, 502, 1201, 1101). Wire layout per item
// (19 bytes):
//
//	[0:4]   itemId (uint32 LE)
//	[4:8]   price (uint32 LE)
//	[8:12]  discountPrice (uint32 LE)
//	[12]    itemType (uint8)
//	[13:15] viewSprite (uint16 LE)
//	[15:19] location (uint32 LE)
func stageCZAckSelectDealTypeBuy(t *testing.T, conn net.Conn, dec *netcodec.Decoder, deadline time.Duration, npcGID uint32) {
	t.Helper()
	if err := (packet.CZAckSelectDealTypeRequest{NpcID: npcGID, Type: 0x00}).Encode(conn); err != nil {
		t.Fatalf("encode CZ_ACK_SELECT_DEALTYPE: %v", err)
	}

	cmd, frame, err := feedAndNext(t, conn, dec, deadline)
	if err != nil {
		t.Fatalf("read ZC_PC_PURCHASE_ITEMLIST: %v", err)
	}
	if cmd != packet.HeaderZCPCPURCHASEITEMLIST {
		t.Fatalf("CZ_ACK_SELECT_DEALTYPE response cmd = 0x%04x, want 0x%04x (ZC_PC_PURCHASE_ITEMLIST); frame=% x",
			cmd, packet.HeaderZCPCPURCHASEITEMLIST, frame)
	}
	const wantItemCount = 4
	wantLen := 4 + wantItemCount*shopBuyItemWireSize
	if len(frame) != wantLen {
		t.Fatalf("ZC_PC_PURCHASE_ITEMLIST length = %d, want %d (frame=% x)",
			len(frame), wantLen, frame)
	}
	if plen := binary.LittleEndian.Uint16(frame[2:4]); int(plen) != wantLen {
		t.Errorf("ZC_PC_PURCHASE_ITEMLIST packetLength = %d, want %d", plen, wantLen)
	}

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
		if id := binary.LittleEndian.Uint32(frame[off : off+4]); id != w.itemID {
			t.Errorf("item[%d] itemId = %d, want %d", i, id, w.itemID)
		}
		if price := binary.LittleEndian.Uint32(frame[off+4 : off+8]); price != w.price {
			t.Errorf("item[%d] price = %d, want %d", i, price, w.price)
		}
		if ty := frame[off+12]; ty != w.itemTy {
			t.Errorf("item[%d] itemType = %d, want %d", i, ty, w.itemTy)
		}
		if sprite := binary.LittleEndian.Uint16(frame[off+13 : off+15]); sprite != w.sprite {
			t.Errorf("item[%d] viewSprite = %d, want %d", i, sprite, w.sprite)
		}
		if loc := binary.LittleEndian.Uint32(frame[off+15 : off+19]); loc != w.loc {
			t.Errorf("item[%d] location = %d, want %d", i, loc, w.loc)
		}
	}
	t.Logf("CZ_ACK_SELECT_DEALTYPE buy ok: items=%d", wantItemCount)
}

// stageCZPCPurchaseItemList sends CZ_PC_PURCHASE_ITEMLIST for one item
// (Red Potion, amount=1) and asserts ZC_PC_PURCHASE_RESULT arrives
// with result=0 (success). Wire layout:
//
//	[0:2]   cmd 0x00ca
//	[2]     result (uint8)
func stageCZPCPurchaseItemList(t *testing.T, conn net.Conn, dec *netcodec.Decoder, deadline time.Duration) {
	t.Helper()
	req := packet.CZPCPurchaseItemListRequest{
		Entries: []packet.CZPCPurchaseItemListEntry{
			{ItemID: 501, Amount: 1},
		},
	}
	if err := req.Encode(conn); err != nil {
		t.Fatalf("encode CZ_PC_PURCHASE_ITEMLIST: %v", err)
	}

	cmd, frame, err := feedAndNext(t, conn, dec, deadline)
	if err != nil {
		t.Fatalf("read ZC_PC_PURCHASE_RESULT: %v", err)
	}
	if cmd != packet.HeaderZCPCPURCHASERESULT {
		t.Fatalf("CZ_PC_PURCHASE_ITEMLIST response cmd = 0x%04x, want 0x%04x (ZC_PC_PURCHASE_RESULT); frame=% x",
			cmd, packet.HeaderZCPCPURCHASERESULT, frame)
	}
	if len(frame) != zcPCPurchaseResultSize {
		t.Fatalf("ZC_PC_PURCHASE_RESULT length = %d, want %d (frame=% x)",
			len(frame), zcPCPurchaseResultSize, frame)
	}
	if res := frame[2]; res != 0 {
		t.Errorf("ZC_PC_PURCHASE_RESULT result = %d, want 0 (success)", res)
	}
	t.Logf("CZ_PC_PURCHASE_ITEMLIST ok: result=success")
}

// stageCZAckSelectDealTypeCancel sends CZ_ACK_SELECT_DEALTYPE with
// type=2 (Cancel) and asserts no response is written.
func stageCZAckSelectDealTypeCancel(t *testing.T, conn net.Conn, dec *netcodec.Decoder, deadline time.Duration, npcGID uint32) {
	t.Helper()
	if err := (packet.CZAckSelectDealTypeRequest{NpcID: npcGID, Type: 0x02}).Encode(conn); err != nil {
		t.Fatalf("encode CZ_ACK_SELECT_DEALTYPE (Cancel): %v", err)
	}

	if err := conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	chunk := make([]byte, 4096)
	for {
		cmd, _, derr := dec.Next()
		if derr == nil {
			t.Fatalf("unexpected response packet 0x%04x after CZ_ACK_SELECT_DEALTYPE (Cancel)", cmd)
		}
		if derr != netcodec.ErrIncomplete {
			break
		}
		n, rerr := conn.Read(chunk)
		if n > 0 {
			dec.Feed(chunk[:n])
			continue
		}
		if rerr == nil {
			break
		}
		if netErr, ok := rerr.(net.Error); ok && netErr.Timeout() {
			break
		}
		t.Fatalf("read after CZ_ACK_SELECT_DEALTYPE (Cancel): %v", rerr)
	}
	t.Logf("CZ_ACK_SELECT_DEALTYPE cancel ok: no response")
}

// TestE2E_GatewayFullFlow_TCP speaks the raw kRO binary packet protocol
// over a single TCP connection to the running gateway and exercises the
// full login → char → map → move round-trip across every real
// boundary:
//
//	gateway (TCP parse / encrypt / dispatch)
//	  → identity gRPC (Authenticate, GetCharacterList)
//	  → zone gRPC     (EnterZone, Spawn self, PlayerMove)
//	  → MariaDB       (account + char fixtures)
//
// The test fails fast on a live cluster that misbehaves; it skips when
// the gateway TCP port is unreachable (no cluster booted).
func TestE2E_GatewayFullFlow_TCP(t *testing.T) {
	_ = TestContext(t) // future hooks (logging, trace) wired through the per-test ctx.

	h := NewE2EHarness(t)

	userID := UniqueUserID()
	const password = "m7b-tcp-pass"
	accountID := createTestAccount(t, h, userID, password, domain.SexMale)
	t.Cleanup(func() { deleteTestAccount(t, h, accountID) })
	t.Cleanup(func() {
		dctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		deleteValkeySession(dctx, h, accountID)
	})

	charName := UniqueCharName()
	charID := createTestCharacter(t, h, accountID, 0, charName)
	t.Cleanup(func() { deleteTestCharacter(t, h, charID) })

	conn, dec := dialGatewayAndDecode(t, h.Config.GatewayTCPAddr)

	// Bound every read / write so a broken cluster cannot hang the
	// test forever.
	const ioDeadline = 10 * time.Second
	if err := conn.SetDeadline(time.Now().Add(ioDeadline)); err != nil {
		t.Fatalf("set conn deadline: %v", err)
	}

	aid, loginID1, sexByte := stageCALogin(t, conn, dec, ioDeadline, userID, password)
	if aid != accountID {
		t.Fatalf("AC_ACCEPT_LOGIN AID = %d, want %d (account fixture)", aid, accountID)
	}
	slot0GID := stageCHEnter(t, conn, dec, ioDeadline, aid, loginID1, sexByte, charID, charName)
	stageCHSelectChar(t, conn, dec, ioDeadline)
	spawnX, spawnY := stageCZEnter(t, conn, dec, ioDeadline, aid, slot0GID, loginID1, sexByte, charName)
	stageCZNotifyActorInit(t, conn, dec, ioDeadline)
	stageCZRequestTime(t, conn, dec, ioDeadline)
	stageCZRequestMove(t, conn, dec, ioDeadline, spawnX, spawnY)
	// M11: chat + sit/stand echo. The single-player echo path drops
	// packets back to the sender with the AID stamped as the GID — no
	// AOI broadcast yet (zone-side work in M14+).
	stageCZGlobalMessage(t, conn, dec, ioDeadline, aid)
	stageCZActionRequestSit(t, conn, dec, ioDeadline, aid)
	// M12: direction change + emotion echo. Same single-player
	// pattern — the gateway echoes the request back with the AID in
	// the srcId/GID slot.
	stageCZChangeDir(t, conn, dec, ioDeadline, aid)
	stageCZReqEmotion(t, conn, dec, ioDeadline, aid)
	// M13: name request + restart. Name request echoes the character
	// name when the GID matches the player's own CharID. Restart is
	// logged only (state transition to char select is deferred).
	stageCZGetCharNameRequest(t, conn, dec, ioDeadline, slot0GID, charName)
	stageCZRestart(t, conn, dec, ioDeadline)
	// M15: NPC dialog interaction. Click on Kafra Employee (GID 110000001),
	// verify dialog text + "Next" button, then continue to "Close" button,
	// then close the dialog.
	stageCZContactNPC(t, conn, dec, ioDeadline, 110000001, "Kafra Employee")
	stageCZReqNextScript(t, conn, dec, ioDeadline, 110000001)
	stageCZCloseDialog(t, conn, dec, ioDeadline, 110000001)
	// M16: NPC shop interaction. Click on Weapon Shop (GID 110000002),
	// verify ZC_SELECT_DEALTYPE, request the buy list, verify the 4-item
	// stock, submit a purchase for 1 Red Potion, verify the success
	// result, then close the deal window.
	stageCZContactNPCShop(t, conn, dec, ioDeadline, 110000002)
	stageCZAckSelectDealTypeBuy(t, conn, dec, ioDeadline, 110000002)
	stageCZPCPurchaseItemList(t, conn, dec, ioDeadline)
	stageCZAckSelectDealTypeCancel(t, conn, dec, ioDeadline, 110000002)
}

// cstrBytes returns the NUL-terminated prefix of b as a string, or the
// full slice if no NUL byte is present.
func cstrBytes(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		return string(b[:i])
	}
	return string(b)
}

// decodePosBytes unpacks a kRO 3-byte packed position (the same scheme
// as decodePos in pkg/ro/packet/coords.go, but inlined here to keep
// the e2e test self-contained against the net package).
func decodePosBytes(src []byte) (int16, int16, uint8) {
	x := int16((uint16(src[0]) << 2) | (uint16(src[1]) >> 6))
	y := int16((uint16(src[1]&0x3f) << 4) | (uint16(src[2]) >> 4))
	dir := src[2] & 0x0f
	return x, y, dir
}
