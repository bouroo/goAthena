package packet

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// TestFriendsAddResponse_Encode pins the ZC_ADD_FRIENDS (0x0209, 36B) wire
// layout: cmd + result.W + AID.L + CID.L + name[24] (clif.cpp:15397).
func TestFriendsAddResponse_Encode(t *testing.T) {
	resp := FriendsAddResponse{Result: FriendAddOK, AID: 2000001, CID: 150001, Name: "Hero"}
	var buf bytes.Buffer
	if err := resp.Encode(&buf); err != nil {
		t.Fatalf("encode: %v", err)
	}
	out := buf.Bytes()
	if binary.LittleEndian.Uint16(out[0:]) != HeaderZCADDFRIENDS {
		t.Errorf("cmd = 0x%04x, want 0x%04x", binary.LittleEndian.Uint16(out[0:]), HeaderZCADDFRIENDS)
	}
	if result := int16(binary.LittleEndian.Uint16(out[2:])); result != FriendAddOK {
		t.Errorf("result = %d, want 0", result)
	}
	if aid := binary.LittleEndian.Uint32(out[4:]); aid != 2000001 {
		t.Errorf("AID = %d, want 2000001", aid)
	}
	if cid := binary.LittleEndian.Uint32(out[8:]); cid != 150001 {
		t.Errorf("CID = %d, want 150001", cid)
	}
	if name := readNameField(out, 12, 24); name != "Hero" {
		t.Errorf("name = %q, want %q", name, "Hero")
	}
}

// TestFriendsListResponse_Encode pins the ZC_FRIENDS_LIST (0x0201) variable
// layout: entries are (AID, CID) pairs with NO name at PACKETVER >= 20180221
// (packets.hpp:286-292), and the declared length covers the whole frame.
func TestFriendsListResponse_Encode(t *testing.T) {
	resp := FriendsListResponse{Friends: []FriendsListEntry{
		{AID: 2000001, CID: 150001},
		{AID: 2000002, CID: 150002},
	}}
	if resp.Size() != 4+2*sizeZCFriendsListSub {
		t.Fatalf("Size = %d, want %d", resp.Size(), 4+2*sizeZCFriendsListSub)
	}
	var buf bytes.Buffer
	if err := resp.Encode(&buf); err != nil {
		t.Fatalf("encode: %v", err)
	}
	out := buf.Bytes()
	if got := binary.LittleEndian.Uint16(out[2:]); int(got) != len(out) {
		t.Errorf("declared len = %d, want %d", got, len(out))
	}
	if binary.LittleEndian.Uint32(out[4:]) != 2000001 || binary.LittleEndian.Uint32(out[8:]) != 150001 {
		t.Errorf("entry 0 = (%d,%d)", binary.LittleEndian.Uint32(out[4:]), binary.LittleEndian.Uint32(out[8:]))
	}
	// 20180221 shape: entries are exactly 8 bytes — no name tail.
	if len(out) != 4+2*8 {
		t.Errorf("frame = %d bytes, want %d (AID+CID only)", len(out), 4+2*8)
	}
}

// TestFriendsStateResponse_Encode pins ZC_FRIENDS_STATE (0x0206, 35B): the
// offline byte is inverted (1 = offline) and the name field IS present at
// PACKETVER >= 20180221 (packets.hpp:1911-1918).
func TestFriendsStateResponse_Encode(t *testing.T) {
	resp := FriendsStateResponse{AID: 2000002, CID: 150002, Offline: true, Name: "Partner"}
	var buf bytes.Buffer
	if err := resp.Encode(&buf); err != nil {
		t.Fatalf("encode: %v", err)
	}
	out := buf.Bytes()
	if out[10] != 1 {
		t.Errorf("offline byte = %d, want 1", out[10])
	}
	if name := readNameField(out, 11, 24); name != "Partner" {
		t.Errorf("name = %q, want %q", name, "Partner")
	}
}

// TestCZFriendsReply_RoundTrip pins CZ_ACK_REQ_ADD_FRIENDS (0x0208, 14B) with
// the uint32 reply field (PACKETVER >= 20040705, clif_packetdb.hpp:277).
func TestCZFriendsReply_RoundTrip(t *testing.T) {
	req := CZFriendsReply{AID: 2000001, CID: 150001, Reply: FriendReplyAccept}
	var buf bytes.Buffer
	if err := req.Encode(&buf); err != nil {
		t.Fatalf("encode: %v", err)
	}
	got, err := ParseCZFriendsReply(buf.Bytes())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got != req {
		t.Errorf("round trip = %+v, want %+v", got, req)
	}
}
