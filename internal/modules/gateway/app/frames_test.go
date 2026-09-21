package app_test

// Framing authority for the gateway end-to-end suite.
//
// Every frame reader in this package (the drain/scan helpers in the
// integration tests, and the deterministic tests below) resolves a frame's
// on-wire length through serverFrames — the same packet database the map
// server itself dispatches on. Sizes therefore cannot drift from the wire.
//
// That drift is what this file replaces: the suite previously carried a
// hand-maintained table of six opcodes (fixedReplyLen) for the scanning
// readers, and most readers simply trusted bytes [2:4] as a length slot.
// Neither is sound. Only variable-length frames carry a length slot there;
// a fixed frame's bytes [2:4] are payload. ZC_ACCEPT_ENTER (13B) is the
// clearest case — offset 2 is startTime (pkg/ro/packet/map_encode.go:46-54),
// so reading it as a length yields whatever the clock said.
//
// The consequence was a silent desync: the reader consumed the wrong number
// of bytes and then reported a downstream artefact as the failure
// ("frame 0x0096 bad length 0" — 0x0096 is the client→server CZ_WHISPER, so
// a byte offset was being read as an opcode). serverFrameSize refuses a
// client→server opcode outright, so the same stream now fails at the frame
// that is actually wrong.
//
// This file is deliberately untagged: it compiles under both `-tags=unit`
// (the fast gate) and `-tags=integration` (the e2e gate), so the
// deterministic proofs below run in both.

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	ropacket "github.com/bouroo/goAthena/pkg/ro/packet"
)

// serverFrames is the wire-size authority for this suite — the map server's own
// dispatch database, so the harness and the server cannot disagree.
var serverFrames = ropacket.NewMapServerDB()

// serverFrameSize resolves the total on-wire length of a server→client frame.
//
// It returns an error for an opcode the database does not know, and for one
// that is a client→server opcode: a client frame cannot appear in a
// server→client stream, so encountering one means the reader is desynced, and
// naming that beats framing the wrong bytes and failing somewhere downstream.
//
// ropacket.VariableLength (-1) means the frame carries its own length slot.
func serverFrameSize(cmd uint16) (int, error) {
	def, ok := serverFrames.Lookup(cmd)
	if !ok {
		return 0, fmt.Errorf("opcode 0x%04x is not in the map-server packet DB", cmd)
	}
	if def.Direction != ropacket.DirectionServerToClient {
		return 0, fmt.Errorf("opcode 0x%04x is %s (client→server), not a server frame: the stream is desynced", cmd, def.Name)
	}
	return def.Length, nil
}

// frameSize is serverFrameSize for readers that treat an unframeable opcode as
// a hard harness failure rather than something to recover from.
func frameSize(t *testing.T, cmd uint16) int {
	t.Helper()
	size, err := serverFrameSize(cmd)
	if err != nil {
		t.Fatalf("cannot frame 0x%04x: %v", cmd, err)
	}
	return size
}

// readServerFrame reads exactly one server→client frame from c and returns its
// opcode and raw bytes. I/O failures (including the deadline) are returned;
// an unframeable opcode is fatal, because no reader can continue past it.
func readServerFrame(t *testing.T, c net.Conn, within time.Duration) (uint16, []byte, error) {
	t.Helper()
	if err := c.SetReadDeadline(time.Now().Add(within)); err != nil {
		return 0, nil, fmt.Errorf("set read deadline: %w", err)
	}
	frame := make([]byte, 2)
	if _, err := io.ReadFull(c, frame); err != nil {
		return 0, nil, err //nolint:wrapcheck // test helper: the caller reports the read that failed
	}
	cmd := binary.LittleEndian.Uint16(frame)
	size := frameSize(t, cmd)
	consumed := 2
	if size == ropacket.VariableLength {
		slot := make([]byte, 2)
		if _, err := io.ReadFull(c, slot); err != nil {
			return 0, nil, err //nolint:wrapcheck // ditto
		}
		frame = append(frame, slot...)
		consumed = 4
		size = int(binary.LittleEndian.Uint16(slot))
		if size < consumed {
			t.Fatalf("variable frame 0x%04x declares length %d, want >= %d", cmd, size, consumed)
		}
	}
	if rest := size - consumed; rest > 0 {
		body := make([]byte, rest)
		if _, err := io.ReadFull(c, body); err != nil {
			return 0, nil, fmt.Errorf("read body of frame 0x%04x (declared %dB): %w", cmd, size, err)
		}
		frame = append(frame, body...)
	}
	return cmd, frame, nil
}

