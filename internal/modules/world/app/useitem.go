package app

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"

	gwdomain "github.com/bouroo/goAthena/internal/modules/gateway/domain"
	invdomain "github.com/bouroo/goAthena/internal/modules/inventory/domain"
	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/itemdb"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// UseItemService resolves CZ_USE_ITEM2 requests for Healing inventory items.
// It validates the live player and inventory row before atomically consuming one
// unit, applying the item's itemheal range, and notifying the client.
type UseItemService struct {
	items   invdomain.InventoryRepository
	itemDB  *itemdb.Registry
	players *domain.PlayerRegistry
}

// NewUseItemService binds the inventory, item database, and live player registry.
func NewUseItemService(items invdomain.InventoryRepository, itemDB *itemdb.Registry, players *domain.PlayerRegistry) *UseItemService {
	return &UseItemService{items: items, itemDB: itemDB, players: players}
}

// UseItem handles one inventory-index use for the verified account. Expected
// client rejections receive a failure ack and leave the map session alive.
func (s *UseItemService) UseItem(ctx context.Context, accountID uint32, index uint16) error {
	player, ok := s.players.ByAccount(accountID)
	if !ok {
		return nil
	}
	if s.items == nil || s.itemDB == nil {
		return s.sendFailure(player, index)
	}

	rows, err := s.items.LoadByChar(ctx, accountID, player.CharID)
	if err != nil {
		return fmt.Errorf("use item: load bag for account %d char %d: %w", accountID, player.CharID, err)
	}
	row, ok := inventoryRowAt(rows, index)
	if !ok {
		return s.sendFailure(player, index)
	}
	entry := s.itemDB.Get(int32(row.NameID)) //nolint:gosec // G115: valid item_db nameids fit int32
	if entry == nil || !strings.EqualFold(entry.Type, "Healing") {
		return s.sendFailure(player, index)
	}
	hpMin, hpMax, spMin, spMax, ok := entry.Heal()
	if !ok {
		return s.sendFailure(player, index)
	}

	consumed, deleted, err := s.items.ConsumeItem(ctx, accountID, player.CharID, index, 1)
	if err != nil {
		if errors.Is(err, invdomain.ErrItemNotFound) {
			return s.sendFailure(player, index)
		}
		return fmt.Errorf("use item: consume account %d char %d index %d: %w", accountID, player.CharID, index, err)
	}

	hp := rollHeal(hpMin, hpMax)
	sp := rollHeal(spMin, spMax)
	player.Heal(hp, sp)
	currentHP, currentSP := player.Vitals()
	remaining := uint16(0)
	if !deleted {
		remaining = uint16(consumed.Amount) //nolint:gosec // G115: post-consume stack amount fits the wire count
	}
	ack := packet.UseItemAck2Response{
		Index:  consumed.Index,
		ItemID: uint16(consumed.NameID), //nolint:gosec // G115: item_db nameids fit the packet's item id field
		AID:    accountID,
		Amount: remaining,
		Result: 1,
	}
	if err := ack.Encode(connWriter{player.Conn}); err != nil {
		return fmt.Errorf("use item: encode success ack account %d index %d: %w", accountID, index, err)
	}
	for _, change := range []packet.ParChangeResponse{
		{VarID: packet.SPHP, Count: int32(currentHP)}, //nolint:gosec // G115: RO HP fits the int32 wire field
		{VarID: packet.SPSP, Count: int32(currentSP)}, //nolint:gosec // G115: RO SP fits the int32 wire field
	} {
		if err := change.Encode(connWriter{player.Conn}); err != nil {
			return fmt.Errorf("use item: encode vital update account %d var %d: %w", accountID, change.VarID, err)
		}
	}
	return nil
}

func inventoryRowAt(rows []invdomain.InventoryItem, index uint16) (invdomain.InventoryItem, bool) {
	for _, row := range rows {
		if row.Index == index {
			return row, true
		}
	}
	return invdomain.InventoryItem{}, false
}

func rollHeal(lo, hi int32) int32 {
	if hi <= lo {
		return lo
	}
	span := int64(hi) - int64(lo) + 1
	return lo + int32(rand.Intn(int(span))) //nolint:gosec // G404: itemheal randomness is gameplay, not security-sensitive
}

func (s *UseItemService) sendFailure(player *domain.Player, index uint16) error {
	if err := (packet.UseItemAck2Response{Index: index, Result: 0}).Encode(connWriter{player.Conn}); err != nil {
		return fmt.Errorf("use item: encode failure ack account %d index %d: %w", player.AccountID, index, err)
	}
	return nil
}

// UseItemHandler adapts UseItemService to the map gateway dispatcher.
type UseItemHandler struct {
	svc *UseItemService
}

// NewUseItemHandler binds the handler to its use-item service.
func NewUseItemHandler(svc *UseItemService) *UseItemHandler {
	return &UseItemHandler{svc: svc}
}

// Handle implements gateway/domain.PacketHandler for CZ_USE_ITEM2.
func (h *UseItemHandler) Handle(ctx context.Context, conn gwdomain.Conn, frame gwdomain.Frame) error {
	req, err := packet.ParseCZUseItem(frame.Raw)
	if err != nil {
		return fmt.Errorf("parse CZ_USE_ITEM2: %w", err)
	}
	accountID := conn.Auth().AccountID
	if accountID == 0 {
		return errors.New("use item: connection has no verified account (CZ_ENTER not completed)")
	}
	return h.svc.UseItem(ctx, accountID, req.Index)
}

var _ gwdomain.PacketHandler = (*UseItemHandler)(nil).Handle
