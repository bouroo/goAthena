// Package app: this file adds the M10b equip/unequip use case — a player wears
// or removes a bag item, the worn-location bitmask is persisted through the
// inventory bounded context, and the client's status panel is refreshed so the
// weapon/armor contribution shows up live. Equip is a world use case for the
// same reason pickup is: it crosses the player registry, the inventory port,
// and the statcalc seam. The bag itself lives in the inventory module; this use
// case only resolves that module's domain port.
//
// Fork fidelity (rathenaThailand @ current): clif_parse_EquipItem
// (clif.cpp:12115) → pc_equipitem (pc.cpp:14776) validates the request then, on
// success, sets inventory.u.items_inventory[n].equip = flag and answers with a
// ZC_REQ_WEAR_EQUIP_ACK (0x0999): result 0=fail, 1=ok, 2=low-level fail
// (clif.cpp:4301-4325). clif_parse_UnequipItem → pc_unequipitem clears the bit
// and answers ZC_REQ_TAKEOFF_EQUIP_ACK (0x099a), whose flag is wire-inverted for
// PACKETVER >= 20110824 (clif.cpp:4338) so 0=success, 1=failure on the wire. The
// stat recompute mirrors status_calc_pc: the equipment contributions fold into
// StatusInputs.Equipment so a fresh ZC_STATUS reflects worn gear (Atk2=weapon
// ATK, Def1=armor DEF). P2A adds the AREA look sync: equip/unequip also broadcasts
// a ZC_SPRITE_CHANGE (0x01d7) so the actor + AOI neighbors see the weapon/shield
// sprite update (rAthena clif_changelook, combined LOOK_WEAPON PACKETVER>=4 path).
package app

import (
	"context"
	"errors"
	"fmt"

	gwdomain "github.com/bouroo/goAthena/internal/modules/gateway/domain"
	invdomain "github.com/bouroo/goAthena/internal/modules/inventory/domain"
	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/equip"
	"github.com/bouroo/goAthena/pkg/ro/itemdb"
	"github.com/bouroo/goAthena/pkg/ro/packet"
	"github.com/bouroo/goAthena/pkg/ro/statcalc"
)

// equipAck results for ZC_REQ_WEAR_EQUIP_ACK (clif.cpp:4306-4309).
const (
	equipResultFail     uint8 = 0 // not equipment / slot not permitted / stale
	equipResultOK       uint8 = 1
	equipResultLowLevel uint8 = 2
)

// EquipService resolves CZ_REQ_WEAR_EQUIP and CZ_REQ_TAKEOFF_EQUIP: it validates
// the request against the item_db and the character's level, persists the
// worn-location bitmask through the inventory port, acks the client, and
// refreshes ZC_STATUS so worn gear shows up in the HUD. Every collaborator is
// the same kind the combat/spawn use cases hold; chars is the character module's
// read port (the same CharacterGetter SpawnService uses) so the recompute reads
// the authoritative StatusPoint + base stats rather than a stale snapshot.
type EquipService struct {
	items   invdomain.InventoryRepository
	itemDB  *itemdb.Registry
	players *domain.PlayerRegistry
	chars   domain.CharacterGetter
	fs      statcalc.FormulaSet
	maps    domain.MapStore
	look    domain.LookStore
}

// NewEquipService binds the equip collaborators. items is the inventory port the
// bitmask mutation goes through; a nil items (no inventory context wired) makes
// every equip a fail ack. itemDB resolves the item's permitted locations, level
// requirement, attack/defense, and view sprite; a nil itemDB fails every equip.
// players resolves the live session; chars reloads the character for the stat
// recompute; fs is the mode's formula set ZC_STATUS derives from; look persists
// the worn weapon/shield sprites so a relog keeps the look (chars is read-only).
func NewEquipService(items invdomain.InventoryRepository, itemDB *itemdb.Registry, players *domain.PlayerRegistry, chars domain.CharacterGetter, fs statcalc.FormulaSet, maps domain.MapStore, look domain.LookStore) *EquipService {
	return &EquipService{items: items, itemDB: itemDB, players: players, chars: chars, fs: fs, maps: maps, look: look}
}

