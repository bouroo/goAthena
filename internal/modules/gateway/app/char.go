package app

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/panjf2000/gnet/v2"

	charapp "github.com/bouroo/goAthena/internal/modules/character/app"
	chardomain "github.com/bouroo/goAthena/internal/modules/character/domain"
	ropacket "github.com/bouroo/goAthena/pkg/ro/packet"
)

// charEnterFrameSize is the wire length of CH_ENTER (0x0065): 2 cmd +
// 4 AID + 4 loginID1 + 4 loginID2 + 2 reserved + 1 sex = 17. Matches
// pkg/ro/packet sizeCHEnter (unexported there).
const charEnterFrameSize = 17

// charSelectFrameSize is the wire length of CH_SELECT_CHAR (0x0066):
// 2 cmd + 1 slot = 3. Matches pkg/ro/packet sizeCHSelectChar.
const charSelectFrameSize = 3

// charMakeFrameSize is the wire length of CH_MAKE_CHAR (0x0a39):
// 2 cmd + 24 name + 1 slot + 2 hair_color + 2 hair_style + 4 job + 1 sex = 36.
// Matches pkg/ro/packet sizeCHMakeChar.
const charMakeFrameSize = 36

// maxChars is the upper bound on character slots advertised to the client.
const maxChars = 9

// HC_REFUSE_MAKECHAR error bytes (mirror rAthena's char_make_new_char return
// mapping documented on ropacket.RefuseMakeCharResponse).
const (
	makeCharRefuseNameTaken uint8 = 0x00 // 0x00 = charname already exists / reserved name
	makeCharRefuseSlotTaken uint8 = 0x03 // 0x03 = slot not eligible
	makeCharRefuseDenied    uint8 = 0xFF // 0xFF = char creation denied / invalid input
)

// CharServer is the gnet TCP listener for the char-select protocol.
type CharServer struct {
	gnet.BuiltinEventEngine
	engine  gnet.Engine
	booted  bool
	chars   *charapp.CharService
	log     *slog.Logger
	mapIP   uint32 // advertised map-server IPv4 (wire uint32)
	mapPort uint16 // advertised map-server port
	// handlers is the opcode→handler dispatch table. CH_ENTER admits the
	// connection; CH_MAKE_CHAR/CH_SELECT_CHAR are only valid post-auth.
	handlers map[uint16]charHandler
	// db is the wire-length oracle for opcodes that have no wired handler.
	// OnTraffic consults it to skip a DB-registered frame instead of
	// disconnecting the client.
	db *ropacket.DB
}

// NewCharServer builds a char-select listener. mapHost/mapPort are the map-server
// endpoint advertised inside NotifyZoneServerResponse when the client picks a
// character to enter the world with.
func NewCharServer(chars *charapp.CharService, log *slog.Logger, mapHost string, mapPort uint16) (*CharServer, error) {
	ip, err := ipToWire(mapHost)
	if err != nil {
		return nil, fmt.Errorf("map server host %q: %w", mapHost, err)
	}
	return &CharServer{
		chars:    chars,
		log:      log,
		mapIP:    ip,
		mapPort:  mapPort,
		handlers: charHandlers(),
		db:       ropacket.NewCharServerDB(),
	}, nil
}

// charHandler is one entry in the char-server dispatch table: a fixed frame size
// and the function that processes a complete frame of that opcode.
type charHandler struct {
	size int
	fn   func(s *CharServer, c gnet.Conn, frame []byte)
}

// charHandlers is the opcode→handler table the char server dispatches against.
// Every char packet is fixed-length today. The connection's first packet is
// always CH_ENTER (the trust gate); CH_MAKE_CHAR/CH_SELECT_CHAR are only valid
// post-auth.
func charHandlers() map[uint16]charHandler {
	return map[uint16]charHandler{
		ropacket.HeaderCHENTER:      {size: charEnterFrameSize, fn: (*CharServer).handleEnter},
		ropacket.HeaderCHSELECTCHAR: {size: charSelectFrameSize, fn: (*CharServer).handleSelectChar},
		ropacket.HeaderCHMAKECHAR:   {size: charMakeFrameSize, fn: (*CharServer).handleMakeChar},
	}
}

