package packet

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Party family for PACKETVER 20250604 (MAIN 20250604). Every gate cited below
// is open at that version. Sources:
//   - third_party/rathena/src/map/clif_packetdb.hpp:98-106 (C→S bindings).
//   - third_party/rathena/src/map/packets.hpp:1343-1366, :1929-1941 (C→S structs).
//   - third_party/rathena/src/map/packets_struct.hpp:2050-2091 (S→C member/roster
//     structs), :5076-5101 (create/invite ack), :5158-5164 (withdraw).
//   - third_party/rathena/src/map/clif.cpp:7792 (created), :7803 (member info),
//     :7840 (roster), :7924 (invite), :8025 (withdraw) — the encoders mirrored.
//
// NOTE on the S→C opcode band: at PACKETVER >= 20171207 the roster/member
// packets switch to 0x0ae5/0x0ae4 (packets_struct.hpp:274-276). The older
// 0x014a/0x0104/0x01e9/0x0a43 variants are NOT sent at 20250604.
const (
	// C→S opcodes (clif_packetdb.hpp:98-106).
	HeaderCZMAKEGROUP           uint16 = 0x00f9 // CZ_MAKE_GROUP — clif_parse_CreateParty
	HeaderCZMAKEGROUP2          uint16 = 0x01e8 // CZ_MAKE_GROUP2 — clif_parse_CreateParty2 (adds item rules)
	HeaderCZREQJOINGROUP        uint16 = 0x00fc // CZ_REQ_JOIN_GROUP — clif_parse_PartyInvite (by account id)
	HeaderCZJOINGROUP           uint16 = 0x00ff // CZ_JOIN_GROUP — clif_parse_ReplyPartyInvite
	HeaderCZREQLEAVEGROUP       uint16 = 0x0100 // CZ_REQ_LEAVE_GROUP — clif_parse_LeaveParty
	HeaderCZCHANGEGROUPEXPOPT   uint16 = 0x0102 // CZ_CHANGE_GROUPEXPOPTION — clif_parse_PartyChangeOption
	HeaderCZREQEXPELGROUPMEMBER uint16 = 0x0103 // CZ_REQ_EXPEL_GROUP_MEMBER — clif_parse_RemovePartyMember
	// S→C opcodes.
	HeaderZCACKMAKEGROUP        uint16 = 0x00fa // ZC_ACK_MAKE_GROUP (packets_struct.hpp:5080)
	HeaderZCPARTYJOINREQ        uint16 = 0x02c6 // ZC_PARTY_JOIN_REQ (packets_struct.hpp:5090, >=20070821)
	HeaderZCPARTYJOINREQACK     uint16 = 0x02c5 // ZC_PARTY_JOIN_REQ_ACK (packets_struct.hpp:5101, >=20070821)
	HeaderZCPARTYCONFIG         uint16 = 0x02c9 // ZC_PARTY_CONFIG (packets_struct.hpp:4040)
	HeaderZCGROUPLIST           uint16 = 0x0ae5 // ZC_GROUP_LIST (packets_struct.hpp:276, >=20171207)
	HeaderZCADDMEMBERTOGROUP    uint16 = 0x0ae4 // ZC_ADD_MEMBER_TO_GROUP (packets_struct.hpp:275, >=20171207)
	HeaderZCDELETEMEMBERFROMGRP uint16 = 0x0105 // ZC_DELETE_MEMBER_FROM_GROUP (packets_struct.hpp:5164)
)

