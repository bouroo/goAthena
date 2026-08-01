// Package app: this file serves CZ_GETCHARNAMEREQUEST (0x0094), the client's
// "resolve this entity's name" packet (mouseover/target). rAthena's
// clif_parse_GetCharNameRequest resolves the GID via map_id2bl and, if the entity
// is in view, clif_name emits the REQNAMEALL reply. At PACKETVER 20250604 the
// active opcodes (packets_struct.hpp:3562-3602) are 0x0a30 for a PC and 0x0adf
// for a mob/NPC — not the legacy 0x0095/0x0195.
package app

import (
	"context"
	"errors"
	"fmt"

	gwdomain "github.com/bouroo/goAthena/internal/modules/gateway/domain"
	"github.com/bouroo/goAthena/pkg/ro/aoi"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// nameViewRadius is the maximum cell distance at which a name reply is sent. It
// matches the AOI broadcast radius (aoi.DefaultBroadcastRadius = 15 = rAthena
// AREA_SIZE+1): a client only ever asks for the name of an entity the spawn
// system already placed in its view, so this gate keeps the reply consistent
// with what the client can see.
const nameViewRadius = 15

// SendNameReply resolves a requested GID to a live entity and writes the name
// reply rAthena's clif_name emits: 0x0a30 (ZC_ACK_REQNAMEALL2) for a PC, 0x0adf
// (ZC_ACK_REQNAMEALL_NPC) for a mob/NPC. An unknown GID, a requester that is not
// an online player, or a target outside the requester's view all no-op — rAthena
// silently sends nothing in those cases (clif.cpp:11489-11523).
//
// The GID the client sends is the entity's spawn-packet identity: a PC's charID
// (the GID field) or a mob/NPC's EntityID (the AID field). The id ranges are
// partitioned (account 2M, char 150K, mob 110M, npc 500M) so resolving against
// each registry in turn never mis-matches an id to the wrong kind. goAthena has
// no party/guild/clan systems, so a PC's affiliation names stay empty and its
// TitleID is 0; a mob/NPC's GroupID and Title are 0/empty.
func (s *SpawnService) SendNameReply(_ context.Context, conn gwdomain.Conn, accountID, gid uint32) error {
	requester, ok := s.registry.ByAccount(accountID)
	if !ok {
		return nil
	}
	rMap := requester.MapName
	rx, ry, _ := requester.Position()

	if target, ok := s.registry.ByCharID(gid); ok {
		tx, ty, _ := target.Position()
		if !sameView(rMap, rx, ry, target.MapName, tx, ty) {
			return nil
		}
		if err := (packet.ReqNameAll2Response{GID: gid, Name: target.Name}).Encode(connWriter{conn}); err != nil {
			return fmt.Errorf("name: encode ZC_ACK_REQNAMEALL2: %w", err)
		}
		return nil
	}
	if target, ok := s.mobs.ByEntity(aoi.EntityID(gid)); ok { //nolint:gosec // G115: gid is an allocated mob EntityID (< 2^31)
		if !sameView(rMap, rx, ry, target.MapName, target.PosX, target.PosY) {
			return nil
		}
		if err := (packet.ReqNameAllNPCResponse{GID: gid, Name: target.Name}).Encode(connWriter{conn}); err != nil {
			return fmt.Errorf("name: encode ZC_ACK_REQNAMEALL_NPC (mob): %w", err)
		}
		return nil
	}
	if target, ok := s.npcs.ByEntity(aoi.EntityID(gid)); ok { //nolint:gosec // G115: gid is an allocated NPC EntityID (< 2^31)
		if !sameView(rMap, rx, ry, target.MapName, target.PosX, target.PosY) {
			return nil
		}
		if err := (packet.ReqNameAllNPCResponse{GID: gid, Name: target.Name}).Encode(connWriter{conn}); err != nil {
			return fmt.Errorf("name: encode ZC_ACK_REQNAMEALL_NPC (npc): %w", err)
		}
		return nil
	}
	return nil
}

// sameView reports whether (tMap, tx, ty) is on the requester's map and within
// the name reply view radius (Manhattan distance). Map coordinates are small
// non-negative int16 values (map sizes ≤ ~512), so the difference cannot
// overflow int16.
func sameView(rMap string, rx, ry int16, tMap string, tx, ty int16) bool {
	if rMap != tMap {
		return false
	}
	return absInt16(rx-tx)+absInt16(ry-ty) <= nameViewRadius
}

// absInt16 returns the absolute value of v; the caller guarantees |v| fits int16.
func absInt16(v int16) int16 {
	if v < 0 {
		return -v
	}
	return v
}

// NameHandler serves CZ_GETCHARNAMEREQUEST (0x0094). A verified connection (a
// player online via CZ_ENTER) is replied to with the requested entity's name;
// the 6-byte frame carries one GID. Without this handler the opcode hit
// ErrNoHandler and names never resolved on mouseover/target.
type NameHandler struct {
	svc *SpawnService
}

// NewNameHandler binds the SpawnService that owns the player/mob/NPC registries.
func NewNameHandler(svc *SpawnService) *NameHandler {
	return &NameHandler{svc: svc}
}

// Handle implements gateway/domain.PacketHandler for CZ_GETCHARNAMEREQUEST.
func (h *NameHandler) Handle(ctx context.Context, conn gwdomain.Conn, frame gwdomain.Frame) error {
	req, err := packet.ParseCZGetCharNameRequest(frame.Raw)
	if err != nil {
		return fmt.Errorf("parse CZ_GETCHARNAMEREQUEST: %w", err)
	}
	if conn.Auth().AccountID == 0 {
		return errors.New("name: connection has no verified account (CZ_ENTER not completed)")
	}
	return h.svc.SendNameReply(ctx, conn, conn.Auth().AccountID, req.GID)
}

// compile-time: NameHandler satisfies the gateway handler contract.
var _ gwdomain.PacketHandler = (*NameHandler)(nil).Handle
