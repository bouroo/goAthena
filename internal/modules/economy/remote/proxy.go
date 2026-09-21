package remote

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bouroo/goAthena/internal/modules/economy/app"
)

// Proxy is the client side of the extraction: it satisfies economy.Service
// by turning every call into one NATS request against the economy host. The
// proxy holds no economy state — balances live on the host and in its DB.
type Proxy struct {
	nc      *nats.Conn
	timeout time.Duration
}

// NewProxy builds the request/reply client. timeout bounds a call whose
// context carries no deadline (DefaultTimeout when zero).
func NewProxy(nc *nats.Conn, timeout time.Duration) *Proxy {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Proxy{nc: nc, timeout: timeout}
}

// GetZeny reads the balance from the host.
func (p *Proxy) GetZeny(ctx context.Context, charID uint32) (int32, error) {
	data, err := json.Marshal(zenyGetReq{CharID: charID})
	if err != nil {
		return 0, fmt.Errorf("encode zeny get: %w", err)
	}
	r, err := p.call(ctx, SubjectZenyGet, data)
	if err != nil {
		return 0, err
	}
	return r.Amount, nil
}

// DeductZeny subtracts amount with the ledger default (ReasonUnknown).
func (p *Proxy) DeductZeny(ctx context.Context, charID uint32, amount int32) error {
	return p.move(ctx, SubjectZenyDeduct, charID, amount, app.LedgerEntry{})
}

// CreditZeny adds amount with the ledger default (ReasonUnknown).
func (p *Proxy) CreditZeny(ctx context.Context, charID uint32, amount int32) error {
	return p.move(ctx, SubjectZenyCredit, charID, amount, app.LedgerEntry{})
}

// DeductZenyFor subtracts amount, carrying the caller's ledger entry.
func (p *Proxy) DeductZenyFor(ctx context.Context, charID uint32, amount int32, entry app.LedgerEntry) error {
	return p.move(ctx, SubjectZenyDeduct, charID, amount, entry)
}

// CreditZenyFor adds amount, carrying the caller's ledger entry.
func (p *Proxy) CreditZenyFor(ctx context.Context, charID uint32, amount int32, entry app.LedgerEntry) error {
	return p.move(ctx, SubjectZenyCredit, charID, amount, entry)
}

// DeductZenyWithPeer subtracts amount as a trade leg against peer, writing
// the identical audit row the in-process service writes (app.TradeEntry).
func (p *Proxy) DeductZenyWithPeer(ctx context.Context, charID uint32, amount int32, peer uint32) error {
	return p.move(ctx, SubjectZenyDeduct, charID, amount, app.TradeEntry(peer))
}

// CreditZenyWithPeer adds amount as a trade leg against peer.
func (p *Proxy) CreditZenyWithPeer(ctx context.Context, charID uint32, amount int32, peer uint32) error {
	return p.move(ctx, SubjectZenyCredit, charID, amount, app.TradeEntry(peer))
}

// move sends one credit or deduct.
func (p *Proxy) move(ctx context.Context, subject string, charID uint32, amount int32, entry app.LedgerEntry) error {
	data, err := json.Marshal(zenyMoveReq{CharID: charID, Amount: amount, Entry: entryToWire(entry)})
	if err != nil {
		return fmt.Errorf("encode %s: %w", subject, err)
	}
	_, err = p.call(ctx, subject, data)
	return err
}

// call publishes one request and decodes the envelope. The context wins when
// it carries a deadline; otherwise the proxy timeout applies.
func (p *Proxy) call(ctx context.Context, subject string, data []byte) (reply, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.timeout)
		defer cancel()
	}
	msg, err := p.nc.RequestWithContext(ctx, subject, data)
	if err != nil {
		return reply{}, fmt.Errorf("economy call %s: %w", subject, err)
	}
	var r reply
	if err := json.Unmarshal(msg.Data, &r); err != nil {
		return reply{}, fmt.Errorf("economy reply %s: decode: %w", subject, err)
	}
	if !r.OK {
		return reply{}, errFor(r.ErrCode, r.ErrMsg)
	}
	return r, nil
}
