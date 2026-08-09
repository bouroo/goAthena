package app

import (
	"context"
	"errors"
	"fmt"
	"sync"

	invdomain "github.com/bouroo/goAthena/internal/modules/inventory/domain"
	"github.com/bouroo/goAthena/internal/modules/world/domain"
)

// TradeService runs the per-map player-to-player trade state machine: a
// request/ack handshake opens a session, each side stages items and zeny without
// touching their inventories, and pressing Ok on both sides concludes with an
// atomic item+zeny swap. A cancel (or any conclude failure) tears both sessions
// down and never duplicates or deletes items. It is safe for concurrent use: a
// single mutex serializes every state transition.
//
// Isolation note: the mutex serializes trade calls, but the inventory/economy
// ports are independent. A drop/use on another path can still mutate a bag
// between this service's verify and remove at conclude — a TOCTOU window the
// conclude verify narrows but cannot close. The proper fix is a transactional
// inventory op, which does not exist yet; until then a verified-but-raced remove
// fails the swap and rolls back.
type TradeService struct {
	mu       sync.Mutex
	sessions map[uint32]*tradeSession
	world    *WorldService
	inv      TradeInventoryPort
	econ     TradeEconPort
}

// TradeInventoryPort is the narrow inventory surface trade needs: load a char's
// bag (to resolve add-item slots and re-verify staged items at conclude), remove
// a staged row, and grant an item by nameID. Defining it locally keeps world/app
// off inventory/app; inventory.InventoryService satisfies it directly.
type TradeInventoryPort interface {
	LoadByChar(ctx context.Context, accountID, charID uint32) ([]invdomain.Item, error)
	Add(ctx context.Context, charID, nameID uint32, amount int) (invdomain.Item, error)
	Remove(ctx context.Context, id invdomain.ItemID, amount int) error
}

// TradeEconPort is the narrow economy surface trade needs: read a balance (to
// validate staged zeny) and move zeny at conclude. economy.EconomyService
// satisfies it directly.
type TradeEconPort interface {
	GetZeny(ctx context.Context, charID uint32) (int32, error)
	DeductZeny(ctx context.Context, charID uint32, amount int32) error
	CreditZeny(ctx context.Context, charID uint32, amount int32) error
}

// AddItemResult carries what the gateway emits after a successful stage: the
// self-ack index, the staged zeny (Index==0), or the resolved item (Index>0) for
// the partner's ZC_ADD_EXCHANGE_ITEM. Exactly one of Zeny/Item is meaningful per
// the wire convention (Index==0 is zeny).
type AddItemResult struct {
	Index uint16
	Zeny  int32
	Item  invdomain.Item
}

// Trade state-machine errors are distinct sentinels so the gateway maps each to a
// specific S→C result byte without parsing strings (no branch on error strings).
var (
	ErrTradeSelf             = errors.New("trade: requester is the target")
	ErrTradeAlreadyTrading   = errors.New("trade: a party is already trading")
	ErrTradeTargetOffline    = errors.New("trade: target is not an online player")
	ErrTradeDifferentMap     = errors.New("trade: parties are on different maps")
	ErrTradeNotActive        = errors.New("trade: no active trade session")
	ErrTradeLocked           = errors.New("trade: side already locked, cannot change offer")
	ErrTradeItemOutOfRange   = errors.New("trade: inventory index out of range")
	ErrTradeItemInsufficient = errors.New("trade: insufficient item or zeny")
	ErrTradeItemEquipped     = errors.New("trade: cannot trade an equipped item")
	ErrTradeConcludeFailed   = errors.New("trade: conclude failed, rolled back and cancelled")
)

// tradeState is the two-phase lifecycle of one side of a session.
type tradeState uint8

const (
	statePending tradeState = iota // request sent, awaiting partner ack
	stateActive                    // both accepted, items/zeny stageable until Ok
)

// offeredItem records one staged stack: the bag row id (for conclude remove),
// the nameID (for conclude grant to the partner), and the staged amount.
type offeredItem struct {
	itemID invdomain.ItemID
	nameID uint32
	amount uint32
}

// tradeSession is one participant's view of a trade: the partner charID, the
// accountID needed to load the bag, the staged items/zeny, and the lock state.
type tradeSession struct {
	partner   uint32
	accountID uint32
	state     tradeState
	offered   []offeredItem
	zeny      int32
	locked    bool
}

// NewTradeService builds a TradeService over the world registry and the
// inventory/economy ports.
func NewTradeService(world *WorldService, inv TradeInventoryPort, econ TradeEconPort) *TradeService {
	return &TradeService{
		sessions: make(map[uint32]*tradeSession),
		world:    world,
		inv:      inv,
		econ:     econ,
	}
}

