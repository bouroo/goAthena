package app

import (
	"context"
	"encoding/binary"
	"errors"
	"strconv"

	"github.com/panjf2000/gnet/v2"

	dialogdomain "github.com/bouroo/goAthena/internal/modules/content/domain"
	worldapp "github.com/bouroo/goAthena/internal/modules/world/app"
	worlddomain "github.com/bouroo/goAthena/internal/modules/world/domain"
	ropacket "github.com/bouroo/goAthena/pkg/ro/packet"
)

// mapHandler is one entry in the map-server dispatch table and the function that
// processes a complete frame of that opcode.
//
// Most map packets are fixed-length: size is their constant byte count. The few
// variable-length packets (e.g. CZ_INPUT_EDITDLGSTR 0x01d5) leave size at 0 and
// set frameSize, which derives the full frame length from the buffered bytes and
// reports whether the whole frame has arrived — mirroring the fixed-size wait in
// OnTraffic without disturbing it.
type mapHandler struct {
	// size is the fixed frame byte count for constant-length packets. Ignored
	// when frameSize is non-nil.
	size int
	// frameSize, when set, derives the full frame length for a variable-length
	// packet from the buffered bytes (its on-wire uint16 length at offset 2) and
	// returns false until the whole frame has buffered. nil means use size.
	frameSize func(c gnet.Conn) (int, bool)
	// fn receives the authed identity resolved on the eventloop so handlers never
	// read c.Context() off-loop, where gnet's conn.release() races on close.
	fn func(s *MapServer, c gnet.Conn, auth *mapAuth, frame []byte)
}

// frameLen returns the full frame byte count for this opcode and whether that
// many bytes are already buffered. Fixed-length packets compare size directly;
// variable-length packets delegate to frameSize. Callers (OnTraffic) read this
// once and then Next(n), keeping the size decision in one place.
func (h mapHandler) frameLen(c gnet.Conn) (n int, ready bool) {
	if h.frameSize != nil {
		return h.frameSize(c)
	}
	return h.size, c.InboundBuffered() >= h.size
}

// mapHandlers is the opcode→handler table the map server dispatches against.
// Fixed-length packets carry their constant size; variable-length packets carry
// a frameSize func instead. A connection's first packet is always CZ_ENTER (the
// trust gate); the rest are only valid post-auth.
func mapHandlers() map[uint16]mapHandler {
	return map[uint16]mapHandler{
		0x0072:                              {size: czEnterSize, fn: (*MapServer).handleEnterFrame},
		0x007d:                              {size: 2, fn: (*MapServer).handleLoadEndAck},
		0x0085:                              {size: 5, fn: (*MapServer).handleRequestMove},
		0x0089:                              {size: 7, fn: (*MapServer).handleActionRequest},                         // CZ_ACTION_REQUEST
		0x0090:                              {size: 7, fn: (*MapServer).handleContactNPC},                            // CZ_CONTACT_NPC (NPC click)
		0x00b8:                              {size: 7, fn: (*MapServer).handleChooseMenu},                            // CZ_CHOOSE_MENU
		0x00b9:                              {size: 6, fn: (*MapServer).handleReqNextScript},                         // CZ_REQ_NEXT_SCRIPT
		0x0143:                              {size: 10, fn: (*MapServer).handleInputEditDlg},                         // CZ_INPUT_EDITDLG
		0x01d5:                              {frameSize: variableFrameSize, fn: (*MapServer).handleInputEditDlgStr},  // CZ_INPUT_EDITDLGSTR (variable length)
		0x0146:                              {size: 6, fn: (*MapServer).handleCloseDialog},                           // CZ_CLOSE_DIALOG
		ropacket.HeaderCZACKSELECTDEALTYPE:  {size: 7, fn: (*MapServer).handleAckSelectDealtype},                     // CZ_ACK_SELECT_DEALTYPE (NPC shop open)
		ropacket.HeaderCZPCPURCHASEITEMLIST: {frameSize: variableFrameSize, fn: (*MapServer).handlePurchaseItemList}, // CZ_PC_PURCHASE_ITEMLIST (variable)
		ropacket.HeaderCZPCSELLITEMLIST:     {frameSize: variableFrameSize, fn: (*MapServer).handleSellItemList},     // CZ_PC_SELL_ITEMLIST (variable)
		0x0362:                              {size: 6, fn: (*MapServer).handleItemPickup},                            // CZ_ITEM_PICKUP @ 20250604
		0x0363:                              {size: 6, fn: (*MapServer).handleItemDrop},                              // CZ_ITEM_DROP @ 20250604
		0x0438:                              {size: 10, fn: (*MapServer).handleUseSkill2},                            // CZ_USE_SKILL2 @ 20250604 (clif_shuffle.hpp:4750)
		0x0af4:                              {size: 11, fn: (*MapServer).handleUseSkillToPos},                        // CZ_USE_SKILL_TOPOS @ 20250604 (clif_packetdb.hpp:1905)
		ropacket.HeaderCZTRADEREQUEST:       {size: 6, fn: (*MapServer).handleTradeRequest},                          // CZ_TRADE_REQUEST 0x00e4 (cmd+targetGID)
		ropacket.HeaderCZTRADEACK:           {size: 3, fn: (*MapServer).handleTradeAck},                              // CZ_TRADE_ACK 0x00e6 (cmd+type)
		ropacket.HeaderCZADDEXCHANGEITEM:    {size: 8, fn: (*MapServer).handleAddExchangeItem},                       // CZ_ADD_EXCHANGE_ITEM 0x00e8 (cmd+index+amount)
		ropacket.HeaderCZTRADEOK:            {size: 2, fn: (*MapServer).handleTradeOK},                               // CZ_TRADE_OK 0x00eb (cmd only)
		ropacket.HeaderCZTRADECANCEL:        {size: 2, fn: (*MapServer).handleTradeCancel},                           // CZ_TRADE_CANCEL 0x00ed (cmd only)
		ropacket.HeaderCZREQWEAREQUIPV5:     {size: 8, fn: (*MapServer).handleReqWearEquip},                          // CZ_REQ_WEAR_EQUIP_V5 0x0998 (cmd+index+position)
		ropacket.HeaderCZREQTAKEOFFEQUIP:    {size: 4, fn: (*MapServer).handleReqTakeoffEquip},                       // CZ_REQ_TAKEOFF_EQUIP 0x00ab (cmd+index)
	}
}

// handleContactNPC starts an NPC dialog script on click (CZ_CONTACT_NPC 0x0090).
func (s *MapServer) handleContactNPC(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		return
	}
	req, err := ropacket.ParseCZContactNPC(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_CONTACT_NPC", "err", err)
		return
	}
	s.content.StartDialog(auth.accountID, auth.charID, req.AID, gnetWriter{c: c})
}

// handleReqNextScript advances an active dialog (CZ_REQ_NEXT_SCRIPT 0x00b9).
func (s *MapServer) handleReqNextScript(_ gnet.Conn, auth *mapAuth, _ []byte) {
	if auth == nil {
		return
	}
	s.content.Signal(auth.accountID, dialogdomain.DialogSignal{Advance: true})
}

