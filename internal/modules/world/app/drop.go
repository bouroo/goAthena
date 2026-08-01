// Package app: this file adds the A4 drop use case — a player drops an item
// from the bag onto the ground, the item leaves the bag via the inventory
// port, a floor item is registered so other players can pick it up, and the
// dropper + nearby observers are told. Drop is a world use case for the same
// reason combat and pickup are: it crosses the player registry, the floor-item
// registry, the map AOI, and the inventory port.
//
// Fork fidelity (rathenaThailand @ PACKETVER 20250604): the effective drop
// opcode is 0x0363 (clif_shuffle.hpp:4733 — clif_parse_DropItem overrides the
// clif_packetdb.hpp:1604 TickSend binding because clif_shuffle.hpp is included
// after clif_packetdb.hpp and packetdb_addpacket last-writes-wins). The
// successful drop order is pc_dropitem (pc.cpp:6167-6175): map_addflooritem
// (registers the floor item + broadcasts ZC_ITEM_FALL_ENTRY to the area) →
// pc_delitem(sd, n, amount, 1) (removes from the bag; deltype=1 skips the
// ZC_DELETE_ITEM_FROM_BODY packet so the client learns the bag change from the
// throw ack) → clif_dropitem (the SELF ZC_ITEM_THROW_ACK). A failed drop
// (unknown/bad index, qty > stack) emits the throw ack with count=0 — the
// rAthena verbatim comment is "Because the client does not like being ignored"
// (clif.cpp:12088-12090), so a rejection is an ack, not a silent drop.
package app