// Request opens a pending trade between requesterID and targetID. Both must be
// online PCs on the same map and neither may already be trading. On success the
// gateway emits ZC_REQ_EXCHANGE_ITEM to the target (carrying the requester's
// name/AID/level, which it resolves from the world registry).
func (s *TradeService) Request(_ context.Context, requesterID, targetID uint32) error {
	if requesterID == targetID {
		return ErrTradeSelf
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[requesterID]; ok {
		return ErrTradeAlreadyTrading
	}
	if _, ok := s.sessions[targetID]; ok {
		return ErrTradeAlreadyTrading
	}
	req, err := s.pcEntity(requesterID)
	if err != nil {
		return err
	}
	tgt, err := s.pcEntity(targetID)
	if err != nil {
		return ErrTradeTargetOffline
	}
	if req.Map != tgt.Map {
		return ErrTradeDifferentMap
	}
	s.sessions[requesterID] = &tradeSession{partner: targetID, accountID: req.Account, state: statePending}
	s.sessions[targetID] = &tradeSession{partner: requesterID, accountID: tgt.Account, state: statePending}
	return nil
}

// Ack resolves the target's response to a pending request. On accept both sides
// move to active; on reject both sessions are torn down. The gateway already
// knows accept/reject (the CZ_TRADE_ACK type it parsed) and emits the matching
// ZC_ACK_EXCHANGE_ITEM / ZC_CANCEL_EXCHANGE_ITEM.
func (s *TradeService) Ack(_ context.Context, charID uint32, accept bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[charID]
	if !ok || sess.state != statePending {
		return ErrTradeNotActive
	}
	partner, pok := s.sessions[sess.partner]
	if !pok {
		delete(s.sessions, charID)
		return ErrTradeNotActive
	}
	if !accept {
		delete(s.sessions, charID)
		delete(s.sessions, sess.partner)
		return nil
	}
	sess.state = stateActive
	partner.state = stateActive
	return nil
}

// AddItem stages an item (invIndex>0) or zeny (invIndex==0) on charID's side of
// an active, unlocked trade. The inventory is not mutated; staging only records
// the intent. It validates the slot is in range, the item is not equipped, the
// staged amount (across prior stages of the same row) does not exceed the stack,
// and for zeny that the balance covers it. On success the gateway emits
// ZC_ACK_ADD_EXCHANGE_ITEM to the adder and ZC_ADD_EXCHANGE_ITEM to the partner.
func (s *TradeService) AddItem(ctx context.Context, charID uint32, invIndex, amount int) (AddItemResult, error) {
	if invIndex < 0 || amount <= 0 {
		return AddItemResult{}, ErrTradeItemInsufficient
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[charID]
	if !ok || sess.state != stateActive {
		return AddItemResult{}, ErrTradeNotActive
	}
	if sess.locked {
		return AddItemResult{}, ErrTradeLocked
	}
	if invIndex == 0 {
		return s.stageZeny(ctx, charID, amount)
	}
	return s.stageItem(ctx, charID, invIndex, amount, sess)
}

// OK locks charID's side. When both sides are locked it runs the atomic conclude
// swap. Returns concluded=false while waiting for the partner (gateway emits
// ZC_CONCLUDE_EXCHANGE_ITEM with Who=0 to the locker, Who=1 to the partner), and
// concluded=true once the swap succeeds (sessions are torn down either way).
func (s *TradeService) OK(ctx context.Context, charID uint32) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[charID]
	if !ok || sess.state != stateActive {
		return false, ErrTradeNotActive
	}
	partner, pok := s.sessions[sess.partner]
	if !pok {
		delete(s.sessions, charID)
		return false, ErrTradeNotActive
	}
	if sess.locked {
		// Idempotent re-lock: only the both-locked transition concludes.
		return partner.locked, nil
	}
	sess.locked = true
	if !partner.locked {
		return false, nil
	}
	if err := s.conclude(ctx, charID, sess, sess.partner, partner); err != nil {
		return false, err
	}
	return true, nil
}

// Cancel tears down charID's session and its partner's. The gateway emits
// ZC_CANCEL_EXCHANGE_ITEM to both. A no-op when charID is not trading.
func (s *TradeService) Cancel(_ context.Context, charID uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[charID]
	if !ok {
		return
	}
	delete(s.sessions, charID)
	delete(s.sessions, sess.partner)
}

// Partner returns the charID the given char is currently trading with (pending or
// active), and whether such a session exists. The gateway resolves the partner's
// connection through this so trade packets reach both sides; resolve it BEFORE
// calling Ack/OK/Cancel, which tear the session down.
func (s *TradeService) Partner(_ context.Context, charID uint32) (uint32, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[charID]
	if !ok {
		return 0, false
	}
	return sess.partner, true
}

// pcEntity resolves an online player entity, returning ErrTradeTargetOffline for
// a missing or non-PC id.
func (s *TradeService) pcEntity(id uint32) (domain.Entity, error) {
	e, err := s.world.Get(domain.EntityID(id))
	if err != nil {
		return domain.Entity{}, ErrTradeTargetOffline
	}
	if e.Type != domain.EntityTypePC {
		return domain.Entity{}, ErrTradeTargetOffline
	}
	return e, nil
}

// stageZeny records a zeny offer after checking the balance covers it. Re-staging
// replaces the prior zeny offer (matches the client, which resends the full
// amount).
func (s *TradeService) stageZeny(ctx context.Context, charID uint32, amount int) (AddItemResult, error) {
	sess := s.sessions[charID]
	want := int32(amount) //nolint:gosec // G115: wire Amount is a signed int32; balance check bounds it.
	bal, err := s.econ.GetZeny(ctx, charID)
	if err != nil {
		return AddItemResult{}, fmt.Errorf("trade: zeny balance: %w", err)
	}
	if want > bal {
		return AddItemResult{}, ErrTradeItemInsufficient
	}
	sess.zeny = want
	return AddItemResult{Index: 0, Zeny: want}, nil
}

// stageItem records an item offer after validating the slot, equipped state, and
// stack availability across prior stages of the same row.
func (s *TradeService) stageItem(ctx context.Context, charID uint32, invIndex, amount int, sess *tradeSession) (AddItemResult, error) {
	items, err := s.inv.LoadByChar(ctx, sess.accountID, charID)
	if err != nil {
		return AddItemResult{}, fmt.Errorf("trade: load inventory: %w", err)
	}
	if invIndex > len(items) {
		return AddItemResult{}, ErrTradeItemOutOfRange
	}
	item := items[invIndex-1]
	if item.IsEquipped() {
		return AddItemResult{}, ErrTradeItemEquipped
	}
	staged := uint32(0)
	for _, o := range sess.offered {
		if o.itemID == item.ID {
			staged += o.amount
		}
	}
	want := uint32(amount) //nolint:gosec // G115: amount>0, bounded by the stack check below.
	if staged+want > item.Amount {
		return AddItemResult{}, ErrTradeItemInsufficient
	}
	sess.offered = append(sess.offered, offeredItem{
		itemID: item.ID,
		nameID: item.NameID,
		amount: want,
	})
	return AddItemResult{Index: uint16(invIndex), Item: item}, nil //nolint:gosec // G115: invIndex bounded by len(items).
}

// conclude verifies both sides' offers are still satisfiable, then atomically
// swaps items and zeny with full rollback on any failure. Sessions are torn down
// on both success and failure.
func (s *TradeService) conclude(ctx context.Context, aID uint32, a *tradeSession, bID uint32, b *tradeSession) error {
	defer s.cancelBoth(aID, bID)
	if err := s.verifySide(ctx, aID, a); err != nil {
		return err
	}
	if err := s.verifySide(ctx, bID, b); err != nil {
		return err
	}
	return s.swap(ctx, aID, a, bID, b)
}

// verifySide re-checks that a side's staged items still exist with enough amount
// and its staged zeny is still covered, defending against a concurrent drop/use
// between staging and conclude. A failed verify cancels the trade with no
// inventory mutation.
func (s *TradeService) verifySide(ctx context.Context, charID uint32, sess *tradeSession) error {
	if sess.zeny != 0 {
		bal, err := s.econ.GetZeny(ctx, charID)
		if err != nil {
			return fmt.Errorf("%w: zeny check: %w", ErrTradeConcludeFailed, err)
		}
		if bal < sess.zeny {
			return fmt.Errorf("%w: zeny %d < staged %d", ErrTradeConcludeFailed, bal, sess.zeny)
		}
	}
	if len(sess.offered) == 0 {
		return nil
	}
	items, err := s.inv.LoadByChar(ctx, sess.accountID, charID)
	if err != nil {
		return fmt.Errorf("%w: load inventory: %w", ErrTradeConcludeFailed, err)
	}
	avail := make(map[invdomain.ItemID]uint32, len(items))
	for _, it := range items {
		avail[it.ID] = it.Amount
	}
	for _, o := range sess.offered {
		if avail[o.itemID] < o.amount {
			return fmt.Errorf("%w: item %d short", ErrTradeConcludeFailed, o.itemID)
		}
	}
	return nil
}

// swap performs the item+zeny exchange, recording a compensating undo for every
// successful mutation. The first failure runs every recorded undo in reverse
// (best-effort) so neither side loses or duplicates items.
func (s *TradeService) swap(ctx context.Context, aID uint32, a *tradeSession, bID uint32, b *tradeSession) error {
	var undos undoStack
	rollback := func(reason error) error {
		undos.apply(ctx)
		return reason
	}
	if err := s.removeOffered(ctx, aID, a.offered, &undos); err != nil {
		return rollback(fmt.Errorf("%w: remove A: %w", ErrTradeConcludeFailed, err))
	}
	if err := s.removeOffered(ctx, bID, b.offered, &undos); err != nil {
		return rollback(fmt.Errorf("%w: remove B: %w", ErrTradeConcludeFailed, err))
	}
	if err := s.grantOffered(ctx, bID, a.offered, &undos); err != nil {
		return rollback(fmt.Errorf("%w: grant to B: %w", ErrTradeConcludeFailed, err))
	}
	if err := s.grantOffered(ctx, aID, b.offered, &undos); err != nil {
		return rollback(fmt.Errorf("%w: grant to A: %w", ErrTradeConcludeFailed, err))
	}
	if err := s.moveZeny(ctx, aID, a.zeny, bID, &undos); err != nil {
		return rollback(fmt.Errorf("%w: zeny A: %w", ErrTradeConcludeFailed, err))
	}
	if err := s.moveZeny(ctx, bID, b.zeny, aID, &undos); err != nil {
		return rollback(fmt.Errorf("%w: zeny B: %w", ErrTradeConcludeFailed, err))
	}
	return nil
}

// removeOffered removes every staged row from charID, pushing a re-add undo per
// row so a later failure restores the owner.
func (s *TradeService) removeOffered(ctx context.Context, charID uint32, offered []offeredItem, undos *undoStack) error {
	for _, o := range offered {
		if err := s.inv.Remove(ctx, o.itemID, int(o.amount)); err != nil { //nolint:gosec // G115: amount bounded by stack.
			return fmt.Errorf("remove item %d: %w", o.itemID, err)
		}
		o := o
		undos.push(func(ctx context.Context) error {
			_, err := s.inv.Add(ctx, charID, o.nameID, int(o.amount)) //nolint:gosec // G115: restoring the same amount.
			return fmt.Errorf("undo remove re-add: %w", err)
		})
	}
	return nil
}

// grantOffered adds every staged row to charID, pushing a remove undo per granted
// row so a later failure undoes the grant.
func (s *TradeService) grantOffered(ctx context.Context, charID uint32, offered []offeredItem, undos *undoStack) error {
	for _, o := range offered {
		added, err := s.inv.Add(ctx, charID, o.nameID, int(o.amount)) //nolint:gosec // G115: amount bounded by stack.
		if err != nil {
			return fmt.Errorf("grant item %d: %w", o.nameID, err)
		}
		id, amt := added.ID, o.amount
		undos.push(func(ctx context.Context) error {
			return fmt.Errorf("undo grant remove: %w", s.inv.Remove(ctx, id, int(amt))) //nolint:gosec // G115: undoing the exact amount just granted.
		})
	}
	return nil
}

// moveZeny deducts amount from fromID and credits it to toID, pushing undos for
// each step. A zero amount is a no-op.
func (s *TradeService) moveZeny(ctx context.Context, fromID uint32, amount int32, toID uint32, undos *undoStack) error {
	if amount == 0 {
		return nil
	}
	if err := s.econ.DeductZeny(ctx, fromID, amount); err != nil {
		return fmt.Errorf("deduct zeny %d: %w", fromID, err)
	}
	undos.push(func(ctx context.Context) error {
		return fmt.Errorf("undo zeny credit %d: %w", fromID, s.econ.CreditZeny(ctx, fromID, amount))
	})
	if err := s.econ.CreditZeny(ctx, toID, amount); err != nil {
		return fmt.Errorf("credit zeny %d: %w", toID, err)
	}
	undos.push(func(ctx context.Context) error {
		return fmt.Errorf("undo zeny deduct %d: %w", toID, s.econ.DeductZeny(ctx, toID, amount))
	})
	return nil
}

// cancelBoth tears down both sides of a trade. Called under s.mu.
func (s *TradeService) cancelBoth(aID, bID uint32) {
	delete(s.sessions, aID)
	delete(s.sessions, bID)
}

// undoStack is a LIFO of compensating mutations applied in reverse on swap
// failure. apply runs every undo; undo errors are ignored (a failed undo means
// state may be inconsistent, but partial rollback is still strictly better than
// losing the items outright).
type undoStack []func(context.Context) error

func (u *undoStack) push(f func(context.Context) error) { *u = append(*u, f) }

func (u undoStack) apply(ctx context.Context) {
	for i := len(u) - 1; i >= 0; i-- {
		_ = u[i](ctx)
	}
}
