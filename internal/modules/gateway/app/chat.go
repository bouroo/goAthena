package app

import (
	"bytes"
	"errors"
	"sort"
	"time"

	"github.com/panjf2000/gnet/v2"

	worlddomain "github.com/bouroo/goAthena/internal/modules/world/domain"
	ropacket "github.com/bouroo/goAthena/pkg/ro/packet"
)

// maxIgnoreList caps per-char ignored names (rAthena map.hpp:80
// MAX_IGNORE_LIST 20; official clients ship 14).
const maxIgnoreList = 20

// handleWhisper delivers CZ_WHISPER (0x0096): the target receives ZC_WHISPER
// (0x09de) carrying sender name/GID + text; the sender receives ZC_ACK_WHISPER
// (0x09df) with result 0 (success) or 1 (target offline).
func (s *MapServer) handleWhisper(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		return
	}
	req, err := ropacket.ParseCZWhisper(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_WHISPER", "err", err)
		s.writeWhisperAck(c, auth.charID, 1)
		return
	}
	sender, gerr := s.world.Get(worlddomain.EntityID(auth.charID))
	if gerr != nil {
		s.log.Warn("map: resolve whisper sender", "gid", auth.charID, "err", gerr)
		s.writeWhisperAck(c, auth.charID, 1)
		return
	}
	target, ok := s.world.PlayerByName(req.TargetNick)
	if !ok {
		s.writeWhisperAck(c, auth.charID, 1)
		return
	}
	targetConn, cok := s.connFor(uint32(target.ID))
	if !cok {
		s.writeWhisperAck(c, auth.charID, 1)
		return
	}
	// rAthena clif_parse_WisMessage delivery gate: deny-all → result 3,
	// sender ignored by name → result 2 (intif.cpp:1295-1305). GM-level
	// bypass is out of scope (no GM levels modeled yet).
	targetID := uint32(target.ID) //nolint:gosec // G115: EntityID wraps char_id.
	s.ignoreMu.RLock()
	denied := s.ignoreAll[targetID] || s.ignores[targetID][sender.Name]
	s.ignoreMu.RUnlock()
	if denied {
		if s.ignoreAll[targetID] {
			s.writeWhisperAck(c, auth.charID, 3)
		} else {
			s.writeWhisperAck(c, auth.charID, 2)
		}
		return
	}
	var wbuf bytes.Buffer
	_ = ropacket.ZCWhisperResponse{ //nolint:errcheck // buffer write cannot fail
		SenderGID: auth.accountID, SenderName: sender.Name, Message: req.Message,
	}.Encode(&wbuf)
	_ = targetConn.AsyncWrite(wbuf.Bytes(), nil)
	s.writeWhisperAck(c, auth.charID, 0)
}

// writeWhisperAck sends the fixed 7-byte ZC_ACK_WHISPER result frame.
func (s *MapServer) writeWhisperAck(c gnet.Conn, charID uint32, result uint8) {
	var abuf bytes.Buffer
	_ = ropacket.ZCAckWhisperResponse{Result: result, CID: charID}.Encode(&abuf) //nolint:errcheck // buffer write cannot fail
	_ = c.AsyncWrite(abuf.Bytes(), nil)
}

// handleGlobalMessage broadcasts CZ_GLOBAL_MESSAGE (0x008c) — public say chat —
// to the speaker's AOI neighbors as ZC_NOTIFY_CHAT (0x008d) carrying
// "<name> : <text>" (rAthena clif_GlobalMessage; the encoder writes the message
// verbatim, so the name prefix is composed here). The speaker's own client
// prints its message locally and is excluded, matching the broadcast helper's
// exclude-actor semantics.
func (s *MapServer) handleGlobalMessage(_ gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		return
	}
	req, err := ropacket.ParseCZGlobalMessage(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_GLOBAL_MESSAGE", "err", err)
		return
	}
	sender, gerr := s.world.Get(worlddomain.EntityID(auth.charID))
	if gerr != nil {
		s.log.Warn("map: resolve chat sender", "gid", auth.charID, "err", gerr)
		return
	}
	var buf bytes.Buffer
	_ = ropacket.NotifyChatResponse{ //nolint:errcheck // buffer write cannot fail
		GID: auth.accountID, Message: sender.Name + " : " + req.Message,
	}.Encode(&buf)
	s.broadcast(buf.Bytes(), sender.Map, sender.Pos, auth.charID)
}

