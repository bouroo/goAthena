package app

import (
	"context"
	"errors"
	"strings"

	"github.com/panjf2000/gnet/v2"

	guildapp "github.com/bouroo/goAthena/internal/modules/social/guild/app"
	guilddomain "github.com/bouroo/goAthena/internal/modules/social/guild/domain"
	ropacket "github.com/bouroo/goAthena/pkg/ro/packet"
)

// Guild verb handlers. The guild state machine itself lives in the guild
// module; this file owns the wire conversation and the delivery of each result
// to the right connections.
//
// Invitations are held HERE, not in the guild module, for the same reason party
// invitations are: rAthena keeps `sd.guild_invite` / `sd.guild_invite_account`
// on the session and never persists them. A pending invite is exactly one
// (invitee char → guildID), so the map keyed by invitee char id is the whole
// state.
type pendingGuildInvite struct {
	guildID uint32
	// inviterCharID addresses the inviter's connection for the reply.
	inviterCharID uint32
}

// maxGuildChat is the guild chat line cap on the wire (rAthena clamps the
// CZ_GUILD_CHAT body into the ZC frame; 98 keeps the total under 100 bytes,
// matching the chat-family convention).
const maxGuildChat = 98

// SetGuild attaches the guild service post-construction, mirroring SetParty.
// A nil service degrades every guild dispatch entry to a log-and-skip.
func (s *MapServer) SetGuild(svc *guildapp.GuildService) { s.guild = svc }

// guildInviteSlot returns the pending invite for a char, if any.
func (s *MapServer) guildInviteSlot(charID uint32) (pendingGuildInvite, bool) {
	s.guildMu.RLock()
	defer s.guildMu.RUnlock()
	inv, ok := s.guildInvites[charID]
	return inv, ok
}

func (s *MapServer) setGuildInvite(charID uint32, inv pendingGuildInvite) {
	s.guildMu.Lock()
	defer s.guildMu.Unlock()
	s.guildInvites[charID] = inv
}

func (s *MapServer) clearGuildInvite(charID uint32) {
	s.guildMu.Lock()
	defer s.guildMu.Unlock()
	delete(s.guildInvites, charID)
}

// clearGuildInvitesFor drops every invite touching charID — used on disconnect
// so a stale invite cannot be answered after a reconnect.
func (s *MapServer) clearGuildInvitesFor(charID uint32) {
	s.guildMu.Lock()
	defer s.guildMu.Unlock()
	delete(s.guildInvites, charID)
	for target, inv := range s.guildInvites {
		if inv.inviterCharID == charID {
			delete(s.guildInvites, target)
		}
	}
}

// handleCreateGuild processes CZ_REQ_MAKE_GUILD — create a guild led by the
// requester (rAthena guild_create). The reply is ZC_RESULT_MAKE_GUILD to the
// requester alone; a success additionally delivers the belong-info, guild-info
// and roster burst so the client's guild window opens populated.
func (s *MapServer) handleCreateGuild(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		return
	}
	if s.guild == nil {
		s.log.Debug("map: guild not wired, ignoring CZ_REQ_MAKE_GUILD")
		return
	}
	req, err := ropacket.ParseCZCreateGuild(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_REQ_MAKE_GUILD", "err", err)
		return
	}
	// Blank names are silently dropped in rAthena (guild.cpp:696 — the parser
	// returns false before any ack); mirror that rather than acking a failure.
	if strings.TrimSpace(req.Name) == "" {
		return
	}
	g, err := s.guild.Create(context.Background(), auth.accountID, auth.charID, req.Name)
	if err != nil {
		s.writeCreateGuildAck(c, createGuildResult(err))
		return
	}
	s.writeCreateGuildAck(c, ropacket.GuildCreateOK)
	var burst []byte
	burst = s.appendGuildBurst(burst, g)
	_ = c.AsyncWrite(burst, nil)
}