// nextServerFrame reads the next frame and fails the test if it cannot. It
// never skips: callers that need to search a stream use scanServerFrame.
func nextServerFrame(t *testing.T, c net.Conn, within time.Duration) (uint16, []byte) {
	t.Helper()
	cmd, frame, err := readServerFrame(t, c, within)
	if err != nil {
		t.Fatalf("read next server frame: %v", err)
	}
	return cmd, frame
}

// scanServerFrame reads frames until one carries the wanted opcode, failing
// after within. Every frame it passes is framed by its true size, so an
// unexpected frame (a peer's spawn-unit, a mob's walk) is skipped instead of
// desyncing the scan.
func scanServerFrame(t *testing.T, c net.Conn, want uint16, within time.Duration) []byte {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			t.Fatalf("no frame 0x%04x within %s", want, within)
		}
		cmd, frame, err := readServerFrame(t, c, remaining)
		if err != nil {
			t.Fatalf("no frame 0x%04x within %s: %v", want, within, err)
		}
		if cmd == want {
			return frame
		}
	}
}

// awaitAcceptEnter consumes frames until the connection's ZC_ACCEPT_ENTER
// (0x02eb, 13B) arrives, and returns it.
//
// It scans rather than reading a bare 13 bytes because the accept-enter is not
// guaranteed to be frame #1: a peer's dispatch goroutine can deliver a
// ZC_SPAWN_UNIT to this connection, and a bare 13-byte read would then consume
// 13 bytes of a 107-byte frame and desync everything after it. The gateway
// orders its own writes so that the accept-enter comes first (handleEnter
// submits it before registering the connection as a broadcast target); this
// helper is what makes the suite correct whether or not that holds.
func awaitAcceptEnter(t *testing.T, c net.Conn) []byte {
	t.Helper()
	frame := scanServerFrame(t, c, ropacket.HeaderZCACCEPTENTER, 3*time.Second)
	if len(frame) != 13 {
		t.Fatalf("ZC_ACCEPT_ENTER length = %d, want 13", len(frame))
	}
	return frame
}

// encoder is the subset of the packet encoders this file needs to build real
// frames (as opposed to hand-assembled byte slices).
type encoder interface {
	Encode(w io.Writer) error
}

// encodeFrame encodes a real packet into a fresh byte slice.
func encodeFrame(t *testing.T, enc encoder, what string) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := enc.Encode(&buf); err != nil {
		t.Fatalf("encode %s: %v", what, err)
	}
	return buf.Bytes()
}

// spawnUnitFrame builds a real ZC_SPAWN_UNIT (107B) for the framing tests.
func spawnUnitFrame(t *testing.T, gid uint32, name string) []byte {
	t.Helper()
	return encodeFrame(t, ropacket.SpawnUnitResponse{ObjectType: 0, AID: gid, GID: gid, Name: name}, "ZC_SPAWN_UNIT")
}

// acceptEnterFrame builds a real ZC_ACCEPT_ENTER (13B) for the framing tests.
func acceptEnterFrame(t *testing.T) []byte {
	t.Helper()
	return encodeFrame(t, ropacket.MapAcceptEnterResponse{PosX: 100, PosY: 100, XSize: 5, YSize: 5}, "ZC_ACCEPT_ENTER")
}

