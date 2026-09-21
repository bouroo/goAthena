package app

import (
	"context"
	"errors"
	"io"

	"github.com/panjf2000/gnet/v2"

	partyapp "github.com/bouroo/goAthena/internal/modules/social/party/app"
	partydomain "github.com/bouroo/goAthena/internal/modules/social/party/domain"
	worldapp "github.com/bouroo/goAthena/internal/modules/world/app"
	worlddomain "github.com/bouroo/goAthena/internal/modules/world/domain"
	ropacket "github.com/bouroo/goAthena/pkg/ro/packet"
)

// Party (group) verb handlers. The party state machine itself lives in the party
// module; this file owns the wire conversation and the delivery of each result to
// the right connections.
//
// Invitations are held HERE, not in the party module, for the same reason trade
// sessions live in the trade service: rAthena keeps `sd.party_invite` /
// `sd.party_invite_account` on the session (src/map/party.cpp:454-455) and never
// persists them. A pending invite is exactly one (invitee char → partyID), so the
// map keyed by invitee char id is the whole state — it also makes "second invite
// replaces the first" fall out for free.
type pendingInvite struct {
	partyID uint32
	// inviterCharID addresses the inviter's connection for the reply.
	inviterCharID uint32
}

// SetParty attaches the party service post-construction, mirroring SetStorage.
// A nil service degrades every party dispatch entry to a log-and-skip.
func (s *MapServer) SetParty(svc *partyapp.PartyService) { s.party = svc }

// partyInviteSlot returns the pending invite for a char, if any.
func (s *MapServer) partyInviteSlot(charID uint32) (pendingInvite, bool) {
	s.partyMu.RLock()
	defer s.partyMu.RUnlock()
	inv, ok := s.partyInvites[charID]
	return inv, ok
}

func (s *MapServer) setPartyInvite(charID uint32, inv pendingInvite) {
	s.partyMu.Lock()
	defer s.partyMu.Unlock()
	s.partyInvites[charID] = inv
}

func (s *MapServer) clearPartyInvite(charID uint32) {
	s.partyMu.Lock()
	defer s.partyMu.Unlock()
	delete(s.partyInvites, charID)
}

// clearPartyInvitesFor drops every invite whose target is charID — used on
// disconnect so a stale invite cannot be accepted after a reconnect.
func (s *MapServer) clearPartyInvitesFor(charID uint32) {
	s.partyMu.Lock()
	defer s.partyMu.Unlock()
	delete(s.partyInvites, charID)
	for target, inv := range s.partyInvites {
		if inv.inviterCharID == charID {
			delete(s.partyInvites, target)
		}
	}
}

// handleMakeGroup processes CZ_MAKE_GROUP / CZ_MAKE_GROUP2 — create a party led
// by the requester. The reply is ZC_ACK_MAKE_GROUP to the requester alone; the
// client's own roster refresh follows from the ZC_GROUP_LIST below.
func (s *MapServer) handleMakeGroup(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		return
	}
	if s.party == nil {
		s.log.Debug("map: party not wired, ignoring CZ_MAKE_GROUP")
		return
	}
	req, err := ropacket.ParseCZMakeGroup(frame)
	if err != nil {
		if req, err = ropacket.ParseCZMakeGroup2(frame); err != nil {
			s.log.Warn("map: parse CZ_MAKE_GROUP", "err", err)
			return
		}
	}
	p, err := s.party.Create(context.Background(), auth.accountID, auth.charID, req.Name)
	if err != nil {
		s.writeMakeGroupAck(c, makeGroupResult(err))
		return
	}
	s.writeMakeGroupAck(c, ropacket.PartyCreateOK)
	s.sendGroupList(c, p.ID)
}