// OnBoot captures the engine for shutdown.
func (s *CharServer) OnBoot(e gnet.Engine) gnet.Action {
	s.engine = e
	s.booted = true
	s.log.Info("char listener booted")
	return gnet.None
}

// OnTraffic is the table-driven dispatcher: peek the 2-byte opcode, look up the
// handler + frame size, read the full frame, and dispatch on a goroutine so the
// reactor never blocks. An opcode without a wired handler is not fatal: the
// packet DB supplies its on-wire length (or, for an opcode unknown to the DB
// too, the 2-byte header) so the frame is skipped and the connection stays
// alive — a client sending a not-yet-wired verb must not be booted.
func (s *CharServer) OnTraffic(c gnet.Conn) gnet.Action {
	for {
		if c.InboundBuffered() < 2 {
			return gnet.None // need at least the 2-byte opcode header
		}
		hdr, err := c.Peek(2)
		if err != nil {
			return gnet.None
		}
		opcode := binary.LittleEndian.Uint16(hdr)
		if h, ok := s.handlers[opcode]; ok {
			if c.InboundBuffered() < h.size {
				return gnet.None // wait for the full frame to arrive
			}
			frame, err := c.Next(h.size)
			if err != nil {
				return gnet.None
			}
			cp := append([]byte(nil), frame...) // detach from gnet's ring buffer
			go h.fn(s, c, cp)
			continue
		}
		// Unwired opcode: skip the frame using the DB's length so the client
		// stays connected. Never close on a registered opcode.
		skip, buffered := s.unhandledSkip(c, opcode)
		if !buffered {
			return gnet.None // frame not fully arrived yet
		}
		if _, err := c.Discard(skip); err != nil {
			return gnet.None
		}
		s.log.Debug("char: skipping unhandled opcode", "cmd", fmt.Sprintf("0x%04x", opcode), "skip", skip)
	}
}

// unhandledSkip returns how many leading bytes to discard for an opcode that has
// no wired handler, and whether that many bytes are already buffered. A
// DB-registered opcode skips its definition's fixed length; a variable-length
// packet reads its on-wire length from the uint16 at offset 2. An opcode absent
// from the DB skips only the 2-byte header — a truly unknown stream cannot be
// safely aligned, so we resync one header and keep the connection alive rather
// than booting the client.
func (s *CharServer) unhandledSkip(c gnet.Conn, opcode uint16) (skip int, buffered bool) {
	def, ok := s.db.Lookup(opcode)
	if !ok {
		return 2, true
	}
	if def.Length != ropacket.VariableLength {
		if c.InboundBuffered() < def.Length {
			return def.Length, false // wait for the full fixed frame
		}
		return def.Length, true
	}
	// Variable-length frame: [cmd:2][len:2][payload...]. The length prefix is
	// the uint16 at byte offset 2.
	if c.InboundBuffered() < 4 {
		return 0, false
	}
	prefix, err := c.Peek(4)
	if err != nil {
		return 0, false
	}
	n := int(binary.LittleEndian.Uint16(prefix[2:4]))
	if n < 4 {
		return 2, true // malformed length prefix: resync over the header
	}
	if c.InboundBuffered() < n {
		return n, false // wait for the rest of the frame
	}
	return n, true
}

