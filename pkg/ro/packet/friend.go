package packet

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Friend family for PACKETVER 20250604 (MAIN 20250604). Sources:
//   - third_party/rathena/src/map/clif_packetdb.hpp:257-263, :277 (C→S bindings;
//     0x0208 grows 11→14 at PACKETVER >= 20040705 — the reply is a uint32 there).
//   - third_party/rathena/src/map/packets.hpp:286-299 (ZC_FRIENDS_LIST),
//     :1911-1918 (ZC_FRIENDS_STATE, name field present ≥ 20180221).
//   - third_party/rathena/src/map/clif.cpp:15307 (toggle/state), :15355 (list),
//     :15397 (add result), :15417 (add request), :15432 (CZ add),
//     :15485 (CZ reply), :15554 (CZ remove).
//
// NOTE on the ≥ 20180221 shape: ZC_FRIENDS_LIST entries drop the name (the
// client resolves it from its local mob/char cache after ZC_FRIENDS_STATE);
// ZC_FRIENDS_STATE KEEPS its trailing name. Both gates are open at 20250604.
const (
	// C→S opcodes (clif_packetdb.hpp:257-263).
	HeaderCZFRIENDSADD    uint16 = 0x0202 // CZ_ADD_FRIENDS — clif_parse_FriendsListAdd
	HeaderCZFRIENDSDELETE uint16 = 0x0203 // CZ_DELETE_FRIENDS — clif_parse_FriendsListRemove
	HeaderCZFRIENDSREPLY  uint16 = 0x0208 // CZ_ACK_REQ_ADD_FRIENDS — clif_parse_FriendsListReply
	// S→C opcodes.
	HeaderZCFRIENDSLIST   uint16 = 0x0201 // ZC_FRIENDS_LIST (packets.hpp:299)
	HeaderZCFRIENDSSTATE  uint16 = 0x0206 // ZC_FRIENDS_STATE (packets.hpp:1918)
	HeaderZCREQADDFRIENDS uint16 = 0x0207 // ZC_REQ_ADD_FRIENDS (clif.cpp:15417)
	HeaderZCADDFRIENDS    uint16 = 0x0209 // ZC_ADD_FRIENDS (clif.cpp:15397)
	HeaderZCDELETEFRIENDS uint16 = 0x020a // ZC_DELETE_FRIENDS (clif.cpp:15586)
)

// On-wire byte sizes at PACKETVER 20250604.
const (
	sizeCZFriendsAdd    = 26 // int16 cmd + char name[24]
	sizeCZFriendsDelete = 10 // int16 cmd + uint32 AID + uint32 CID
	sizeCZFriendsReply  = 14 // int16 cmd + uint32 AID + uint32 CID + int32 reply (≥ 20040705)
	sizeZCFriendsState  = 35 // int16 cmd + uint32 AID + uint32 CID + uint8 offline + char name[24]
	sizeZCReqAddFriend  = 34 // int16 cmd + uint32 AID + uint32 CID + char name[24]
	sizeZCAddFriend     = 36 // int16 cmd + int16 result + uint32 AID + uint32 CID + char name[24]
	sizeZCDeleteFriends = 10 // int16 cmd + uint32 AID + uint32 CID
	// ZC_FRIENDS_LIST is variable: cmd(2) + packetLen(2) + N*8 {uint32 AID, uint32 CID}.
	sizeZCFriendsListHeader = 4
	sizeZCFriendsListSub    = 8
)

// ZC_ADD_FRIENDS result values (clif_friendslist_reqack, clif.cpp:15397-15401).
const (
	FriendAddOK            int16 = 0 // "You have become friends with (%s)."
	FriendAddRefused       int16 = 1 // "(%s) does not want to be friends with you."
	FriendAddOwnListFull   int16 = 2 // "Your Friend List is full."
	FriendAddTheirListFull int16 = 3 // "(%s)'s Friend List is full."
)