// handleChooseMenu delivers a menu selection (CZ_CHOOSE_MENU 0x00b8).
func (s *MapServer) handleChooseMenu(_ gnet.Conn, auth *mapAuth, frame []byte) {
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
func (s *MapServer) handleInputEditDlg(_ gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		return
	}
	req, err := ropacket.ParseCZInputEditDlg(frame)
	if err != nil {
		return
	}
	s.content.Signal(auth.accountID, dialogdomain.DialogSignal{Input: strconv.FormatInt(int64(req.Value), 10)})
}

// handleInputEditDlgStr delivers a text input (CZ_INPUT_EDITDLGSTR 0x01d5). The
// frame is already detached and length-resolved by the dispatcher; this mirrors
// the numeric handler, substituting the raw string value for a decimal string.
func (s *MapServer) handleInputEditDlgStr(_ gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		return
	}
	req, err := ropacket.ParseCZInputEditDlgStr(frame)
	if err != nil {
		return
	}
	s.content.Signal(auth.accountID, dialogdomain.DialogSignal{Input: req.Value})
}

// variableFrameSize derives the full byte length of a length-prefixed variable
// packet by reading its uint16 total-length field at offset 2 — the rAthena
// convention for non-constant-length frames (CZ_INPUT_EDITDLGSTR 0x01d5 today).
// It returns false until the whole frame has buffered. A malformed length
// smaller than its own header resyncs over the 2-byte opcode (matching
// unhandledSkip), so the dispatcher never spins on a zero-length read.
func variableFrameSize(c gnet.Conn) (int, bool) {
	prefix, err := c.Peek(4)
	if err != nil {
		return 0, false // length field not fully buffered yet
	}
	n := int(binary.LittleEndian.Uint16(prefix[2:4]))
	if n < 4 {
		return 2, true // malformed length prefix: resync over the header
	}
	if c.InboundBuffered() < n {
		return 0, false // wait for the rest of the frame
	}
	return n, true
}

// handleCloseDialog cancels an active dialog (CZ_CLOSE_DIALOG 0x0146).
func (s *MapServer) handleCloseDialog(_ gnet.Conn, auth *mapAuth, _ []byte) {
	if auth == nil {
		return
	}
	s.content.Signal(auth.accountID, dialogdomain.DialogSignal{Cancel: true})
}

// CZ_ACK_SELECT_DEALTYPE type byte (rAthena clif_parse_NpcSelectDealType) and the
// ZC_PC_PURCHASE/SELL_RESULT result byte. The wire carries raw bytes, so they are
// defined here at the dispatch layer rather than in the packet encoder.
const (
	dealTypeBuy    uint8 = 0
	dealTypeSell   uint8 = 1
	dealTypeCancel uint8 = 2

	shopResultSuccess uint8 = 0
	shopResultFailed  uint8 = 1
)

// handleAckSelectDealtype handles CZ_ACK_SELECT_DEALTYPE (0x00c5, 7B): the client
// picked Buy/Sell/Cancel on a shop NPC. It resolves the NPC GID to a shop name,
// threads that name for the following purchase/sell frames (which carry item
// entries, not the NPC id), and emits the priced buy list (Buy) or sell list
// (Sell). Cancel and unknown NPCs are no-ops that keep the connection alive.
func (s *MapServer) handleAckSelectDealtype(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		s.log.Warn("map: CZ_ACK_SELECT_DEALTYPE from unauthed conn")
		return
	}
	if s.shops == nil || s.shopStore == nil {
		s.log.Debug("map: shop not wired, ignoring CZ_ACK_SELECT_DEALTYPE")
		return
	}
	req, err := ropacket.ParseCZAckSelectDealType(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_ACK_SELECT_DEALTYPE", "err", err)
		return
	}
	shopName, ok := s.shopStore.ShopForNPC(context.Background(), req.NpcID)
	if !ok {
		s.log.Debug("map: CZ_ACK_SELECT_DEALTYPE for non-shop NPC", "npc", req.NpcID)
		return
	}
	s.setOpenedShop(auth.charID, shopName)

	switch req.Type {
	case dealTypeBuy:
		s.writePurchaseItemList(c, shopName)
	case dealTypeSell:
		s.writeSellItemList(context.Background(), c, shopName, auth.accountID, auth.charID)
	default:
		// dealTypeCancel (2) and any unknown value: close the deal window, no list.
	}
}

// handlePurchaseItemList handles CZ_PC_PURCHASE_ITEMLIST (0x00c8, variable): the
// player's buy request against the last-opened shop. Each entry is one
// (itemId, amount); ShopService charges zeny and grants the item. On any entry
// error it emits ZC_PC_PURCHASE_RESULT(failed) and keeps the connection alive.
// Partial success is not rolled back (documented simplification: earlier entries
// in the same request that succeeded stay bought).
func (s *MapServer) handlePurchaseItemList(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		s.log.Warn("map: CZ_PC_PURCHASE_ITEMLIST from unauthed conn")
		return
	}
	if s.shops == nil {
		s.log.Debug("map: shop not wired, ignoring CZ_PC_PURCHASE_ITEMLIST")
		return
	}
	shopName, ok := s.openedShop(auth.charID)
	if !ok {
		s.log.Debug("map: CZ_PC_PURCHASE_ITEMLIST with no opened shop", "gid", auth.charID)
		s.writePurchaseResult(c, shopResultFailed)
		return
	}
	req, err := ropacket.ParseCZPCPurchaseItemList(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_PC_PURCHASE_ITEMLIST", "err", err)
		s.writePurchaseResult(c, shopResultFailed)
		return
	}
	ctx := context.Background()
	result := shopResultSuccess
	for _, e := range req.Entries {
		if err := s.shops.Buy(ctx, auth.charID, shopName, e.ItemID, int(e.Amount)); err != nil {
			s.log.Warn("map: shop buy", "shop", shopName, "item", e.ItemID, "amount", e.Amount, "err", err)
			result = shopResultFailed
			break
		}
	}
	s.writePurchaseResult(c, result)
}

// handleSellItemList handles CZ_PC_SELL_ITEMLIST (0x00c9, variable): the player's
// sell request. Each entry is (index, amount) where index is the client inventory
// slot. Two simplifications are documented honestly rather than faked:
//
//   - index resolution: the client slot is assumed to match LoadByChar's list
//     order (rAthena assigns client indices during the init burst and they are
//     not guaranteed equal to DB row order). The same assumption backs the sell
//     list emitted on open, so index ↔ item stays consistent within a session.
//   - pricing: the shop pays its catalog SellPrice (real rAthena uses the
//     item_db sell price + overcharge, not just the shop catalog).
//
// On any error it emits ZC_PC_SELL_RESULT(failed) and keeps the connection alive.
func (s *MapServer) handleSellItemList(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		s.log.Warn("map: CZ_PC_SELL_ITEMLIST from unauthed conn")
		return
	}
	if s.shops == nil {
		s.log.Debug("map: shop not wired, ignoring CZ_PC_SELL_ITEMLIST")
		return
	}
	shopName, ok := s.openedShop(auth.charID)
	if !ok {
		s.log.Debug("map: CZ_PC_SELL_ITEMLIST with no opened shop", "gid", auth.charID)
		s.writeSellResult(c, shopResultFailed)
		return
	}
	req, err := ropacket.ParseCZPCSellItemList(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_PC_SELL_ITEMLIST", "err", err)
		s.writeSellResult(c, shopResultFailed)
		return
	}
	ctx := context.Background()
	items, err := s.inv.LoadByChar(ctx, auth.accountID, auth.charID)
	if err != nil {
		s.log.Error("map: load inventory for sell", "err", err)
		s.writeSellResult(c, shopResultFailed)
		return
	}
	result := shopResultSuccess
	for _, e := range req.Entries {
		idx := int(e.Index)
		if idx >= len(items) {
			result = shopResultFailed
			break
		}
		it := items[idx]
		if it.IsEquipped() {
			result = shopResultFailed
			break
		}
		price, ok := s.shops.SellPriceFor(shopName, it.NameID)
		if !ok {
			result = shopResultFailed
			break
		}
		if err := s.shops.Sell(ctx, auth.charID, it.ID, it.NameID, int(e.Amount), price); err != nil {
			s.log.Warn("map: shop sell", "shop", shopName, "item", it.NameID, "amount", e.Amount, "err", err)
			result = shopResultFailed
			break
		}
	}
	s.writeSellResult(c, result)
}