// handleEnter validates the CH_ENTER, loads the character list, and replies.
func (s *CharServer) handleEnter(c gnet.Conn, frame []byte) {
	req, err := ropacket.ParseCHEnter(frame)
	if err != nil {
		s.log.Warn("char: unparseable CH_ENTER", "err", err)
		return
	}
	// Validate the login session: AID + loginID1 must match what login stored.
	if _, err := s.chars.Authorize(context.Background(), req.AccountID, req.LoginID1); err != nil {
		s.writeRefuseEnter(c)
		s.log.Info("char refused", "aid", req.AccountID, "err", err)
		return
	}
	// Cache the authed identity on the connection so make/select know whose
	// connection this is without re-verifying. AID/sex are sourced from the
	// verified CH_ENTER, never re-read from later client-controlled packets.
	c.SetContext(charAuth{accountID: req.AccountID, sex: req.Sex})
	chars, err := s.chars.List(context.Background(), req.AccountID)
	if err != nil {
		s.log.Error("char: list", "aid", req.AccountID, "err", err)
		s.writeRefuseEnter(c)
		return
	}
	resp := ropacket.AcceptEnterResponse{
		Total:        maxChars,
		PremiumStart: maxChars,
		PremiumEnd:   maxChars,
		Characters:   toCharacterInfos(chars, req.Sex),
	}
	out := make([]byte, resp.Size())
	if err := resp.Encode(sliceWriter(out)); err != nil {
		s.log.Error("char: encode accept-enter", "err", err)
		return
	}
	_ = c.AsyncWrite(out, nil)
	s.log.Info("char-select served", "aid", req.AccountID, "chars", len(chars))
}

// charAuth is the per-connection auth cache set after a verified CH_ENTER.
type charAuth struct {
	accountID uint32
	sex       uint8
}

// charAuthFromConn extracts the cached charAuth from a gnet connection, or nil if
// the connection has not passed the CH_ENTER trust gate.
func charAuthFromConn(c gnet.Conn) *charAuth {
	v := c.Context()
	auth, ok := v.(charAuth)
	if !ok {
		return nil
	}
	return &auth
}

// handleMakeChar validates the CH_MAKE_CHAR, creates the character, and replies
// with HC_ACCEPT_MAKECHAR or HC_REFUSE_MAKECHAR.
func (s *CharServer) handleMakeChar(c gnet.Conn, frame []byte) {
	auth := charAuthFromConn(c)
	if auth == nil {
		s.writeRefuseMakeChar(c, makeCharRefuseDenied)
		return
	}
	req, err := ropacket.ParseCHMakeChar(frame)
	if err != nil {
		s.log.Warn("char: unparseable CH_MAKE_CHAR", "err", err)
		s.writeRefuseMakeChar(c, makeCharRefuseDenied)
		return
	}
	created, err := s.chars.Create(context.Background(), auth.accountID, int8(req.Slot), req.Name) //nolint:gosec // G115: slot is a validated 0..N client slot index.
	if err != nil {
		switch {
		case errors.Is(err, chardomain.ErrNameTaken):
			s.writeRefuseMakeChar(c, makeCharRefuseNameTaken)
		case errors.Is(err, chardomain.ErrSlotTaken):
			s.writeRefuseMakeChar(c, makeCharRefuseSlotTaken)
		default:
			s.log.Error("char: create", "aid", auth.accountID, "err", err)
			s.writeRefuseMakeChar(c, makeCharRefuseDenied)
		}
		return
	}
	resp := ropacket.AcceptMakeCharResponse{Character: toCharacterInfos([]chardomain.Character{created}, auth.sex)[0]}
	out := make([]byte, resp.Size())
	if err := resp.Encode(sliceWriter(out)); err != nil {
		s.log.Error("char: encode accept-makechar", "err", err)
		return
	}
	_ = c.AsyncWrite(out, nil)
	s.log.Info("char created", "aid", auth.accountID, "gid", created.ID, "name", created.Name)
}