// handleReqJoinGroup processes CZ_REQ_JOIN_GROUP — an invite addressed by the
// target's account id. The inviter must be a party leader, and every refusal is
// reported to the INVITER via ZC_PARTY_JOIN_REQ_ACK (rAthena clif_party_invite_reply,
// src/map/party.cpp:390-450).
func (s *MapServer) handleReqJoinGroup(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		return
	}
	if s.party == nil {
		s.log.Debug("map: party not wired, ignoring CZ_REQ_JOIN_GROUP")
		return
	}
	req, err := ropacket.ParseCZReqJoinGroup(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_REQ_JOIN_GROUP", "err", err)
		return
	}
	ctx := context.Background()
	party, err := s.party.GetByMember(ctx, auth.charID)
	if err != nil {
		// Not in a party: nothing to invite into. rAthena just shows a message;
		// the ack's empty name + REJECTED is the closest wire equivalent.
		s.writeJoinReqAck(c, "", ropacket.PartyReplyRejected)
		return
	}
	if party.LeaderChar != auth.charID {
		s.writeJoinReqAck(c, "", ropacket.PartyReplyRejected)
		return
	}
	target, ok := s.world.PlayerByAccount(req.AID)
	if !ok {
		s.writeJoinReqAck(c, "", ropacket.PartyReplyOffline)
		return
	}
	targetCharID := uint32(target.ID) //nolint:gosec // G115: charID is a uint32 value domain.
	targetConn, ok := s.connFor(targetCharID)
	if !ok {
		s.writeJoinReqAck(c, target.Name, ropacket.PartyReplyRejected)
		return
	}
	// The target must not already belong to a party (src/map/party.cpp:443-448).
	if _, err := s.party.GetByMember(ctx, targetCharID); err == nil {
		s.writeJoinReqAck(c, target.Name, ropacket.PartyReplyJoinOtherParty)
		return
	}
	// Open-slot check before delivering the invitation (src/map/party.cpp:418-424).
	members, err := s.party.Members(ctx, party.ID)
	if err != nil {
		s.log.Debug("map: party members read failed", "party", party.ID, "err", err)
		return
	}
	if len(members) >= partydomain.MaxPartySize {
		s.writeJoinReqAck(c, target.Name, ropacket.PartyReplyFull)
		return
	}
	s.setPartyInvite(targetCharID, pendingInvite{partyID: uint32(party.ID), inviterCharID: auth.charID})
	s.writeJoinReq(targetConn, uint32(party.ID), party.Name)
}

// handleJoinGroup processes CZ_JOIN_GROUP — the invitee's accept/reject. On
// accept the roster is refreshed for everyone; on reject only the inviter is
// told (src/map/party.cpp:570-604).
func (s *MapServer) handleJoinGroup(_ gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		return
	}
	if s.party == nil {
		s.log.Debug("map: party not wired, ignoring CZ_JOIN_GROUP")
		return
	}
	req, err := ropacket.ParseCZJoinGroup(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_JOIN_GROUP", "err", err)
		return
	}
	inv, ok := s.partyInviteSlot(auth.charID)
	if !ok {
		s.log.Debug("map: CZ_JOIN_GROUP with no pending invite", "gid", auth.charID)
		return
	}
	// The reply must name the invitation being answered: a stale dialog from an
	// earlier party is not an answer to the current one.
	if inv.partyID != req.PartyID {
		s.log.Debug("map: CZ_JOIN_GROUP party mismatch", "gid", auth.charID, "want", inv.partyID, "got", req.PartyID)
		return
	}
	s.clearPartyInvite(auth.charID)
	ctx := context.Background()
	if req.Flag != ropacket.PartyJoinAccept {
		if pc, ok := s.connFor(inv.inviterCharID); ok {
			s.writeJoinReqAck(pc, playerName(s.world, auth.charID), ropacket.PartyReplyRejected)
		}
		return
	}
	if err := s.party.Accept(ctx, partydomain.PartyID(req.PartyID), auth.charID); err != nil {
		if pc, ok := s.connFor(inv.inviterCharID); ok {
			s.writeJoinReqAck(pc, playerName(s.world, auth.charID), joinReplyResult(err))
		}
		return
	}
	if pc, ok := s.connFor(inv.inviterCharID); ok {
		s.writeJoinReqAck(pc, playerName(s.world, auth.charID), ropacket.PartyReplyAccepted)
	}
	s.broadcastGroupList(partydomain.PartyID(req.PartyID))
}

// handleLeaveGroup processes CZ_REQ_LEAVE_GROUP — the requester leaves.
//
// When the leader leaves, rAthena disbands the party and withdraws everyone
// (src/char/int_party.cpp:651-676). Each member therefore gets its own
// ZC_DELETE_MEMBER_FROM_GROUP, so the roster must be read BEFORE the leave is
// applied.
func (s *MapServer) handleLeaveGroup(c gnet.Conn, auth *mapAuth, _ []byte) {
	if auth == nil {
		return
	}
	if s.party == nil {
		s.log.Debug("map: party not wired, ignoring CZ_REQ_LEAVE_GROUP")
		return
	}
	ctx := context.Background()
	party, err := s.party.GetByMember(ctx, auth.charID)
	if err != nil {
		s.log.Debug("map: CZ_REQ_LEAVE_GROUP not in party", "gid", auth.charID)
		return
	}
	members, err := s.party.Members(ctx, party.ID)
	if err != nil {
		s.log.Debug("map: party members read failed", "party", party.ID, "err", err)
		return
	}
	if err := s.party.Leave(ctx, party.ID, auth.charID); err != nil {
		s.log.Debug("map: party leave rejected", "gid", auth.charID, "err", err)
		return
	}
	// The leaver is told directly; the rest are told only if the party survived
	// (a leader's departure took them all out too, one frame each).
	if auth.charID != party.LeaderChar {
		s.writeDeleteMember(c, ropacket.PartyWithdrawLeave, auth.accountID, playerName(s.world, auth.charID))
	}
	for _, m := range members {
		if m.CharID == auth.charID {
			continue
		}
		if pc, ok := s.connFor(m.CharID); ok {
			s.writeDeleteMember(pc, ropacket.PartyWithdrawLeave, m.AccountID, m.Name)
		}
	}
}

