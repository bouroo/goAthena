// Package app: this file owns the CZ_NOTIFY_ACTORINIT (LoadEndAck, 0x007d)
// reply — the inventory/skill/hotkey init burst rAthena sends on
// map-load-complete (clif.cpp clif_parse_LoadEndAck). It is split from spawn.go
// because the populated path crosses the inventory repository + item_db, which
// the spawn/enter path does not touch.
package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	gwdomain "github.com/bouroo/goAthena/internal/modules/gateway/domain"
	invdomain "github.com/bouroo/goAthena/internal/modules/inventory/domain"
	"github.com/bouroo/goAthena/pkg/ro/itemdb"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// wireITArmor / wireITWeapon are the item_db IT_* type bytes (itemdb.WireType
// output) rAthena routes to ZC_INVENTORY_ITEMLIST_EQUIP; every other type
// (healing/usable/etc/card/ammo/cash) goes to ZC_INVENTORY_ITEMLIST_NORMAL.
const (
	wireITArmor  uint16 = 4 // IT_ARMOR
	wireITWeapon uint16 = 5 // IT_WEAPON
)

// SendLoadEndAckInit emits the init burst rAthena sends in clif_parse_LoadEndAck:
// ZC_INVENTORY_START bracketing ITEMLIST_NORMAL + ITEMLIST_EQUIP (populated from
// the persisted bag), ZC_INVENTORY_END, then the skill and hotkey lists. The
// skill/hotkey lists are empty today (skill progression is not persisted yet and
// a Novice starts with none) — they are well-formed empty frames, not omitted.
//
// When any collaborator is absent (no inventory store, no item_db, or no live
// player for accountID) it degrades to the well-formed empty lists — the
// nil-contract the rest of the combat/inventory slice upholds — so the init
// handshake always completes rather than leaving the client's windows
// uninitialized.
func (s *SpawnService) SendLoadEndAckInit(ctx context.Context, conn gwdomain.Conn, accountID uint32) error {
	normal, equip := s.inventoryLists(ctx, accountID)
	w := connWriter{conn}
	for _, f := range [][]byte{
		packet.EncodeInventoryStart(),
		encodeNormalList(normal),
		encodeEquipList(equip),
		packet.EncodeInventoryEnd(),
		packet.EncodeEmptySkillList(),
		packet.EncodeEmptyHotkeyList(0),
	} {
		if _, err := w.Write(f); err != nil {
			return fmt.Errorf("spawn: send LoadEndAck init: %w", err)
		}
	}
	return nil
}

// inventoryLists splits the live player's bag into the normal/equip wire slices
// the two ITEMLIST packets carry. A nil inventory store, nil item_db, a missing
// player (a LoadEndAck before the CZ_ENTER spawn completed), or a load fault all
// yield empty slices so SendLoadEndAckInit emits the empty lists. accountID comes
// from the verified conn auth cache, never the packet; the charID is resolved
// through the player registry (the impersonation guard the combat/pickup paths
// share). Each bag row's type/view is resolved from item_db so an item absent
// from item_db still lists (as IT_ETC) rather than disappearing.
func (s *SpawnService) inventoryLists(ctx context.Context, accountID uint32) ([]packet.InventoryNormalItem, []packet.InventoryEquipItem) {
	if s.items == nil || s.itemDB == nil {
		return nil, nil
	}
	player, found := s.registry.ByAccount(accountID)
	if !found {
		return nil, nil
	}
	bag, err := s.items.LoadByChar(ctx, accountID, player.CharID)
	if err != nil {
		return nil, nil
	}
	var normal []packet.InventoryNormalItem
	var equip []packet.InventoryEquipItem
	for _, it := range bag {
		typ, view := s.itemType(it.NameID)
		switch typ {
		case wireITArmor, wireITWeapon:
			equip = append(equip, equipItemWire(it, typ, view))
		default:
			normal = append(normal, normalItemWire(it, typ))
		}
	}
	return normal, equip
}

// itemType resolves the wire IT_* type byte and client view sprite for an item
// nameid from item_db. An unknown item (nil entry) yields IT_ETC and no view, so
// it still appears in the normal list rather than being dropped.
func (s *SpawnService) itemType(nameID uint32) (typ uint16, view uint16) {
	entry := s.itemDB.Get(int32(nameID)) //nolint:gosec // G115: nameID is a positive item id
	if entry == nil {
		return itemdb.WireType(""), 0
	}
	return itemdb.WireType(entry.Type), uint16(entry.View) //nolint:gosec // G115: item_db View fits uint16
}

// normalItemWire maps a bag row to the NORMALITEM_INFO wire entry.
func normalItemWire(it invdomain.InventoryItem, typ uint16) packet.InventoryNormalItem {
	return packet.InventoryNormalItem{
		Index:           it.Index,
		ITID:            uint16(it.NameID), //nolint:gosec // G115: item nameid fits uint16 at PACKETVER 20250604
		Type:            uint8(typ),        //nolint:gosec // G115: IT_* type 0-13 fits uint8
		Count:           uint16(it.Amount), //nolint:gosec // G115: stack count fits uint16
		Card:            cardsWire(it.Cards),
		HireExpireDate:  it.ExpireTime,
		BindOnEquipType: uint16(it.Bound),
		Flag:            itemFlag(it.Identified),
	}
}