// writePurchaseItemList emits ZC_PC_PURCHASE_ITEMLIST: the shop's priced buy
// catalog. ItemType/ViewSprite/Location come from item_db, not the shop catalog,
// so they are zero until the item_db loader resolves them (M5b); Price ==
// DiscountPrice (no discount model yet).
func (s *MapServer) writePurchaseItemList(c gnet.Conn, shopName string) {
	items, ok := s.shops.CatalogItems(shopName)
	if !ok {
		return // shop vanished between open and list; nothing to send
	}
	buy := make([]ropacket.ShopBuyItem, 0, len(items))
	for _, it := range items {
		price := uint32(it.Price) //nolint:gosec // G115: catalog prices are non-negative zeny; int32 is the domain's zeny type.
		buy = append(buy, ropacket.ShopBuyItem{
			ItemID:        it.NameID,
			Price:         price,
			DiscountPrice: price,
		})
	}
	resp := ropacket.PurchaseItemListResponse{Items: buy}
	out := make([]byte, resp.Size())
	if err := resp.Encode(sliceWriter(out)); err != nil {
		s.log.Error("map: encode ZC_PC_PURCHASE_ITEMLIST", "err", err)
		return
	}
	_ = c.AsyncWrite(out, nil)
}

// writeSellItemList emits ZC_PC_SELL_ITEMLIST: the player's inventory priced for
// resale. Index is the LoadByChar list position (matches the sell handler's
// resolution); Price == Overcharge (no overcharge model yet). Only items the shop
// trades appear (pricing simplification, see handleSellItemList).
func (s *MapServer) writeSellItemList(ctx context.Context, c gnet.Conn, shopName string, accountID, charID uint32) {
	items, err := s.inv.LoadByChar(ctx, accountID, charID)
	if err != nil {
		s.log.Error("map: load inventory for sell list", "err", err)
		return
	}
	sell := make([]ropacket.ShopSellItem, 0, len(items))
	for i, it := range items {
		if it.IsEquipped() {
			continue
		}
		sellPrice, ok := s.shops.SellPriceFor(shopName, it.NameID)
		if !ok {
			continue
		}
		price := uint32(sellPrice) //nolint:gosec // G115: catalog sell prices are non-negative zeny; int32 is the domain's zeny type.
		sell = append(sell, ropacket.ShopSellItem{
			Index:      uint16(i), //nolint:gosec // G115: list position, bounded by MAX_INVENTORY
			Price:      price,
			Overcharge: price,
		})
	}
	resp := ropacket.SellItemListResponse{Items: sell}
	out := make([]byte, resp.Size())
	if err := resp.Encode(sliceWriter(out)); err != nil {
		s.log.Error("map: encode ZC_PC_SELL_ITEMLIST", "err", err)
		return
	}
	_ = c.AsyncWrite(out, nil)
}

// writePurchaseResult emits ZC_PC_PURCHASE_RESULT (0=success, 1=failed).
func (s *MapServer) writePurchaseResult(c gnet.Conn, result uint8) {
	resp := ropacket.PurchaseResultResponse{Result: result}
	out := make([]byte, resp.Size())
	if err := resp.Encode(sliceWriter(out)); err != nil {
		s.log.Error("map: encode ZC_PC_PURCHASE_RESULT", "err", err)
		return
	}
	_ = c.AsyncWrite(out, nil)
}

// writeSellResult emits ZC_PC_SELL_RESULT (0=success, 1=failed).
func (s *MapServer) writeSellResult(c gnet.Conn, result uint8) {
	resp := ropacket.SellResultResponse{Result: result}
	out := make([]byte, resp.Size())
	if err := resp.Encode(sliceWriter(out)); err != nil {
		s.log.Error("map: encode ZC_PC_SELL_RESULT", "err", err)
		return
	}
	_ = c.AsyncWrite(out, nil)
}

// handleEnterFrame wraps handleEnter to satisfy the dispatch signature (the
// frame is already detached from gnet's ring buffer by the caller).
func (s *MapServer) handleEnterFrame(c gnet.Conn, auth *mapAuth, frame []byte) {
	s.handleEnter(c, auth, frame)
}