// On-wire byte sizes at PACKETVER 20250604.
const (
	sizeCZMakeGroup       = 26 // int16 cmd + char name[24] (packets.hpp:1929)
	sizeCZMakeGroup2      = 28 // int16 cmd + name[24] + uint8 item_pickup + uint8 item_share
	sizeCZReqJoinGroup    = 6  // int16 cmd + uint32 AID
	sizeCZJoinGroup       = 10 // int16 cmd + uint32 party_id + int32 flag
	sizeCZReqLeaveGroup   = 2  // int16 cmd only
	sizeCZChangeGroupExp  = 6  // int16 cmd + int32 expflag (clif_packetdb.hpp:102 binds 0x0102,6)
	sizeCZReqExpelMember  = 30 // int16 cmd + uint32 AID + char name[24]
	sizeZCDeleteMember    = 31 // int16 cmd + int32 AID + char characterName[24] + int8 result
	sizeZCPartyJoinReq    = 30 // int16 cmd + int GRID + char groupName[24]
	sizeZCPartyJoinReqAck = 30 // int16 cmd + char characterName[24] + int result
	sizeZCGroupListSub    = 54 // uint32 AID + uint32 GID + char playerName[24] + char mapName[16] + uint8 leader + uint8 offline + int16 class + int16 baseLevel
	sizeZCAckMakeGroup    = 3  // int16 cmd + int8 result
	sizeZCPartyConfig     = 3  // int16 cmd + uint8 denyPartyInvites
	// sizeZCAddMemberToGroup = cmd(2) + AID(4) + GID(4) + leader(4) + class(2) +
	// baseLevel(2) + x(2) + y(2) + offline(1) + partyName(24) + playerName(24) +
	// mapName(16) + sharePickup(1) + shareLoot(1) = 89.
	sizeZCAddMemberToGroup = 89
	// ZC_GROUP_LIST is variable: cmd(2) + packetLen(2) + partyName(24) + N*54.
	sizeZCGroupListHeader = 28
)

// ZC_ACK_MAKE_GROUP result values (src/map/party.cpp:156,:183,:193 — the
// clif_party_created argument). 0 is success; 2 means the char already belongs
// to a party; 1 is a duplicate party name.
const (
	PartyCreateOK             uint8 = 0
	PartyCreateNameExists     uint8 = 1
	PartyCreateAlreadyInParty uint8 = 2
)

// ZC_PARTY_JOIN_REQ_ACK result values (e_party_invite_reply, clif.hpp:161-171).
// CZ_JOIN_GROUP's own flag is a raw 0=reject / 1=accept, NOT these values.
const (
	PartyReplyJoinOtherParty int32 = 0 // already joined another party
	PartyReplyRejected       int32 = 1
	PartyReplyAccepted       int32 = 2
	PartyReplyFull           int32 = 3
	PartyReplyDual           int32 = 4 // same account already in the party
	PartyReplyJoinMsgRefuse  int32 = 5
	PartyReplyUnknownError   int32 = 6
	PartyReplyOffline        int32 = 7
)

// CZ_JOIN_GROUP flag (clif.cpp:13876-13878).
const (
	PartyJoinReject int32 = 0
	PartyJoinAccept int32 = 1
)

// ZC_DELETE_MEMBER_FROM_GROUP result values (e_party_member_withdraw,
// mmo.hpp:1143-1147).
const (
	PartyWithdrawLeave     int8 = 0
	PartyWithdrawExpel     int8 = 1
	PartyWithdrawCantLeave int8 = 2
	PartyWithdrawCantExpel int8 = 3
)

// --- C→S parsers (+ Encode, used by the e2e harness) ---

// CZMakeGroup is a decoded CZ_MAKE_GROUP / CZ_MAKE_GROUP2 frame. MakeGroup2
// carries the item rules; MakeGroup leaves them zero (clif_parse_CreateParty
// passes 0,0 — src/map/clif.cpp:13816).
type CZMakeGroup struct {
	Name       string
	ItemPickup uint8
	ItemShare  uint8
}

// ParseCZMakeGroup decodes a CZ_MAKE_GROUP (0x00f9, 26B) frame.
func ParseCZMakeGroup(frame []byte) (CZMakeGroup, error) {
	return parseMakeGroup(frame, HeaderCZMAKEGROUP, sizeCZMakeGroup)
}

// ParseCZMakeGroup2 decodes a CZ_MAKE_GROUP2 (0x01e8, 28B) frame.
func ParseCZMakeGroup2(frame []byte) (CZMakeGroup, error) {
	return parseMakeGroup(frame, HeaderCZMAKEGROUP2, sizeCZMakeGroup2)
}

func parseMakeGroup(frame []byte, want uint16, size int) (CZMakeGroup, error) {
	if len(frame) < size {
		return CZMakeGroup{}, fmt.Errorf("packet: parse party create: want at least %d bytes, got %d", size, len(frame))
	}
	if cmd := binary.LittleEndian.Uint16(frame[0:2]); cmd != want {
		return CZMakeGroup{}, fmt.Errorf("packet: parse party create: unexpected cmd 0x%04x", cmd)
	}
	out := CZMakeGroup{Name: readNameField(frame, 2, 24)}
	if size == sizeCZMakeGroup2 {
		out.ItemPickup = frame[26]
		out.ItemShare = frame[27]
	}
	return out, nil
}

