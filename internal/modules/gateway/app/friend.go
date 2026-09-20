package app

import (
	"context"

	"github.com/panjf2000/gnet/v2"

	friendapp "github.com/bouroo/goAthena/internal/modules/social/friend/app"
	frienddomain "github.com/bouroo/goAthena/internal/modules/social/friend/domain"
	ropacket "github.com/bouroo/goAthena/pkg/ro/packet"
)

// Friend verb handlers. The pair state machine lives in the friend module; this
// file owns the wire conversation and the delivery of each result to the right
// connections.
//
// Requests are held HERE, not in the friend module, for the same reason party
// invitations are: rAthena keeps `sd.friend_req` on the session
// (src/map/clif.cpp:15458-15459) and never persists it. A pending request is
// exactly one (acceptor char → requester), so the map keyed by acceptor char
// id is the whole state — a second request replacing the first falls out free.
type pendingFriendReq struct {
	requesterCharID    uint32
	requesterAccountID uint32
	requesterName      string
}

// SetFriend attaches the friend service post-construction, mirroring SetParty.
// A nil service degrades every friend dispatch entry to a log-and-skip.
func (s *MapServer) SetFriend(svc *friendapp.FriendService) { s.friend = svc }

func (s *MapServer) setFriendReq(acceptorCharID uint32, req pendingFriendReq) {
	s.friendMu.Lock()
	defer s.friendMu.Unlock()
	s.friendReqs[acceptorCharID] = req
}

// takeFriendReq pops the acceptor's pending request, or reports none.
func (s *MapServer) takeFriendReq(acceptorCharID uint32) (pendingFriendReq, bool) {
	s.friendMu.Lock()
	defer s.friendMu.Unlock()
	req, ok := s.friendReqs[acceptorCharID]
	delete(s.friendReqs, acceptorCharID)
	return req, ok
}

// clearFriendReqsFor drops every request touching charID — used on disconnect
// so a stale request cannot be answered after a reconnect (both directions go,
// mirroring clearPartyInvitesFor).
func (s *MapServer) clearFriendReqsFor(charID uint32) {
	s.friendMu.Lock()
	defer s.friendMu.Unlock()
	delete(s.friendReqs, charID)
	for acceptor, req := range s.friendReqs {
		if req.requesterCharID == charID {
			delete(s.friendReqs, acceptor)
		}
	}
}

// handleFriendsAdd processes CZ_ADD_FRIENDS — an add request addressed by the
// target's display name. The target must be online (rAthena resolves it with
// map_nick2sd, clif.cpp:15434); the requester must have room; on success the
// target gets the ZC_REQ_ADD_FRIENDS dialog (clif_friendlist_req, clif.cpp:15417).
func (s *MapServer) handleFriendsAdd(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		return
	}
	if s.friend == nil {
		s.log.Debug("map: friend not wired, ignoring CZ_ADD_FRIENDS")
		return
	}
	req, err := ropacket.ParseCZFriendsAdd(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_ADD_FRIENDS", "err", err)
		return
	}
	target, ok := s.world.PlayerByName(req.Name)
	if !ok {
		// rAthena replies with display message msg_txt 3 (clif.cpp:15438); we
		// have no msg table, so the no-op is the closest wire equivalent.
		s.log.Debug("map: CZ_ADD_FRIENDS target not online", "name", req.Name)
		return
	}
	targetCharID := uint32(target.ID) //nolint:gosec // G115: charID is a uint32 value domain.
	if targetCharID == auth.charID {
		return // rAthena silently returns on self-add (clif.cpp:15440).
	}
	targetConn, ok := s.connFor(targetCharID)
	if !ok {
		return
	}
	ctx := context.Background()
	friends, err := s.friend.List(ctx, auth.charID)
	if err != nil {
		s.log.Debug("map: friend list read failed", "gid", auth.charID, "err", err)
		return
	}
	for _, f := range friends {
		if f.FriendCharID == targetCharID {
			// rAthena displays msg_txt 671 "Friend already exists." (clif.cpp:15446).
			s.log.Debug("map: CZ_ADD_FRIENDS already friends", "gid", auth.charID, "target", targetCharID)
			return
		}
	}
	// The requester's own list is checked at request time; the acceptor's at
	// reply time (clif.cpp:15442-15447 and :15511-15528).
	if len(friends) >= frienddomain.MaxFriends {
		s.writeFriendAddAck(c, ropacket.FriendAddOwnListFull, target.Account, targetCharID, target.Name)
		return
	}
	s.setFriendReq(targetCharID, pendingFriendReq{
		requesterCharID:    auth.charID,
		requesterAccountID: auth.accountID,
		requesterName:      playerName(s.world, auth.charID),
	})
	s.writeFriendReq(targetConn, auth.accountID, auth.charID, playerName(s.world, auth.charID))
}