// handleLoadEndAck handles CZ_NOTIFY_ACTORINIT (0x007d, 2B cmd-only). This is
// the signal the client finished loading the map; rAthena replies with the
// inventory/skill/hotkey init burst. The burst is: ZC_INVENTORY_START →
// ZC_INVENTORY_ITEMLIST_NORMAL → ZC_INVENTORY_ITEMLIST_EQUIP → ZC_INVENTORY_END.
// Populated item lists (with item_db type/view resolution) land in M5b; for a
// fresh character (or before item_db is loaded) the empty lists are correct.
func (s *MapServer) handleLoadEndAck(c gnet.Conn, auth *mapAuth, _ []byte) {
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
func (s *MapServer) handleRequestMove(c gnet.Conn, auth *mapAuth, frame []byte) {
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

	// Broadcast the walk to OTHER nearby clients so they see the mover travel.
	// ZC_UNIT_WALKING (0x09fd) carries the mover's GID and is distinct from the
	// self-only ZC_NOTIFY_PLAYERMOVE (0x0087) emitted above — the anchor is the
	// destination cell, so any player who can see where the mover ends up is told.
	if wbuf, ok := encodeUnitWalk(s, unitWalkFromEntity(src, req.DestX, req.DestY)); ok {
		s.broadcast(wbuf, src.Map, worlddomain.Position{X: req.DestX, Y: req.DestY}, auth.charID)
	}
}

// objectTypePC is the ZC_SPAWN_UNIT / ZC_UNIT_WALKING object-type byte for a
// player character (rAthena's clif_bl_type: 0=PC).
const objectTypePC uint8 = 0

// unitWalkFromEntity builds the ZC_UNIT_WALKING (0x09fd) observer broadcast for
// a PC moving from src to dest. The mover's own move-ack (ZC_NOTIFY_PLAYERMOVE
// 0x0087) is a separate packet; this is what OTHER nearby clients receive so
// they see the sprite travel. Look fields come from the entity captured before
// the move, whose position is still the move's source cell.
func unitWalkFromEntity(src worlddomain.Entity, destX, destY int16) ropacket.UnitWalkingResponse {
	return ropacket.UnitWalkingResponse{
		ObjectType: objectTypePC,
		AID:        src.Account,
		GID:        uint32(src.ID), //nolint:gosec // G115: EntityID wraps a uint32 char_id.
		Speed:      src.Speed,
		Job:        src.Job,
		Head:       src.Head,
		Weapon:     src.Weapon,
		Shield:     src.Shield,
		Sex:        src.Sex,
		SrcX:       src.Pos.X,
		SrcY:       src.Pos.Y,
		DestX:      destX,
		DestY:      destY,
		XSize:      5, // rAthena hardcodes 5 for PCs.
		YSize:      5,
		CLevel:     src.Level,
		MaxHP:      src.MaxHP,
		HP:         src.HP,
		Body:       src.Job,
		Name:       src.Name,
	}
}

// spawnUnitFromEntity builds the ZC_SPAWN_UNIT (0x09fe) broadcast for a PC that
// just entered a map, so OTHER nearby clients see the player appear.
func spawnUnitFromEntity(e worlddomain.Entity) ropacket.SpawnUnitResponse {
	return ropacket.SpawnUnitResponse{
		ObjectType: objectTypePC,
		AID:        e.Account,
		GID:        uint32(e.ID), //nolint:gosec // G115: EntityID wraps a uint32 char_id.
		Speed:      e.Speed,
		Job:        e.Job,
		Head:       e.Head,
		Weapon:     e.Weapon,
		Shield:     e.Shield,
		Sex:        e.Sex,
		PosX:       e.Pos.X,
		PosY:       e.Pos.Y,
		Dir:        e.Dir,
		XSize:      5, // rAthena hardcodes 5 for PCs.
		YSize:      5,
		CLevel:     e.Level,
		MaxHP:      e.MaxHP,
		HP:         e.HP,
		Body:       e.Job,
		Name:       e.Name,
	}
}

// encodeUnitWalk encodes a UnitWalkingResponse into a fresh buffer, logging and
// returning ok=false on failure so the caller can skip the broadcast without a
// wire error.
func encodeUnitWalk(s *MapServer, r ropacket.UnitWalkingResponse) ([]byte, bool) {
	buf := make([]byte, r.Size())
	if err := r.Encode(sliceWriter(buf)); err != nil {
		s.log.Error("map: encode unit-walking", "err", err)
		return nil, false
	}
	return buf, true
}

// encodeSpawnUnit encodes a SpawnUnitResponse into a fresh buffer, logging and
// returning ok=false on failure.
func encodeSpawnUnit(s *MapServer, r ropacket.SpawnUnitResponse) ([]byte, bool) {
	buf := make([]byte, r.Size())
	if err := r.Encode(sliceWriter(buf)); err != nil {
		s.log.Error("map: encode spawn-unit", "err", err)
		return nil, false
	}
	return buf, true
}

// handleItemPickup handles CZ_ITEM_PICKUP (0x0362, 6B): parse GroundID, look up
// the floor item, remove it from the ground, add it to the player's inventory,
// and reply ZC_ITEM_PICKUP_ACK.
func (s *MapServer) handleItemPickup(c gnet.Conn, auth *mapAuth, frame []byte) {
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

// handleItemDrop handles CZ_ITEM_DROP (0x0363, 6B): resolve the inventory slot
// the client drops from, remove Amount units from the bag, spawn a floor item at
// the player's feet, and reply ZC_ITEM_THROW_ACK (SELF) + ZC_ITEM_FALL_ENTRY
// (the floor-item landing packet the rathenaThailand fork emits on a drop).
//
// The wire InventoryIndex is the client's 1-based slot. The inventory port keys
// removal by ItemID, not index, so the index resolves against the ordered list
// LoadByChar returns (the same order the LoadEndAck init burst will assign once
// populated, M5b). The GORM repo orders by id; a future inventory-list emitter
// owns the canonical index once M5b populates the init burst.
func (s *MapServer) handleItemDrop(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		s.log.Warn("map: CZ_ITEM_DROP from unauthed conn")
		return
	}
	req, err := ropacket.ParseCZDropItem(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_ITEM_DROP", "err", err)
		return
	}
	if req.Amount == 0 {
		s.log.Warn("map: CZ_ITEM_DROP zero amount", "index", req.InventoryIndex)
		return
	}
	items, err := s.inv.LoadByChar(context.Background(), auth.accountID, auth.charID)
	if err != nil {
		s.log.Error("map: drop load inventory", "err", err)
		return
	}
	idx := int(req.InventoryIndex) //nolint:gosec // G115: uint16→int is lossless; slot count is tiny.
	if idx < 1 || idx > len(items) {
		s.log.Warn("map: CZ_ITEM_DROP index out of range", "index", req.InventoryIndex, "slots", len(items))
		return
	}
	item := items[idx-1]
	if uint32(req.Amount) > item.Amount { //nolint:gosec // G115: uint16→uint32 is lossless.
		s.log.Warn("map: CZ_ITEM_DROP amount over stack", "index", req.InventoryIndex, "want", req.Amount, "have", item.Amount)
		return
	}
	if err := s.inv.Remove(context.Background(), item.ID, int(req.Amount)); err != nil {
		s.log.Warn("map: drop remove inventory", "err", err, "id", item.ID)
		return
	}
	gid := worlddomain.EntityID(auth.charID)
	entity, _ := s.world.Get(gid)
	fi := s.spawn.DropItem(item.NameID, uint32(req.Amount), entity.Map, entity.Pos, gid) //nolint:gosec // G115: uint16→uint32 is lossless.

	// Coalesce the SELF throw-ack (tells the dropping client the bag row left)
	// and the floor-item landing (0x0ADD, the fork's only drop-path packet) into
	// one AsyncWrite to avoid two syscalls.
	var burst []byte
	throwAck := ropacket.ItemThrowAckResponse{Index: req.InventoryIndex, Count: req.Amount}
	abuf := make([]byte, throwAck.Size())
	if err := throwAck.Encode(sliceWriter(abuf)); err != nil {
		s.log.Error("map: encode item-throw-ack", "err", err)
		return
	}
	burst = append(burst, abuf...)
	fallEntry := ropacket.ItemFallEntryResponse{
		ID:         fi.GroundID,
		NameID:     fi.NameID,
		Identified: 1,
		X:          uint16(fi.PosX),   //nolint:gosec // G115: map coords are non-negative int16.
		Y:          uint16(fi.PosY),   //nolint:gosec // G115: map coords are non-negative int16.
		Amount:     uint16(fi.Amount), //nolint:gosec // G115: amount bounded to small stack values.
	}
	fbuf := make([]byte, fallEntry.Size())
	if err := fallEntry.Encode(sliceWriter(fbuf)); err != nil {
		s.log.Error("map: encode item-fall-entry", "err", err)
		return
	}
	burst = append(burst, fbuf...)
	_ = c.AsyncWrite(burst, nil)
	// Other nearby players see the dropped item land (ZC_ITEM_FALL_ENTRY); the
	// throw-ack above is dropper-only, so just the fall-entry is fanned out. The
	// anchor is the dropper's cell, where the item spawns at the player's feet.
	s.broadcast(fbuf, entity.Map, entity.Pos, auth.charID)
}

// handleReqWearEquip handles CZ_REQ_WEAR_EQUIP_V5 (0x0998, 8B): the client
// requests wearing the item at inventory Index into Position (an EQP_* bitmask).
// On success it persists the equip via EquipService (which resolves slot
// conflicts) and replies ZC_REQ_WEAR_EQUIP_ACK_V5 with result=1. On a validation
// failure (sentinel error) it logs and keeps the connection alive — only the
// success path emits an ack, because the exact rAthena failure-encoding for the
// V5 ack (which field carries the success/fail byte varies by client era) is
// uncertain; emitting a wrong failure byte could wedge the client's equip slot.
// ItemSpriteNumber (the client view sprite) is 0 this milestone: item_db.View
// resolution is deferred and the field is cosmetic (the WeaponATK combat win is
// unaffected).
func (s *MapServer) handleReqWearEquip(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		s.log.Warn("map: CZ_REQ_WEAR_EQUIP from unauthed conn")
		return
	}
	req, err := ropacket.ParseCZReqWearEquip(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_REQ_WEAR_EQUIP", "err", err)
		return
	}
	if err := s.equip.Equip(context.Background(), auth.accountID, auth.charID, int(req.Index), req.Position); err != nil {
		s.log.Warn("map: wear equip", "gid", auth.charID, "index", req.Index, "err", err)
		return
	}
	resp := ropacket.ReqWearEquipAckResponse{
		Index:            req.Index,
		WearLocation:     req.Position,
		ItemSpriteNumber: 0, // view sprite resolution deferred (item_db.View)
		Result:           1, // 1 = success
	}
	out := make([]byte, resp.Size())
	if err := resp.Encode(sliceWriter(out)); err != nil {
		s.log.Error("map: encode wear-equip ack", "err", err)
		return
	}
	_ = c.AsyncWrite(out, nil)
}

