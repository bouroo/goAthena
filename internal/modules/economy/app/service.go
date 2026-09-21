// Package app implements the economy bounded context use cases: get, credit,
// and deduct zeny for a character, persisting via the character repo and
// appending one zeny-ledger row per movement. The ledger is the audit trail —
// without it the balance is a number with no history; with it, every movement
// carries a reason, a peer (when one exists), and a timestamp.
//
// Write order: validate → ledger append → balance update. A failed ledger
// aborts the call before the balance moves, so the audit row count always
// matches the call count. A failed balance update leaves an audit row
// recording the attempted movement — a reconciliation job (SumByChar vs.
// balance) detects the drift.
package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	chardomain "github.com/bouroo/goAthena/internal/modules/character/domain"
	"github.com/bouroo/goAthena/internal/modules/economy/domain"
)

// ErrLedgerAppendFailed is returned when the ledger append rejects a movement;
// the caller's balance is left unchanged. The ledger row is the precondition,
// not the side effect.
var ErrLedgerAppendFailed = errors.New("ledger append failed")

// LedgerEntry carries the optional reason/peer/map the caller has on hand when
// the movement happens. Zero values are accepted; the service substitutes
// ReasonUnknown and the character's current map (when it can resolve it).
type LedgerEntry struct {
	Reason     domain.Reason
	PeerCharID uint32
	MapName    string
}

// EconomyService moves zeny for a character. It reads the current balance from
// the character repo, applies overflow-safe arithmetic, appends a ledger row,
// then writes the new balance back.
type EconomyService struct {
	repos  chardomain.CharacterRepository
	ledger domain.LedgerRepository
	now    func() time.Time
}

// NewEconomyService builds an EconomyService backed by the character repo and
// the zeny ledger. The ledger may be nil only for callers that explicitly
// accept the missing audit trail (tests, the migration bootstrap before the
// ledger table exists).
func NewEconomyService(repos chardomain.CharacterRepository, ledger domain.LedgerRepository) *EconomyService {
	return &EconomyService{repos: repos, ledger: ledger, now: time.Now}
}

// Balance returns the character's current zeny.
func (s *EconomyService) Balance(ctx context.Context, accountID, charID uint32) (domain.Zeny, error) {
	chars, err := s.repos.ListByAccount(ctx, accountID)
	if err != nil {
		return domain.Zeny{}, fmt.Errorf("list chars: %w", err)
	}
	for _, c := range chars {
		if uint32(c.ID) == charID {
			z, err := domain.Zeny{}.Credit(int32(c.Zeny)) //nolint:gosec // G115: zeny bounded.
			if err != nil {
				return domain.Zeny{}, fmt.Errorf("zeny credit: %w", err)
			}
			return z, nil
		}
	}
	return domain.Zeny{}, fmt.Errorf("char %d: %w", charID, chardomain.ErrCharacterNotFound)
}

// DeductZeny subtracts amount from the character's balance and persists it.
// The movement is recorded in the ledger with ReasonUnknown; callers that know
// the reason should call DeductZenyFor instead.
func (s *EconomyService) DeductZeny(ctx context.Context, charID uint32, amount int32) error {
	return s.DeductZenyFor(ctx, charID, amount, LedgerEntry{Reason: domain.ReasonUnknown})
}

// DeductZenyFor subtracts amount and records the movement with the supplied
// reason/peer/map. The amount is rejected if it would overdraw the balance; in
// that case neither the balance nor the ledger moves.
func (s *EconomyService) DeductZenyFor(ctx context.Context, charID uint32, amount int32, entry LedgerEntry) error {
	if amount <= 0 {
		return fmt.Errorf("deduct %d: %w", amount, domain.ErrInsufficientFunds)
	}
	c, err := s.findChar(ctx, charID)
	if err != nil {
		return err
	}
	z, err := domain.NewZeny(int32(c.Zeny)) //nolint:gosec // G115
	if err != nil {
		return fmt.Errorf("zeny load: %w", err)
	}
	next, err := z.Deduct(amount)
	if err != nil {
		return fmt.Errorf("deduct: %w", err)
	}
	if err := s.appendLedger(ctx, c, -amount, entry); err != nil {
		return err
	}
	if err := s.repos.UpdateZeny(ctx, c.ID, uint32(next.Amount())); err != nil { //nolint:gosec // G115
		return fmt.Errorf("update zeny: %w", err)
	}
	return nil
}

// CreditZeny adds amount to the character's balance and persists it. Recorded
// in the ledger with ReasonUnknown; callers that know the reason should call
// CreditZenyFor instead.
func (s *EconomyService) CreditZeny(ctx context.Context, charID uint32, amount int32) error {
	return s.CreditZenyFor(ctx, charID, amount, LedgerEntry{Reason: domain.ReasonUnknown})
}