// Equip wears the bag item at the given grid slot into the requested EQP_*
// position. It mirrors pc_equipitem: a no-session request is a silent drop; a
// rejected equip (unknown/non-equipment item, low level, slot the item does not
// permit, stale index) yields the fail ack and returns nil — an expected client
// outcome, not a session fault. Only an infra fault (repository load/persist,
// character reload) is returned so ProcessBytes can log it.
func (s *EquipService) Equip(ctx context.Context, accountID uint32, index uint16, requestedPos uint32) error {
	player, ok := s.players.ByAccount(accountID)
	if !ok {
		// No live session: nothing to ack and nowhere to write it.
		return nil
	}
	if s.items == nil || s.itemDB == nil {
		// No inventory context or item_db wired: cannot resolve the item, so the
		// request must still be answered with the fail ack.
		s.sendEquipFail(player, index)
		return nil
	}

	rows, err := s.items.LoadByChar(ctx, accountID, player.CharID)
	if err != nil {
		return fmt.Errorf("equip: load bag for account %d char %d: %w", accountID, player.CharID, err)
	}
	if int(index) >= len(rows) { //nolint:gosec // G115: index is a bag slot < MaxInventorySlots (100); len fits int
		// Forged or stale index (the slot the client cached no longer exists).
		s.sendEquipFail(player, index)
		return nil
	}
	row := rows[index]

	entry := s.itemDB.Get(int32(row.NameID)) //nolint:gosec // G115: NameID < 2^31 (item_db id); int32 cast is value-preserving
	if entry == nil {
		s.sendEquipFail(player, index)
		return nil
	}
	// itemdb loads the EQP_* bitmask from the item_db_equip.yml Locations block;
	// 0 means the item is not wearable (IT_ETC/usable/card). Fail ack, not equip.
	if entry.EquipLocations == 0 {
		s.sendEquipFail(player, index)
		return nil
	}
	// pc_equipitem's level gate (pc.cpp:14800): the char must meet the item's
	// EquipLevelMin. A low-level fail is its own result code so the client can
	// show "level too low".
	if int32(player.CLevel) < entry.EquipLevelMin { //nolint:gosec // G115: uint16 level → int32 for the signed EquipLevelMin compare
		s.sendEquipFailReason(player, index, equipResultLowLevel)
		return nil
	}
	// pc_equippoint_check (pc.cpp:14758): the client's requested position must
	// intersect the item's permitted locations. The intersection is the resolved
	// bitmask we persist (e.g. a Knife's Right_Hand only).
	resolved := requestedPos & entry.EquipLocations
	if resolved == 0 {
		s.sendEquipFail(player, index)
		return nil
	}

	updated, err := s.items.EquipItem(ctx, accountID, player.CharID, index, resolved)
	if err != nil {
		if errors.Is(err, invdomain.ErrItemNotFound) {
			// The slot vanished between the load and the persist (a concurrent
			// mutation). Fail ack, return.
			s.sendEquipFail(player, index)
			return nil
		}
		return fmt.Errorf("equip: persist char %d index %d: %w", player.CharID, index, err)
	}
	// rows predates the persist, so it still shows the slot unequipped; fold the
	// authoritative returned row in so the status recompute sees the worn gear.
	rows[index] = updated

	// Ack the client with the resolved slot and the item's view sprite. rAthena
	// emits the sprite only for visible slots; weapons carry no View column in
	// this fork's item_db (the client derives the weapon sprite from the weapon
	// class), so a weapon's ItemSpriteNumber is 0 — faithful to the loaded data.
	ack := packet.ReqWearEquipAckResponse{
		Index:            index,
		WearLocation:     updated.Equip,
		ItemSpriteNumber: uint16(entry.View), //nolint:gosec // G115: int32 View is a small client sprite id; uint16 wire slot
		Result:           equipResultOK,
	}
	if err := ack.Encode(connWriter{player.Conn}); err != nil {
		return fmt.Errorf("equip: encode ZC_REQ_WEAR_EQUIP_ACK char %d index %d: %w", player.CharID, index, err)
	}

	// status_calc_pc refresh: a fresh ZC_STATUS folds in the worn gear so Atk2
	// (weapon ATK) and Def1 (armor DEF) update live. A send fault is returned
	// for logging; the equip already persisted and acked, so the session
	// continues — the client refreshes on its next status burst.
	if err := s.broadcastLook(ctx, player, rows); err != nil {
		return fmt.Errorf("equip: %w", err)
	}
	return s.sendStatus(ctx, player, rows)
}