// handleReqTakeoffEquip handles CZ_REQ_TAKEOFF_EQUIP (0x00ab, 4B): the client
// requests removing the item at inventory Index from its slot. It captures the
// worn slot, clears the equip bitmask via EquipService, and replies
// ZC_REQ_TAKEOFF_EQUIP_ACK with flag=0 (success on the wire — the byte is
// inverted for PACKETVER >= 20110824 so 0 = success). On failure it logs and
// keeps the connection alive.
func (s *MapServer) handleReqTakeoffEquip(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		s.log.Warn("map: CZ_REQ_TAKEOFF_EQUIP from unauthed conn")
		return
	}
	req, err := ropacket.ParseCZReqTakeoffEquip(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_REQ_TAKEOFF_EQUIP", "err", err)
		return
	}
	worn, ok := s.wornSlot(auth, int(req.Index))
	if !ok {
		s.log.Warn("map: takeoff index out of range", "gid", auth.charID, "index", req.Index)
		return
	}
	if err := s.equip.Unequip(context.Background(), auth.accountID, auth.charID, int(req.Index)); err != nil {
		s.log.Warn("map: takeoff equip", "gid", auth.charID, "index", req.Index, "err", err)
		return
	}
	resp := ropacket.ReqTakeoffEquipAckResponse{
		Index:        req.Index,
		WearLocation: worn,
		Flag:         0, // 0 = success on the wire (inverted) for PACKETVER >= 20110824
	}
	out := make([]byte, resp.Size())
	if err := resp.Encode(sliceWriter(out)); err != nil {
		s.log.Error("map: encode takeoff-equip ack", "err", err)
		return
	}
	_ = c.AsyncWrite(out, nil)
}

// wornSlot resolves the EQP_* bitmask currently worn by the item at the 1-based
// inventory index, so the takeoff ack can report the slot it freed. It returns
// ok=false when the index is out of range (the player cannot unequip a slot that
// has no item). The read is best-effort: between this load and Unequip's
// internal clear the slot could change for a racy double-unequip, but the only
// consequence is a stale WearLocation in one ack — cosmetic, not state-corrupting.
func (s *MapServer) wornSlot(auth *mapAuth, invIndex int) (uint32, bool) {
	items, err := s.inv.LoadByChar(context.Background(), auth.accountID, auth.charID)
	if err != nil {
		s.log.Error("map: load inventory for takeoff", "err", err)
		return 0, false
	}
	if invIndex < 1 || invIndex > len(items) {
		return 0, false
	}
	return items[invIndex-1].Equip, true
}

// handleActionRequest handles CZ_ACTION_REQUEST (0x0089, 7B): sit/stand/attack.
// Sit/stand just echo back; attack (action 0x07) resolves melee damage via
// CombatService, echoes the action, and — when the hit kills a mob — drives the
// death loop: drops + despawn (SpawnService.OnMobDeath) then a ZC_NOTIFY_VANISH
// + one ZC_ITEM_ENTRY per rolled drop.
func (s *MapServer) handleActionRequest(c gnet.Conn, auth *mapAuth, frame []byte) {
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
		s.handleMobDeath(c, auth.charID, req.TargetGID)
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

// handleMobDeath despawns a dead mob, rolls its drops, and notifies the killing
// client: ZC_NOTIFY_VANISH (mob leaves the map) then one ZC_ITEM_ENTRY per
// rolled drop. The same vanish+drop burst is broadcast to every OTHER player
// near the death cell so the shared world shows the mob dying and its loot
// landing. The death/drop state lives in SpawnService.OnMobDeath; this only
// does the wire side. Only mobs despawn+drop on death (a PC reaching 0 HP is a
// revive flow). The frames are coalesced into one AsyncWrite per recipient.
func (s *MapServer) handleMobDeath(c gnet.Conn, killerCharID uint32, mobGID uint32) {
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
	// Broadcast the mob vanish + loot to OTHER nearby players (not the killer,
	// who already received burst above). burst is immutable after this point, so
	// fanning the same buffer to multiple connections is safe.
	s.broadcast(burst, defender.Map, defender.Pos, killerCharID)
}

// handleUseSkill2 handles CZ_USE_SKILL2 (0x0438, 10B): cast a single-target
// skill onto an entity. It validates the cast through SkillService (skill known,
// level in range, target reachable, SP affordable), then on success emits
// ZC_NOTIFY_SKILL (0x01de) carrying the resolved damage. The damage is a
// melee-equivalent hit routed through the existing CombatService path — full
// skill-damage modeling (element/size/crit/per-skill multipliers) is kernel
// future work and deliberately not faked here. On failure it emits
// ZC_ACK_TOUSESKILL only for the verified SP-insufficient cause; other
// validation failures are logged and the connection is kept alive (their
// USESKILL_FAIL_* wire codes are not yet defined in the packet layer, a known
// gap — no wire value is invented). When the hit kills a mob, the same
// drop/despawn loop as CZ_ACTION_REQUEST runs.
func (s *MapServer) handleUseSkill2(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		s.log.Warn("map: CZ_USE_SKILL2 from unauthed conn")
		return
	}
	req, err := ropacket.ParseCZUseSkill(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_USE_SKILL2", "err", err)
		return
	}
	dmg, died, err := s.skills.UseSkillOnTarget(
		worlddomain.EntityID(auth.charID),
		int32(req.SkillID),
		req.SkillLv,
		worlddomain.EntityID(req.TargetID),
	)
	if err != nil {
		s.log.Warn("map: skill cast", "skill", req.SkillID, "level", req.SkillLv, "err", err)
		s.sendSkillFail(c, req.SkillID, err)
		return
	}
	resp := ropacket.NotifySkillResponse{
		SKID:     req.SkillID,
		AID:      auth.charID,
		TargetID: req.TargetID,
		Damage:   dmg,
		Level:    req.SkillLv,
		Count:    1,
		Action:   0, // DMG_NORMAL (clif.cpp damage_type selector)
	}
	out := make([]byte, resp.Size())
	if err := resp.Encode(sliceWriter(out)); err != nil {
		s.log.Error("map: encode notify-skill", "err", err)
		return
	}
	_ = c.AsyncWrite(out, nil)
	s.log.Debug("map: skill cast", "skill", req.SkillID, "level", req.SkillLv, "target", req.TargetID, "dmg", dmg)
	if died {
		s.handleMobDeath(c, auth.charID, req.TargetID)
	}
}

