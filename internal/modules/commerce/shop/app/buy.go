// Package app: the shop buy use case completes the commerce loop the content
// module opens at CZ_CONTACTNPC. The content module's ContactNPCHandler
// short-circuits shop NPCs to ZC_SELECT_DEALTYPE (a Buy/Sell/Cancel selector);
// this use case consumes the Buy selection (CZ_ACK_SELECT_DEALTYPE type=0),
// ships the catalog as ZC_PC_PURCHASE_ITEMLIST, and resolves the subsequent
// CZ_PC_PURCHASE_ITEMLIST by validating every entry against the catalog,
// debiting zeny through the character repository, adding items through the
// inventory repository, acking the result, and broadcasting the new zeny
// balance via ZC_LONGPAR_CHANGE. Sell/Cancel are no-ops here — the sell loop
// is a future milestone, cancel is the client closing the deal window.
package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"

	chardomain "github.com/bouroo/goAthena/internal/modules/character/domain"
	shopdomain "github.com/bouroo/goAthena/internal/modules/commerce/shop/domain"
	gwdomain "github.com/bouroo/goAthena/internal/modules/gateway/domain"
	invdomain "github.com/bouroo/goAthena/internal/modules/inventory/domain"
	worlddomain "github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/itemdb"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// buySession caps the active shop dialog one client may hold at a time. The
// NPC EntityID is the only state; the catalog reads the rest on demand.
type buySession struct {
	npcID uint32
}

// BuyService resolves the shop buy flow: the deal-type selector (Buy branch),
// the catalog broadcast (ZC_PC_PURCHASE_ITEMLIST), and the purchase request
// (CZ_PC_PURCHASE_ITEMLIST → zeny debit + inventory fill + ZC_PC_PURCHASE_RESULT
// + ZC_LONGPAR_CHANGE). sessions tracks the active shop dialog per account;
// the mutex guards the map so a player cannot race two CZ_ACK_SELECT_DEALTYPE
// frames and double-spend. catalog, items, players, and itemDB are the
// upstream ports the use case composes — every collaborator is a module's
// domain port, never a cross-module impl import (the architecture guard).
type BuyService struct {
	catalog  shopdomain.ShopCatalog
	chars    chardomain.CharacterRepository
	items    invdomain.InventoryRepository
	players  *worlddomain.PlayerRegistry
	itemDB   *itemdb.Registry
	mu       sync.Mutex
	sessions map[uint32]buySession
}

// NewBuyService binds the five collaborators the buy use case depends on. A
// nil itemDB is tolerated: every catalog item then uses the wire defaults
// (IT_ETC, ViewSprite=0, Location=0) — the packet still emits, the buy list
// still renders on the client, the player just cannot preview sprites.
func NewBuyService(
	catalog shopdomain.ShopCatalog,
	chars chardomain.CharacterRepository,
	items invdomain.InventoryRepository,
	players *worlddomain.PlayerRegistry,
	itemDB *itemdb.Registry,
) *BuyService {
	return &BuyService{
		catalog:  catalog,
		chars:    chars,
		items:    items,
		players:  players,
		itemDB:   itemDB,
		sessions: make(map[uint32]buySession),
	}
}

// HandleAckSelectDealtype serves CZ_ACK_SELECT_DEALTYPE (0x00c5). The Buy
// selection (type=0) opens the catalog broadcast; Sell/Cancel are no-ops,
// falling through to return nil so the connection stays open. A missing
// session is the same outcome as Cancel — the request is rejected by the
// dispatcher logging, not by a fail packet. An unauthenticated connection
// (CZ_ENTER not completed) is rejected with a non-nil error so the
// dispatcher can log it; the conn layer accepts the error and keeps the
// socket open.
func (s *BuyService) HandleAckSelectDealtype(ctx context.Context, conn gwdomain.Conn, frame gwdomain.Frame) error {
	req, err := packet.ParseCZAckSelectDealType(frame.Raw)
	if err != nil {
		return fmt.Errorf("parse CZ_ACK_SELECT_DEALTYPE: %w", err)
	}
	if req.Type != 0 {
		// Sell (1) and Cancel (2) are out of scope for this use case.
		return nil
	}
	auth := conn.Auth()
	if auth.AccountID == 0 {
		return errors.New("shop buy: connection has no verified account (CZ_ENTER not completed)")
	}
	shop, ok := s.catalog.Get(req.NpcID)
	if !ok {
		// The deal-type selector only renders for shop NPCs, so an unknown
		// NPC at this point is a stale or forged request — log + drop.
		return fmt.Errorf("shop buy: unknown NPC entity id %d for account %d", req.NpcID, auth.AccountID)
	}
	s.mu.Lock()
	s.sessions[auth.AccountID] = buySession{npcID: req.NpcID}
	s.mu.Unlock()

	resp := packet.PurchaseItemListResponse{Items: s.buildShopItems(shop)}
	var buf bytes.Buffer
	if err := resp.Encode(&buf); err != nil {
		return fmt.Errorf("shop buy: encode ZC_PC_PURCHASE_ITEMLIST for account %d npc %d: %w", auth.AccountID, req.NpcID, err)
	}
	if err := conn.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("shop buy: send ZC_PC_PURCHASE_ITEMLIST for account %d npc %d: %w", auth.AccountID, req.NpcID, err)
	}
	return nil
}