// handleGetCharNameRequest answers CZ_GETCHARNAMEREQUEST (0x0094) — the
// client's mouseover/target name lookup. A PC GID resolves through the full
// ZC_ACK_REQNAMEALL2 (0x0a30: name + empty party/guild/position, title 0);
// an NPC/mob GID resolves the compact ZC_ACK_REQNAMEALL_NPC (0x0adf). A GID
// beyond the AOI radius or unknown to the world gets no reply — the client
// asked about something it cannot see.
func (s *MapServer) handleGetCharNameRequest(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		return
	}
	req, err := ropacket.ParseCZGetCharNameRequest(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_GETCHARNAMEREQUEST", "err", err)
		return
	}
	e, gerr := s.world.Get(worlddomain.EntityID(req.GID))
	if gerr != nil {
		return
	}
	viewer, verr := s.world.Get(worlddomain.EntityID(auth.charID))
	if verr != nil {
		return
	}
	if chebyshev(viewer.Pos.X, viewer.Pos.Y, e.Pos.X, e.Pos.Y) > aoiSweepRadius || viewer.Map != e.Map {
		return
	}
	var buf bytes.Buffer
	if e.Type == worlddomain.EntityTypePC {
		_ = ropacket.ReqNameAll2Response{GID: req.GID, Name: e.Name}.Encode(&buf) //nolint:errcheck // buffer write cannot fail
	} else {
		_ = ropacket.ReqNameAllNPCResponse{GID: req.GID, Name: e.Name}.Encode(&buf) //nolint:errcheck // buffer write cannot fail
	}
	_ = c.AsyncWrite(buf.Bytes(), nil)
}

// chebyshev is the cell distance metric the AOI grid uses.
func chebyshev(x1, y1, x2, y2 int16) int {
	dx := int(x1) - int(x2)
	dy := int(y1) - int(y2)
	if dx < 0 {
		dx = -dx
	}
	if dy < 0 {
		dy = -dy
	}
	if dx > dy {
		return dx
	}
	return dy
}

// handleRequestTime answers CZ_REQUEST_TIME (0x007e) — the client's periodic
// clock ping — with ZC_NOTIFY_TIME (0x007f) carrying the server tick. An
// unanswered ping is why stock clients drop the connection after a while, so
// this is a keep-alive verb, not just latency telemetry.
func (s *MapServer) handleRequestTime(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		return
	}
	if _, err := ropacket.ParseCZRequestTime(frame); err != nil {
		s.log.Warn("map: parse CZ_REQUEST_TIME", "err", err)
		return
	}
	var buf bytes.Buffer
	_ = ropacket.NotifyTimeResponse{ //nolint:errcheck // buffer write cannot fail
		Time: uint32(time.Now().UnixMilli()), //nolint:gosec // G115: client uses low 32 bits for RTT only.
	}.Encode(&buf)
	_ = c.AsyncWrite(buf.Bytes(), nil)
}

// handleReqEmotion broadcasts CZ_REQ_EMOTION (0x00bf) as ZC_EMOTION (0x00c0,
// [4:GID][1:type]) to AOI neighbors. The emotion byte is forwarded verbatim —
// the client owns the icon set — and the actor is excluded because its own
// client renders the icon locally (same exclude-actor semantics as public
// chat). No state to persist; this verb is a pure fan-out.
func (s *MapServer) handleReqEmotion(_ gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		return
	}
	req, err := ropacket.ParseCZReqEmotion(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_REQ_EMOTION", "err", err)
		return
	}
	sender, gerr := s.world.Get(worlddomain.EntityID(auth.charID))
	if gerr != nil {
		return
	}
	var buf bytes.Buffer
	_ = ropacket.EmotionResponse{ //nolint:errcheck // buffer write cannot fail
		GID: auth.charID, Type: req.EmotionType,
	}.Encode(&buf)
	s.broadcast(buf.Bytes(), sender.Map, sender.Pos, auth.charID)
}

// handleChangeDir answers CZ_CHANGE_DIR (0x009b): persist the new body/head
// facing on the world entity, then broadcast ZC_CHANGE_DIR (0x009c,
// [4:GID][2:headDir][1:dir]) to AOI neighbors. The actor is excluded — its
// client already turned locally. Facing lives on the cached entity so later
// spawn/walk frames carry it; rAthena clamps headDir 0..2 upstream and the
// server forwards verbatim.
func (s *MapServer) handleChangeDir(_ gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		return
	}
	req, err := ropacket.ParseCZChangeDir(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_CHANGE_DIR", "err", err)
		return
	}
	sender, gerr := s.world.Get(worlddomain.EntityID(auth.charID))
	if gerr != nil {
		return
	}
	if ferr := s.world.SetFacing(auth.charID, req.Dir, req.HeadDir); ferr != nil && !errors.Is(ferr, worlddomain.ErrEntityNotFound) {
		s.log.Warn("map: persist facing", "gid", auth.charID, "err", ferr)
	}
	var buf bytes.Buffer
	_ = ropacket.ChangeDirResponse{ //nolint:errcheck // buffer write cannot fail
		SrcID: auth.charID, HeadDir: req.HeadDir, Dir: req.Dir,
	}.Encode(&buf)
	s.broadcast(buf.Bytes(), sender.Map, sender.Pos, auth.charID)
}