// handleGuildInvite processes CZ_REQ_JOIN_GUILD — an invite addressed by the
// target's account id. The inviter must be the guild master (positions with
// invite permission are a later commit), and every refusal is reported to the
// INVITER via ZC_ACK_REQ_JOIN_GUILD (rAthena guild_invite, guild.cpp:925-970).
func (s *MapServer) handleGuildInvite(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		return
	}
	if s.guild == nil {
		s.log.Debug("map: guild not wired, ignoring CZ_REQ_JOIN_GUILD")
		return
	}
	req, err := ropacket.ParseCZReqJoinGuild(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_REQ_JOIN_GUILD", "err", err)
		return
	}
	ctx := context.Background()
	g, err := s.guild.GetByMember(ctx, auth.charID)
	if err != nil {
		s.writeInviteAck(c, ropacket.GuildInviteRejected)
		return
	}
	if g.Master != auth.charID {
		s.writeInviteAck(c, ropacket.GuildInviteRejected)
		return
	}
	target, ok := s.world.PlayerByAccount(req.AID)
	if !ok {
		s.writeInviteAck(c, ropacket.GuildInviteRejected)
		return
	}
	targetCharID := uint32(target.ID) //nolint:gosec // G115: charID is a uint32 value domain.
	targetConn, ok := s.connFor(targetCharID)
	if !ok {
		s.writeInviteAck(c, ropacket.GuildInviteRejected)
		return
	}
	// The target must not already belong to a guild (guild.cpp:943 — flag 0).
	if _, err := s.guild.GetByMember(ctx, targetCharID); err == nil {
		s.writeInviteAck(c, ropacket.GuildInviteAlreadyIn)
		return
	}
	// Open-slot check before delivering the invitation (guild.cpp:948 — flag 3).
	members, err := s.guild.Members(ctx, g.ID)
	if err != nil {
		s.log.Debug("map: guild members read failed", "guild", g.ID, "err", err)
		return
	}
	if len(members) >= int(g.MaxMember) {
		s.writeInviteAck(c, ropacket.GuildInviteFull)
		return
	}
	s.setGuildInvite(targetCharID, pendingGuildInvite{guildID: uint32(g.ID), inviterCharID: auth.charID})
	s.writeFrame(targetConn, ropacket.ReqJoinGuildResponse{GuildID: uint32(g.ID), GuildName: g.Name}, "ZC_REQ_JOIN_GUILD")
}

// handleGuildReplyInvite processes CZ_JOIN_GUILD — the invitee's accept/reject.
// On accept the roster is refreshed for everyone; on reject only the inviter is
// told (guild.cpp:972-1008).
func (s *MapServer) handleGuildReplyInvite(_ gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		return
	}
	if s.guild == nil {
		s.log.Debug("map: guild not wired, ignoring CZ_JOIN_GUILD")
		return
	}
	req, err := ropacket.ParseCZJoinGuild(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_JOIN_GUILD", "err", err)
		return
	}
	inv, ok := s.guildInviteSlot(auth.charID)
	if !ok {
		s.log.Debug("map: CZ_JOIN_GUILD with no pending invite", "gid", auth.charID)
		return
	}
	// The reply must name the invitation being answered: a stale dialog from an
	// earlier guild is not an answer to the current one.
	if inv.guildID != req.GuildID {
		s.log.Debug("map: CZ_JOIN_GUILD guild mismatch", "gid", auth.charID, "want", inv.guildID, "got", req.GuildID)
		return
	}
	s.clearGuildInvite(auth.charID)
	ctx := context.Background()
	if req.Answer != ropacket.GuildJoinAccept {
		if pc, ok := s.connFor(inv.inviterCharID); ok {
			s.writeInviteAck(pc, ropacket.GuildInviteRejected)
		}
		return
	}
	if err := s.guild.Accept(ctx, guilddomain.GuildID(req.GuildID), auth.charID); err != nil {
		if pc, ok := s.connFor(inv.inviterCharID); ok {
			s.writeInviteAck(pc, guildInviteResult(err))
		}
		return
	}
	if pc, ok := s.connFor(inv.inviterCharID); ok {
		s.writeInviteAck(pc, ropacket.GuildInviteAccepted)
	}
	s.broadcastGuildMembers(guilddomain.GuildID(req.GuildID))
}

