package domain

import "sync"

// StagedItem is a REFERENCE to one item a trader has staged in the trade window
// (S2). It holds only the bag index + amount the client asked to add — never a
// copy of the item itself — so staging mutates no inventory and carries zero
// duplication surface. The atomic, all-or-nothing MOVE of these references into
// the partner's bag is the deferred commit slice (S3).
type StagedItem struct {
	// Index is the trader's bag slot (goAthena's 0-based InventoryItem.Index).
	Index uint16
	// Amount is the stack count the trader staged from that slot.
	Amount int32
}

// TradeDeal is the shared staging state for one open trade window (S2). Both
// partners hold a pointer to the SAME deal (see Player.deal), so AddItem/Ok are
// consistent across both sides. The deal owns its mutex (it spans two players,
// so it cannot reuse either player's mu); accessors hold ONLY the deal's lock,
// never a player's lock at the same time, so there is no lock-ordering deadlock.
//
// S2 INVARIANT: this struct only stages REFERENCES (Index+Amount) — it never
// removes items from either bag and never moves zeny. The Ok flags lock each
// side; once both are locked the trade waits for CZ_TRADE_COMMIT (S3) which is
// NOT implemented here. Cancel clears the deal. A reset re-arms both Ok flags
// when either side adds/changes an item after an Ok (rAthena trade.cpp unlocks
// the partner on a late add).
type TradeDeal struct {
	mu sync.Mutex
	// items[0] is the staging list of the player who holds this deal via
	// SetTrading(true, deal); items[1] is the partner's. The index is resolved by
	// the caller via TradeDeal.slotFor(accountID) against the two bound AIDs.
	items [2][]StagedItem
	zeny  [2]int32
	ok    [2]bool
	aid   [2]uint32 // the two partners' account ids, in the same [0]/[1] order
}

// NewTradeDeal binds a deal to its two partners (aid0 holds index 0). Both
// partners must call SetTrading(true, d) with the returned deal.
func NewTradeDeal(aid0, aid1 uint32) *TradeDeal {
	return &TradeDeal{aid: [2]uint32{aid0, aid1}}
}

// slotFor returns 0 or 1 for accountID (which of the two partners it is). A
// caller passing an unbound account id gets -1 — a bug the handler treats as a
// silent no-op.
func (d *TradeDeal) slotFor(accountID uint32) int {
	for i, a := range d.aid {
		if a == accountID {
			return i
		}
	}
	return -1
}

// Add stages one item (or zeny when index==0) for accountID. It caps the per-
// side item list at tradeMaxItems (rAthena MAX_DEAL_ITEMS=10) and, on any staging
// AFTER the partner pressed Ok, clears that partner's Ok (rAthena re-locks the
// partner on a late add). Returns false if the cap is hit (caller NACKs).
func (d *TradeDeal) Add(accountID uint32, item StagedItem) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	slot := d.slotFor(accountID)
	if slot < 0 {
		return false
	}
	if item.Index == 0 {
		d.zeny[slot] = item.Amount
	} else {
		// rAthena's trade_tradeadditem does ARR_FIND on the deal list for the same
		// index and UPDATES it (the client re-staging a slot replaces, not
		// duplicates). Merge by index so a re-add does not spawn two ZC_ADD rows or
		// over-stage the source stack.
		for i := range d.items[slot] {
			if d.items[slot][i].Index == item.Index {
				d.items[slot][i].Amount = item.Amount
				d.ok[1-slot] = false
				return true
			}
		}
		if len(d.items[slot]) >= 10 { // tradeMaxItems, inlined to avoid a packet import cycle.
			return false
		}
		d.items[slot] = append(d.items[slot], item)
	}
	// A late add re-locks the partner (rAthena trade.cpp trade_tradeadditem clears
	// the partner's deal_locked when the adder wasn't locked).
	d.ok[1-slot] = false
	return true
}

// Ok locks accountID's side. Returns the partner's slot (-1 if unbound) so the
// caller can emit the right ZC_CONCLUDE frames. It is a no-op if already locked.
func (d *TradeDeal) Ok(accountID uint32) (slot, partnerSlot int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	slot = d.slotFor(accountID)
	if slot < 0 {
		return -1, -1
	}
	d.ok[slot] = true
	return slot, 1 - slot
}

// Clear empties both sides' staging (cancel/close path).
func (d *TradeDeal) Clear() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.items = [2][]StagedItem{}
	d.zeny = [2]int32{}
	d.ok = [2]bool{}
}

// Snapshot returns a copy of accountID's staged items (for the caller to resolve
// item details and build the ZC_ADD frame) and its staged zeny. The slice is a
// defensive copy so the caller can range it without holding the deal lock.
func (d *TradeDeal) Snapshot(accountID uint32) (items []StagedItem, zeny int32) {
	d.mu.Lock()
	defer d.mu.Unlock()
	slot := d.slotFor(accountID)
	if slot < 0 {
		return nil, 0
	}
	cp := make([]StagedItem, len(d.items[slot]))
	copy(cp, d.items[slot])
	return cp, d.zeny[slot]
}