// buildShopItems assembles the wire catalog from the shop's ShopItem slice,
// resolving the item_db fields (IT_* type, View, EQP_* Location) the client
// needs to render and equip-preview each entry. Each entry's Price is the
// catalog's authoritative buy price; DiscountPrice equals Price because the
// shipped shop YAML has no discount column.
func (s *BuyService) buildShopItems(shop shopdomain.Shop) []packet.ShopBuyItem {
	out := make([]packet.ShopBuyItem, 0, len(shop.Items))
	for _, it := range shop.Items {
		itemType, viewSprite, location := s.resolveItemWire(it.NameID)
		out = append(out, packet.ShopBuyItem{
			ItemID:        it.NameID,
			Price:         it.Price,
			DiscountPrice: it.Price,
			ItemType:      itemType,
			ViewSprite:    viewSprite,
			Location:      location,
		})
	}
	return out
}

// resolveItemWire returns the (ItemType, ViewSprite, Location) the
// ZC_PC_PURCHASE_ITEMLIST packet carries for nameID. A missing itemDB or a
// missing entry yields the safe defaults (IT_ETC, no sprite, no equip) so
// the catalog still broadcasts.
func (s *BuyService) resolveItemWire(nameID uint32) (itemType uint8, viewSprite uint16, location uint32) {
	if s.itemDB == nil {
		return mapItemType(""), 0, 0
	}
	entry := s.itemDB.Get(int32(nameID)) //nolint:gosec // G115: item_db nameids fit int32
	if entry == nil {
		return mapItemType(""), 0, 0
	}
	if entry.View < 0 {
		viewSprite = 0
	} else {
		viewSprite = uint16(entry.View) //nolint:gosec // G115: View is a non-negative item index
	}
	return mapItemType(entry.Type), viewSprite, entry.EquipLocations
}

// mapItemType maps an item_db Type string to the IT_* byte the wire carries.
// The shipped values are the canonical rAthena names; default→2 (IT_ETC)
// matches the load-time default and keeps an unknown item renderable.
func mapItemType(typeStr string) uint8 {
	switch typeStr {
	case "Healing":
		return 0
	case "Etc":
		return 2
	case "Weapon":
		return 3
	case "Armor":
		return 4
	case "Card":
		return 5
	case "Ammunition", "Ammo":
		return 6
	default:
		return 2
	}
}

