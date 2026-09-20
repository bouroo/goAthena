package packet

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Guild family for PACKETVER 20250604 (MAIN 20250604). Sources cited per
// declaration; rAthena paths are third_party/rathena/.
//
// Scope note: this slice covers the social core — create, info, roster,
// invite/reply, leave, expel, break, chat, login-state. Positions, emblems,
// alliances, castles, and skills are follow-ups (each its own commit).
const (
	// C→S opcodes (clif_packetdb.hpp:150-159).
	HeaderCZREQLEAVEGUILD      uint16 = 0x0159 // CZ_REQ_LEAVE_GUILD — clif_parse_GuildLeave
	HeaderCZREQBANGUILD        uint16 = 0x015b // CZ_REQ_BAN_GUILD — clif_parse_GuildExpulsion
	HeaderCZREQDISORGANIZEGILD uint16 = 0x015d // CZ_REQ_DISORGANIZE_GUILD — clif_parse_GuildBreak
	HeaderCZCREATEGUILD        uint16 = 0x0165 // CZ_REQ_MAKE_GUILD — clif_parse_CreateGuild
	HeaderCZREQJOINGUILD       uint16 = 0x0168 // CZ_REQ_JOIN_GUILD — clif_parse_GuildInvite
	HeaderCZJOINGUILD          uint16 = 0x016b // CZ_JOIN_GUILD — clif_parse_GuildReplyInvite
	HeaderCZGUILDCHAT          uint16 = 0x017e // CZ_GUILD_CHAT — clif_parse_GuildMessage
	// S→C opcodes.
	HeaderZCGUILDCHAT       uint16 = 0x017f // ZC_GUILD_CHAT (packets.hpp:867)
	HeaderZCREQUESTMAKEGILD uint16 = 0x0167 // ZC_RESULT_MAKE_GUILD (packets.hpp:1300)
	HeaderZCREQJOINGUILD    uint16 = 0x016a // ZC_REQ_JOIN_GUILD (packets.hpp:1320)
	HeaderZCACKRQJOINGUILD  uint16 = 0x0169 // ZC_ACK_REQ_JOIN_GUILD (packets.hpp:1313)
	HeaderZCUPDATEGDID      uint16 = 0x02f7 // ZC_UPDATE_GDID (packets_struct.hpp:5378 branch, >=20220216; 0x016c before)
	HeaderZCUPDATECHARSTAT  uint16 = 0x016d // ZC_UPDATE_CHARSTAT (clif.cpp:8682 memberlogin)
	HeaderZCACKLEAVEGUILD   uint16 = 0x015a // ZC_ACK_LEAVE_GUILD v2 (packets_struct.hpp:3686, >=20161019)
	HeaderZCACKBANGUILD     uint16 = 0x015c // ZC_ACK_BAN_GUILD v3 (packets_struct.hpp:3668, >=20161019)
	HeaderZCACKDISORGGUILD  uint16 = 0x015e // ZC_ACK_DISORGANIZE_GUILD_RESULT (packets.hpp:1293)
	HeaderZCACKMENUGUILD    uint16 = 0x014e // ZC_ACK_GUILD_MENUINTERFACE (packets.hpp:842)
	HeaderZCGUILDINFO       uint16 = 0x0b7b // ZC_GUILD_INFO (packets_struct.hpp:4809 branch, >=20200902)
	HeaderZCMEMBERMGRINFO   uint16 = 0x0b7d // ZC_MEMBERMGR_INFO (packets_struct.hpp:4759 branch, >=20200902)
)