// CZ_ACK_REQ_ADD_FRIENDS reply values (clif_parse_FriendsListReply, clif.cpp:15487).
const (
	FriendReplyReject int32 = 0
	FriendReplyAccept int32 = 1
)

// CZFriendsAdd is a decoded CZ_ADD_FRIENDS frame: an add request addressed by
// the target's display name (clif_parse_FriendsListAdd resolves it with
// map_nick2sd).
type CZFriendsAdd struct {
	Name string
}

// ParseCZFriendsAdd decodes a CZ_ADD_FRIENDS (0x0202, 26B) frame.
func ParseCZFriendsAdd(frame []byte) (CZFriendsAdd, error) {
	if len(frame) < sizeCZFriendsAdd {
		return CZFriendsAdd{}, fmt.Errorf("packet: parse CZ_ADD_FRIENDS: want at least %d bytes, got %d", sizeCZFriendsAdd, len(frame))
	}
	if cmd := binary.LittleEndian.Uint16(frame[0:2]); cmd != HeaderCZFRIENDSADD {
		return CZFriendsAdd{}, fmt.Errorf("packet: parse CZ_ADD_FRIENDS: unexpected cmd 0x%04x", cmd)
	}
	return CZFriendsAdd{Name: readNameField(frame, 2, 24)}, nil
}

// Encode writes CZ_ADD_FRIENDS to w (used by the e2e harness).
func (m CZFriendsAdd) Encode(w io.Writer) error {
	var buf [sizeCZFriendsAdd]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderCZFRIENDSADD)
	writeNameField(buf[:], 2, m.Name)
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write CZ_ADD_FRIENDS: %w", err)
	}
	return nil
}

// CZFriendsDelete is a decoded CZ_DELETE_FRIENDS frame: the friend identified
// by the (AID, CID) pair.
type CZFriendsDelete struct {
	AID uint32
	CID uint32
}

// ParseCZFriendsDelete decodes a CZ_DELETE_FRIENDS (0x0203, 10B) frame.
func ParseCZFriendsDelete(frame []byte) (CZFriendsDelete, error) {
	if len(frame) < sizeCZFriendsDelete {
		return CZFriendsDelete{}, fmt.Errorf("packet: parse CZ_DELETE_FRIENDS: want at least %d bytes, got %d", sizeCZFriendsDelete, len(frame))
	}
	if cmd := binary.LittleEndian.Uint16(frame[0:2]); cmd != HeaderCZFRIENDSDELETE {
		return CZFriendsDelete{}, fmt.Errorf("packet: parse CZ_DELETE_FRIENDS: unexpected cmd 0x%04x", cmd)
	}
	return CZFriendsDelete{
		AID: binary.LittleEndian.Uint32(frame[2:6]),
		CID: binary.LittleEndian.Uint32(frame[6:10]),
	}, nil
}

// Encode writes CZ_DELETE_FRIENDS to w.
func (m CZFriendsDelete) Encode(w io.Writer) error {
	var buf [sizeCZFriendsDelete]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderCZFRIENDSDELETE)
	binary.LittleEndian.PutUint32(buf[2:], m.AID)
	binary.LittleEndian.PutUint32(buf[6:], m.CID)
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write CZ_DELETE_FRIENDS: %w", err)
	}
	return nil
}

// CZFriendsReply is a decoded CZ_ACK_REQ_ADD_FRIENDS frame: the invitee's
// answer to a ZC_REQ_ADD_FRIENDS. The inviter is identified by the (AID, CID)
// pair the invitation carried.
type CZFriendsReply struct {
	AID   uint32
	CID   uint32
	Reply int32
}

