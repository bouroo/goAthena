// Package app: this file adds the M10a pickup use case — a player picks up a
// dropped floor item (the M9c-1 drop → M10a loot loop), the item is moved into
// that character's bag via the inventory bounded context, and observers are
// told the floor item is gone. Pickup is a world use case for the same reason
// combat and movement are: it crosses the player registry, the floor-item
// registry, the map AOI, and now the inventory port. The bag itself lives in
// the inventory module; this use case only resolves that module's domain port.
//
// Fork fidelity (rathenaThailand @ current): clif_parse_TakeItem
// (clif.cpp:12024) is a do/while(0) whose every failure branch breaks to an
// unconditional clif_additem(sd,0,0,6) — verbatim comment "Client REQUIRES a
// fail packet or you can no longer pick items." The success path returns early
// (clif.cpp:12049) and never reaches the fail ack. So a pickup request MUST
// always yield a ZC_ITEM_PICKUP_ACK (0x0b41): success result=0 (plus the full
// item detail), or fail result=6. The wrong-map check fitem->m != sd->m
// (clif.cpp:12040) and the out-of-range check in pc_takeitem (pc.cpp:6247,
// check_distance_bl 2) are both fail-ack paths, not silent drops.
package app

import (
	"context"
	"errors"
	"fmt"

	gwdomain "github.com/bouroo/goAthena/internal/modules/gateway/domain"
	invdomain "github.com/bouroo/goAthena/internal/modules/inventory/domain"
	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/aoi"
	"github.com/bouroo/goAthena/pkg/ro/combat"
	"github.com/bouroo/goAthena/pkg/ro/itemdb"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// PickupService resolves CZ_ITEM_PICKUP requests: it validates the request
// against the floor-item registry and the picker's position, moves the item
// into the bag through the inventory port, and broadcasts the pickup frames.
// Every collaborator is the same kind the combat/movement use cases already
// hold; items is the inventory module's domain port (resolved via the injector,
// never an infra import — the cross-module license arch_test grants).
type PickupService struct {
	floorItems *domain.FloorItemRegistry
	players    *domain.PlayerRegistry
	maps       domain.MapStore
	items      invdomain.InventoryRepository
	itemDB     *itemdb.Registry
	clock      Clock
}

// NewPickupService binds the pickup collaborators. floorItems owns the
// dropped-item index; players resolves the live session and its AOI neighbors;
// maps loads the picker's map for the AREA broadcasts. items is the inventory
// port the bag mutation goes through; a nil items (no inventory context wired)
// makes every pickup a fail ack — the world stays playable, the client is never
// left without a response. itemDB resolves stackability for the merge-vs-insert
// decision; a nil itemDB treats every pickup as a fresh row (non-stackable
// insert), the safe fallback when item_db is unavailable. clock stamps the
// pickup animation's ZC_NOTIFY_ACT server tick, the same field combat fills.
func NewPickupService(floorItems *domain.FloorItemRegistry, players *domain.PlayerRegistry, maps domain.MapStore, items invdomain.InventoryRepository, itemDB *itemdb.Registry, clock Clock) *PickupService {
	return &PickupService{floorItems: floorItems, players: players, maps: maps, items: items, itemDB: itemDB, clock: clock}
}

// Pickup resolves one CZ_ITEM_PICKUP from accountID against groundID. It mirrors
// clif_parse_TakeItem + pc_takeitem: a no-session request is a silent drop (the
// client is gone); every other rejection (unknown/wrong-map item, out of range,
// bag full) yields the fail ack result=6 and returns nil — a rejected pickup is
// an expected client outcome, not a session fault. Only an infra fault (map
// load, a non-full repository error) is returned so ProcessBytes can log it.
func (s *PickupService) Pickup(ctx context.Context, accountID uint32, groundID uint32) error {
	picker, ok := s.players.ByAccount(accountID)
	if !ok {
		// No live session: the conn never entered the world or already
		// disconnected. Nothing to ack and nowhere to write it.
		return nil
	}
	fi, ok := s.floorItems.ByEntity(aoi.EntityID(groundID))
	if !ok {
		s.sendPickupFail(picker)
		return nil
	}
	// fitem->m != sd->m (clif.cpp:12040): an item id the client cached from
	// another map. Fail ack, not a silent drop.
	if fi.MapName != picker.MapName {
		s.sendPickupFail(picker)
		return nil
	}
	px, py, _ := picker.Position()
	if !combat.InPickupRange(int(px), int(py), int(fi.PosX), int(fi.PosY)) {
		s.sendPickupFail(picker)
		return nil
	}
	if s.items == nil {
		// No inventory context wired: nothing can be added, so the request
		// must still be answered with the fail ack per the client lock.
		s.sendPickupFail(picker)
		return nil
	}

	// Load the picker's map before mutating the bag so a cached-map load
	// fault is reported before the item is moved (no stranded mutation).
	mp, err := s.maps.Load(ctx, picker.MapName)
	if err != nil {
		return fmt.Errorf("pickup: load map %q for account %d: %w", picker.MapName, accountID, err)
	}
	if mp == nil {
		return fmt.Errorf("pickup: map %q loaded nil for account %d", picker.MapName, accountID)
	}

	// Stackability is a data concern resolved once from item_db; the
	// repository performs the atomic merge-or-insert with that decision in
	// hand so it does not depend on the item_db package (a persistence
	// concern). A missing item_db was already fail-acked above.
	stackable := s.itemDB != nil && s.itemDB.IsStackable(fi.NameID)
	got, err := s.items.AddItem(ctx, accountID, picker.CharID, invdomain.NewItem{
		NameID:    fi.NameID,
		Amount:    uint32(fi.Amount), //nolint:gosec // G115: drop Amount is uint16 (1 for a mob drop), well under uint32
		Stackable: stackable,
	})
	if err != nil {
		if errors.Is(err, invdomain.ErrInventoryFull) {
			// pc_additem ADDITEM_FAIL (0) → clif_additem(sd,0,0,6): the
			// bag has no free slot and no stack to merge. Fail ack, return.
			s.sendPickupFail(picker)
			return nil
		}
		return fmt.Errorf("pickup: add item nameid %d for account %d char %d: %w", fi.NameID, accountID, picker.CharID, err)
	}

	// Success. Unregister first so a racing pickup on the same ground id
	// fails (ByEntity returns false) rather than double-crediting the item;
	// the registry's Unregister is idempotent and concurrency-safe.
	s.floorItems.Unregister(fi.EntityID)

	// Fork success order (pc.cpp:6306-6316): the ack (SELF, result=0) from
	// pc_additem → clif_takeitem's ZC_NOTIFY_ACT pickup animation (AREA) →
	// map_clearflooritem's ZC_ITEM_DISAPPEAR (AREA). The picker is within
	// AOI of the item (range ≤ 2), so the AREA broadcasts reach it too.
	s.sendPickupAck(picker, got, fi)
	s.broadcastPickupAct(mp, fi.PosX, fi.PosY, picker.AccountID, fi.EntityID)
	s.broadcastFloorVanish(mp, fi.PosX, fi.PosY, fi.EntityID)
	return nil
}

// sendPickupAck writes the success ZC_ITEM_PICKUP_ACK (result=0) to the picker
// only (SELF). clif_additem builds the ack from the just-added inventory row;
// the bag mutation already ran, so Index/Count/NameID carry real values. Type
// is the IT_* enum cached on the floor item at drop time (itemdb.WireType);
// IsIdentified is 0 to match a mob drop's identify flag (the same 0 the drop
// broadcast carries). Cards/options/look stay zero — the basic drop slice has
// no carded or random-option loot.
func (s *PickupService) sendPickupAck(picker *domain.Player, got invdomain.InventoryItem, fi *domain.FloorItem) {
	ack := packet.ItemPickupAckResponse{
		Index:        got.Index,
		Count:        fi.Amount,
		NameID:       fi.NameID,
		IsIdentified: 0,
		Type:         uint8(fi.Type), //nolint:gosec // G115: IT_* enum fits a wire byte; values are 0..18
		Result:       0,              // success
	}
	_ = ack.Encode(connWriter{picker.Conn})
}

// sendPickupFail writes the fail ZC_ITEM_PICKUP_ACK (result=6) to the picker.
// clif_additem (clif.cpp:2836) zeroes the whole frame and writes only
// result=fail, so every other field is 0 — the result byte is the only signal.
func (s *PickupService) sendPickupFail(picker *domain.Player) {
	ack := packet.ItemPickupAckResponse{Result: 6}
	_ = ack.Encode(connWriter{picker.Conn})
}

// broadcastPickupAct sends ZC_NOTIFY_ACT with type=DMG_PICKUP_ITEM to every
// player within AOI of the item's cell, the picker included — the client plays
// the pickup animation on receipt. clif_takeitem(*sd, *fitem) (clif.cpp:5315)
// sets srcID = picker, targetID = floor item, so SrcID is the account id and
// TargetID the ground id. Damage/amotion are zero: a pickup has no hit.
func (s *PickupService) broadcastPickupAct(mp *domain.Map, x, y int16, srcAccountID uint32, targetEntityID aoi.EntityID) {
	act := packet.NotifyActResponse{
		SrcID:      srcAccountID,
		TargetID:   uint32(targetEntityID), //nolint:gosec // G115: EntityID is a uint32-derived aoi.EntityID
		ServerTick: s.clock.MoveStart(),
		Type:       packet.DMGPickup,
		Div:        1,
	}
	for _, e := range mp.AOI.QueryVisible(int(x), int(y)) {
		if e.Type != aoi.EntityPlayer {
			continue
		}
		neighbor, ok := s.players.ByAccount(uint32(e.ID))
		if !ok {
			continue
		}
		_ = act.Encode(connWriter{neighbor.Conn})
	}
}

// broadcastFloorVanish sends ZC_ITEM_DISAPPEAR (0x00a1) to every player within
// AOI of the item's cell so the floor sprite is removed. map_clearflooritem →
// clif_clearflooritem_area is the fork's only floor-removal broadcast; the
// picker gets it too (the ack already credited the bag). Same observer/write
// policy as the combat broadcasts: non-players skipped, dead sockets ignored.
func (s *PickupService) broadcastFloorVanish(mp *domain.Map, x, y int16, entityID aoi.EntityID) {
	msg := packet.ItemDisappearResponse{AID: uint32(entityID)} //nolint:gosec // G115: EntityID is a uint32-derived aoi.EntityID
	for _, e := range mp.AOI.QueryVisible(int(x), int(y)) {
		if e.Type != aoi.EntityPlayer {
			continue
		}
		neighbor, ok := s.players.ByAccount(uint32(e.ID))
		if !ok {
			continue
		}
		_ = msg.Encode(connWriter{neighbor.Conn})
	}
}

// PickupHandler adapts PickupService to the gateway dispatcher for
// CZ_ITEM_PICKUP (0x009f). It reads the verified account id from the connection
// (never the packet) and forwards the parsed ground id, the same auth pattern
// ActionHandler uses for CZ_ACTION_REQUEST.
type PickupHandler struct {
	svc *PickupService
}

// NewPickupHandler binds the handler to its pickup service.
func NewPickupHandler(svc *PickupService) *PickupHandler {
	return &PickupHandler{svc: svc}
}

// Handle implements gateway/domain.PacketHandler for CZ_ITEM_PICKUP.
func (h *PickupHandler) Handle(ctx context.Context, conn gwdomain.Conn, frame gwdomain.Frame) error {
	req, err := packet.ParseCZItemPickup(frame.Raw)
	if err != nil {
		return fmt.Errorf("parse CZ_ITEM_PICKUP: %w", err)
	}
	accountID := conn.Auth().AccountID
	if accountID == 0 {
		// No cached auth ⇒ the connection never passed the CZ_ENTER gate.
		// Tolerated by the gateway (the conn stays open) but the pickup is
		// dropped — there is no verified identity to credit the item to.
		return errors.New("pickup: connection has no verified account (CZ_ENTER not completed)")
	}
	return h.svc.Pickup(ctx, accountID, req.GroundID)
}

// Compile-time check that PickupHandler satisfies the gateway handler shape.
var _ gwdomain.PacketHandler = (*PickupHandler)(nil).Handle