// On-wire byte sizes.
const (
	// CZ_REQ_MAKE_GUILD: int16 cmd + uint32 char id + char name[24]
	// (clif_packetdb.hpp:156 "0x0165,30"; clif.cpp:14258 — the char id is
	// read but unused, the session supplies the identity).
	sizeCZCreateGuild = 30
	// CZ_REQ_LEAVE_GUILD: cmd + guild_id + AID + CID + message[40] = 52.
	sizeCZGuildLeave = 52
	// CZ_REQ_BAN_GUILD: same shape as leave = 52.
	sizeCZGuildBan = 52
	// CZ_REQ_DISORGANIZE_GUILD: cmd + key[40] = 44 (packets.hpp:1284).
	sizeCZGuildBreak = 44
	// CZ_REQ_JOIN_GUILD: cmd + AID + inviter_AID + inviter_CID = 14
	// (packets.hpp:1302).
	sizeCZReqJoinGuild = 14
	// CZ_JOIN_GUILD: cmd + guild_id + answer = 10 (packets.hpp:1323).
	sizeCZJoinGuild = 10
	// CZ_GUILD_CHAT: cmd + message[≤98] variable — dispatch uses fixed 100
	// (clif_packetdb.hpp binds 0x017e as -1 variable; the handler reads to
	// frame end). Handlers use the var-length path, so no fixed size const.
	// ZC_GUILD_CHAT: cmd + len + message[].
	sizeZCGuildChatHeader = 4
	// ZC_RESULT_MAKE_GUILD: cmd + result = 3.
	sizeZCResultMakeGuild = 3
	// ZC_REQ_JOIN_GUILD: cmd + guild_id + guild name[24] = 30.
	sizeZCReqJoinGuild = 30
	// ZC_ACK_REQ_JOIN_GUILD: cmd + result = 3.
	sizeZCAckReqJoinGuild = 3
	// ZC_UPDATE_GDID: cmd + guildId + emblemVersion + mode + isMaster +
	// interSid + guild name[24] + masterGID = 47 (packets_struct.hpp,
	// masterGID active PACKETVER_MAIN_NUM >= 20220216).
	sizeZCUpdateGDID = 47
	// ZC_UPDATE_CHARSTAT: cmd + aid + cid + status = 14 (clif.cpp:8682).
	sizeZCUpdateCharStat = 14
	// ZC_ACK_LEAVE_GUILD v2: cmd + GID + reason[40] = 46
	// (packets_struct.hpp:3686, >=20161019 sends GID).
	sizeZCAckLeaveGuild = 46
	// ZC_ACK_BAN_GUILD v3: cmd + reason[40] + GID = 46
	// (packets_struct.hpp:3668).
	sizeZCAckBanGuild = 46
	// ZC_ACK_DISORGANIZE_GUILD_RESULT: cmd + result(int32) = 6
	// (packets.hpp:1293 result is int32).
	sizeZCAckDisorgGuild = 6
	// ZC_ACK_GUILD_MENUINTERFACE: cmd + flag(uint32, is master) = 6
	// (clif.cpp:8762 clif_guild_masterormember).
	sizeZCAckMenuInterface = 6
	// ZC_GUILD_INFO at >=20200902 (packets_struct.hpp:4809): cmd(2) + GDID(4)
	// + level(4) + userNum(4) + maxUserNum(4) + userAverageLevel(4) + exp(4)
	// + maxExp(4) + point(4) + honor(4) + virtue(4) + emblemVersion(4) +
	// guildname(24) + manageLand(16) + zeny(4) + masterGID(4) +
	// masterName(24) = 118.
	sizeZCGuildInfo = 118
	// GUILD_MEMBER_INFO at >=20200902 (packets_struct.hpp:4745): AID(4) +
	// GID(4) + head(2) + headPalette(2) + sex(2) + job(2) + level(2) +
	// contributionExp(4) + currentState(4) + positionID(4) +
	// lastLoginTime(4) + char_name(24) = 58.
	sizeZCGuildMemberInfo = 58
	// ZC_MEMBERMGR_INFO header: cmd(2) + packetLength(2).
	sizeZCMemberMgrHeader = 4
)

// ZC_RESULT_MAKE_GUILD result values (guild_create flag, src/map/guild.cpp:693-717):
// 0 created, 1 already in a guild, 2 duplicate name / server rejected,
// 3 emperium required (guild_emperium_check on by default, battle.cpp:8349).
const (
	GuildCreateOK            uint8 = 0
	GuildCreateAlreadyIn     uint8 = 1
	GuildCreateDuplicateName uint8 = 2
	GuildCreateNeedEmperium  uint8 = 3
)

// ZC_ACK_REQ_JOIN_GUILD answers (clif.cpp:9156-9162).
const (
	GuildInviteAlreadyIn int32 = 0
	GuildInviteRejected  int32 = 1
	GuildInviteAccepted  int32 = 2
	GuildInviteFull      int32 = 3
)

// CZ_JOIN_GUILD answer (clif.cpp GuildReplyInvite: 0=reject, 1=accept).
const (
	GuildJoinReject int32 = 0
	GuildJoinAccept int32 = 1
)

// ZC_ACK_DISORGANIZE_GUILD_RESULT results (clif_guild_broken flags,
// src/map/guild.cpp:2310 — 2 = "still have members"; 0 = broken).
const (
	GuildBrokenOK     int32 = 0
	GuildBrokenHasMem int32 = 2
)

// --- C→S parsers (+ Encode for the e2e harness) ---

// CZCreateGuild is a decoded CZ_REQ_MAKE_GUILD frame. CharID rides the wire
// but the session is authoritative (clif.cpp:14258 leaves it unused).
type CZCreateGuild struct {
	CharID uint32
	Name   string
}