// handleUseSkillToPos handles CZ_USE_SKILL_TOPOS (0x0AF4, 11B): a client casts a
// ground-target skill onto a tile. It always emits ZC_NOTIFY_GROUNDSKILL (0x0117)
// to place the skill's visual effect on the cast tile, so the client renders the
// cast even when no mob is in range. As an honest single-target approximation of
// the ground cast it then resolves the nearest mob to the cast tile and routes
// one skill hit through SkillService — the same combat path as CZ_USE_SKILL2 —
// so the cast deals damage when a mob is adjacent. Full ground-AoE damage (every
// mob within the skill's tile radius, element/size-modified skill-damage) is
// kernel future work and deliberately not faked here: only the single nearest
// mob is affected. Validation failures are logged, surface ZC_ACK_TOUSESKILL only
// for the verified SP-insufficient cause, and never close the connection.
func (s *MapServer) handleUseSkillToPos(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		s.log.Warn("map: CZ_USE_SKILL_TOPOS from unauthed conn")
		return
	}
	req, err := ropacket.ParseCZUseSkillToPos(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_USE_SKILL_TOPOS", "err", err)
		return
	}
	// Place the cast visual on the tile. AID is the caster's GID, matching the
	// rAthena layout documented on GroundSkillPoseEffect.
	pose := ropacket.GroundSkillPoseEffect{
		SKID:      req.SkillID,
		AID:       auth.charID,
		Level:     req.SkillLv,
		XPos:      int16(req.X), //nolint:gosec // G115: map coords are non-negative int16.
		YPos:      int16(req.Y), //nolint:gosec // G115: map coords are non-negative int16.
		StartTime: 0,            // server tick at cast resolution; 0 acceptable for the local clock.
	}
	out := make([]byte, pose.Size())
	if err := pose.Encode(sliceWriter(out)); err != nil {
		s.log.Error("map: encode notify-groundskill", "err", err)
		return
	}
	_ = c.AsyncWrite(out, nil)
	s.log.Debug("map: ground-skill cast", "skill", req.SkillID, "level", req.SkillLv, "x", req.X, "y", req.Y)

	// Resolve the nearest mob to the cast tile and route one hit through the
	// existing SkillService path. A true AoE (every mob in the skill radius) is
	// kernel future work; this single-target approximation keeps the cast honest.
	caster, err := s.world.Get(worlddomain.EntityID(auth.charID))
	if err != nil {
		s.log.Warn("map: ground-skill caster lookup", "err", err)
		return
	}
	mobID := nearestMobID(s.world, caster.Map, int(req.X), int(req.Y))
	if mobID == 0 {
		return
	}
	dmg, died, err := s.skills.UseSkillOnTarget(
		worlddomain.EntityID(auth.charID),
		int32(req.SkillID),
		req.SkillLv,
		mobID,
	)
	if err != nil {
		s.log.Warn("map: ground-skill cast", "skill", req.SkillID, "level", req.SkillLv, "err", err)
		s.sendSkillFail(c, req.SkillID, err)
		return
	}
	// Surface the resolved hit to the client (ZC_NOTIFY_SKILL), mirroring the
	// CZ_USE_SKILL2 path, so the caster sees the damage applied to the nearest mob
	// — not just the ground visual. Full AoE (per-mob notifications) is future work.
	hit := ropacket.NotifySkillResponse{
		SKID:     req.SkillID,
		AID:      auth.charID,
		TargetID: uint32(mobID), //nolint:gosec // G115: EntityID is uint32 by definition.
		Damage:   dmg,
		Level:    req.SkillLv,
		Count:    1,
		Action:   0, // DMG_NORMAL
	}
	out2 := make([]byte, hit.Size())
	if err := hit.Encode(sliceWriter(out2)); err != nil {
		s.log.Error("map: encode ground-skill notify-skill", "err", err)
		return
	}
	_ = c.AsyncWrite(out2, nil)
	s.log.Debug("map: ground-skill hit", "skill", req.SkillID, "target", mobID, "dmg", dmg)
	if died {
		s.handleMobDeath(c, auth.charID, uint32(mobID)) //nolint:gosec // G115: EntityID is uint32 by definition.
	}
}

// nearestMobID returns the EntityID of the mob nearest (Chebyshev cell distance)
// to tile (x, y) on mapName, or 0 when no mob is visible from that tile. It is
// the honest single-target approximation of a ground-AoE skill: full per-radius
// multi-target resolution is kernel future work.
func nearestMobID(world *worldapp.WorldService, mapName string, x, y int) worlddomain.EntityID {
	var nearest worlddomain.EntityID
	bestDist := -1
	for _, id := range world.QueryVisible(mapName, x, y) {
		e, err := world.Get(id)
		if err != nil || e.Type != worlddomain.EntityTypeMob {
			continue
		}
		dx := abs(x - int(e.Pos.X))
		dy := abs(y - int(e.Pos.Y))
		d := dx
		if dy > dx {
			d = dy
		}
		if bestDist < 0 || d < bestDist {
			bestDist = d
			nearest = id
		}
	}
	return nearest
}

// abs returns the absolute value of x.
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// sendSkillFail emits ZC_ACK_TOUSESKILL (0x0110) to reject a cast. Only the
// SP-insufficient cause (12) is verified in the packet layer; other failure
// reasons have no defined wire code yet, so they are dropped (logged at the
// call site) rather than emitting an unverified cause.
func (s *MapServer) sendSkillFail(c gnet.Conn, skillID uint16, castErr error) {
	if !errors.Is(castErr, worldapp.ErrInsufficientSP) {
		return
	}
	resp := ropacket.AckUseSkillResponse{SkillID: skillID, Cause: ropacket.UseSkillFailSPInsufficient}
	out := make([]byte, resp.Size())
	if err := resp.Encode(sliceWriter(out)); err != nil {
		s.log.Error("map: encode ack-touseskill", "err", err)
		return
	}
	_ = c.AsyncWrite(out, nil)
}

// --- player-to-player trade handlers ---
//
// Trade runs a request→ack→(stage)→ok state machine across TWO connections: the
// sender's (c) and the partner's (resolved through the conn-registry shim by
// charID). Every handler resolves the partner BEFORE calling a service method
// that may tear the session down (Ack cancel / OK conclude / Cancel), since
// Partner() needs the live session to map charID→partner.