// handlePMIgnore serves CZ_PMIgnore (0x00cf) — /ex (block) and /in (unblock)
// one name — answering ZC_SETTING_WHISPER_PC (0x00d1) with the request's type
// byte and a result: 0 success, 1 failed (removing a name not on the list),
// 2 too many blocks (list full at maxIgnoreList, rAthena MAX_IGNORE_LIST).
// Duplicate-add reports success without a second entry (Aegis semantics).
func (s *MapServer) handlePMIgnore(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		return
	}
	req, err := ropacket.ParseCZPMIgnore(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_PMIgnore", "err", err)
		return
	}
	result := uint8(0)
	s.ignoreMu.Lock()
	set := s.ignores[auth.charID]
	if set == nil {
		set = make(map[string]bool)
		s.ignores[auth.charID] = set
	}
	if req.Type == 0 {
		switch {
		case set[req.Name]:
			// already ignored — Aegis reports success
		case len(set) >= maxIgnoreList:
			result = 2
		default:
			set[req.Name] = true
		}
	} else {
		switch {
		case !set[req.Name]:
			result = 1 // not on the list
		default:
			delete(set, req.Name)
		}
	}
	s.ignoreMu.Unlock()
	var buf bytes.Buffer
	_ = ropacket.ZCSettingWhisperPCResponse{Type: req.Type, Result: result}.Encode(&buf) //nolint:errcheck // buffer write cannot fail
	_ = c.AsyncWrite(buf.Bytes(), nil)
}

// handleSettingWhisperState serves CZ_SETTING_WHISPER_STATE (0x00d0) — /exall
// (deny all) and /inall (allow all) — answering ZC_SETTING_WHISPER_STATE
// (0x00d2). Per rAthena clif_parse_PMIgnoreAll: /exall fails only when already
// denying; /inall clears the deny flag AND wipes the per-name list (failing
// when neither was set — the client uses that to print "nobody was ignored").
func (s *MapServer) handleSettingWhisperState(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		return
	}
	req, err := ropacket.ParseCZSettingWhisperState(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_SETTING_WHISPER_STATE", "err", err)
		return
	}
	result := uint8(0)
	s.ignoreMu.Lock()
	switch alreadyDenying, hasEntries := s.ignoreAll[auth.charID], len(s.ignores[auth.charID]) > 0; {
	case req.Type == 0 && alreadyDenying:
		result = 1 // already denying all
	case req.Type == 0:
		s.ignoreAll[auth.charID] = true
	case !alreadyDenying && !hasEntries:
		result = 1 // nothing to allow
	default:
		s.ignoreAll[auth.charID] = false
		s.ignores[auth.charID] = make(map[string]bool)
	}
	s.ignoreMu.Unlock()
	var buf bytes.Buffer
	_ = ropacket.ZCSettingWhisperStateResponse{Type: req.Type, Result: result}.Encode(&buf) //nolint:errcheck // buffer write cannot fail
	_ = c.AsyncWrite(buf.Bytes(), nil)
}

// handleReqWhisperList serves CZ_REQ_WHISPER_LIST (0x00d3) — /wl — with
// ZC_WHISPER_LIST (0x00d4): [2:cmd][2:packetSize]{24B names}*, names in list
// order.
func (s *MapServer) handleReqWhisperList(c gnet.Conn, auth *mapAuth, _ []byte) {
	if auth == nil {
		return
	}
	s.ignoreMu.RLock()
	names := make([]string, 0, len(s.ignores[auth.charID]))
	for n := range s.ignores[auth.charID] {
		names = append(names, n)
	}
	s.ignoreMu.RUnlock()
	sort.Strings(names)
	var buf bytes.Buffer
	_ = ropacket.ZCWhisperListResponse{Names: names}.Encode(&buf) //nolint:errcheck // buffer write cannot fail
	_ = c.AsyncWrite(buf.Bytes(), nil)
}