// Unequip removes the worn bag item at the given grid slot. It mirrors
// pc_unequipitem: a no-session request is a silent drop; a rejected takeoff
// (stale index, slot not worn) yields the wire-inverted fail ack (flag=1) and
// returns nil. The takeoff ack reports the slot the item was in; a fresh
// ZC_STATUS drops the gear contribution afterward.
func (s *EquipService) Unequip(ctx context.Context, accountID uint32, index uint16) error {
	player, ok := s.players.ByAccount(accountID)
	if !ok {
		return nil
	}
	if s.items == nil {
		s.sendTakeoffFail(player, index)
		return nil
	}

	rows, err := s.items.LoadByChar(ctx, accountID, player.CharID)
	if err != nil {
		return fmt.Errorf("unequip: load bag for account %d char %d: %w", accountID, player.CharID, err)
	}
	if int(index) >= len(rows) { //nolint:gosec // G115: index is a bag slot < MaxInventorySlots (100); len fits int
		s.sendTakeoffFail(player, index)
		return nil
	}
	prevEquip := rows[index].Equip
	if prevEquip == 0 {
		// The slot is not worn — nothing to take off. rAthena answers a takeoff
		// of an unequipped item with a fail; do the same.
		s.sendTakeoffFail(player, index)
		return nil
	}

	if _, err := s.items.UnequipItem(ctx, accountID, player.CharID, index); err != nil {
		if errors.Is(err, invdomain.ErrItemNotFound) {
			s.sendTakeoffFail(player, index)
			return nil
		}
		return fmt.Errorf("unequip: persist char %d index %d: %w", player.CharID, index, err)
	}
	// rows predates the persist, so the slot still shows worn; reflect the cleared
	// bitmask so the status recompute drops the gear contribution.
	rows[index].Equip = 0

	// Takeoff ack: the slot the item was in, with the wire-inverted flag (0 =
	// success for PACKETVER >= 20110824). The encoder does not invert again.
	ack := packet.ReqTakeoffEquipAckResponse{
		Index:        index,
		WearLocation: prevEquip,
		Flag:         0, // success
	}
	if err := ack.Encode(connWriter{player.Conn}); err != nil {
		return fmt.Errorf("unequip: encode ZC_REQ_TAKEOFF_EQUIP_ACK char %d index %d: %w", player.CharID, index, err)
	}

	if err := s.broadcastLook(ctx, player, rows); err != nil {
		return fmt.Errorf("unequip: %w", err)
	}
	return s.sendStatus(ctx, player, rows)
}

// equipmentStats folds the equipped bag rows into the right/left-side
// contributions ZC_STATUS carries separately from the naked base (Atk1/Def2 stay
// base-derived; Atk2/Def1/Mdef1 are the equipment deltas). A weapon (whose
// permitted locations include the right hand) adds its Attack to WeaponATK;
// every other worn item adds its Defense to ItemDEF. ItemMDEF is 0: this fork's
// item_db_equip.yml carries no mdef column, so there is no contribution to fold.
// Refine bonuses are omitted (fresh pickups are +0).
func equipmentStats(rows []invdomain.InventoryItem, itemDB *itemdb.Registry) statcalc.Equipment {
	if itemDB == nil {
		return statcalc.Equipment{}
	}
	var eq statcalc.Equipment
	for _, r := range rows {
		if r.Equip == 0 {
			continue
		}
		entry := itemDB.Get(int32(r.NameID)) //nolint:gosec // G115: NameID < 2^31; value-preserving cast
		if entry == nil {
			continue
		}
		if entry.EquipLocations&equip.HandRight != 0 {
			eq.WeaponATK += entry.Attack
		} else {
			eq.ItemDEF += entry.Defense
		}
	}
	return eq
}