// handleGuildLeave processes CZ_REQ_LEAVE_GUILD — the requester leaves. The
// notice goes to the whole guild INCLUDING the leaver (rAthena clif_guild_leave
// GUILD_NOBG), which is also what clears the leaver's own guild window.
func (s *MapServer) handleGuildLeave(_ gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		return
	}
	if s.guild == nil {
		s.log.Debug("map: guild not wired, ignoring CZ_REQ_LEAVE_GUILD")
		return
	}
	req, err := ropacket.ParseCZGuildLeave(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_REQ_LEAVE_GUILD", "err", err)
		return
	}
	ctx := context.Background()
	g, err := s.guild.GetByMember(ctx, auth.charID)
	if err != nil || uint32(g.ID) != req.GuildID {
		s.log.Debug("map: CZ_REQ_LEAVE_GUILD not in guild", "gid", auth.charID)
		return
	}
	members, err := s.guild.Members(ctx, g.ID)
	if err != nil {
		s.log.Debug("map: guild members read failed", "guild", g.ID, "err", err)
		return
	}
	if err := s.guild.Leave(ctx, g.ID, auth.charID); err != nil {
		s.log.Debug("map: guild leave rejected", "gid", auth.charID, "err", err)
		return
	}
	for _, m := range members {
		if pc, ok := s.connFor(m.CharID); ok {
			s.writeFrame(pc, ropacket.AckLeaveGuildResponse{GID: auth.charID, Reason: req.Message}, "ZC_ACK_LEAVE_GUILD")
		}
	}
}

// handleGuildBan processes CZ_REQ_BAN_GUILD — the master expels a member
// (guild.cpp:1189-1237). The notice goes to the whole guild including the
// expelled char (clif_guild_expulsion GUILD_NOBG).
func (s *MapServer) handleGuildBan(_ gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		return
	}
	if s.guild == nil {
		s.log.Debug("map: guild not wired, ignoring CZ_REQ_BAN_GUILD")
		return
	}
	req, err := ropacket.ParseCZGuildBan(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_REQ_BAN_GUILD", "err", err)
		return
	}
	ctx := context.Background()
	g, err := s.guild.GetByMember(ctx, auth.charID)
	if err != nil || uint32(g.ID) != req.GuildID {
		s.log.Debug("map: CZ_REQ_BAN_GUILD not in guild", "gid", auth.charID)
		return
	}
	members, err := s.guild.Members(ctx, g.ID)
	if err != nil {
		s.log.Debug("map: guild members read failed", "guild", g.ID, "err", err)
		return
	}
	if err := s.guild.Kick(ctx, g.ID, auth.charID, req.CID); err != nil {
		s.log.Debug("map: guild ban rejected", "gid", auth.charID, "target", req.CID, "err", err)
		return
	}
	for _, m := range members {
		if pc, ok := s.connFor(m.CharID); ok {
			s.writeFrame(pc, ropacket.AckBanGuildResponse{GID: req.CID, Reason: req.Message}, "ZC_ACK_BAN_GUILD")
		}
	}
}

// handleGuildBreak processes CZ_REQ_DISORGANIZE_GUILD — the master disbands the
// guild. rAthena requires the key to echo the guild name, the actor to be the
// master, and every OTHER member to be offline (guild.cpp:2289-2310); the
// service enforces those rules and the result is ZC_ACK_DISORGANIZE_GUILD_RESULT.
func (s *MapServer) handleGuildBreak(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		return
	}
	if s.guild == nil {
		s.log.Debug("map: guild not wired, ignoring CZ_REQ_DISORGANIZE_GUILD")
		return
	}
	req, err := ropacket.ParseCZGuildBreak(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_REQ_DISORGANIZE_GUILD", "err", err)
		return
	}
	ctx := context.Background()
	g, err := s.guild.GetByMember(ctx, auth.charID)
	if err != nil {
		return
	}
	if err := s.guild.Break(ctx, g.ID, auth.charID, req.Key); err != nil {
		if !errors.Is(err, guilddomain.ErrMembersOnline) {
			s.log.Debug("map: guild break rejected", "gid", auth.charID, "err", err)
			return
		}
		s.writeFrame(c, ropacket.AckDisorganizeGuildResponse{Result: ropacket.GuildBrokenHasMem}, "ZC_ACK_DISORGANIZE_GUILD_RESULT")
		return
	}
	s.writeFrame(c, ropacket.AckDisorganizeGuildResponse{Result: ropacket.GuildBrokenOK}, "ZC_ACK_DISORGANIZE_GUILD_RESULT")
}

