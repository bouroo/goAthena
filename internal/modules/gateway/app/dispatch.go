package app

import (
	"github.com/panjf2000/gnet/v2"

	worlddomain "github.com/bouroo/goAthena/internal/modules/world/domain"
	ropacket "github.com/bouroo/goAthena/pkg/ro/packet"
)

// mapHandler is one entry in the map-server dispatch table: a fixed frame size
// and the function that processes a complete frame of that opcode.
type mapHandler struct {
	size int
	fn   func(s *MapServer, c gnet.Conn, frame []byte)
}

// mapHandlers is the opcode→handler table the map server dispatches against.
// Every map packet is fixed-length today. A connection's first packet is always
// CZ_ENTER (the trust gate); the rest are only valid post-auth.
func mapHandlers() map[uint16]mapHandler {
	return map[uint16]mapHandler{
		0x0072: {size: czEnterSize, fn: (*MapServer).handleEnterFrame},
		0x007d: {size: 2, fn: (*MapServer).handleLoadEndAck},
		0x0085: {size: 5, fn: (*MapServer).handleRequestMove},
	}
}

// handleEnterFrame wraps handleEnter to satisfy the dispatch signature (the
// frame is already detached from gnet's ring buffer by the caller).
func (s *MapServer) handleEnterFrame(c gnet.Conn, frame []byte) { s.handleEnter(c, frame) }

// handleLoadEndAck handles CZ_NOTIFY_ACTORINIT (0x007d, 2B cmd-only). This is
// the signal the client finished loading the map; rAthena replies with the
// inventory/skill/hotkey init burst. The burst is: ZC_INVENTORY_START →
// ZC_INVENTORY_ITEMLIST_NORMAL → ZC_INVENTORY_ITEMLIST_EQUIP → ZC_INVENTORY_END.
// Populated item lists (with item_db type/view resolution) land in M5b; for a
// fresh character (or before item_db is loaded) the empty lists are correct.
func (s *MapServer) handleLoadEndAck(c gnet.Conn, _ []byte) {
	auth := authFromConn(c)
	if auth == nil {
		s.log.Warn("map: LoadEndAck from unauthed conn")
		return
	}
	// Coalesce the 4-frame burst into one AsyncWrite to avoid four syscalls.
	var burst []byte
	burst = append(burst, ropacket.EncodeInventoryStart()...)
	burst = append(burst, ropacket.EncodeEmptyInventoryListNormal()...)
	burst = append(burst, ropacket.EncodeEmptyInventoryListEquip()...)
	burst = append(burst, ropacket.EncodeInventoryEnd()...)
	_ = c.AsyncWrite(burst, nil)
	s.log.Debug("map: client load complete (inventory init sent)", "aid", auth.accountID, "gid", auth.charID)
}

// handleRequestMove handles CZ_REQUEST_MOVE (0x0085, 5B): parse the 3-byte
// packed destination, move the entity in the world, and reply
// ZC_NOTIFY_PLAYERMOVE. Full AOI broadcast to neighbors lands with the
// connection-registry in M4b.
func (s *MapServer) handleRequestMove(c gnet.Conn, frame []byte) {
	auth := authFromConn(c)
	if auth == nil {
		s.log.Warn("map: CZ_REQUEST_MOVE from unauthed conn")
		return
	}
	req, err := ropacket.ParseCZRequestMove(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_REQUEST_MOVE", "err", err)
		return
	}
	gid := worlddomain.EntityID(auth.charID)
	// Source position for the move response (before the move).
	src, _ := s.world.Get(gid)
	dest := worlddomain.Position{X: req.DestX, Y: req.DestY}
	if err := s.world.MoveEntity(gid, dest); err != nil {
		s.log.Warn("map: move entity", "gid", auth.charID, "err", err)
		return
	}
	resp := ropacket.MapNotifyPlayerMoveResponse{
		MoveStartTime: 0, // server tick at move start; 0 acceptable for local clock
		SrcX:          src.Pos.X,
		SrcY:          src.Pos.Y,
		DestX:         req.DestX,
		DestY:         req.DestY,
	}
	out := make([]byte, resp.Size())
	if err := resp.Encode(sliceWriter(out)); err != nil {
		s.log.Error("map: encode player-move", "err", err)
		return
	}
	_ = c.AsyncWrite(out, nil)
}

// authFromConn extracts the cached mapAuth from a gnet connection, or nil if the
// connection has not passed the CZ_ENTER trust gate.
func authFromConn(c gnet.Conn) *mapAuth {
	v := c.Context()
	auth, ok := v.(mapAuth)
	if !ok {
		return nil
	}
	return &auth
}