// computeLook derives the worn-gear look sprites for ZC_SPRITE_CHANGE from the
// inventory bag. It mirrors rAthena pc.cpp's equip look logic: the right-hand
// weapon's class sprite (weapon_type, from the item's SubType) becomes
// LOOK_WEAPON's val; the left-hand armor's View becomes LOOK_SHIELD's val2. A
// left-hand weapon (dual wield) sets status.shield=W_FIST(0) and combines the two
// weapons into one weapon view via pc_calcweapontype; that combine is NOT mirrored
// here (single right-hand weapon only) — a documented simplification tracked as a
// follow-up. Two-handed weapons occupy both hands and correctly yield
// weapon=class, shield=0. A weapon is identified by its item_db location including
// the right hand (equip.HandRight), the same test equipmentStats uses.
func computeLook(rows []invdomain.InventoryItem, itemDB *itemdb.Registry) (weapon, shield uint16) {
	if itemDB == nil {
		return 0, 0
	}
	for _, r := range rows {
		if r.Equip == 0 {
			continue
		}
		entry := itemDB.Get(int32(r.NameID)) //nolint:gosec // G115: NameID < 2^31; value-preserving cast
		if entry == nil {
			continue
		}
		isWeapon := entry.EquipLocations&equip.HandRight != 0
		switch {
		case r.Equip&equip.HandRight != 0 && isWeapon:
			if c, ok := itemdb.WeaponClass(entry.SubType); ok {
				weapon = c
			}
		case r.Equip&equip.HandLeft != 0 && !isWeapon:
			shield = uint16(entry.View) //nolint:gosec // G115: small client view sprite id → uint16
		}
	}
	return weapon, shield
}

// broadcastLook recomputes the player's weapon/shield view sprites from the worn
// gear, persists them to the char DB so a logout/login cycle keeps the look, then
// broadcasts a ZC_SPRITE_CHANGE (0x01d7) to the actor + AOI neighbors, mirroring
// rAthena clif_changelook's PACKETVER>=4 combined LOOK_WEAPON AREA send
// (clif.cpp:3963). It also writes player.Weapon/Shield so a late-arriving
// observer's spawn packet carries the updated look. rAthena persists the look via
// chrif_save on logout; goAthena has no logout save, so the look is committed here
// at the moment it changes (SaveLook, like SavePosition).
//
// The fan-out is best-effort per the drop-service contract: a neighbor whose Conn
// write fails misses one cosmetic update and is reaped by the next movement/social
// broadcast. The SaveLook write and the map-store load are real infra faults and
// propagate; nil maps (no world wired, e.g. unit tests) skips the broadcast while
// still persisting the look and updating the in-memory fields.
func (s *EquipService) broadcastLook(ctx context.Context, player *domain.Player, rows []invdomain.InventoryItem) error {
	weapon, shield := computeLook(rows, s.itemDB)
	player.Weapon = weapon
	player.Shield = shield
	if err := s.look.SaveLook(ctx, player.AccountID, player.CharID, weapon, shield); err != nil {
		return fmt.Errorf("save look char %d: %w", player.CharID, err)
	}
	if s.maps == nil {
		return nil
	}
	mp, err := s.maps.Load(ctx, player.MapName)
	if err != nil {
		return fmt.Errorf("load map %q for look broadcast: %w", player.MapName, err)
	}
	resp := packet.SpriteChangeResponse{
		GID:  player.AccountID,
		Type: packet.LookWeapon,
		Val:  uint32(weapon), //nolint:gosec // G115: weapon_type < 2^16 → uint32 view slot
		Val2: uint32(shield), //nolint:gosec // G115: shield view sprite < 2^16 → uint32 slot
	}
	x, y, _ := player.Position()
	_ = resp.Encode(connWriter{player.Conn})
	for _, e := range mp.AOI.QueryVisible(int(x), int(y)) {
		if e.ID == player.EntityID {
			continue
		}
		neighbor, ok := s.players.ByAccount(uint32(e.ID))
		if !ok {
			continue
		}
		_ = resp.Encode(connWriter{neighbor.Conn})
	}
	return nil
}

// sendStatus reloads the character (authoritative StatusPoint + base stats) and
// emits a fresh ZC_STATUS whose Equipment reflects the current bag. It reuses
// the rows the caller already loaded post-mutation. A character or bag load
// fault is returned; an encode fault is returned too so a stuck write is logged.
func (s *EquipService) sendStatus(ctx context.Context, player *domain.Player, rows []invdomain.InventoryItem) error {
	char, err := s.chars.GetByID(ctx, player.AccountID, player.CharID)
	if err != nil {
		return fmt.Errorf("equip: reload char for account %d char %d: %w", player.AccountID, player.CharID, err)
	}
	in := statcalc.StatusInputs{
		Base: statcalc.Base{
			Level: char.BaseLevel,
			Str:   char.Str, Agi: char.Agi, Vit: char.Vit,
			Int: char.Int, Dex: char.Dex, Luk: char.Luk,
		},
		StatusPoint:    char.StatusPoint,
		Equipment:      equipmentStats(rows, s.itemDB),
		WeaponBaseASPD: noviceFistASPD,
	}
	if err := (statcalc.ZCStatus(in, s.fs)).Encode(connWriter{player.Conn}); err != nil {
		return fmt.Errorf("equip: encode ZC_STATUS char %d: %w", player.CharID, err)
	}
	return nil
}

