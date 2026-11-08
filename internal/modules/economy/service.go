// Package economy is the economy bounded-context module root.
//
// Service is the surface the rest of the monolith consumes. It exists so the
// module can be extracted over the NATS bus (M13): consumers resolve Service
// from DI, and composition decides whether that is the in-process
// *app.EconomyService (the monolith default) or a request/reply proxy talking
// to a remote economy host (`goathena serve-economy`). Both satisfy the same
// interface, so no consumer knows which one is wired.
package economy

import (
	"context"

	"github.com/bouroo/goAthena/internal/modules/economy/app"
)

// Service is the cross-module economy surface: read a balance and move zeny,
// with the ledger-entry variants that tag the audit row (reason, peer, map)
// and the trade variants that keep world/app off the economy domain types.
//
// The narrow method set is deliberate: everything a caller can express is one
// of these seven verbs, which is also the entire wire surface the remote
// proxy must carry.
type Service interface {
	// GetZeny returns the character's current zeny balance.
	GetZeny(ctx context.Context, charID uint32) (int32, error)
	// DeductZeny subtracts amount, recording ReasonUnknown in the ledger.
	DeductZeny(ctx context.Context, charID uint32, amount int32) error
	// CreditZeny adds amount, recording ReasonUnknown in the ledger.
	CreditZeny(ctx context.Context, charID uint32, amount int32) error
	// DeductZenyFor subtracts amount, recording the caller's ledger entry.
	DeductZenyFor(ctx context.Context, charID uint32, amount int32, entry app.LedgerEntry) error
	// CreditZenyFor adds amount, recording the caller's ledger entry.
	CreditZenyFor(ctx context.Context, charID uint32, amount int32, entry app.LedgerEntry) error
	// DeductZenyWithPeer subtracts amount, recording ReasonTrade + the peer.
	DeductZenyWithPeer(ctx context.Context, charID uint32, amount int32, peer uint32) error
	// CreditZenyWithPeer adds amount, recording ReasonTrade + the peer.
	CreditZenyWithPeer(ctx context.Context, charID uint32, amount int32, peer uint32) error
}

// The in-process service is the reference implementation of the interface.
var _ Service = (*app.EconomyService)(nil)