// handleTradeRequest opens a trade request (CZ_TRADE_REQUEST 0x00e4). The
// requester targets a partner GID; on success the server opens the TARGET's trade
// dialog (ZC_REQ_EXCHANGE_ITEM carries the requester's name/AID/level — the real
// wire format sends this to the TARGET only, never the requester). On failure the
// requester gets a ZC_ACK_EXCHANGE_ITEM reject reason.
func (s *MapServer) handleTradeRequest(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		s.log.Warn("map: CZ_TRADE_REQUEST from unauthed conn")
		return
	}
	if s.trade == nil {
		s.log.Debug("map: trade not wired, ignoring CZ_TRADE_REQUEST")
		return
	}
	req, err := ropacket.ParseCZTradeRequest(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_TRADE_REQUEST", "err", err)
		return
	}
	targetGID := req.TargetGID
	if err := s.trade.Request(context.Background(), auth.charID, targetGID); err != nil {
		s.writeTradeAck(c, tradeAckResult(err), 0, 0)
		s.log.Debug("map: trade request rejected", "req", auth.charID, "tgt", targetGID, "err", err)
		return
	}
	// Request succeeded: resolve the requester's name/AID/level to populate the
	// target's dialog, then deliver it to the target's connection.
	reqEnt, gerr := s.world.Get(worlddomain.EntityID(auth.charID))
	if gerr != nil {
		s.trade.Cancel(context.Background(), auth.charID)
		s.log.Error("map: resolve requester entity for trade", "gid", auth.charID, "err", gerr)
		return
	}
	targetConn, ok := s.connFor(targetGID)
	if !ok {
		// Target is an online PC (Request verified it) but not reachable through
		// the conn-registry shim — tear the session down and reject the requester.
		s.trade.Cancel(context.Background(), auth.charID)
		s.writeTradeAck(c, ropacket.TradeAckCharNotExist, 0, 0)
		return
	}
	s.writeTradeRequest(targetConn, reqEnt.Name, auth.accountID, uint16(reqEnt.Level)) //nolint:gosec // G115: base level fits uint16
}

// handleTradeAck applies the target's accept/cancel of a pending request
// (CZ_TRADE_ACK 0x00e6). Type 3 = accept, anything else = cancel. On accept both
// sides get ZC_ACK_EXCHANGE_ITEM(Accept) carrying the OTHER party's AID/level; on
// cancel both get ZC_CANCEL_EXCHANGE_ITEM. The ack-sender is the target (the one
// whose dialog was opened); its partner is the requester.
func (s *MapServer) handleTradeAck(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		s.log.Warn("map: CZ_TRADE_ACK from unauthed conn")
		return
	}
	if s.trade == nil {
		s.log.Debug("map: trade not wired, ignoring CZ_TRADE_ACK")
		return
	}
	req, err := ropacket.ParseCZTradeAck(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_TRADE_ACK", "err", err)
		return
	}
	accept := req.Type == ropacket.CZTradeAckAccept
	// Resolve the partner BEFORE Ack: an accept leaves both sessions active, but a
	// cancel tears both down.
	partnerID, ok := s.trade.Partner(context.Background(), auth.charID)
	if !ok {
		s.log.Debug("map: CZ_TRADE_ACK with no active trade", "gid", auth.charID)
		return
	}
	if err := s.trade.Ack(context.Background(), auth.charID, accept); err != nil {
		s.log.Debug("map: trade ack failed", "gid", auth.charID, "err", err)
		return
	}
	if !accept {
		s.writeTradeCancel(c)
		if pc, ok := s.connFor(partnerID); ok {
			s.writeTradeCancel(pc)
		}
		return
	}
	// Accept: cross-echo each side the OTHER party's AID/level.
	selfEnt, _ := s.world.Get(worlddomain.EntityID(auth.charID))
	partnerEnt, _ := s.world.Get(worlddomain.EntityID(partnerID))
	s.writeTradeAck(c, ropacket.TradeAckAccept, partnerEnt.Account, uint16(partnerEnt.Level)) //nolint:gosec // G115: base level fits uint16
	if pc, ok := s.connFor(partnerID); ok {
		s.writeTradeAck(pc, ropacket.TradeAckAccept, selfEnt.Account, uint16(selfEnt.Level)) //nolint:gosec // G115: base level fits uint16
	}
}

// handleAddExchangeItem stages an item (index>0) or zeny (index==0) on the
// sender's side (CZ_ADD_EXCHANGE_ITEM 0x00e8). On success the SENDER gets
// ZC_ACK_ADD_EXCHANGE_ITEM(Success) and the PARTNER gets ZC_ADD_EXCHANGE_ITEM (the
// staged view); on failure only the sender is told.
func (s *MapServer) handleAddExchangeItem(c gnet.Conn, auth *mapAuth, frame []byte) {
	if auth == nil {
		s.log.Warn("map: CZ_ADD_EXCHANGE_ITEM from unauthed conn")
		return
	}
	if s.trade == nil {
		s.log.Debug("map: trade not wired, ignoring CZ_ADD_EXCHANGE_ITEM")
		return
	}
	req, err := ropacket.ParseCZAddExchangeItem(frame)
	if err != nil {
		s.log.Warn("map: parse CZ_ADD_EXCHANGE_ITEM", "err", err)
		return
	}
	partnerID, ok := s.trade.Partner(context.Background(), auth.charID)
	if !ok {
		s.writeAckAddItem(c, req.Index, ropacket.TradeItemAddCanceled)
		s.log.Debug("map: CZ_ADD_EXCHANGE_ITEM with no active trade", "gid", auth.charID)
		return
	}
	res, err := s.trade.AddItem(context.Background(), auth.charID, int(req.Index), int(req.Amount)) //nolint:gosec // G115: wire index/amount are small positives
	if err != nil {
		s.writeAckAddItem(c, req.Index, tradeItemAddResult(err))
		s.log.Debug("map: trade add-item rejected", "gid", auth.charID, "err", err)
		return
	}
	s.writeAckAddItem(c, req.Index, ropacket.TradeItemAddSuccess)
	if pc, ok := s.connFor(partnerID); ok {
		s.writeZCAddItem(pc, res)
	}
}

// handleTradeOK locks the sender's side (CZ_TRADE_OK 0x00eb). While the partner has
// not yet locked, both sides get ZC_CONCLUDE_EXCHANGE_ITEM (Who=0 to the locker,
// Who=1 to the partner). When both lock, the service runs the atomic conclude
// swap; the lock notifications are still emitted. A conclude failure (the known
// verify-then-swap TOCTOU window) rolls back, cancels both sessions, and tells both
// sides the trade was cancelled.
func (s *MapServer) handleTradeOK(c gnet.Conn, auth *mapAuth, _ []byte) {
	if auth == nil {
		s.log.Warn("map: CZ_TRADE_OK from unauthed conn")
		return
	}
	if s.trade == nil {
		s.log.Debug("map: trade not wired, ignoring CZ_TRADE_OK")
		return
	}
	partnerID, ok := s.trade.Partner(context.Background(), auth.charID)
	if !ok {
		s.log.Debug("map: CZ_TRADE_OK with no active trade", "gid", auth.charID)
		return
	}
	concluded, err := s.trade.OK(context.Background(), auth.charID)
	if err != nil {
		s.writeTradeCancel(c)
		if pc, ok := s.connFor(partnerID); ok {
			s.writeTradeCancel(pc)
		}
		s.log.Warn("map: trade conclude failed", "gid", auth.charID, "err", err)
		return
	}
	s.writeConclude(c, 0) // you pressed Ok
	if pc, ok := s.connFor(partnerID); ok {
		s.writeConclude(pc, 1) // your partner pressed Ok
	}
	if concluded {
		s.log.Info("map: trade concluded", "gid", auth.charID, "partner", partnerID)
	}
}