// ParseCZCreateGuild decodes CZ_REQ_MAKE_GUILD (0x0165, 30B).
func ParseCZCreateGuild(frame []byte) (CZCreateGuild, error) {
	if len(frame) < sizeCZCreateGuild {
		return CZCreateGuild{}, fmt.Errorf("packet: parse CZ_REQ_MAKE_GUILD: want at least %d bytes, got %d", sizeCZCreateGuild, len(frame))
	}
	if cmd := binary.LittleEndian.Uint16(frame[0:2]); cmd != HeaderCZCREATEGUILD {
		return CZCreateGuild{}, fmt.Errorf("packet: parse CZ_REQ_MAKE_GUILD: unexpected cmd 0x%04x", cmd)
	}
	return CZCreateGuild{
		CharID: binary.LittleEndian.Uint32(frame[2:6]),
		Name:   readNameField(frame, 6, 24),
	}, nil
}

// Encode writes the CZ_REQ_MAKE_GUILD frame to w.
func (m CZCreateGuild) Encode(w io.Writer) error {
	var buf [sizeCZCreateGuild]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderCZCREATEGUILD)
	binary.LittleEndian.PutUint32(buf[2:], m.CharID)
	writeNameField(buf[:], 6, m.Name)
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write CZ_REQ_MAKE_GUILD: %w", err)
	}
	return nil
}

// CZGuildLeave is a decoded CZ_REQ_LEAVE_GUILD / CZ_REQ_BAN_GUILD frame — both
// share the shape (guild_id, AID, CID, message[40]); ban is the leader/expel
// path, leave is the self path.
type CZGuildLeave struct {
	GuildID uint32
	AID     uint32
	CID     uint32
	Message string
}

// ParseCZGuildLeave decodes CZ_REQ_LEAVE_GUILD (0x0159, 52B).
func ParseCZGuildLeave(frame []byte) (CZGuildLeave, error) {
	return parseCZGuildLeave(frame, HeaderCZREQLEAVEGUILD)
}

// ParseCZGuildBan decodes CZ_REQ_BAN_GUILD (0x015b, 52B).
func ParseCZGuildBan(frame []byte) (CZGuildLeave, error) {
	return parseCZGuildLeave(frame, HeaderCZREQBANGUILD)
}

func parseCZGuildLeave(frame []byte, want uint16) (CZGuildLeave, error) {
	if len(frame) < sizeCZGuildLeave {
		return CZGuildLeave{}, fmt.Errorf("packet: parse guild leave/ban: want at least %d bytes, got %d", sizeCZGuildLeave, len(frame))
	}
	if cmd := binary.LittleEndian.Uint16(frame[0:2]); cmd != want {
		return CZGuildLeave{}, fmt.Errorf("packet: parse guild leave/ban: unexpected cmd 0x%04x", cmd)
	}
	return CZGuildLeave{
		GuildID: binary.LittleEndian.Uint32(frame[2:6]),
		AID:     binary.LittleEndian.Uint32(frame[6:10]),
		CID:     binary.LittleEndian.Uint32(frame[10:14]),
		Message: readNameField(frame, 14, 40),
	}, nil
}

// EncodeCZGuildLeave writes a CZ_REQ_LEAVE_GUILD frame to w.
func EncodeCZGuildLeave(w io.Writer, guildID, aid, cid uint32, message string) error {
	return encodeCZGuildLeave(w, HeaderCZREQLEAVEGUILD, guildID, aid, cid, message)
}

// EncodeCZGuildBan writes a CZ_REQ_BAN_GUILD frame to w.
func EncodeCZGuildBan(w io.Writer, guildID, aid, cid uint32, message string) error {
	return encodeCZGuildLeave(w, HeaderCZREQBANGUILD, guildID, aid, cid, message)
}

func encodeCZGuildLeave(w io.Writer, cmd uint16, guildID, aid, cid uint32, message string) error {
	var buf [sizeCZGuildLeave]byte
	binary.LittleEndian.PutUint16(buf[0:], cmd)
	binary.LittleEndian.PutUint32(buf[2:], guildID)
	binary.LittleEndian.PutUint32(buf[6:], aid)
	binary.LittleEndian.PutUint32(buf[10:], cid)
	writeNameField(buf[:], 14, message)
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write guild leave/ban: %w", err)
	}
	return nil
}

// CZGuildBreak is a decoded CZ_REQ_DISORGANIZE_GUILD frame. Key echoes the
// guild name; the server requires it to match (guild.cpp:2300).
type CZGuildBreak struct {
	Key string
}

// ParseCZGuildBreak decodes CZ_REQ_DISORGANIZE_GUILD (0x015d, 44B).
func ParseCZGuildBreak(frame []byte) (CZGuildBreak, error) {
	if len(frame) < sizeCZGuildBreak {
		return CZGuildBreak{}, fmt.Errorf("packet: parse CZ_REQ_DISORGANIZE_GUILD: want at least %d bytes, got %d", sizeCZGuildBreak, len(frame))
	}
	if cmd := binary.LittleEndian.Uint16(frame[0:2]); cmd != HeaderCZREQDISORGANIZEGILD {
		return CZGuildBreak{}, fmt.Errorf("packet: parse CZ_REQ_DISORGANIZE_GUILD: unexpected cmd 0x%04x", cmd)
	}
	return CZGuildBreak{Key: readNameField(frame, 2, 40)}, nil
}