// handleFriendsReply processes CZ_ACK_REQ_ADD_FRIENDS — the invitee's
// accept/reject. On reject only the requester is told (result 1). On accept
// both lists gain the pair and EACH side gets its own ZC_ADD_FRIENDS(result 0)
// naming the other — rAthena's friend_auto_add default (clif.cpp:15519-15555).
func (s *MapServer) handleFriendsReply(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		return
	}
	if s.friend == nil {
		s.log.Debug("map: friend not wired, ignoring CZ_ACK_REQ_ADD_FRIENDS")
		return
	}
	req, err := ropacket.ParseCZFriendsReply(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_ACK_REQ_ADD_FRIENDS", "err", err)
		return
	}
	inv, ok := s.takeFriendReq(auth.charID)
	if !ok {
		s.log.Debug("map: CZ_ACK_REQ_ADD_FRIENDS with no pending request", "gid", auth.charID)
		return
	}
	// The reply must name the invitation being answered: a stale dialog from an
	// earlier request is not an answer to the current one.
	if inv.requesterCharID != req.CID || inv.requesterAccountID != req.AID {
		s.log.Debug("map: CZ_ACK_REQ_ADD_FRIENDS inviter mismatch", "gid", auth.charID, "want", inv.requesterCharID, "got", req.CID)
		return
	}
	requesterConn, ok := s.connFor(inv.requesterCharID)
	if !ok {
		return // requester left; nothing to report to.
	}
	if req.Reply != ropacket.FriendReplyAccept {
		s.writeFriendAddAck(requesterConn, ropacket.FriendAddRefused, auth.accountID, auth.charID, playerName(s.world, auth.charID))
		return
	}
	acceptorName := playerName(s.world, auth.charID)
	err = s.friend.Add(context.Background(), inv.requesterCharID, auth.charID)
	switch err { //nolint:exhaustive // default covers the wrapped-error tail.
	case nil:
		s.writeFriendAddAck(requesterConn, ropacket.FriendAddOK, auth.accountID, auth.charID, acceptorName)
		// The reverse echo: with auto_add the acceptor's list also gained the
		// pair (clif.cpp:15546-15550), so their client needs it too.
		s.writeFriendAddAck(c, ropacket.FriendAddOK, inv.requesterAccountID, inv.requesterCharID, inv.requesterName)
	case frienddomain.ErrFriendListFull:
		s.writeFriendAddAck(requesterConn, ropacket.FriendAddOwnListFull, auth.accountID, auth.charID, acceptorName)
	case frienddomain.ErrAcceptorListFull:
		s.writeFriendAddAck(requesterConn, ropacket.FriendAddTheirListFull, auth.accountID, auth.charID, acceptorName)
		s.writeFriendAddAck(c, ropacket.FriendAddOwnListFull, inv.requesterAccountID, inv.requesterCharID, inv.requesterName)
	default:
		s.writeFriendAddAck(requesterConn, ropacket.FriendAddRefused, auth.accountID, auth.charID, acceptorName)
	}
}

