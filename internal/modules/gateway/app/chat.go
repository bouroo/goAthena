package app

import (
	"bytes"
	"errors"
	"time"

	"github.com/panjf2000/gnet/v2"

	worlddomain "github.com/bouroo/goAthena/internal/modules/world/domain"
	ropacket "github.com/bouroo/goAthena/pkg/ro/packet"
)

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
