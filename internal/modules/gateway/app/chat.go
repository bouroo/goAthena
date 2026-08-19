package app

import (
	"bytes"

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