// EncodeCZGuildBreak writes a CZ_REQ_DISORGANIZE_GUILD frame to w.
func EncodeCZGuildBreak(w io.Writer, key string) error {
	var buf [sizeCZGuildBreak]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderCZREQDISORGANIZEGILD)
	writeNameField(buf[:], 2, key)
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write CZ_REQ_DISORGANIZE_GUILD: %w", err)
	}
	return nil
}

// CZReqJoinGuild is a decoded CZ_REQ_JOIN_GUILD frame: an invite addressed by
// the target's account id. The inviter fields ride the wire but the session is
// authoritative.
type CZReqJoinGuild struct {
	AID        uint32
	InviterAID uint32
	InviterCID uint32
}

// ParseCZReqJoinGuild decodes CZ_REQ_JOIN_GUILD (0x0168, 14B).
func ParseCZReqJoinGuild(frame []byte) (CZReqJoinGuild, error) {
	if len(frame) < sizeCZReqJoinGuild {
		return CZReqJoinGuild{}, fmt.Errorf("packet: parse CZ_REQ_JOIN_GUILD: want at least %d bytes, got %d", sizeCZReqJoinGuild, len(frame))
	}
	if cmd := binary.LittleEndian.Uint16(frame[0:2]); cmd != HeaderCZREQJOINGUILD {
		return CZReqJoinGuild{}, fmt.Errorf("packet: parse CZ_REQ_JOIN_GUILD: unexpected cmd 0x%04x", cmd)
	}
	return CZReqJoinGuild{
		AID:        binary.LittleEndian.Uint32(frame[2:6]),
		InviterAID: binary.LittleEndian.Uint32(frame[6:10]),
		InviterCID: binary.LittleEndian.Uint32(frame[10:14]),
	}, nil
}

// EncodeCZReqJoinGuild writes a CZ_REQ_JOIN_GUILD frame to w.
func EncodeCZReqJoinGuild(w io.Writer, aid, inviterAID, inviterCID uint32) error {
	var buf [sizeCZReqJoinGuild]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderCZREQJOINGUILD)
	binary.LittleEndian.PutUint32(buf[2:], aid)
	binary.LittleEndian.PutUint32(buf[6:], inviterAID)
	binary.LittleEndian.PutUint32(buf[10:], inviterCID)
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write CZ_REQ_JOIN_GUILD: %w", err)
	}
	return nil
}

// CZJoinGuild is a decoded CZ_JOIN_GUILD frame: the invitee's reply. Answer is
// a raw 0=reject / 1=accept (GuildJoinReject / GuildJoinAccept).
type CZJoinGuild struct {
	GuildID uint32
	Answer  int32
}

// ParseCZJoinGuild decodes CZ_JOIN_GUILD (0x016b, 10B).
func ParseCZJoinGuild(frame []byte) (CZJoinGuild, error) {
	if len(frame) < sizeCZJoinGuild {
		return CZJoinGuild{}, fmt.Errorf("packet: parse CZ_JOIN_GUILD: want at least %d bytes, got %d", sizeCZJoinGuild, len(frame))
	}
	if cmd := binary.LittleEndian.Uint16(frame[0:2]); cmd != HeaderCZJOINGUILD {
		return CZJoinGuild{}, fmt.Errorf("packet: parse CZ_JOIN_GUILD: unexpected cmd 0x%04x", cmd)
	}
	return CZJoinGuild{
		GuildID: binary.LittleEndian.Uint32(frame[2:6]),
		Answer:  int32(binary.LittleEndian.Uint32(frame[6:10])), //nolint:gosec // G115: wire int32.
	}, nil
}

// EncodeCZJoinGuild writes a CZ_JOIN_GUILD frame to w.
func EncodeCZJoinGuild(w io.Writer, guildID uint32, answer int32) error {
	var buf [sizeCZJoinGuild]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderCZJOINGUILD)
	binary.LittleEndian.PutUint32(buf[2:], guildID)
	binary.LittleEndian.PutUint32(buf[6:], uint32(answer)) //nolint:gosec // G115: wire int32.
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write CZ_JOIN_GUILD: %w", err)
	}
	return nil
}