// handleFriendsRemove processes CZ_DELETE_FRIENDS — the requester drops the
// friend named by (AID, CID). The pair leaves BOTH lists atomically; the
// removed friend's client hears the REMOVER's ids, the requester's own client
// hears the removed friend's ids (clif_parse_FriendsListRemove,
// clif.cpp:15586-15613).
func (s *MapServer) handleFriendsRemove(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		return
	}
	if s.friend == nil {
		s.log.Debug("map: friend not wired, ignoring CZ_DELETE_FRIENDS")
		return
	}
	req, err := ropacket.ParseCZFriendsDelete(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_DELETE_FRIENDS", "err", err)
		return
	}
	friends, err := s.friend.List(context.Background(), auth.charID)
	if err != nil {
		s.log.Debug("map: friend list read failed", "gid", auth.charID, "err", err)
		return
	}
	var target *frienddomain.Friend
	for i := range friends {
		if friends[i].FriendCharID == req.CID && friends[i].FriendAccountID == req.AID {
			target = &friends[i]
			break
		}
	}
	if target == nil {
		// rAthena displays msg_txt 672 "Name not found in list." (clif.cpp:15565).
		s.log.Debug("map: CZ_DELETE_FRIENDS target not in list", "gid", auth.charID, "target", req.CID)
		return
	}
	if err := s.friend.Remove(context.Background(), auth.charID, target.FriendCharID); err != nil {
		s.log.Debug("map: friend remove rejected", "gid", auth.charID, "err", err)
		return
	}
	// Requester's own client: the removed friend's ids.
	s.writeFriendDelete(c, target.FriendAccountID, target.FriendCharID)
	// Removed friend's client (if online): the remover's ids.
	if pc, ok := s.connFor(target.FriendCharID); ok {
		s.writeFriendDelete(pc, auth.accountID, auth.charID)
	}
}

// --- delivery helpers ---

// appendFriendsList appends the ZC_FRIENDS_LIST burst plus one online
// ZC_FRIENDS_STATE per connected friend to buf, for the LoadEndAck coalesce
// (clif_friendslist_send, clif.cpp:15355-15391: list first, then toggles for
// every friend the map registry reports online).
func (s *MapServer) appendFriendsList(buf []byte, friends []frienddomain.Friend) []byte {
	resp := ropacket.FriendsListResponse{}
	states := make([]ropacket.FriendsStateResponse, 0, len(friends))
	for _, f := range friends {
		resp.Friends = append(resp.Friends, ropacket.FriendsListEntry{AID: f.FriendAccountID, CID: f.FriendCharID})
		if f.Online {
			states = append(states, ropacket.FriendsStateResponse{
				AID: f.FriendAccountID, CID: f.FriendCharID, Name: f.Name,
			})
		}
	}
	start := len(buf)
	buf = append(buf, make([]byte, resp.Size())...)
	if err := resp.Encode(sliceWriter(buf[start:])); err != nil {
		s.log.Error("map: encode ZC_FRIENDS_LIST", "err", err)
		return buf[:start]
	}
	for _, st := range states {
		start = len(buf)
		buf = append(buf, make([]byte, st.Size())...)
		if err := st.Encode(sliceWriter(buf[start:])); err != nil {
			s.log.Error("map: encode ZC_FRIENDS_STATE", "err", err)
			return buf[:start]
		}
	}
	return buf
}

// notifyFriendsOnline tells every CONNECTED friend that charID just entered the
// map (the login toggle, clif.cpp:10940-10979). Offline friends are skipped:
// their next map-enter re-reads state.
func (s *MapServer) notifyFriendsOnline(charID uint32, accountID uint32, name string, online bool) {
	if s.friend == nil {
		return
	}
	friends, err := s.friend.List(context.Background(), charID)
	if err != nil {
		return
	}
	for _, f := range friends {
		pc, ok := s.connFor(f.FriendCharID)
		if !ok {
			continue
		}
		s.writeFrame(pc, ropacket.FriendsStateResponse{
			AID: accountID, CID: charID, Offline: !online, Name: name,
		}, "ZC_FRIENDS_STATE")
	}
}

func (s *MapServer) writeFriendReq(c gnet.Conn, aid, cid uint32, name string) {
	s.writeFrame(c, ropacket.FriendsReqAddResponse{AID: aid, CID: cid, Name: name}, "ZC_REQ_ADD_FRIENDS")
}

func (s *MapServer) writeFriendAddAck(c gnet.Conn, result int16, aid, cid uint32, name string) {
	s.writeFrame(c, ropacket.FriendsAddResponse{Result: result, AID: aid, CID: cid, Name: name}, "ZC_ADD_FRIENDS")
}

func (s *MapServer) writeFriendDelete(c gnet.Conn, aid, cid uint32) {
	s.writeFrame(c, ropacket.FriendsDeleteResponse{AID: aid, CID: cid}, "ZC_DELETE_FRIENDS")
}
