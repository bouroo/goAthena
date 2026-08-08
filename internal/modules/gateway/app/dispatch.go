package app

import (
	"context"
	"strconv"

	"github.com/panjf2000/gnet/v2"

	dialogdomain "github.com/bouroo/goAthena/internal/modules/content/domain"
	contentinfra "github.com/bouroo/goAthena/internal/modules/content/infra"
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
		0x0089: {size: 7, fn: (*MapServer).handleActionRequest}, // CZ_ACTION_REQUEST
		0x0090: {size: 7, fn: (*MapServer).handleContactNPC},    // CZ_CONTACT_NPC (NPC click)
		0x00b8: {size: 7, fn: (*MapServer).handleChooseMenu},    // CZ_CHOOSE_MENU
		0x00b9: {size: 6, fn: (*MapServer).handleReqNextScript}, // CZ_REQ_NEXT_SCRIPT
		0x0143: {size: 10, fn: (*MapServer).handleInputEditDlg}, // CZ_INPUT_EDITDLG
		0x0146: {size: 6, fn: (*MapServer).handleCloseDialog},   // CZ_CLOSE_DIALOG
		0x0362: {size: 6, fn: (*MapServer).handleItemPickup},    // CZ_ITEM_PICKUP @ 20250604
	}
}

// handleContactNPC starts an NPC dialog script on click (CZ_CONTACT_NPC 0x0090).
func (s *MapServer) handleContactNPC(c gnet.Conn, frame []byte) {
	auth := authFromConn(c)
	if auth == nil {
		return
	}
	req, err := ropacket.ParseCZContactNPC(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_CONTACT_NPC", "err", err)
		return
	}
	s.content.StartDialog(auth.accountID, auth.charID, req.AID, contentinfra.GnetPacketWriter{Conn: c})
}

// handleReqNextScript advances an active dialog (CZ_REQ_NEXT_SCRIPT 0x00b9).
func (s *MapServer) handleReqNextScript(c gnet.Conn, _ []byte) {
	auth := authFromConn(c)
	if auth == nil {
		return
	}
	s.content.Signal(auth.accountID, dialogdomain.DialogSignal{Advance: true})
}

// handleChooseMenu delivers a menu selection (CZ_CHOOSE_MENU 0x00b8).
func (s *MapServer) handleChooseMenu(c gnet.Conn, frame []byte) {
	auth := authFromConn(c)
	if auth == nil {
		return
	}
	req, err := ropacket.ParseCZChooseMenu(frame)
	if err != nil {
		return
	}
	s.content.Signal(auth.accountID, dialogdomain.DialogSignal{Choice: uint8(req.Selected)}) //nolint:gosec // G115: -1→255 cancel.
}

// handleInputEditDlg delivers a numeric input (CZ_INPUT_EDITDLG 0x0143).
func (s *MapServer) handleInputEditDlg(c gnet.Conn, frame []byte) {
	auth := authFromConn(c)
	if auth == nil {
		return
	}
	req, err := ropacket.ParseCZInputEditDlg(frame)
	if err != nil {
		return
	}
	s.content.Signal(auth.accountID, dialogdomain.DialogSignal{Input: strconv.FormatInt(int64(req.Value), 10)})
}