// Encode writes the CZ_MAKE_GROUP frame to w.
func (m CZMakeGroup) Encode(w io.Writer) error {
	var buf [sizeCZMakeGroup]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderCZMAKEGROUP)
	writeNameField(buf[:], 2, m.Name)
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write CZ_MAKE_GROUP: %w", err)
	}
	return nil
}

// CZReqJoinGroup is a decoded CZ_REQ_JOIN_GROUP frame: an invite addressed by the
// target's account id (clif_parse_PartyInvite resolves it via map_id2sd).
type CZReqJoinGroup struct {
	AID uint32
}

// ParseCZReqJoinGroup decodes a CZ_REQ_JOIN_GROUP (0x00fc, 6B) frame.
func ParseCZReqJoinGroup(frame []byte) (CZReqJoinGroup, error) {
	if len(frame) < sizeCZReqJoinGroup {
		return CZReqJoinGroup{}, fmt.Errorf("packet: parse CZ_REQ_JOIN_GROUP: want at least %d bytes, got %d", sizeCZReqJoinGroup, len(frame))
	}
	if cmd := binary.LittleEndian.Uint16(frame[0:2]); cmd != HeaderCZREQJOINGROUP {
		return CZReqJoinGroup{}, fmt.Errorf("packet: parse CZ_REQ_JOIN_GROUP: unexpected cmd 0x%04x", cmd)
	}
	return CZReqJoinGroup{AID: binary.LittleEndian.Uint32(frame[2:6])}, nil
}

// Encode writes the CZ_REQ_JOIN_GROUP frame to w.
func (r CZReqJoinGroup) Encode(w io.Writer) error {
	var buf [sizeCZReqJoinGroup]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderCZREQJOINGROUP)
	binary.LittleEndian.PutUint32(buf[2:], r.AID)
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write CZ_REQ_JOIN_GROUP: %w", err)
	}
	return nil
}

// CZJoinGroup is a decoded CZ_JOIN_GROUP frame: the invitee's reply. Flag is a
// raw 0=reject / 1=accept (PartyJoinReject / PartyJoinAccept).
type CZJoinGroup struct {
	PartyID uint32
	Flag    int32
}

// ParseCZJoinGroup decodes a CZ_JOIN_GROUP (0x00ff, 10B) frame.
func ParseCZJoinGroup(frame []byte) (CZJoinGroup, error) {
	if len(frame) < sizeCZJoinGroup {
		return CZJoinGroup{}, fmt.Errorf("packet: parse CZ_JOIN_GROUP: want at least %d bytes, got %d", sizeCZJoinGroup, len(frame))
	}
	if cmd := binary.LittleEndian.Uint16(frame[0:2]); cmd != HeaderCZJOINGROUP {
		return CZJoinGroup{}, fmt.Errorf("packet: parse CZ_JOIN_GROUP: unexpected cmd 0x%04x", cmd)
	}
	return CZJoinGroup{
		PartyID: binary.LittleEndian.Uint32(frame[2:6]),
		Flag:    int32(binary.LittleEndian.Uint32(frame[6:10])), //nolint:gosec // G115: wire int32.
	}, nil
}

// Encode writes the CZ_JOIN_GROUP frame to w.
func (r CZJoinGroup) Encode(w io.Writer) error {
	var buf [sizeCZJoinGroup]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderCZJOINGROUP)
	binary.LittleEndian.PutUint32(buf[2:], r.PartyID)
	binary.LittleEndian.PutUint32(buf[6:], uint32(r.Flag)) //nolint:gosec // G115: wire int32; r.Flag is int32.
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write CZ_JOIN_GROUP: %w", err)
	}
	return nil
}

// EncodeCZReqLeaveGroup writes the CZ_REQ_LEAVE_GROUP frame to w (a bare 2-byte
// cmd; there is no body to parse — the handler validates the cmd only).
func EncodeCZReqLeaveGroup(w io.Writer) error {
	var buf [sizeCZReqLeaveGroup]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderCZREQLEAVEGROUP)
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write CZ_REQ_LEAVE_GROUP: %w", err)
	}
	return nil
}