// ParseCZGuildChat decodes a CZ_GUILD_CHAT (0x017e, variable) frame: the
// message after the cmd+length header, trimmed to the wire cap. The dispatch
// table binds it as a variable-length opcode so frame spans the full message.
func ParseCZGuildChat(frame []byte) (string, error) {
	if len(frame) < 4 {
		return "", fmt.Errorf("packet: parse CZ_GUILD_CHAT: short frame %d", len(frame))
	}
	if cmd := binary.LittleEndian.Uint16(frame[0:2]); cmd != HeaderCZGUILDCHAT {
		return "", fmt.Errorf("packet: parse CZ_GUILD_CHAT: unexpected cmd 0x%04x", cmd)
	}
	return readNameField(frame, 4, len(frame)-4), nil
}

// EncodeCZGuildChat writes a CZ_GUILD_CHAT frame to w (cmd + total length +
// message; the length prefix is what variableFrameSize frames on).
func EncodeCZGuildChat(w io.Writer, message string) error {
	buf := make([]byte, 4+len(message))
	binary.LittleEndian.PutUint16(buf[0:], HeaderCZGUILDCHAT)
	binary.LittleEndian.PutUint16(buf[2:], uint16(len(buf))) //nolint:gosec // G115: bounded by message len.
	copy(buf[4:], message)
	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write CZ_GUILD_CHAT: %w", err)
	}
	return nil
}

// --- S→C encoders ---

// ResultMakeGuildResponse encodes ZC_RESULT_MAKE_GUILD (0x0167, 3B) — the
// create result to the requester alone (clif.cpp:8640).
type ResultMakeGuildResponse struct {
	Result uint8
}

// Size returns the frame length (always 3).
func (r ResultMakeGuildResponse) Size() int { return sizeZCResultMakeGuild }

// Encode writes ZC_RESULT_MAKE_GUILD to w.
func (r ResultMakeGuildResponse) Encode(w io.Writer) error {
	var buf [sizeZCResultMakeGuild]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCREQUESTMAKEGILD)
	buf[2] = r.Result
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_RESULT_MAKE_GUILD: %w", err)
	}
	return nil
}

// ReqJoinGuildResponse encodes ZC_REQ_JOIN_GUILD (0x016a, 30B) — the invitation
// shown on the TARGET's screen (clif.cpp:9145).
type ReqJoinGuildResponse struct {
	GuildID   uint32
	GuildName string
}

// Size returns the frame length (always 30).
func (r ReqJoinGuildResponse) Size() int { return sizeZCReqJoinGuild }

// Encode writes ZC_REQ_JOIN_GUILD to w.
func (r ReqJoinGuildResponse) Encode(w io.Writer) error {
	var buf [sizeZCReqJoinGuild]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCREQJOINGUILD)
	binary.LittleEndian.PutUint32(buf[2:], r.GuildID)
	writeNameField(buf[:], 6, r.GuildName)
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_REQ_JOIN_GUILD: %w", err)
	}
	return nil
}

// AckReqJoinGuildResponse encodes ZC_ACK_REQ_JOIN_GUILD (0x0169, 3B) — the
// invitation outcome sent to the INVITER (clif.cpp:9160). Result rides the
// wire as one byte (PACKET_ZC_ACK_REQ_JOIN_GUILD.result is uint8).
type AckReqJoinGuildResponse struct {
	Result int32
}

// Size returns the frame length (always 3).
func (r AckReqJoinGuildResponse) Size() int { return sizeZCAckReqJoinGuild }

// Encode writes ZC_ACK_REQ_JOIN_GUILD to w.
func (r AckReqJoinGuildResponse) Encode(w io.Writer) error {
	var buf [sizeZCAckReqJoinGuild]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCACKRQJOINGUILD)
	buf[2] = byte(r.Result) //nolint:gosec // G115: result is a small enum.
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_ACK_REQ_JOIN_GUILD: %w", err)
	}
	return nil
}

// UpdateGDIDResponse encodes ZC_UPDATE_GDID (0x02f7, 47B — the >=20220216
// branch; pre-2022 clients use 0x016c/43B without masterGID) — "you belong to
// this guild" (clif_guild_belonginfo, clif.cpp:8652). Mode mirrors the
// member's position permission bits (&0x01 invite, &0x10 expel); IsMaster
// gates the client's guild-management UI.
type UpdateGDIDResponse struct {
	GuildID       uint32
	EmblemVersion uint32
	Mode          uint32
	IsMaster      bool
	// InterSid is rAthena's placeholder (always 0, clif.cpp:8668).
	InterSid  uint32
	GuildName string
	// MasterGID is the guild leader's char id (appended since
	// PACKETVER_MAIN_NUM 20220216; always sent at PACKETVER 20250604).
	MasterGID uint32
}

// Size returns the frame length (always 47).
func (r UpdateGDIDResponse) Size() int { return sizeZCUpdateGDID }