// handleExpelGroupMember processes CZ_REQ_EXPEL_GROUP_MEMBER — the leader kicks a
// member (src/map/party.cpp:699-731).
func (s *MapServer) handleExpelGroupMember(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		return
	}
	if s.party == nil {
		s.log.Debug("map: party not wired, ignoring CZ_REQ_EXPEL_GROUP_MEMBER")
		return
	}
	req, err := ropacket.ParseCZReqExpelGroupMember(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_REQ_EXPEL_GROUP_MEMBER", "err", err)
		return
	}
	ctx := context.Background()
	party, err := s.party.GetByMember(ctx, auth.charID)
	if err != nil {
		s.log.Debug("map: CZ_REQ_EXPEL_GROUP_MEMBER not in party", "gid", auth.charID)
		return
	}
	members, err := s.party.Members(ctx, party.ID)
	if err != nil {
		s.log.Debug("map: party members read failed", "party", party.ID, "err", err)
		return
	}
	// The client names the target by (account id, name). Resolve the char from
	// the roster rather than trusting either field alone.
	var target *partydomain.PartyMember
	for i := range members {
		if members[i].AccountID == req.AID && members[i].Name == req.Name {
			target = &members[i]
			break
		}
	}
	if target == nil {
		s.writeDeleteMember(c, ropacket.PartyWithdrawCantExpel, req.AID, req.Name)
		return
	}
	if err := s.party.Kick(ctx, party.ID, auth.charID, target.CharID); err != nil {
		s.writeDeleteMember(c, ropacket.PartyWithdrawCantExpel, req.AID, req.Name)
		return
	}
	s.writeDeleteMember(c, ropacket.PartyWithdrawExpel, target.AccountID, target.Name)
	if pc, ok := s.connFor(target.CharID); ok {
		s.writeDeleteMember(pc, ropacket.PartyWithdrawExpel, target.AccountID, target.Name)
	}
	s.broadcastGroupList(party.ID)
}

// handleChangeGroupExpOption processes CZ_CHANGE_GROUPEXPOPTION — the leader
// flips the exp-share rule. Only the exp flag is settable on this opcode; the
// item rules keep their current value (src/map/clif.cpp:13956).
func (s *MapServer) handleChangeGroupExpOption(_ gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		return
	}
	if s.party == nil {
		s.log.Debug("map: party not wired, ignoring CZ_CHANGE_GROUPEXPOPTION")
		return
	}
	req, err := ropacket.ParseCZChangeGroupExpOption(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_CHANGE_GROUPEXPOPTION", "err", err)
		return
	}
	ctx := context.Background()
	party, err := s.party.GetByMember(ctx, auth.charID)
	if err != nil {
		return
	}
	// expflag is a boolean on the wire: any non-zero means share.
	exp := uint8(0)
	if req.ExpFlag != 0 {
		exp = 1
	}
	if err := s.party.SetOptions(ctx, party.ID, auth.charID, exp, party.Item); err != nil {
		s.log.Debug("map: party option change rejected", "gid", auth.charID, "err", err)
		return
	}
	s.broadcastGroupList(party.ID)
}

// --- delivery helpers ---

// sendGroupList writes a ZC_GROUP_LIST burst for the party to one connection.
func (s *MapServer) sendGroupList(c gnet.Conn, id partydomain.PartyID) {
	party, err := s.party.Get(context.Background(), id)
	if err != nil {
		return
	}
	members, err := s.party.Members(context.Background(), id)
	if err != nil {
		return
	}
	s.writeGroupList(c, party, members)
}

// broadcastGroupList refreshes the roster on every CONNECTED member's own
// connection. Offline members are skipped: their next map-enter re-reads state.
func (s *MapServer) broadcastGroupList(id partydomain.PartyID) {
	party, err := s.party.Get(context.Background(), id)
	if err != nil {
		return
	}
	members, err := s.party.Members(context.Background(), id)
	if err != nil {
		return
	}
	for _, m := range members {
		if pc, ok := s.connFor(m.CharID); ok {
			s.writeGroupList(pc, party, members)
		}
	}
}

func (s *MapServer) writeGroupList(c gnet.Conn, party partydomain.Party, members []partydomain.PartyMember) {
	resp := groupListFrame(party, members)
	buf := make([]byte, resp.Size())
	if err := resp.Encode(sliceWriter(buf)); err != nil {
		s.log.Error("map: encode ZC_GROUP_LIST", "err", err)
		return
	}
	_ = c.AsyncWrite(buf, nil)
}