// ParseCZFriendsReply decodes a CZ_ACK_REQ_ADD_FRIENDS (0x0208, 14B) frame.
// The 11-byte variant (reply as int8) predates PACKETVER 6 and is not read.
func ParseCZFriendsReply(frame []byte) (CZFriendsReply, error) {
	if len(frame) < sizeCZFriendsReply {
		return CZFriendsReply{}, fmt.Errorf("packet: parse CZ_ACK_REQ_ADD_FRIENDS: want at least %d bytes, got %d", sizeCZFriendsReply, len(frame))
	}
	if cmd := binary.LittleEndian.Uint16(frame[0:2]); cmd != HeaderCZFRIENDSREPLY {
		return CZFriendsReply{}, fmt.Errorf("packet: parse CZ_ACK_REQ_ADD_FRIENDS: unexpected cmd 0x%04x", cmd)
	}
	return CZFriendsReply{
		AID:   binary.LittleEndian.Uint32(frame[2:6]),
		CID:   binary.LittleEndian.Uint32(frame[6:10]),
		Reply: int32(binary.LittleEndian.Uint32(frame[10:14])), //nolint:gosec // G115: wire int32.
	}, nil
}

// Encode writes CZ_ACK_REQ_ADD_FRIENDS to w.
func (m CZFriendsReply) Encode(w io.Writer) error {
	var buf [sizeCZFriendsReply]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderCZFRIENDSREPLY)
	binary.LittleEndian.PutUint32(buf[2:], m.AID)
	binary.LittleEndian.PutUint32(buf[6:], m.CID)
	binary.LittleEndian.PutUint32(buf[10:], uint32(m.Reply)) //nolint:gosec // G115: wire int32.
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write CZ_ACK_REQ_ADD_FRIENDS: %w", err)
	}
	return nil
}

// FriendsStateResponse encodes ZC_FRIENDS_STATE (0x0206, 35B) — one friend's
// online/offline toggle, sent to the client that owns the friend entry
// (clif_friendslist_toggle, clif.cpp:15307). Offline is 1 when the friend
// disconnected, 0 when it logged in.
type FriendsStateResponse struct {
	AID     uint32
	CID     uint32
	Offline bool
	Name    string
}

// Size returns ZC_FRIENDS_STATE frame length.
func (r FriendsStateResponse) Size() int { return sizeZCFriendsState }

// Encode writes ZC_FRIENDS_STATE to w.
func (r FriendsStateResponse) Encode(w io.Writer) error {
	var buf [sizeZCFriendsState]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCFRIENDSSTATE)
	binary.LittleEndian.PutUint32(buf[2:], r.AID)
	binary.LittleEndian.PutUint32(buf[6:], r.CID)
	if r.Offline {
		buf[10] = 1
	}
	writeNameField(buf[:], 11, r.Name)
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_FRIENDS_STATE: %w", err)
	}
	return nil
}

// FriendsReqAddResponse encodes ZC_REQ_ADD_FRIENDS (0x0207, 34B) — the
// invitation "X wants to add you as a friend", sent to the target
// (clif_friendlist_req, clif.cpp:15417).
type FriendsReqAddResponse struct {
	AID  uint32
	CID  uint32
	Name string
}

// Size returns ZC_REQ_ADD_FRIENDS frame length.
func (r FriendsReqAddResponse) Size() int { return sizeZCReqAddFriend }

// Encode writes ZC_REQ_ADD_FRIENDS to w.
func (r FriendsReqAddResponse) Encode(w io.Writer) error {
	var buf [sizeZCReqAddFriend]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCREQADDFRIENDS)
	binary.LittleEndian.PutUint32(buf[2:], r.AID)
	binary.LittleEndian.PutUint32(buf[6:], r.CID)
	writeNameField(buf[:], 10, r.Name)
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_REQ_ADD_FRIENDS: %w", err)
	}
	return nil
}

// FriendsAddResponse encodes ZC_ADD_FRIENDS (0x0209, 36B) — the outcome of a
// friend-add request, sent to the requester (clif_friendslist_reqack,
// clif.cpp:15397). Result values are the FriendAdd* constants.
type FriendsAddResponse struct {
	Result int16
	AID    uint32
	CID    uint32
	Name   string
}