// TestServerFrameSize_ReadsThePacketDB pins the sizes the readers depend on to
// the values the database carries, so a drifting table cannot silently return.
func TestServerFrameSize_ReadsThePacketDB(t *testing.T) {
	for _, tc := range []struct {
		name string
		cmd  uint16
		want int
	}{
		{"ZC_ACCEPT_ENTER", ropacket.HeaderZCACCEPTENTER, 13},
		{"ZC_SPAWN_UNIT", ropacket.HeaderZCSPAWNUNIT, 107},
		// Registered in Phase 43: the map server has always emitted this on a
		// warp portal, but the DB never declared it, so the warp test's reader
		// could not frame the one frame that test exists to assert.
		{"ZC_NPCACK_MAPMOVE", ropacket.HeaderZCNPCACKMAPMOVE, 22},
		{"ZC_PAR_CHANGE", ropacket.HeaderZCPARCHANGE, 8},
		{"ZC_NOTIFY_TIME", ropacket.HeaderZCNOTIFYTIME, 6},
		{"ZC_EMOTION", ropacket.HeaderZCEMOTION, 7},
		{"ZC_SETTING_WHISPER_PC", ropacket.HeaderZCSETTINGWHISPERPC, 4},
		{"ZC_WHISPER_LIST", ropacket.HeaderZCWHISPERLIST, ropacket.VariableLength},
		{"ZC_NOTIFY_CHAT", ropacket.HeaderZCNOTIFYCHAT, ropacket.VariableLength},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := serverFrameSize(tc.cmd)
			if err != nil {
				t.Fatalf("serverFrameSize(0x%04x): %v", tc.cmd, err)
			}
			if got != tc.want {
				t.Fatalf("serverFrameSize(0x%04x) = %d, want %d", tc.cmd, got, tc.want)
			}
		})
	}
}

// TestServerFrameSize_ClientOpcodeIsADesync: a client→server opcode in a
// server→client stream is a desync, and the harness must refuse it rather than
// frame the wrong bytes. 0x0096 (CZ_WHISPER) is exactly the opcode the old
// readers used to report — ninety-odd bytes after the stream had already gone
// wrong.
func TestServerFrameSize_ClientOpcodeIsADesync(t *testing.T) {
	_, err := serverFrameSize(ropacket.HeaderCZWHISPER)
	if err == nil {
		t.Fatalf("serverFrameSize(CZ_WHISPER 0x%04x) accepted a client→server opcode, want a desync error", ropacket.HeaderCZWHISPER)
	}
	if !bytes.Contains([]byte(err.Error()), []byte("desynced")) {
		t.Fatalf("error = %q, want it to name the desync", err)
	}

	if _, err := serverFrameSize(0xffff); err == nil {
		t.Fatalf("serverFrameSize(0xffff) accepted an opcode absent from the packet DB")
	}
}