// CreditZenyFor adds amount and records the movement with the supplied
// reason/peer/map. Overflow is rejected before the ledger or balance moves.
func (s *EconomyService) CreditZenyFor(ctx context.Context, charID uint32, amount int32, entry LedgerEntry) error {
	if amount <= 0 {
		return fmt.Errorf("credit %d: must be positive", amount)
	}
	c, err := s.findChar(ctx, charID)
	if err != nil {
		return err
	}
	z, err := domain.NewZeny(int32(c.Zeny)) //nolint:gosec // G115
	if err != nil {
		return fmt.Errorf("zeny load: %w", err)
	}
	next, err := z.Credit(amount)
	if err != nil {
		return fmt.Errorf("credit: %w", err)
	}
	if err := s.appendLedger(ctx, c, amount, entry); err != nil {
		return err
	}
	if err := s.repos.UpdateZeny(ctx, c.ID, uint32(next.Amount())); err != nil { //nolint:gosec // G115
		return fmt.Errorf("update zeny: %w", err)
	}
	return nil
}

// DeductZenyWithPeer deducts amount and records ReasonTrade with peerCharID set
// to peer. It is the trade-specific entry point that keeps the world/app/trade
// module from importing the economy domain types (the LedgerEntry stays
// internal). peer == 0 means "no partner recorded" and the audit row carries
// ReasonUnknown instead.
func (s *EconomyService) DeductZenyWithPeer(ctx context.Context, charID uint32, amount int32, peer uint32) error {
	return s.DeductZenyFor(ctx, charID, amount, s.tradeEntry(peer))
}

// CreditZenyWithPeer mirrors DeductZenyWithPeer on the credit leg.
func (s *EconomyService) CreditZenyWithPeer(ctx context.Context, charID uint32, amount int32, peer uint32) error {
	return s.CreditZenyFor(ctx, charID, amount, s.tradeEntry(peer))
}

// tradeEntry builds the LedgerEntry used by both trade legs. A zero peer
// degrades to ReasonUnknown — the legacy posture — so a partially-wired caller
// still produces an audit row (just without the "who paid whom" linkage).
func (s *EconomyService) tradeEntry(peer uint32) LedgerEntry {
	if peer == 0 {
		return LedgerEntry{Reason: domain.ReasonUnknown}
	}
	return LedgerEntry{Reason: domain.ReasonTrade, PeerCharID: peer}
}

// GetZeny returns the character's current zeny balance by charID. It is the
// read-only counterpart of DeductZeny/CreditZeny the trade service uses to
// validate staged zeny without moving the balance.
func (s *EconomyService) GetZeny(ctx context.Context, charID uint32) (int32, error) {
	c, err := s.findChar(ctx, charID)
	if err != nil {
		return 0, err
	}
	return int32(c.Zeny), nil //nolint:gosec // G115: zeny is bounded by the domain Zeny type on every write path.
}

// RecentTransactions returns the most recent ledger entries for charID. It is
// the read-only counterpart of the write path the ops team uses to reconstruct
// a player's balance history.
func (s *EconomyService) RecentTransactions(ctx context.Context, charID uint32, limit int) ([]domain.ZenyTransaction, error) {
	if s.ledger == nil {
		return nil, errors.New("ledger not configured")
	}
	txs, err := s.ledger.ListByChar(ctx, charID, limit)
	if err != nil {
		return nil, fmt.Errorf("ledger list: %w", err)
	}
	return txs, nil
}

// appendLedger writes one audit row. It is the gate between balance validation
// and balance mutation — every call to this method must be paired with a
// subsequent UpdateZeny, and a failure here must short-circuit the movement.
func (s *EconomyService) appendLedger(ctx context.Context, c chardomain.Character, amount int32, entry LedgerEntry) error {
	if s.ledger == nil {
		// No ledger wired (boot-time test path): the audit trail is missing
		// for this movement. Surface as a typed error so callers can choose
		// whether to proceed or refuse.
		return ErrLedgerAppendFailed
	}
	reason := entry.Reason
	if reason == "" {
		reason = domain.ReasonUnknown
	}
	tx := domain.ZenyTransaction{
		AccountID:  c.AccountID,
		CharID:     uint32(c.ID),
		Amount:     amount,
		Reason:     reason,
		PeerCharID: entry.PeerCharID,
		MapName:    entry.MapName,
		CreatedAt:  s.now(),
	}
	if _, err := s.ledger.Append(ctx, tx); err != nil {
		return fmt.Errorf("ledger append: %w", err)
	}
	return nil
}

// findChar loads a character by charID (used when accountID is not available;
// commerce callers have charID from the conn auth).
func (s *EconomyService) findChar(ctx context.Context, charID uint32) (chardomain.Character, error) {
	c, err := s.repos.FindByID(ctx, chardomain.CharID(charID))
	if err != nil {
		return chardomain.Character{}, fmt.Errorf("find char %d: %w", charID, err)
	}
	return c, nil
}