// handleCloseDialog cancels an active dialog (CZ_CLOSE_DIALOG 0x0146).
func (s *MapServer) handleCloseDialog(c gnet.Conn, _ []byte) {
	auth := authFromConn(c)
	if auth == nil {
		return
	}
	s.content.Signal(auth.accountID, dialogdomain.DialogSignal{Cancel: true})
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

// handleItemPickup handles CZ_ITEM_PICKUP (0x0362, 6B): parse GroundID, look up
// the floor item, remove it from the ground, add it to the player's inventory,
// and reply ZC_ITEM_PICKUP_ACK.
func (s *MapServer) handleItemPickup(c gnet.Conn, frame []byte) {
	auth := authFromConn(c)
	if auth == nil {
		s.log.Warn("map: CZ_ITEM_PICKUP from unauthed conn")
		return
	}
	req, err := ropacket.ParseCZItemPickup(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_ITEM_PICKUP", "err", err)
		return
	}
	fi, err := s.spawn.PickupFloorItem(req.GroundID)
	if err != nil {
		s.log.Debug("map: pickup (not found)", "gid", req.GroundID)
		return // item already taken or gone — client re-syncs
	}
	_, err = s.inv.Add(context.Background(), auth.charID, fi.NameID, int(fi.Amount))
	if err != nil {
		s.log.Error("map: pickup add inventory", "err", err)
		return
	}
	resp := ropacket.ItemPickupAckResponse{
		Count:        uint16(fi.Amount), //nolint:gosec // G115: item amount bounded to small stack values.
		NameID:       fi.NameID,
		IsIdentified: 1,
		Result:       0, // success
	}
	out := make([]byte, resp.Size())
	if err := resp.Encode(sliceWriter(out)); err != nil {
		s.log.Error("map: encode pickup-ack", "err", err)
		return
	}
	_ = c.AsyncWrite(out, nil)
}

// handleActionRequest handles CZ_ACTION_REQUEST (0x0089, 7B): sit/stand/attack.
// Sit/stand just echo back; attack (action 0x07) resolves melee damage via
// CombatService, echoes the action, and — when the hit kills a mob — drives the
// death loop: drops + despawn (SpawnService.OnMobDeath) then a ZC_NOTIFY_VANISH
// + one ZC_ITEM_ENTRY per rolled drop.
func (s *MapServer) handleActionRequest(c gnet.Conn, frame []byte) {
	auth := authFromConn(c)
	if auth == nil {
		s.log.Warn("map: CZ_ACTION_REQUEST from unauthed conn")
		return
	}
	req, err := ropacket.ParseCZActionRequest(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_ACTION_REQUEST", "err", err)
		return
	}
	if req.Action != 0x07 { // sit/stand: echo only
		s.sendActionResponse(c, auth.charID, req.Action, req.TargetGID)
		return
	}
	// attack (0x07)
	dmg, died, err := s.combat.Attack(worlddomain.EntityID(auth.charID), worlddomain.EntityID(req.TargetGID))
	if err != nil {
		s.log.Warn("map: attack", "err", err)
		return
	}
	s.log.Debug("map: attack", "attacker", auth.charID, "target", req.TargetGID, "dmg", dmg)
	s.sendActionResponse(c, auth.charID, req.Action, req.TargetGID)
	if died {
		s.handleMobDeath(c, req.TargetGID)
	}
}

// sendActionResponse encodes and writes ZC_ACTION_RESPONSE (the action echo).
func (s *MapServer) sendActionResponse(c gnet.Conn, charID uint32, action uint8, targetGID uint32) {
	resp := ropacket.ActionResponse{GID: charID, Action: action, TargetGID: targetGID}
	out := make([]byte, resp.Size())
	if err := resp.Encode(sliceWriter(out)); err != nil {
		s.log.Error("map: encode action-response", "err", err)
		return
	}
	_ = c.AsyncWrite(out, nil)
}

// handleMobDeath despawns a dead mob, rolls its drops, and notifies the client:
// ZC_NOTIFY_VANISH (mob leaves the map) then one ZC_ITEM_ENTRY per rolled drop.
// The death/drop state lives in SpawnService.OnMobDeath; this only does the wire
// side. Only mobs despawn+drop on death (a PC reaching 0 HP is a revive flow).
// The frames are coalesced into one AsyncWrite. Full AOI-neighbor broadcast
// lands with a connection registry; today the killing player's connection is
// notified, matching the per-connection pattern used by move/pickup.
func (s *MapServer) handleMobDeath(c gnet.Conn, mobGID uint32) {
	defender, err := s.world.Get(worlddomain.EntityID(mobGID))
	if err != nil {
		return // already removed (concurrent death) — nothing to broadcast
	}
	if defender.Type != worlddomain.EntityTypeMob {
		return
	}
	drops := s.spawn.OnMobDeath(defender.Class, defender.Map, defender.Pos, worlddomain.EntityID(mobGID))

	var burst []byte
	vanish := ropacket.NotifyVanishResponse{GID: mobGID, Type: ropacket.VanishDead}
	vbuf := make([]byte, vanish.Size())
	if err := vanish.Encode(sliceWriter(vbuf)); err != nil {
		s.log.Error("map: encode vanish", "err", err)
		return
	}
	burst = append(burst, vbuf...)
	for _, fi := range drops {
		entry := ropacket.ItemEntryResponse{
			AID:        fi.GroundID,
			NameID:     fi.NameID,
			Identified: 1,
			X:          uint16(fi.PosX),   //nolint:gosec // G115: map coords are non-negative int16.
			Y:          uint16(fi.PosY),   //nolint:gosec // G115: map coords are non-negative int16.
			Amount:     uint16(fi.Amount), //nolint:gosec // G115: amount bounded to small stack values.
		}
		ebuf := make([]byte, entry.Size())
		if err := entry.Encode(sliceWriter(ebuf)); err != nil {
			s.log.Error("map: encode item-entry", "err", err)
			continue
		}
		burst = append(burst, ebuf...)
	}
	_ = c.AsyncWrite(burst, nil)
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