import (
	"context"
	"errors"
	"fmt"

	gwdomain "github.com/bouroo/goAthena/internal/modules/gateway/domain"
	invdomain "github.com/bouroo/goAthena/internal/modules/inventory/domain"
	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/aoi"
	"github.com/bouroo/goAthena/pkg/ro/itemdb"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// DropService resolves CZ_ITEM_DROP requests: it validates the request against
// the bag (via the inventory port), moves the item out of the bag, registers a
// floor item for other players to pick up, and broadcasts the drop frames. It
// holds the same collaborators as PickupService — floorItems, players, maps,
// the inventory port, and item_db — so a drop feeds straight into the pickup
// loot loop the M10a milestone already proves.
type DropService struct {
	floorItems *domain.FloorItemRegistry
	players    *domain.PlayerRegistry
	maps       domain.MapStore
	items      invdomain.InventoryRepository
	itemDB     *itemdb.Registry
}

// NewDropService wires the drop use case. A nil itemDB disables the drop (the
// item cannot be resolved to a floor-item sprite); a nil inventory repo makes
// every drop fail — both mirror the pickup service's nil-tolerance.
func NewDropService(floorItems *domain.FloorItemRegistry, players *domain.PlayerRegistry, maps domain.MapStore, items invdomain.InventoryRepository, itemDB *itemdb.Registry) *DropService {
	return &DropService{
		floorItems: floorItems,
		players:    players,
		maps:       maps,
		items:      items,
		itemDB:     itemDB,
	}
}

// Drop resolves one CZ_ITEM_DROP from accountID for the bag slot at rawIndex,
// dropping qty units. It mirrors clif_parse_DropItem + pc_dropitem: a missing
// session is a silent no-op (the client is gone, nothing to ack); every other
// rejection — unknown bag slot, qty beyond the stack, an unresolvable item_db
// entry, or a map load fault — yields the throw ack with count=0 (rAthena's
// "client does not like being ignored"). Only an infra fault (map load, a
// non-not-found repository error) is returned so ProcessBytes can log it.
func (s *DropService) Drop(ctx context.Context, accountID uint32, rawIndex uint16, qty uint16) error {
	dropper, ok := s.players.ByAccount(accountID)
	if !ok {
		// No live session: the conn never entered the world or already
		// disconnected. Nothing to ack and nowhere to write it.
		return nil
	}

	// Resolve the bag row WITHOUT mutating first: pc_dropitem validates the
	// index/amount/item_db before pc_delitem, so a rejected drop must leave the
	// bag untouched (pc.cpp:6187-6204). ConsumeItem re-validates atomically
	// below, so a racing mutation degrades to the fail ack rather than a fault.
	rows, err := s.items.LoadByChar(ctx, accountID, dropper.CharID)
	if err != nil {
		return fmt.Errorf("drop: load bag for account %d char %d: %w", accountID, dropper.CharID, err)
	}
	if int(rawIndex) >= len(rows) { //nolint:gosec // G115: rawIndex is a bag slot < 100; len fits int
		// Unknown slot. rAthena acks a rejected drop with the throw ack count=0
		// (clif.cpp:12088-12090 "the client does not like being ignored").
		s.sendDropFail(dropper)
		return nil
	}
	item := rows[rawIndex]
	if qty == 0 || uint32(qty) > item.Amount { //nolint:gosec // G115: qty is uint16, validated against uint32 Amount
		s.sendDropFail(dropper)
		return nil
	}
	if s.itemDB == nil {
		// item_db is unconfigured: nothing to resolve the sprite from, so the
		// item cannot land. Reject before consuming.
		s.sendDropFail(dropper)
		return nil
	}
	entry := s.itemDB.Get(int32(item.NameID)) //nolint:gosec // G115: item_db Id is int32; a valid item id is positive and well under int32
	if entry == nil {
		// No item_db entry for the NameID (should not happen: the bag only holds
		// item_db-valid items). Reject before consuming.
		s.sendDropFail(dropper)
		return nil
	}

	mp, err := s.maps.Load(ctx, dropper.MapName)
	if err != nil {
		return fmt.Errorf("drop: load map %q for account %d: %w", dropper.MapName, accountID, err)
	}
	if mp == nil {
		return fmt.Errorf("drop: map %q loaded nil for account %d", dropper.MapName, accountID)
	}

	if _, _, err := s.items.ConsumeItem(ctx, accountID, dropper.CharID, rawIndex, qty); err != nil {
		if errors.Is(err, invdomain.ErrItemNotFound) {
			// Racing mutation or a foreign slot surfaced between the load and the
			// consume. The item is still in the bag; ack the rejection.
			s.sendDropFail(dropper)
			return nil
		}
		return fmt.Errorf("drop: consume nameid at slot %d for account %d char %d: %w", rawIndex, accountID, dropper.CharID, err)
	}

	x, y, _ := dropper.Position()

	// Register the floor item so the drop is pickable, then broadcast. The
	// item's position is the dropper's cell (pc_dropitem drops at sd->x, sd->y;
	// the scatter rAthena applies for a blocked cell is a deferred nicety).
	fi := &domain.FloorItem{
		EntityID: s.floorItems.NextEntityID(),
		MapName:  dropper.MapName,
		NameID:   item.NameID,
		Type:     itemdb.WireType(entry.Type),
		Amount:   qty,
		PosX:     x,
		PosY:     y,
	}
	if err := s.floorItems.Register(fi); err != nil {
		// NextEntityID is allocator-unique; unreachable. The item already left
		// the bag, so ack the success — the client's bag update comes from the
		// throw ack (a count=0 ack would re-add a row that is gone) — and let
		// ProcessBytes log the floor-registration fault.
		s.sendDropAck(dropper, rawIndex, qty)
		return fmt.Errorf("drop: register floor item: %w", err)
	}

	// Fork success order (pc.cpp:6167-6175): the area sees the floor item first
	// (ZC_ITEM_FALL_ENTRY), then the dropper gets the throw ack. The dropper is
	// inside its own AOI, so the area broadcast reaches it too.
	s.broadcastFloorItem(mp, fi)
	s.sendDropAck(dropper, rawIndex, qty)
	return nil
}

// sendDropAck writes the success ZC_ITEM_THROW_ACK (count=qty) to the dropper
// only (SELF). clif_dropitem echoes the inventory slot the client dropped from
// (clif.cpp:2883 client_index(n)) and the amount; the raw index is the
// client-visible 0-based slot this codebase uses everywhere (see the
// equip/use-item handlers).
func (s *DropService) sendDropAck(dropper *domain.Player, rawIndex, qty uint16) {
	_ = (&packet.ItemThrowAckResponse{Index: rawIndex, Count: qty}).Encode(connWriter{dropper.Conn})
}

// sendDropFail writes the rejection ZC_ITEM_THROW_ACK with count=0. rAthena
// does not carry a failure code on the drop ack — count=0 is the only signal —
// so every rejection path funnels here.
func (s *DropService) sendDropFail(dropper *domain.Player) {
	_ = (&packet.ItemThrowAckResponse{Index: 0, Count: 0}).Encode(connWriter{dropper.Conn})
}

// broadcastFloorItem sends ZC_ITEM_FALL_ENTRY (0x0ADD) for the freshly dropped
// item to every player within AOI of its cell, so observers see it land. Same
// observer-resolution and write-failure policy as the combat drop path: non-player
// entities are skipped, and a dead observer socket is ignored (its own dispatch
// goroutine owns teardown).
func (s *DropService) broadcastFloorItem(mp *domain.Map, fi *domain.FloorItem) {
	msg := packet.ItemFallEntryResponse{
		ID:             uint32(fi.EntityID), //nolint:gosec // G115: EntityID is a uint32-derived aoi.EntityID
		NameID:         fi.NameID,
		Type:           fi.Type,
		Identified:     0,               // a bag drop is unidentified (fitem->item.identify == 0)
		X:              uint16(fi.PosX), //nolint:gosec // G115: map cell in int16 wire slot
		Y:              uint16(fi.PosY), //nolint:gosec // G115: map cell in int16 wire slot
		Amount:         fi.Amount,
		ShowDropEffect: 0, // no MVP/random-option drop effect for the basic slice
		DropEffectMode: 0,
	}
	for _, e := range mp.AOI.QueryVisible(int(fi.PosX), int(fi.PosY)) {
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

// DropHandler serves CZ_ITEM_DROP (0x0363 at PACKETVER 20250604). A verified
// connection (a player online via CZ_ENTER) is required; without it there is no
// bag to consume from. The 6-byte frame carries the source slot + amount.
type DropHandler struct {
	svc *DropService
}

// NewDropHandler binds the DropService that owns the bag/floor-item mutation.
func NewDropHandler(svc *DropService) *DropHandler {
	return &DropHandler{svc: svc}
}

// Handle implements gateway/domain.PacketHandler for CZ_ITEM_DROP.
func (h *DropHandler) Handle(ctx context.Context, conn gwdomain.Conn, frame gwdomain.Frame) error {
	req, err := packet.ParseCZDropItem(frame.Raw)
	if err != nil {
		return fmt.Errorf("parse CZ_ITEM_DROP: %w", err)
	}
	accountID := conn.Auth().AccountID
	if accountID == 0 {
		// No cached auth ⇒ the connection never passed the CZ_ENTER gate.
		// Tolerated by the gateway (the conn stays open) but the drop is
		// dropped — there is no verified identity to consume from.
		return errors.New("drop: connection has no verified account (CZ_ENTER not completed)")
	}
	return h.svc.Drop(ctx, accountID, req.InventoryIndex, req.Amount)
}

// Compile-time check that DropHandler satisfies the gateway handler shape.
var _ gwdomain.PacketHandler = (*DropHandler)(nil).Handle