// Size returns ZC_ADD_FRIENDS frame length.
func (r FriendsAddResponse) Size() int { return sizeZCAddFriend }

// Encode writes ZC_ADD_FRIENDS to w.
func (r FriendsAddResponse) Encode(w io.Writer) error {
	var buf [sizeZCAddFriend]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCADDFRIENDS)
	binary.LittleEndian.PutUint16(buf[2:], uint16(r.Result)) //nolint:gosec // G115: bounded by the four FriendAdd* constants.
	binary.LittleEndian.PutUint32(buf[4:], r.AID)
	binary.LittleEndian.PutUint32(buf[8:], r.CID)
	writeNameField(buf[:], 12, r.Name)
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_ADD_FRIENDS: %w", err)
	}
	return nil
}

// FriendsDeleteResponse encodes ZC_DELETE_FRIENDS (0x020a, 10B). The IDs
// written depend on the receiver (clif_parse_FriendsListRemove, clif.cpp:15586
// and :15609): the removed friend's client receives the REMOVER's (AID, CID);
// the remover's own client receives the REMOVED friend's (AID, CID).
type FriendsDeleteResponse struct {
	AID uint32
	CID uint32
}

// Size returns ZC_DELETE_FRIENDS frame length.
func (r FriendsDeleteResponse) Size() int { return sizeZCDeleteFriends }

// Encode writes ZC_DELETE_FRIENDS to w.
func (r FriendsDeleteResponse) Encode(w io.Writer) error {
	var buf [sizeZCDeleteFriends]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCDELETEFRIENDS)
	binary.LittleEndian.PutUint32(buf[2:], r.AID)
	binary.LittleEndian.PutUint32(buf[6:], r.CID)
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_DELETE_FRIENDS: %w", err)
	}
	return nil
}

// FriendsListResponse encodes ZC_FRIENDS_LIST (0x0201, variable: 4 + 8*N) —
// the whole friend list, sent during the LoadEndAck burst
// (clif_friendslist_send, clif.cpp:15355). Entries carry (AID, CID) only; the
// name field was dropped at PACKETVER ≥ 20180221 and each online friend is
// announced separately by ZC_FRIENDS_STATE immediately after this burst.
type FriendsListResponse struct {
	Friends []FriendsListEntry
}

// FriendsListEntry is one roster entry: the friend's account and char ids.
type FriendsListEntry struct {
	AID uint32
	CID uint32
}

// Size returns the on-wire byte length Encode will write (4 + 8*N, capped at
// MaxFriends).
func (r FriendsListResponse) Size() int {
	n := len(r.Friends)
	if n > MaxFriends {
		n = MaxFriends
	}
	return sizeZCFriendsListHeader + sizeZCFriendsListSub*n
}

// Encode writes ZC_FRIENDS_LIST to w. The list is capped at MaxFriends so an
// over-long roster cannot produce a frame the client mis-parses.
func (r FriendsListResponse) Encode(w io.Writer) error {
	friends := r.Friends
	if len(friends) > MaxFriends {
		friends = friends[:MaxFriends]
	}
	buf := make([]byte, sizeZCFriendsListHeader+sizeZCFriendsListSub*len(friends))
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCFRIENDSLIST)
	binary.LittleEndian.PutUint16(buf[2:], uint16(len(buf))) //nolint:gosec // G115: bounded by MaxFriends.
	off := sizeZCFriendsListHeader
	for _, f := range friends {
		binary.LittleEndian.PutUint32(buf[off:], f.AID)
		binary.LittleEndian.PutUint32(buf[off+4:], f.CID)
		off += sizeZCFriendsListSub
	}
	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write ZC_FRIENDS_LIST: %w", err)
	}
	return nil
}

// MaxFriends is rAthena's MAX_FRIENDS (src/common/mmo.hpp:168).
const MaxFriends = 40