// TestReadServerFrame_FixedFrameHasNoLengthSlot is the drift proof.
//
// ZC_PAR_CHANGE is 8 bytes and writes VarID at offset 2 (map_encode.go:824-833).
// A reader that trusts bytes [2:4] as a length reads a 5-byte frame here, leaves
// three bytes of it unread, and then reads those leftovers as the next opcode —
// the desync class this file exists to remove.
//
// The assert reads the frame *boundaries*, not merely that no error occurred: a
// reader that got the boundary wrong fails on the size and on the next opcode.
func TestReadServerFrame_FixedFrameHasNoLengthSlot(t *testing.T) {
	// SPHP = 5: the VarID value is what an offset-2 reader would mistake for a
	// 5-byte frame length.
	par := ropacket.ParChangeResponse{VarID: ropacket.SPHP, Count: 42}
	first := encodeFrame(t, par, "ZC_PAR_CHANGE")
	timeFrame := make([]byte, 6)
	binary.LittleEndian.PutUint16(timeFrame[0:], ropacket.HeaderZCNOTIFYTIME)
	binary.LittleEndian.PutUint32(timeFrame[2:], 987654)

	client, server := net.Pipe()
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()
	go func() { _, _ = server.Write(append(append([]byte{}, first...), timeFrame...)) }()

	cmd1, got1 := nextServerFrame(t, client, 2*time.Second)
	if cmd1 != ropacket.HeaderZCPARCHANGE {
		t.Fatalf("frame 1 opcode = 0x%04x, want 0x%04x (ZC_PAR_CHANGE)", cmd1, ropacket.HeaderZCPARCHANGE)
	}
	if len(got1) != 8 {
		t.Fatalf("frame 1 length = %d, want 8 (a reader trusting bytes [2:4] reads %d)", len(got1), par.VarID)
	}
	if got := binary.LittleEndian.Uint16(got1[2:4]); got != ropacket.SPHP {
		t.Fatalf("frame 1 VarID = %d, want %d", got, ropacket.SPHP)
	}

	cmd2, got2 := nextServerFrame(t, client, 2*time.Second)
	if cmd2 != ropacket.HeaderZCNOTIFYTIME {
		t.Fatalf("frame 2 opcode = 0x%04x, want 0x%04x (ZC_NOTIFY_TIME)", cmd2, ropacket.HeaderZCNOTIFYTIME)
	}
	if len(got2) != 6 {
		t.Fatalf("frame 2 length = %d, want 6", len(got2))
	}
	if got := binary.LittleEndian.Uint32(got2[2:6]); got != 987654 {
		t.Fatalf("frame 2 time = %d, want 987654", got)
	}
}

// TestReadServerFrame_VariableFrameUsesItsSlot: variable-length frames do carry
// a length slot, and the reader must use it (the fixed case above must not be
// generalized into "never trust offset 2").
func TestReadServerFrame_VariableFrameUsesItsSlot(t *testing.T) {
	list := ropacket.EncodeEmptyInventoryListNormal()
	if len(list) < 4 {
		t.Fatalf("empty inventory list frame = %d bytes, want >= 4", len(list))
	}
	client, server := net.Pipe()
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()
	go func() { _, _ = server.Write(list) }()

	cmd, got := nextServerFrame(t, client, 2*time.Second)
	if cmd != ropacket.HeaderZCINVENTORYITEMLISTNORMAL {
		t.Fatalf("opcode = 0x%04x, want 0x%04x", cmd, ropacket.HeaderZCINVENTORYITEMLISTNORMAL)
	}
	if len(got) != len(list) {
		t.Fatalf("length = %d, want %d (the declared slot)", len(got), len(list))
	}
}

// TestScanServerFrame_PastAMisorderedSpawnUnit is the misorder proof: a
// ZC_SPAWN_UNIT that reached the connection ahead of its accept-enter must not
// cost the reader the accept-enter that follows it.
func TestScanServerFrame_PastAMisorderedSpawnUnit(t *testing.T) {
	spawn := spawnUnitFrame(t, 150001, "Peer")
	accept := acceptEnterFrame(t)
	if binary.LittleEndian.Uint16(spawn[0:2]) != ropacket.HeaderZCSPAWNUNIT {
		t.Fatalf("built frame is not a spawn-unit")
	}

	client, server := net.Pipe()
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()
	go func() { _, _ = server.Write(append(append([]byte{}, spawn...), accept...)) }()

	got := awaitAcceptEnter(t, client)
	if len(got) != 13 {
		t.Fatalf("accept-enter length = %d, want 13", len(got))
	}
	if binary.LittleEndian.Uint16(got[0:2]) != ropacket.HeaderZCACCEPTENTER {
		t.Fatalf("opcode = 0x%04x, want 0x%04x", binary.LittleEndian.Uint16(got[0:2]), ropacket.HeaderZCACCEPTENTER)
	}
}