// Encode writes ZC_UPDATE_GDID to w.
func (r UpdateGDIDResponse) Encode(w io.Writer) error {
	var buf [sizeZCUpdateGDID]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCUPDATEGDID)
	binary.LittleEndian.PutUint32(buf[2:], r.GuildID)
	binary.LittleEndian.PutUint32(buf[6:], r.EmblemVersion)
	binary.LittleEndian.PutUint32(buf[10:], r.Mode)
	if r.IsMaster {
		buf[14] = 1
	}
	binary.LittleEndian.PutUint32(buf[15:], r.InterSid)
	writeNameField(buf[:], 19, r.GuildName)
	binary.LittleEndian.PutUint32(buf[43:], r.MasterGID)
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_UPDATE_GDID: %w", err)
	}
	return nil
}

// UpdateCharStatResponse encodes ZC_UPDATE_CHARSTAT (0x016d, 14B) — one
// member's online/offline toggle broadcast to the guild
// (clif_guild_memberlogin_notice, clif.cpp:8682). Status 1 = online.
type UpdateCharStatResponse struct {
	AID    uint32
	CID    uint32
	Status uint32
}

// Size returns the frame length (always 14).
func (r UpdateCharStatResponse) Size() int { return sizeZCUpdateCharStat }

// Encode writes ZC_UPDATE_CHARSTAT to w.
func (r UpdateCharStatResponse) Encode(w io.Writer) error {
	var buf [sizeZCUpdateCharStat]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCUPDATECHARSTAT)
	binary.LittleEndian.PutUint32(buf[2:], r.AID)
	binary.LittleEndian.PutUint32(buf[6:], r.CID)
	binary.LittleEndian.PutUint32(buf[10:], r.Status)
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_UPDATE_CHARSTAT: %w", err)
	}
	return nil
}

// AckLeaveGuildResponse encodes ZC_ACK_LEAVE_GUILD v2 (0x015a, 46B) — a member
// withdrew (clif_guild_leave, clif.cpp:~9220). GID form at >=20161019.
type AckLeaveGuildResponse struct {
	GID    uint32
	Reason string
}

// Size returns the frame length (always 46).
func (r AckLeaveGuildResponse) Size() int { return sizeZCAckLeaveGuild }

// Encode writes ZC_ACK_LEAVE_GUILD to w.
func (r AckLeaveGuildResponse) Encode(w io.Writer) error {
	var buf [sizeZCAckLeaveGuild]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCACKLEAVEGUILD)
	binary.LittleEndian.PutUint32(buf[2:], r.GID)
	writeNameField(buf[:], 6, r.Reason)
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_ACK_LEAVE_GUILD: %w", err)
	}
	return nil
}

// AckBanGuildResponse encodes ZC_ACK_BAN_GUILD v3 (0x015c, 46B) — a member was
// expelled (clif_guild_expulsion). GID form at >=20161019.
type AckBanGuildResponse struct {
	GID    uint32
	Reason string
}

// Size returns the frame length (always 46).
func (r AckBanGuildResponse) Size() int { return sizeZCAckBanGuild }

// Encode writes ZC_ACK_BAN_GUILD to w.
func (r AckBanGuildResponse) Encode(w io.Writer) error {
	var buf [sizeZCAckBanGuild]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCACKBANGUILD)
	writeNameField(buf[:], 2, r.Reason)
	binary.LittleEndian.PutUint32(buf[42:], r.GID)
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_ACK_BAN_GUILD: %w", err)
	}
	return nil
}

// AckDisorganizeGuildResponse encodes ZC_ACK_DISORGANIZE_GUILD_RESULT
// (0x015e, 6B) — the disband result (clif_guild_broken, clif.cpp:9377).
type AckDisorganizeGuildResponse struct {
	Result int32
}

// Size returns the frame length (always 6).
func (r AckDisorganizeGuildResponse) Size() int { return sizeZCAckDisorgGuild }

// Encode writes ZC_ACK_DISORGANIZE_GUILD_RESULT to w.
func (r AckDisorganizeGuildResponse) Encode(w io.Writer) error {
	var buf [sizeZCAckDisorgGuild]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCACKDISORGGUILD)
	binary.LittleEndian.PutUint32(buf[2:], uint32(r.Result)) //nolint:gosec // G115: wire int32.
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_ACK_DISORGANIZE_GUILD_RESULT: %w", err)
	}
	return nil
}

// GuildChatResponse encodes ZC_GUILD_CHAT (0x017f, variable) — a guild-wide
// chat line (clif.cpp ZC_GUILD_CHAT sender path). Message is the full line
// including the speaker prefix rAthena's client renders.
type GuildChatResponse struct {
	Message string
}

// Size returns the frame length (4 + len(message) + 1 — rAthena appends the
// NUL terminator, clif.cpp clif_guild_message packetLength += len + 1).
func (r GuildChatResponse) Size() int { return sizeZCGuildChatHeader + len(r.Message) + 1 }