// PartyConfigResponse encodes ZC_PARTY_CONFIG (0x02c9, 3B) — the invite-denial
// flag the client shows as "refuse party invites". rAthena sends it during the
// login/load burst (src/map/clif.cpp:7912, reached from clif_parse_LoadEndAck).
// Deny=0 allows invitations, 1 auto-denies.
type PartyConfigResponse struct {
	Deny uint8
}

// Size returns the ZC_PARTY_CONFIG frame length.
func (r PartyConfigResponse) Size() int { return sizeZCPartyConfig }

// Encode writes ZC_PARTY_CONFIG to w.
func (r PartyConfigResponse) Encode(w io.Writer) error {
	var buf [sizeZCPartyConfig]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCPARTYCONFIG)
	buf[2] = r.Deny
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_PARTY_CONFIG: %w", err)
	}
	return nil
}

// CZChangeGroupExpOption is a decoded CZ_CHANGE_GROUPEXPOPTION frame: the
// leader's new exp-share rule. Only the exp flag is client-settable on 0x0102 —
// the item rules ride the separate CZ_GROUPINFO_CHANGE_V2, which this slice does
// not implement (clif.cpp:13956).
type CZChangeGroupExpOption struct {
	ExpFlag int32
}

// ParseCZChangeGroupExpOption decodes a CZ_CHANGE_GROUPEXPOPTION (0x0102, 6B) frame.
func ParseCZChangeGroupExpOption(frame []byte) (CZChangeGroupExpOption, error) {
	if len(frame) < sizeCZChangeGroupExp {
		return CZChangeGroupExpOption{}, fmt.Errorf("packet: parse CZ_CHANGE_GROUPEXPOPTION: want at least %d bytes, got %d", sizeCZChangeGroupExp, len(frame))
	}
	if cmd := binary.LittleEndian.Uint16(frame[0:2]); cmd != HeaderCZCHANGEGROUPEXPOPT {
		return CZChangeGroupExpOption{}, fmt.Errorf("packet: parse CZ_CHANGE_GROUPEXPOPTION: unexpected cmd 0x%04x", cmd)
	}
	return CZChangeGroupExpOption{
		ExpFlag: int32(binary.LittleEndian.Uint32(frame[2:6])), //nolint:gosec // G115: wire int32.
	}, nil
}

// CZReqExpelGroupMember is a decoded CZ_REQ_EXPEL_GROUP_MEMBER frame. The client
// sends both the target's account id and name; the server matches on the pair.
type CZReqExpelGroupMember struct {
	AID  uint32
	Name string
}

// ParseCZReqExpelGroupMember decodes a CZ_REQ_EXPEL_GROUP_MEMBER (0x0103, 30B) frame.
func ParseCZReqExpelGroupMember(frame []byte) (CZReqExpelGroupMember, error) {
	if len(frame) < sizeCZReqExpelMember {
		return CZReqExpelGroupMember{}, fmt.Errorf("packet: parse CZ_REQ_EXPEL_GROUP_MEMBER: want at least %d bytes, got %d", sizeCZReqExpelMember, len(frame))
	}
	if cmd := binary.LittleEndian.Uint16(frame[0:2]); cmd != HeaderCZREQEXPELGROUPMEMBER {
		return CZReqExpelGroupMember{}, fmt.Errorf("packet: parse CZ_REQ_EXPEL_GROUP_MEMBER: unexpected cmd 0x%04x", cmd)
	}
	return CZReqExpelGroupMember{
		AID:  binary.LittleEndian.Uint32(frame[2:6]),
		Name: readNameField(frame, 6, 24),
	}, nil
}

// --- S→C encoders ---

// AckMakeGroupResponse encodes ZC_ACK_MAKE_GROUP (0x00fa, 3B) — the create result
// sent to the requester alone (clif.cpp:7792).
type AckMakeGroupResponse struct {
	// Result is one of PartyCreateOK / PartyCreateNameExists /
	// PartyCreateAlreadyInParty.
	Result uint8
}

// Size returns the on-wire byte length Encode will write (always 3).
func (r AckMakeGroupResponse) Size() int { return sizeZCAckMakeGroup }