// handleTradeCancel tears down the sender's trade (CZ_TRADE_CANCEL 0x00ed) and
// tells both sides via ZC_CANCEL_EXCHANGE_ITEM. A no-op when the sender is not
// trading.
func (s *MapServer) handleTradeCancel(c gnet.Conn, auth *mapAuth, _ []byte) {
	if auth == nil {
		s.log.Warn("map: CZ_TRADE_CANCEL from unauthed conn")
		return
	}
	if s.trade == nil {
		return
	}
	partnerID, ok := s.trade.Partner(context.Background(), auth.charID)
	s.trade.Cancel(context.Background(), auth.charID)
	s.writeTradeCancel(c)
	if ok {
		if pc, ok := s.connFor(partnerID); ok {
			s.writeTradeCancel(pc)
		}
	}
}

// --- trade write helpers ---

func (s *MapServer) writeTradeRequest(c gnet.Conn, requesterName string, requesterAID uint32, requesterLv uint16) {
	resp := ropacket.TradeRequestResponse{RequesterName: requesterName, TargetID: requesterAID, TargetLv: requesterLv}
	out := make([]byte, resp.Size())
	if err := resp.Encode(sliceWriter(out)); err != nil {
		s.log.Error("map: encode ZC_REQ_EXCHANGE_ITEM", "err", err)
		return
	}
	_ = c.AsyncWrite(out, nil)
}

func (s *MapServer) writeTradeAck(c gnet.Conn, result uint8, targetAID uint32, targetLv uint16) {
	resp := ropacket.TradeAckResponse{Result: result, TargetID: targetAID, TargetLv: targetLv}
	out := make([]byte, resp.Size())
	if err := resp.Encode(sliceWriter(out)); err != nil {
		s.log.Error("map: encode ZC_ACK_EXCHANGE_ITEM", "err", err)
		return
	}
	_ = c.AsyncWrite(out, nil)
}

func (s *MapServer) writeTradeCancel(c gnet.Conn) {
	resp := ropacket.CancelExchangeResponse{}
	out := make([]byte, resp.Size())
	if err := resp.Encode(sliceWriter(out)); err != nil {
		s.log.Error("map: encode ZC_CANCEL_EXCHANGE_ITEM", "err", err)
		return
	}
	_ = c.AsyncWrite(out, nil)
}

func (s *MapServer) writeAckAddItem(c gnet.Conn, index uint16, result uint8) {
	resp := ropacket.AckAddExchangeItem{Index: index, Result: result}
	out := make([]byte, resp.Size())
	if err := resp.Encode(sliceWriter(out)); err != nil {
		s.log.Error("map: encode ZC_ACK_ADD_EXCHANGE_ITEM", "err", err)
		return
	}
	_ = c.AsyncWrite(out, nil)
}

// writeZCAddItem emits the partner's staged view (ZC_ADD_EXCHANGE_ITEM). Item
// rendering fields that need item_db (ItemType, Location, Look, item options) are
// left zero — a best-effort view; the trade state machine and atomic swap do not
// depend on it.
func (s *MapServer) writeZCAddItem(c gnet.Conn, res worldapp.AddItemResult) {
	resp := ropacket.ZCAddExchangeItem{}
	if res.Index == 0 {
		resp.Amount = res.Zeny
	} else {
		it := res.Item
		resp.ItemID = it.NameID
		resp.Amount = int32(it.Amount) //nolint:gosec // G115: stack count fits int32
		resp.Damaged = it.Attribute
		resp.Refine = it.Refine
		resp.Cards = [4]uint32{it.Card0, it.Card1, it.Card2, it.Card3}
		if it.Identify > 0 {
			resp.Identified = 1
		}
	}
	out := make([]byte, resp.Size())
	if err := resp.Encode(sliceWriter(out)); err != nil {
		s.log.Error("map: encode ZC_ADD_EXCHANGE_ITEM", "err", err)
		return
	}
	_ = c.AsyncWrite(out, nil)
}

func (s *MapServer) writeConclude(c gnet.Conn, who uint8) {
	resp := ropacket.ConcludeExchangeItem{Who: who}
	out := make([]byte, resp.Size())
	if err := resp.Encode(sliceWriter(out)); err != nil {
		s.log.Error("map: encode ZC_CONCLUDE_EXCHANGE_ITEM", "err", err)
		return
	}
	_ = c.AsyncWrite(out, nil)
}

// tradeAckResult maps a TradeService sentinel to the ZC_ACK_EXCHANGE_ITEM result
// byte for a request/ack failure (sentinel errors only; no error-string branch).
// The success path emits Accept/Cancel directly, so this only chooses a reject
// reason for the (logged) failure cases.
func tradeAckResult(err error) uint8 {
	switch {
	case errors.Is(err, worldapp.ErrTradeTargetOffline):
		return ropacket.TradeAckCharNotExist
	case errors.Is(err, worldapp.ErrTradeDifferentMap):
		return ropacket.TradeAckTooFar
	case errors.Is(err, worldapp.ErrTradeAlreadyTrading):
		return ropacket.TradeAckBusy
	default:
		return ropacket.TradeAckFailed
	}
}

// tradeItemAddResult maps a TradeService sentinel to the ZC_ACK_ADD_EXCHANGE_ITEM
// result byte (sentinel errors only). Best-effort: the codes do not map 1:1 to the
// failure causes, and only the success path is asserted by tests.
func tradeItemAddResult(err error) uint8 {
	switch {
	case errors.Is(err, worldapp.ErrTradeNotActive), errors.Is(err, worldapp.ErrTradeLocked):
		return ropacket.TradeItemAddCanceled
	case errors.Is(err, worldapp.ErrTradeItemInsufficient):
		return ropacket.TradeItemAddStackExceed
	case errors.Is(err, worldapp.ErrTradeItemOutOfRange), errors.Is(err, worldapp.ErrTradeItemEquipped):
		return ropacket.TradeItemAddInvFull
	default:
		return ropacket.TradeItemAddStackExceed
	}
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

// gnetWriter adapts a gnet.Conn to content/domain.PacketWriter. It is the
// gateway's own bridge from the gnet wire to the content domain's writer port,
// so the gateway app layer depends only on content/domain, not content/infra.
type gnetWriter struct{ c gnet.Conn }

// WritePacket sends raw bytes to the connection (concurrency-safe via AsyncWrite).
func (w gnetWriter) WritePacket(data []byte) {
	_ = w.c.AsyncWrite(data, nil)
}