// equipItemWire maps a bag row to the EQUIPITEM_INFO wire entry, adding the
// equip-location bitmask, refine level, view sprite, and option slots the normal
// variant omits.
func equipItemWire(it invdomain.InventoryItem, typ, view uint16) packet.InventoryEquipItem {
	return packet.InventoryEquipItem{
		Index:            it.Index,
		ITID:             uint16(it.NameID), //nolint:gosec // G115: item nameid fits uint16
		Type:             uint8(typ),        //nolint:gosec // G115: IT_* type 0-13 fits uint8
		Location:         it.Equip,
		RefiningLevel:    it.Refine,
		Card:             cardsWire(it.Cards),
		HireExpireDate:   it.ExpireTime,
		BindOnEquipType:  uint16(it.Bound),
		ItemSpriteNumber: view,
		OptionCount:      optionCount(it.Options),
		OptionData:       optionsWire(it.Options),
		Flag:             itemFlag(it.Identified),
	}
}

// cardsWire widens the bag's uint32 card ids to the uint16[4] wire slot. Card ids
// are small item ids; the truncation is value-preserving for every valid card.
func cardsWire(c [4]uint32) [4]uint16 {
	return [4]uint16{
		uint16(c[0]), uint16(c[1]), uint16(c[2]), uint16(c[3]), //nolint:gosec // G115: card ids fit uint16
	}
}

// optionsWire maps the bag's five option slots to the wire shape. A zero-ID slot
// is empty; the wire still writes all five slots, the client reads OptionCount.
func optionsWire(opts [5]invdomain.ItemOption) [5]packet.ItemOption {
	var w [5]packet.ItemOption
	for i, o := range opts {
		w[i] = packet.ItemOption{Index: o.ID, Value: uint16(o.Value), Param: uint8(o.Param)} //nolint:gosec // G115: option fields fit their wire width
	}
	return w
}

// optionCount returns how many of the five option slots are populated (non-zero
// ID). The client reads only the first OptionCount entries.
func optionCount(opts [5]invdomain.ItemOption) uint8 {
	var n uint8
	for _, o := range opts {
		if o.ID != 0 {
			n++
		}
	}
	return n
}

// itemFlag packs the IsIdentified bit (bit 0) shared by both ITEMLIST variants.
// IsDamaged (equip bit 1) and PlaceETCTab (bit 1 normal / bit 2 equip) are false
// today — no broken items, no etc-tab override — so only the identified bit is
// set.
func itemFlag(identified bool) uint8 {
	if identified {
		return 1
	}
	return 0
}

// encodeNormalList encodes the ZC_INVENTORY_ITEMLIST_NORMAL packet; on the
// (unreachable for a real bag) length-overflow error it falls back to the empty
// frame so the init burst stays well-formed.
func encodeNormalList(items []packet.InventoryNormalItem) []byte {
	var b bytes.Buffer
	if err := (packet.InventoryListNormalResponse{Items: items}).Encode(&b); err != nil {
		return packet.EncodeEmptyInventoryListNormal()
	}
	return b.Bytes()
}

// encodeEquipList encodes the ZC_INVENTORY_ITEMLIST_EQUIP packet; see
// encodeNormalList for the overflow fallback.
func encodeEquipList(items []packet.InventoryEquipItem) []byte {
	var b bytes.Buffer
	if err := (packet.InventoryListEquipResponse{Items: items}).Encode(&b); err != nil {
		return packet.EncodeEmptyInventoryListEquip()
	}
	return b.Bytes()
}

// LoadEndAckHandler serves CZ_NOTIFY_ACTORINIT (0x007d), the client's
// map-load-complete signal. A verified connection (CZ_ENTER done) is replied to
// with the inventory/skill/hotkey init burst; the 2-byte cmd-only frame carries
// no body, so nothing is parsed. Before this handler the opcode hit ErrNoHandler
// on every enter/rewarp and the init packets were never sent.
type LoadEndAckHandler struct {
	svc *SpawnService
}

// NewLoadEndAckHandler binds the SpawnService that owns the init burst.
func NewLoadEndAckHandler(svc *SpawnService) *LoadEndAckHandler {
	return &LoadEndAckHandler{svc: svc}
}

// Handle implements gateway/domain.PacketHandler for CZ_NOTIFY_ACTORINIT.
func (h *LoadEndAckHandler) Handle(ctx context.Context, conn gwdomain.Conn, _ gwdomain.Frame) error {
	if conn.Auth().AccountID == 0 {
		return errors.New("load-end-ack: connection has no verified account (CZ_ENTER not completed)")
	}
	return h.svc.SendLoadEndAckInit(ctx, conn, conn.Auth().AccountID)
}

// compile-time: LoadEndAckHandler satisfies the gateway handler contract.
var _ gwdomain.PacketHandler = (*LoadEndAckHandler)(nil).Handle