// appendGroupList appends an encoded ZC_GROUP_LIST to buf, for callers that
// coalesce several frames into one write (the LoadEndAck burst).
func (s *MapServer) appendGroupList(buf []byte, party partydomain.Party, members []partydomain.PartyMember) []byte {
	resp := groupListFrame(party, members)
	start := len(buf)
	buf = append(buf, make([]byte, resp.Size())...)
	if err := resp.Encode(sliceWriter(buf[start:])); err != nil {
		s.log.Error("map: encode ZC_GROUP_LIST", "err", err)
		return buf[:start]
	}
	return buf
}

// encodePartyConfig encodes a ZC_PARTY_CONFIG frame for callers that coalesce
// frames into one write.
func encodePartyConfig(deny uint8) []byte {
	resp := ropacket.PartyConfigResponse{Deny: deny}
	buf := make([]byte, resp.Size())
	if err := resp.Encode(sliceWriter(buf)); err != nil {
		return nil
	}
	return buf
}

// groupListFrame builds the ZC_GROUP_LIST reply from the roster.
func groupListFrame(party partydomain.Party, members []partydomain.PartyMember) ropacket.GroupListResponse {
	resp := ropacket.GroupListResponse{PartyName: party.Name}
	for _, m := range members {
		resp.Members = append(resp.Members, ropacket.GroupListMember{
			AID:        m.AccountID,
			GID:        m.CharID,
			PlayerName: m.Name,
			MapName:    m.Map,
			Leader:     m.Leader,
			Online:     m.Online,
			Class:      m.Class,
			BaseLevel:  m.BaseLevel,
		})
	}
	return resp
}

func (s *MapServer) writeMakeGroupAck(c gnet.Conn, result uint8) {
	s.writeFrame(c, ropacket.AckMakeGroupResponse{Result: result}, "ZC_ACK_MAKE_GROUP")
}

func (s *MapServer) writeJoinReq(c gnet.Conn, partyID uint32, partyName string) {
	s.writeFrame(c, ropacket.PartyJoinReqResponse{PartyID: partyID, PartyName: partyName}, "ZC_PARTY_JOIN_REQ")
}

func (s *MapServer) writeJoinReqAck(c gnet.Conn, name string, result int32) {
	s.writeFrame(c, ropacket.PartyJoinReqAckResponse{CharacterName: name, Result: result}, "ZC_PARTY_JOIN_REQ_ACK")
}

func (s *MapServer) writeDeleteMember(c gnet.Conn, result int8, aid uint32, name string) {
	s.writeFrame(c, ropacket.DeleteMemberFromGroupResponse{AID: aid, CharacterName: name, Result: result}, "ZC_DELETE_MEMBER_FROM_GROUP")
}

// partyFrame is what every party reply has in common: a pre-sized byte length and
// an Encode into it.
type partyFrame interface {
	Size() int
	Encode(w io.Writer) error
}

// writeFrame sizes, encodes, and writes one party reply. The name is only for
// the error log — a failed encode must never take the connection down.
func (s *MapServer) writeFrame(c gnet.Conn, resp partyFrame, name string) {
	buf := make([]byte, resp.Size())
	if err := resp.Encode(sliceWriter(buf)); err != nil {
		s.log.Error("map: encode "+name, "err", err)
		return
	}
	_ = c.AsyncWrite(buf, nil)
}

// playerName resolves an online char's display name, or "" when it has already
// left the registry.
func playerName(w *worldapp.WorldService, charID uint32) string {
	e, err := w.Get(worlddomain.EntityID(charID))
	if err != nil {
		return ""
	}
	return e.Name
}

// makeGroupResult maps a create failure onto ZC_ACK_MAKE_GROUP's result byte.
func makeGroupResult(err error) uint8 {
	switch {
	case errors.Is(err, partydomain.ErrAlreadyInParty):
		return ropacket.PartyCreateAlreadyInParty
	case errors.Is(err, partydomain.ErrEmptyPartyName):
		return ropacket.PartyCreateNameExists
	default:
		return ropacket.PartyCreateNameExists
	}
}

// joinReplyResult maps an accept failure onto ZC_PARTY_JOIN_REQ_ACK's result.
func joinReplyResult(err error) int32 {
	switch {
	case errors.Is(err, partydomain.ErrPartyFull):
		return ropacket.PartyReplyFull
	case errors.Is(err, partydomain.ErrAlreadyInParty):
		return ropacket.PartyReplyJoinOtherParty
	case errors.Is(err, partydomain.ErrPartyNotFound):
		return ropacket.PartyReplyUnknownError
	default:
		return ropacket.PartyReplyUnknownError
	}
}