// HandlePurchaseItemList serves CZ_PC_PURCHASE_ITEMLIST (0x00c8). The flow:
// resolve the active shop session, validate every entry against the catalog,
// compute total cost, debit zeny, add items, ack the result, broadcast the
// new zeny balance. Any rejected request (no session, no player, missing
// char, item not in shop, insufficient zeny) yields ZC_PC_PURCHASE_RESULT
// result=1 and clears the session — the client must re-pick the deal type.
// Success sends result=0, performs the mutations, and keeps the session
// open so the player can buy more without re-opening the shop.
func (s *BuyService) HandlePurchaseItemList(ctx context.Context, conn gwdomain.Conn, frame gwdomain.Frame) error {
	req, err := packet.ParseCZPCPurchaseItemList(frame.Raw)
	if err != nil {
		return fmt.Errorf("parse CZ_PC_PURCHASE_ITEMLIST: %w", err)
	}
	auth := conn.Auth()
	if auth.AccountID == 0 {
		return errors.New("shop buy: connection has no verified account (CZ_ENTER not completed)")
	}

	s.mu.Lock()
	sess, ok := s.sessions[auth.AccountID]
	s.mu.Unlock()
	if !ok {
		return s.sendResult(conn, 1)
	}
	shop, ok := s.catalog.Get(sess.npcID)
	if !ok {
		// The shop vanished under us (catalog reload) — drop the session
		// so the client can re-pick the deal type.
		s.clearSession(auth.AccountID)
		return s.sendResult(conn, 1)
	}

	player, ok := s.players.ByAccount(auth.AccountID)
	if !ok {
		return s.sendResult(conn, 1)
	}
	charID := player.CharID

	total, ok := computeTotal(shop.Items, req.Entries)
	if !ok {
		return s.sendResult(conn, 1)
	}

	char, err := s.chars.GetByID(ctx, auth.AccountID, charID)
	if err != nil {
		return fmt.Errorf("shop buy: load character %d for account %d: %w", charID, auth.AccountID, err)
	}
	if char.Zeny < uint32(total) { //nolint:gosec // G115: total bounded by catalog price * amount, both u32
		return s.sendResult(conn, 1)
	}

	for _, e := range req.Entries {
		stackable := s.itemDB != nil && s.itemDB.IsStackable(e.ItemID)
		if _, err := s.items.AddItem(ctx, auth.AccountID, charID, invdomain.NewItem{
			NameID:    e.ItemID,
			Amount:    uint32(e.Amount), //nolint:gosec // G115: wire amount is uint16, well under uint32
			Stackable: stackable,
		}); err != nil {
			if errors.Is(err, invdomain.ErrInventoryFull) {
				return s.sendResult(conn, 1)
			}
			return fmt.Errorf("shop buy: add item %d x%d for account %d char %d: %w", e.ItemID, e.Amount, auth.AccountID, charID, err)
		}
	}

	newZeny := char.Zeny - uint32(total) //nolint:gosec // G115: totalCost was checked against char.Zeny
	char.Zeny = newZeny
	if err := s.chars.SaveProgression(ctx, auth.AccountID, charID, chardomain.ProgressionOf(char)); err != nil {
		return fmt.Errorf("shop buy: save zeny for account %d char %d: %w", auth.AccountID, charID, err)
	}

	if err := s.sendResult(conn, 0); err != nil {
		return err
	}
	return s.sendZenyUpdate(conn, newZeny)
}

// computeTotal validates every purchase entry against the shop catalog and
// returns the total cost. ok=false means at least one entry is invalid.
func computeTotal(shopItems []shopdomain.ShopItem, entries []packet.CZPCPurchaseItemListEntry) (total uint64, ok bool) {
	priceByNameID := make(map[uint32]uint32, len(shopItems))
	for _, it := range shopItems {
		priceByNameID[it.NameID] = it.Price
	}
	for _, e := range entries {
		price, exists := priceByNameID[e.ItemID]
		if !exists {
			return 0, false
		}
		total += uint64(price) * uint64(e.Amount)
	}
	if total == 0 && len(entries) > 0 {
		return 0, false
	}
	return total, true
}

// sendResult writes ZC_PC_PURCHASE_RESULT (0x00ca, 3 bytes) to conn. Used
// for both the success and the failure branch — the only field that flips
// is the result byte.
func (s *BuyService) sendResult(conn gwdomain.Conn, result uint8) error {
	var buf bytes.Buffer
	if err := (packet.PurchaseResultResponse{Result: result}).Encode(&buf); err != nil {
		return fmt.Errorf("shop buy: encode ZC_PC_PURCHASE_RESULT result=%d: %w", result, err)
	}
	if err := conn.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("shop buy: send ZC_PC_PURCHASE_RESULT result=%d: %w", result, err)
	}
	return nil
}

// sendZenyUpdate broadcasts the new zeny balance via ZC_LONGPAR_CHANGE so the
// client's zeny display updates immediately after a purchase.
func (s *BuyService) sendZenyUpdate(conn gwdomain.Conn, newZeny uint32) error {
	var buf bytes.Buffer
	zeny := packet.LongParChangeResponse{
		VarID:  packet.SPZeny,
		Amount: int32(newZeny), //nolint:gosec // G115: uint32 zeny fits the int32 wire slot
	}
	if err := zeny.Encode(&buf); err != nil {
		return fmt.Errorf("shop buy: encode ZC_LONGPAR_CHANGE: %w", err)
	}
	if err := conn.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("shop buy: send ZC_LONGPAR_CHANGE: %w", err)
	}
	return nil
}

// clearSession drops the active shop dialog for accountID under the lock.
func (s *BuyService) clearSession(accountID uint32) {
	s.mu.Lock()
	delete(s.sessions, accountID)
	s.mu.Unlock()
}