// Encode writes ZC_ACK_MAKE_GROUP to w.
func (r AckMakeGroupResponse) Encode(w io.Writer) error {
	var buf [sizeZCAckMakeGroup]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCACKMAKEGROUP)
	buf[2] = r.Result
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_ACK_MAKE_GROUP: %w", err)
	}
	return nil
}

// PartyJoinReqResponse encodes ZC_PARTY_JOIN_REQ (0x02c6, 30B) — the invitation
// shown on the TARGET's screen. GRID is the party id and GroupName the party's
// name (clif.cpp:7924).
type PartyJoinReqResponse struct {
	PartyID   uint32
	PartyName string
}

// Size returns the on-wire byte length Encode will write (always 30).
func (r PartyJoinReqResponse) Size() int { return sizeZCPartyJoinReq }

// Encode writes ZC_PARTY_JOIN_REQ to w.
func (r PartyJoinReqResponse) Encode(w io.Writer) error {
	var buf [sizeZCPartyJoinReq]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCPARTYJOINREQ)
	binary.LittleEndian.PutUint32(buf[2:], r.PartyID)
	writeNameField(buf[:], 6, r.PartyName)
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_PARTY_JOIN_REQ: %w", err)
	}
	return nil
}

// PartyJoinReqAckResponse encodes ZC_PARTY_JOIN_REQ_ACK (0x02c5, 30B) — the
// invitation outcome sent to the INVITER (clif_party_invite_reply,
// clif.cpp:7958). CharacterName is the invitee's name and Result a
// PartyReply* value.
type PartyJoinReqAckResponse struct {
	CharacterName string
	Result        int32
}

// Size returns the on-wire byte length Encode will write (always 30).
func (r PartyJoinReqAckResponse) Size() int { return sizeZCPartyJoinReqAck }

// Encode writes ZC_PARTY_JOIN_REQ_ACK to w.
func (r PartyJoinReqAckResponse) Encode(w io.Writer) error {
	var buf [sizeZCPartyJoinReqAck]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCPARTYJOINREQACK)
	writeNameField(buf[:], 2, r.CharacterName)
	binary.LittleEndian.PutUint32(buf[26:], uint32(r.Result)) //nolint:gosec // G115: wire int32; r.Result is int32.
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_PARTY_JOIN_REQ_ACK: %w", err)
	}
	return nil
}

// DeleteMemberFromGroupResponse encodes ZC_DELETE_MEMBER_FROM_GROUP (0x0105, 31B)
// — one member withdrawn (or a leave/kick refusal), broadcast to the party
// (clif.cpp:8025).
type DeleteMemberFromGroupResponse struct {
	// AID is the withdrawn member's account id.
	AID uint32
	// CharacterName is the withdrawn member's character name.
	CharacterName string
	// Result is a PartyWithdraw* value.
	Result int8
}

// Size returns the on-wire byte length Encode will write (always 31).
func (r DeleteMemberFromGroupResponse) Size() int { return sizeZCDeleteMember }

// Encode writes ZC_DELETE_MEMBER_FROM_GROUP to w.
func (r DeleteMemberFromGroupResponse) Encode(w io.Writer) error {
	var buf [sizeZCDeleteMember]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCDELETEMEMBERFROMGRP)
	binary.LittleEndian.PutUint32(buf[2:], r.AID)
	writeNameField(buf[:], 6, r.CharacterName)
	buf[30] = byte(r.Result) //nolint:gosec // G115: wire int8; r.Result is int8.
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_DELETE_MEMBER_FROM_GROUP: %w", err)
	}
	return nil
}

// AddMemberToGroupResponse encodes ZC_ADD_MEMBER_TO_GROUP (0x0ae4, 89B) — one
// member's full state, broadcast to the party when they join or their state
// changes (clif_party_member_info, clif.cpp:7803).
//
// The Leader and Online fields are positive booleans; the wire inverts both
// (leader 0 = leader, offline 0 = connected).
type AddMemberToGroupResponse struct {
	AID        uint32
	GID        uint32
	Leader     bool
	Class      uint16
	BaseLevel  uint16
	X          uint16
	Y          uint16
	Online     bool
	PartyName  string
	PlayerName string
	MapName    string
	// SharePickup / ShareLoot mirror party.item bit 0 / bit 1.
	SharePickup uint8
	ShareLoot   uint8
}