// Encode writes ZC_GUILD_CHAT to w.
func (r GuildChatResponse) Encode(w io.Writer) error {
	if r.Size() > 0xffff {
		return fmt.Errorf("packet: write ZC_GUILD_CHAT: message too long (%d)", len(r.Message))
	}
	buf := make([]byte, r.Size())
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCGUILDCHAT)
	binary.LittleEndian.PutUint16(buf[2:], uint16(r.Size())) //nolint:gosec // G115: bounded above.
	copy(buf[4:], r.Message)                                 // trailing byte stays NUL
	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write ZC_GUILD_CHAT: %w", err)
	}
	return nil
}

// AckMenuInterfaceResponse encodes ZC_ACK_GUILD_MENUINTERFACE (0x014e, 6B) —
// the reply to CZ_REQ_GUILD_MENUINTERFACE naming whether this char is the
// guild master (clif_guild_masterormember, clif.cpp:8762). Flag is a uint32
// boolean the client uses to gate the guild-management UI.
type AckMenuInterfaceResponse struct {
	IsMaster bool
}

// Size returns the frame length (always 6).
func (r AckMenuInterfaceResponse) Size() int { return sizeZCAckMenuInterface }

// Encode writes ZC_ACK_GUILD_MENUINTERFACE to w.
func (r AckMenuInterfaceResponse) Encode(w io.Writer) error {
	var buf [sizeZCAckMenuInterface]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCACKMENUGUILD)
	if r.IsMaster {
		binary.LittleEndian.PutUint32(buf[2:], 1)
	}
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_ACK_GUILD_MENUINTERFACE: %w", err)
	}
	return nil
}

// GuildInfoResponse encodes ZC_GUILD_INFO (0x0b7b, 118B) — the guild's basic
// info panel (clif.cpp clif_guild_info). The economy columns (exp, points,
// zeny) stay zero this slice; the fields are zero-filled on the wire.
type GuildInfoResponse struct {
	GuildID    uint32
	Level      uint32
	UserNum    uint32
	MaxUserNum uint32
	AvgLevel   uint32
	// Exp / MaxExp are the guild contribution totals (uint32 wire at this
	// version); zero until guild-exp donation lands.
	Exp    uint32
	MaxExp uint32
	// Point is the unspent guild skill-point total (zero until skills land).
	Point uint32
	// Honor / Virtue carry the guild's emblem-color modifiers (rAthena
	// defaults 0).
	Honor  uint32
	Virtue uint32
	// EmblemVersion is zero until emblems land.
	EmblemVersion uint32
	GuildName     string
	// ManageLand is the guild's castle/territory name (empty this slice).
	ManageLand string
	// Zeny is the guild fund (zero until the fund feature lands).
	Zeny       uint32
	MasterGID  uint32
	MasterName string
}

// Size returns the frame length (always 118).
func (r GuildInfoResponse) Size() int { return sizeZCGuildInfo }

// Encode writes ZC_GUILD_INFO to w.
func (r GuildInfoResponse) Encode(w io.Writer) error {
	var buf [sizeZCGuildInfo]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCGUILDINFO)
	binary.LittleEndian.PutUint32(buf[2:], r.GuildID)
	binary.LittleEndian.PutUint32(buf[6:], r.Level)
	binary.LittleEndian.PutUint32(buf[10:], r.UserNum)
	binary.LittleEndian.PutUint32(buf[14:], r.MaxUserNum)
	binary.LittleEndian.PutUint32(buf[18:], r.AvgLevel)
	binary.LittleEndian.PutUint32(buf[22:], r.Exp)
	binary.LittleEndian.PutUint32(buf[26:], r.MaxExp)
	binary.LittleEndian.PutUint32(buf[30:], r.Point)
	binary.LittleEndian.PutUint32(buf[34:], r.Honor)
	binary.LittleEndian.PutUint32(buf[38:], r.Virtue)
	binary.LittleEndian.PutUint32(buf[42:], r.EmblemVersion)
	writeNameField(buf[:], 46, r.GuildName)
	writeNameField(buf[:], 70, r.ManageLand)
	binary.LittleEndian.PutUint32(buf[86:], r.Zeny)
	binary.LittleEndian.PutUint32(buf[90:], r.MasterGID)
	writeNameField(buf[:], 94, r.MasterName)
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_GUILD_INFO: %w", err)
	}
	return nil
}

// GuildMemberInfo is one roster entry in a ZC_MEMBERMGR_INFO burst. currentState
// is a uint32 boolean (1 = connected).
type GuildMemberInfo struct {
	AID uint32
	GID uint32
	// Head / HeadPalette / Sex are the appearance fields rAthena copies from
	// the char row; zero-filled this slice (the client renders defaults).
	Head        uint16
	HeadPalette uint16
	Sex         uint16
	Job         uint16
	Level       uint16
	// Contribution is the member's donated exp (zero until donation lands).
	Contribution uint32
	State        uint32
	// Position: 0 = master, 1+ = member ranks.
	Position int32
	// LastLogin is the unix ts rAthena shows in the manager window (zero this
	// slice).
	LastLogin uint32
	CharName  string
}