// handleSelectChar resolves the chosen slot to a character and redirects the
// client to the map server via HC_NOTIFY_ZONESVR.
func (s *CharServer) handleSelectChar(c gnet.Conn, frame []byte) {
	auth := charAuthFromConn(c)
	if auth == nil {
		return
	}
	req, err := ropacket.ParseCHSelectChar(frame)
	if err != nil {
		s.log.Warn("char: unparseable CH_SELECT_CHAR", "err", err)
		return
	}
	chars, err := s.chars.List(context.Background(), auth.accountID)
	if err != nil {
		s.log.Error("char: list for select", "aid", auth.accountID, "err", err)
		return
	}
	var mapName string
	var gid uint32
	found := false
	for _, ch := range chars {
		if ch.CharNum == int8(req.Slot) { //nolint:gosec // G115: slot is a validated 0..N client slot index.
			gid = uint32(ch.ID)
			mapName = ch.LastMap
			found = true
			break
		}
	}
	if !found {
		s.log.Warn("char: select slot not found", "aid", auth.accountID, "slot", req.Slot)
		return
	}
	resp := ropacket.NotifyZoneServerResponse{
		CID:     gid,
		MapName: mapName,
		IP:      s.mapIP,
		Port:    s.mapPort,
	}
	out := make([]byte, resp.Size())
	if err := resp.Encode(sliceWriter(out)); err != nil {
		s.log.Error("char: encode notify-zone", "err", err)
		return
	}
	_ = c.AsyncWrite(out, nil)
	s.log.Info("char selected", "aid", auth.accountID, "gid", gid, "map", mapName)
}

// writeRefuseEnter sends an HC_REFUSE_ENTER (the char-server analog of refuse-login).
func (s *CharServer) writeRefuseEnter(c gnet.Conn) {
	resp := ropacket.RefuseEnterResponse{Error: 0}
	out := make([]byte, resp.Size())
	if err := resp.Encode(sliceWriter(out)); err != nil {
		s.log.Error("char: encode refuse-enter", "err", err)
		return
	}
	_ = c.AsyncWrite(out, nil)
}

// writeRefuseMakeChar sends an HC_REFUSE_MAKECHAR for a failed create.
func (s *CharServer) writeRefuseMakeChar(c gnet.Conn, code uint8) {
	resp := ropacket.RefuseMakeCharResponse{Error: code}
	out := make([]byte, resp.Size())
	if err := resp.Encode(sliceWriter(out)); err != nil {
		s.log.Error("char: encode refuse-makechar", "err", err)
		return
	}
	_ = c.AsyncWrite(out, nil)
}

// Start runs the char listener in a goroutine.
func (s *CharServer) Start(addr string) {
	go func() {
		if err := gnet.Run(s, addr, gnet.WithTicker(true)); err != nil {
			s.log.Error("char listener stopped", "addr", addr, "err", err)
		}
	}()
}

// Stop shuts the listener down.
func (s *CharServer) Stop() {
	if !s.booted {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.engine.Stop(ctx)
	s.booted = false
}

// toCharacterInfos maps domain characters to the wire CHARACTER_INFO structs.
//
//nolint:gosec // G115: all narrowing here is from bounded stat/exp/level ranges to the fixed-size wire fields; values are inherently small.
func toCharacterInfos(chars []chardomain.Character, sex uint8) []ropacket.CharacterInfo {
	out := make([]ropacket.CharacterInfo, 0, len(chars))
	for _, c := range chars {
		out = append(out, ropacket.CharacterInfo{
			GID:      uint32(c.ID),
			Exp:      int64(c.BaseExp),
			Money:    int32(c.Zeny),
			JobExp:   int64(c.JobExp),
			JobLevel: int32(c.JobLevel),
			HP:       int64(c.HP),
			MaxHP:    int64(c.MaxHP),
			SP:       int64(c.SP),
			MaxSP:    int64(c.MaxSP),
			Job:      int16(c.Class),
			Weapon:   int16(c.Weapon),
			Level:    int16(c.BaseLevel),
			Shield:   int16(c.Shield),
			Head:     int16(c.HeadTop),
			Name:     c.Name,
			Str:      uint8(c.Str),
			Agi:      uint8(c.Agi),
			Vit:      uint8(c.Vit),
			Int:      uint8(c.Int),
			Dex:      uint8(c.Dex),
			Luk:      uint8(c.Luk),
			CharNum:  uint8(c.CharNum),
			MapName:  c.LastMap,
			Sex:      sex,
		})
	}
	return out
}