// handleGuildChat processes CZ_GUILD_CHAT — a line broadcast to the whole
// guild, sender included (rAthena clif_guild_message prefixes the speaker name
// and sends ZC_GUILD_CHAT to GUILD_NOBG).
func (s *MapServer) handleGuildChat(_ gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		return
	}
	if s.guild == nil {
		s.log.Debug("map: guild not wired, ignoring CZ_GUILD_CHAT")
		return
	}
	msg, err := ropacket.ParseCZGuildChat(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_GUILD_CHAT", "err", err)
		return
	}
	if len(msg) > maxGuildChat {
		msg = msg[:maxGuildChat]
	}
	senderName := playerName(s.world, auth.charID)
	line := msg
	if senderName != "" {
		line = senderName + " : " + msg
	}
	g, err := s.guild.GetByMember(context.Background(), auth.charID)
	if err != nil {
		return
	}
	members, err := s.guild.Members(context.Background(), g.ID)
	if err != nil {
		return
	}
	resp := ropacket.GuildChatResponse{Message: line}
	for _, m := range members {
		if pc, ok := s.connFor(m.CharID); ok {
			s.writeFrame(pc, resp, "ZC_GUILD_CHAT")
		}
	}
}

// handleGuildCheckMaster processes CZ_REQ_GUILD_MENUINTERFACE (0x014d) — the
// client's guild-window permission poll. The reply names whether this char is
// the master (clif_guild_masterormember, clif.cpp:8762).
func (s *MapServer) handleGuildCheckMaster(c gnet.Conn, auth *mapAuth, _ []byte) {
	if auth == nil {
		return
	}
	if s.guild == nil {
		return
	}
	isMaster := false
	if g, err := s.guild.GetByMember(context.Background(), auth.charID); err == nil {
		isMaster = g.Master == auth.charID
	}
	s.writeFrame(c, ropacket.AckMenuInterfaceResponse{IsMaster: isMaster}, "ZC_ACK_GUILD_MENUINTERFACE")
}

// guildMode is the ZC_UPDATE_GDID permission bitmask: 0x01 invite, 0x10 expel.
// The master holds both; ordinary members hold neither this slice (positions
// land with guild_position support).
func guildMode(master bool) uint32 {
	if master {
		return 0x11
	}
	return 0
}

// --- delivery helpers ---

// appendGuildBurst encodes the belong-info + guild-info + member-list frames a
// guild member receives on entry (rAthena clif_guild_belonginfo,
// clif_guild_info, clif_guild_memberlist) into buf.
func (s *MapServer) appendGuildBurst(buf []byte, g guilddomain.Guild) []byte {
	members, err := s.guild.Members(context.Background(), g.ID)
	if err != nil {
		return buf
	}
	frames := []partyFrame{
		guildBelongInfo(g, members),
		guildInfoFrame(g, members),
		guildMembersFrame(members),
	}
	for _, f := range frames {
		start := len(buf)
		buf = append(buf, make([]byte, f.Size())...)
		if err := f.Encode(sliceWriter(buf[start:])); err != nil {
			s.log.Error("map: encode guild burst", "err", err)
			return buf[:start]
		}
	}
	return buf
}

// broadcastGuildMembers refreshes the member list on every CONNECTED member's
// own connection. Offline members are skipped: their next map-enter re-reads
// state.
func (s *MapServer) broadcastGuildMembers(id guilddomain.GuildID) {
	members, err := s.guild.Members(context.Background(), id)
	if err != nil {
		return
	}
	resp := guildMembersFrame(members)
	for _, m := range members {
		if pc, ok := s.connFor(m.CharID); ok {
			buf := make([]byte, resp.Size())
			if err := resp.Encode(sliceWriter(buf)); err != nil {
				s.log.Error("map: encode ZC_MEMBERMGR_INFO", "err", err)
				return
			}
			_ = pc.AsyncWrite(buf, nil)
		}
	}
}