// MemberMgrInfoResponse encodes ZC_MEMBERMGR_INFO (0x0b7d, 4 + 58*N) — the
// whole roster, sent when the guild window opens and after any membership
// change (clif_guild_memberlist, clif.cpp:8860).
type MemberMgrInfoResponse struct {
	Members []GuildMemberInfo
}

// Size returns the on-wire byte length Encode will write (4 + 58*N, capped at
// MaxGuildSize).
func (r MemberMgrInfoResponse) Size() int {
	n := len(r.Members)
	if n > MaxGuildSize {
		n = MaxGuildSize
	}
	return sizeZCMemberMgrHeader + sizeZCGuildMemberInfo*n
}

// Encode writes ZC_MEMBERMGR_INFO to w. The member list is capped at
// MaxGuildSize so an over-long roster cannot produce a frame the client
// mis-parses.
func (r MemberMgrInfoResponse) Encode(w io.Writer) error {
	members := r.Members
	if len(members) > MaxGuildSize {
		members = members[:MaxGuildSize]
	}
	buf := make([]byte, sizeZCMemberMgrHeader+sizeZCGuildMemberInfo*len(members))
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCMEMBERMGRINFO)
	binary.LittleEndian.PutUint16(buf[2:], uint16(len(buf))) //nolint:gosec // G115: bounded by MaxGuildSize.
	off := sizeZCMemberMgrHeader
	for _, m := range members {
		binary.LittleEndian.PutUint32(buf[off:], m.AID)
		binary.LittleEndian.PutUint32(buf[off+4:], m.GID)
		binary.LittleEndian.PutUint16(buf[off+8:], m.Head)
		binary.LittleEndian.PutUint16(buf[off+10:], m.HeadPalette)
		binary.LittleEndian.PutUint16(buf[off+12:], m.Sex)
		binary.LittleEndian.PutUint16(buf[off+14:], m.Job)
		binary.LittleEndian.PutUint16(buf[off+16:], m.Level)
		binary.LittleEndian.PutUint32(buf[off+18:], m.Contribution)
		binary.LittleEndian.PutUint32(buf[off+22:], m.State)
		binary.LittleEndian.PutUint32(buf[off+26:], uint32(m.Position)) //nolint:gosec // G115: wire int32.
		binary.LittleEndian.PutUint32(buf[off+30:], m.LastLogin)
		writeNameField(buf, off+34, m.CharName)
		off += sizeZCGuildMemberInfo
	}
	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write ZC_MEMBERMGR_INFO: %w", err)
	}
	return nil
}

// MaxGuildSize mirrors the domain's member ceiling (rAthena base MAX_GUILD 16)
// for frame sizing; kept in the packet package because encode buffers depend
// on it. See internal/modules/social/guild/domain for the enforced cap.
const MaxGuildSize = 16

// Exported C→S frame sizes and headers the gateway dispatch table registers.
// They duplicate the unexported parser constants so the dispatch table and the
// parsers cannot drift apart without a compile error.
const (
	// HeaderCZGUILDCHECKMASTER is CZ_REQ_GUILD_MENUINTERFACE (0x014d, cmd-only
	// 2B) — the guild-window permission poll (clif_packetdb.hpp:150).
	HeaderCZGUILDCHECKMASTER uint16 = 0x014d

	// SizeCZCreateGuild is CZ_REQ_MAKE_GUILD's frame size (0x0165, 30B).
	SizeCZCreateGuild = sizeCZCreateGuild
	// SizeCZReqJoinGuild is CZ_REQ_JOIN_GUILD's frame size (0x0168, 14B).
	SizeCZReqJoinGuild = sizeCZReqJoinGuild
	// SizeCZJoinGuild is CZ_JOIN_GUILD's frame size (0x016b, 10B).
	SizeCZJoinGuild = sizeCZJoinGuild
	// SizeCZGuildLeave is CZ_REQ_LEAVE_GUILD's frame size (0x0159, 52B).
	SizeCZGuildLeave = sizeCZGuildLeave
	// SizeCZGuildBan is CZ_REQ_BAN_GUILD's frame size (0x015b, 52B).
	SizeCZGuildBan = sizeCZGuildBan
	// SizeCZGuildBreak is CZ_REQ_DISORGANIZE_GUILD's frame size (0x015d, 44B).
	SizeCZGuildBreak = sizeCZGuildBreak
	// SizeCZGuildCheckMaster is CZ_REQ_GUILD_MENUINTERFACE's frame size
	// (0x014d, 2B).
	SizeCZGuildCheckMaster = 2
)