// Size returns the on-wire byte length Encode will write (always 89).
func (r AddMemberToGroupResponse) Size() int { return sizeZCAddMemberToGroup }

// Encode writes ZC_ADD_MEMBER_TO_GROUP to w.
func (r AddMemberToGroupResponse) Encode(w io.Writer) error {
	var buf [sizeZCAddMemberToGroup]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCADDMEMBERTOGROUP)
	binary.LittleEndian.PutUint32(buf[2:], r.AID)
	binary.LittleEndian.PutUint32(buf[6:], r.GID)
	// leader: 0 = leader, 1 = normal (clif.cpp:7815).
	binary.LittleEndian.PutUint32(buf[10:], boolToU32(!r.Leader))
	binary.LittleEndian.PutUint16(buf[14:], r.Class)
	binary.LittleEndian.PutUint16(buf[16:], r.BaseLevel)
	binary.LittleEndian.PutUint16(buf[18:], r.X)
	binary.LittleEndian.PutUint16(buf[20:], r.Y)
	// offline: 0 = connected, 1 = disconnected (clif.cpp:7820).
	if !r.Online {
		buf[22] = 1
	}
	writeNameField(buf[:], 23, r.PartyName)
	writeNameField(buf[:], 47, r.PlayerName)
	copy(buf[71:87], r.MapName)
	buf[87] = r.SharePickup
	buf[88] = r.ShareLoot
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_ADD_MEMBER_TO_GROUP: %w", err)
	}
	return nil
}

// GroupListMember is one roster entry in a ZC_GROUP_LIST burst. Leader and
// Online are positive booleans; the wire inverts both.
type GroupListMember struct {
	AID        uint32
	GID        uint32
	PlayerName string
	MapName    string
	Leader     bool
	Online     bool
	Class      uint16
	BaseLevel  uint16
}

// GroupListResponse encodes ZC_GROUP_LIST (0x0ae5, variable: 28 + 54*N) — the
// whole roster, sent to the whole party after any membership change
// (clif_party_info, clif.cpp:7840).
type GroupListResponse struct {
	PartyName string
	Members   []GroupListMember
}

// Size returns the on-wire byte length Encode will write for the current member
// list (28 + 54*N, capped at MaxPartySize).
func (r GroupListResponse) Size() int {
	n := min(len(r.Members), MaxPartySize)
	return sizeZCGroupListHeader + sizeZCGroupListSub*n
}

// Encode writes ZC_GROUP_LIST to w. The member list is capped at MaxPartySize so
// an over-long roster cannot produce a frame the client mis-parses.
func (r GroupListResponse) Encode(w io.Writer) error {
	members := r.Members
	if len(members) > MaxPartySize {
		members = members[:MaxPartySize]
	}
	buf := make([]byte, sizeZCGroupListHeader+sizeZCGroupListSub*len(members))
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCGROUPLIST)
	binary.LittleEndian.PutUint16(buf[2:], uint16(len(buf))) //nolint:gosec // G115: bounded by MaxPartySize.
	writeNameField(buf, 4, r.PartyName)
	off := sizeZCGroupListHeader
	for _, m := range members {
		binary.LittleEndian.PutUint32(buf[off:], m.AID)
		binary.LittleEndian.PutUint32(buf[off+4:], m.GID)
		writeNameField(buf, off+8, m.PlayerName)
		copy(buf[off+32:off+48], m.MapName)
		// leader: 0 = leader, 1 = normal (clif.cpp:7870).
		if !m.Leader {
			buf[off+48] = 1
		}
		// offline: 0 = connected, 1 = disconnected (clif.cpp:7871).
		if !m.Online {
			buf[off+49] = 1
		}
		binary.LittleEndian.PutUint16(buf[off+50:], m.Class)
		binary.LittleEndian.PutUint16(buf[off+52:], m.BaseLevel)
		off += sizeZCGroupListSub
	}
	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write ZC_GROUP_LIST: %w", err)
	}
	return nil
}

// MaxPartySize is rAthena's MAX_PARTY — the roster ceiling the client enforces.
const MaxPartySize = 12

// boolToU32 maps true→1, false→0 for the inverted leader field.
func boolToU32(b bool) uint32 {
	if b {
		return 1
	}
	return 0
}