// guildBelongInfo builds ZC_UPDATE_GDID for a guild member.
func guildBelongInfo(g guilddomain.Guild, members []guilddomain.GuildMember) ropacket.UpdateGDIDResponse {
	masterCID := uint32(0)
	isMaster := false
	for _, m := range members {
		if m.Master {
			masterCID = m.CharID
			isMaster = m.Master
			break
		}
	}
	return ropacket.UpdateGDIDResponse{
		GuildID:   uint32(g.ID),
		Mode:      guildMode(isMaster),
		IsMaster:  isMaster,
		GuildName: g.Name,
		MasterGID: masterCID,
	}
}

// guildInfoFrame builds the ZC_GUILD_INFO response from the guild row and its
// roster: level, member counts, average level, and the master identity.
func guildInfoFrame(g guilddomain.Guild, members []guilddomain.GuildMember) ropacket.GuildInfoResponse {
	resp := ropacket.GuildInfoResponse{
		GuildID:    uint32(g.ID),
		Level:      uint32(g.GuildLv),
		UserNum:    uint32(len(members)), //nolint:gosec // G115: bounded by MaxGuildSize.
		MaxUserNum: uint32(g.MaxMember),  //nolint:gosec // G115: bounded by MaxGuildSize.
		GuildName:  g.Name,
	}
	sum := 0
	for _, m := range members {
		sum += int(m.BaseLevel)
		if m.Master {
			resp.MasterGID = m.CharID
			resp.MasterName = m.Name
		}
	}
	if len(members) > 0 {
		resp.AvgLevel = uint32(sum / len(members)) //nolint:gosec // G115: bounded.
	}
	return resp
}

// guildMembersFrame builds the ZC_MEMBERMGR_INFO response from the roster.
func guildMembersFrame(members []guilddomain.GuildMember) ropacket.MemberMgrInfoResponse {
	resp := ropacket.MemberMgrInfoResponse{}
	for _, m := range members {
		state := uint32(0)
		if m.Online {
			state = 1
		}
		resp.Members = append(resp.Members, ropacket.GuildMemberInfo{
			AID:      m.AccountID,
			GID:      m.CharID,
			Job:      m.Class,
			Level:    m.BaseLevel,
			State:    state,
			Position: m.Position,
			CharName: m.Name,
		})
	}
	return resp
}

func (s *MapServer) writeCreateGuildAck(c gnet.Conn, result uint8) {
	s.writeFrame(c, ropacket.ResultMakeGuildResponse{Result: result}, "ZC_RESULT_MAKE_GUILD")
}

func (s *MapServer) writeInviteAck(c gnet.Conn, result int32) {
	s.writeFrame(c, ropacket.AckReqJoinGuildResponse{Result: result}, "ZC_ACK_REQ_JOIN_GUILD")
}

// createGuildResult maps a create failure onto ZC_RESULT_MAKE_GUILD's flag.
func createGuildResult(err error) uint8 {
	switch {
	case errors.Is(err, guilddomain.ErrAlreadyInGuild):
		return ropacket.GuildCreateAlreadyIn
	case errors.Is(err, guilddomain.ErrNameExists):
		return ropacket.GuildCreateDuplicateName
	default:
		return ropacket.GuildCreateDuplicateName
	}
}

// guildInviteResult maps an accept failure onto ZC_ACK_REQ_JOIN_GUILD's flag.
func guildInviteResult(err error) int32 {
	switch {
	case errors.Is(err, guilddomain.ErrGuildFull):
		return ropacket.GuildInviteFull
	case errors.Is(err, guilddomain.ErrAlreadyInGuild):
		return ropacket.GuildInviteAlreadyIn
	default:
		return ropacket.GuildInviteRejected
	}
}
