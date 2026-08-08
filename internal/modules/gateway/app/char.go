package app

import (
	"context"
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

// maxChars is the upper bound on character slots advertised to the client.
const maxChars = 9

// CharServer is the gnet TCP listener for the char-select protocol.
type CharServer struct {
	gnet.BuiltinEventEngine
	engine  gnet.Engine
	booted  bool
	chars   *charapp.CharService
	log     *slog.Logger
	mapIP   uint32 // advertised map-server IPv4 (wire uint32)
	mapPort uint16 // advertised map-server port
}

// NewCharServer builds a char-select listener. mapHost/mapPort are the map-server
// endpoint advertised inside NotifyZoneServerResponse when the client picks a
// character to enter the world with.
func NewCharServer(chars *charapp.CharService, log *slog.Logger, mapHost string, mapPort uint16) (*CharServer, error) {
	ip, err := ipToWire(mapHost)
	if err != nil {
		return nil, fmt.Errorf("map server host %q: %w", mapHost, err)
	}
	return &CharServer{chars: chars, log: log, mapIP: ip, mapPort: mapPort}, nil
}

// OnBoot captures the engine for shutdown.
func (s *CharServer) OnBoot(e gnet.Engine) gnet.Action {
	s.engine = e
	s.booted = true
	s.log.Info("char listener booted")
	return gnet.None
}

// OnTraffic reads complete CH_ENTER frames. The handshake is two-phase but
// single-packet for the first step: the client sends CH_ENTER, the server replies
// with the character list (AcceptEnterResponse). Later packets (create/delete/
// enter-game) extend this path in subsequent phases.
func (s *CharServer) OnTraffic(c gnet.Conn) gnet.Action {
	for c.InboundBuffered() >= charEnterFrameSize {
		frame, err := c.Next(charEnterFrameSize)
		if err != nil {
			break
		}
		cp := append([]byte(nil), frame...)
		go s.handleEnter(c, cp)
	}
	return gnet.None
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