// sendEquipFail writes the fail ZC_REQ_WEAR_EQUIP_ACK (result=0) to the player.
// rAthena zeroes WearLocation/ItemSpriteNumber on the fail branches, so only the
// index and the result byte carry meaning.
func (s *EquipService) sendEquipFail(player *domain.Player, index uint16) {
	s.sendEquipFailReason(player, index, equipResultFail)
}

// sendEquipFailReason writes a ZC_REQ_WEAR_EQUIP_ACK with a specific result
// (used by the low-level fail, result=2). WearLocation/ItemSpriteNumber stay 0.
func (s *EquipService) sendEquipFailReason(player *domain.Player, index uint16, result uint8) {
	ack := packet.ReqWearEquipAckResponse{Index: index, Result: result}
	_ = ack.Encode(connWriter{player.Conn})
}

// sendTakeoffFail writes the wire-inverted fail ZC_REQ_TAKEOFF_EQUIP_ACK
// (flag=1) to the player.
func (s *EquipService) sendTakeoffFail(player *domain.Player, index uint16) {
	ack := packet.ReqTakeoffEquipAckResponse{Index: index, Flag: 1}
	_ = ack.Encode(connWriter{player.Conn})
}

// EquipHandler adapts EquipService to the gateway dispatcher for
// CZ_REQ_WEAR_EQUIP_V5 (0x0998). It reads the verified account id from the
// connection (never the packet) and forwards the parsed index + requested
// position.
type EquipHandler struct {
	svc *EquipService
}

// NewEquipHandler binds the handler to its equip service.
func NewEquipHandler(svc *EquipService) *EquipHandler {
	return &EquipHandler{svc: svc}
}

// Handle implements gateway/domain.PacketHandler for CZ_REQ_WEAR_EQUIP_V5.
func (h *EquipHandler) Handle(ctx context.Context, conn gwdomain.Conn, frame gwdomain.Frame) error {
	req, err := packet.ParseCZReqWearEquip(frame.Raw)
	if err != nil {
		return fmt.Errorf("parse CZ_REQ_WEAR_EQUIP_V5: %w", err)
	}
	accountID := conn.Auth().AccountID
	if accountID == 0 {
		return errors.New("equip: connection has no verified account (CZ_ENTER not completed)")
	}
	return h.svc.Equip(ctx, accountID, req.Index, req.Position)
}

// TakeoffHandler adapts EquipService to the gateway dispatcher for
// CZ_REQ_TAKEOFF_EQUIP (0x00ab). It reads the verified account id from the
// connection and forwards the parsed index; the worn slot is resolved from the
// bag row server-side (the client's position field is ignored, matching
// clif_parse_UnequipItem).
type TakeoffHandler struct {
	svc *EquipService
}

// NewTakeoffHandler binds the handler to its equip service.
func NewTakeoffHandler(svc *EquipService) *TakeoffHandler {
	return &TakeoffHandler{svc: svc}
}

// Handle implements gateway/domain.PacketHandler for CZ_REQ_TAKEOFF_EQUIP.
func (h *TakeoffHandler) Handle(ctx context.Context, conn gwdomain.Conn, frame gwdomain.Frame) error {
	req, err := packet.ParseCZReqTakeoffEquip(frame.Raw)
	if err != nil {
		return fmt.Errorf("parse CZ_REQ_TAKEOFF_EQUIP: %w", err)
	}
	accountID := conn.Auth().AccountID
	if accountID == 0 {
		return errors.New("unequip: connection has no verified account (CZ_ENTER not completed)")
	}
	return h.svc.Unequip(ctx, accountID, req.Index)
}

// Compile-time checks that the handlers satisfy the gateway handler shape.
var (
	_ gwdomain.PacketHandler = (*EquipHandler)(nil).Handle
	_ gwdomain.PacketHandler = (*TakeoffHandler)(nil).Handle
)
